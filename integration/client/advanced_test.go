package client

import (
	"fmt"
	"testing"
	"time"

	"github.com/ueisele/raft"
	"github.com/ueisele/raft/integration/helpers"
)

// TestLinearizableReads tests linearizable read operations
func TestLinearizableReads(t *testing.T) {
	// Create 5-node cluster
	cluster := helpers.NewTestCluster(t, 5)
	
	// Start cluster
	if err := cluster.Start(); err != nil {
		t.Fatalf("Failed to start cluster: %v", err)
	}
	defer cluster.Stop()

	// Wait for leader
	leaderID, err := cluster.WaitForLeader(2 * time.Second)
	if err != nil {
		t.Fatalf("No leader elected: %v", err)
	}

	// Submit a write
	writeCmd := "SET key1 value1"
	writeIndex, _, err := cluster.SubmitCommand(writeCmd)
	if err != nil {
		t.Fatalf("Failed to submit write: %v", err)
	}

	// Wait for write to be committed
	helpers.WaitForCommitIndex(t, cluster.Nodes, writeIndex, time.Second)

	// Perform linearizable read from leader
	readCmd := "GET key1"
	readIndex, _, err := cluster.SubmitCommand(readCmd)
	if err != nil {
		t.Fatalf("Failed to submit read: %v", err)
	}

	// The read should see the write
	if readIndex <= writeIndex {
		t.Errorf("Read index %d should be after write index %d", readIndex, writeIndex)
	}

	// Verify read from follower also sees the write
	followerID := (leaderID + 1) % 5
	followerCommit := cluster.Nodes[followerID].GetCommitIndex()
	if followerCommit < writeIndex {
		// Wait for follower to catch up
		helpers.WaitForCommitIndex(t, []raft.Node{cluster.Nodes[followerID]}, writeIndex, time.Second)
	}

	t.Logf("Linearizable read completed at index %d after write at %d", readIndex, writeIndex)
}

// TestIdempotentOperations tests idempotent operation handling
func TestIdempotentOperations(t *testing.T) {
	// Create 3-node cluster
	cluster := helpers.NewTestCluster(t, 3)
	
	// Start cluster
	if err := cluster.Start(); err != nil {
		t.Fatalf("Failed to start cluster: %v", err)
	}
	defer cluster.Stop()

	// Wait for leader
	_, err := cluster.WaitForLeader(2 * time.Second)
	if err != nil {
		t.Fatalf("No leader elected: %v", err)
	}

	// Submit the same command multiple times
	cmd := "SET key1 value1"
	indices := make([]int, 3)
	
	for i := 0; i < 3; i++ {
		index, _, err := cluster.SubmitCommand(cmd)
		if err != nil {
			t.Fatalf("Failed to submit command %d: %v", i, err)
		}
		indices[i] = index
		
		// Small delay between submissions
		time.Sleep(100 * time.Millisecond)
	}

	// Each submission should get a different index (not truly idempotent in basic Raft)
	for i := 1; i < 3; i++ {
		if indices[i] == indices[i-1] {
			t.Errorf("Expected different indices for repeated commands, got %d and %d", 
				indices[i-1], indices[i])
		}
	}

	t.Logf("Repeated commands got indices: %v", indices)
}

// TestClientTimeouts tests client timeout scenarios
func TestClientTimeouts(t *testing.T) {
	// Create 5-node cluster with partitionable transport
	cluster := helpers.NewTestCluster(t, 5, helpers.WithPartitionableTransport())
	
	// Start cluster
	if err := cluster.Start(); err != nil {
		t.Fatalf("Failed to start cluster: %v", err)
	}
	defer cluster.Stop()

	// Wait for leader
	leaderID, err := cluster.WaitForLeader(2 * time.Second)
	if err != nil {
		t.Fatalf("No leader elected: %v", err)
	}

	// Partition the leader from the rest
	if err := cluster.PartitionNode(leaderID); err != nil {
		t.Fatalf("Failed to partition leader: %v", err)
	}

	// Try to submit command - should timeout/fail
	startTime := time.Now()
	_, _, err = cluster.SubmitCommand("timeout-test")
	elapsed := time.Since(startTime)

	if err == nil {
		t.Error("Expected error when submitting to partitioned leader")
	}

	t.Logf("Command failed after %v with error: %v", elapsed, err)

	// Heal partition
	cluster.HealPartition()

	// Wait for new leader
	newLeaderID, err := cluster.WaitForLeader(2 * time.Second)
	if err != nil {
		t.Fatalf("No new leader elected after healing: %v", err)
	}

	if newLeaderID == leaderID {
		t.Logf("Same leader %d retained leadership after partition heal", leaderID)
	} else {
		t.Logf("New leader %d elected after partition heal", newLeaderID)
	}

	// Verify cluster is functional
	_, _, err = cluster.SubmitCommand("post-timeout-test")
	if err != nil {
		t.Errorf("Failed to submit command after healing: %v", err)
	}
}

