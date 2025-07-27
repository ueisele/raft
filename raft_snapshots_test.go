package raft

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"
)

// MockSnapshotData represents a simple key-value store for testing
type MockSnapshotData struct {
	Data map[string]string
}

// TestBasicSnapshot tests basic snapshotting functionality
func TestBasicSnapshot(t *testing.T) {
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
	leaderIdx := WaitForLeader(t, rafts, 2*time.Second)
	leader := rafts[leaderIdx].Raft

	// Submit some commands
	commands := []string{"cmd1", "cmd2", "cmd3", "cmd4", "cmd5"}
	for _, cmd := range commands {
		leader.Submit(cmd)
	}

	// Wait for commands to be committed
	WaitForCommitIndex(t, rafts, len(commands), 5*time.Second)

	// Create a snapshot at index 3
	snapshotData := []byte("snapshot at index 3")
	err := leader.TakeSnapshot(snapshotData, 3)
	if err != nil {
		t.Fatalf("Failed to create snapshot: %v", err)
	}

	// Verify snapshot was created
	leader.mu.RLock()
	if leader.lastSnapshotIndex != 3 {
		t.Errorf("Expected lastSnapshotIndex to be 3, got %d", leader.lastSnapshotIndex)
	}
	
	// Verify log was trimmed
	logLen := len(leader.log)
	leader.mu.RUnlock()
	
	if logLen > 3 {
		t.Errorf("Log should be trimmed after snapshot, but has %d entries", logLen)
	}

	// Submit more commands
	idx6, _, _ := leader.Submit("cmd6")
	idx7, _, _ := leader.Submit("cmd7")
	
	t.Logf("Submitted cmd6 at index %d, cmd7 at index %d", idx6, idx7)
	
	// Check current state
	leader.mu.RLock()
	t.Logf("Leader lastLogIndex: %d, commitIndex: %d", leader.getLastLogIndex(), leader.commitIndex)
	leader.mu.RUnlock()

	// Wait for new commands to be committed
	WaitForCommitIndex(t, rafts, 7, 5*time.Second)

	// Stop all instances
	for i := 0; i < 3; i++ {
		rafts[i].Stop()
	}
}

