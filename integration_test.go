package raft_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/ueisele/raft"
	"github.com/ueisele/raft/test"
)

// TestLeaderElection tests basic leader election
func TestLeaderElection(t *testing.T) {
	// Create a 3-node cluster
	cluster := test.NewTestCluster(t, test.DefaultClusterConfig(3))
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

	// Verify only one leader
	leaderCount := 0
	for _, node := range cluster.Nodes {
		if node.IsLeader() {
			leaderCount++
		}
	}

	if leaderCount != 1 {
		t.Errorf("Expected exactly 1 leader, got %d", leaderCount)
	}

	// Verify all nodes are in the same term
	leaderTerm := cluster.Nodes[leaderID].GetCurrentTerm()
	for i, node := range cluster.Nodes {
		if node.GetCurrentTerm() != leaderTerm {
			t.Errorf("Node %d has term %d, expected %d", i, node.GetCurrentTerm(), leaderTerm)
		}
	}
}

// TestLogReplication tests basic log replication
func TestLogReplication(t *testing.T) {
	// Create a 3-node cluster
	cluster := test.NewTestCluster(t, test.DefaultClusterConfig(3))
	defer cluster.Stop()

	ctx := context.Background()
	if err := cluster.Start(ctx); err != nil {
		t.Fatalf("Failed to start cluster: %v", err)
	}

	// Wait for leader election
	cluster.WaitForLeaderElection(t, 2*time.Second)

	// Submit commands
	commands := []string{"cmd1", "cmd2", "cmd3"}
	for _, cmd := range commands {
		index, term, isLeader := cluster.SubmitCommand(cmd)
		if !isLeader {
			t.Fatal("Lost leadership during command submission")
		}
		t.Logf("Submitted %s at index %d, term %d", cmd, index, term)
	}

	// Wait for replication
	cluster.WaitForReplication(t, len(commands), 2*time.Second)

	// Verify all nodes have the same log
	for i, node := range cluster.Nodes {
		if node.GetLogLength() < len(commands) {
			t.Errorf("Node %d has log length %d, expected at least %d",
				i, node.GetLogLength(), len(commands))
		}
	}

	// Verify commands were applied to state machines
	// Wait for the last command to be committed on all nodes
	timing := test.DefaultTimingConfig()
	lastCmdIndex := len(commands)
	test.WaitForCommitIndexWithConfig(t, cluster.Nodes, lastCmdIndex, timing)

	for i, sm := range cluster.StateMachines {
		appliedCount := sm.GetAppliedCount()
		if appliedCount < len(commands) {
			t.Errorf("Node %d applied %d commands, expected at least %d",
				i, appliedCount, len(commands))
		}
	}
}

// TestLeaderFailover tests leader failover
func TestLeaderFailover(t *testing.T) {
	// Create a 5-node cluster for better stability
	cluster := test.NewTestCluster(t, test.DefaultClusterConfig(5))
	defer cluster.Stop()

	ctx := context.Background()
	if err := cluster.Start(ctx); err != nil {
		t.Fatalf("Failed to start cluster: %v", err)
	}

	// Wait for initial leader
	oldLeaderID := cluster.WaitForLeaderElection(t, 2*time.Second)
	oldTerm := cluster.Nodes[oldLeaderID].GetCurrentTerm()

	// Submit some commands
	for i := 0; i < 3; i++ {
		cluster.SubmitCommand(i)
	}

	// Wait for replication
	cluster.WaitForReplication(t, 3, 2*time.Second)

	// Disconnect the leader
	cluster.DisconnectNode(oldLeaderID)

	// Wait for new leader election
	timing := test.DefaultTimingConfig()
	timing.ElectionTimeout = 3 * time.Second
	newLeaderID := test.WaitForLeaderWithConfig(t, cluster.Nodes, timing)
	if newLeaderID == oldLeaderID {
		t.Error("Same leader after disconnection")
	}

	newTerm := cluster.Nodes[newLeaderID].GetCurrentTerm()
	if newTerm <= oldTerm {
		t.Errorf("New term %d should be greater than old term %d", newTerm, oldTerm)
	}

	// Submit more commands with new leader
	for i := 3; i < 6; i++ {
		index, _, isLeader := cluster.Nodes[newLeaderID].Submit(i)
		if !isLeader {
			t.Fatal("New leader rejected command")
		}
		t.Logf("New leader accepted command at index %d", index)
	}

	// Verify replication continues
	// Wait for the new command to be replicated
	activeNodes := []raft.Node{}
	for i, node := range cluster.Nodes {
		if i != oldLeaderID {
			activeNodes = append(activeNodes, node)
		}
	}
	// Get the last submitted command index
	lastIndex := cluster.Nodes[newLeaderID].GetLogLength() - 1
	test.WaitForCommitIndexWithConfig(t, activeNodes, lastIndex, timing)

	activeNodeCount := 0
	for i, node := range cluster.Nodes {
		if i != oldLeaderID {
			activeNodeCount++
			if node.GetCommitIndex() < 3 {
				t.Errorf("Node %d should have replicated at least 3 entries", i)
			}
		}
	}

	if activeNodeCount < 4 {
		t.Error("Should have at least 4 active nodes")
	}
}

