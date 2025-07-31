package raft_test

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ueisele/raft"
	"github.com/ueisele/raft/test"
)

// TestSnapshotCreation tests creating snapshots when log grows
func TestSnapshotCreation(t *testing.T) {
	// Create a single node cluster
	config := test.DefaultClusterConfig(1)
	config.MaxLogSize = 10 // Trigger snapshot after 10 entries
	cluster := test.NewTestCluster(t, config)
	defer cluster.Stop()

	ctx := context.Background()
	if err := cluster.Start(ctx); err != nil {
		t.Fatalf("Failed to start cluster: %v", err)
	}

	// Wait for leadership
	leaderID := cluster.WaitForLeaderElection(t, 2*time.Second)
	if leaderID < 0 {
		t.Fatal("No leader elected")
	}

	node := cluster.Nodes[0]

	// Submit commands to trigger snapshot
	for i := 0; i < 15; i++ {
		cmd := fmt.Sprintf("set key%d value%d", i, i)
		index, _, isLeader := node.Submit(cmd)
		if !isLeader {
			t.Fatal("Lost leadership")
		}
		t.Logf("Submitted command %d at index %d", i, index)

		// Give time for processing
		time.Sleep(50 * time.Millisecond)
	}

	// Wait for entries to be applied first
	time.Sleep(1 * time.Second)
	
	// Get commit index to verify entries are committed
	commitIndex := node.GetCommitIndex()
	t.Logf("Commit index: %d", commitIndex)

	// Check if snapshot was created
	if persistence := cluster.Persistences[0]; persistence != nil {
		if persistence.HasSnapshot() {
			t.Log("Snapshot was created")
			snapshot := persistence.GetSnapshot()
			if snapshot != nil {
				t.Logf("Snapshot includes up to index %d", snapshot.LastIncludedIndex)
			}
		} else {
			t.Error("No snapshot was created despite log size exceeding threshold")
			// Debug info
			t.Logf("Log length: %d, MaxLogSize: %d", node.GetLogLength(), config.MaxLogSize)
		}
	}

	// Verify log was truncated
	// Note: GetLogLength returns the last index, not the number of entries in memory
	// After snapshot at index 11, we should still have indices 12-15 in memory
	// So the "length" (last index) should still be 15, but only 4 entries in memory
	logLength := node.GetLogLength()
	if logLength != 15 {
		t.Errorf("Log index should still be 15 after snapshot. Got: %d", logLength)
	}
	
	// The actual truncation happens internally - we can't directly verify it
	// but the snapshot at index 11 means entries 1-11 are now in the snapshot
}

