package raft

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"sync"
	"time"
)

// Server states
type State int

const (
	Follower State = iota
	Candidate
	Leader
)

func (s State) String() string {
	switch s {
	case Follower:
		return "Follower"
	case Candidate:
		return "Candidate"
	case Leader:
		return "Leader"
	default:
		return "Unknown"
	}
}

// LogEntry represents a single entry in the replicated log
type LogEntry struct {
	Term    int         `json:"term"`
	Index   int         `json:"index"`
	Command interface{} `json:"command"`
}

// RequestVoteArgs represents arguments for RequestVote RPC
type RequestVoteArgs struct {
	Term         int `json:"term"`
	CandidateID  int `json:"candidateId"`
	LastLogIndex int `json:"lastLogIndex"`
	LastLogTerm  int `json:"lastLogTerm"`
}

// RequestVoteReply represents reply for RequestVote RPC
type RequestVoteReply struct {
	Term        int  `json:"term"`
	VoteGranted bool `json:"voteGranted"`
}

// AppendEntriesArgs represents arguments for AppendEntries RPC
type AppendEntriesArgs struct {
	Term         int        `json:"term"`
	LeaderID     int        `json:"leaderId"`
	PrevLogIndex int        `json:"prevLogIndex"`
	PrevLogTerm  int        `json:"prevLogTerm"`
	Entries      []LogEntry `json:"entries"`
	LeaderCommit int        `json:"leaderCommit"`
}

// AppendEntriesReply represents reply for AppendEntries RPC
type AppendEntriesReply struct {
	Term    int  `json:"term"`
	Success bool `json:"success"`
}

// InstallSnapshotArgs represents arguments for InstallSnapshot RPC
type InstallSnapshotArgs struct {
	Term              int    `json:"term"`
	LeaderID          int    `json:"leaderId"`
	LastIncludedIndex int    `json:"lastIncludedIndex"`
	LastIncludedTerm  int    `json:"lastIncludedTerm"`
	Offset            int    `json:"offset"`
	Data              []byte `json:"data"`
	Done              bool   `json:"done"`
}

// InstallSnapshotReply represents reply for InstallSnapshot RPC
type InstallSnapshotReply struct {
	Term int `json:"term"`
}

// incompleteSnapshot tracks the state of an in-progress multi-chunk snapshot
type incompleteSnapshot struct {
	lastIncludedIndex int
	lastIncludedTerm  int
	data              []byte
	expectedSize      int
	receivedSize      int
}

// Raft represents a Raft server instance
type Raft struct {
	mu    sync.RWMutex
	peers []int // peer IDs
	me    int   // this server's index into peers[]

	// Persistent state on all servers
	currentTerm int
	votedFor    *int // candidateId that received vote in current term (nil if none)
	log         []LogEntry

	// Volatile state on all servers
	commitIndex int
	lastApplied int

	// Volatile state on leaders
	nextIndex  []int
	matchIndex []int

	// Current state
	state         State
	electionTimer *time.Timer
	heartbeatTick *time.Ticker

	// Channels
	applyCh chan LogEntry
	stopCh  chan struct{}

	// Random source for election timeouts
	rand *rand.Rand

	// Configuration
	electionTimeoutMin time.Duration
	electionTimeoutMax time.Duration
	heartbeatInterval  time.Duration

	// Statistics
	lastHeartbeat time.Time
	lastMajorityContact time.Time
	lastPeerResponse []time.Time // Track when each peer last responded

	// Persistence
	persister *Persister

	// Snapshot state for multi-chunk handling
	incomingSnapshot *incompleteSnapshot

	// Snapshot state tracking (for when no persister is available)
	lastSnapshotIndex int
	lastSnapshotTerm  int

	// Configuration management
	currentConfig     Configuration
	inJointConsensus  bool

	// Client operation deduplication
	appliedCommands   map[string]int    // command -> last applied index
	maxDedupEntries   int               // maximum entries to keep for deduplication

	// RPC functions (can be overridden for testing)
	sendRequestVote       func(peer int, args *RequestVoteArgs, reply *RequestVoteReply) bool
	sendAppendEntries     func(peer int, args *AppendEntriesArgs, reply *AppendEntriesReply) bool
	sendInstallSnapshotFn func(peer int, args *InstallSnapshotArgs, reply *InstallSnapshotReply) bool
}

