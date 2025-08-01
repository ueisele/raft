package raft

import (
	"context"
	"fmt"
	"testing"
	"time"
	// "github.com/ueisele/raft/test" - removed to avoid import cycle
)

// ExampleClientInteraction shows how clients should properly interact with Raft
func TestExampleClientInteraction(t *testing.T) {
	// Setup a 3-node cluster (abbreviated for clarity)
	nodes, cleanup := setupTestCluster(t, 3)
	defer cleanup()

	// Find the leader
	var leader Node
	for _, node := range nodes {
		if node.IsLeader() {
			leader = node
			break
		}
	}

	if leader == nil {
		t.Fatal("No leader found")
	}

	// Example 1: Proper client pattern with commit waiting
	t.Run("ProperClientPattern", func(t *testing.T) {
		// Client submits command
		command := "SET x=1"
		index, term, isLeader := leader.Submit(command)

		if !isLeader {
			t.Fatal("Node is not leader anymore")
		}

		t.Logf("Command submitted at index=%d, term=%d", index, term)

		// Client MUST wait for commit
		WaitForConditionWithProgress(t, func() (bool, string) {
			commitIndex := leader.GetCommitIndex()
			if commitIndex >= index {
				return true, fmt.Sprintf("command committed at index %d", commitIndex)
			}
			return false, fmt.Sprintf("waiting for commit, current index %d, need %d", commitIndex, index)
		}, 5*time.Second, "command commit")

		t.Logf("Command committed! CommitIndex=%d", leader.GetCommitIndex())
	})

	// Example 2: What happens if leader crashes
	t.Run("LeaderCrashBeforeCommit", func(t *testing.T) {
		// This demonstrates why Raft keeps uncommitted entries

		// Client submits command
		command := "SET y=2"
		index, _, isLeader := leader.Submit(command)

		if !isLeader {
			t.Fatal("Node is not leader")
		}

		// Simulate leader crash immediately after submit
		// (In real scenario, this might happen after partial replication)
		leader.Stop()

		// Wait for new leader
		timing := DefaultTimingConfig()
		timing.ElectionTimeout = 2 * time.Second
		var newLeader Node
		WaitForConditionWithProgress(t, func() (bool, string) {
			for _, node := range nodes {
				if node.IsLeader() {
					newLeader = node
					return true, "new leader elected"
				}
			}
			return false, "waiting for new leader"
		}, timing.ElectionTimeout, "new leader election")

		if newLeader == nil {
			t.Fatal("No new leader elected")
		}

		// The new leader might have the entry (if it was replicated)
		// Client must retry or check if command was applied
		logLength := newLeader.GetLogLength()
		t.Logf("New leader log length: %d (original index was %d)", logLength, index)

		// In a real system, client would:
		// 1. Retry the command with new leader
		// 2. Check if original command was committed (using unique ID)
	})
}

// RaftClient shows a proper client implementation
type RaftClient struct {
	cluster []Node
}

// SubmitAndWait submits a command and waits for commit
func (c *RaftClient) SubmitAndWait(cmd interface{}, timeout time.Duration) error {
	// Find leader
	var leader Node
	for _, node := range c.cluster {
		if node.IsLeader() {
			leader = node
			break
		}
	}

	if leader == nil {
		return fmt.Errorf("no leader found")
	}

	// Submit command
	index, _, isLeader := leader.Submit(cmd)
	if !isLeader {
		return fmt.Errorf("node is not leader")
	}

	// Wait for commit
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("timeout waiting for commit")
		case <-ticker.C:
			if leader.GetCommitIndex() >= index {
				return nil // Success!
			}
			// Check if still leader
			if !leader.IsLeader() {
				return fmt.Errorf("leader changed during commit")
			}
		}
	}
}

// setupTestCluster creates a test cluster
func setupTestCluster(t *testing.T, size int) ([]Node, func()) {
	nodes := make([]Node, size)

	// Create transports that can communicate
	registry := &nodeRegistry{
		nodes: make(map[int]RPCHandler),
	}

	// Create all nodes
	for i := 0; i < size; i++ {
		config := &Config{
			ID:                 i,
			Peers:              makeRange(0, size),
			ElectionTimeoutMin: 150 * time.Millisecond,
			ElectionTimeoutMax: 300 * time.Millisecond,
			HeartbeatInterval:  50 * time.Millisecond,
		}

		transport := &multiNodeTransport{
			id:       i,
			registry: registry,
		}

		stateMachine := &testStateMachine{
			data: make(map[string]string),
		}

		node, err := NewNode(config, transport, nil, stateMachine)
		if err != nil {
			t.Fatalf("Failed to create node %d: %v", i, err)
		}

		nodes[i] = node
	}

	// Register all nodes with their transports
	for i, node := range nodes {
		registry.nodes[i] = node.(RPCHandler)
	}

	// Start all nodes
	ctx := context.Background()
	for i, node := range nodes {
		if err := node.Start(ctx); err != nil {
			t.Fatalf("Failed to start node %d: %v", i, err)
		}
	}

	// Wait for leader election
	waitForLeader(t, nodes)

	cleanup := func() {
		for _, node := range nodes {
			node.Stop()
		}
	}

	return nodes, cleanup
}

// makeRange creates a slice of ints from 0 to n-1
func makeRange(start, end int) []int {
	result := make([]int, end-start)
	for i := range result {
		result[i] = start + i
	}
	return result
}

// waitForLeader waits for a single leader to be elected
func waitForLeader(t *testing.T, nodes []Node) {
	timing := DefaultTimingConfig()
	timing.ElectionTimeout = 3 * time.Second
	WaitForConditionWithProgress(t, func() (bool, string) {
		leaderCount := 0
		for _, node := range nodes {
			_, isLeader := node.GetState()
			if isLeader {
				leaderCount++
			}
		}

		if leaderCount == 1 {
			return true, "single leader elected"
		}

		if leaderCount > 1 {
			t.Fatalf("Multiple leaders detected: %d", leaderCount)
		}

		return false, fmt.Sprintf("%d leaders", leaderCount)
	}, timing.ElectionTimeout, "leader election")
}
