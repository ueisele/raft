package transporttest_test

import (
	"testing"
	"time"

	"github.com/ueisele/raft"
	"github.com/ueisele/raft/integration/helpers"
	"github.com/ueisele/raft/integration/helpers/transporttest"
)

// TestDelayDecorator tests basic delay functionality
func TestDelayDecorator(t *testing.T) {
	// Create a cluster with delay decorator
	cluster := helpers.NewTestCluster(t, []int{0, 1, 2},
		helpers.WithTransportDecorators(
			func(nodeID int, wrapped raft.Transport) raft.Transport {
				return transporttest.NewDelayDecorator(wrapped)
			},
		),
		helpers.WithClusterAutoStart(),
	)

	// Wait for initial leader election
	_, err := cluster.WaitForLeader(2 * time.Second)
	if err != nil {
		t.Fatalf("No leader elected: %v", err)
	}

	// Set a delay for all nodes
	transporttest.SetDelay(cluster, -1, 50*time.Millisecond)

	// Submit a command and measure time
	start := time.Now()
	idx, _, err := cluster.SubmitCommand("test")
	if err != nil {
		t.Fatalf("Failed to submit command: %v", err)
	}

	// Wait for commit
	err = cluster.WaitForCommitIndex(idx, 2*time.Second)
	if err != nil {
		t.Fatalf("Command not committed: %v", err)
	}
	elapsed := time.Since(start)

	// With 50ms delay, replication should take noticeably longer
	// We expect at least one round of AppendEntries with delay
	if elapsed < 50*time.Millisecond {
		t.Errorf("Expected delay of at least 50ms, but got %v", elapsed)
	}
	t.Logf("Command committed with delay in %v", elapsed)

	// Clear delays
	transporttest.ClearAllDelays(cluster)

	// Submit another command without delay
	start = time.Now()
	idx, _, err = cluster.SubmitCommand("test2")
	if err != nil {
		t.Fatalf("Failed to submit command: %v", err)
	}

	err = cluster.WaitForCommitIndex(idx, 1*time.Second)
	if err != nil {
		t.Fatalf("Command not committed: %v", err)
	}
	elapsed2 := time.Since(start)

	t.Logf("Command committed without delay in %v", elapsed2)

	// Without delay should be faster (though this can be flaky)
	if elapsed2 > elapsed {
		t.Logf("Warning: Command without delay took longer (%v) than with delay (%v)", elapsed2, elapsed)
	}
}

// TestDelayBetweenNodes tests asymmetric delays between specific nodes
func TestDelayBetweenNodes(t *testing.T) {
	// Create a 3-node cluster with delay capability
	cluster := helpers.NewTestCluster(t, []int{0, 1, 2},
		helpers.WithTransportDecorators(
			func(nodeID int, wrapped raft.Transport) raft.Transport {
				return transporttest.NewDelayDecorator(wrapped)
			},
		),
		helpers.WithClusterAutoStart(),
	)

	// Wait for leader
	leaderID, err := cluster.WaitForLeader(2 * time.Second)
	if err != nil {
		t.Fatalf("No leader elected: %v", err)
	}

	// Set delay only from leader to follower 1
	follower1 := (leaderID + 1) % 3
	transporttest.SetDelayBetween(cluster, leaderID, follower1, 100*time.Millisecond)

	// Verify the delay is set correctly
	if delay, ok := transporttest.GetCapability[transporttest.DelayCapable](cluster, leaderID); ok {
		if d := delay.GetDelay(follower1); d != 100*time.Millisecond {
			t.Errorf("Expected delay of 100ms to follower %d, got %v", follower1, d)
		}
		// Check no delay to other follower
		follower2 := (leaderID + 2) % 3
		if d := delay.GetDelay(follower2); d != 0 {
			t.Errorf("Expected no delay to follower %d, got %v", follower2, d)
		}
	} else {
		t.Fatal("Transport doesn't support delays")
	}

	// Submit commands and verify they still work
	for i := 0; i < 5; i++ {
		idx, _, err := cluster.SubmitCommand(i)
		if err == nil {
			cluster.WaitForCommitIndex(idx, 1*time.Second) //nolint:errcheck
		}
	}

	// Clear delays
	transporttest.ClearAllDelays(cluster)
}

