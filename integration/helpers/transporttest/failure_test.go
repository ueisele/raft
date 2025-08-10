package transporttest_test

import (
	"testing"
	"time"

	"github.com/ueisele/raft"
	"github.com/ueisele/raft/integration/helpers"
	"github.com/ueisele/raft/integration/helpers/transporttest"
)

// TestFailureDecorator tests the failure decorator functionality
func TestFailureDecorator(t *testing.T) {
	// Create a cluster with failure transport
	cluster := helpers.NewTestCluster(t, 3,
		helpers.WithFailureTransport(0.5), // 50% failure rate
		helpers.WithClusterAutoStart(),
	)

	// Wait for leader to ensure cluster is operational
	_, err := cluster.WaitForLeader(2 * time.Second)
	if err != nil {
		t.Fatalf("No leader elected: %v", err)
	}

	// Submit some commands to generate traffic
	var lastIndex int
	for i := 0; i < 10; i++ {
		idx, _, err := cluster.SubmitCommand(i)
		if err == nil && idx > lastIndex {
			lastIndex = idx
		}
	}

	// Wait for replication attempts if we got any successful submissions
	if lastIndex > 0 {
		cluster.WaitForCommitIndex(lastIndex, 500*time.Millisecond) //nolint:errcheck // may fail due to failures
	}

	// Check failure statistics
	attempts, failures := helpers.GetFailureStats(cluster)
	if attempts == 0 {
		t.Error("No attempts recorded")
	}
	if failures == 0 {
		t.Log("Warning: No failures occurred (this could happen by chance)")
	}

	t.Logf("Failure stats: %d failures out of %d attempts (%.1f%%)",
		failures, attempts, float64(failures)/float64(attempts)*100)

	// Set failure rate to 0
	helpers.SetFailureRate(cluster, 0.0)

	// Submit more commands - should have no failures
	beforeAttempts, beforeFailures := helpers.GetFailureStats(cluster)
	lastIndex = 0
	for i := 0; i < 10; i++ {
		idx, _, err := cluster.SubmitCommand(i)
		if err == nil && idx > lastIndex {
			lastIndex = idx
		}
	}
	// Wait for commits to ensure replication attempts
	if lastIndex > 0 {
		cluster.WaitForCommitIndex(lastIndex, 500*time.Millisecond) //nolint:errcheck
	}
	afterAttempts, afterFailures := helpers.GetFailureStats(cluster)

	newFailures := afterFailures - beforeFailures
	newAttempts := afterAttempts - beforeAttempts
	if newFailures > 0 {
		t.Errorf("Should have no failures with 0%% rate, but had %d/%d", newFailures, newAttempts)
	}
}

// TestFailureRateAdjustment tests dynamic failure rate adjustment
func TestFailureRateAdjustment(t *testing.T) {
	// Create a cluster with initial 0% failure rate
	cluster := helpers.NewTestCluster(t, 3,
		helpers.WithFailureTransport(0.0),
		helpers.WithClusterAutoStart(),
	)

	// Verify initial failure rate
	for i := 0; i < 3; i++ {
		if failure, ok := helpers.GetTransportCapability[transporttest.FailureCapable](cluster, i); ok {
			if rate := failure.GetFailureRate(); rate != 0.0 {
				t.Errorf("Node %d: expected initial failure rate 0.0, got %f", i, rate)
			}
		}
	}

	// Change failure rate to 30%
	helpers.SetFailureRate(cluster, 0.3)

	// Verify new failure rate
	for i := 0; i < 3; i++ {
		if failure, ok := helpers.GetTransportCapability[transporttest.FailureCapable](cluster, i); ok {
			if rate := failure.GetFailureRate(); rate != 0.3 {
				t.Errorf("Node %d: expected failure rate 0.3, got %f", i, rate)
			}
		}
	}

	// Generate some traffic
	var lastIdx int
	for i := 0; i < 20; i++ {
		idx, _, err := cluster.SubmitCommand(i)
		if err == nil && idx > lastIdx {
			lastIdx = idx
		}
	}
	// Wait for replication attempts
	if lastIdx > 0 {
		cluster.WaitForCommitIndex(lastIdx, 500*time.Millisecond) //nolint:errcheck // may fail due to failures
	}

	attempts, failures := helpers.GetFailureStats(cluster)
	if attempts > 0 {
		actualRate := float64(failures) / float64(attempts)
		t.Logf("Actual failure rate: %.2f (%d/%d)", actualRate, failures, attempts)

		// With 30% failure rate, we expect some failures but not all
		if failures == 0 || failures == attempts {
			t.Logf("Warning: Unexpected failure count with 30%% rate: %d/%d", failures, attempts)
		}
	}
}

// TestFailureStatsReset tests resetting failure statistics
func TestFailureStatsReset(t *testing.T) {
	cluster := helpers.NewTestCluster(t, 3,
		helpers.WithFailureTransport(0.5),
		helpers.WithClusterAutoStart(),
	)

	// Wait for cluster to stabilize
	_, err := cluster.WaitForLeader(2 * time.Second)
	if err != nil {
		t.Fatalf("No leader elected: %v", err)
	}

	// Generate some traffic
	var lastIdx int
	for i := 0; i < 10; i++ {
		idx, _, err := cluster.SubmitCommand(i)
		if err == nil && idx > lastIdx {
			lastIdx = idx
		}
	}
	// Wait for replication attempts
	if lastIdx > 0 {
		cluster.WaitForCommitIndex(lastIdx, 500*time.Millisecond) //nolint:errcheck // may fail due to failures
	}

	// Check we have some stats
	attempts1, _ := helpers.GetFailureStats(cluster)
	if attempts1 == 0 {
		t.Fatal("No attempts recorded before reset")
	}

	// Reset stats
	for i := 0; i < 3; i++ {
		if failure, ok := helpers.GetTransportCapability[transporttest.FailureCapable](cluster, i); ok {
			failure.ResetStats()
		}
	}

	// Verify stats are reset
	attempts2, failures2 := helpers.GetFailureStats(cluster)
	if attempts2 != 0 || failures2 != 0 {
		t.Errorf("Stats not reset: attempts=%d, failures=%d", attempts2, failures2)
	}
}

// TestFailureWithMultipleDecorators tests failure capability with other decorators
func TestFailureWithMultipleDecorators(t *testing.T) {
	// Create a cluster with multiple decorators including failure
	cluster := helpers.NewTestCluster(t, 3,
		helpers.WithTransportDecorators(
			func(nodeID int, wrapped raft.Transport) raft.Transport {
				return transporttest.NewFailureDecorator(wrapped, 0.2)
			},
			func(nodeID int, wrapped raft.Transport) raft.Transport {
				return transporttest.NewPartitionableDecorator(wrapped)
			},
		),
		helpers.WithClusterAutoStart(),
	)

	// Verify failure capability is accessible
	if _, ok := helpers.GetTransportCapability[transporttest.FailureCapable](cluster, 0); !ok {
		t.Error("Transport should support failures even with multiple decorators")
	}

	// Test failure rate adjustment works
	helpers.SetFailureRate(cluster, 0.1)

	if failure, ok := helpers.GetTransportCapability[transporttest.FailureCapable](cluster, 0); ok {
		if rate := failure.GetFailureRate(); rate != 0.1 {
			t.Errorf("Expected failure rate 0.1, got %f", rate)
		}
	}
}

