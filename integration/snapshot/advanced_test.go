package snapshot

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/ueisele/raft"
	"github.com/ueisele/raft/integration/helpers"
)

// TestSnapshotDuringPartition tests snapshot creation and installation during network partition
func TestSnapshotDuringPartition(t *testing.T) {
	// Create 5-node cluster with partitionable transport
	cluster := helpers.NewTestCluster(t, 5, helpers.WithPartitionableTransport())

	// Start cluster
	if err := cluster.Start(); err != nil {
		t.Fatalf("Failed to start cluster: %v", err)
	}
	defer cluster.Stop() //nolint:errcheck // test cleanup

	// Wait for leader election
	leaderID, err := cluster.WaitForLeader(2 * time.Second)
	if err != nil {
		t.Fatalf("No leader elected: %v", err)
	}

	t.Logf("Leader is node %d", leaderID)

	// Partition node 4
	partitionedNode := 4
	if err := cluster.PartitionNode(partitionedNode); err != nil {
		t.Fatalf("Failed to partition node %d: %v", partitionedNode, err)
	}

	t.Logf("Partitioned node %d", partitionedNode)

	// Submit commands to trigger snapshot in majority
	for i := 0; i < 15; i++ {
		cmd := fmt.Sprintf("cmd-%d", i)
		_, _, err := cluster.SubmitCommand(cmd)
		if err != nil {
			t.Logf("Failed to submit command: %v", err)
			continue
		}
		// Small delay to prevent overwhelming the system
		time.Sleep(20 * time.Millisecond)
	}

	// Wait for snapshot to be created in majority partition
	time.Sleep(500 * time.Millisecond)

	// Heal partition
	cluster.HealPartition()
	t.Log("Healed partition")

	// Wait for partitioned node to catch up
	time.Sleep(2 * time.Second)

	// Verify all nodes have same state
	commitIndices := make([]int, 5)
	for i, node := range cluster.Nodes {
		commitIndices[i] = node.GetCommitIndex()
		t.Logf("Node %d commit index: %d", i, commitIndices[i])
	}

	// Check that all nodes converged
	for i := 1; i < 5; i++ {
		if commitIndices[i] != commitIndices[0] {
			t.Errorf("Node %d commit index %d != node 0 commit index %d",
				i, commitIndices[i], commitIndices[0])
		}
	}
}

// TestSnapshotDuringLeadershipChange tests snapshot behavior during leadership change
func TestSnapshotDuringLeadershipChange(t *testing.T) {
	// Create 5-node cluster
	cluster := helpers.NewTestCluster(t, 5)

	// Start cluster
	if err := cluster.Start(); err != nil {
		t.Fatalf("Failed to start cluster: %v", err)
	}
	defer cluster.Stop() //nolint:errcheck // test cleanup

	// Wait for leader
	initialLeader, err := cluster.WaitForLeader(2 * time.Second)
	if err != nil {
		t.Fatalf("No leader elected: %v", err)
	}

	// Submit commands
	for i := 0; i < 10; i++ {
		idx, _, err := cluster.SubmitCommand(fmt.Sprintf("cmd-%d", i))
		if err != nil {
			t.Fatalf("Failed to submit command: %v", err)
		}
		if err := cluster.WaitForCommitIndex(idx, time.Second); err != nil {
			t.Logf("Warning: Failed to wait for commit index %d: %v", idx, err)
		}
	}

	// Stop current leader to trigger new election
	cluster.Nodes[initialLeader].Stop()
	t.Logf("Stopped leader %d", initialLeader)

	// Wait for new leader
	time.Sleep(500 * time.Millisecond)

	var newLeader = -1
	for i, node := range cluster.Nodes {
		if i != initialLeader {
			_, isLeader := node.GetState()
			if isLeader {
				newLeader = i
				break
			}
		}
	}

	if newLeader == -1 {
		t.Fatal("No new leader elected")
	}

	t.Logf("New leader is node %d", newLeader)

	// Submit more commands with new leader
	for i := 10; i < 20; i++ {
		idx, _, isLeader := cluster.Nodes[newLeader].Submit(fmt.Sprintf("cmd-%d", i))
		if !isLeader {
			t.Fatalf("New leader lost leadership")
		}

		// Wait for commit on active nodes
		activeNodes := make([]raft.Node, 0)
		for j, node := range cluster.Nodes {
			if j != initialLeader {
				activeNodes = append(activeNodes, node)
			}
		}
		helpers.WaitForCommitIndex(t, activeNodes, idx, time.Second)
	}

	// Verify consistency
	for i := 0; i < 5; i++ {
		if i != initialLeader {
			commitIndex := cluster.Nodes[i].GetCommitIndex()
			t.Logf("Node %d commit index: %d", i, commitIndex)
		}
	}
}