// NewRaft creates a new Raft server
func NewRaft(peers []int, me int, applyCh chan LogEntry) *Raft {
	rf := &Raft{
		peers:   peers,
		me:      me,
		applyCh: applyCh,
		stopCh:  make(chan struct{}),

		// Initialize persistent state
		currentTerm: 0,
		votedFor:    nil,
		log:         make([]LogEntry, 1), // log[0] is a dummy entry

		// Initialize volatile state
		commitIndex: 0,
		lastApplied: 0,

		// Initialize state
		state: Follower,

		// Configuration
		electionTimeoutMin: 150 * time.Millisecond,
		electionTimeoutMax: 300 * time.Millisecond,
		heartbeatInterval:  50 * time.Millisecond,

		// Random source
		rand: rand.New(rand.NewSource(time.Now().UnixNano() + int64(me))),

		// Initialize deduplication
		appliedCommands: make(map[string]int),
		maxDedupEntries: 1000,
	}

	// Initialize configuration
	rf.currentConfig = Configuration{
		Servers: make([]ServerConfiguration, len(peers)),
	}
	for i, peerID := range peers {
		rf.currentConfig.Servers[i] = ServerConfiguration{
			ID:      peerID,
			Address: fmt.Sprintf("localhost:%d", 8000+peerID),
		}
	}

	// Initialize log[0] as dummy entry
	rf.log[0] = LogEntry{Term: 0, Index: 0}

	// Initialize leader state
	rf.nextIndex = make([]int, len(peers))
	rf.matchIndex = make([]int, len(peers))
	rf.lastPeerResponse = make([]time.Time, len(peers))

	// Set default RPC functions
	rf.sendRequestVote = rf.defaultSendRequestVote
	rf.sendAppendEntries = rf.defaultSendAppendEntries
	rf.sendInstallSnapshotFn = rf.defaultSendInstallSnapshot

	return rf
}

// SetSendRequestVote sets the function used for sending RequestVote RPCs
func (rf *Raft) SetSendRequestVote(fn func(peer int, args *RequestVoteArgs, reply *RequestVoteReply) bool) {
	rf.sendRequestVote = fn
}

// SetSendAppendEntries sets the function used for sending AppendEntries RPCs
func (rf *Raft) SetSendAppendEntries(fn func(peer int, args *AppendEntriesArgs, reply *AppendEntriesReply) bool) {
	rf.sendAppendEntries = fn
}

// SetSendInstallSnapshot sets the function used for sending InstallSnapshot RPCs
func (rf *Raft) SetSendInstallSnapshot(fn func(peer int, args *InstallSnapshotArgs, reply *InstallSnapshotReply) bool) {
	rf.sendInstallSnapshotFn = fn
}

// Start starts the Raft server
func (rf *Raft) Start(ctx context.Context) {
	go rf.run(ctx)
}

// Stop stops the Raft server
func (rf *Raft) Stop() {
	close(rf.stopCh)
}

// GetState returns the current term and whether this server believes it is the leader
func (rf *Raft) GetState() (int, bool) {
	rf.mu.RLock()
	defer rf.mu.RUnlock()
	return rf.currentTerm, rf.state == Leader
}

// Submit submits a command to the Raft cluster
func (rf *Raft) Submit(command interface{}) (int, int, bool) {
	rf.mu.Lock()
	defer rf.mu.Unlock()

	if rf.state != Leader {
		return -1, rf.currentTerm, false
	}

	// Check if leader should step down due to lack of majority contact
	if time.Since(rf.lastMajorityContact) > 2*rf.heartbeatInterval {
		rf.state = Follower
		rf.stopElectionTimer()
		if rf.heartbeatTick != nil {
			rf.heartbeatTick.Stop()
		}
		rf.resetElectionTimer()
		return -1, rf.currentTerm, false
	}

	// Check for duplicate command (simple string-based deduplication)
	if cmdStr, ok := command.(string); ok && cmdStr != "" {
		// First check if command was already applied
		if lastIndex, exists := rf.appliedCommands[cmdStr]; exists {
			// Command already applied, return the previous index
			return lastIndex, rf.currentTerm, true
		}
		
		// Then check if command is already in the log but not yet applied
		for i := rf.lastApplied + 1; i < len(rf.log); i++ {
			if rf.log[i].Command == cmdStr {
				// Command already in log, return its index
				return rf.log[i].Index, rf.log[i].Term, true
			}
		}
	}

	// Get the next index after the last entry
	index := rf.getLastLogIndex() + 1
	term := rf.currentTerm

	entry := LogEntry{
		Term:    term,
		Index:   index,
		Command: command,
	}

	rf.log = append(rf.log, entry)
	rf.persist()

	// Trigger immediate heartbeat to replicate entry
	go rf.broadcastAppendEntries()

	return index, term, true
}

