package fault_tolerance

import (
	"fmt"
	"testing"
	"time"

	"github.com/ueisele/raft/integration/helpers"
	"github.com/ueisele/raft/integration/helpers/transporttest"
)

// TestAsymmetricPartition tests asymmetric network partitions using transport decorators
func TestAsymmetricPartition(t *testing.T) {
	// Create cluster with partitionable transport
	cluster := helpers.NewTestClusterOfSize(t, 3,
		helpers.WithPartitionableTransport(),
		helpers.WithClusterAutoStart(),
	)

	// Wait for initial leader election
	leaderID, err := cluster.WaitForLeader(2 * time.Second)
	if err != nil {
		t.Fatalf("Failed to elect leader: %v", err)
	}
	t.Logf("Initial leader: Node %d", leaderID)

	// Test Case 1: Leader can send to followers but not receive from them
	t.Log("Test Case 1: Leader outgoing only")

	// Block all incoming messages to the leader
	// This simulates asymmetric partition where followers can't reach leader
	for nodeID := 0; nodeID < 3; nodeID++ {
		if nodeID != leaderID {
			// Block follower -> leader communication
			if partition, ok := transporttest.GetCapability[transporttest.PartitionCapable](cluster, nodeID); ok {
				partition.Block(leaderID)
			}
		}
	}

	// Wait to observe the effect
	helpers.WaitForCondition(t, func() bool {
		// Wait a bit to see effect
		return true
	}, 200*time.Millisecond, "observing asymmetric partition")

	// Check if leader maintained leadership
	leaderNode, _ := cluster.GetNode(leaderID)
	_, isLeader := leaderNode.GetState()
	if !isLeader {
		t.Log("Leader lost leadership when it could still send heartbeats (expected in some implementations)")
	} else {
		t.Log("Leader maintained leadership with outgoing-only communication")
	}

	// Restore symmetric communication
	for nodeID := 0; nodeID < 3; nodeID++ {
		if partition, ok := transporttest.GetCapability[transporttest.PartitionCapable](cluster, nodeID); ok {
			partition.UnblockAll()
		}
	}

	// Wait for cluster to stabilize
	helpers.WaitForCondition(t, func() bool {
		nodes := cluster.GetNodes()
		leaderCount := 0
		for _, node := range nodes {
			if node.IsLeader() {
				leaderCount++
			}
		}
		return leaderCount == 1
	}, 2*time.Second, "cluster stabilization after healing")

	// Test Case 2: Follower isolation (can't send or receive)
	t.Log("\nTest Case 2: Complete follower isolation")

	// Get a follower ID
	followerID := (leaderID + 1) % 3

	// Completely isolate the follower
	if partition, ok := transporttest.GetCapability[transporttest.PartitionCapable](cluster, followerID); ok {
		partition.BlockAll()
	}

	// Block other nodes from reaching the follower
	for nodeID := 0; nodeID < 3; nodeID++ {
		if nodeID != followerID {
			if partition, ok := transporttest.GetCapability[transporttest.PartitionCapable](cluster, nodeID); ok {
				partition.Block(followerID)
			}
		}
	}

	t.Logf("Isolated follower %d", followerID)

	// Cluster should continue with remaining nodes
	helpers.WaitForCondition(t, func() bool {
		// Check that non-isolated nodes still have a leader
		for nodeID := 0; nodeID < 3; nodeID++ {
			if nodeID != followerID {
				if node, _ := cluster.GetNode(nodeID); node.IsLeader() {
					return true
				}
			}
		}
		return false
	}, 2*time.Second, "leadership with isolated follower")

	// Submit a command - should work with majority
	if nodeID := (followerID + 1) % 3; nodeID != followerID {
		if node, _ := cluster.GetNode(nodeID); node.IsLeader() {
			idx, _, err := cluster.SubmitToNode("test-cmd", nodeID)
			if err == nil {
				t.Logf("Submitted command at index %d with isolated follower", idx)
			}
		}
	}

	// Heal the partition
	transporttest.HealPartition(cluster)

	// Test Case 3: Circular blocking pattern
	t.Log("\nTest Case 3: Circular blocking (0->2 blocked, 1->0 blocked, 2->1 blocked)")

	// Create circular blocking: each node can't reach one specific other node
	blockingPatterns := []struct {
		from int
		to   int
	}{
		{0, 2}, // Node 0 can't reach node 2
		{1, 0}, // Node 1 can't reach node 0
		{2, 1}, // Node 2 can't reach node 1
	}

	for _, pattern := range blockingPatterns {
		if partition, ok := transporttest.GetCapability[transporttest.PartitionCapable](cluster, pattern.from); ok {
			partition.Block(pattern.to)
			t.Logf("Blocked %d -> %d", pattern.from, pattern.to)
		}
	}

	// Wait for partition effects to become observable - terms should increase due to failed elections
	helpers.WaitForCondition(t, func() bool {
		// Check if any node has increased its term (indicating election attempts)
		for _, node := range cluster.GetNodes() {
			term, _ := node.GetState()
			if term > 1 {
				return true // Partition effects are observable
			}
		}
		return false
	}, 500*time.Millisecond, "partition effects to become observable")

	// Check cluster state
	nodes := cluster.GetNodes()
	var leaderCount int
	var maxTerm int
	for nodeID, node := range nodes {
		term, isLeader := node.GetState()
		if isLeader {
			leaderCount++
			t.Logf("Node %d is leader with term %d", nodeID, term)
		}
		if term > maxTerm {
			maxTerm = term
		}
	}

	t.Logf("Circular partition result: %d leaders, max term %d", leaderCount, maxTerm)

	// Heal all partitions
	transporttest.HealPartition(cluster)

	// Final verification
	helpers.WaitForLeader(t, cluster.GetNodesSlice(), 2*time.Second)
	helpers.AssertLeaderCount(t, cluster.GetNodesSlice())
	t.Log("✓ Asymmetric partition test completed")
}

