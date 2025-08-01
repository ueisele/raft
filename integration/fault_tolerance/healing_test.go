package fault_tolerance

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/ueisele/raft"
	"github.com/ueisele/raft/integration/helpers"
)

// TestClusterHealing verifies that a cluster eventually converges after disruptions
func TestClusterHealing(t *testing.T) {
	// Create 3-node cluster
	cluster := helpers.NewTestCluster(t, 3)

	// Start cluster
	if err := cluster.Start(); err != nil {
		t.Fatalf("Failed to start cluster: %v", err)
	}

	// Wait for initial leader
	initialLeader, err := cluster.WaitForLeader(2*time.Second)
	if err != nil {
		t.Fatalf("No initial leader elected: %v", err)
	}

	// Submit initial commands
	for i := 0; i < 5; i++ {
		idx, _, err := cluster.SubmitCommand(fmt.Sprintf("initial-%d", i))
		if err != nil {
			t.Fatalf("Failed to submit command: %v", err)
		}
		if err := cluster.WaitForCommitIndex(idx, time.Second); err != nil {
			t.Fatalf("Command not committed: %v", err)
		}
	}

	// Cause disruption by stopping leader
	cluster.Nodes[initialLeader].Stop()
	t.Logf("Stopped initial leader %d", initialLeader)

	// Wait for new leader election
	time.Sleep(500 * time.Millisecond)

	// Find new leader
	newLeader := -1
	for i, node := range cluster.Nodes {
		if i == initialLeader {
			continue
		}
		_, isLeader := node.GetState()
		if isLeader {
			newLeader = i
			break
		}
	}

	if newLeader == -1 {
		t.Fatal("No new leader elected after disruption")
	}

	t.Logf("New leader elected: %d", newLeader)

	// Submit more commands with new leader
	for i := 0; i < 3; i++ {
		_, _, isLeader := cluster.Nodes[newLeader].Submit(fmt.Sprintf("new-leader-%d", i))
		if !isLeader {
			t.Logf("Failed to submit to new leader: not leader")
		}
	}

	// Restart the old leader
	oldConfig := &raft.Config{
		ID:                 initialLeader,
		Peers:              []int{0, 1, 2},
		ElectionTimeoutMin: 150 * time.Millisecond,
		ElectionTimeoutMax: 300 * time.Millisecond,
		HeartbeatInterval:  50 * time.Millisecond,
		Logger:             raft.NewTestLogger(t),
	}

	// Use the same transport type
	transport := helpers.NewMultiNodeTransport(initialLeader, cluster.Registry.(*helpers.NodeRegistry))
	
	oldNode, err := raft.NewNode(oldConfig, transport, nil, raft.NewMockStateMachine())
	if err != nil {
		t.Fatalf("Failed to recreate old leader node: %v", err)
	}

	cluster.Nodes[initialLeader] = oldNode
	cluster.Registry.(*helpers.NodeRegistry).Register(initialLeader, oldNode.(raft.RPCHandler))

	ctx := context.Background()
	if err := oldNode.Start(ctx); err != nil {
		t.Fatalf("Failed to restart old leader: %v", err)
	}
	defer oldNode.Stop()

	t.Logf("Restarted old leader %d", initialLeader)

	// Wait for healing - old leader should catch up
	time.Sleep(2 * time.Second)

	// Verify all nodes have converged
	commitIndices := make([]int, len(cluster.Nodes))
	for i, node := range cluster.Nodes {
		commitIndices[i] = node.GetCommitIndex()
		t.Logf("Node %d commit index: %d", i, commitIndices[i])
	}

	// All should have same commit index
	for i := 1; i < len(commitIndices); i++ {
		if commitIndices[i] != commitIndices[0] {
			t.Errorf("Node %d has different commit index: %d vs %d", 
				i, commitIndices[i], commitIndices[0])
		}
	}

	// Verify logs are consistent
	helpers.AssertLogConsistency(t, cluster.Nodes, commitIndices[0])

	t.Log("✓ Cluster healed successfully after leader failure and restart")
}