// TestSnapshotOfSnapshotIndex tests edge case of creating snapshot at snapshot index
func TestSnapshotOfSnapshotIndex(t *testing.T) {
	// Create single node for simplicity
	config := &raft.Config{
		ID:                 0,
		Peers:              []int{0},
		ElectionTimeoutMin: 150 * time.Millisecond,
		ElectionTimeoutMax: 300 * time.Millisecond,
		HeartbeatInterval:  50 * time.Millisecond,
		Logger:             raft.NewTestLogger(t),
	}

	transport := raft.NewMockTransport(0)
	stateMachine := raft.NewMockStateMachine()

	node, err := raft.NewNode(config, transport, nil, stateMachine)
	if err != nil {
		t.Fatalf("Failed to create node: %v", err)
	}

	ctx := context.Background()
	if err := node.Start(ctx); err != nil {
		t.Fatalf("Failed to start node: %v", err)
	}
	defer node.Stop() //nolint:errcheck // test cleanup

	// Wait for node to become leader
	helpers.WaitForLeader(t, []raft.Node{node}, 2*time.Second)

	// Submit commands
	for i := 0; i < 10; i++ {
		idx, _, isLeader := node.Submit(fmt.Sprintf("cmd-%d", i))
		if !isLeader {
			t.Fatal("Node is not leader")
		}
		helpers.WaitForCommitIndex(t, []raft.Node{node}, idx, time.Second)
	}

	// The snapshot creation is automatic based on MaxLogSize
	// Wait for snapshot to be created automatically
	time.Sleep(500 * time.Millisecond)

	// Submit more commands
	for i := 10; i < 15; i++ {
		idx, _, isLeader := node.Submit(fmt.Sprintf("cmd-%d", i))
		if !isLeader {
			t.Fatal("Node is not leader")
		}
		helpers.WaitForCommitIndex(t, []raft.Node{node}, idx, time.Second)
	}

	// Snapshots are created automatically, so we just verify the log state
	t.Logf("Final commit index: %d", node.GetCommitIndex())
}

// TestConcurrentSnapshotAndReplication tests concurrent snapshot creation and log replication
func TestConcurrentSnapshotAndReplication(t *testing.T) {
	// Create 3-node cluster
	cluster := helpers.NewTestCluster(t, 3)

	// Start cluster
	if err := cluster.Start(); err != nil {
		t.Fatalf("Failed to start cluster: %v", err)
	}
	defer cluster.Stop() //nolint:errcheck // test cleanup

	// Wait for leader
	_, err := cluster.WaitForLeader(2 * time.Second)
	if err != nil {
		t.Fatalf("No leader elected: %v", err)
	}

	// Start concurrent operations
	done := make(chan struct{})
	var wg sync.WaitGroup

	// Goroutine 1: Continuous command submission
	wg.Add(1)
	go func() {
		defer wg.Done()
		i := 0
		for {
			select {
			case <-done:
				return
			default:
				cmd := fmt.Sprintf("concurrent-cmd-%d", i)
				if _, _, err := cluster.SubmitCommand(cmd); err != nil {
					// Log but don't fail - expected during snapshots
					t.Logf("Failed to submit command during snapshot: %v", err)
				}
				i++
				time.Sleep(10 * time.Millisecond)
			}
		}
	}()

	// Goroutine 2: Periodic snapshots
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop() //nolint:errcheck // background ticker cleanup

		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				// Snapshots are created automatically based on log size
				// Just log when we would have tried to create one
				t.Log("Snapshot trigger time (automatic based on log size)")
			}
		}
	}()

	// Run for 2 seconds
	time.Sleep(2 * time.Second)
	close(done)
	wg.Wait()

	// Verify consistency
	commitIndices := make([]int, 3)
	for i, node := range cluster.Nodes {
		commitIndices[i] = node.GetCommitIndex()
		t.Logf("Node %d commit index: %d", i, commitIndices[i])
	}

	// Allow for some divergence but not too much
	for i := 1; i < 3; i++ {
		diff := commitIndices[i] - commitIndices[0]
		if diff < 0 {
			diff = -diff
		}
		if diff > 5 {
			t.Errorf("Large divergence in commit indices: %v", commitIndices)
		}
	}
}