// run is the main event loop for the Raft server
func (rf *Raft) run(ctx context.Context) {
	rf.resetElectionTimer()

	for {
		select {
		case <-ctx.Done():
			return
		case <-rf.stopCh:
			return
		case <-rf.electionTimer.C:
			rf.handleElectionTimeout()
		default:
			if rf.state == Leader {
				select {
				case <-rf.heartbeatTick.C:
					rf.broadcastAppendEntries()
				default:
				}
			}
			time.Sleep(1 * time.Millisecond) // Small sleep to prevent busy waiting
		}
	}
}

// handleElectionTimeout handles election timeout
func (rf *Raft) handleElectionTimeout() {
	rf.mu.Lock()
	defer rf.mu.Unlock()

	if rf.state == Leader {
		return // Leaders don't timeout
	}

	// Check if we're still in the configuration
	if !rf.isInConfiguration(rf.currentConfig, rf.me) {
		// We've been removed from the configuration, don't start elections
		rf.resetElectionTimer() // Keep resetting timer to prevent busy loop
		return
	}

	// Convert to candidate and start election
	rf.state = Candidate
	rf.currentTerm++
	rf.votedFor = &rf.me
	rf.persist()
	rf.resetElectionTimer()

	log.Printf("Server %d starting election for term %d", rf.me, rf.currentTerm)

	// Vote for self
	votes := 1
	finished := 1
	totalVotingPeers := 1 // Count self as voting peer
	cond := sync.NewCond(&rf.mu)

	for i := range rf.peers {
		if i == rf.me {
			continue
		}
		
		// Skip non-voting members in elections
		if !rf.isVotingMember(rf.peers[i]) {
			continue
		}
		totalVotingPeers++

		go func(peer int) {
			args := RequestVoteArgs{
				Term:         rf.currentTerm,
				CandidateID:  rf.me,
				LastLogIndex: len(rf.log) - 1,
				LastLogTerm:  rf.log[len(rf.log)-1].Term,
			}

			reply := RequestVoteReply{}
			if rf.sendRequestVote(peer, &args, &reply) {
				rf.mu.Lock()
				defer rf.mu.Unlock()

				if reply.Term > rf.currentTerm {
					rf.currentTerm = reply.Term
					rf.state = Follower
					rf.votedFor = nil
					rf.persist()
					rf.resetElectionTimer()
				} else if reply.VoteGranted && rf.state == Candidate && args.Term == rf.currentTerm {
					votes++
				}
				finished++
				cond.Broadcast()
			} else {
				rf.mu.Lock()
				finished++
				cond.Broadcast()
				rf.mu.Unlock()
			}
		}(i)
	}

	// Get the majority size based on voting members only
	majoritySize := rf.getMajoritySize()
	
	// Wait for majority or all voting peer responses
	for votes < majoritySize && finished < totalVotingPeers {
		cond.Wait()
	}

	if rf.state == Candidate && votes >= majoritySize {
		// Won election
		rf.state = Leader
		rf.initializeLeaderState()
		rf.stopElectionTimer()
		rf.startHeartbeat()
		log.Printf("Server %d became leader for term %d", rf.me, rf.currentTerm)
		go rf.broadcastAppendEntries()
	}
}

// resetElectionTimer resets the election timer with a random timeout
func (rf *Raft) resetElectionTimer() {
	if rf.electionTimer != nil {
		rf.electionTimer.Stop()
	}
	timeout := rf.electionTimeoutMin + time.Duration(rf.rand.Int63n(int64(rf.electionTimeoutMax-rf.electionTimeoutMin)))
	rf.electionTimer = time.NewTimer(timeout)
}

// stopElectionTimer stops the election timer
func (rf *Raft) stopElectionTimer() {
	if rf.electionTimer != nil {
		rf.electionTimer.Stop()
	}
}

// startHeartbeat starts the heartbeat ticker for leaders
func (rf *Raft) startHeartbeat() {
	if rf.heartbeatTick != nil {
		rf.heartbeatTick.Stop()
	}
	rf.heartbeatTick = time.NewTicker(rf.heartbeatInterval)
}

