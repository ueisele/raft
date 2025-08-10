package transporttest_test

import (
	"testing"
	"time"

	"github.com/ueisele/raft/integration/helpers"
	"github.com/ueisele/raft/integration/helpers/transporttest"
)

// TestWithDelayTransportOption tests the WithDelayTransport cluster option
func TestWithDelayTransportOption(t *testing.T) {
	// Create a cluster using the WithDelayTransport option
	cluster := helpers.NewTestCluster(t, 3,
		helpers.WithDelayTransport(),
		helpers.WithClusterAutoStart(),
	)

	// Verify delay capability is available
	if _, ok := transporttest.GetCapability[transporttest.DelayCapable](cluster, 0); !ok {
		t.Fatal("Transport should support delays when using WithDelayTransport option")
	}

	// Set a delay and verify it works
	transporttest.SetDelay(cluster, -1, 25*time.Millisecond)

	// Verify delay is set
	for i := 0; i < 3; i++ {
		if delay, ok := transporttest.GetCapability[transporttest.DelayCapable](cluster, i); ok {
			if d := delay.GetDelay(-1); d != 25*time.Millisecond {
				t.Errorf("Node %d: expected delay of 25ms, got %v", i, d)
			}
		}
	}

	// Clean up
	transporttest.ClearAllDelays(cluster)
}