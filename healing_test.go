package raft

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

// TestClusterHealing verifies that a cluster eventually converges after disruptions
func TestClusterHealing(t *testing.T) {
	// Create 3-node cluster
	nodes := make([]Node, 3)
	registry := &debugNodeRegistry{
		nodes:  make(map[int]RPCHandler),
		logger: newTestLogger(t),
	}

	for i := 0; i < 3; i++ {
		config := &Config{
			ID:                 i,
			Peers:              []int{0, 1, 2},
			ElectionTimeoutMin: 150 * time.Millisecond,
			ElectionTimeoutMax: 300 * time.Millisecond,
			HeartbeatInterval:  50 * time.Millisecond,
			Logger:             newTestLogger(t),
		}

		transport := &debugTransport{
			id:       i,
			registry: registry,
			logger:   newTestLogger(t),
		}

		stateMachine := &testStateMachine{
			mu:   sync.Mutex{},
			data: make(map[string]string),
		}

		node, err := NewNode(config, transport, nil, stateMachine)
		if err != nil {
			t.Fatalf("Failed to create node %d: %v", i, err)
		}

		nodes[i] = node
		registry.nodes[i] = node.(RPCHandler)
	}

	// Start all nodes
	ctx := context.Background()
	for i, node := range nodes {
		if err := node.Start(ctx); err != nil {
			t.Fatalf("Failed to start node %d: %v", i, err)
		}
		defer node.Stop()
	}

	// Wait for leader election
	time.Sleep(500 * time.Millisecond)

	// Find leader
	var leader Node
	var leaderID int
	for i, node := range nodes {
		if node.IsLeader() {
			leader = node
			leaderID = i
			break
		}
	}

	if leader == nil {
		t.Fatal("No leader elected")
	}

	t.Logf("Initial leader is node %d", leaderID)

	// Submit a command
	index1, term1, isLeader := leader.Submit("command-1")
	if !isLeader {
		t.Fatal("Not leader when submitting")
	}
	t.Logf("Submitted command-1 at index %d, term %d", index1, term1)

	// Wait a bit for replication
	time.Sleep(200 * time.Millisecond)

	// Force leader to step down by stopping it
	t.Logf("Stopping leader node %d", leaderID)
	nodes[leaderID].Stop()

	// Wait for new leader election
	time.Sleep(1 * time.Second)

	// Find new leader
	var newLeader Node
	var newLeaderID int
	for i, node := range nodes {
		if i != leaderID && node.IsLeader() {
			newLeader = node
			newLeaderID = i
			break
		}
	}

	if newLeader == nil {
		t.Fatal("No new leader elected")
	}

	t.Logf("New leader is node %d", newLeaderID)

	// Submit another command with new leader
	index2, term2, isLeader := newLeader.Submit("command-2")
	if !isLeader {
		t.Fatal("Not leader when submitting")
	}
	t.Logf("Submitted command-2 at index %d, term %d", index2, term2)

	// Wait for cluster to stabilize
	t.Log("Waiting for cluster to heal...")
	
	// Check convergence
	maxRetries := 20
	for retry := 0; retry < maxRetries; retry++ {
		time.Sleep(500 * time.Millisecond)
		
		// Get state of remaining nodes
		states := make(map[int]string)
		for i, node := range nodes {
			if i == leaderID {
				continue // Skip stopped node
			}
			commitIndex := node.GetCommitIndex()
			logLength := node.GetLogLength()
			states[i] = fmt.Sprintf("commit=%d,len=%d", commitIndex, logLength)
		}
		
		// Check if both remaining nodes have converged
		converged := true
		var firstState string
		for _, state := range states {
			if firstState == "" {
				firstState = state
			} else if state != firstState {
				converged = false
				break
			}
		}
		
		if converged {
			t.Logf("SUCCESS: Cluster converged after %d retries (%.1f seconds): %v", 
				retry, float64(retry)*0.5, states)
			
			// Verify they have both commands
			for i, node := range nodes {
				if i == leaderID {
					continue
				}
				if node.GetCommitIndex() < index2 {
					t.Errorf("Node %d hasn't committed all entries: commit=%d, expected>=%d",
						i, node.GetCommitIndex(), index2)
				}
			}
			return
		}
		
		if retry > 0 && retry % 5 == 0 {
			t.Logf("Retry %d: States: %v", retry, states)
		}
	}
	
	t.Fatal("Cluster failed to converge after timeout")
}