// initializeLeaderState initializes leader-specific state
func (rf *Raft) initializeLeaderState() {
	// Reset leader state - the new leader must reconfirm all replication
	for i := range rf.nextIndex {
		rf.nextIndex[i] = rf.getLastLogIndex() + 1
		rf.matchIndex[i] = 0  // Reset match indices - leader must reconfirm all replication
	}
	
	// Initialize response tracking
	if len(rf.lastPeerResponse) != len(rf.peers) {
		rf.lastPeerResponse = make([]time.Time, len(rf.peers))
	}
	// Clear previous response times
	for i := range rf.lastPeerResponse {
		rf.lastPeerResponse[i] = time.Time{}
	}
	
	// Initialize majority contact time
	rf.lastMajorityContact = time.Now()
	
	// According to Raft safety rules (Section 5.4.2), a new leader cannot immediately conclude 
	// that entries from previous terms are committed, even if they are stored on a majority of servers.
	// The leader can only commit previous term entries by committing an entry from its current term.
	// 
	// IMPORTANT: We do NOT reset commitIndex here. The leader maintains its current commitIndex
	// but won't increase it for entries from previous terms until it commits an entry from its own term.
	// This is handled in updateCommitIndex().
}

// broadcastAppendEntries sends AppendEntries RPCs to all followers
func (rf *Raft) broadcastAppendEntries() {
	rf.mu.RLock()
	if rf.state != Leader {
		rf.mu.RUnlock()
		return
	}
	peers := rf.peers
	me := rf.me
	rf.mu.RUnlock()

	for i, peerID := range peers {
		if peerID == me {
			continue
		}
		go rf.sendAppendEntriesToPeerByIndex(i)
	}
	
	// Also check majority contact periodically during heartbeats
	rf.mu.Lock()
	if rf.state == Leader {
		rf.updateCommitIndex()
	}
	rf.mu.Unlock()
}

// sendAppendEntriesToPeerByIndex sends AppendEntries RPC to a specific peer by array index
func (rf *Raft) sendAppendEntriesToPeerByIndex(peerIndex int) {
	rf.mu.Lock()
	if rf.state != Leader {
		rf.mu.Unlock()
		return
	}

	// Check bounds
	if peerIndex >= len(rf.nextIndex) || peerIndex >= len(rf.peers) {
		rf.mu.Unlock()
		return
	}

	peerID := rf.peers[peerIndex]
	rf.sendAppendEntriesToPeerLocked(peerIndex, peerID)
}

// sendAppendEntriesToPeer sends AppendEntries RPC to a specific peer
func (rf *Raft) sendAppendEntriesToPeer(peer int) {
	rf.mu.Lock()
	if rf.state != Leader {
		rf.mu.Unlock()
		return
	}

	// Find the peer index
	peerIndex := -1
	for i, peerID := range rf.peers {
		if peerID == peer {
			peerIndex = i
			break
		}
	}

	if peerIndex == -1 {
		rf.mu.Unlock()
		return
	}

	rf.sendAppendEntriesToPeerLocked(peerIndex, peer)
}

