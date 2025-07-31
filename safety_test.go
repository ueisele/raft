package raft

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

// TestElectionSafety verifies that at most one leader can be elected in a given term
func TestElectionSafety(t *testing.T) {
	// Create a 5-node cluster
	numNodes := 5
	nodes := make([]Node, numNodes)
	registry := &debugNodeRegistry{
		nodes:  make(map[int]RPCHandler),
		logger: newTestLogger(t),
	}

	for i := 0; i < numNodes; i++ {
		config := &Config{
			ID:                 i,
			Peers:              []int{0, 1, 2, 3, 4},
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

	// Track leaders per term
	leadersPerTerm := make(map[int][]int)
	mu := sync.Mutex{}

	// Monitor for 5 seconds, checking leadership
	done := make(chan bool)
	go func() {
		for i := 0; i < 50; i++ {
			time.Sleep(100 * time.Millisecond)

			mu.Lock()
			for id, node := range nodes {
				term, isLeader := node.GetState()
				if isLeader {
					if leadersPerTerm[term] == nil {
						leadersPerTerm[term] = []int{}
					}

					// Check if this node is already recorded as leader for this term
					found := false
					for _, leaderID := range leadersPerTerm[term] {
						if leaderID == id {
							found = true
							break
						}
					}

					if !found {
						leadersPerTerm[term] = append(leadersPerTerm[term], id)
					}
				}
			}
			mu.Unlock()
		}
		done <- true
	}()

	<-done

	// Verify election safety: at most one leader per term
	mu.Lock()
	defer mu.Unlock()

	for term, leaders := range leadersPerTerm {
		if len(leaders) > 1 {
			t.Errorf("Election safety violated: term %d has %d leaders: %v",
				term, len(leaders), leaders)
		}
	}

	t.Logf("Election safety verified. Leaders per term: %v", leadersPerTerm)
}

// TestLogMatchingProperty verifies that logs are consistent across nodes
func TestLogMatchingProperty(t *testing.T) {
	// Create a 3-node cluster
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

	// Submit multiple commands
	commands := []string{"cmd1", "cmd2", "cmd3", "cmd4", "cmd5"}
	for _, cmd := range commands {
		index, term, isLeader := leader.Submit(cmd)
		if !isLeader {
			t.Fatal("Lost leadership during command submission")
		}
		t.Logf("Submitted %s at index %d, term %d", cmd, index, term)
	}

	// Wait for replication
	time.Sleep(1 * time.Second)

	// Verify all nodes have the same commit index
	commitIndices := make(map[int]int)
	for i, node := range nodes {
		commitIndices[i] = node.GetCommitIndex()
	}

	// All nodes should have the same commit index
	expectedCommitIndex := commitIndices[leaderID]
	for i, idx := range commitIndices {
		if idx != expectedCommitIndex {
			t.Errorf("Node %d has commit index %d, expected %d", i, idx, expectedCommitIndex)
		}
	}

	t.Logf("All nodes have commit index %d", expectedCommitIndex)
}

// TestLeaderCompleteness verifies that a leader has all committed entries
func TestLeaderCompleteness(t *testing.T) {
	// Create a 5-node cluster with partitionable transport
	numNodes := 5
	nodes := make([]Node, numNodes)
	transports := make([]*partitionableTransport, numNodes)
	registry := &partitionRegistry{
		nodes: make(map[int]RPCHandler),
		mu:    sync.RWMutex{},
	}

	for i := 0; i < numNodes; i++ {
		config := &Config{
			ID:                 i,
			Peers:              []int{0, 1, 2, 3, 4},
			ElectionTimeoutMin: 150 * time.Millisecond,
			ElectionTimeoutMax: 300 * time.Millisecond,
			HeartbeatInterval:  50 * time.Millisecond,
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

	// Wait for initial leader election
	time.Sleep(500 * time.Millisecond)

	// Find initial leader
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
		t.Fatal("No initial leader elected")
	}

	t.Logf("Initial leader is node %d", leaderID)

	// Submit commands
	for i := 0; i < 3; i++ {
		cmd := fmt.Sprintf("command-%d", i)
		index, _, isLeader := leader.Submit(cmd)
		if !isLeader {
			t.Fatal("Lost leadership during command submission")
		}
		t.Logf("Submitted %s at index %d", cmd, index)
	}

	// Wait for replication
	time.Sleep(500 * time.Millisecond)

	// Record the commit index
	initialCommitIndex := leader.GetCommitIndex()
	t.Logf("Initial commit index: %d", initialCommitIndex)

	// Partition the leader to force new election
	for i := 0; i < numNodes; i++ {
		if i != leaderID {
			transports[leaderID].Block(i)
			transports[i].Block(leaderID)
		}
	}

	t.Log("Leader partitioned, waiting for new election")

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
		t.Fatal("No new leader elected after partition")
	}

	t.Logf("New leader is node %d", newLeaderID)

	// Verify new leader has at least the same commit index
	newLeaderCommitIndex := newLeader.GetCommitIndex()
	if newLeaderCommitIndex < initialCommitIndex {
		t.Errorf("New leader has commit index %d, but previous leader had %d",
			newLeaderCommitIndex, initialCommitIndex)
	}

	t.Logf("Leader completeness verified: new leader commit index %d >= old leader commit index %d",
		newLeaderCommitIndex, initialCommitIndex)
}
