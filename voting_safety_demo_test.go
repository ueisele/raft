package raft

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

// TestVotingSafetyDemonstrationSimple shows the concrete danger of immediate voting rights
func TestVotingSafetyDemonstrationSimple(t *testing.T) {
	// Create a 3-node cluster
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
			Logger:             nil, // Quiet output for clearer demo
			MaxLogSize:         1000,
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

	// Submit important commands that should be preserved
	importantData := []string{"user:alice=admin", "config:db=primary", "state:critical=true"}
	for _, cmd := range importantData {
		index, _, ok := leader.Submit(cmd)
		if !ok {
			t.Fatal("Failed to submit command")
		}
		t.Logf("Submitted critical data '%s' at index %d", cmd, index)
	}

	// Wait for replication
	time.Sleep(1 * time.Second)

	// Verify data is committed
	commitIndex := leader.GetCommitIndex()
	t.Logf("Commit index before adding new node: %d", commitIndex)

	// Now add a new VOTING server
	newNodeID := 3
	newConfig := &Config{
		ID:                 newNodeID,
		Peers:              []int{},
		ElectionTimeoutMin: 100 * time.Millisecond, // Aggressive election timeout
		ElectionTimeoutMax: 200 * time.Millisecond,
		HeartbeatInterval:  50 * time.Millisecond,
		Logger:             newTestLogger(t),
		MaxLogSize:         1000,
	}

	newTransport := &partitionableTransport{
		id:       newNodeID,
		registry: registry,
		blocked:  make(map[int]bool),
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

	// Add as VOTING member immediately
	err = leader.AddServer(newNodeID, fmt.Sprintf("server-%d", newNodeID), true)
	if err != nil {
		t.Fatalf("Failed to add voting server: %v", err)
	}

	// Wait for configuration change
	time.Sleep(500 * time.Millisecond)

	// Now simulate a scenario where the new node can cause problems
	// Partition the old leader and one follower from the rest
	// This leaves the new node and one old node able to communicate
	
	// Find a follower to keep with new node
	var followerID int
	for i := 0; i < 3; i++ {
		if i != leaderID {
			followerID = i
			break
		}
	}

	// Create partition: [leader, other follower] | [one follower, new node]
	// The new node with empty log can now participate in elections
	for i := 0; i < 3; i++ {
		if i != followerID {
			// Block communication between this node and the follower+new node
			transports[i].Block(followerID)
			transports[followerID].Block(i)
			transports[i].Block(newNodeID)
			newTransport.Block(i)
		}
	}

	t.Logf("Created partition: [nodes %v] | [nodes %d, %d]", 
		[]int{leaderID, 3 - leaderID - followerID}, followerID, newNodeID)

	// Wait for new election in the minority partition
	time.Sleep(1 * time.Second)

	// Check if the new node became leader
	if newNode.IsLeader() {
		t.Log("DANGER: New node with empty log became leader!")
		
		// Show that it has a lower commit index
		newCommitIndex := newNode.GetCommitIndex()
		t.Logf("New leader commit index: %d (was %d)", newCommitIndex, commitIndex)
		
		// The new leader might overwrite committed entries!
		// Let's submit a conflicting command
		conflictCmd := "user:alice=hacker"
		index, _, ok := newNode.Submit(conflictCmd)
		if ok {
			t.Logf("New leader accepted command '%s' at index %d", conflictCmd, index)
			t.Log("This could overwrite the committed 'user:alice=admin' entry!")
		}
	}

	// Check state of all nodes
	t.Log("\nFinal state of all nodes:")
	for i := 0; i < 4; i++ {
		var node Node
		if i < 3 {
			node = nodes[i]
		} else {
			node = newNode
		}
		
		if node != nil {
			term, isLeader := node.GetState()
			commitIdx := node.GetCommitIndex()
			t.Logf("Node %d: term=%d, leader=%v, commit=%d", 
				i, term, isLeader, commitIdx)
		}
	}

	// Show the safer approach
	t.Log("\n=== Demonstrating Safer Approach ===")
	t.Log("1. Add new server as non-voting member first")
	t.Log("2. Wait for it to catch up (replicate all log entries)")
	t.Log("3. Only then promote to voting member")
	t.Log("4. This prevents empty-log nodes from affecting quorum")
}

// TestImmediateVotingDanger shows the specific danger scenario
func TestImmediateVotingDanger(t *testing.T) {
	// This test demonstrates the exact scenario from the Raft paper
	// where immediate voting can lead to unavailability
	
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
			Logger:             nil, // Quiet output for clearer demo
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
	time.Sleep(500 * time.Millisecond)

	// Find leader
	var leader Node
	for _, node := range nodes {
		if node.IsLeader() {
			leader = node
			break
		}
	}

	if leader == nil {
		t.Fatal("No leader elected")
	}

	// Build up a significant log
	for i := 0; i < 100; i++ {
		cmd := fmt.Sprintf("entry-%d", i)
		leader.Submit(cmd)
	}

	// Wait for replication
	time.Sleep(2 * time.Second)

	originalCommit := leader.GetCommitIndex()
	t.Logf("Original commit index: %d", originalCommit)

	// Now let's add multiple new servers at once (simulating aggressive scaling)
	// This is the danger scenario - adding multiple voting servers with empty logs
	newNodes := make([]Node, 3)
	for i := 0; i < 3; i++ {
		newID := 3 + i
		
		config := &Config{
			ID:                 newID,
			Peers:              []int{},
			ElectionTimeoutMin: 150 * time.Millisecond,
			ElectionTimeoutMax: 300 * time.Millisecond,
			HeartbeatInterval:  50 * time.Millisecond,
			Logger:             nil, // Quiet output for clearer demo
			MaxLogSize:         1000,
		}

		transport := &debugTransport{
			id:       newID,
			registry: registry,
			logger:   newTestLogger(t),
		}

		stateMachine := &testStateMachine{
			mu:   sync.Mutex{},
			data: make(map[string]string),
		}

		node, err := NewNode(config, transport, nil, stateMachine)
		if err != nil {
			t.Fatalf("Failed to create node %d: %v", newID, err)
		}

		newNodes[i] = node
		registry.nodes[newID] = node.(RPCHandler)

		if err := node.Start(ctx); err != nil {
			t.Fatalf("Failed to start node %d: %v", newID, err)
		}
		defer node.Stop()

		// Add as voting member
		err = leader.AddServer(newID, fmt.Sprintf("server-%d", newID), true)
		if err != nil {
			t.Logf("Failed to add server %d: %v", newID, err)
		}
	}

	// Wait for configuration changes
	time.Sleep(1 * time.Second)

	// Now we have 6 nodes total: 3 original (with full logs) and 3 new (with empty logs)
	// The new nodes with empty logs now count toward the majority!
	// Majority is 4/6, but we have 3 nodes with empty logs
	
	config := leader.GetConfiguration()
	votingCount := 0
	for _, server := range config.Servers {
		if server.Voting {
			votingCount++
		}
	}
	
	t.Logf("Total voting members: %d, majority needed: %d", votingCount, votingCount/2+1)
	t.Log("We now have 3 nodes with empty logs that count toward quorum!")
	t.Log("This means the leader needs empty-log nodes to agree to commit anything")
	t.Log("But they can't agree because they don't have the entries!")
	
	// Try to submit a new command - it might not get committed!
	testCmd := "test-after-scale"
	index, _, ok := leader.Submit(testCmd)
	if ok {
		t.Logf("Submitted command at index %d", index)
		
		// Wait and check if it gets committed
		time.Sleep(2 * time.Second)
		
		newCommit := leader.GetCommitIndex()
		if newCommit == originalCommit {
			t.Log("DANGER: New command was not committed!")
			t.Log("The cluster is effectively unavailable because empty-log nodes prevent progress")
		}
	}
}