// sendAppendEntriesToPeerLocked sends AppendEntries RPC to a specific peer (assumes lock is held)
func (rf *Raft) sendAppendEntriesToPeerLocked(peerIndex int, peerID int) {
	nextIndex := rf.nextIndex[peerIndex]
	prevLogIndex := nextIndex - 1
	prevLogTerm := 0
	
	// Handle prevLogIndex that might be in snapshot
	if prevLogIndex > 0 {
		if prevLogIndex == rf.lastSnapshotIndex {
			prevLogTerm = rf.lastSnapshotTerm
		} else if prevLogIndex > rf.lastSnapshotIndex {
			// Find the entry in current log
			if entry := rf.getLogEntry(prevLogIndex); entry != nil {
				prevLogTerm = entry.Term
			}
		}
	}

	// Get entries to send
	entries := make([]LogEntry, 0)
	for _, entry := range rf.log {
		if entry.Index >= nextIndex {
			entries = append(entries, entry)
		}
	}

	args := AppendEntriesArgs{
		Term:         rf.currentTerm,
		LeaderID:     rf.me,
		PrevLogIndex: prevLogIndex,
		PrevLogTerm:  prevLogTerm,
		Entries:      entries,
		LeaderCommit: rf.commitIndex,
	}
	rf.mu.Unlock()

	reply := AppendEntriesReply{}
	rpcSuccess := rf.sendAppendEntries(peerID, &args, &reply)
	
	rf.mu.Lock()
	defer rf.mu.Unlock()

	if rpcSuccess {
		if reply.Term > rf.currentTerm {
			rf.currentTerm = reply.Term
			rf.state = Follower
			rf.votedFor = nil
			rf.persist()
			rf.resetElectionTimer()
			return
		}

		if rf.state != Leader || args.Term != rf.currentTerm {
			return
		}

		// Check bounds again after reacquiring lock
		if peerIndex >= len(rf.nextIndex) || peerIndex >= len(rf.matchIndex) {
			return
		}

		// We got a response - peer is alive
		if peerIndex < len(rf.lastPeerResponse) {
			rf.lastPeerResponse[peerIndex] = time.Now()
			// log.Printf("Server %d: Got response from peer %d", rf.me, peerID)
		}
		
		if reply.Success {
			rf.nextIndex[peerIndex] = args.PrevLogIndex + len(args.Entries) + 1
			rf.matchIndex[peerIndex] = args.PrevLogIndex + len(args.Entries)
			rf.updateCommitIndex()
		} else {
			rf.nextIndex[peerIndex] = max(1, rf.nextIndex[peerIndex]-1)
			
			// Check if we need to send a snapshot
			// If the required log entry is no longer available (was snapshotted), send InstallSnapshot
			lastSnapshotIndex := rf.getLastSnapshotIndex()
			if lastSnapshotIndex > 0 && rf.nextIndex[peerIndex] <= lastSnapshotIndex {
				go rf.sendInstallSnapshot(peerID)
			}
		}
	} else {
		// RPC failed - no response from peer
		if rf.state == Leader && rf.currentTerm == args.Term {
			// log.Printf("Server %d: Failed to send AppendEntries to peer %d", rf.me, peerID)
			rf.updateCommitIndex()
		}
	}
}


// updateCommitIndex updates the commit index based on majority replication
func (rf *Raft) updateCommitIndex() {
	// Track which peers have responded recently (within reasonable timeout)
	recentResponseTimeout := 5 * rf.heartbeatInterval // Peers should respond within 5 heartbeats
	responsivePeers := make([]int, 0)
	responsivePeers = append(responsivePeers, rf.me) // Self is always responsive
	
	for i := range rf.peers {
		if i != rf.me && i < len(rf.lastPeerResponse) {
			// Check if peer has responded recently
			if !rf.lastPeerResponse[i].IsZero() && time.Since(rf.lastPeerResponse[i]) < recentResponseTimeout {
				responsivePeers = append(responsivePeers, rf.peers[i])
			} else if rf.state == Leader && !rf.lastPeerResponse[i].IsZero() {
				// Debug: log unresponsive peers
				// log.Printf("Server %d: Peer %d last responded %v ago", rf.me, rf.peers[i], time.Since(rf.lastPeerResponse[i]))
			}
		}
	}

	// Count voting members among responsive peers
	votingResponsiveCount := 0
	for _, peerID := range responsivePeers {
		if rf.isVotingMember(peerID) {
			votingResponsiveCount++
		}
	}
	
	// Check if we have majority contact
	if votingResponsiveCount >= rf.getMajoritySize() {
		rf.lastMajorityContact = time.Now()
	} else {
		// Check if we should step down due to lack of majority contact
		timeSinceContact := time.Since(rf.lastMajorityContact)
		// Use a reasonable timeout (e.g., 10x heartbeat interval)
		if timeSinceContact > 10*rf.heartbeatInterval {
			// Lost majority contact - step down
			rf.state = Follower
			rf.votedFor = nil
			rf.persist()
			rf.stopElectionTimer()
			if rf.heartbeatTick != nil {
				rf.heartbeatTick.Stop()
			}
			rf.resetElectionTimer()
			log.Printf("Server %d stepping down due to lost majority contact (no responses for %v)", rf.me, timeSinceContact)
			return
		}
		// Debug logging
		// if rf.state == Leader {
		// 	log.Printf("Server %d: Only %d voting members responsive, need %d (time since majority: %v)", 
		// 		rf.me, votingResponsiveCount, rf.getMajoritySize(), timeSinceContact)
		// }
	}

	// Find the highest index that can be committed from current term
	for n := rf.getLastLogIndex(); n > rf.commitIndex; n-- {
		entry := rf.getLogEntry(n)
		if entry == nil || entry.Term != rf.currentTerm {
			continue
		}

		count := 1 // Count self if voting member
		if !rf.isVotingMember(rf.me) {
			count = 0
		}
		
		for i := range rf.peers {
			if i != rf.me && i < len(rf.matchIndex) && rf.matchIndex[i] >= n && rf.isVotingMember(rf.peers[i]) {
				count++
			}
		}

		if count >= rf.getMajoritySize() {
			// When committing current term entry, we can also commit all previous entries
			rf.commitIndex = n
			go rf.applyCommittedEntries()
			break
		}
	}
}

