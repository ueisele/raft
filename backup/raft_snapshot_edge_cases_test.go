package raft

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// TestSnapshotDuringLeadershipChange tests taking snapshots during leadership transitions
func TestSnapshotDuringLeadershipChange(t *testing.T) {
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

	// Wait for leader
	leaderIdx := WaitForLeader(t, rafts, 2*time.Second)
	leader := rafts[leaderIdx].Raft

	// Submit commands
	for i := 1; i <= 10; i++ {
		leader.Submit(fmt.Sprintf("cmd%d", i))
	}

	WaitForCommitIndex(t, rafts, 10, 2*time.Second)

	// Start taking snapshot
	snapshotDone := make(chan error, 1)
	go func() {
		err := leader.TakeSnapshot([]byte("snapshot data"), 8)
		snapshotDone <- err
	}()

	// Immediately cause leadership change by isolating leader
	time.Sleep(50 * time.Millisecond)
	transport.DisconnectServer(leaderIdx)

	// Wait for new leader
	remainingRafts := make([]*TestRaft, 0)
	for i, rf := range rafts {
		if i != leaderIdx {
			remainingRafts = append(remainingRafts, rf)
		}
	}

	newLeaderIdx := WaitForLeader(t, remainingRafts, 2*time.Second)
	if newLeaderIdx != -1 {
		t.Logf("New leader elected: server %d", remainingRafts[newLeaderIdx].me)
	}

	// Check snapshot result
	select {
	case err := <-snapshotDone:
		if err != nil {
			t.Logf("Snapshot failed during leadership change (expected): %v", err)
		} else {
			t.Log("Snapshot succeeded despite leadership change")
		}
	case <-time.After(2 * time.Second):
		t.Log("Snapshot operation timed out")
	}

	// Reconnect old leader
	transport.ReconnectServer(leaderIdx)

	// Verify system is still functional
	finalLeaderIdx := WaitForLeader(t, rafts, 2*time.Second)
	finalLeader := rafts[finalLeaderIdx].Raft
	finalLeader.Submit("test-after-snapshot")

	// Stop all servers
	for i := 0; i < 3; i++ {
		rafts[i].Stop()
	}
}

// TestSnapshotOfSnapshotIndex tests edge case of taking snapshot at the snapshot index
func TestSnapshotOfSnapshotIndex(t *testing.T) {
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

	// Wait for leader
	leaderIdx := WaitForLeader(t, rafts, 2*time.Second)
	leader := rafts[leaderIdx].Raft

	// Submit commands
	for i := 1; i <= 10; i++ {
		leader.Submit(i)
	}

	WaitForCommitIndex(t, rafts, 10, 2*time.Second)

	// Take first snapshot
	err := leader.TakeSnapshot([]byte("first snapshot"), 5)
	if err != nil {
		t.Fatalf("Failed to take first snapshot: %v", err)
	}

	// Try to take another snapshot at the same index
	err = leader.TakeSnapshot([]byte("second snapshot at same index"), 5)
	if err == nil {
		t.Error("Taking snapshot at same index should fail")
	}

	// Try to take snapshot at earlier index
	err = leader.TakeSnapshot([]byte("earlier snapshot"), 3)
	if err == nil {
		t.Error("Taking snapshot at earlier index should fail")
	}

	// Take valid snapshot at later index
	err = leader.TakeSnapshot([]byte("valid later snapshot"), 8)
	if err != nil {
		t.Errorf("Failed to take valid snapshot at later index: %v", err)
	}

	// Verify final snapshot state
	leader.mu.RLock()
	if leader.lastSnapshotIndex != 8 {
		t.Errorf("Expected lastSnapshotIndex=8, got %d", leader.lastSnapshotIndex)
	}
	leader.mu.RUnlock()

	// Stop all servers
	for i := 0; i < 3; i++ {
		rafts[i].Stop()
	}
}