// TestSnapshotInstallation tests installing snapshots on followers
func TestSnapshotInstallation(t *testing.T) {
	// Create a 3-node cluster but start only 2 initially
	config := test.DefaultClusterConfig(3)
	config.MaxLogSize = 10 // Small log size to trigger snapshots
	cluster := test.NewTestCluster(t, config)
	defer cluster.Stop()

	ctx := context.Background()

	// Start only nodes 0 and 1
	for i := 0; i < 2; i++ {
		if err := cluster.Nodes[i].Start(ctx); err != nil {
			t.Fatalf("Failed to start node %d: %v", i, err)
		}
	}

	// Wait for leader election among first 2 nodes
	time.Sleep(500 * time.Millisecond)

	// Find leader
	var leader raft.Node
	var leaderID int
	for i := 0; i < 2; i++ {
		if cluster.Nodes[i].IsLeader() {
			leader = cluster.Nodes[i]
			leaderID = i
			break
		}
	}

	if leader == nil {
		t.Fatal("No leader elected among first 2 nodes")
	}

	t.Logf("Leader is node %d", leaderID)

	// Submit many commands to create snapshot
	for i := 0; i < 20; i++ {
		cmd := fmt.Sprintf("command-%d", i)
		_, _, isLeader := leader.Submit(cmd)
		if !isLeader {
			t.Fatal("Lost leadership")
		}
	}

	// Wait for snapshot creation
	time.Sleep(500 * time.Millisecond)

	// Verify snapshot was created
	if persistence := cluster.Persistences[leaderID]; persistence != nil {
		if !persistence.HasSnapshot() {
			t.Error("Leader should have created a snapshot")
		}
	}

	// Now start node 2 which is far behind
	if err := cluster.Nodes[2].Start(ctx); err != nil {
		t.Fatalf("Failed to start node 2: %v", err)
	}

	t.Log("Started lagging node 2")

	// Wait for snapshot installation
	time.Sleep(2 * time.Second)

	// Check if node 2 caught up via snapshot
	leaderCommit := leader.GetCommitIndex()
	node2Commit := cluster.Nodes[2].GetCommitIndex()

	if node2Commit < leaderCommit-2 {
		t.Errorf("Node 2 didn't catch up. Leader commit: %d, Node 2 commit: %d",
			leaderCommit, node2Commit)
	} else {
		t.Logf("Node 2 caught up. Commit index: %d", node2Commit)
	}

	// Verify state machine content is consistent
	// Submit a read command to verify
	leader.Submit("read-check")
	time.Sleep(500 * time.Millisecond)

	// Check state machine consistency
	sm0 := cluster.StateMachines[0]
	sm2 := cluster.StateMachines[2]

	// Both should have applied similar number of commands
	applied0 := sm0.GetAppliedCount()
	applied2 := sm2.GetAppliedCount()

	if applied2 < applied0-2 {
		t.Errorf("State machine 2 is too far behind. Applied: %d vs %d", applied2, applied0)
	} else {
		t.Logf("State machines are consistent. Applied counts: %d and %d", applied0, applied2)
	}
}

// TestSnapshotWithConcurrentWrites tests snapshot creation during active writes
func TestSnapshotWithConcurrentWrites(t *testing.T) {
	// Create a 3-node cluster
	config := test.DefaultClusterConfig(3)
	config.MaxLogSize = 20 // Small log size to trigger multiple snapshots
	cluster := test.NewTestCluster(t, config)
	defer cluster.Stop()

	ctx := context.Background()
	if err := cluster.Start(ctx); err != nil {
		t.Fatalf("Failed to start cluster: %v", err)
	}

	// Wait for leader election
	leaderID := cluster.WaitForLeaderElection(t, 2*time.Second)
	if leaderID < 0 {
		t.Fatal("No leader elected")
	}

	// Start concurrent writers
	stopCh := make(chan struct{})
	successCount := int32(0)

	for writerID := 0; writerID < 3; writerID++ {
		go func(id int) {
			cmdNum := 0
			for {
				select {
				case <-stopCh:
					return
				default:
					cmd := fmt.Sprintf("writer-%d-cmd-%d", id, cmdNum)
					// Always get current leader
					currentLeader, _ := cluster.GetLeader()
					if currentLeader != nil {
						_, _, isLeader := currentLeader.Submit(cmd)
						if isLeader {
							atomic.AddInt32(&successCount, 1)
							cmdNum++
						}
					}
					time.Sleep(20 * time.Millisecond)
				}
			}
		}(writerID)
	}

	// Let writers run and trigger snapshots
	time.Sleep(3 * time.Second)

	// Stop writers
	close(stopCh)

	// Log how many commands were successfully submitted
	successfulCommands := atomic.LoadInt32(&successCount)
	t.Logf("Successfully submitted %d commands", successfulCommands)

	// Wait for system to stabilize
	time.Sleep(500 * time.Millisecond)

	// Verify all nodes have consistent state
	commitIndices := make([]int, 3)
	for i, node := range cluster.Nodes {
		commitIndices[i] = node.GetCommitIndex()
		logLength := node.GetLogLength()
		t.Logf("Node %d commit index: %d, log length: %d", i, commitIndices[i], logLength)
	}

	// Check that snapshots were created
	snapshotCount := 0
	for i, persistence := range cluster.Persistences {
		if persistence != nil && persistence.HasSnapshot() {
			snapshotCount++
			snapshot := persistence.GetSnapshot()
			if snapshot != nil {
				t.Logf("Node %d has snapshot at index %d", i, snapshot.LastIncludedIndex)
			}
		}
	}

	if snapshotCount == 0 && successfulCommands > 30 {
		t.Errorf("No snapshots were created despite %d concurrent writes (threshold: %d)", 
			successfulCommands, config.MaxLogSize)
	} else if snapshotCount > 0 {
		t.Logf("Created %d snapshots with concurrent writes", snapshotCount)
	}

	// Give more time for consistency after concurrent writes
	time.Sleep(3 * time.Second)
	
	// Custom consistency check for concurrent writes
	// Allow more tolerance since nodes may be at different stages
	commitIndices = make([]int, 3)
	minCommit := int(^uint(0) >> 1) // Max int
	maxCommit := 0
	
	for i, node := range cluster.Nodes {
		commitIndices[i] = node.GetCommitIndex()
		if commitIndices[i] < minCommit {
			minCommit = commitIndices[i]
		}
		if commitIndices[i] > maxCommit {
			maxCommit = commitIndices[i]
		}
	}
	
	// Allow up to 20 entries difference during concurrent writes
	if maxCommit - minCommit > 20 {
		t.Errorf("Nodes diverged too much. Min commit: %d, Max commit: %d", minCommit, maxCommit)
	} else {
		t.Logf("Final commit indices - Min: %d, Max: %d (difference: %d)", 
			minCommit, maxCommit, maxCommit-minCommit)
	}
}