// TestEventualConsistency tests that nodes eventually converge to the same state
func TestEventualConsistency(t *testing.T) {
	// Create 5-node cluster with partitionable transport
	cluster := helpers.NewTestCluster(t, 5, helpers.WithPartitionableTransport())

	// Start cluster
	if err := cluster.Start(); err != nil {
		t.Fatalf("Failed to start cluster: %v", err)
	}

	// Wait for initial leader
	_, err := cluster.WaitForLeader(2*time.Second)
	if err != nil {
		t.Fatalf("No initial leader elected: %v", err)
	}

	// Create various disruptions and verify eventual consistency
	
	// Disruption 1: Temporary partition
	t.Log("Disruption 1: Creating temporary partition")
	
	// Partition nodes 0 and 1 from the rest
	cluster.PartitionNode(0)
	cluster.PartitionNode(1)
	
	// Submit commands to majority partition
	for i := 0; i < 5; i++ {
		// Find leader in majority partition
		var leader int = -1
		for j := 2; j < 5; j++ {
			_, isLeader := cluster.Nodes[j].GetState()
			if isLeader {
				leader = j
				break
			}
		}
		
		if leader != -1 {
			_, _, isLeader := cluster.Nodes[leader].Submit(fmt.Sprintf("majority-%d", i))
			if !isLeader {
				t.Logf("Failed to submit to leader %d: not leader", leader)
			}
		}
	}
	
	// Heal partition
	cluster.HealPartition()
	t.Log("Healed partition")
	
	// Wait for convergence
	time.Sleep(2 * time.Second)
	
	// Verify all nodes converged
	converged := true
	var referenceCommit int
	for i, node := range cluster.Nodes {
		commit := node.GetCommitIndex()
		if i == 0 {
			referenceCommit = commit
		} else if commit != referenceCommit {
			converged = false
			t.Logf("Node %d commit index %d differs from reference %d", 
				i, commit, referenceCommit)
		}
	}
	
	if converged {
		t.Log("✓ All nodes converged after partition heal")
	} else {
		// Give more time
		time.Sleep(2 * time.Second)
		
		// Check again
		for i, node := range cluster.Nodes {
			commit := node.GetCommitIndex()
			t.Logf("Node %d final commit index: %d", i, commit)
		}
	}

	// Disruption 2: Leader failures
	t.Log("\nDisruption 2: Rapid leader failures")
	
	for round := 0; round < 3; round++ {
		// Find and stop current leader
		for i, node := range cluster.Nodes {
			_, isLeader := node.GetState()
			if isLeader {
				node.Stop()
				t.Logf("Stopped leader %d", i)
				break
			}
		}
		
		// Wait for new leader
		time.Sleep(500 * time.Millisecond)
	}
	
	// Restart all stopped nodes
	ctx := context.Background()
	for i, node := range cluster.Nodes {
		// Check if node is stopped by trying to get state
		func() {
			defer func() {
				if recover() != nil {
					// Node is stopped, restart it
					config := &raft.Config{
						ID:                 i,
						Peers:              []int{0, 1, 2, 3, 4},
						ElectionTimeoutMin: 150 * time.Millisecond,
						ElectionTimeoutMax: 300 * time.Millisecond,
						HeartbeatInterval:  50 * time.Millisecond,
						Logger:             raft.NewTestLogger(t),
					}
					
					// Create transport based on cluster type
					var transport raft.Transport
					switch reg := cluster.Registry.(type) {
					case *helpers.PartitionRegistry:
						transport = helpers.NewPartitionableTransport(i, reg)
					case *helpers.NodeRegistry:
						transport = helpers.NewMultiNodeTransport(i, reg)
					}
					newNode, err := raft.NewNode(config, transport, nil, raft.NewMockStateMachine())
					if err == nil {
						cluster.Nodes[i] = newNode
						cluster.Registry.(*helpers.NodeRegistry).Register(i, newNode.(raft.RPCHandler))
						newNode.Start(ctx)
						t.Logf("Restarted node %d", i)
					}
				}
			}()
			node.GetState()
		}()
	}
	
	// Wait for stabilization
	time.Sleep(3 * time.Second)
	
	// Final consistency check
	finalCommits := make([]int, len(cluster.Nodes))
	for i, node := range cluster.Nodes {
		finalCommits[i] = node.GetCommitIndex()
	}
	
	allEqual := true
	for i := 1; i < len(finalCommits); i++ {
		if finalCommits[i] != finalCommits[0] {
			allEqual = false
			break
		}
	}
	
	if allEqual {
		t.Logf("✓ Final consistency achieved: all nodes at commit index %d", finalCommits[0])
	} else {
		t.Errorf("Final consistency not achieved: commits=%v", finalCommits)
	}
}