// TestNetworkPartition tests behavior during network partition
func TestNetworkPartition(t *testing.T) {
	// Create a 5-node cluster
	cluster := test.NewTestCluster(t, test.DefaultClusterConfig(5))
	defer cluster.Stop()

	ctx := context.Background()
	if err := cluster.Start(ctx); err != nil {
		t.Fatalf("Failed to start cluster: %v", err)
	}

	// Wait for leader election
	leaderID := cluster.WaitForLeaderElection(t, 2*time.Second)

	// Create partition: leader + 1 node vs 3 nodes
	var minorityGroup, majorityGroup []int

	if leaderID < 2 {
		minorityGroup = []int{0, 1}
		majorityGroup = []int{2, 3, 4}
	} else {
		minorityGroup = []int{leaderID, 0}
		majorityGroup = []int{1, 2, 3}
		if leaderID <= 3 {
			majorityGroup = []int{1, 2, 4}
		}
	}

	// Create the partition
	cluster.CreatePartition(minorityGroup, majorityGroup)

	// Old leader in minority should not be able to commit
	if contains(minorityGroup, leaderID) {
		_, _, isLeader := cluster.Nodes[leaderID].Submit("minority_cmd")
		if isLeader {
			// Leader might still think it's leader, but shouldn't be able to commit
			initialCommit := cluster.Nodes[leaderID].GetCommitIndex()
			
			// Use test.Consistently to verify commit index doesn't advance
			test.Consistently(t, func() bool {
				currentCommit := cluster.Nodes[leaderID].GetCommitIndex()
				return currentCommit <= initialCommit+1
			}, 1*time.Second, "minority leader should not commit")
			
			finalCommit := cluster.Nodes[leaderID].GetCommitIndex()

			if finalCommit > initialCommit+1 {
				t.Error("Minority leader shouldn't be able to commit new entries")
			}
		}
	}

	// Wait for new leader in majority
	timing := test.DefaultTimingConfig()
	timing.ElectionTimeout = 2 * time.Second

	// Find leader in majority group
	var majorityLeader int = -1
	for _, id := range majorityGroup {
		if cluster.Nodes[id].IsLeader() {
			majorityLeader = id
			break
		}
	}

	if majorityLeader == -1 {
		// Log the state of all nodes for debugging
		for i, node := range cluster.Nodes {
			t.Logf("Node %d: IsLeader=%v, Term=%d, CommitIndex=%d", 
				i, node.IsLeader(), node.GetCurrentTerm(), node.GetCommitIndex())
		}
		t.Fatal("No leader elected in majority partition")
	}

	// Majority should be able to make progress
	index, _, isLeader := cluster.Nodes[majorityLeader].Submit("majority_cmd")
	if !isLeader {
		t.Fatal("Majority leader rejected command")
	}

	// Wait for replication in majority
	majorityNodes := []raft.Node{}
	for _, id := range majorityGroup {
		majorityNodes = append(majorityNodes, cluster.Nodes[id])
	}
	test.WaitForCommitIndexWithConfig(t, majorityNodes, index, timing)

	// Verify majority nodes replicated
	replicatedCount := 0
	for _, id := range majorityGroup {
		if cluster.Nodes[id].GetLogLength() >= index {
			replicatedCount++
		}
	}

	if replicatedCount < 2 {
		t.Errorf("Majority partition should replicate entries. Only %d out of %d nodes replicated index %d",
			replicatedCount, len(majorityGroup), index)
	}

	// Heal partition
	cluster.HealPartition(minorityGroup, majorityGroup)

	// Wait for reconciliation
	test.WaitForStableLeader(t, cluster.Nodes, timing)

	// test.Eventually all nodes should converge
	cluster.VerifyConsistency(t)
}

