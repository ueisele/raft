package raft

import (
	"fmt"
	"sync"
	"time"
)

// ReplicationManager handles log replication
// Implements Section 5.3 of the Raft paper
type ReplicationManager struct {
	mu       sync.RWMutex
	serverID int
	peers    []int

	// Dependencies
	state            *StateManager
	logManager       *LogManager
	transport        Transport
	config           *Config
	stateMachine     StateMachine
	snapshotProvider SnapshotProvider

	// Leader state (reinitialized after election)
	nextIndex  map[int]int // For each server, index of next log entry to send
	matchIndex map[int]int // For each server, index of highest log entry known to be replicated

	// Tracking successful replication
	lastContact  map[int]time.Time
	inflightRPCs map[int]bool

	// Notification channel for apply loop
	applyNotify chan<- struct{}

	// Function to get voting members count (set by node)
	getVotingMembersCount func() int
}

// NewReplicationManager creates a new replication manager
func NewReplicationManager(serverID int, peers []int, state *StateManager, logManager *LogManager, transport Transport, config *Config, stateMachine StateMachine, snapshotProvider SnapshotProvider, applyNotify chan<- struct{}) *ReplicationManager {
	return &ReplicationManager{
		serverID:         serverID,
		peers:            peers,
		state:            state,
		logManager:       logManager,
		transport:        transport,
		config:           config,
		stateMachine:     stateMachine,
		snapshotProvider: snapshotProvider,
		nextIndex:        make(map[int]int),
		matchIndex:       make(map[int]int),
		lastContact:      make(map[int]time.Time),
		inflightRPCs:     make(map[int]bool),
		applyNotify:      applyNotify,
		// Default to all peers being voting members
		getVotingMembersCount: func() int { return len(peers) },
	}
}

// BecomeLeader initializes leader state for replication
func (rm *ReplicationManager) BecomeLeader() {
	rm.mu.Lock()
	lastIndex := rm.logManager.GetLastIndex()

	// Initialize nextIndex and matchIndex for all peers
	for _, peer := range rm.peers {
		if peer != rm.serverID {
			rm.nextIndex[peer] = lastIndex + 1
			rm.matchIndex[peer] = 0
			rm.lastContact[peer] = time.Now() // Assume all peers are initially reachable
		}
	}

	if rm.config.Logger != nil {
		rm.config.Logger.Info("Initialized leader state, nextIndex=%d for all peers", lastIndex+1)
	}
	rm.mu.Unlock()

	// Send initial empty AppendEntries to assert leadership
	rm.sendHeartbeats()
}

// Replicate sends log entries to all followers
func (rm *ReplicationManager) Replicate() {
	// Skip the leader check - we're only called from Submit which already verified we're the leader
	// This avoids potential deadlock with state manager
	// state, _ := rm.state.GetState()
	// if state != Leader {
	//     return
	// }

	// NOTE: We cannot lock here because Submit() holds n.mu while calling us,
	// and other goroutines might hold rm.mu while needing n.mu (deadlock).
	// Since Submit is already synchronized, we can safely access rm.peers without locking.
	peers := rm.peers

	// Send AppendEntries to all peers in parallel
	for _, peer := range peers {
		if peer != rm.serverID {
			if rm.config != nil && rm.config.Logger != nil {
				rm.config.Logger.Debug("Starting replication to peer %d", peer)
			}
			go rm.replicateToPeer(peer)
		}
	}

	// For single-node cluster, immediately advance commit index
	if len(peers) == 1 {
		rm.mu.Lock()
		rm.advanceCommitIndexWithLock()
		rm.mu.Unlock()
	}
}

// SendHeartbeats sends heartbeat messages to all peers
func (rm *ReplicationManager) SendHeartbeats() {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	if rm.config.Logger != nil {
		rm.config.Logger.Debug("Leader %d sending heartbeats", rm.serverID)
	}
	rm.sendHeartbeatsWithLock()

	// Check if we can still reach a majority
	rm.checkQuorum()
}