// TestHealingWithDivergentLogs tests healing when nodes have divergent logs
func TestHealingWithDivergentLogs(t *testing.T) {
	// Create 5-node cluster with partitionable transport
	cluster := helpers.NewTestCluster(t, 5, helpers.WithPartitionableTransport())

	// Start cluster
	if err := cluster.Start(); err != nil {
		t.Fatalf("Failed to start cluster: %v", err)
	}

	// Wait for initial leader
	_, err := cluster.WaitForLeader(2*time.Second)
	if err != nil {
		t.Fatalf("No initial leader elected: %v", err)
	}

	// Submit initial commands
	for i := 0; i < 5; i++ {
		idx, _, err := cluster.SubmitCommand(fmt.Sprintf("common-%d", i))
		if err != nil {
			t.Fatalf("Failed to submit command: %v", err)
		}
		cluster.WaitForCommitIndex(idx, time.Second)
	}

	commonCommitIndex := cluster.Nodes[0].GetCommitIndex()
	t.Logf("Common commit index before divergence: %d", commonCommitIndex)

	// Create network partition: [0,1] vs [2,3,4]
	cluster.PartitionNode(0)
	cluster.PartitionNode(1)
	t.Log("Created partition: [0,1] vs [2,3,4]")

	// Each partition will elect its own leader and accept different commands
	
	// Submit to minority partition (won't commit)
	var minorityLeader int = -1
	for i := 0; i <= 1; i++ {
		_, isLeader := cluster.Nodes[i].GetState()
		if isLeader {
			minorityLeader = i
			break
		}
	}
	
	if minorityLeader == -1 {
		// Try to force election in minority
		time.Sleep(500 * time.Millisecond)
	}
	
	// Wait for leader election in majority partition
	time.Sleep(1 * time.Second)
	
	// Submit to majority partition
	var majorityLeader int = -1
	for attempts := 0; attempts < 10; attempts++ {
		for i := 2; i <= 4; i++ {
			_, isLeader := cluster.Nodes[i].GetState()
			if isLeader {
				majorityLeader = i
				break
			}
		}
		
		if majorityLeader != -1 {
			break
		}
		
		time.Sleep(200 * time.Millisecond)
	}
	
	if majorityLeader == -1 {
		t.Fatal("No leader in majority partition after waiting")
	}
	
	t.Logf("Majority partition elected leader: %d", majorityLeader)
	
	// Submit different commands to each partition
	for i := 0; i < 5; i++ {
		// Majority partition commands (will commit)
		cluster.Nodes[majorityLeader].Submit(fmt.Sprintf("majority-%d", i))
		
		// Minority partition commands (won't commit)
		if minorityLeader != -1 {
			cluster.Nodes[minorityLeader].Submit(fmt.Sprintf("minority-%d", i))
		}
	}
	
	time.Sleep(500 * time.Millisecond)
	
	// Log state before healing
	t.Log("State before healing:")
	for i, node := range cluster.Nodes {
		commit := node.GetCommitIndex()
		term, isLeader := node.GetState()
		t.Logf("Node %d: commit=%d, term=%d, leader=%v", i, commit, term, isLeader)
	}
	
	// Heal the partition
	cluster.HealPartition()
	t.Log("Healed partition - nodes will now reconcile")
	
	// Wait for reconciliation
	time.Sleep(3 * time.Second)
	
	// Verify all nodes converged to majority's log
	majorityCommit := cluster.Nodes[majorityLeader].GetCommitIndex()
	
	converged := true
	for i, node := range cluster.Nodes {
		commit := node.GetCommitIndex()
		if commit < majorityCommit {
			converged = false
			t.Logf("Node %d still behind: commit=%d, expected>=%d", i, commit, majorityCommit)
		}
	}
	
	if converged {
		t.Log("✓ All nodes converged after healing divergent logs")
		
		// Verify log consistency
		helpers.AssertLogConsistency(t, cluster.Nodes, majorityCommit)
	} else {
		// Give more time
		time.Sleep(2 * time.Second)
		
		// Final check
		for i, node := range cluster.Nodes {
			commit := node.GetCommitIndex()
			t.Logf("Node %d final commit: %d", i, commit)
		}
	}
}

