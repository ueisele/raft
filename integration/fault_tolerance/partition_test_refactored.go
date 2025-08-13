package fault_tolerance

import (
	"testing"
	"time"

	"github.com/ueisele/raft"
	"github.com/ueisele/raft/integration/helpers"
	"github.com/ueisele/raft/integration/helpers/transporttest"
)

// TestAsymmetricPartitionRefactored tests asymmetric network partitions using transport decorators
func TestAsymmetricPartitionRefactored(t *testing.T) {
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

	// Leader should still be able to send heartbeats
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

	// This creates an interesting scenario where each node can reach some but not all peers
	helpers.WaitForCondition(t, func() bool {
		return true
	}, 500*time.Millisecond, "observing circular partition")

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
	helpers.WaitForLeader(t, getNodesList(cluster), 2*time.Second)
	helpers.AssertLeaderCount(t, getNodesList(cluster))
	t.Log("✓ Asymmetric partition test completed")
}

// Helper to convert map to slice for compatibility with existing helpers
func getNodesList(cluster *helpers.TestCluster) []raft.Node {
	nodes := cluster.GetNodes()
	nodesList := make([]raft.Node, 0, len(nodes))
	for _, node := range nodes {
		nodesList = append(nodesList, node)
	}
	return nodesList
}