// HandleAppendEntries handles incoming AppendEntries RPC
func (rm *ReplicationManager) HandleAppendEntries(args *AppendEntriesArgs, reply *AppendEntriesReply) {
	state, currentTerm := rm.state.GetState()
	reply.Term = currentTerm
	reply.Success = false

	// Reply false if term < currentTerm (5.1)
	if args.Term < currentTerm {
		return
	}

	// If RPC request contains term T > currentTerm: set currentTerm = T, convert to follower (5.1)
	if args.Term > currentTerm || (args.Term == currentTerm && state == Candidate) {
		rm.state.BecomeFollower(args.Term)
	}

	// Reset election timer
	rm.state.ResetElectionTimer()

	// Update leader ID
	leaderID := args.LeaderID
	rm.state.SetLeaderID(&leaderID)

	// Check log consistency
	if args.PrevLogIndex > 0 {
		// Check if we have the previous entry
		prevEntry := rm.logManager.GetEntry(args.PrevLogIndex)
		if prevEntry == nil || prevEntry.Term != args.PrevLogTerm {
			if rm.config.Logger != nil {
				rm.config.Logger.Debug("Log inconsistency at index %d", args.PrevLogIndex)
			}
			return
		}
	}

	// Append entries to log
	if len(args.Entries) > 0 {
		err := rm.logManager.AppendEntries(args.PrevLogIndex, args.PrevLogTerm, args.Entries)
		if err != nil {
			if rm.config.Logger != nil {
				rm.config.Logger.Debug("Failed to append entries: %v", err)
			}
			return
		}
	}

	// Update commit index
	if args.LeaderCommit > rm.logManager.GetCommitIndex() {
		lastNewIndex := args.PrevLogIndex + len(args.Entries)
		newCommitIndex := min(args.LeaderCommit, lastNewIndex)
		rm.logManager.SetCommitIndex(newCommitIndex)

		// Notify node's apply loop about new commits
		// (Don't apply here - let the centralized apply loop handle it)
		select {
		case rm.applyNotify <- struct{}{}:
		default:
		}
	}

	reply.Success = true
}

// replicateToPeer sends AppendEntries to a specific peer
func (rm *ReplicationManager) replicateToPeer(peer int) {
	// Check state first without holding lock to avoid deadlock
	state, currentTerm := rm.state.GetState()
	if state != Leader {
		return
	}

	rm.mu.Lock()
	// Check if RPC is already in flight
	if rm.inflightRPCs[peer] {
		rm.mu.Unlock()
		return
	}

	// Mark RPC as in flight
	rm.inflightRPCs[peer] = true

	// Prepare AppendEntries arguments
	nextIndex := rm.nextIndex[peer]
	prevIndex := nextIndex - 1
	prevTerm := 0

	if prevIndex > 0 {
		prevEntry := rm.logManager.GetEntry(prevIndex)
		if prevEntry == nil {
			// Entry might be in snapshot, need to send InstallSnapshot
			if rm.config.Logger != nil {
				rm.config.Logger.Debug("prevEntry is nil for peer %d at index %d (nextIndex=%d)",
					peer, prevIndex, nextIndex)
			}
			// Clear the in-flight flag before unlocking
			delete(rm.inflightRPCs, peer)
			rm.mu.Unlock()
			rm.sendSnapshot(peer)
			return
		}
		prevTerm = prevEntry.Term
	}

	// Get entries to send
	lastIndex := rm.logManager.GetLastIndex()
	entries := rm.logManager.GetEntries(nextIndex, lastIndex+1)

	args := &AppendEntriesArgs{
		Term:         currentTerm,
		LeaderID:     rm.serverID,
		PrevLogIndex: prevIndex,
		PrevLogTerm:  prevTerm,
		Entries:      entries,
		LeaderCommit: rm.logManager.GetCommitIndex(),
	}

	// Don't hold lock while sending RPC
	rm.mu.Unlock()

	// Send RPC
	reply, err := rm.transport.SendAppendEntries(peer, args)

	// Process reply (this will acquire lock again and clear the in-flight flag)
	rm.handleAppendEntriesReply(peer, args, reply, err)
}

