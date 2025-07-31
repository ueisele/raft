package raft

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	// "github.com/ueisele/raft/test" - removed to avoid import cycle
)

// TestNewVotingServerSafety demonstrates the safety issue with immediately 
// allowing new voting servers to participate before they catch up
func TestNewVotingServerSafety(t *testing.T) {
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
			MaxLogSize:         1000,
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
	timing := DefaultTimingConfig()
	timing.ElectionTimeout = 500 * time.Millisecond
	leaderID := WaitForLeaderWithConfig(t, nodes, timing)
	if leaderID < 0 {
		t.Fatal("No leader elected")
	}
	leader := nodes[leaderID]

	// Submit some commands to build up the log
	for i := 0; i < 10; i++ {
		index, _, ok := leader.Submit(fmt.Sprintf("cmd-%d", i))
		if !ok {
			t.Fatal("Failed to submit command")
		}
		t.Logf("Submitted command %d at index %d", i, index)
	}

	// Wait for replication
	WaitForCommitIndexWithConfig(t, nodes, 10, timing)

	// Verify all nodes have the commands
	for i, node := range nodes {
		commitIndex := node.GetCommitIndex()
		if commitIndex < 10 {
			t.Errorf("Node %d has commit index %d, expected at least 10", i, commitIndex)
		}
	}

	// Now add a new VOTING server with empty log
	newNodeID := 3
	newConfig := &Config{
		ID:                 newNodeID,
		Peers:              []int{}, // Empty - will be updated by configuration change
		ElectionTimeoutMin: 150 * time.Millisecond,
		ElectionTimeoutMax: 300 * time.Millisecond,
		HeartbeatInterval:  50 * time.Millisecond,
		Logger:             newTestLogger(t),
		MaxLogSize:         1000,
	}

	newTransport := &debugTransport{
		id:       newNodeID,
		registry: registry,
		logger:   newTestLogger(t),
	}

	newStateMachine := &testStateMachine{
		mu:   sync.Mutex{},
		data: make(map[string]string),
	}

	newNode, err := NewNode(newConfig, newTransport, nil, newStateMachine)
	if err != nil {
		t.Fatalf("Failed to create new node: %v", err)
	}

	registry.nodes[newNodeID] = newNode.(RPCHandler)

	// Start new node
	if err := newNode.Start(ctx); err != nil {
		t.Fatalf("Failed to start new node: %v", err)
	}
	defer newNode.Stop()

	// Add as VOTING member immediately (dangerous!)
	err = leader.AddServer(newNodeID, fmt.Sprintf("server-%d", newNodeID), true)
	if err != nil {
		t.Fatalf("Failed to add voting server: %v", err)
	}

	// Wait for configuration change to propagate
	markerIndex, _, _ := leader.Submit("config-marker")
	WaitForCommitIndexWithConfig(t, nodes, markerIndex, timing)

	// Check new node's state
	newNodeCommitIndex := newNode.GetCommitIndex()
	t.Logf("New node commit index after joining: %d", newNodeCommitIndex)

	// Now cause an election by stopping the current leader
	currentLeaderID := leader.(*raftNode).config.ID
	nodes[currentLeaderID].Stop()
	t.Log("Stopped current leader to trigger election")

	// Wait for new election
	WaitForConditionWithProgress(t, func() (bool, string) {
		for i, node := range []Node{nodes[0], nodes[1], nodes[2], newNode} {
			if i == currentLeaderID {
				continue
			}
			if node.IsLeader() {
				return true, fmt.Sprintf("node %d is new leader", i)
			}
		}
		return false, "waiting for new leader"
	}, timing.ElectionTimeout*2, "new leader election")

	// Check who is the new leader
	var newLeader Node
	var newLeaderID int
	for i, node := range []Node{nodes[0], nodes[1], nodes[2], newNode} {
		if i == currentLeaderID {
			continue // Skip stopped node
		}
		if node.IsLeader() {
			newLeader = node
			if node == newNode {
				newLeaderID = newNodeID
			} else {
				// Find which of the original nodes it is
				for j, origNode := range nodes {
					if origNode == node {
						newLeaderID = j
						break
					}
				}
			}
			break
		}
	}

	if newLeader == nil {
		t.Fatal("No new leader elected after stopping old leader")
	}

	t.Logf("New leader is node %d", newLeaderID)

	// DANGER: If the new node (with empty log) became leader, it could
	// overwrite committed entries!
	if newLeaderID == newNodeID {
		t.Error("SAFETY VIOLATION: New node with incomplete log became leader!")
		t.Logf("New node had commit index %d but became leader", newNodeCommitIndex)
	}

	// Even if it didn't become leader, check if it could have affected quorum
	config := newLeader.GetConfiguration()
	votingCount := 0
	for _, server := range config.Servers {
		if server.Voting {
			votingCount++
		}
	}

	t.Logf("Voting members: %d, Majority needed: %d", votingCount, votingCount/2+1)
	
	// With 4 voting members, we need 3 for majority
	// The new node with empty log counts toward this!
	t.Log("WARNING: New node with empty log is counted in quorum calculations immediately")
}

