package raft

import (
	"fmt"
	"sync"
)

// SnapshotManager handles snapshot creation and installation
// Implements Section 7 of the Raft paper
type SnapshotManager struct {
	mu sync.Mutex

	// Dependencies
	logManager   *LogManager
	stateMachine StateMachine
	persistence  SnapshotPersistence
	config       *Config

	// Snapshot state
	lastSnapshotIndex int
	lastSnapshotTerm  int

	// In-progress snapshot for multi-chunk transfer
	incomingSnapshot *incomingSnapshot
}

// incomingSnapshot tracks a snapshot being received
type incomingSnapshot struct {
	lastIncludedIndex int
	lastIncludedTerm  int
	offset            int
	data              []byte
}

// NewSnapshotManager creates a new snapshot manager
func NewSnapshotManager(logManager *LogManager, stateMachine StateMachine, persistence SnapshotPersistence, config *Config) *SnapshotManager {
	return &SnapshotManager{
		logManager:   logManager,
		stateMachine: stateMachine,
		persistence:  persistence,
		config:       config,
	}
}

// TakeSnapshot creates a snapshot of the current state
func (sm *SnapshotManager) TakeSnapshot(index int) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	// Validate index
	commitIndex := sm.logManager.GetCommitIndex()
	if index > commitIndex {
		return fmt.Errorf("cannot snapshot beyond commit index: %d > %d", index, commitIndex)
	}

	if index <= sm.lastSnapshotIndex {
		return fmt.Errorf("cannot snapshot before last snapshot: %d <= %d", index, sm.lastSnapshotIndex)
	}

	// Get the term at the snapshot index
	entry := sm.logManager.GetEntry(index)
	if entry == nil {
		return fmt.Errorf("no entry at index %d", index)
	}

	// Create snapshot from state machine
	data, err := sm.stateMachine.Snapshot()
	if err != nil {
		return fmt.Errorf("failed to create snapshot: %v", err)
	}

	// Create snapshot object
	snapshot := &Snapshot{
		Data:              data,
		LastIncludedIndex: index,
		LastIncludedTerm:  entry.Term,
	}

	// Save to persistence
	if sm.persistence != nil {
		if err := sm.persistence.SaveSnapshot(snapshot); err != nil {
			return fmt.Errorf("failed to save snapshot: %v", err)
		}
	}

	// Update state
	sm.lastSnapshotIndex = index
	sm.lastSnapshotTerm = entry.Term

	// Trim log
	if err := sm.logManager.CreateSnapshot(index, entry.Term); err != nil {
		return fmt.Errorf("failed to trim log: %v", err)
	}

	if sm.config.Logger != nil {
		sm.config.Logger.Info("Created snapshot at index %d, term %d", index, entry.Term)
	}

	// Record metrics
	if sm.config.Metrics != nil {
		sm.config.Metrics.RecordSnapshot(len(data), 0)
	}

	return nil
}

// NeedsSnapshot checks if a snapshot should be taken
func (sm *SnapshotManager) NeedsSnapshot() bool {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	// Check if log has grown beyond threshold
	logSize := sm.logManager.GetLastIndex() - sm.lastSnapshotIndex
	return logSize > sm.config.MaxLogSize &&
		sm.logManager.GetCommitIndex() > sm.lastSnapshotIndex
}

// HandleInstallSnapshot handles incoming InstallSnapshot RPC
func (sm *SnapshotManager) HandleInstallSnapshot(args *InstallSnapshotArgs, reply *InstallSnapshotReply, currentTerm int) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	reply.Term = currentTerm

	// Handle first chunk (offset 0)
	if args.Offset == 0 {
		sm.incomingSnapshot = &incomingSnapshot{
			lastIncludedIndex: args.LastIncludedIndex,
			lastIncludedTerm:  args.LastIncludedTerm,
			offset:            0,
			data:              make([]byte, 0, len(args.Data)*2), // Pre-allocate
		}
	}

	// Validate chunk
	if sm.incomingSnapshot == nil ||
		sm.incomingSnapshot.lastIncludedIndex != args.LastIncludedIndex ||
		sm.incomingSnapshot.lastIncludedTerm != args.LastIncludedTerm {
		return fmt.Errorf("invalid snapshot chunk")
	}

	// Validate offset
	if args.Offset != sm.incomingSnapshot.offset {
		return fmt.Errorf("out of order chunk: expected offset %d, got %d",
			sm.incomingSnapshot.offset, args.Offset)
	}

	// Append data
	sm.incomingSnapshot.data = append(sm.incomingSnapshot.data, args.Data...)
	sm.incomingSnapshot.offset += len(args.Data)

	// If not done, wait for more chunks
	if !args.Done {
		return nil
	}

	// Complete snapshot received - install it
	return sm.installSnapshot(sm.incomingSnapshot)
}

// installSnapshot installs a complete snapshot
func (sm *SnapshotManager) installSnapshot(snapshot *incomingSnapshot) error {
	// Save snapshot to persistence
	snap := &Snapshot{
		Data:              snapshot.data,
		LastIncludedIndex: snapshot.lastIncludedIndex,
		LastIncludedTerm:  snapshot.lastIncludedTerm,
	}

	if sm.persistence != nil {
		if err := sm.persistence.SaveSnapshot(snap); err != nil {
			return fmt.Errorf("failed to save snapshot: %v", err)
		}
	}

	// Restore state machine from snapshot
	if err := sm.stateMachine.Restore(snapshot.data); err != nil {
		return fmt.Errorf("failed to restore state machine: %v", err)
	}

	// Update log manager
	sm.logManager.InstallSnapshot(snapshot.lastIncludedIndex, snapshot.lastIncludedTerm)

	// Update state
	sm.lastSnapshotIndex = snapshot.lastIncludedIndex
	sm.lastSnapshotTerm = snapshot.lastIncludedTerm

	// Clear incoming snapshot
	sm.incomingSnapshot = nil

	if sm.config.Logger != nil {
		sm.config.Logger.Info("Installed snapshot at index %d, term %d",
			snapshot.lastIncludedIndex, snapshot.lastIncludedTerm)
	}

	return nil
}

// GetLastSnapshot returns the last snapshot index and term
func (sm *SnapshotManager) GetLastSnapshot() (int, int) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	return sm.lastSnapshotIndex, sm.lastSnapshotTerm
}

// LoadSnapshot loads the latest snapshot from persistence
func (sm *SnapshotManager) LoadSnapshot() error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if sm.persistence == nil || !sm.persistence.HasSnapshot() {
		return nil
	}

	snapshot, err := sm.persistence.LoadSnapshot()
	if err != nil {
		return fmt.Errorf("failed to load snapshot: %v", err)
	}

	if snapshot == nil {
		return nil
	}

	// Restore state machine
	if err := sm.stateMachine.Restore(snapshot.Data); err != nil {
		return fmt.Errorf("failed to restore state machine: %v", err)
	}

	// Update state
	sm.lastSnapshotIndex = snapshot.LastIncludedIndex
	sm.lastSnapshotTerm = snapshot.LastIncludedTerm

	if sm.config.Logger != nil {
		sm.config.Logger.Info("Loaded snapshot at index %d, term %d",
			snapshot.LastIncludedIndex, snapshot.LastIncludedTerm)
	}

	return nil
}

// GetLatestSnapshot returns the latest snapshot for sending to followers
func (sm *SnapshotManager) GetLatestSnapshot() (*Snapshot, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if sm.persistence == nil || !sm.persistence.HasSnapshot() {
		return nil, fmt.Errorf("no snapshot available")
	}

	return sm.persistence.LoadSnapshot()
}
