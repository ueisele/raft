package transporttest_test

import (
	"testing"
	"time"

	"github.com/ueisele/raft"
	"github.com/ueisele/raft/integration/helpers"
	"github.com/ueisele/raft/integration/helpers/transporttest"
)

// TestPartitionableDecorator tests the partitionable decorator functionality
func TestPartitionableDecorator(t *testing.T) {
	// Create a cluster with partitionable transport
	cluster := helpers.NewTestCluster(t, 3,
		helpers.WithPartitionableTransport(),
		helpers.WithClusterAutoStart(),
	)

	// Wait for leader
	leaderID, err := cluster.WaitForLeader(2 * time.Second)
	if err != nil {
		t.Fatalf("No leader elected: %v", err)
	}

	// Use the helper function to partition a node
	followerID := (leaderID + 1) % 3
	if err := transporttest.PartitionNode(cluster, followerID); err != nil {
		t.Fatalf("Failed to partition node: %v", err)
	}

	// Verify the node is partitioned by checking capability
	if partition, ok := transporttest.GetCapability[transporttest.PartitionCapable](cluster, followerID); ok {
		// Check if it's blocked from all nodes
		if !partition.IsBlocked(-1) && !partition.IsBlocked(leaderID) {
			t.Error("Node should be blocked but isn't")
		}
	} else {
		t.Fatal("Transport doesn't support partitioning")
	}

	// Heal the partition
	transporttest.HealPartition(cluster)

	// Verify partition is healed
	if partition, ok := transporttest.GetCapability[transporttest.PartitionCapable](cluster, followerID); ok {
		if partition.IsBlocked(leaderID) {
			t.Error("Node should not be blocked after healing")
		}
	}
}

// TestPartitionGroups tests creating partitions between groups of nodes
func TestPartitionGroups(t *testing.T) {
	// Create a 5-node cluster
	cluster := helpers.NewTestCluster(t, 5,
		helpers.WithPartitionableTransport(),
		helpers.WithClusterAutoStart(),
	)

	// Wait for initial leader
	_, err := cluster.WaitForLeader(2 * time.Second)
	if err != nil {
		t.Fatalf("No leader elected: %v", err)
	}

	// Create partition: [0,1] vs [2,3,4]
	group1 := []int{0, 1}
	group2 := []int{2, 3, 4}
	if err := transporttest.CreatePartition(cluster, group1, group2); err != nil {
		t.Fatalf("Failed to create partition: %v", err)
	}

	// Verify nodes in group1 cannot reach group2
	for _, id1 := range group1 {
		if partition, ok := transporttest.GetCapability[transporttest.PartitionCapable](cluster, id1); ok {
			for _, id2 := range group2 {
				if !partition.IsBlocked(id2) {
					t.Errorf("Node %d should be blocked from node %d", id1, id2)
				}
			}
		}
	}

	// Verify nodes in group2 cannot reach group1
	for _, id2 := range group2 {
		if partition, ok := transporttest.GetCapability[transporttest.PartitionCapable](cluster, id2); ok {
			for _, id1 := range group1 {
				if !partition.IsBlocked(id1) {
					t.Errorf("Node %d should be blocked from node %d", id2, id1)
				}
			}
		}
	}

	// Heal partition
	transporttest.HealPartition(cluster)

	// Verify all nodes can communicate
	for i := 0; i < 5; i++ {
		if partition, ok := transporttest.GetCapability[transporttest.PartitionCapable](cluster, i); ok {
			for j := 0; j < 5; j++ {
				if partition.IsBlocked(j) {
					t.Errorf("Node %d should not be blocked from node %d after healing", i, j)
				}
			}
		}
	}
}

// TestPartitionableWithMultipleDecorators tests partition capability with other decorators
func TestPartitionableWithMultipleDecorators(t *testing.T) {
	// Create a cluster with multiple decorators including partition
	cluster := helpers.NewTestCluster(t, 3,
		helpers.WithTransportDecorators(
			func(nodeID int, wrapped raft.Transport) raft.Transport {
				return transporttest.NewPartitionableDecorator(wrapped)
			},
			func(nodeID int, wrapped raft.Transport) raft.Transport {
				return transporttest.NewDebugDecorator(wrapped, nodeID, nil)
			},
		),
		helpers.WithClusterAutoStart(),
	)

	// Verify partition capability is accessible through decorator chain
	if _, ok := transporttest.GetCapability[transporttest.PartitionCapable](cluster, 0); !ok {
		t.Error("Transport should support partitioning even with multiple decorators")
	}

	// Test partition functionality works
	if err := transporttest.PartitionNode(cluster, 1); err != nil {
		t.Fatalf("Failed to partition node: %v", err)
	}

	if partition, ok := transporttest.GetCapability[transporttest.PartitionCapable](cluster, 1); ok {
		if !partition.IsBlocked(-1) {
			t.Error("Partition not active")
		}
	}

	// Clean up
	transporttest.HealPartition(cluster)
}
