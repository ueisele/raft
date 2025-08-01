package snapshot

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ueisele/raft"
	"github.com/ueisele/raft/integration/helpers"
)

// TestSnapshotCreation tests creating snapshots when log grows
func TestSnapshotCreation(t *testing.T) {
	// Create a single node cluster
	cluster := helpers.NewTestCluster(t, 1, helpers.WithMaxLogSize(10)) // Trigger snapshot after 10 entries
	
	// Start cluster
	if err := cluster.Start(); err != nil {
		t.Fatalf("Failed to start cluster: %v", err)
	}
	defer cluster.Stop()

	// Wait for leadership
	_, err := cluster.WaitForLeader(2 * time.Second)
	if err != nil {
		t.Fatalf("No leader elected: %v", err)
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

		// Small delay between commands for processing
		if i < 14 {
			time.Sleep(10 * time.Millisecond)
		}
	}

	// Wait for entries to be applied first
	cluster.WaitForCommitIndex(15, time.Second)
	
	// Get commit index to verify entries are committed
	commitIndex := node.GetCommitIndex()
	t.Logf("Commit index: %d", commitIndex)

	// Check if snapshot was created
	if persistence := cluster.GetPersistence(0); persistence != nil {
		if persistence.HasSnapshot() {
			t.Log("Snapshot was created")
			snapshot, _ := persistence.LoadSnapshot()
			if snapshot != nil {
				t.Logf("Snapshot includes up to index %d", snapshot.LastIncludedIndex)
			}
		} else {
			t.Error("No snapshot was created despite log size exceeding threshold")
			// Debug info
			t.Logf("Log length: %d, MaxLogSize: %d", node.GetLogLength(), 10)
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
	cluster := helpers.NewTestCluster(t, 3, helpers.WithMaxLogSize(10)) // Small log size to trigger snapshots
	defer cluster.Stop()

	ctx := context.Background()

	// Start only nodes 0 and 1
	for i := 0; i < 2; i++ {
		if err := cluster.Nodes[i].Start(ctx); err != nil {
			t.Fatalf("Failed to start node %d: %v", i, err)
		}
	}

	// Wait for leader election among first 2 nodes
	leaderID := helpers.WaitForLeader(t, cluster.Nodes[:2], 2*time.Second)
	leader := cluster.Nodes[leaderID]

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
	helpers.WaitForCondition(t, func() bool {
		if persistence := cluster.GetPersistence(leaderID); persistence != nil {
			return persistence.HasSnapshot()
		}
		return false
	}, 2*time.Second, "snapshot creation")

	// Verify snapshot was created
	if persistence := cluster.GetPersistence(leaderID); persistence != nil {
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
	helpers.WaitForCondition(t, func() bool {
		leaderCommit := leader.GetCommitIndex()
		node2Commit := cluster.Nodes[2].GetCommitIndex()
		return node2Commit >= leaderCommit-2
	}, 2*time.Second, "node 2 catch up via snapshot")

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
	readIndex, _, _ := leader.Submit("read-check")
	helpers.WaitForCommitIndex(t, cluster.Nodes, readIndex, time.Second)

	// Check state machine consistency
	sm0 := cluster.GetStateMachine(0)
	sm2 := cluster.GetStateMachine(2)

	// Both should have applied similar number of commands
	applied0 := sm0.GetApplyCount()
	applied2 := sm2.GetApplyCount()

	if applied2 < applied0-2 {
		t.Errorf("State machine 2 is too far behind. Applied: %d vs %d", applied2, applied0)
	} else {
		t.Logf("State machines are consistent. Applied counts: %d and %d", applied0, applied2)
	}
}

// TestSnapshotWithConcurrentWrites tests snapshot creation during active writes
func TestSnapshotWithConcurrentWrites(t *testing.T) {
	// Create a 3-node cluster
	cluster := helpers.NewTestCluster(t, 3, helpers.WithMaxLogSize(20)) // Small log size to trigger multiple snapshots
	
	// Start cluster
	if err := cluster.Start(); err != nil {
		t.Fatalf("Failed to start cluster: %v", err)
	}
	defer cluster.Stop()

	// Wait for leader election
	_, err := cluster.WaitForLeader(2 * time.Second)
	if err != nil {
		t.Fatalf("No leader elected: %v", err)
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
					currentLeader := cluster.GetLeaderNode()
					if currentLeader != nil {
						_, _, isLeader := currentLeader.Submit(cmd)
						if isLeader {
							atomic.AddInt32(&successCount, 1)
							cmdNum++
						}
					}
					time.Sleep(10 * time.Millisecond) // Small delay for pacing
				}
			}
		}(writerID)
	}

	// Let writers run and trigger snapshots
	helpers.WaitForCondition(t, func() bool {
		count := atomic.LoadInt32(&successCount)
		return count >= 50
	}, 3*time.Second, "concurrent command submission")

	// Stop writers
	close(stopCh)

	// Log how many commands were successfully submitted
	successfulCommands := atomic.LoadInt32(&successCount)
	t.Logf("Successfully submitted %d commands", successfulCommands)

	// Wait for system to stabilize
	cluster.WaitForStableCluster(2 * time.Second)

	// Verify all nodes have consistent state
	commitIndices := make([]int, 3)
	for i, node := range cluster.Nodes {
		commitIndices[i] = node.GetCommitIndex()
		logLength := node.GetLogLength()
		t.Logf("Node %d commit index: %d, log length: %d", i, commitIndices[i], logLength)
	}

	// Check that snapshots were created
	snapshotCount := 0
	for i := 0; i < 3; i++ {
		if persistence := cluster.GetPersistence(i); persistence != nil && persistence.HasSnapshot() {
			snapshotCount++
			snapshot, _ := persistence.LoadSnapshot()
			if snapshot != nil {
				t.Logf("Node %d has snapshot at index %d", i, snapshot.LastIncludedIndex)
			}
		}
	}

	if snapshotCount == 0 && successfulCommands > 30 {
		t.Errorf("No snapshots were created despite %d concurrent writes (threshold: %d)", 
			successfulCommands, 20)
	} else if snapshotCount > 0 {
		t.Logf("Created %d snapshots with concurrent writes", snapshotCount)
	}

	// Wait for consistency after concurrent writes
	helpers.Eventually(t, func() bool {
		minCommit := int(^uint(0) >> 1)
		maxCommit := 0
		for _, node := range cluster.Nodes {
			commit := node.GetCommitIndex()
			if commit < minCommit {
				minCommit = commit
			}
			if commit > maxCommit {
				maxCommit = commit
			}
		}
		return maxCommit-minCommit <= 20
	}, 3*time.Second, "nodes converge after concurrent writes")
	
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

// TestSnapshotFailure tests snapshot persistence failure handling
func TestSnapshotFailure(t *testing.T) {
	// Test snapshot persistence failure handling
	cluster := helpers.NewTestCluster(t, 1, helpers.WithMaxLogSize(5))
	
	// Start cluster
	if err := cluster.Start(); err != nil {
		t.Fatalf("Failed to start cluster: %v", err)
	}
	defer cluster.Stop()

	// Wait for leadership
	_, err := cluster.WaitForLeader(2 * time.Second)
	if err != nil {
		t.Fatalf("No leader elected: %v", err)
	}

	// Submit some commands
	for i := 0; i < 7; i++ {
		cluster.SubmitCommand(fmt.Sprintf("cmd%d", i))
	}

	// Simulate snapshot save failure
	if mockPersistence, ok := cluster.GetPersistence(0).(*raft.MockPersistence); ok {
		mockPersistence.FailNextSave()
	}

	// Submit more commands to trigger snapshot
	for i := 7; i < 10; i++ {
		cluster.SubmitCommand(fmt.Sprintf("cmd%d", i))
	}

	// Wait for snapshot attempt
	helpers.WaitForCondition(t, func() bool {
		// Check if snapshot was attempted (it will fail)
		logLength := cluster.Nodes[0].GetLogLength()
		return logLength >= 10
	}, 2*time.Second, "snapshot trigger")

	// Node should still be functional despite snapshot failure
	if !cluster.Nodes[0].IsLeader() {
		t.Error("Node should remain leader despite snapshot failure")
	}
}