// TestConcurrentSnapshotAndReplication tests concurrent snapshot and log replication
func TestConcurrentSnapshotAndReplication(t *testing.T) {
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

	// Start consumers
	stopConsumers := make(chan struct{})
	defer close(stopConsumers)
	for i := 0; i < 3; i++ {
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

	for i := 0; i < 3; i++ {
		rafts[i].Start(ctx)
	}

	// Wait for leader
	leaderIdx := WaitForLeader(t, rafts, 2*time.Second)
	leader := rafts[leaderIdx].Raft

	// Submit initial commands
	for i := 1; i <= 20; i++ {
		leader.Submit(i)
	}

	WaitForCommitIndex(t, rafts, 20, 3*time.Second)

	// Start continuous command submission
	stopSubmitting := make(chan struct{})
	go func() {
		i := 21
		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				leader.Submit(i)
				i++
			case <-stopSubmitting:
				return
			}
		}
	}()

	// Take snapshot while replication is ongoing
	time.Sleep(200 * time.Millisecond)
	err := leader.TakeSnapshot([]byte("concurrent snapshot"), 15)
	if err != nil {
		t.Errorf("Failed to take snapshot during replication: %v", err)
	}

	// Stop submitting
	close(stopSubmitting)
	time.Sleep(100 * time.Millisecond)

	// Verify system is still consistent
	leader.mu.RLock()
	leaderCommit := leader.commitIndex
	leader.mu.RUnlock()

	// All servers should eventually reach same commit index
	WaitForCommitIndex(t, rafts, leaderCommit, 2*time.Second)

	// Stop all servers
	for i := 0; i < 3; i++ {
		rafts[i].Stop()
	}
}

// TestSnapshotInstallationRaceConditions tests race conditions during snapshot installation
func TestSnapshotInstallationRaceConditions(t *testing.T) {
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

	// Start only servers 0 and 1
	for i := 0; i < 2; i++ {
		rafts[i].Start(ctx)
	}

	// Wait for leader
	leaderIdx := WaitForLeader(t, rafts[:2], 2*time.Second)
	leader := rafts[leaderIdx].Raft

	// Submit many commands
	for i := 1; i <= 30; i++ {
		leader.Submit(i)
	}

	WaitForCommitIndex(t, rafts[:2], 30, 3*time.Second)

	// Take snapshot
	err := leader.TakeSnapshot([]byte("race condition snapshot"), 25)
	if err != nil {
		t.Fatalf("Failed to take snapshot: %v", err)
	}

	// Start server 2 which will need snapshot
	rafts[2].Start(ctx)

	// Immediately submit more commands while snapshot is being sent
	go func() {
		for i := 31; i <= 40; i++ {
			leader.Submit(i)
			time.Sleep(10 * time.Millisecond)
		}
	}()

	// Wait for server 2 to catch up
	time.Sleep(2 * time.Second)

	// Verify server 2 received snapshot and is catching up
	rafts[2].mu.RLock()
	hasSnapshot := rafts[2].lastSnapshotIndex >= 25
	commitIndex := rafts[2].commitIndex
	rafts[2].mu.RUnlock()

	if !hasSnapshot && commitIndex < 25 {
		t.Error("Server 2 should have received snapshot or caught up past index 25")
	}

	t.Logf("Server 2 state: snapshot=%v, commitIndex=%d", hasSnapshot, commitIndex)

	// Stop all servers
	for i := 0; i < 3; i++ {
		rafts[i].Stop()
	}
}