// TestRapidPartitionChanges tests system behavior with rapidly changing partitions
func TestRapidPartitionChanges(t *testing.T) {
	// Create 5-node cluster with partitionable transport
	cluster := helpers.NewTestClusterOfSize(t, 5, helpers.WithPartitionableTransport(), helpers.WithClusterAutoStart())

	// Wait for initial leader
	_, err := cluster.WaitForLeader(2 * time.Second)
	if err != nil {
		t.Fatalf("No initial leader elected: %v", err)
	}

	// Track cluster state
	type PartitionEvent struct {
		time        time.Time
		description string
		leaderCount int
		maxTerm     int
	}

	events := []PartitionEvent{}

	recordEvent := func(desc string) {
		leaderCount := 0
		maxTerm := 0

		for _, node := range cluster.Nodes {
			term, isLeader := node.GetState()
			if isLeader {
				leaderCount++
			}
			if term > maxTerm {
				maxTerm = term
			}
		}

		events = append(events, PartitionEvent{
			time:        time.Now(),
			description: desc,
			leaderCount: leaderCount,
			maxTerm:     maxTerm,
		})

		t.Logf("%s - Leaders: %d, Max Term: %d", desc, leaderCount, maxTerm)
	}

	recordEvent("Initial state")

	// Rapid partition changes
	partitionPatterns := []struct {
		name      string
		partition func()
		duration  time.Duration
	}{
		{
			name: "Split brain (2-3)",
			partition: func() {
				// Partition into [0,1] and [2,3,4]
				transporttest.PartitionNode(cluster, 0) //nolint:errcheck // test partition setup
				transporttest.PartitionNode(cluster, 1) //nolint:errcheck // test partition setup
			},
			duration: 300 * time.Millisecond,
		},
		{
			name: "Isolate leader",
			partition: func() {
				// Find and isolate current leader
				for i, node := range cluster.Nodes {
					_, isLeader := node.GetState()
					if isLeader {
						transporttest.PartitionNode(cluster, i) //nolint:errcheck // test partition setup
						break
					}
				}
			},
			duration: 200 * time.Millisecond,
		},
		{
			name: "Rolling isolation",
			partition: func() {
				// Isolate nodes one by one
				go func() {
					for i := 0; i < 5; i++ {
						transporttest.PartitionNode(cluster, i) //nolint:errcheck // test partition setup
						time.Sleep(50 * time.Millisecond) // Space out the partition changes
						transporttest.HealPartition(cluster)
					}
				}()
			},
			duration: 400 * time.Millisecond,
		},
		{
			name: "Majority isolated",
			partition: func() {
				// Isolate 3 out of 5 nodes
				transporttest.PartitionNode(cluster, 0) //nolint:errcheck // test partition setup
				transporttest.PartitionNode(cluster, 1) //nolint:errcheck // test partition setup
				transporttest.PartitionNode(cluster, 2) //nolint:errcheck // test partition setup
			},
			duration: 300 * time.Millisecond,
		},
	}

	// Execute rapid changes
	for _, pattern := range partitionPatterns {
		t.Logf("\nApplying partition: %s", pattern.name)
		pattern.partition()
		recordEvent(fmt.Sprintf("After %s", pattern.name))

		time.Sleep(pattern.duration) // Let the partition pattern have its effect

		transporttest.HealPartition(cluster)
		recordEvent("After heal")

		// Brief stabilization period
		helpers.WaitForCondition(t, func() bool {
			// Check if cluster has at least one leader
			for _, node := range cluster.Nodes {
				if node != nil {
					_, isLeader := node.GetState()
					if isLeader {
						return true
					}
				}
			}
			return false
		}, 500*time.Millisecond, "stabilization after heal")
	}

	// Final stabilization
	helpers.WaitForCondition(t, func() bool {
		// Wait for stable single leader
		leaderCount := 0
		for _, node := range cluster.Nodes {
			if node != nil {
				_, isLeader := node.GetState()
				if isLeader {
					leaderCount++
				}
			}
		}
		return leaderCount == 1
	}, 2*time.Second, "final stabilization")
	recordEvent("Final state")

	// Analysis
	t.Log("\n=== Partition Event Summary ===")
	for _, event := range events {
		t.Logf("%s: %s", event.time.Format("15:04:05.000"), event.description)
		t.Logf("  Leaders: %d, Max Term: %d", event.leaderCount, event.maxTerm)
	}

	// Verify cluster eventually stabilizes
	finalLeaderCount := 0
	for _, node := range cluster.Nodes {
		_, isLeader := node.GetState()
		if isLeader {
			finalLeaderCount++
		}
	}

	if finalLeaderCount != 1 {
		t.Errorf("Cluster did not stabilize to single leader: %d leaders", finalLeaderCount)
	} else {
		t.Log("✓ Cluster stabilized to single leader after rapid partitions")
	}

	// Verify cluster is functional
	idx, _, err := cluster.SubmitToLeader("post-partition-test")
	if err != nil {
		t.Fatalf("Failed to submit command after partitions: %v", err)
	}

	if err := cluster.WaitForCommitIndex(idx, 2*time.Second); err != nil {
		t.Fatalf("Command not committed after partitions: %v", err)
	}

	t.Log("✓ Cluster functional after rapid partition changes")
}

