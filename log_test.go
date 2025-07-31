package raft

import (
	"testing"
)

// TestLogManagerAppend tests log append operations
func TestLogManagerAppend(t *testing.T) {
	lm := NewLogManager()

	// Test initial state
	if lm.GetLastLogIndex() != 0 {
		t.Errorf("Initial last log index should be 0, got %d", lm.GetLastLogIndex())
	}
	if lm.GetLastLogTerm() != 0 {
		t.Errorf("Initial last log term should be 0, got %d", lm.GetLastLogTerm())
	}

	// Test appending entries
	entries := []LogEntry{
		{Term: 1, Index: 1, Command: "cmd1"},
		{Term: 1, Index: 2, Command: "cmd2"},
		{Term: 2, Index: 3, Command: "cmd3"},
	}

	err := lm.AppendEntries(0, 0, entries)
	if err != nil {
		t.Errorf("AppendEntries failed: %v", err)
	}

	if lm.GetLastLogIndex() != 3 {
		t.Errorf("Last log index should be 3, got %d", lm.GetLastLogIndex())
	}
	if lm.GetLastLogTerm() != 2 {
		t.Errorf("Last log term should be 2, got %d", lm.GetLastLogTerm())
	}
}

// TestLogManagerGet tests retrieving log entries
func TestLogManagerGet(t *testing.T) {
	lm := NewLogManager()

	// Add some entries
	entries := []LogEntry{
		{Term: 1, Index: 1, Command: "cmd1"},
		{Term: 1, Index: 2, Command: "cmd2"},
		{Term: 2, Index: 3, Command: "cmd3"},
	}
	lm.AppendEntries(0, 0, entries)

	// Test GetEntry
	entry := lm.GetEntry(2)
	if entry == nil {
		t.Error("Entry at index 2 should exist")
	} else if entry.Index != 2 || entry.Term != 1 || entry.Command != "cmd2" {
		t.Errorf("Got wrong entry: %+v", entry)
	}

	// Test GetEntry with invalid index
	entry0 := lm.GetEntry(0)
	if entry0 != nil {
		t.Error("Entry at index 0 should not exist")
	}
	entry4 := lm.GetEntry(4)
	if entry4 != nil {
		t.Error("Entry at index 4 should not exist")
	}

	// Test GetEntries - GetEntries uses half-open interval [start, end)
	retrieved := lm.GetEntries(2, 4)
	if len(retrieved) != 2 {
		t.Errorf("Expected 2 entries, got %d", len(retrieved))
	}
	if len(retrieved) == 2 && (retrieved[0].Index != 2 || retrieved[1].Index != 3) {
		t.Error("Got wrong entries")
	}

	// Test GetEntries with invalid range
	retrieved = lm.GetEntries(4, 5)
	if len(retrieved) != 0 {
		t.Error("Should get empty slice for invalid range")
	}
}

// TestLogManagerMatch tests log matching
func TestLogManagerMatch(t *testing.T) {
	lm := NewLogManager()

	// Add some entries
	entries := []LogEntry{
		{Term: 1, Index: 1, Command: "cmd1"},
		{Term: 1, Index: 2, Command: "cmd2"},
		{Term: 2, Index: 3, Command: "cmd3"},
	}
	lm.AppendEntries(0, 0, entries)

	// Test successful match
	if !lm.MatchEntry(2, 1) {
		t.Error("Should match entry at index 2 with term 1")
	}

	// Test match with wrong term
	if lm.MatchEntry(2, 2) {
		t.Error("Should not match entry at index 2 with term 2")
	}

	// Test match at index 0 (always matches)
	if !lm.MatchEntry(0, 0) {
		t.Error("Should always match at index 0")
	}

	// Test match beyond log
	if lm.MatchEntry(4, 2) {
		t.Error("Should not match beyond log")
	}
}

// TestLogManagerTruncate tests log truncation
func TestLogManagerTruncate(t *testing.T) {
	lm := NewLogManager()

	// Add some entries
	entries := []LogEntry{
		{Term: 1, Index: 1, Command: "cmd1"},
		{Term: 1, Index: 2, Command: "cmd2"},
		{Term: 2, Index: 3, Command: "cmd3"},
		{Term: 2, Index: 4, Command: "cmd4"},
	}
	lm.AppendEntries(0, 0, entries)

	// Truncate after index 2
	lm.TruncateAfter(2)

	if lm.GetLastLogIndex() != 2 {
		t.Errorf("Last log index should be 2 after truncation, got %d", lm.GetLastLogIndex())
	}

	// Verify entries after index 2 are gone
	entry3 := lm.GetEntry(3)
	if entry3 != nil {
		t.Error("Entry at index 3 should not exist after truncation")
	}

	// Verify entries before truncation point still exist
	entry2 := lm.GetEntry(2)
	if entry2 == nil || entry2.Command != "cmd2" {
		t.Error("Entry at index 2 should still exist")
	}
}

