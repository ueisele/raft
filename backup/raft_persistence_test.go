package raft

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// TestBasicPersistence tests that state is persisted and recovered correctly
func TestBasicPersistence(t *testing.T) {
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
	rf.currentTerm = 5
	votedFor := 1
	rf.votedFor = &votedFor
	rf.log = append(rf.log, LogEntry{Term: 5, Index: 1, Command: "test-cmd"})
	rf.persist()
	rf.mu.Unlock()

	// Wait for persistence
	WaitForPersistence(t, persister, func(term int, vf *int, log []LogEntry) bool {
		return term == 5 && vf != nil && *vf == 1 && len(log) == 2
	}, 1*time.Second)

	// Simulate crash and recovery
	newRf := SimulateCrashAndRecover(t, rf, persister, peers, applyCh)
	newRf.Start(ctx)
	defer newRf.Stop()

	// Verify state was recovered
	VerifyPersistentState(t, persister, 5, &votedFor, 2)

	// Also check the in-memory state
	newRf.mu.RLock()
	if newRf.currentTerm != 5 {
		t.Errorf("Expected term 5 after recovery, got %d", newRf.currentTerm)
	}
	if newRf.votedFor == nil || *newRf.votedFor != 1 {
		t.Errorf("Expected votedFor 1 after recovery, got %v", newRf.votedFor)
	}
	if len(newRf.log) != 2 || newRf.log[1].Command != "test-cmd" {
		t.Errorf("Expected log to be recovered correctly")
	}
	newRf.mu.RUnlock()
}