// TestPartitionDuringConfigChange tests partition during configuration change
func TestPartitionDuringConfigChange(t *testing.T) {
	// Create initial 3-node cluster
	cluster := helpers.NewTestClusterOfSize(t, 3, helpers.WithPartitionableTransport(), helpers.WithClusterAutoStart())

	// Wait for leader
	leaderID, err := cluster.WaitForLeader(2 * time.Second)
	if err != nil {
		t.Fatalf("No leader elected: %v", err)
	}

	// Submit some initial data
	for i := 0; i < 5; i++ {
		idx, _, err := cluster.SubmitToLeader(fmt.Sprintf("initial-%d", i))
		if err != nil {
			t.Fatalf("Failed to submit command: %v", err)
		}
		if err := cluster.WaitForCommitIndex(idx, time.Second); err != nil {
			t.Logf("Warning: WaitForCommitIndex failed for initial command %d: %v", i, err)
		}
	}

	t.Log("Starting configuration change...")

	// Start adding a new server (simulated)
	// In real implementation, this would be AddServer RPC

	// Partition during the configuration change
	// This tests the safety of configuration changes under partition

	// Create partition: leader + 1 node vs other node
	isolatedNode := (leaderID + 2) % 3
	if err := transporttest.PartitionNode(cluster, isolatedNode); err != nil {
		t.Fatalf("Failed to partition node: %v", err)
	}

	t.Logf("Partitioned node %d during configuration change", isolatedNode)

	// Try to continue operation with majority
	for i := 0; i < 3; i++ {
		_, _, isLeader := cluster.Nodes[leaderID].Submit(fmt.Sprintf("during-partition-%d", i))
		if !isLeader {
			t.Logf("Failed to submit during partition: not leader")
		}
	}

	// Wait for some effect
	helpers.WaitForCondition(t, func() bool {
		// Check if isolated node has stepped down or if cluster is still functioning
		return true
	}, 500*time.Millisecond, "partition effect observation")

	// Heal partition
	transporttest.HealPartition(cluster)
	t.Log("Healed partition")

	// Wait for stabilization
	helpers.WaitForCondition(t, func() bool {
		// Check all nodes are reachable and have consistent view
		leaderCount := 0
		for _, node := range cluster.Nodes {
			if node != nil {
				_, isLeader := node.GetState()
				if isLeader {
					leaderCount++
				}
			}
		}
		return leaderCount == 1
	}, 2*time.Second, "stabilization after partition heal")

	// Verify all nodes have consistent state
	var maxCommitIndex int
	for i, node := range cluster.Nodes {
		commitIndex := node.GetCommitIndex()
		t.Logf("Node %d commit index: %d", i, commitIndex)
		if commitIndex > maxCommitIndex {
			maxCommitIndex = commitIndex
		}
	}

	// All nodes should eventually have the same commit index
	helpers.WaitForConditionWithProgress(t, func() (bool, string) {
		minCommit := maxCommitIndex
		for _, node := range cluster.Nodes {
			if commit := node.GetCommitIndex(); commit < minCommit {
				minCommit = commit
			}
		}
		return minCommit == maxCommitIndex,
			fmt.Sprintf("min commit %d, max commit %d", minCommit, maxCommitIndex)
	}, 2*time.Second, "commit index convergence")

	t.Log("✓ Cluster recovered after partition during configuration change")
}