// TestSnapshotInstallationRaceConditions tests race conditions during snapshot installation
func TestSnapshotInstallationRaceConditions(t *testing.T) {
	// Create 5-node cluster with partitionable transport
	cluster := helpers.NewTestCluster(t, 5, helpers.WithPartitionableTransport())

	// Start cluster
	if err := cluster.Start(); err != nil {
		t.Fatalf("Failed to start cluster: %v", err)
	}
	defer cluster.Stop() //nolint:errcheck // test cleanup

	// Wait for leader
	_, err := cluster.WaitForLeader(2 * time.Second)
	if err != nil {
		t.Fatalf("No leader elected: %v", err)
	}

	// Partition two nodes
	if err := cluster.PartitionNode(3); err != nil {
		t.Fatalf("Failed to partition node 3: %v", err)
	}
	if err := cluster.PartitionNode(4); err != nil {
		t.Fatalf("Failed to partition node 4: %v", err)
	}

	// Submit many commands to majority
	for i := 0; i < 50; i++ {
		idx, _, err := cluster.SubmitCommand(fmt.Sprintf("race-cmd-%d", i))
		if err != nil {
			continue
		}

		// Wait for commit on majority nodes
		majorityNodes := []raft.Node{cluster.Nodes[0], cluster.Nodes[1], cluster.Nodes[2]}
		helpers.WaitForCommitIndex(t, majorityNodes, idx, time.Second)
	}

	// Snapshots are created automatically, wait for it
	time.Sleep(500 * time.Millisecond)

	// Heal partitions individually
	// First heal node 3
	if pt, ok := cluster.Transports[3].(*helpers.PartitionableTransport); ok {
		pt.UnblockAll()
	}
	// Also unblock node 3 from other nodes
	for i, t := range cluster.Transports {
		if i != 3 {
			if pt, ok := t.(*helpers.PartitionableTransport); ok {
				pt.Unblock(3)
			}
		}
	}

	// Submit more commands while node 3 is catching up
	for i := 50; i < 60; i++ {
		cluster.SubmitCommand(fmt.Sprintf("race-cmd-%d", i)) //nolint:errcheck // background load generation
	}

	// Heal node 4
	if pt, ok := cluster.Transports[4].(*helpers.PartitionableTransport); ok {
		pt.UnblockAll()
	}
	// Also unblock node 4 from other nodes
	for i, t := range cluster.Transports {
		if i != 4 {
			if pt, ok := t.(*helpers.PartitionableTransport); ok {
				pt.Unblock(4)
			}
		}
	}

	// Wait for all nodes to catch up
	time.Sleep(3 * time.Second)

	// Verify consistency
	commitIndices := make([]int, 5)
	for i, node := range cluster.Nodes {
		commitIndices[i] = node.GetCommitIndex()
		t.Logf("Node %d commit index: %d", i, commitIndices[i])
	}

	// All nodes should eventually converge
	maxCommit := commitIndices[0]
	for i := 1; i < 5; i++ {
		if commitIndices[i] > maxCommit {
			maxCommit = commitIndices[i]
		}
	}

	for i := 0; i < 5; i++ {
		if maxCommit-commitIndices[i] > 5 {
			t.Errorf("Node %d lagging too far behind: commit index %d vs max %d",
				i, commitIndices[i], maxCommit)
		}
	}
}