// TestPersistenceAcrossElections tests that election state is persisted
func TestPersistenceAcrossElections(t *testing.T) {
	peers := []int{0, 1, 2}
	rafts := make([]*TestRaft, 3)
	persisters := make([]*Persister, 3)
	cleanups := make([]func(), 3)
	applyChannels := make([]chan LogEntry, 3)

	// Create all servers with persistence
	for i := 0; i < 3; i++ {
		applyChannels[i] = make(chan LogEntry, 100)
		rafts[i], persisters[i], cleanups[i] = CreateTestRaftWithPersistence(t, peers, i, applyChannels[i])
		defer cleanups[i]()
	}

	// Set up transport connections
	transport := rafts[0].transport
	for i := 1; i < 3; i++ {
		rafts[i].transport = transport
		transport.RegisterServer(i, rafts[i].Raft)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	for i := 0; i < 3; i++ {
		rafts[i].Start(ctx)
	}

	// Wait for leader election
	leaderIdx := WaitForLeader(t, rafts, 2*time.Second)
	leader := rafts[leaderIdx].Raft

	// Submit some commands
	for i := 0; i < 5; i++ {
		leader.Submit(fmt.Sprintf("cmd%d", i))
	}

	WaitForCommitIndex(t, rafts, 5, 2*time.Second)

	// Get current term
	currentTerm, _ := leader.GetState()

	// Crash and recover all servers
	for i := 0; i < 3; i++ {
		rafts[i].Stop()
	}

	// Wait a bit
	time.Sleep(100 * time.Millisecond)

	// Recover all servers
	newRafts := make([]*TestRaft, 3)
	for i := 0; i < 3; i++ {
		newRafts[i] = SimulateCrashAndRecover(t, rafts[i], persisters[i], peers, applyChannels[i])
		newRafts[i].Start(ctx)
	}

	// Re-establish transport connections
	for i := 0; i < 3; i++ {
		newRafts[i].transport = transport
		transport.RegisterServer(i, newRafts[i].Raft)
	}

	// They should be able to elect a leader without issues
	newLeaderIdx := WaitForLeader(t, newRafts, 3*time.Second)
	newLeader := newRafts[newLeaderIdx].Raft

	// New term should be at least as high as before
	newTerm, _ := newLeader.GetState()
	if newTerm < currentTerm {
		t.Errorf("Term went backwards after recovery: %d -> %d", currentTerm, newTerm)
	}

	// Verify logs were persisted correctly
	// Note: commitIndex is volatile and will be 0 after recovery until
	// the leader re-establishes what's committed through AppendEntries
	for i := 0; i < 3; i++ {
		newRafts[i].mu.RLock()
		if len(newRafts[i].log) < 6 { // 1 dummy + 5 commands
			t.Errorf("Server %d lost log entries after recovery: log length=%d", i, len(newRafts[i].log))
		}
		newRafts[i].mu.RUnlock()
	}

	// Submit new commands to verify system is working
	for i := 0; i < 3; i++ {
		newLeader.Submit(fmt.Sprintf("new-cmd%d", i))
	}

	WaitForCommitIndex(t, newRafts, 8, 2*time.Second)

	// Stop all servers
	for i := 0; i < 3; i++ {
		newRafts[i].Stop()
	}
}

// TestPersistenceWithPartialClusterFailure tests persistence when only some nodes fail
func TestPersistenceWithPartialClusterFailure(t *testing.T) {
	peers := []int{0, 1, 2, 3, 4}
	rafts := make([]*TestRaft, 5)
	persisters := make([]*Persister, 5)
	cleanups := make([]func(), 5)
	applyChannels := make([]chan LogEntry, 5)

	// Create all servers with persistence
	for i := 0; i < 5; i++ {
		applyChannels[i] = make(chan LogEntry, 100)
		rafts[i], persisters[i], cleanups[i] = CreateTestRaftWithPersistence(t, peers, i, applyChannels[i])
		defer cleanups[i]()
	}

	// Set up transport connections
	transport := rafts[0].transport
	for i := 1; i < 5; i++ {
		rafts[i].transport = transport
		transport.RegisterServer(i, rafts[i].Raft)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	for i := 0; i < 5; i++ {
		rafts[i].Start(ctx)
	}

	// Wait for leader election
	leaderIdx := WaitForLeader(t, rafts, 2*time.Second)
	leader := rafts[leaderIdx].Raft

	// Submit some commands
	for i := 0; i < 10; i++ {
		leader.Submit(fmt.Sprintf("cmd%d", i))
	}

	WaitForCommitIndex(t, rafts, 10, 2*time.Second)

	// Crash servers 0 and 1
	rafts[0].Stop()
	rafts[1].Stop()

	// Remaining servers should still work
	remainingRafts := []*TestRaft{rafts[2], rafts[3], rafts[4]}

	// Wait for new leader among remaining servers
	newLeaderIdx := WaitForLeader(t, remainingRafts, 2*time.Second)
	newLeader := remainingRafts[newLeaderIdx].Raft

	// Submit more commands
	for i := 0; i < 5; i++ {
		newLeader.Submit(fmt.Sprintf("cmd-partial%d", i))
	}

	// Should commit with majority (3 out of 5)
	WaitForCommitIndex(t, remainingRafts, 15, 2*time.Second)

	// Now recover crashed servers
	for i := 0; i < 2; i++ {
		newRaft := SimulateCrashAndRecover(t, rafts[i], persisters[i], peers, applyChannels[i])
		newRaft.transport = transport
		transport.RegisterServer(i, newRaft.Raft)
		newRaft.Start(ctx)
		rafts[i] = newRaft
	}

	// They should catch up
	time.Sleep(1 * time.Second)
	WaitForCommitIndex(t, rafts, 15, 3*time.Second)

	// Verify all servers have the same log
	for i := 1; i < 5; i++ {
		rafts[0].mu.RLock()
		rafts[i].mu.RLock()
		if len(rafts[0].log) != len(rafts[i].log) {
			rafts[0].mu.RUnlock()
			rafts[i].mu.RUnlock()
			t.Errorf("Server 0 and server %d have different log lengths after recovery", i)
			continue
		}
		rafts[0].mu.RUnlock()
		rafts[i].mu.RUnlock()
	}

	// Stop all servers
	for i := 0; i < 5; i++ {
		rafts[i].Stop()
	}
}

// TestSnapshotPersistenceAndRecovery tests snapshot persistence
func TestSnapshotPersistenceAndRecovery(t *testing.T) {
	peers := []int{0, 1, 2}
	applyCh := make(chan LogEntry, 100)

	// Create Raft with persistence
	rf, persister, cleanup := CreateTestRaftWithPersistence(t, peers, 0, applyCh)
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	rf.Start(ctx)

	// Create some log entries
	rf.mu.Lock()
	rf.currentTerm = 3
	for i := 1; i <= 10; i++ {
		rf.log = append(rf.log, LogEntry{
			Term:    3,
			Index:   i,
			Command: fmt.Sprintf("cmd%d", i),
		})
	}
	rf.commitIndex = 10
	rf.lastApplied = 10
	rf.persist()
	rf.mu.Unlock()

	// Create and save a snapshot
	snapshotData := CreateSnapshotData([]LogEntry{
		{Term: 1, Index: 1, Command: "cmd1"},
		{Term: 2, Index: 2, Command: "cmd2"},
		{Term: 3, Index: 10, Command: "cmd10"},
	})

	err := persister.SaveSnapshot(snapshotData, 10, 3)
	if err != nil {
		t.Fatalf("Failed to save snapshot: %v", err)
	}

	// Verify snapshot was saved
	VerifySnapshot(t, persister, 10, 3)

	// Simulate crash and recovery
	rf.Stop()
	time.Sleep(100 * time.Millisecond)

	// Create new Raft instance with same persister
	newRf := NewRaft(peers, 0, applyCh)
	newRf.SetPersister(persister)

	// Verify snapshot was loaded
	newRf.mu.RLock()
	if newRf.lastSnapshotIndex != 10 {
		t.Errorf("Expected lastSnapshotIndex 10, got %d", newRf.lastSnapshotIndex)
	}
	if newRf.lastSnapshotTerm != 3 {
		t.Errorf("Expected lastSnapshotTerm 3, got %d", newRf.lastSnapshotTerm)
	}
	if newRf.lastApplied < 10 {
		t.Errorf("Expected lastApplied >= 10, got %d", newRf.lastApplied)
	}
	if newRf.commitIndex < 10 {
		t.Errorf("Expected commitIndex >= 10, got %d", newRf.commitIndex)
	}
	newRf.mu.RUnlock()
}

// TestPersistenceStressTest tests persistence under heavy load
func TestPersistenceStressTest(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping stress test in short mode")
	}

	peers := []int{0, 1, 2, 3, 4}
	rafts := make([]*TestRaft, 5)
	persisters := make([]*Persister, 5)
	cleanups := make([]func(), 5)
	applyChannels := make([]chan LogEntry, 5)

	// Create all servers with persistence
	for i := 0; i < 5; i++ {
		applyChannels[i] = make(chan LogEntry, 1000)
		rafts[i], persisters[i], cleanups[i] = CreateTestRaftWithPersistence(t, peers, i, applyChannels[i])
		defer cleanups[i]()
	}

	// Set up transport connections
	transport := rafts[0].transport
	for i := 1; i < 5; i++ {
		rafts[i].transport = transport
		transport.RegisterServer(i, rafts[i].Raft)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start consumers
	stopConsumers := make(chan struct{})
	for i := 0; i < 5; i++ {
		go func(ch chan LogEntry) {
			for {
				select {
				case <-ch:
				case <-stopConsumers:
					return
				}
			}
		}(applyChannels[i])
	}
	defer close(stopConsumers)

	for i := 0; i < 5; i++ {
		rafts[i].Start(ctx)
	}

	// Submit many commands while randomly crashing and recovering servers
	commandCount := 0
	crashCount := 0

	for round := 0; round < 10; round++ {
		// Find current leader
		var leader *Raft
		for _, rf := range rafts {
			if rf.Raft != nil {
				_, isLeader := rf.GetState()
				if isLeader {
					leader = rf.Raft
					break
				}
			}
		}

		if leader != nil {
			// Submit some commands
			for i := 0; i < 10; i++ {
				leader.Submit(fmt.Sprintf("round%d-cmd%d", round, i))
				commandCount++
			}
		}

		// Randomly crash a server (but keep majority alive)
		if round%3 == 0 && crashCount < 2 {
			victimIdx := round % 5
			if rafts[victimIdx].Raft != nil {
				rafts[victimIdx].Stop()
				crashCount++

				// Recover after a delay
				go func(idx int) {
					time.Sleep(500 * time.Millisecond)
					newRaft := SimulateCrashAndRecover(t, rafts[idx], persisters[idx], peers, applyChannels[idx])
					newRaft.transport = transport
					transport.RegisterServer(idx, newRaft.Raft)
					newRaft.Start(ctx)
					rafts[idx] = newRaft
					crashCount--
				}(victimIdx)
			}
		}

		time.Sleep(200 * time.Millisecond)
	}

	// Wait for everything to stabilize
	time.Sleep(2 * time.Second)

	// Find final leader
	var finalLeader *Raft
	for _, rf := range rafts {
		if rf.Raft != nil {
			_, isLeader := rf.GetState()
			if isLeader {
				finalLeader = rf.Raft
				break
			}
		}
	}

	if finalLeader == nil {
		t.Fatal("No leader after stress test")
	}

	// Verify some commands were committed
	finalLeader.mu.RLock()
	finalCommitIndex := finalLeader.commitIndex
	finalLeader.mu.RUnlock()

	if finalCommitIndex < commandCount/2 {
		t.Errorf("Too few commands committed: %d out of %d", finalCommitIndex, commandCount)
	}

	t.Logf("Stress test completed: %d commands submitted, %d committed", commandCount, finalCommitIndex)

	// Stop all servers
	for i := 0; i < 5; i++ {
		if rafts[i] != nil && rafts[i].Raft != nil {
			rafts[i].Stop()
		}
	}
}
