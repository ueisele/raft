package raft

import (
	"context"
	"os"
	"testing"
	"time"
)

// Test basic Raft functionality
func TestBasicRaft(t *testing.T) {
	// Create a 3-node cluster
	peers := []int{0, 1, 2}
	rafts := make([]*TestRaft, 3)
	applyChannels := make([]chan LogEntry, 3)
	transport := NewTestTransport()

	// Initialize Raft instances
	for i := 0; i < 3; i++ {
		applyChannels[i] = make(chan LogEntry, 100)
		rafts[i] = NewTestRaft(peers, i, applyChannels[i], transport)
	}

	// Start all Raft instances
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	for i := 0; i < 3; i++ {
		rafts[i].Start(ctx)
	}

	// Wait for leader election
	leaderIndex := WaitForLeader(t, rafts, 2*time.Second)
	leader := rafts[leaderIndex].Raft

	t.Logf("Leader elected: server %d", leaderIndex)

	// Submit a command
	command := "test-command"
	index, term, isLeader := leader.Submit(command)
	if !isLeader {
		t.Fatal("Leader lost leadership")
	}

	t.Logf("Command submitted: index=%d, term=%d", index, term)

	// Wait for command to be applied
	applied := WaitForAppliedEntries(t, applyChannels[leaderIndex], 1, 1*time.Second)
	if applied[0].Command != command {
		t.Fatalf("Applied command mismatch: got %v, want %v", applied[0].Command, command)
	}

	// Stop all Raft instances
	for i := 0; i < 3; i++ {
		rafts[i].Stop()
	}
}

// Test leader election
func TestLeaderElection(t *testing.T) {
	peers := []int{0, 1, 2}
	rafts := make([]*TestRaft, 3)
	applyChannels := make([]chan LogEntry, 3)
	transport := NewTestTransport()

	for i := 0; i < 3; i++ {
		applyChannels[i] = make(chan LogEntry, 100)
		rafts[i] = NewTestRaft(peers, i, applyChannels[i], transport)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	for i := 0; i < 3; i++ {
		rafts[i].Start(ctx)
	}

	// Wait for leader election
	leaderIndex := WaitForLeader(t, rafts, 2*time.Second)
	t.Logf("Server %d is leader", leaderIndex)

	// Verify only one leader
	leaderCount := 0
	for _, rf := range rafts {
		_, isLeader := rf.GetState()
		if isLeader {
			leaderCount++
		}
	}

	if leaderCount != 1 {
		t.Fatalf("Expected exactly 1 leader, got %d", leaderCount)
	}

	// Stop all instances
	for i := 0; i < 3; i++ {
		rafts[i].Stop()
	}
}

// Test log replication
func TestLogReplication(t *testing.T) {
	peers := []int{0, 1, 2}
	rafts := make([]*TestRaft, 3)
	applyChannels := make([]chan LogEntry, 3)
	transport := NewTestTransport()

	for i := 0; i < 3; i++ {
		applyChannels[i] = make(chan LogEntry, 100)
		rafts[i] = NewTestRaft(peers, i, applyChannels[i], transport)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	for i := 0; i < 3; i++ {
		rafts[i].Start(ctx)
	}

	// Wait for leader election
	leaderIndex := WaitForLeader(t, rafts, 2*time.Second)
	leader := rafts[leaderIndex].Raft

	// Submit multiple commands
	commands := []string{"cmd1", "cmd2", "cmd3"}
	for _, cmd := range commands {
		_, _, isLeader := leader.Submit(cmd)
		if !isLeader {
			t.Fatal("Leader lost leadership")
		}
	}

	// Wait for replication
	WaitForCommitIndex(t, rafts, len(commands), 2*time.Second)

	// Check that all servers have the same log length
	expectedLogLength := len(commands) + 1 // +1 for dummy entry
	for i, rf := range rafts {
		rf.mu.RLock()
		logLength := len(rf.log)
		rf.mu.RUnlock()

		if logLength != expectedLogLength {
			t.Errorf("Server %d has log length %d, expected %d", i, logLength, expectedLogLength)
		}
	}

	// Stop all instances
	for i := 0; i < 3; i++ {
		rafts[i].Stop()
	}
}

// Test persistence
func TestPersistence(t *testing.T) {
	peers := []int{0, 1, 2}
	applyCh := make(chan LogEntry, 100)

	// Create a temporary directory for testing
	dataDir := "/tmp/raft-test"
	persister := NewPersister(dataDir, 0)

	// Clean up any existing state files first
	os.Remove("/tmp/raft-test/raft-state-0.json")
	persister.DeleteSnapshot()

	// Create Raft instance
	rf := NewRaft(peers, 0, applyCh)
	rf.SetPersister(persister)

	// Modify some state
	rf.mu.Lock()
	rf.currentTerm = 5
	rf.votedFor = &peers[1]
	rf.log = append(rf.log, LogEntry{Term: 5, Index: 1, Command: "test"})
	rf.persistWithPersister()
	rf.mu.Unlock()

	// Create new instance and check if state is restored
	rf2 := NewRaft(peers, 0, applyCh)
	rf2.SetPersister(persister)

	rf2.mu.RLock()
	if rf2.currentTerm != 5 {
		t.Errorf("Expected currentTerm=5, got %d", rf2.currentTerm)
	}
	if rf2.votedFor == nil || *rf2.votedFor != peers[1] {
		t.Errorf("Expected votedFor=%d, got %v", peers[1], rf2.votedFor)
	}
	expectedLogLength := 2 // dummy entry + test entry
	if len(rf2.log) != expectedLogLength {
		t.Errorf("Expected log length=%d, got %d", expectedLogLength, len(rf2.log))
	}
	rf2.mu.RUnlock()

	// Clean up
	os.Remove("/tmp/raft-test/raft-state-0.json")
	persister.DeleteSnapshot()
}

// Benchmark leader election
func BenchmarkLeaderElection(b *testing.B) {
	for i := 0; i < b.N; i++ {
		peers := []int{0, 1, 2}
		rafts := make([]*TestRaft, 3)
		applyChannels := make([]chan LogEntry, 3)
		transport := NewTestTransport()

		for j := 0; j < 3; j++ {
			applyChannels[j] = make(chan LogEntry, 100)
			rafts[j] = NewTestRaft(peers, j, applyChannels[j], transport)
		}

		ctx, cancel := context.WithCancel(context.Background())

		for j := 0; j < 3; j++ {
			rafts[j].Start(ctx)
		}

		// Wait for leader election
		WaitForLeader(b, rafts, 1*time.Second)

		cancel()
		for j := 0; j < 3; j++ {
			rafts[j].Stop()
		}
	}
}
