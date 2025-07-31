package raft

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

// silentTestLogger is a no-op logger for cleaner test output
type silentTestLogger struct{}

func (l *silentTestLogger) Debug(format string, args ...interface{}) {}
func (l *silentTestLogger) Info(format string, args ...interface{})  {}
func (l *silentTestLogger) Warn(format string, args ...interface{})  {}
func (l *silentTestLogger) Error(format string, args ...interface{}) {}

// TestVotingMemberSafetyAnalysis analyzes the safety implications of immediate voting rights
func TestVotingMemberSafetyAnalysis(t *testing.T) {
	t.Log("=== ANALYSIS: IMMEDIATE VOTING RIGHTS SAFETY ===")
	t.Log("")
	
	// Create a minimal 3-node cluster
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
			Logger:             nil, // Quiet for analysis
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

	t.Logf("Initial Configuration:")
	t.Logf("- Leader: Node %d", leaderID)
	t.Logf("- Cluster: 3 nodes (all voting)")
	t.Logf("- Majority required: 2 nodes")
	t.Log("")

	// Build up committed entries
	t.Log("Step 1: Building committed log entries...")
	for i := 1; i <= 5; i++ {
		cmd := fmt.Sprintf("entry-%d", i)
		index, _, ok := leader.Submit(cmd)
		if !ok {
			t.Fatal("Failed to submit command")
		}
		t.Logf("  - Submitted '%s' at index %d", cmd, index)
	}

	// Wait for replication
	time.Sleep(1 * time.Second)
	commitIndex := leader.GetCommitIndex()
	t.Logf("  - Commit index: %d", commitIndex)
	t.Log("")

	// Add new voting server with empty log
	t.Log("Step 2: Adding new voting server with empty log...")
	newNodeID := 3
	newConfig := &Config{
		ID:                 newNodeID,
		Peers:              []int{},
		ElectionTimeoutMin: 100 * time.Millisecond, // Aggressive
		ElectionTimeoutMax: 200 * time.Millisecond,
		HeartbeatInterval:  50 * time.Millisecond,
		Logger:             nil,
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

	// Add as voting member
	err = leader.AddServer(newNodeID, fmt.Sprintf("server-%d", newNodeID), true)
	if err != nil {
		t.Fatalf("Failed to add voting server: %v", err)
	}
	t.Logf("  - Added node %d as VOTING member", newNodeID)

	// Wait for configuration change
	time.Sleep(500 * time.Millisecond)

	t.Log("")
	t.Log("Step 3: Current State Analysis")
	t.Logf("  - Cluster now has 4 voting nodes")
	t.Logf("  - Majority required: 3 nodes")
	t.Logf("  - Node %d has empty log (no entries)", newNodeID)
	t.Logf("  - Other nodes have %d committed entries", commitIndex)
	t.Log("")

	// Create specific partition scenarios
	t.Log("Step 4: Analyzing Dangerous Scenarios")
	t.Log("")

	// Scenario 1: Empty log node can affect quorum
	t.Log("Scenario 1: Empty Log Node Affects Quorum")
	t.Log("  - If we partition [leader, 1 follower] | [1 follower, new empty node]")
	t.Log("  - The new node with empty log can:")
	t.Log("    a) Vote for the other follower in new election")
	t.Log("    b) Help form a majority (2/4) without having committed entries")
	t.Log("    c) Potentially allow election of leader with incomplete log")
	t.Log("")

	// Scenario 2: Multiple empty nodes
	t.Log("Scenario 2: Multiple Empty Nodes (Scaling Issue)")
	t.Log("  - If we add 3 new voting nodes at once (now 7 total)")
	t.Log("  - Majority becomes 4/7")
	t.Log("  - But 3 nodes have empty logs!")
	t.Log("  - Leader needs agreement from empty nodes to commit")
	t.Log("  - Result: Cluster becomes unavailable")
	t.Log("")

	// Demonstrate actual partition
	t.Log("Step 5: Demonstrating Actual Partition...")
	
	// Find a follower to keep with new node
	var followerID int
	for i := 0; i < 3; i++ {
		if i != leaderID {
			followerID = i
			break
		}
	}

	// Create partition
	for i := 0; i < 3; i++ {
		if i != followerID {
			transports[i].Block(followerID)
			transports[followerID].Block(i)
			transports[i].Block(newNodeID)
			newTransport.Block(i)
		}
	}

	t.Logf("  - Created partition: [nodes %d, %d] | [nodes %d, %d]", 
		leaderID, 3-leaderID-followerID, followerID, newNodeID)
	t.Log("  - The minority partition has 1 node with full log + 1 with empty log")
	t.Log("  - They form 2/4 nodes (50%) - enough to disrupt but not elect")

	// Wait to see election attempts
	time.Sleep(1 * time.Second)

	// Check node states
	for i := 0; i < 4; i++ {
		var node Node
		if i < 3 {
			node = nodes[i]
		} else {
			node = newNode
		}
		
		if node != nil {
			term, isLeader := node.GetState()
			logLen := node.GetLogLength()
			t.Logf("  - Node %d: term=%d, leader=%v, log_length=%d", 
				i, term, isLeader, logLen)
		}
	}

	t.Log("")
	t.Log("=== SAFER ALTERNATIVE: NON-VOTING FIRST ===")
	t.Log("")
	t.Log("1. Add new server as NON-VOTING member")
	t.Log("2. New server receives log entries but cannot vote")
	t.Log("3. Wait for new server to catch up (replicate all entries)")
	t.Log("4. Only then promote to VOTING member")
	t.Log("")
	t.Log("Benefits:")
	t.Log("- Preserves quorum calculations during catch-up")
	t.Log("- Prevents empty-log nodes from affecting elections")
	t.Log("- Maintains cluster availability during scaling")
	t.Log("- Standard practice in production Raft implementations")
	t.Log("")
	t.Log("=== END OF ANALYSIS ===")
}

// TestDangerOfImmediateVoting shows specific dangerous scenarios
func TestDangerOfImmediateVoting(t *testing.T) {
	// Scenario: Adding multiple voting servers causes unavailability
	t.Log("=== DANGER SCENARIO: CLUSTER UNAVAILABILITY ===")
	t.Log("")
	
	// Silence debug output by creating a no-op logger
	silentLogger := &silentTestLogger{}
	
	// Create initial 3-node cluster
	nodes := make([]Node, 3)
	registry := &debugNodeRegistry{
		nodes:  make(map[int]RPCHandler),
		logger: silentLogger,
	}

	for i := 0; i < 3; i++ {
		config := &Config{
			ID:                 i,
			Peers:              []int{0, 1, 2},
			ElectionTimeoutMin: 150 * time.Millisecond,
			ElectionTimeoutMax: 300 * time.Millisecond,
			HeartbeatInterval:  50 * time.Millisecond,
			Logger:             nil,
			MaxLogSize:         1000,
		}

		transport := &debugTransport{
			id:       i,
			registry: registry,
			logger:   silentLogger,
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

	// Build significant log
	t.Log("Building log with 50 entries...")
	for i := 0; i < 50; i++ {
		cmd := fmt.Sprintf("entry-%d", i)
		leader.Submit(cmd)
	}

	// Wait for replication
	time.Sleep(2 * time.Second)

	originalCommit := leader.GetCommitIndex()
	t.Logf("Original commit index: %d", originalCommit)
	t.Log("")

	// Now add 3 new voting servers at once
	t.Log("Adding 3 new VOTING servers with empty logs...")
	newNodes := make([]Node, 3)
	for i := 0; i < 3; i++ {
		newID := 3 + i
		
		config := &Config{
			ID:                 newID,
			Peers:              []int{},
			ElectionTimeoutMin: 150 * time.Millisecond,
			ElectionTimeoutMax: 300 * time.Millisecond,
			HeartbeatInterval:  50 * time.Millisecond,
			Logger:             nil,
			MaxLogSize:         1000,
		}

		transport := &debugTransport{
			id:       newID,
			registry: registry,
			logger:   nil,
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
		} else {
			t.Logf("  - Added node %d as voting member", newID)
		}
	}

	// Wait for configuration changes
	time.Sleep(1 * time.Second)

	// Analyze the situation
	config := leader.GetConfiguration()
	votingCount := 0
	for _, server := range config.Servers {
		if server.Voting {
			votingCount++
		}
	}
	
	t.Log("")
	t.Log("Current situation:")
	t.Logf("- Total voting members: %d", votingCount)
	t.Logf("- Majority needed: %d", votingCount/2+1)
	t.Logf("- Nodes with empty logs: 3")
	t.Log("")
	t.Log("DANGER: To commit new entries, leader needs agreement from")
	t.Logf("at least %d nodes, but 3 nodes have empty logs!", votingCount/2+1)
	t.Log("These empty-log nodes cannot acknowledge entries they don't have.")
	t.Log("")

	// Try to submit a new command
	testCmd := "test-after-scale"
	index, _, ok := leader.Submit(testCmd)
	if ok {
		t.Logf("Submitted command at index %d", index)
		
		// Wait and check if it gets committed
		time.Sleep(2 * time.Second)
		
		newCommit := leader.GetCommitIndex()
		if newCommit == originalCommit {
			t.Log("⚠️  CLUSTER UNAVAILABLE: New command was NOT committed!")
			t.Log("   The empty-log nodes prevent forward progress.")
		} else if newCommit > originalCommit {
			t.Logf("✓ Command was committed (new commit index: %d)", newCommit)
		}
	}

	t.Log("")
	t.Log("This demonstrates why immediate voting rights are dangerous:")
	t.Log("1. Empty-log nodes count toward quorum")
	t.Log("2. They cannot acknowledge entries they don't have")
	t.Log("3. Cluster loses availability until they catch up")
	t.Log("4. In worst case, cluster is permanently unavailable")
}