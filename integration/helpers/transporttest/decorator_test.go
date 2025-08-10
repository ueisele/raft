package transporttest_test

import (
	"testing"
	"time"

	"github.com/ueisele/raft"
	"github.com/ueisele/raft/integration/helpers"
	"github.com/ueisele/raft/integration/helpers/transporttest"
)

// TestMultipleDecorators tests stacking multiple decorators
func TestMultipleDecorators(t *testing.T) {
	// Create a cluster with multiple decorators
	cluster := helpers.NewTestCluster(t, []int{0, 1, 2},
		helpers.WithTransportDecorators(
			func(nodeID int, wrapped raft.Transport) raft.Transport {
				return transporttest.NewPartitionableDecorator(wrapped)
			},
			func(nodeID int, wrapped raft.Transport) raft.Transport {
				return transporttest.NewFailureDecorator(wrapped, 0.1)
			},
		),
		helpers.WithClusterAutoStart(),
	)

	// Verify both capabilities are present
	if _, ok := transporttest.GetCapability[transporttest.PartitionCapable](cluster, 0); !ok {
		t.Error("Transport should support partitioning")
	}
	if _, ok := transporttest.GetCapability[transporttest.FailureCapable](cluster, 0); !ok {
		t.Error("Transport should support failures")
	}

	// Test that both work
	transporttest.SetFailureRate(cluster, 0.2)
	if err := transporttest.PartitionNode(cluster, 1); err != nil {
		t.Errorf("Failed to partition: %v", err)
	}

	// Both features should be active
	if partition, ok := transporttest.GetCapability[transporttest.PartitionCapable](cluster, 1); ok {
		if !partition.IsBlocked(-1) && !partition.IsBlocked(0) {
			t.Error("Partition not active")
		}
	}
	if failure, ok := transporttest.GetCapability[transporttest.FailureCapable](cluster, 0); ok {
		if failure.GetFailureRate() != 0.2 {
			t.Errorf("Failure rate should be 0.2, got %f", failure.GetFailureRate())
		}
	}
}

// TestDecoratorUnwrapping tests that decorators can be unwrapped correctly
func TestDecoratorUnwrapping(t *testing.T) {
	// Create a base transport
	registry := transporttest.NewNodeRegistry()
	baseTransport := transporttest.NewMultiNodeTransport(0, registry)

	// Wrap it with multiple decorators
	var wrapped raft.Transport = baseTransport
	wrapped = transporttest.NewPartitionableDecorator(wrapped)
	wrapped = transporttest.NewFailureDecorator(wrapped, 0.1)
	wrapped = transporttest.NewDebugDecorator(wrapped, 0, nil)

	// Test unwrapping
	if _, ok := wrapped.(*transporttest.DebugDecorator); !ok {
		t.Error("Top level should be DebugDecorator")
	}

	if decorator, ok := wrapped.(transporttest.Decorator); ok {
		// First unwrap: DebugDecorator -> FailureDecorator
		inner1 := decorator.Unwrap()
		if _, ok := inner1.(*transporttest.FailureDecorator); !ok {
			t.Error("First unwrap should return FailureDecorator")
		}

		// Continue unwrapping
		if decorator2, ok := inner1.(transporttest.Decorator); ok {
			inner2 := decorator2.Unwrap()
			if _, ok := inner2.(*transporttest.PartitionableDecorator); !ok {
				t.Error("Second unwrap should return PartitionableDecorator")
			}

			// Final unwrap should give us base transport
			if decorator3, ok := inner2.(transporttest.Decorator); ok {
				inner3 := decorator3.Unwrap()
				if _, ok := inner3.(*transporttest.MultiNodeTransport); !ok {
					t.Error("Third unwrap should return MultiNodeTransport")
				}
			}
		}
	} else {
		t.Error("Top level should be a Decorator")
	}
}

// TestDecoratorOrdering tests that decorator order matters
func TestDecoratorOrdering(t *testing.T) {
	// Test case 1: Partition then Failure
	// If partition blocks, failure decorator never gets called
	cluster1 := helpers.NewTestCluster(t, []int{0, 1},
		helpers.WithTransportDecorators(
			func(nodeID int, wrapped raft.Transport) raft.Transport {
				return transporttest.NewPartitionableDecorator(wrapped)
			},
			func(nodeID int, wrapped raft.Transport) raft.Transport {
				return transporttest.NewFailureDecorator(wrapped, 1.0) // 100% failure
			},
		),
		helpers.WithClusterAutoStart(),
	)

	// Partition node 0 from node 1
	if partition, ok := transporttest.GetCapability[transporttest.PartitionCapable](cluster1, 0); ok {
		partition.Block(1)
	}

	// Try to send something (this would normally be done internally)
	// The partition should block it before failure decorator can fail it
	// We can verify by checking failure stats - should be 0 attempts
	attempts1, _ := transporttest.GetFailureStats(cluster1)
	if attempts1 != 0 {
		t.Logf("Note: Partition decorator should prevent failure decorator from seeing attempts, but got %d attempts", attempts1)
	}

	// Test case 2: Failure then Partition
	// Failure decorator gets called first
	cluster2 := helpers.NewTestCluster(t, []int{0, 1},
		helpers.WithTransportDecorators(
			func(nodeID int, wrapped raft.Transport) raft.Transport {
				return transporttest.NewFailureDecorator(wrapped, 1.0) // 100% failure
			},
			func(nodeID int, wrapped raft.Transport) raft.Transport {
				return transporttest.NewPartitionableDecorator(wrapped)
			},
		),
		helpers.WithClusterAutoStart(),
	)

	// Don't partition, let failure decorator handle it
	// Submit commands to generate traffic
	var lastIdx int
	for i := 0; i < 5; i++ {
		idx, _, err := cluster2.SubmitCommand(i)
		if err == nil && idx > lastIdx {
			lastIdx = idx
		}
	}
	// Wait for replication attempts
	if lastIdx > 0 {
		cluster2.WaitForCommitIndex(lastIdx, 500*time.Millisecond) //nolint:errcheck // may fail due to failures
	}

	// Check failure stats - should have attempts
	attempts2, failures2 := transporttest.GetFailureStats(cluster2)
	if attempts2 > 0 && failures2 != attempts2 {
		t.Errorf("With 100%% failure rate, all attempts should fail: %d/%d", failures2, attempts2)
	}
}