// applyCommittedEntries applies committed entries to the state machine
func (rf *Raft) applyCommittedEntries() {
	rf.mu.Lock()
	defer rf.mu.Unlock()

	for rf.lastApplied < rf.commitIndex {
		rf.lastApplied++
		
		// Skip if entry is in snapshot
		if rf.lastApplied <= rf.lastSnapshotIndex {
			continue
		}
		
		// Find the entry
		entry := rf.getLogEntry(rf.lastApplied)
		if entry == nil {
			// Entry not found, it might be in snapshot
			continue
		}
		
		// Update deduplication map for applied commands
		if cmdStr, ok := entry.Command.(string); ok && cmdStr != "" {
			rf.appliedCommands[cmdStr] = entry.Index
			// Clean up old entries if map gets too large
			if len(rf.appliedCommands) > rf.maxDedupEntries {
				rf.cleanupDedupMap()
			}
		}
		
		// Check if this is a configuration change entry
		if rf.isConfigurationEntry(*entry) {
			rf.handleConfigurationEntry(*entry)
		}
		
		select {
		case rf.applyCh <- *entry:
		case <-time.After(500 * time.Millisecond):
			// Log warning about slow consumer but continue
			log.Printf("Server %d: Warning - apply channel blocked, skipping entry %d", rf.me, entry.Index)
		case <-rf.stopCh:
			return
		}
	}
}

// cleanupDedupMap removes old entries from the deduplication map
func (rf *Raft) cleanupDedupMap() {
	// Simple cleanup: remove entries that are too old
	minIndex := rf.lastApplied - rf.maxDedupEntries/2
	for cmd, index := range rf.appliedCommands {
		if index < minIndex {
			delete(rf.appliedCommands, cmd)
		}
	}
}

// persist saves persistent state to stable storage
func (rf *Raft) persist() {
	if rf.persister != nil {
		rf.persister.SaveState(rf.currentTerm, rf.votedFor, rf.log)
	}
}


// getLogEntry returns the log entry at the given index, accounting for snapshots
func (rf *Raft) getLogEntry(index int) *LogEntry {
	if index <= rf.lastSnapshotIndex {
		return nil // Entry was snapshotted
	}
	// Find the entry in the log
	for i := range rf.log {
		if rf.log[i].Index == index {
			return &rf.log[i]
		}
	}
	return nil
}

// getLastLogIndex returns the index of the last log entry
func (rf *Raft) getLastLogIndex() int {
	if len(rf.log) > 0 {
		lastEntry := rf.log[len(rf.log)-1]
		if lastEntry.Index > rf.lastSnapshotIndex {
			return lastEntry.Index
		}
	}
	return rf.lastSnapshotIndex
}

// getLastLogTerm returns the term of the last log entry
func (rf *Raft) getLastLogTerm() int {
	if len(rf.log) > 0 {
		lastEntry := rf.log[len(rf.log)-1]
		if lastEntry.Index > rf.lastSnapshotIndex {
			return lastEntry.Term
		}
	}
	return rf.lastSnapshotTerm
}

// readPersist restores persistent state from stable storage
func (rf *Raft) readPersist(data []byte) {
	if data == nil || len(data) == 0 {
		return
	}

	var state PersistentState
	if err := json.Unmarshal(data, &state); err != nil {
		log.Printf("Server %d: Failed to unmarshal persistent state: %v", rf.me, err)
		return
	}

	rf.mu.Lock()
	defer rf.mu.Unlock()

	rf.currentTerm = state.CurrentTerm
	rf.votedFor = state.VotedFor

	// Ensure log has at least the dummy entry
	if len(state.Log) == 0 {
		rf.log = []LogEntry{{Term: 0, Index: 0}}
	} else {
		rf.log = state.Log
	}

	log.Printf("Server %d: Restored state - Term: %d, VotedFor: %v, Log entries: %d",
		rf.me, rf.currentTerm, rf.votedFor, len(rf.log))
}