// TestLogManagerCommitIndex tests commit index management
func TestLogManagerCommitIndex(t *testing.T) {
	lm := NewLogManager()

	// Add some entries
	entries := []LogEntry{
		{Term: 1, Index: 1, Command: "cmd1"},
		{Term: 1, Index: 2, Command: "cmd2"},
		{Term: 2, Index: 3, Command: "cmd3"},
	}
	lm.AppendEntries(0, 0, entries)

	// Test initial commit index
	if lm.GetCommitIndex() != 0 {
		t.Errorf("Initial commit index should be 0, got %d", lm.GetCommitIndex())
	}

	// Test updating commit index
	lm.SetCommitIndex(2)
	if lm.GetCommitIndex() != 2 {
		t.Errorf("Commit index should be 2, got %d", lm.GetCommitIndex())
	}

	// Test commit index cannot go backwards
	lm.SetCommitIndex(1)
	if lm.GetCommitIndex() != 2 {
		t.Errorf("Commit index should still be 2, got %d", lm.GetCommitIndex())
	}

	// Test commit index cannot exceed log length
	lm.SetCommitIndex(5)
	if lm.GetCommitIndex() != 3 {
		t.Errorf("Commit index should be capped at 3, got %d", lm.GetCommitIndex())
	}
}

// TestLogManagerSnapshot tests snapshot-related operations
func TestLogManagerSnapshot(t *testing.T) {
	lm := NewLogManager()

	// Add some entries
	entries := []LogEntry{
		{Term: 1, Index: 1, Command: "cmd1"},
		{Term: 1, Index: 2, Command: "cmd2"},
		{Term: 2, Index: 3, Command: "cmd3"},
		{Term: 2, Index: 4, Command: "cmd4"},
	}
	lm.AppendEntries(0, 0, entries)
	lm.SetCommitIndex(3)

	// Create snapshot at index 2
	lm.CreateSnapshot(2, 1)

	// Verify we can still get entries after snapshot
	entry := lm.GetEntry(3)
	if entry == nil || entry.Command != "cmd3" {
		t.Error("Should be able to get entry at index 3 after snapshot")
	}

	// Verify we cannot get snapshotted entries
	entry = lm.GetEntry(1)
	if entry != nil {
		t.Error("Should not be able to get snapshotted entry at index 1")
	}
}

// TestLogManagerReplaceEntries tests replacing conflicting entries
func TestLogManagerReplaceEntries(t *testing.T) {
	lm := NewLogManager()

	// Add initial entries
	entries := []LogEntry{
		{Term: 1, Index: 1, Command: "cmd1"},
		{Term: 1, Index: 2, Command: "cmd2"},
		{Term: 1, Index: 3, Command: "cmd3"},
	}
	lm.AppendEntries(0, 0, entries)

	// Replace entries starting from index 2 with different terms
	newEntries := []LogEntry{
		{Term: 2, Index: 2, Command: "new_cmd2"},
		{Term: 2, Index: 3, Command: "new_cmd3"},
		{Term: 3, Index: 4, Command: "new_cmd4"},
	}

	// Truncate after index 1 and append new entries
	lm.TruncateAfter(1)
	lm.AppendEntries(1, 1, newEntries)

	// Verify the log state
	if lm.GetLastLogIndex() != 4 {
		t.Errorf("Last log index should be 4, got %d", lm.GetLastLogIndex())
	}

	// Verify old entry at index 1 is preserved
	entry := lm.GetEntry(1)
	if entry == nil || entry.Command != "cmd1" {
		t.Error("Entry at index 1 should be preserved")
	}

	// Verify new entries replaced old ones
	entry = lm.GetEntry(2)
	if entry == nil || entry.Command != "new_cmd2" || entry.Term != 2 {
		t.Error("Entry at index 2 should be replaced")
	}

	entry = lm.GetEntry(4)
	if entry == nil || entry.Command != "new_cmd4" || entry.Term != 3 {
		t.Error("New entry at index 4 should be added")
	}
}
