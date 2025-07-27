package raft

import (
	"context"
	"testing"
	"time"
)

// TestPersistenceHelpers tests the persistence helper functions
func TestPersistenceHelpers(t *testing.T) {
	// Test CreateTestPersister
	persister, cleanup := CreateTestPersister(t, 0)
	defer cleanup()
	
	// Test saving and loading state
	votedFor := 1
	err := persister.SaveState(5, &votedFor, []LogEntry{
		{Term: 0, Index: 0},
		{Term: 1, Index: 1, Command: "cmd1"},
	})
	if err != nil {
		t.Fatalf("Failed to save state: %v", err)
	}
	
	// Test VerifyPersistentState
	VerifyPersistentState(t, persister, 5, &votedFor, 2)
	
	// Test WaitForPersistence
	WaitForPersistence(t, persister, func(term int, vf *int, log []LogEntry) bool {
		return term == 5 && vf != nil && *vf == 1
	}, 1*time.Second)
	
	// Test snapshot helpers
	snapshotData := CreateSnapshotData([]LogEntry{
		{Term: 1, Index: 1, Command: "cmd1"},
		{Term: 1, Index: 2, Command: "cmd2"},
	})
	
	err = persister.SaveSnapshot(snapshotData, 2, 1)
	if err != nil {
		t.Fatalf("Failed to save snapshot: %v", err)
	}
	
	VerifySnapshot(t, persister, 2, 1)
}

// TestCrashRecoveryHelper tests the crash recovery helper
func TestCrashRecoveryHelper(t *testing.T) {
	peers := []int{0, 1, 2}
	applyCh := make(chan LogEntry, 100)
	
	// Create Raft with persistence
	rf, persister, cleanup := CreateTestRaftWithPersistence(t, peers, 0, applyCh)
	defer cleanup()
	
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	
	rf.Start(ctx)
	
	// Make some state changes
	rf.mu.Lock()
	rf.currentTerm = 3
	votedFor := 1
	rf.votedFor = &votedFor
	rf.log = append(rf.log, LogEntry{Term: 3, Index: 1, Command: "test"})
	rf.persist()
	rf.mu.Unlock()
	
	// Wait for persistence
	time.Sleep(50 * time.Millisecond)
	
	// Simulate crash and recovery
	newRf := SimulateCrashAndRecover(t, rf, persister, peers, applyCh)
	newRf.Start(ctx)
	defer newRf.Stop()
	
	// Verify state was recovered
	newRf.mu.RLock()
	if newRf.currentTerm != 3 {
		t.Errorf("Expected term 3 after recovery, got %d", newRf.currentTerm)
	}
	if newRf.votedFor == nil || *newRf.votedFor != 1 {
		t.Errorf("Expected votedFor 1 after recovery")
	}
	if len(newRf.log) != 2 {
		t.Errorf("Expected 2 log entries after recovery, got %d", len(newRf.log))
	}
	newRf.mu.RUnlock()
}