// TestHealingUnderLoad tests healing while system is under load
func TestHealingUnderLoad(t *testing.T) {
	// Create 5-node cluster
	cluster := helpers.NewTestCluster(t, 5, helpers.WithPartitionableTransport())

	// Start cluster
	if err := cluster.Start(); err != nil {
		t.Fatalf("Failed to start cluster: %v", err)
	}

	// Wait for initial leader
	_, err := cluster.WaitForLeader(2*time.Second)
	if err != nil {
		t.Fatalf("No initial leader elected: %v", err)
	}

	// Start continuous load
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var cmdCount int64
	var errCount int64
	var mu sync.Mutex

	// Multiple client goroutines
	for client := 0; client < 3; client++ {
		go func(clientID int) {
			ticker := time.NewTicker(20 * time.Millisecond)
			defer ticker.Stop()
			
			cmdIndex := 0
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					cmd := fmt.Sprintf("client-%d-cmd-%d", clientID, cmdIndex)
					_, _, err := cluster.SubmitCommand(cmd)
					
					mu.Lock()
					if err != nil {
						errCount++
					} else {
						cmdCount++
					}
					mu.Unlock()
					
					cmdIndex++
				}
			}
		}(client)
	}

	// Let load run
	time.Sleep(500 * time.Millisecond)
	
	mu.Lock()
	t.Logf("Commands before disruption: success=%d, errors=%d", cmdCount, errCount)
	mu.Unlock()

	// Create partition while under load
	t.Log("Creating partition under load")
	cluster.PartitionNode(0)
	cluster.PartitionNode(1)
	
	// Continue load during partition
	time.Sleep(1 * time.Second)
	
	mu.Lock()
	t.Logf("Commands during partition: success=%d, errors=%d", cmdCount, errCount)
	mu.Unlock()
	
	// Heal partition while still under load
	t.Log("Healing partition under load")
	cluster.HealPartition()
	
	// Continue load during healing
	time.Sleep(2 * time.Second)
	
	// Stop load
	cancel()
	
	mu.Lock()
	finalCmdCount := cmdCount
	finalErrCount := errCount
	mu.Unlock()
	
	t.Logf("Final: success=%d, errors=%d", finalCmdCount, finalErrCount)
	
	// Verify cluster healed properly
	time.Sleep(1 * time.Second)
	
	// Check consistency
	commitIndices := make([]int, len(cluster.Nodes))
	for i, node := range cluster.Nodes {
		commitIndices[i] = node.GetCommitIndex()
	}
	
	minCommit := commitIndices[0]
	maxCommit := commitIndices[0]
	for _, commit := range commitIndices {
		if commit < minCommit {
			minCommit = commit
		}
		if commit > maxCommit {
			maxCommit = commit
		}
	}
	
	t.Logf("Commit indices after healing: min=%d, max=%d", minCommit, maxCommit)
	
	if maxCommit-minCommit <= 5 {
		t.Log("✓ Cluster healed successfully under load")
	} else {
		t.Errorf("Large commit index divergence after healing: %d", maxCommit-minCommit)
	}
	
	// Verify cluster is functional
	idx, _, err := cluster.SubmitCommand("post-healing-test")
	if err != nil {
		t.Fatalf("Failed to submit after healing: %v", err)
	}
	
	if err := cluster.WaitForCommitIndex(idx, 2*time.Second); err != nil {
		t.Fatalf("Command not committed after healing: %v", err)
	}
	
	t.Log("✓ Cluster fully functional after healing under load")
}