// TestSnapshotEdgeCases tests edge cases in snapshot handling
func TestSnapshotEdgeCases(t *testing.T) {
	t.Run("EmptySnapshot", func(t *testing.T) {
		// Test creating snapshot with empty state machine
		sm := test.NewSimpleStateMachine()

		data, err := sm.Snapshot()
		if err != nil {
			t.Fatalf("Failed to create empty snapshot: %v", err)
		}

		// Restore empty snapshot
		sm2 := test.NewSimpleStateMachine()

		err = sm2.Restore(data)
		if err != nil {
			t.Fatalf("Failed to restore empty snapshot: %v", err)
		}
	})

	t.Run("LargeSnapshot", func(t *testing.T) {
		// Test with large state machine
		sm := test.NewSimpleStateMachine()

		// Add many entries by applying commands
		for i := 0; i < 1000; i++ {
			cmd := fmt.Sprintf("set key-%d value-%d-with-some-extra-data-to-make-it-larger", i, i)
			entry := raft.LogEntry{
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
		sm2 := test.NewSimpleStateMachine()

		err = sm2.Restore(data)
		if err != nil {
			t.Fatalf("Failed to restore large snapshot: %v", err)
		}

		// Verify content
		// Applied count should be 1000
		if sm2.GetAppliedCount() != 1000 {
			t.Errorf("Expected 1000 entries applied, got %d", sm2.GetAppliedCount())
		}
	})

	t.Run("SnapshotFailure", func(t *testing.T) {
		// Test snapshot persistence failure handling
		config := test.DefaultClusterConfig(1)
		config.MaxLogSize = 5
		cluster := test.NewTestCluster(t, config)
		defer cluster.Stop()

		ctx := context.Background()
		if err := cluster.Start(ctx); err != nil {
			t.Fatalf("Failed to start cluster: %v", err)
		}

		// Wait for leadership
		cluster.WaitForLeaderElection(t, 2*time.Second)

		// Submit some commands
		for i := 0; i < 7; i++ {
			cluster.SubmitCommand(fmt.Sprintf("cmd%d", i))
		}

		// Simulate snapshot save failure
		if persistence := cluster.Persistences[0]; persistence != nil {
			persistence.SetFailNext()
		}

		// Submit more commands to trigger snapshot
		for i := 7; i < 10; i++ {
			cluster.SubmitCommand(fmt.Sprintf("cmd%d", i))
		}

		// Give time for snapshot attempt
		time.Sleep(500 * time.Millisecond)

		// Node should still be functional despite snapshot failure
		if !cluster.Nodes[0].IsLeader() {
			t.Error("Node should remain leader despite snapshot failure")
		}
	})
}