// handleAppendEntriesReply processes the reply from AppendEntries RPC
func (rm *ReplicationManager) handleAppendEntriesReply(peer int, args *AppendEntriesArgs, reply *AppendEntriesReply, err error) {
	// Check if we're still leader before acquiring lock to avoid deadlock
	state, currentTerm := rm.state.GetState()
	if state != Leader || args.Term != currentTerm {
		// Still need to clear in-flight flag
		rm.mu.Lock()
		delete(rm.inflightRPCs, peer)
		rm.mu.Unlock()
		return
	}

	rm.mu.Lock()
	defer rm.mu.Unlock()

	// Clear in-flight marker
	delete(rm.inflightRPCs, peer)

	if err != nil {
		if rm.config.Logger != nil {
			rm.config.Logger.Warn("AppendEntries to %d failed: %v", peer, err)
		}
		// On error, the follower won't receive heartbeats, so their election timer will eventually expire
		return
	}

	// Handle reply with higher term
	if reply.Term > currentTerm {
		rm.state.SetTerm(reply.Term)
		return
	}

	// Update last contact time
	rm.lastContact[peer] = time.Now()

	if reply.Success {
		// Update nextIndex and matchIndex
		rm.nextIndex[peer] = args.PrevLogIndex + len(args.Entries) + 1
		rm.matchIndex[peer] = args.PrevLogIndex + len(args.Entries)

		// Try to advance commit index (we already hold the lock)
		rm.advanceCommitIndexWithLock()

		// Record metrics
		if rm.config.Metrics != nil {
			rm.config.Metrics.RecordHeartbeat(peer, true, 0)
		}
	} else {
		// Decrement nextIndex and retry
		rm.nextIndex[peer] = max(1, rm.nextIndex[peer]-1)

		if rm.config.Logger != nil {
			rm.config.Logger.Debug("Decremented nextIndex for %d to %d", peer, rm.nextIndex[peer])
		}

		// Immediately retry with lower index
		go rm.replicateToPeer(peer)
	}
}

// advanceCommitIndex checks if we can advance the commit index
// NOTE: Caller must hold rm.mu lock
func (rm *ReplicationManager) advanceCommitIndexWithLock() {
	// Caller already holds the lock, don't acquire again

	// Find the highest index that has been replicated on a majority
	lastIndex := rm.logManager.GetLastIndex()
	currentTerm := rm.state.GetTerm()
	currentCommitIndex := rm.logManager.GetCommitIndex()

	if rm.config.Logger != nil {
		rm.config.Logger.Debug("Attempting to advance commit index: current=%d, lastIndex=%d, currentTerm=%d",
			currentCommitIndex, lastIndex, currentTerm)
	}

	// Special case for single-node cluster: immediately commit all entries from current term
	if len(rm.peers) == 1 {
		for n := currentCommitIndex + 1; n <= lastIndex; n++ {
			entry := rm.logManager.GetEntry(n)
			if entry != nil && entry.Term == currentTerm {
				rm.logManager.SetCommitIndex(n)
				if rm.config.Logger != nil {
					rm.config.Logger.Debug("Single-node cluster: advanced commit index to %d", n)
				}
			}
		}

		// Notify apply loop if we committed something
		if rm.logManager.GetCommitIndex() > currentCommitIndex {
			select {
			case rm.applyNotify <- struct{}{}:
			default:
			}
		}
		return
	}

	for n := currentCommitIndex + 1; n <= lastIndex; n++ {
		entry := rm.logManager.GetEntry(n)
		if entry == nil {
			if rm.config.Logger != nil {
				rm.config.Logger.Debug("Entry at index %d is nil", n)
			}
			continue
		}

		if entry.Term != currentTerm {
			// Only commit entries from current term (Section 5.4.2)
			if rm.config.Logger != nil {
				rm.config.Logger.Debug("Skipping entry at index %d: term %d != current term %d", n, entry.Term, currentTerm)
			}
			continue
		}

		// Count how many servers have this entry
		count := 1 // Leader always has it
		matchDetails := fmt.Sprintf("leader=%d", rm.serverID)

		// For single-node cluster, matchIndex will be empty
		// In that case, only the leader has the entry
		for peer, matchIdx := range rm.matchIndex {
			if peer != rm.serverID {
				matchDetails += fmt.Sprintf(", peer%d.match=%d", peer, matchIdx)
				if matchIdx >= n {
					count++
				}
			}
		}

		// Check if majority has it (only counting voting members)
		votingMembers := rm.getVotingMembersCount()
		if votingMembers == 0 {
			// Default to peer count if voting members function returns 0
			votingMembers = len(rm.peers)
		}
		majority := votingMembers/2 + 1
		if rm.config.Logger != nil {
			rm.config.Logger.Debug("Entry %d: replicated on %d/%d servers (need %d for majority, %d voting members) [%s]",
				n, count, len(rm.peers), majority, votingMembers, matchDetails)
		}

		if count >= majority {
			rm.logManager.SetCommitIndex(n)

			if rm.config.Logger != nil {
				rm.config.Logger.Debug("Advanced commit index to %d", n)
			}

			// Notify node's apply loop about new commits
			// (Don't apply here - let the centralized apply loop handle it)
			select {
			case rm.applyNotify <- struct{}{}:
			default:
			}
		} else {
			// Can't advance further if this entry doesn't have majority
			break
		}
	}
}

