package cluster

import (
	"context"
	"testing"
	"time"

	"github.com/ueisele/raft"
	"github.com/ueisele/raft/integration/helpers"
)

// TestLeaderElection tests basic leader election
func TestLeaderElection(t *testing.T) {
	// Create a 3-node cluster
	cluster := helpers.NewTestCluster(t, 3)

	// Start cluster
	if err := cluster.Start(); err != nil {
		t.Fatalf("Failed to start cluster: %v", err)
	}

	// Wait for leader election
	leaderID, err := cluster.WaitForLeader(2 * time.Second)
	if err != nil {
		t.Fatalf("No leader elected: %v", err)
	}

	// Verify only one leader
	leaderCount := 0
	for _, node := range cluster.Nodes {
		if node.IsLeader() {
			leaderCount++
		}
	}

	if leaderCount != 1 {
		t.Errorf("Expected exactly 1 leader, got %d", leaderCount)
	}

	// Verify all nodes are in the same term
	leaderTerm := cluster.Nodes[leaderID].GetCurrentTerm()
	for i, node := range cluster.Nodes {
		if node.GetCurrentTerm() != leaderTerm {
			t.Errorf("Node %d has term %d, expected %d", i, node.GetCurrentTerm(), leaderTerm)
		}
	}
}

// TestLeaderFailover tests leader failover when current leader fails
func TestLeaderFailover(t *testing.T) {
	// Create a 5-node cluster for better fault tolerance
	cluster := helpers.NewTestCluster(t, 5)

	// Start cluster
	if err := cluster.Start(); err != nil {
		t.Fatalf("Failed to start cluster: %v", err)
	}

	// Wait for initial leader
	initialLeaderID, err := cluster.WaitForLeader(2 * time.Second)
	if err != nil {
		t.Fatalf("No initial leader elected: %v", err)
	}

	initialTerm := cluster.Nodes[initialLeaderID].GetCurrentTerm()
	t.Logf("Initial leader is node %d (term %d)", initialLeaderID, initialTerm)

	// Submit some commands to establish leadership
	for i := 0; i < 5; i++ {
		idx, _, err := cluster.SubmitCommand(i)
		if err != nil {
			t.Fatalf("Failed to submit command: %v", err)
		}

		// Wait for command to be committed
		if err := cluster.WaitForCommitIndex(idx, time.Second); err != nil {
			t.Fatalf("Command not committed: %v", err)
		}
	}

	// Stop the leader
	cluster.Nodes[initialLeaderID].Stop()
	t.Logf("Stopped leader node %d", initialLeaderID)

	// Wait for new leader election
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var newLeaderID int
	for {
		select {
		case <-ctx.Done():
			t.Fatal("Timeout waiting for new leader")
		case <-time.After(100 * time.Millisecond):
			// Check for new leader
			leaderCount := 0
			for i, node := range cluster.Nodes {
				if i == initialLeaderID {
					continue // Skip old leader
				}
				if node.IsLeader() {
					newLeaderID = i
					leaderCount++
				}
			}

			if leaderCount == 1 {
				// New leader elected
				goto elected
			}
		}
	}

elected:
	newTerm := cluster.Nodes[newLeaderID].GetCurrentTerm()
	t.Logf("New leader is node %d (term %d)", newLeaderID, newTerm)

	// Verify new term is higher
	if newTerm <= initialTerm {
		t.Errorf("New term %d should be higher than initial term %d", newTerm, initialTerm)
	}

	// Verify the new leader can process commands
	idx, _, err := cluster.SubmitCommand("test-after-failover")
	if err != nil {
		t.Fatalf("New leader failed to accept command: %v", err)
	}

	// Wait for command to be committed (only on active nodes)
	activeNodes := make([]raft.Node, 0, len(cluster.Nodes)-1)
	for i, node := range cluster.Nodes {
		if i != initialLeaderID {
			activeNodes = append(activeNodes, node)
		}
	}

	helpers.WaitForCommitIndex(t, activeNodes, idx, 2*time.Second)
}