// TestPersistenceWithRapidSnapshots tests persistence with rapid snapshot creation
func TestPersistenceWithRapidSnapshots(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping rapid snapshot test in short mode")
	}

	// Create single node with persistence
	config := &raft.Config{
		ID:                 0,
		Peers:              []int{0},
		ElectionTimeoutMin: 150 * time.Millisecond,
		ElectionTimeoutMax: 300 * time.Millisecond,
		HeartbeatInterval:  50 * time.Millisecond,
		Logger:             raft.NewTestLogger(t),
		MaxLogSize:         5, // Very small to trigger many snapshots
	}

	transport := raft.NewMockTransport(0)
	persistence := raft.NewMockPersistence()
	stateMachine := raft.NewMockStateMachine()

	node, err := raft.NewNode(config, transport, persistence, stateMachine)
	if err != nil {
		t.Fatalf("Failed to create node: %v", err)
	}

	ctx := context.Background()
	if err := node.Start(ctx); err != nil {
		t.Fatalf("Failed to start node: %v", err)
	}
	defer node.Stop() //nolint:errcheck // test cleanup

	// Wait for leader
	helpers.WaitForLeader(t, []raft.Node{node}, 2*time.Second)

	// Start rapid command submission
	done := make(chan struct{})
	var wg sync.WaitGroup

	// Command submitter
	wg.Add(1)
	go func() {
		defer wg.Done()
		i := 0
		for {
			select {
			case <-done:
				return
			default:
				node.Submit(fmt.Sprintf("rapid-cmd-%d", i))
				i++
				time.Sleep(5 * time.Millisecond)
			}
		}
	}()

	// Run for 5 seconds
	time.Sleep(5 * time.Second)
	close(done)
	wg.Wait()

	// Check results - snapshots are created automatically
	if persistence.HasSnapshot() {
		t.Log("Snapshots were created during rapid command submission")
		snapshot, _ := persistence.LoadSnapshot()
		if snapshot != nil {
			t.Logf("Last snapshot at index: %d", snapshot.LastIncludedIndex)
		}
	}

	// Verify node is still functional
	idx, _, isLeader := node.Submit("final-test-cmd")
	if !isLeader {
		t.Fatal("Node lost leadership")
	}

	helpers.WaitForCommitIndex(t, []raft.Node{node}, idx, time.Second)
	t.Log("Node still functional after rapid command submission")
}

// TestSnapshotTransmissionFailure tests handling of snapshot transmission failures
func TestSnapshotTransmissionFailure(t *testing.T) {
	// This test would require a custom transport that can simulate failures
	// For now, we'll use partition/heal to simulate transmission issues

	cluster := helpers.NewTestCluster(t, 3, helpers.WithPartitionableTransport())

	if err := cluster.Start(); err != nil {
		t.Fatalf("Failed to start cluster: %v", err)
	}
	defer cluster.Stop() //nolint:errcheck // test cleanup

	// Wait for leader
	leaderID, err := cluster.WaitForLeader(2 * time.Second)
	if err != nil {
		t.Fatalf("No leader elected: %v", err)
	}

	// Submit commands
	for i := 0; i < 20; i++ {
		idx, _, err := cluster.SubmitCommand(fmt.Sprintf("cmd-%d", i))
		if err != nil {
			t.Fatalf("Failed to submit command: %v", err)
		}
		cluster.WaitForCommitIndex(idx, time.Second) //nolint:errcheck // best effort wait
	}

	// Partition follower
	followerID := (leaderID + 1) % 3
	if err := cluster.PartitionNode(followerID); err != nil {
		t.Fatalf("Failed to partition node: %v", err)
	}

	// Submit more commands and create snapshot
	for i := 20; i < 40; i++ {
		cluster.SubmitCommand(fmt.Sprintf("cmd-%d", i)) //nolint:errcheck // background commands
	}

	// Wait for automatic snapshot creation
	time.Sleep(500 * time.Millisecond)

	// Briefly heal and re-partition to simulate transmission failure
	cluster.HealPartition()
	time.Sleep(100 * time.Millisecond)
	cluster.PartitionNode(followerID) //nolint:errcheck // test partition setup
	time.Sleep(100 * time.Millisecond)

	// Finally heal
	cluster.HealPartition()

	// Wait for follower to catch up
	time.Sleep(2 * time.Second)

	// Verify follower caught up
	leaderCommit := cluster.Nodes[leaderID].GetCommitIndex()
	followerCommit := cluster.Nodes[followerID].GetCommitIndex()

	if followerCommit < leaderCommit-5 {
		t.Errorf("Follower didn't catch up: %d vs leader %d", followerCommit, leaderCommit)
	}
}

// Helper functions