// TestPersistenceWithRapidSnapshots tests persistence under rapid snapshot creation
func TestPersistenceWithRapidSnapshots(t *testing.T) {
	peers := []int{0, 1, 2}
	rafts := make([]*TestRaft, 3)
	persisters := make([]*Persister, 3)
	cleanups := make([]func(), 3)
	applyChannels := make([]chan LogEntry, 3)

	// Create servers with persistence
	for i := 0; i < 3; i++ {
		applyChannels[i] = make(chan LogEntry, 100)
		rafts[i], persisters[i], cleanups[i] = CreateTestRaftWithPersistence(t, peers, i, applyChannels[i])
		defer cleanups[i]()
	}

	// Set up transport
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

	// Wait for leader
	leaderIdx := WaitForLeader(t, rafts, 2*time.Second)
	leader := rafts[leaderIdx].Raft

	// Submit commands and take rapid snapshots
	for round := 0; round < 5; round++ {
		// Submit 10 commands
		for i := 1; i <= 10; i++ {
			leader.Submit(fmt.Sprintf("round%d-cmd%d", round, i))
		}

		expectedIndex := (round + 1) * 10
		WaitForCommitIndex(t, rafts, expectedIndex, 2*time.Second)

		// Take snapshot
		snapshotIndex := expectedIndex - 2
		err := leader.TakeSnapshot([]byte(fmt.Sprintf("snapshot-%d", round)), snapshotIndex)
		if err != nil {
			t.Errorf("Failed to take snapshot %d: %v", round, err)
		}

		// Verify snapshot was persisted
		if persisters[leaderIdx].HasSnapshot() {
			_, lastIndex, lastTerm, err := persisters[leaderIdx].LoadSnapshot()
			if err != nil {
				t.Errorf("Failed to load snapshot: %v", err)
			} else if lastIndex != snapshotIndex {
				t.Errorf("Snapshot index mismatch: expected %d, got %d", snapshotIndex, lastIndex)
			} else {
				t.Logf("Snapshot %d persisted correctly at index %d, term %d",
					round, lastIndex, lastTerm)
			}
		}
	}

	// Simulate crash and recovery
	for i := 0; i < 3; i++ {
		rafts[i].Stop()
	}

	time.Sleep(100 * time.Millisecond)

	// Recover all servers
	newRafts := make([]*TestRaft, 3)
	for i := 0; i < 3; i++ {
		// Check what's in the persister before recovery
		if persisters[i].HasSnapshot() {
			_, idx, term, _ := persisters[i].LoadSnapshot()
			t.Logf("Server %d has persisted snapshot at index %d, term %d", i, idx, term)
		}

		newRafts[i] = SimulateCrashAndRecover(t, rafts[i], persisters[i], peers, applyChannels[i])
		newRafts[i].Start(ctx)
		transport.RegisterServer(i, newRafts[i].Raft)
	}

	// Wait for election and recovery
	time.Sleep(1 * time.Second)

	// Verify recovery
	hasLeaderWithSnapshot := false
	for i := 0; i < 3; i++ {
		newRafts[i].mu.RLock()
		lastSnapshotIndex := newRafts[i].lastSnapshotIndex
		lastLogIndex := newRafts[i].getLastLogIndex()
		currentTerm := newRafts[i].currentTerm
		newRafts[i].mu.RUnlock()

		t.Logf("Server %d after recovery: lastSnapshotIndex=%d, lastLogIndex=%d, currentTerm=%d",
			i, lastSnapshotIndex, lastLogIndex, currentTerm)

		// At least one server (the old leader) should have recovered with a snapshot
		if lastSnapshotIndex >= 48 {
			hasLeaderWithSnapshot = true
		}
	}

	if !hasLeaderWithSnapshot {
		t.Error("No server recovered with the expected snapshot (index >= 48)")
	}

	// Stop all servers
	for i := 0; i < 3; i++ {
		newRafts[i].Stop()
	}
}

// TestSnapshotTransmissionFailure tests handling of snapshot transmission failures
func TestSnapshotTransmissionFailure(t *testing.T) {
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

	// Start only servers 0 and 1
	for i := 0; i < 2; i++ {
		rafts[i].Start(ctx)
	}

	// Wait for leader
	leaderIdx := WaitForLeader(t, rafts[:2], 2*time.Second)
	leader := rafts[leaderIdx].Raft

	// Submit commands
	for i := 1; i <= 20; i++ {
		leader.Submit(i)
	}

	WaitForCommitIndex(t, rafts[:2], 20, 2*time.Second)

	// Take snapshot
	err := leader.TakeSnapshot([]byte("snapshot for transmission failure test"), 15)
	if err != nil {
		t.Fatalf("Failed to take snapshot: %v", err)
	}

	// Start server 2
	rafts[2].Start(ctx)

	// Create intermittent connection issues during snapshot transmission
	go func() {
		for i := 0; i < 10; i++ {
			time.Sleep(100 * time.Millisecond)
			// Briefly disconnect server 2
			transport.DisconnectServer(2)
			time.Sleep(50 * time.Millisecond)
			transport.ReconnectServer(2)
		}
	}()

	// Continue submitting commands
	for i := 21; i <= 30; i++ {
		leader.Submit(i)
		time.Sleep(50 * time.Millisecond)
	}

	// Eventually server 2 should catch up despite transmission issues
	time.Sleep(3 * time.Second)

	// Check if server 2 eventually got the snapshot or caught up
	rafts[2].mu.RLock()
	hasSnapshot := rafts[2].lastSnapshotIndex > 0
	commitIndex := rafts[2].commitIndex
	rafts[2].mu.RUnlock()

	t.Logf("Server 2 final state: hasSnapshot=%v, commitIndex=%d", hasSnapshot, commitIndex)

	if !hasSnapshot && commitIndex < 15 {
		t.Error("Server 2 failed to receive snapshot or catch up")
	}

	// Stop all servers
	for i := 0; i < 3; i++ {
		rafts[i].Stop()
	}
}
