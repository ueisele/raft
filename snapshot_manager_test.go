package raft

import (
	"fmt"
	"testing"
)

// TestEmptySnapshot tests creating snapshot with empty state machine
func TestEmptySnapshot(t *testing.T) {
	// Test creating snapshot with empty state machine
	sm := NewMockStateMachine()

	data, err := sm.Snapshot()
	if err != nil {
		t.Fatalf("Failed to create empty snapshot: %v", err)
	}

	// Restore empty snapshot
	sm2 := NewMockStateMachine()

	err = sm2.Restore(data)
	if err != nil {
		t.Fatalf("Failed to restore empty snapshot: %v", err)
	}
}

// TestLargeSnapshot tests with large state machine
func TestLargeSnapshot(t *testing.T) {
	// Test with large state machine
	sm := NewMockStateMachine()

	// Add many entries by applying commands
	for i := 0; i < 1000; i++ {
		cmd := fmt.Sprintf("SET key-%d value-%d-with-some-extra-data-to-make-it-larger", i, i)
		entry := LogEntry{
			Term:    1,
			Index:   i + 1,
			Command: cmd,
		}
		sm.Apply(entry)
	}

	data, err := sm.Snapshot()
	if err != nil {
		t.Fatalf("Failed to create large snapshot: %v", err)
	}

	t.Logf("Large snapshot size: %d bytes", len(data))

	// Restore large snapshot
	sm2 := NewMockStateMachine()

	err = sm2.Restore(data)
	if err != nil {
		t.Fatalf("Failed to restore large snapshot: %v", err)
	}

	// Verify restore cleared the state
	// MockStateMachine just clears state on restore
	if sm2.GetApplyCount() != 0 {
		t.Errorf("Expected 0 entries after restore (MockStateMachine clears on restore), got %d", sm2.GetApplyCount())
	}
}

// TestSnapshotManagerBasics tests basic snapshot manager functionality
func TestSnapshotManagerBasics(t *testing.T) {
	sm := NewMockStateMachine()
	persistence := NewMockPersistence()

	// Apply some entries
	for i := 0; i < 10; i++ {
		entry := LogEntry{
			Term:    1,
			Index:   i + 1,
			Command: fmt.Sprintf("SET key%d value%d", i, i),
		}
		sm.Apply(entry)
	}

	// Create snapshot
	data, err := sm.Snapshot()
	if err != nil {
		t.Fatalf("Failed to create snapshot: %v", err)
	}

	// Save snapshot
	snapshot := &Snapshot{
		LastIncludedIndex: 10,
		LastIncludedTerm:  1,
		Data:              data,
	}

	err = persistence.SaveSnapshot(snapshot)
	if err != nil {
		t.Fatalf("Failed to save snapshot: %v", err)
	}

	// Load snapshot
	loaded, err := persistence.LoadSnapshot()
	if err != nil {
		t.Fatalf("Failed to load snapshot: %v", err)
	}

	if loaded == nil {
		t.Fatal("Loaded snapshot is nil")
	}

	if loaded.LastIncludedIndex != snapshot.LastIncludedIndex {
		t.Errorf("Snapshot index mismatch: expected %d, got %d",
			snapshot.LastIncludedIndex, loaded.LastIncludedIndex)
	}

	if loaded.LastIncludedTerm != snapshot.LastIncludedTerm {
		t.Errorf("Snapshot term mismatch: expected %d, got %d",
			snapshot.LastIncludedTerm, loaded.LastIncludedTerm)
	}
}
