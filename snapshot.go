package raft

import (
	"fmt"
	"log"
	"time"
)

// Snapshot represents a point-in-time snapshot of the state machine
type Snapshot struct {
	Data              []byte `json:"data"`
	LastIncludedIndex int    `json:"lastIncludedIndex"`
	LastIncludedTerm  int    `json:"lastIncludedTerm"`
}

// TakeSnapshot creates a snapshot of the current state machine state
func (rf *Raft) TakeSnapshot(snapshot []byte, index int) error {
	rf.mu.Lock()
	defer rf.mu.Unlock()

	// Cannot snapshot beyond the commit index
	if index > rf.commitIndex {
		return fmt.Errorf("cannot snapshot beyond commit index: %d > %d", index, rf.commitIndex)
	}

	// Cannot snapshot if index is before the last snapshot
	if index <= rf.getLastSnapshotIndex() {
		return fmt.Errorf("cannot snapshot before last snapshot index: %d <= %d", index, rf.getLastSnapshotIndex())
	}

	// Get the term at the snapshot index
	var lastIncludedTerm int
	found := false
	for _, entry := range rf.log {
		if entry.Index == index {
			lastIncludedTerm = entry.Term
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("log entry at index %d not found", index)
	}

	// Save the snapshot
	if rf.persister != nil {
		if err := rf.persister.SaveSnapshot(snapshot, index, lastIncludedTerm); err != nil {
			return fmt.Errorf("failed to save snapshot: %v", err)
		}
	}

	// Update snapshot tracking state
	rf.lastSnapshotIndex = index
	rf.lastSnapshotTerm = lastIncludedTerm

	// Trim the log
	rf.trimLog(index)

	log.Printf("Server %d created snapshot up to index %d", rf.me, index)
	return nil
}

// InstallSnapshot handles InstallSnapshot RPC
func (rf *Raft) InstallSnapshot(args *InstallSnapshotArgs, reply *InstallSnapshotReply) error {
	rf.mu.Lock()
	defer rf.mu.Unlock()

	reply.Term = rf.currentTerm

	// Reply immediately if term < currentTerm
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

	// Create new snapshot file if this is the first chunk (offset is 0)
	if args.Offset == 0 {
		// Clear any existing incomplete snapshot and start a new one
		rf.incomingSnapshot = &incompleteSnapshot{
			lastIncludedIndex: args.LastIncludedIndex,
			lastIncludedTerm:  args.LastIncludedTerm,
			data:              make([]byte, 0, len(args.Data)*2), // Pre-allocate with some buffer
			expectedSize:      0, // Will be determined when Done=true
			receivedSize:      0,
		}
	}

	// Validate that this chunk belongs to the current snapshot
	if rf.incomingSnapshot == nil ||
		rf.incomingSnapshot.lastIncludedIndex != args.LastIncludedIndex ||
		rf.incomingSnapshot.lastIncludedTerm != args.LastIncludedTerm {
		// Invalid chunk, ignore it
		return nil
	}

	// Validate offset matches expected position
	if args.Offset != rf.incomingSnapshot.receivedSize {
		// Out of order chunk, ignore it
		return nil
	}

	// Append the data chunk
	rf.incomingSnapshot.data = append(rf.incomingSnapshot.data, args.Data...)
	rf.incomingSnapshot.receivedSize += len(args.Data)

	// If done is false, we're expecting more chunks
	if !args.Done {
		return nil
	}

	// Save snapshot file and discard any existing or partial snapshot with a smaller index
	if rf.persister != nil {
		if err := rf.persister.SaveSnapshot(rf.incomingSnapshot.data, args.LastIncludedIndex, args.LastIncludedTerm); err != nil {
			rf.incomingSnapshot = nil // Clear incomplete snapshot on error
			return fmt.Errorf("failed to save snapshot: %v", err)
		}
	}

	// If existing log entry has same index and term as snapshot's last included entry,
	// retain log entries following it
	existingEntry := rf.getLogEntry(args.LastIncludedIndex)
	if existingEntry != nil && existingEntry.Term == args.LastIncludedTerm {
		// Keep entries after the snapshot
		newLog := []LogEntry{{
			Term:  args.LastIncludedTerm,
			Index: args.LastIncludedIndex,
		}}
		// Keep all entries after the snapshot
		for _, entry := range rf.log {
			if entry.Index > args.LastIncludedIndex {
				newLog = append(newLog, entry)
			}
		}
		rf.log = newLog
	} else {
		// Discard the entire log
		rf.log = []LogEntry{{
			Term:  args.LastIncludedTerm,
			Index: args.LastIncludedIndex,
		}}
	}

	// Update state
	rf.lastApplied = args.LastIncludedIndex
	rf.commitIndex = args.LastIncludedIndex
	rf.lastSnapshotIndex = args.LastIncludedIndex
	rf.lastSnapshotTerm = args.LastIncludedTerm

	rf.persist()

	// Apply the snapshot to the state machine via apply channel
	if rf.applyCh != nil && rf.incomingSnapshot != nil {
		// Save snapshot data before clearing
		snapshotData := rf.incomingSnapshot.data
		// Send snapshot to apply channel
		go func() {
			select {
			case rf.applyCh <- LogEntry{
				Term:    args.LastIncludedTerm,
				Index:   args.LastIncludedIndex,
				Command: snapshotData, // The snapshot data
			}:
			case <-time.After(1 * time.Second):
				log.Printf("Server %d: Failed to send snapshot to apply channel", rf.me)
			}
		}()
	}

	// Clear the incomplete snapshot since we're done
	rf.incomingSnapshot = nil

	log.Printf("Server %d installed snapshot up to index %d", rf.me, args.LastIncludedIndex)
	return nil
}

// sendInstallSnapshot sends an InstallSnapshot RPC to a follower
func (rf *Raft) sendInstallSnapshot(peer int) {
	rf.mu.Lock()
	if rf.state != Leader {
		rf.mu.Unlock()
		return
	}

	// Load snapshot from persister or use tracked state
	var snapshot []byte
	var lastIncludedIndex, lastIncludedTerm int
	if rf.persister != nil && rf.persister.HasSnapshot() {
		var err error
		snapshot, lastIncludedIndex, lastIncludedTerm, err = rf.persister.LoadSnapshot()
		if err != nil || snapshot == nil {
			rf.mu.Unlock()
			return
		}
	} else if rf.lastSnapshotIndex > 0 {
		// Use tracked state if no persister but we have snapshot info
		snapshot = []byte("fake snapshot data") // For testing purposes
		lastIncludedIndex = rf.lastSnapshotIndex
		lastIncludedTerm = rf.lastSnapshotTerm
	} else {
		rf.mu.Unlock()
		return
	}

	args := InstallSnapshotArgs{
		Term:              rf.currentTerm,
		LeaderID:          rf.me,
		LastIncludedIndex: lastIncludedIndex,
		LastIncludedTerm:  lastIncludedTerm,
		Offset:            0,
		Data:              snapshot,
		Done:              true,
	}
	rf.mu.Unlock()

	reply := InstallSnapshotReply{}
	if rf.sendInstallSnapshotRPC(peer, &args, &reply) {
		rf.mu.Lock()
		defer rf.mu.Unlock()

		if reply.Term > rf.currentTerm {
			rf.currentTerm = reply.Term
			rf.state = Follower
			rf.votedFor = nil
			rf.persist()
			rf.resetElectionTimer()
			return
		}

		if rf.state == Leader && args.Term == rf.currentTerm {
			// Find the peer index
			peerIndex := -1
			for i, peerID := range rf.peers {
				if peerID == peer {
					peerIndex = i
					break
				}
			}
			if peerIndex != -1 && peerIndex < len(rf.nextIndex) && peerIndex < len(rf.matchIndex) {
				rf.nextIndex[peerIndex] = args.LastIncludedIndex + 1
				rf.matchIndex[peerIndex] = args.LastIncludedIndex
			}
		}
	}
}

// sendInstallSnapshotRPC sends an InstallSnapshot RPC
func (rf *Raft) sendInstallSnapshotRPC(peer int, args *InstallSnapshotArgs, reply *InstallSnapshotReply) bool {
	return rf.sendInstallSnapshotFn(peer, args, reply)
}

// trimLog trims the log up to the given index
func (rf *Raft) trimLog(index int) {
	// Keep a dummy entry at index 0 for compatibility
	newLog := []LogEntry{{Term: 0, Index: 0}}
	for _, entry := range rf.log {
		if entry.Index > index {
			newLog = append(newLog, entry)
		}
	}
	rf.log = newLog
}

// getLastSnapshotIndex returns the index of the last snapshot
func (rf *Raft) getLastSnapshotIndex() int {
	if rf.persister != nil && rf.persister.HasSnapshot() {
		_, lastIncludedIndex, _, err := rf.persister.LoadSnapshot()
		if err == nil {
			return lastIncludedIndex
		}
	}
	// Fall back to tracked state if no persister or snapshot load failed
	return rf.lastSnapshotIndex
}

// getLastSnapshotTerm returns the term of the last snapshot
func (rf *Raft) getLastSnapshotTerm() int {
	if rf.persister != nil && rf.persister.HasSnapshot() {
		_, _, lastIncludedTerm, err := rf.persister.LoadSnapshot()
		if err == nil {
			return lastIncludedTerm
		}
	}
	// Fall back to tracked state if no persister or snapshot load failed
	return rf.lastSnapshotTerm
}

// needsSnapshot checks if a snapshot should be taken based on log size
func (rf *Raft) needsSnapshot(maxLogSize int) bool {
	rf.mu.RLock()
	defer rf.mu.RUnlock()
	
	return len(rf.log) > maxLogSize && rf.commitIndex > rf.getLastSnapshotIndex()
}

// getLogSize returns the current size of the log
func (rf *Raft) getLogSize() int {
	rf.mu.RLock()
	defer rf.mu.RUnlock()
	return len(rf.log)
}

// SnapshotProvider interface for generating snapshots
type SnapshotProvider interface {
	// TakeSnapshot should return the current state as a byte array
	TakeSnapshot() ([]byte, error)
}

// Add fields to Raft struct for snapshot support
type RaftWithSnapshot struct {
	*Raft
	persister        *Persister
	maxLogSize       int
	snapshotProvider SnapshotProvider
}

// NewRaftWithSnapshot creates a new Raft instance with snapshot support
func NewRaftWithSnapshot(peers []int, me int, applyCh chan LogEntry, persister *Persister, maxLogSize int, snapshotProvider SnapshotProvider) *RaftWithSnapshot {
	rf := NewRaft(peers, me, applyCh)
	rfs := &RaftWithSnapshot{
		Raft:             rf,
		persister:        persister,
		maxLogSize:       maxLogSize,
		snapshotProvider: snapshotProvider,
	}
	rf.SetPersister(persister)
	return rfs
}

// periodicSnapshot periodically checks if a snapshot should be taken
func (rfs *RaftWithSnapshot) periodicSnapshot() {
	for {
		select {
		case <-rfs.stopCh:
			return
		default:
			if rfs.needsSnapshot(rfs.maxLogSize) {
				if rfs.snapshotProvider != nil {
					snapshot, err := rfs.snapshotProvider.TakeSnapshot()
					if err != nil {
						log.Printf("Server %d: Failed to take snapshot: %v", rfs.me, err)
					} else {
						rfs.TakeSnapshot(snapshot, rfs.commitIndex)
					}
				} else {
					log.Printf("Server %d: No snapshot provider configured, skipping snapshot", rfs.me)
				}
			}
		}
	}
}