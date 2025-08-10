package helpers_test

import (
	"context"
	"testing"
	"time"

	"github.com/ueisele/raft"
	"github.com/ueisele/raft/integration/helpers"
)

// TestExampleSingleNode demonstrates using TestNode for a simple single-node test
func TestExampleSingleNode(t *testing.T) {
	// Create a single node that automatically cleans up
	node := helpers.NewTestNode(t, 0, []int{0}, helpers.WithAutoStart())

	// Wait for it to become leader
	node.WaitForLeader(2 * time.Second)

	// Submit commands
	index, term, err := node.Submit("my-command")
	if err != nil {
		t.Fatalf("Failed to submit: %v", err)
	}

	// Wait for commit
	node.WaitForCommitIndex(index, time.Second)

	t.Logf("Command committed at index %d, term %d", index, term)
	// Node automatically stops when test ends - no cleanup needed!
}

// TestExampleMultiNode demonstrates using CreateTestNodeSet for multi-node test
func TestExampleMultiNode(t *testing.T) {
	// Create 3 nodes that can communicate with each other
	nodes := helpers.CreateTestNodeSet(t, 3, helpers.WithAutoStart())

	// Wait for a leader to emerge
	var leader *helpers.TestNode
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		for _, node := range nodes {
			if node.Node.IsLeader() {
				leader = node
				break
			}
		}
		if leader != nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if leader == nil {
		t.Fatal("No leader elected")
	}

	// Submit command through leader
	index, _, err := leader.Submit("cluster-command")
	if err != nil {
		t.Fatalf("Failed to submit: %v", err)
	}

	// Wait for replication to all nodes
	for i, node := range nodes {
		node.WaitForCommitIndex(index, time.Second)
		t.Logf("Node %d replicated command at index %d", i, index)
	}

	// All nodes automatically stop when test ends!
}

// TestExampleClusterWithAutoStart demonstrates using TestCluster with auto-start
func TestExampleClusterWithAutoStart(t *testing.T) {
	// Create a cluster that automatically starts all nodes
	cluster := helpers.NewTestCluster(t, []int{0, 1, 2}, helpers.WithClusterAutoStart())

	// Since nodes are auto-started, we can immediately wait for a leader
	leaderID, err := cluster.WaitForLeader(2 * time.Second)
	if err != nil {
		t.Fatalf("No leader elected: %v", err)
	}

	t.Logf("Leader elected: node %d", leaderID)

	// Submit a command
	index, term, err := cluster.SubmitCommand("auto-started-command")
	if err != nil {
		t.Fatalf("Failed to submit: %v", err)
	}

	// Wait for replication
	cluster.WaitForCommitIndex(index, time.Second)
	t.Logf("Command committed at index %d, term %d", index, term)

	// Cluster automatically stops when test ends!
}

// Example showing the before and after difference
func TestComparisonOldVsNew(t *testing.T) {
	t.Run("Old way - manual cleanup", func(t *testing.T) {
		// OLD WAY - lots of manual setup, cleanup, and waiting boilerplate
		config := &raft.Config{
			ID:                 0,
			Peers:              []int{0},
			ElectionTimeoutMin: 150 * time.Millisecond,
			ElectionTimeoutMax: 300 * time.Millisecond,
			HeartbeatInterval:  50 * time.Millisecond,
			Logger:             raft.NewTestLogger(t),
		}

		transport := raft.NewMockTransport(0)
		stateMachine := raft.NewMockStateMachine()

		node, err := raft.NewNode(config, transport, nil, stateMachine)
		if err != nil {
			t.Fatalf("Failed to create node: %v", err)
		}

		// Must remember to clean up manually
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			node.Stop(ctx) //nolint:errcheck
		})

		// Must manually start the node
		ctx := context.Background()
		if err := node.Start(ctx); err != nil {
			t.Fatalf("Failed to start: %v", err)
		}

		// Must manually wait for leader with custom logic
		timeout := 2 * time.Second
		deadline := time.Now().Add(timeout)
		for time.Now().Before(deadline) {
			if node.IsLeader() {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		if !node.IsLeader() {
			t.Fatal("Node failed to become leader")
		}

		// Submit a command and manually wait for commit
		index, _, isLeader := node.Submit("test-command")
		if !isLeader {
			t.Fatal("Node is not leader")
		}

		// Manually poll for commit
		commitDeadline := time.Now().Add(time.Second)
		for time.Now().Before(commitDeadline) {
			if node.GetCommitIndex() >= index {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		if node.GetCommitIndex() < index {
			t.Fatal("Command not committed")
		}

		t.Logf("OLD WAY: Command committed at index %d", index)
	})

	t.Run("New way - automatic cleanup", func(t *testing.T) {
		// NEW WAY - clean and simple with helper methods!
		node := helpers.CreateStandaloneTestNode(t, helpers.WithAutoStart())

		// Wait for leadership - one line with timeout
		node.WaitForLeader(time.Second)

		// Submit command and wait for commit - clean and simple
		index, _, err := node.Submit("test-command")
		if err != nil {
			t.Fatalf("Failed to submit: %v", err)
		}
		node.WaitForCommitIndex(index, time.Second)

		t.Logf("NEW WAY: Command committed at index %d", index)
		// No manual cleanup needed - happens automatically!
	})
}