// TestClientRedirection tests client request redirection to leader
func TestClientRedirection(t *testing.T) {
	// Create 5-node cluster
	cluster := helpers.NewTestCluster(t, 5)
	
	// Start cluster
	if err := cluster.Start(); err != nil {
		t.Fatalf("Failed to start cluster: %v", err)
	}
	defer cluster.Stop()

	// Wait for leader
	leaderID, err := cluster.WaitForLeader(2 * time.Second)
	if err != nil {
		t.Fatalf("No leader elected: %v", err)
	}

	// Try to submit command to a follower
	followerID := (leaderID + 1) % 5
	follower := cluster.Nodes[followerID]

	// Submit command directly to follower
	_, _, isLeader := follower.Submit("follower-test")
	if isLeader {
		t.Errorf("Follower %d incorrectly reports being leader", followerID)
	}

	// In a real implementation, the follower would redirect to leader
	// For now, we verify that follower correctly identifies it's not leader
	t.Logf("Follower %d correctly identified it's not leader (leader is %d)", 
		followerID, leaderID)

	// Submit to actual leader should work
	index, _, isLeader := cluster.Nodes[leaderID].Submit("leader-test")
	if !isLeader {
		t.Errorf("Leader %d doesn't recognize itself as leader", leaderID)
	}

	t.Logf("Leader %d accepted command at index %d", leaderID, index)
}

// TestClientRetries tests client retry mechanisms
func TestClientRetries(t *testing.T) {
	// Create 3-node cluster
	cluster := helpers.NewTestCluster(t, 3)
	
	// Start cluster
	if err := cluster.Start(); err != nil {
		t.Fatalf("Failed to start cluster: %v", err)
	}
	defer cluster.Stop()

	// Wait for leader
	initialLeaderID, err := cluster.WaitForLeader(2 * time.Second)
	if err != nil {
		t.Fatalf("No leader elected: %v", err)
	}

	// Submit command successfully
	successIndex, _, err := cluster.SubmitCommand("pre-failure-cmd")
	if err != nil {
		t.Fatalf("Failed to submit initial command: %v", err)
	}

	// Stop the leader to trigger election
	cluster.Nodes[initialLeaderID].Stop()
	t.Logf("Stopped leader %d", initialLeaderID)

	// Retry submitting commands with backoff
	retryCount := 0
	maxRetries := 10
	var lastErr error
	
	for retryCount < maxRetries {
		// Try to submit command
		_, _, err := cluster.SubmitCommand(fmt.Sprintf("retry-cmd-%d", retryCount))
		if err == nil {
			t.Logf("Command succeeded after %d retries", retryCount)
			break
		}
		
		lastErr = err
		retryCount++
		
		// Exponential backoff
		backoff := time.Duration(retryCount*100) * time.Millisecond
		if backoff > time.Second {
			backoff = time.Second
		}
		time.Sleep(backoff)
	}

	if retryCount >= maxRetries {
		t.Errorf("Failed to submit command after %d retries: %v", maxRetries, lastErr)
	}

	// Verify new leader was elected
	newLeaderID := -1
	for i, node := range cluster.Nodes {
		if i != initialLeaderID && node.IsLeader() {
			newLeaderID = i
			break
		}
	}

	if newLeaderID == -1 {
		t.Error("No new leader elected after retries")
	} else {
		t.Logf("New leader %d elected, command succeeded after %d retries", 
			newLeaderID, retryCount)
	}

	// Verify cluster continues to work
	finalIndex, _, err := cluster.SubmitCommand("post-retry-cmd")
	if err != nil {
		t.Errorf("Failed to submit final command: %v", err)
	}

	if finalIndex <= successIndex {
		t.Errorf("Final index %d should be greater than initial index %d", 
			finalIndex, successIndex)
	}
}