// TestSimulateSlowNetwork tests uniform network delay simulation
func TestSimulateSlowNetwork(t *testing.T) {
	cluster := helpers.NewTestCluster(t, []int{0, 1, 2},
		helpers.WithTransportDecorators(
			func(nodeID int, wrapped raft.Transport) raft.Transport {
				return transporttest.NewDelayDecorator(wrapped)
			},
		),
		helpers.WithClusterAutoStart(),
	)

	// Simulate slow network with 30ms delay everywhere
	transporttest.SimulateSlowNetwork(cluster, 30*time.Millisecond)

	// Verify all nodes have the global delay set
	for i := 0; i < 3; i++ {
		if delay, ok := transporttest.GetCapability[transporttest.DelayCapable](cluster, i); ok {
			// Check delay to all nodes (using -1 for global)
			if d := delay.GetDelay(-1); d != 30*time.Millisecond {
				t.Errorf("Node %d: expected global delay of 30ms, got %v", i, d)
			}
		}
	}

	// Election should still work but take longer
	start := time.Now()
	_, err := cluster.WaitForLeader(5 * time.Second)
	if err != nil {
		t.Fatalf("No leader elected with slow network: %v", err)
	}
	elapsed := time.Since(start)
	t.Logf("Leader elected with slow network in %v", elapsed)

	// Clear delays
	transporttest.ClearAllDelays(cluster)
}

// TestAsymmetricDelay tests asymmetric delay where one node has slow outbound connections
func TestAsymmetricDelay(t *testing.T) {
	cluster := helpers.NewTestCluster(t, []int{0, 1, 2},
		helpers.WithTransportDecorators(
			func(nodeID int, wrapped raft.Transport) raft.Transport {
				return transporttest.NewDelayDecorator(wrapped)
			},
		),
		helpers.WithClusterAutoStart(),
	)

	// Make node 1 have slow outbound connections
	transporttest.SimulateAsymmetricDelay(cluster, 1, 75*time.Millisecond)

	// Verify node 1 has delay to all nodes
	if delay, ok := transporttest.GetCapability[transporttest.DelayCapable](cluster, 1); ok {
		if d := delay.GetDelay(-1); d != 75*time.Millisecond {
			t.Errorf("Node 1: expected global delay of 75ms, got %v", d)
		}
	}

	// Other nodes should have no delays
	for i := 0; i < 3; i++ {
		if i == 1 {
			continue
		}
		if delay, ok := transporttest.GetCapability[transporttest.DelayCapable](cluster, i); ok {
			if d := delay.GetDelay(-1); d != 0 {
				t.Errorf("Node %d: expected no delay, got %v", i, d)
			}
		}
	}

	// Wait for leader election
	leaderID, err := cluster.WaitForLeader(3 * time.Second)
	if err != nil {
		t.Fatalf("No leader elected: %v", err)
	}

	// Node 1 with slow outbound is less likely to become leader
	// but it's not deterministic, so we just log it
	if leaderID == 1 {
		t.Log("Node 1 became leader despite slow outbound connections")
	} else {
		t.Logf("Node %d became leader (node 1 has slow outbound)", leaderID)
	}

	// Submit some commands
	for i := 0; i < 3; i++ {
		idx, _, err := cluster.SubmitCommand(i)
		if err == nil {
			cluster.WaitForCommitIndex(idx, 2*time.Second) //nolint:errcheck
		}
	}
}