// TestSnapshotInstallation tests installing snapshots on followers
func TestSnapshotInstallation(t *testing.T) {
	peers := []int{0, 1, 2}
	rafts := make([]*TestRaft, 3)
	applyChannels := make([]chan LogEntry, 3)
	transport := NewTestTransport()

	// Create and start only 2 servers initially
	for i := 0; i < 2; i++ {
		applyChannels[i] = make(chan LogEntry, 100)
		rafts[i] = NewTestRaft(peers, i, applyChannels[i], transport)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start the 2 servers
	for i := 0; i < 2; i++ {
		rafts[i].Start(ctx)
	}

	// Wait for leader election
	leaderIdx := WaitForLeader(t, rafts[:2], 2*time.Second)
	leader := rafts[leaderIdx].Raft

	// Submit many commands
	for i := 1; i <= 20; i++ {
		leader.Submit(i)
	}

	// Wait for commands to be committed on active servers
	WaitForCommitIndex(t, rafts[:2], 20, 5*time.Second)

	// Create a snapshot at index 15
	snapshotData := []byte("snapshot at index 15")
	err := leader.TakeSnapshot(snapshotData, 15)
	if err != nil {
		t.Fatalf("Failed to create snapshot: %v", err)
	}

	// Now create and start the third server
	applyChannels[2] = make(chan LogEntry, 100)
	rafts[2] = NewTestRaft(peers, 2, applyChannels[2], transport)
	rafts[2].Start(ctx)

	// Wait for the new server to catch up via snapshot
	time.Sleep(2 * time.Second)
	
	// Check leader's view of follower
	leader.mu.RLock()
	t.Logf("Leader nextIndex[2]: %d, lastLogIndex: %d, lastSnapshotIndex: %d", 
		leader.nextIndex[2], leader.getLastLogIndex(), leader.lastSnapshotIndex)
	leader.mu.RUnlock()

	// Check if server 2 received the snapshot
	rafts[2].mu.RLock()
	t.Logf("Server 2 lastSnapshotIndex: %d, commitIndex: %d, lastApplied: %d",
		rafts[2].lastSnapshotIndex, rafts[2].commitIndex, rafts[2].lastApplied)
	if rafts[2].lastSnapshotIndex != 15 {
		t.Errorf("Server 2 should have received snapshot at index 15, got %d", rafts[2].lastSnapshotIndex)
	}
	rafts[2].mu.RUnlock()

	// Submit more commands and verify all servers stay in sync
	leader.Submit(21)
	leader.Submit(22)

	WaitForCommitIndex(t, rafts, 22, 5*time.Second)

	// Stop all instances
	for i := 0; i < 3; i++ {
		rafts[i].Stop()
	}
}

// TestSnapshotDuringPartition tests snapshot behavior during network partitions
func TestSnapshotDuringPartition(t *testing.T) {
	peers := []int{0, 1, 2, 3, 4}
	rafts := make([]*TestRaft, 5)
	applyChannels := make([]chan LogEntry, 5)
	transport := NewTestTransport()

	for i := 0; i < 5; i++ {
		applyChannels[i] = make(chan LogEntry, 100)
		rafts[i] = NewTestRaft(peers, i, applyChannels[i], transport)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	for i := 0; i < 5; i++ {
		rafts[i].Start(ctx)
	}

	// Start goroutines to consume from apply channels
	stopConsumers := make(chan struct{})
	for i := 0; i < 5; i++ {
		go func(ch chan LogEntry) {
			for {
				select {
				case <-ch:
					// Just consume
				case <-stopConsumers:
					return
				}
			}
		}(applyChannels[i])
	}
	defer close(stopConsumers)

	// Wait for leader election
	leaderIdx := WaitForLeader(t, rafts, 2*time.Second)
	leader := rafts[leaderIdx].Raft
	t.Logf("Initial leader is server %d", leaderIdx)

	// Submit initial commands
	for i := 1; i <= 10; i++ {
		leader.Submit(i)
	}

	WaitForCommitIndex(t, rafts, 10, 5*time.Second)

	// Partition the network: disconnect servers 3 and 4
	transport.DisconnectServer(3)
	transport.DisconnectServer(4)
	
	// If the leader was in the minority partition, we need a new leader
	if leaderIdx == 3 || leaderIdx == 4 {
		t.Log("Leader was in minority partition, waiting for new leader in majority")
		// Give a moment for the partition to take effect
		time.Sleep(100 * time.Millisecond)
		
		// Find new leader among majority servers
		majorityRafts := []*TestRaft{rafts[0], rafts[1], rafts[2]}
		newLeaderIdx := -1
		for attempts := 0; attempts < 50; attempts++ {
			for i, rf := range majorityRafts {
				_, isLeader := rf.GetState()
				if isLeader {
					newLeaderIdx = i
					break
				}
			}
			if newLeaderIdx != -1 {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		
		if newLeaderIdx == -1 {
			t.Fatal("No new leader elected in majority partition")
		}
		leader = majorityRafts[newLeaderIdx].Raft
		t.Logf("New leader in majority partition is server %d", rafts[newLeaderIdx].me)
	}

	// Create snapshot on majority partition
	snapshotData := []byte("snapshot during partition")
	err := leader.TakeSnapshot(snapshotData, 8)
	if err != nil {
		t.Fatalf("Failed to create snapshot: %v", err)
	}

	// Submit more commands to majority
	for i := 11; i <= 15; i++ {
		leader.Submit(i)
	}

	// Wait for majority to commit
	majorityRafts := []*TestRaft{rafts[0], rafts[1], rafts[2]}
	WaitForCommitIndex(t, majorityRafts, 15, 5*time.Second)

	// Reconnect minority servers
	transport.ReconnectServer(3)
	transport.ReconnectServer(4)

	// Log state before waiting
	for i, rf := range rafts {
		rf.mu.RLock()
		t.Logf("Before catch-up - Server %d: commitIndex=%d, lastApplied=%d, lastSnapshotIndex=%d, log length=%d",
			i, rf.commitIndex, rf.lastApplied, rf.lastSnapshotIndex, len(rf.log))
		rf.mu.RUnlock()
	}

	// Wait for all servers to catch up (may take longer after partition)
	WaitForCommitIndex(t, rafts, 15, 20*time.Second)
	
	// Log final state
	for i, rf := range rafts {
		rf.mu.RLock()
		t.Logf("After catch-up - Server %d: commitIndex=%d, lastApplied=%d, lastSnapshotIndex=%d, log length=%d",
			i, rf.commitIndex, rf.lastApplied, rf.lastSnapshotIndex, len(rf.log))
		rf.mu.RUnlock()
	}

	// Stop all instances
	for i := 0; i < 5; i++ {
		rafts[i].Stop()
	}
}

// TestSnapshotPersistence tests that snapshots survive server restarts
func TestSnapshotPersistence(t *testing.T) {
	// Create temporary directory for test data
	tempDir := fmt.Sprintf("/tmp/raft-test-%d", time.Now().UnixNano())
	err := os.MkdirAll(tempDir, 0755)
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	peers := []int{0, 1, 2}
	rafts := make([]*TestRaft, 3)
	applyChannels := make([]chan LogEntry, 3)
	transport := NewTestTransport()
	persisters := make([]*Persister, 3)

	// Create servers with persisters
	for i := 0; i < 3; i++ {
		applyChannels[i] = make(chan LogEntry, 100)
		rafts[i] = NewTestRaft(peers, i, applyChannels[i], transport)
		
		// Create persister for each server
		serverDir := fmt.Sprintf("%s/server-%d", tempDir, i)
		err := os.MkdirAll(serverDir, 0755)
		if err != nil {
			t.Fatalf("Failed to create server dir: %v", err)
		}
		persisters[i] = NewPersister(serverDir, i)
		rafts[i].SetPersister(persisters[i])
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start servers
	for i := 0; i < 3; i++ {
		rafts[i].Start(ctx)
	}

	// Wait for leader election
	leaderIdx := WaitForLeader(t, rafts, 2*time.Second)
	leader := rafts[leaderIdx].Raft

	// Submit commands
	commands := []string{"cmd1", "cmd2", "cmd3", "cmd4", "cmd5"}
	for _, cmd := range commands {
		leader.Submit(cmd)
	}

	// Wait for replication
	WaitForCommitIndex(t, rafts, len(commands), 3*time.Second)

	// Create snapshots on all servers
	snapshotData := []byte("test-snapshot-data")
	for i, rf := range rafts {
		err := rf.TakeSnapshot(snapshotData, 3) // Snapshot up to index 3
		if err != nil {
			t.Errorf("Server %d failed to take snapshot: %v", i, err)
		}
	}

	// Give time for snapshots to be written
	time.Sleep(500 * time.Millisecond)

	// Stop all servers
	for i := 0; i < 3; i++ {
		rafts[i].Stop()
	}

	// Wait a bit to ensure clean shutdown
	time.Sleep(500 * time.Millisecond)

	// Create new servers with same persisters
	newRafts := make([]*TestRaft, 3)
	newApplyChannels := make([]chan LogEntry, 3)

	for i := 0; i < 3; i++ {
		newApplyChannels[i] = make(chan LogEntry, 100)
		newRafts[i] = NewTestRaft(peers, i, newApplyChannels[i], transport)
		newRafts[i].SetPersister(persisters[i])
	}

	// Start new servers
	newCtx, newCancel := context.WithCancel(context.Background())
	defer newCancel()

	for i := 0; i < 3; i++ {
		newRafts[i].Start(newCtx)
	}

	// Wait for new leader
	newLeaderIdx := WaitForLeader(t, newRafts, 2*time.Second)

	// Verify state was restored from snapshot
	for i, rf := range newRafts {
		rf.mu.RLock()
		// Check that snapshot was loaded
		if rf.lastSnapshotIndex != 3 {
			t.Errorf("Server %d: Expected lastSnapshotIndex=3, got %d", i, rf.lastSnapshotIndex)
		}
		// Check that lastApplied is at least the snapshot index
		if rf.lastApplied < 3 {
			t.Errorf("Server %d: Expected lastApplied>=3, got %d", i, rf.lastApplied)
		}
		// Check that log was trimmed properly
		if len(rf.log) > 0 && rf.log[0].Index != 0 && rf.log[0].Index < 3 {
			t.Errorf("Server %d: Log should be trimmed up to snapshot index", i)
		}
		rf.mu.RUnlock()
	}

	// Submit new commands to verify cluster is functional
	newLeader := newRafts[newLeaderIdx].Raft
	newLeader.Submit("cmd6")
	newLeader.Submit("cmd7")

	// Wait for new commands to be replicated
	WaitForCommitIndex(t, newRafts, 7, 3*time.Second)

	// Stop new servers
	for i := 0; i < 3; i++ {
		newRafts[i].Stop()
	}
}

// TestConcurrentSnapshots tests handling of concurrent snapshot operations
func TestConcurrentSnapshots(t *testing.T) {
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
	leaderIdx := WaitForLeader(t, rafts, 2*time.Second)
	leader := rafts[leaderIdx].Raft

	// Submit commands
	for i := 1; i <= 20; i++ {
		leader.Submit(i)
	}

	WaitForCommitIndex(t, rafts, 20, 5*time.Second)

	// Try to create multiple snapshots at different indices
	err1 := leader.TakeSnapshot([]byte("snapshot at 10"), 10)
	if err1 != nil {
		t.Fatalf("Failed to create first snapshot: %v", err1)
	}

	// Second snapshot should succeed and replace the first
	err2 := leader.TakeSnapshot([]byte("snapshot at 15"), 15)
	if err2 != nil {
		t.Fatalf("Failed to create second snapshot: %v", err2)
	}

	// Try to create snapshot at earlier index (should fail)
	err3 := leader.TakeSnapshot([]byte("snapshot at 5"), 5)
	if err3 == nil {
		t.Error("Creating snapshot at earlier index should fail")
	}

	// Verify final snapshot state
	leader.mu.RLock()
	if leader.lastSnapshotIndex != 15 {
		t.Errorf("Expected lastSnapshotIndex to be 15, got %d", leader.lastSnapshotIndex)
	}
	leader.mu.RUnlock()

	// Stop all instances
	for i := 0; i < 3; i++ {
		rafts[i].Stop()
	}
}

// TestSnapshotWithConfigChange tests a simple scenario of taking snapshots with configuration changes
func TestSnapshotWithConfigChange(t *testing.T) {
	// Start with 3 servers
	peers := []int{0, 1, 2}
	rafts := make([]*TestRaft, 4) // Room for 4 servers
	applyChannels := make([]chan LogEntry, 4)
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

	// Start goroutines to consume from apply channels
	stopConsumers := make(chan struct{})
	defer close(stopConsumers)
	
	for i := 0; i < 3; i++ {
		go func(ch chan LogEntry) {
			for {
				select {
				case <-ch:
					// Just consume
				case <-stopConsumers:
					return
				}
			}
		}(applyChannels[i])
	}

	// Wait for leader election
	leaderIdx := WaitForLeader(t, rafts[:3], 2*time.Second)
	leader := rafts[leaderIdx].Raft

	// Submit some commands
	for i := 1; i <= 10; i++ {
		leader.Submit(fmt.Sprintf("cmd%d", i))
	}

	// Wait for replication
	WaitForCommitIndex(t, rafts[:3], 10, 3*time.Second)

	// Take a snapshot
	snapshotData := []byte("snapshot at index 8")
	err := leader.TakeSnapshot(snapshotData, 8)
	if err != nil {
		t.Fatalf("Failed to take snapshot: %v", err)
	}

	// Add a new server
	newServerID := 3
	applyChannels[newServerID] = make(chan LogEntry, 100)
	rafts[newServerID] = NewTestRaft([]int{newServerID}, newServerID, applyChannels[newServerID], transport)
	rafts[newServerID].Start(ctx)
	
	// Start consumer for new server
	go func(ch chan LogEntry) {
		for {
			select {
			case <-ch:
				// Just consume
			case <-stopConsumers:
				return
			}
		}
	}(applyChannels[newServerID])

	// Add the server to configuration
	err = leader.AddServer(ServerConfiguration{
		ID:      newServerID,
		Address: fmt.Sprintf("localhost:800%d", newServerID),
	})
	if err != nil {
		t.Fatalf("Failed to add server: %v", err)
	}

	// Wait for new server to catch up
	time.Sleep(2 * time.Second)

	// Verify new server received snapshot
	rafts[newServerID].mu.RLock()
	if rafts[newServerID].lastSnapshotIndex < 8 {
		t.Errorf("New server should have received snapshot, lastSnapshotIndex=%d", 
			rafts[newServerID].lastSnapshotIndex)
	}
	rafts[newServerID].mu.RUnlock()

	// Stop all servers
	for i := 0; i < 4; i++ {
		if rafts[i] != nil {
			rafts[i].Stop()
		}
	}
}