// TestClusterHealingWithUncommittedEntry tests healing when there's an uncommitted entry
func TestClusterHealingWithUncommittedEntry(t *testing.T) {
	// Create 3-node cluster
	nodes := make([]Node, 3)
	transports := make([]*partitionableTransport, 3)
	registry := &partitionRegistry{
		nodes: make(map[int]RPCHandler),
		mu:    sync.RWMutex{},
	}

	for i := 0; i < 3; i++ {
		config := &Config{
			ID:                 i,
			Peers:              []int{0, 1, 2},
			ElectionTimeoutMin: 150 * time.Millisecond,
			ElectionTimeoutMax: 300 * time.Millisecond,
			HeartbeatInterval:  50 * time.Millisecond,
			Logger:             newTestLogger(t),
		}

		transport := &partitionableTransport{
			id:       i,
			registry: registry,
			blocked:  make(map[int]bool),
		}
		transports[i] = transport

		stateMachine := &testStateMachine{
			mu:   sync.Mutex{},
			data: make(map[string]string),
		}

		node, err := NewNode(config, transport, nil, stateMachine)
		if err != nil {
			t.Fatalf("Failed to create node %d: %v", i, err)
		}

		nodes[i] = node
		registry.nodes[i] = node.(RPCHandler)
	}

	// Start all nodes
	ctx := context.Background()
	for i, node := range nodes {
		if err := node.Start(ctx); err != nil {
			t.Fatalf("Failed to start node %d: %v", i, err)
		}
		defer node.Stop()
	}

	// Wait for leader election
	time.Sleep(500 * time.Millisecond)

	// Find leader
	var leader Node
	var leaderID int
	for i, node := range nodes {
		if node.IsLeader() {
			leader = node
			leaderID = i
			break
		}
	}

	if leader == nil {
		t.Fatal("No leader elected")
	}

	t.Logf("Initial leader is node %d", leaderID)

	// Partition the leader from one follower before submitting
	followerToPartition := (leaderID + 1) % 3
	t.Logf("Partitioning leader %d from follower %d", leaderID, followerToPartition)
	transports[leaderID].Block(followerToPartition)
	transports[followerToPartition].Block(leaderID)

	// Submit a command - it will replicate to only one follower
	index, term, isLeader := leader.Submit("partially-replicated-command")
	if !isLeader {
		t.Fatal("Not leader when submitting")
	}
	t.Logf("Submitted command at index %d, term %d (only replicated to one follower)", index, term)

	// Wait a bit
	time.Sleep(200 * time.Millisecond)

	// Now partition the leader from everyone
	for i := 0; i < 3; i++ {
		if i != leaderID {
			transports[leaderID].Block(i)
			transports[i].Block(leaderID)
		}
	}
	t.Logf("Fully isolated leader %d", leaderID)

	// Wait for new leader election among remaining nodes
	time.Sleep(1 * time.Second)

	// Heal all partitions
	t.Log("Healing all partitions...")
	for i := 0; i < 3; i++ {
		for j := 0; j < 3; j++ {
			transports[i].Unblock(j)
		}
	}

	// The cluster should eventually converge
	// The uncommitted entry from the old leader should either be:
	// 1. Committed if the new leader has it
	// 2. Overwritten if the new leader doesn't have it

	t.Log("Waiting for cluster to heal after partition...")
	
	maxRetries := 30
	for retry := 0; retry < maxRetries; retry++ {
		time.Sleep(500 * time.Millisecond)
		
		// Get state of all nodes
		states := make(map[int]string)
		commitIndices := make([]int, 3)
		for i, node := range nodes {
			commitIndex := node.GetCommitIndex()
			logLength := node.GetLogLength()
			states[i] = fmt.Sprintf("commit=%d,len=%d", commitIndex, logLength)
			commitIndices[i] = commitIndex
		}
		
		// Check if all nodes have converged
		converged := true
		firstState := states[0]
		for i := 1; i < 3; i++ {
			if states[i] != firstState {
				converged = false
				break
			}
		}
		
		if converged {
			t.Logf("SUCCESS: Cluster converged after %d retries (%.1f seconds): %v", 
				retry, float64(retry)*0.5, states)
			t.Log("The cluster successfully healed after the partition!")
			return
		}
		
		if retry > 0 && retry % 5 == 0 {
			// Find current leader
			var currentLeader *int
			for i, node := range nodes {
				if node.IsLeader() {
					currentLeader = &i
					break
				}
			}
			
			if currentLeader != nil {
				t.Logf("Retry %d: States: %v, leader: node%d", retry, states, *currentLeader)
			} else {
				t.Logf("Retry %d: States: %v, NO LEADER", retry, states)
			}
		}
	}
	
	t.Fatal("Cluster failed to converge after timeout")
}