// TestSaferApproach demonstrates a safer approach using non-voting members first
func TestSaferApproach(t *testing.T) {
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
			MaxLogSize:         1000,
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
	timing := DefaultTimingConfig()
	timing.ElectionTimeout = 500 * time.Millisecond
	leaderID := WaitForLeaderWithConfig(t, nodes, timing)
	if leaderID < 0 {
		t.Fatal("No leader elected")
	}
	leader := nodes[leaderID]

	// Submit some commands
	for i := 0; i < 10; i++ {
		leader.Submit(fmt.Sprintf("cmd-%d", i))
	}

	// Wait for replication
	WaitForCommitIndexWithConfig(t, nodes, 10, timing)

	leaderCommitIndex := leader.GetCommitIndex()
	t.Logf("Leader commit index: %d", leaderCommitIndex)

	// Create new node
	newNodeID := 3
	newConfig := &Config{
		ID:                 newNodeID,
		Peers:              []int{},
		ElectionTimeoutMin: 150 * time.Millisecond,
		ElectionTimeoutMax: 300 * time.Millisecond,
		HeartbeatInterval:  50 * time.Millisecond,
		Logger:             newTestLogger(t),
		MaxLogSize:         1000,
	}

	newTransport := &debugTransport{
		id:       newNodeID,
		registry: registry,
		logger:   newTestLogger(t),
	}

	newStateMachine := &testStateMachine{
		mu:   sync.Mutex{},
		data: make(map[string]string),
	}

	newNode, err := NewNode(newConfig, newTransport, nil, newStateMachine)
	if err != nil {
		t.Fatalf("Failed to create new node: %v", err)
	}

	registry.nodes[newNodeID] = newNode.(RPCHandler)

	if err := newNode.Start(ctx); err != nil {
		t.Fatalf("Failed to start new node: %v", err)
	}
	defer newNode.Stop()

	// SAFER: Add as non-voting member first
	err = leader.AddServer(newNodeID, fmt.Sprintf("server-%d", newNodeID), false)
	if err != nil {
		t.Fatalf("Failed to add non-voting server: %v", err)
	}

	t.Log("Added new server as non-voting member")

	// Wait for the new server to catch up
	WaitForConditionWithProgress(t, func() (bool, string) {
		newNodeCommitIndex := newNode.GetCommitIndex()
		return newNodeCommitIndex >= leaderCommitIndex, 
			fmt.Sprintf("new node commit: %d, leader commit: %d", newNodeCommitIndex, leaderCommitIndex)
	}, timing.ReplicationTimeout*2, "new node to catch up")

	// Verify the new node cannot affect elections while non-voting
	config := leader.GetConfiguration()
	for _, server := range config.Servers {
		if server.ID == newNodeID {
			if !server.Voting {
				t.Log("✓ New server correctly marked as non-voting")
			} else {
				t.Error("New server should be non-voting")
			}
		}
	}
}