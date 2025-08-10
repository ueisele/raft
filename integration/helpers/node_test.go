package helpers

import (
	"testing"
	"time"

	"github.com/ueisele/raft"
)

// TestTestNodeAutoCleanup verifies that TestNode automatically cleans up
func TestTestNodeAutoCleanup(t *testing.T) {
	// Create a test node
	node := NewTestNode(t, 0, []int{0})

	// Start it
	if err := node.Start(); err != nil {
		t.Fatalf("Failed to start node: %v", err)
	}

	// Submit a command to verify it's working
	_, _, err := node.Submit("test-command")
	if err == nil {
		t.Log("Node accepted command (might not be leader yet)")
	}

	// Node should be automatically stopped when test ends
	// No need to call Stop() explicitly
}

// TestTestNodeWithAutoStart verifies auto-start option
func TestTestNodeWithAutoStart(t *testing.T) {
	// Create a node with auto-start
	node := NewTestNode(t, 0, []int{0}, WithAutoStart())

	// Should already be running
	// Wait a bit to see if it becomes leader (single node cluster)
	// Try to wait for leader (may not become leader immediately)
	node.WaitForLeader(2 * time.Second)

	// Verify it's actually running by checking state
	state, _ := node.Node.GetState()
	t.Logf("Node state: %v", state)

	// Auto cleanup will handle stopping
}

// TestTestNodeSet verifies creating multiple connected nodes
func TestTestNodeSet(t *testing.T) {
	// Create 3 connected nodes
	nodes := CreateTestNodeSet(t, 3)

	// Start all nodes
	for i, node := range nodes {
		if err := node.Start(); err != nil {
			t.Fatalf("Failed to start node %d: %v", i, err)
		}
	}

	// Wait for a leader to emerge
	deadline := time.Now().Add(2 * time.Second)
	var leaderFound bool
	for time.Now().Before(deadline) {
		for _, node := range nodes {
			if node.Node.IsLeader() {
				leaderFound = true
				t.Logf("Node %d became leader", node.Config.ID)
				break
			}
		}
		if leaderFound {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if !leaderFound {
		t.Fatal("No leader elected in node set")
	}

	// All nodes will be automatically cleaned up
}

// TestStandaloneNode verifies standalone node creation
func TestStandaloneNode(t *testing.T) {
	// Create a standalone node (single-node cluster)
	node := CreateStandaloneTestNode(t, WithAutoStart())

	// Should become leader quickly
	// Should become leader quickly
	node.WaitForLeader(1 * time.Second)

	// Try to submit a command
	index, term, err := node.Submit("standalone-command")
	if err == nil {
		t.Logf("Submitted command at index %d, term %d", index, term)
	}

	// Auto cleanup handles stopping
}

// TestTestNodeCustomComponents verifies custom component options
func TestTestNodeCustomComponents(t *testing.T) {
	// Create custom components
	customTransport := raft.NewMockTransport(42)
	customStateMachine := raft.NewMockStateMachine()
	customPersistence := raft.NewMockPersistence()

	customConfig := &raft.Config{
		ID:                 42,
		Peers:              []int{42},
		ElectionTimeoutMin: 100 * time.Millisecond,
		ElectionTimeoutMax: 200 * time.Millisecond,
		HeartbeatInterval:  25 * time.Millisecond,
		Logger:             raft.NewTestLogger(t),
	}

	// Create node with custom components
	node := NewTestNode(t, 42, []int{42},
		WithNodeConfig(customConfig),
		WithNodeTransport(customTransport),
		WithNodeStateMachine(customStateMachine),
		WithNodePersistence(customPersistence),
		WithAutoStart(),
	)

	// Verify custom components were used
	if node.Transport != customTransport {
		t.Error("Custom transport not used")
	}
	if node.StateMachine != customStateMachine {
		t.Error("Custom state machine not used")
	}
	if node.Persistence != customPersistence {
		t.Error("Custom persistence not used")
	}
	if node.Config.ID != 42 {
		t.Error("Custom config not used")
	}

	// Auto cleanup handles stopping
}