// TestPersistence tests that state survives restarts
func TestPersistence(t *testing.T) {
	// Create a 3-node cluster with persistence
	config := test.DefaultClusterConfig(3)
	config.WithPersistence = true
	cluster := test.NewTestCluster(t, config)
	defer cluster.Stop()

	ctx := context.Background()
	if err := cluster.Start(ctx); err != nil {
		t.Fatalf("Failed to start cluster: %v", err)
	}

	// Wait for leader and submit commands
	cluster.WaitForLeaderElection(t, 2*time.Second)

	for i := 0; i < 5; i++ {
		cluster.SubmitCommand(i)
	}

	// Wait for replication
	cluster.WaitForReplication(t, 5, 2*time.Second)

	// Get current state
	oldTerm := cluster.Nodes[0].GetCurrentTerm()
	oldLogLength := cluster.Nodes[0].GetLogLength()

	// Stop node 0
	cluster.Nodes[0].Stop()

	// Simulate restart by creating new node with same persistence
	newConfig := &raft.Config{
		ID:                 0,
		Peers:              []int{0, 1, 2},
		ElectionTimeoutMin: config.ElectionTimeoutMin,
		ElectionTimeoutMax: config.ElectionTimeoutMax,
		HeartbeatInterval:  config.HeartbeatInterval,
		MaxLogSize:         config.MaxLogSize,
	}

	newNode, err := raft.NewNode(newConfig, cluster.Transports[0], cluster.Persistences[0], cluster.StateMachines[0])
	if err != nil {
		t.Fatalf("Failed to create new node: %v", err)
	}

	cluster.Nodes[0] = newNode
	
	// Re-register with all transports
	for i := range cluster.Transports {
		cluster.Transports[i].RegisterServer(0, newNode.(raft.RPCHandler))
	}

	if err := newNode.Start(ctx); err != nil {
		t.Fatalf("Failed to start new node: %v", err)
	}

	// Wait for node to recover and rejoin cluster
	timing := test.DefaultTimingConfig()
	test.WaitForConditionWithProgress(t, func() (bool, string) {
		term := newNode.GetCurrentTerm()
		logLen := newNode.GetLogLength()
		return term >= oldTerm && logLen >= oldLogLength, 
			fmt.Sprintf("term=%d (need >=%d), log=%d (need >=%d)", term, oldTerm, logLen, oldLogLength)
	}, timing.ElectionTimeout, "node recovery")

	// Verify state was recovered
	if newNode.GetCurrentTerm() < oldTerm {
		t.Errorf("Term should be at least %d after recovery, got %d", oldTerm, newNode.GetCurrentTerm())
	}

	if newNode.GetLogLength() != oldLogLength {
		t.Errorf("Log length should be %d after recovery, got %d", oldLogLength, newNode.GetLogLength())
	}

	// Verify node can participate in cluster
	// First check who is the leader
	leaderNode, leaderID := cluster.GetLeader()
	if leaderNode == nil {
		t.Fatal("No leader after node restart")
	}
	t.Logf("Leader is node %d", leaderID)
	
	// Submit command through leader
	beforeSubmit := newNode.GetLogLength()
	cmdIndex, cmdTerm, _ := cluster.SubmitCommand("after_restart")
	t.Logf("Submitted command at index %d, term %d", cmdIndex, cmdTerm)
	
	// Wait for replication to complete
	test.WaitForCommitIndexWithConfig(t, cluster.Nodes, cmdIndex, timing)

	afterSubmit := newNode.GetLogLength()
	newCommitIndex := newNode.GetCommitIndex()
	
	// Check all nodes for comparison
	for i, node := range cluster.Nodes {
		t.Logf("Node %d - LogLength: %d, CommitIndex: %d, Term: %d, IsLeader: %v", 
			i, node.GetLogLength(), node.GetCommitIndex(), node.GetCurrentTerm(), node.IsLeader())
	}
	
	if afterSubmit <= beforeSubmit {
		t.Errorf("Recovered node should accept new entries. Log length before: %d, after: %d", 
			beforeSubmit, afterSubmit)
	}
	
	if newCommitIndex == 0 {
		t.Error("Recovered node should have non-zero commit index after rejoining cluster")
	}
}

// Helper function
func contains(slice []int, val int) bool {
	for _, v := range slice {
		if v == val {
			return true
		}
	}
	return false
}