// defaultSendRequestVote is the default implementation of RequestVote RPC
func (rf *Raft) defaultSendRequestVote(peer int, args *RequestVoteArgs, reply *RequestVoteReply) bool {
	// This is the default implementation that returns failure
	// This method should be overridden by SetSendRequestVote with actual transport
	log.Printf("Server %d: No transport configured for RequestVote RPC to peer %d", rf.me, peer)

	// Initialize reply with safe defaults but return false to indicate failure
	reply.Term = args.Term
	reply.VoteGranted = false

	return false
}

// defaultSendAppendEntries is the default implementation of AppendEntries RPC
func (rf *Raft) defaultSendAppendEntries(peer int, args *AppendEntriesArgs, reply *AppendEntriesReply) bool {
	// This is the default implementation that returns failure
	// This method should be overridden by SetSendAppendEntries with actual transport
	log.Printf("Server %d: No transport configured for AppendEntries RPC to peer %d", rf.me, peer)

	// Initialize reply with safe defaults but return false to indicate failure
	reply.Term = args.Term
	reply.Success = false

	return false
}

// defaultSendInstallSnapshot is the default implementation of InstallSnapshot RPC
func (rf *Raft) defaultSendInstallSnapshot(peer int, args *InstallSnapshotArgs, reply *InstallSnapshotReply) bool {
	// This is the default implementation that returns failure
	// This method should be overridden by SetSendInstallSnapshot with actual transport
	log.Printf("Server %d: No transport configured for InstallSnapshot RPC to peer %d", rf.me, peer)

	// Initialize reply with safe defaults but return false to indicate failure
	reply.Term = args.Term

	return false
}

// RequestVote handles RequestVote RPC
func (rf *Raft) RequestVote(args *RequestVoteArgs, reply *RequestVoteReply) error {
	rf.mu.Lock()
	defer rf.mu.Unlock()

	reply.Term = rf.currentTerm
	reply.VoteGranted = false

	// Reply false if term < currentTerm
	if args.Term < rf.currentTerm {
		return nil
	}

	// Check if we're still in the configuration - removed servers shouldn't vote
	if !rf.isInConfiguration(rf.currentConfig, rf.me) {
		// We've been removed from the configuration, don't grant votes
		return nil
	}

	// If RPC request contains term T > currentTerm: set currentTerm = T, convert to follower
	if args.Term > rf.currentTerm {
		rf.currentTerm = args.Term
		rf.state = Follower
		rf.votedFor = nil
		rf.persist()
		reply.Term = rf.currentTerm
	}

	// If votedFor is null or candidateId, and candidate's log is at least as up-to-date as receiver's log, grant vote
	if (rf.votedFor == nil || *rf.votedFor == args.CandidateID) && rf.isLogUpToDate(args.LastLogIndex, args.LastLogTerm) {
		// Check if we've heard from a current leader recently (within minimum election timeout)
		// This prevents unnecessary elections when the leader is still active
		if rf.state == Follower && time.Since(rf.lastHeartbeat) < rf.electionTimeoutMin {
			// Deny vote as we have a current leader
			return nil
		}
		
		rf.votedFor = &args.CandidateID
		reply.VoteGranted = true
		rf.persist()
		rf.resetElectionTimer()
	}

	return nil
}