// TestDelayWithMultipleDecorators tests delay capability with other decorators
func TestDelayWithMultipleDecorators(t *testing.T) {
	// Create a cluster with multiple decorators including delay
	cluster := helpers.NewTestCluster(t, []int{0, 1, 2},
		helpers.WithTransportDecorators(
			func(nodeID int, wrapped raft.Transport) raft.Transport {
				return transporttest.NewDelayDecorator(wrapped)
			},
			func(nodeID int, wrapped raft.Transport) raft.Transport {
				return transporttest.NewPartitionableDecorator(wrapped)
			},
			func(nodeID int, wrapped raft.Transport) raft.Transport {
				return transporttest.NewFailureDecorator(wrapped, 0.1)
			},
		),
		helpers.WithClusterAutoStart(),
	)

	// Verify delay capability is accessible through decorator chain
	if _, ok := transporttest.GetCapability[transporttest.DelayCapable](cluster, 0); !ok {
		t.Error("Transport should support delays even with multiple decorators")
	}

	// Test setting and getting delays
	transporttest.SetDelay(cluster, -1, 25*time.Millisecond)

	if delay, ok := transporttest.GetCapability[transporttest.DelayCapable](cluster, 0); ok {
		if d := delay.GetDelay(-1); d != 25*time.Millisecond {
			t.Errorf("Expected delay of 25ms, got %v", d)
		}
	}

	// Verify all capabilities work together
	if _, ok := transporttest.GetCapability[transporttest.PartitionCapable](cluster, 0); !ok {
		t.Error("Should also support partitioning")
	}
	if _, ok := transporttest.GetCapability[transporttest.FailureCapable](cluster, 0); !ok {
		t.Error("Should also support failures")
	}

	// Clean up
	transporttest.ClearAllDelays(cluster)
}

// TestDelayPersistence tests that delays persist across operations
func TestDelayPersistence(t *testing.T) {
	cluster := helpers.NewTestCluster(t, []int{0, 1, 2},
		helpers.WithTransportDecorators(
			func(nodeID int, wrapped raft.Transport) raft.Transport {
				return transporttest.NewDelayDecorator(wrapped)
			},
		),
		helpers.WithClusterAutoStart(),
	)

	// Set different delays
	transporttest.SetDelayBetween(cluster, 0, 1, 10*time.Millisecond)
	transporttest.SetDelayBetween(cluster, 0, 2, 20*time.Millisecond)
	transporttest.SetDelayBetween(cluster, 1, 2, 30*time.Millisecond)

	// Verify delays persist
	if delay, ok := transporttest.GetCapability[transporttest.DelayCapable](cluster, 0); ok {
		if d := delay.GetDelay(1); d != 10*time.Millisecond {
			t.Errorf("Node 0->1 delay: expected 10ms, got %v", d)
		}
		if d := delay.GetDelay(2); d != 20*time.Millisecond {
			t.Errorf("Node 0->2 delay: expected 20ms, got %v", d)
		}
	}

	if delay, ok := transporttest.GetCapability[transporttest.DelayCapable](cluster, 1); ok {
		if d := delay.GetDelay(2); d != 30*time.Millisecond {
			t.Errorf("Node 1->2 delay: expected 30ms, got %v", d)
		}
	}

	// Clear specific delay
	transporttest.SetDelayBetween(cluster, 0, 1, 0)

	if delay, ok := transporttest.GetCapability[transporttest.DelayCapable](cluster, 0); ok {
		if d := delay.GetDelay(1); d != 0 {
			t.Errorf("Node 0->1 delay should be cleared, got %v", d)
		}
		// Other delay should remain
		if d := delay.GetDelay(2); d != 20*time.Millisecond {
			t.Errorf("Node 0->2 delay should remain 20ms, got %v", d)
		}
	}

	// Clear all delays
	transporttest.ClearAllDelays(cluster)

	// Verify all cleared
	for i := 0; i < 3; i++ {
		if delay, ok := transporttest.GetCapability[transporttest.DelayCapable](cluster, i); ok {
			for j := 0; j < 3; j++ {
				if d := delay.GetDelay(j); d != 0 {
					t.Errorf("Node %d->%d delay should be 0, got %v", i, j, d)
				}
			}
		}
	}
}