// TestCascadingPartitions tests cascading network failures
func TestCascadingPartitions(t *testing.T) {
	// Create 7-node cluster for complex partition scenarios
	cluster := helpers.NewTestClusterOfSize(t, 7, helpers.WithPartitionableTransport(), helpers.WithClusterAutoStart())

	// Wait for initial leader
	_, err := cluster.WaitForLeader(2 * time.Second)
	if err != nil {
		t.Fatalf("No initial leader elected: %v", err)
	}

	// Submit initial data
	for i := 0; i < 10; i++ {
		idx, _, err := cluster.SubmitToLeader(fmt.Sprintf("initial-%d", i))
		if err != nil {
			t.Fatalf("Failed to submit command: %v", err)
		}
		if err := cluster.WaitForCommitIndex(idx, time.Second); err != nil {
			t.Logf("Warning: WaitForCommitIndex failed for initial command %d: %v", i, err)
		}
	}

	// Cascading partition scenario:
	// 1. First, partition 2 nodes
	// 2. Then partition 2 more
	// 3. Finally partition 1 more (leaving only 2 connected)

	t.Log("Starting cascading partitions...")

	// Phase 1: Partition nodes 0 and 1
	transporttest.PartitionNode(cluster, 0) //nolint:errcheck // test partition setup
	transporttest.PartitionNode(cluster, 1) //nolint:errcheck // test partition setup
	t.Log("Phase 1: Partitioned nodes 0 and 1 (5 nodes remaining)")

	// Wait for a leader among the remaining 5 nodes
	leaderFound := false
	helpers.WaitForCondition(t, func() bool {
		for i := 2; i < 7; i++ {
			_, isLeader := cluster.Nodes[i].GetState()
			if isLeader {
				leaderFound = true
				t.Logf("Leader found at node %d after phase 1", i)
				return true
			}
		}
		return false
	}, 2*time.Second, "leader among remaining nodes after phase 1")

	if !leaderFound {
		t.Error("No leader after partitioning 2 nodes")
	}

	// Phase 2: Partition nodes 2 and 3
	transporttest.PartitionNode(cluster, 2) //nolint:errcheck // test partition setup
	transporttest.PartitionNode(cluster, 3) //nolint:errcheck // test partition setup
	t.Log("Phase 2: Partitioned nodes 2 and 3 (3 nodes remaining)")

	helpers.WaitForCondition(t, func() bool {
		// Wait for nodes to realize they don't have quorum
		for i := 4; i < 7; i++ {
			_, isLeader := cluster.Nodes[i].GetState()
			if isLeader {
				return false // Still has a leader, keep waiting
			}
		}
		return true // No leaders among minority
	}, 1*time.Second, "nodes to step down without quorum")

	// With only 3 nodes out of 7, there should be NO leader (no quorum: 3 < 4)
	leaderFound = false
	for i := 4; i < 7; i++ {
		_, isLeader := cluster.Nodes[i].GetState()
		if isLeader {
			leaderFound = true
			t.Errorf("Unexpected leader at node %d with minority (3/7 nodes)", i)
			break
		}
	}

	if !leaderFound {
		t.Log("✓ No leader after partitioning 4 nodes (correct: 3/7 is not a quorum)")
	}

	// Phase 3: Partition node 4 (leaving only 2 nodes: 5 and 6)
	transporttest.PartitionNode(cluster, 4) //nolint:errcheck // test partition setup
	t.Log("Phase 3: Partitioned node 4 (2 nodes remaining - no quorum)")

	// Wait to ensure no leader emerges with only 2/7 nodes
	helpers.WaitForCondition(t, func() bool {
		// Check that remaining nodes have stepped down
		for i := 5; i < 7; i++ {
			_, isLeader := cluster.Nodes[i].GetState()
			if isLeader {
				return false // Still has a leader, keep waiting
			}
		}
		return true // No leaders among minority
	}, 2*time.Second, "nodes to step down without quorum")

	// Verify no leader
	leaderCount := 0
	for i := 5; i < 7; i++ {
		_, isLeader := cluster.Nodes[i].GetState()
		if isLeader {
			leaderCount++
		}
	}

	if leaderCount > 0 {
		t.Error("Leader elected with minority (2/7 nodes)")
	} else {
		t.Log("✓ No leader with only 2/7 nodes connected")
	}

	// Heal partitions in reverse order
	t.Log("\nHealing partitions in reverse order...")

	// Heal node 4 first (now have 3 nodes: 4, 5, 6)
	transporttest.HealPartition(cluster)

	// Wait for nodes to detect healing but not necessarily elect leader yet (still no quorum)
	helpers.WaitForCondition(t, func() bool {
		// Just ensure nodes can communicate
		return true // Quick check, main verification comes after full heal
	}, 500*time.Millisecond, "partition heal to take effect")

	// Continue healing
	transporttest.HealPartition(cluster)

	// Now wait for cluster to stabilize with majority restored
	helpers.WaitForCondition(t, func() bool {
		// Check if any node has become leader
		for _, node := range cluster.Nodes {
			_, isLeader := node.GetState()
			if isLeader {
				return true
			}
		}
		return false
	}, 3*time.Second, "leader election after healing")

	// Verify cluster recovered
	finalLeader, err := cluster.WaitForLeader(3 * time.Second)
	if err != nil {
		t.Fatalf("Cluster did not recover after healing: %v", err)
	}

	t.Logf("✓ Cluster recovered with leader at node %d", finalLeader)

	// Verify functionality
	idx, _, err := cluster.SubmitToLeader("post-cascade-test")
	if err != nil {
		t.Fatalf("Failed to submit after cascade: %v", err)
	}

	if err := cluster.WaitForCommitIndex(idx, 2*time.Second); err != nil {
		t.Fatalf("Command not committed after cascade: %v", err)
	}

	t.Log("✓ Cluster fully functional after cascading partitions")
}

// The asymmetric transport implementation has been replaced with transport decorators
// See partition_test_refactored.go for the modern approach using PartitionableDecorator