// applyCommittedEntries is removed - application is handled by node.go's applyLoop
// This prevents duplicate applications and race conditions

// sendHeartbeats sends empty AppendEntries to all peers
func (rm *ReplicationManager) sendHeartbeats() {
	// Get a copy of peers under read lock
	rm.mu.RLock()
	peers := make([]int, len(rm.peers))
	copy(peers, rm.peers)
	rm.mu.RUnlock()

	for _, peer := range peers {
		if peer != rm.serverID {
			go rm.replicateToPeer(peer)
		}
	}
}

// sendHeartbeatsWithLock sends empty AppendEntries to all peers
// NOTE: Caller must hold rm.mu lock
func (rm *ReplicationManager) sendHeartbeatsWithLock() {
	// Get a copy of peers - caller already holds lock
	peers := make([]int, len(rm.peers))
	copy(peers, rm.peers)

	for _, peer := range peers {
		if peer != rm.serverID {
			go rm.replicateToPeer(peer)
		}
	}
}

// sendSnapshot sends InstallSnapshot RPC to a peer
func (rm *ReplicationManager) sendSnapshot(peer int) {
	rm.mu.Lock()

	// Mark RPC as in flight
	rm.inflightRPCs[peer] = true
	rm.mu.Unlock()

	// Get the latest snapshot
	snapshot, err := rm.snapshotProvider.GetLatestSnapshot()
	if err != nil {
		rm.mu.Lock()
		delete(rm.inflightRPCs, peer)
		rm.mu.Unlock()

		if rm.config.Logger != nil {
			rm.config.Logger.Warn("Failed to get snapshot for peer %d: %v", peer, err)
		}
		// Fall back to sending from beginning of log
		rm.mu.Lock()
		rm.nextIndex[peer] = 1
		rm.mu.Unlock()
		return
	}

	// Send snapshot in chunks
	const chunkSize = 32 * 1024 // 32KB chunks
	offset := 0

	for offset < len(snapshot.Data) {
		// Calculate chunk size
		end := offset + chunkSize
		if end > len(snapshot.Data) {
			end = len(snapshot.Data)
		}

		chunk := snapshot.Data[offset:end]
		done := end == len(snapshot.Data)

		// Prepare InstallSnapshot RPC
		args := &InstallSnapshotArgs{
			Term:              rm.state.GetCurrentTerm(),
			LeaderID:          rm.serverID,
			LastIncludedIndex: snapshot.LastIncludedIndex,
			LastIncludedTerm:  snapshot.LastIncludedTerm,
			Offset:            offset,
			Data:              chunk,
			Done:              done,
		}

		// Send the chunk
		reply, err := rm.transport.SendInstallSnapshot(peer, args)
		if err != nil {
			rm.mu.Lock()
			delete(rm.inflightRPCs, peer)
			rm.mu.Unlock()

			if rm.config.Logger != nil {
				rm.config.Logger.Warn("Failed to send snapshot chunk to peer %d: %v", peer, err)
			}
			return
		}

		// Check reply term
		if reply.Term > rm.state.GetCurrentTerm() {
			rm.state.SetTerm(reply.Term)
			rm.mu.Lock()
			delete(rm.inflightRPCs, peer)
			rm.mu.Unlock()
			return
		}

		// Move to next chunk
		offset = end
	}

	// Snapshot sent successfully, update state
	rm.mu.Lock()
	delete(rm.inflightRPCs, peer)
	rm.nextIndex[peer] = snapshot.LastIncludedIndex + 1
	rm.matchIndex[peer] = snapshot.LastIncludedIndex
	rm.lastContact[peer] = time.Now()
	rm.mu.Unlock()

	if rm.config.Logger != nil {
		rm.config.Logger.Info("Successfully sent snapshot to peer %d (lastIncludedIndex=%d)",
			peer, snapshot.LastIncludedIndex)
	}

	// Record metrics
	if rm.config.Metrics != nil {
		rm.config.Metrics.RecordSnapshot(len(snapshot.Data), 0)
	}
}