// AppendEntries handles AppendEntries RPC
func (rf *Raft) AppendEntries(args *AppendEntriesArgs, reply *AppendEntriesReply) error {
	rf.mu.Lock()
	defer rf.mu.Unlock()

	reply.Term = rf.currentTerm
	reply.Success = false

	// Reply false if term < currentTerm
	if args.Term < rf.currentTerm {
		return nil
	}

	// If RPC request contains term T > currentTerm: set currentTerm = T, convert to follower
	if args.Term > rf.currentTerm {
		rf.currentTerm = args.Term
		rf.state = Follower
		rf.votedFor = nil
		rf.persist()
		reply.Term = rf.currentTerm
	}

	rf.resetElectionTimer()
	rf.lastHeartbeat = time.Now()

	// Reply false if log doesn't contain an entry at prevLogIndex whose term matches prevLogTerm
	if args.PrevLogIndex > 0 {
		// Check if prevLogIndex is in snapshot
		if args.PrevLogIndex < rf.lastSnapshotIndex {
			// Entry is before our snapshot, we can't verify it
			return nil
		} else if args.PrevLogIndex == rf.lastSnapshotIndex {
			// Check snapshot term
			if rf.lastSnapshotTerm != args.PrevLogTerm {
				return nil
			}
		} else {
			// For now, keep simple approach until we fix log indexing
			found := false
			for _, entry := range rf.log {
				if entry.Index == args.PrevLogIndex {
					if entry.Term != args.PrevLogTerm {
						return nil
					}
					found = true
					break
				}
			}
			if !found {
				return nil
			}
		}
	}

	// If an existing entry conflicts with a new one (same index but different terms),
	// delete the existing entry and all that follow it
	for i, entry := range args.Entries {
		index := args.PrevLogIndex + 1 + i
		if index < len(rf.log) {
			if rf.log[index].Term != entry.Term {
				rf.log = rf.log[:index]
				break
			}
		}
	}

	// Append any new entries not already in the log
	for i, entry := range args.Entries {
		index := args.PrevLogIndex + 1 + i
		if index >= len(rf.log) {
			rf.log = append(rf.log, entry)
		}
	}

	rf.persist()

	// If leaderCommit > commitIndex, set commitIndex = min(leaderCommit, index of last new entry)
	// Only update commitIndex if we're still a follower (not a leader)
	if args.LeaderCommit > rf.commitIndex && rf.state != Leader {
		rf.commitIndex = min(args.LeaderCommit, rf.getLastLogIndex())
		go rf.applyCommittedEntries()
	}

	reply.Success = true
	return nil
}

// isLogUpToDate checks if the given log is at least as up-to-date as this server's log
func (rf *Raft) isLogUpToDate(lastLogIndex, lastLogTerm int) bool {
	ourLastIndex := rf.getLastLogIndex()
	ourLastTerm := rf.getLastLogTerm()

	if lastLogTerm != ourLastTerm {
		return lastLogTerm > ourLastTerm
	}
	return lastLogIndex >= ourLastIndex
}

// min returns the minimum of two integers
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// max returns the maximum of two integers
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// hasCurrentTermEntryCommitted checks if there's a current term entry already committed
func (rf *Raft) hasCurrentTermEntryCommitted(currentTerm int, upToIndex int) bool {
	for i := 1; i <= upToIndex && i < len(rf.log); i++ {
		if rf.log[i].Term == currentTerm {
			return true
		}
	}
	return false
}

// TransferLeadership transfers leadership to a specific server
func (rf *Raft) TransferLeadership(targetServer int) error {
	rf.mu.Lock()
	defer rf.mu.Unlock()

	if rf.state != Leader {
		return fmt.Errorf("not the leader")
	}

	// Check if target server exists and is caught up
	targetIndex := -1
	for i, peerID := range rf.peers {
		if peerID == targetServer {
			targetIndex = i
			break
		}
	}

	if targetIndex == -1 {
		return fmt.Errorf("target server %d not found", targetServer)
	}

	// Check if target is voting member
	if !rf.isVotingMember(targetServer) {
		return fmt.Errorf("target server %d is not a voting member", targetServer)
	}

	// First, ensure target is fully caught up
	if targetIndex < len(rf.matchIndex) {
		lastLogIndex := rf.getLastLogIndex()
		if rf.matchIndex[targetIndex] < lastLogIndex {
			// Send AppendEntries to catch up the target
			go rf.sendAppendEntriesToPeer(targetServer)
			return fmt.Errorf("target server not caught up yet")
		}
	}

	// Step down and prevent ourselves from becoming candidate for a while
	rf.state = Follower
	rf.votedFor = &targetServer // Vote for the target to help it win
	rf.persist()
	
	// Set a much longer election timeout to prevent us from interfering
	if rf.electionTimer != nil {
		rf.electionTimer.Stop()
	}
	// Use a very long timeout (10x normal) to ensure we don't interfere
	timeout := rf.electionTimeoutMax * 10
	rf.electionTimer = time.NewTimer(timeout)
	
	if rf.heartbeatTick != nil {
		rf.heartbeatTick.Stop()
	}
	
	log.Printf("Server %d transferring leadership to server %d", rf.me, targetServer)
	return nil
}

// sendTimeoutNow sends a TimeoutNow RPC to trigger immediate election
func (rf *Raft) sendTimeoutNow(server int) {
	// For now, we'll simulate this by disconnecting ourselves temporarily
	// In a real implementation, you'd send a TimeoutNow RPC
	// The target server will timeout and start an election
}