// GetReplicationStatus returns the current replication status
func (rm *ReplicationManager) GetReplicationStatus() map[int]ReplicationStatus {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	status := make(map[int]ReplicationStatus)
	for _, peer := range rm.peers {
		if peer != rm.serverID {
			status[peer] = ReplicationStatus{
				NextIndex:   rm.nextIndex[peer],
				MatchIndex:  rm.matchIndex[peer],
				LastContact: rm.lastContact[peer],
				InFlight:    rm.inflightRPCs[peer],
			}
		}
	}

	return status
}

// ReplicationStatus represents the replication status for a peer
type ReplicationStatus struct {
	NextIndex   int
	MatchIndex  int
	LastContact time.Time
	InFlight    bool
}

// StopReplication resets the replication state when stepping down from leader
func (rm *ReplicationManager) StopReplication() {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	// Reset all peer indices
	for peer := range rm.nextIndex {
		rm.nextIndex[peer] = 1
		rm.matchIndex[peer] = 0
	}

	// Clear last contact times and inflight RPCs
	rm.lastContact = make(map[int]time.Time)
	rm.inflightRPCs = make(map[int]bool)
}

// UpdatePeers updates the peer list for replication (used for configuration changes)
func (rm *ReplicationManager) UpdatePeers(peers []int) {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	rm.peers = peers

	// Initialize state for any new peers
	for _, peer := range peers {
		if peer == rm.serverID {
			continue
		}
		if _, exists := rm.nextIndex[peer]; !exists {
			// For new peers, start from the beginning to ensure they get all entries
			// This is safer than assuming they have entries up to lastLogIndex
			rm.nextIndex[peer] = 1
			rm.matchIndex[peer] = 0
			rm.lastContact[peer] = time.Now()
			rm.inflightRPCs[peer] = false
		}
	}

	// Remove state for peers that are no longer in the configuration
	peerSet := make(map[int]bool)
	for _, peer := range peers {
		peerSet[peer] = true
	}

	for peer := range rm.nextIndex {
		if !peerSet[peer] || peer == rm.serverID {
			delete(rm.nextIndex, peer)
			delete(rm.matchIndex, peer)
			delete(rm.lastContact, peer)
			delete(rm.inflightRPCs, peer)
		}
	}
}

// SetVotingMembersCountFunc sets the function to get voting members count
func (rm *ReplicationManager) SetVotingMembersCountFunc(f func() int) {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	rm.getVotingMembersCount = f
}

// checkQuorum verifies the leader can reach a majority of nodes
func (rm *ReplicationManager) checkQuorum() {
	// Count how many nodes we can reach (including ourselves)
	reachableCount := 1 // Start with 1 for ourselves

	// Use a reasonable timeout for considering a node reachable
	// Should be less than election timeout to prevent split-brain
	reachableTimeout := 3 * rm.config.HeartbeatInterval
	now := time.Now()

	for peer, lastContact := range rm.lastContact {
		if peer != rm.serverID && now.Sub(lastContact) < reachableTimeout {
			reachableCount++
		}
	}

	// Get the total number of voting members
	totalVotingMembers := rm.getVotingMembersCount()
	majorityNeeded := (totalVotingMembers / 2) + 1

	// If we can't reach a majority, step down
	if reachableCount < majorityNeeded {
		if rm.config.Logger != nil {
			rm.config.Logger.Warn("Leader %d cannot reach majority (%d/%d), stepping down",
				rm.serverID, reachableCount, totalVotingMembers)
		}
		// Step down by incrementing term and becoming follower
		currentTerm := rm.state.GetCurrentTerm()
		rm.state.SetTerm(currentTerm + 1)
		rm.state.BecomeFollower(currentTerm + 1)
	}
}

// GetMatchIndex returns the match index for a specific server
func (rm *ReplicationManager) GetMatchIndex(serverID int) int {
	rm.mu.RLock()
	defer rm.mu.RUnlock()

	if matchIndex, exists := rm.matchIndex[serverID]; exists {
		return matchIndex
	}
	return 0
}

// Helper functions
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
