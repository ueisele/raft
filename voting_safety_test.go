package raft

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	// "github.com/ueisele/raft/test" - removed to avoid import cycle
)

// silentTestLogger is a no-op logger for cleaner test output
type silentTestLogger struct{}

func (l *silentTestLogger) Debug(format string, args ...interface{}) {}
func (l *silentTestLogger) Info(format string, args ...interface{})  {}
func (l *silentTestLogger) Warn(format string, args ...interface{})  {}
func (l *silentTestLogger) Error(format string, args ...interface{}) {}

// TestVotingSafety combines all voting safety tests into one comprehensive test
func TestVotingSafety(t *testing.T) {
	t.Run("NewVotingServerSafety", func(t *testing.T) {
		testNewVotingServerSafety(t)
	})

	t.Run("SaferApproach", func(t *testing.T) {
		testSaferApproach(t)
	})

	t.Run("VotingSafetyDemonstrationSimple", func(t *testing.T) {
		testVotingSafetyDemonstrationSimple(t)
	})

	t.Run("ImmediateVotingDanger", func(t *testing.T) {
		testImmediateVotingDanger(t)
	})

	t.Run("VotingMemberSafetyAnalysis", func(t *testing.T) {
		testVotingMemberSafetyAnalysis(t)
	})

	t.Run("DangerOfImmediateVoting", func(t *testing.T) {
		testDangerOfImmediateVoting(t)
	})

	// Vote denial tests
	t.Run("VoteDenialWithActiveLeader", func(t *testing.T) {
		testVoteDenialWithActiveLeader(t)
	})

	t.Run("VoteGrantingAfterTimeout", func(t *testing.T) {
		testVoteGrantingAfterTimeout(t)
	})

	t.Run("VoteDenialPreventsUnnecessaryElections", func(t *testing.T) {
		testVoteDenialPreventsUnnecessaryElections(t)
	})
}

// testNewVotingServerSafety demonstrates the safety issue with immediately 
// allowing new voting servers to participate before they catch up
func testNewVotingServerSafety(t *testing.T) {
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

// testSaferApproach demonstrates a safer approach using non-voting members first
func testSaferApproach(t *testing.T) {
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

// testVotingSafetyDemonstrationSimple shows the concrete danger of immediate voting rights
func testVotingSafetyDemonstrationSimple(t *testing.T) {
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
	timing := DefaultTimingConfig()
	timing.ElectionTimeout = 500 * time.Millisecond
	leaderID := WaitForLeaderWithConfig(t, nodes, timing)
	if leaderID < 0 {
		t.Fatal("No leader elected")
	}
	leader := nodes[leaderID]

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
	WaitForCommitIndexWithConfig(t, nodes, len(importantData), timing)

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
	markerIndex, _, _ := leader.Submit("config-marker")
	WaitForCommitIndexWithConfig(t, nodes, markerIndex, timing)

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
	WaitForConditionWithProgress(t, func() (bool, string) {
		// Check if any node in the minority partition became leader
		term, isLeader := newNode.GetState()
		if isLeader {
			return true, fmt.Sprintf("new node is leader in term %d", term)
		}
		term2, isLeader2 := nodes[followerID].GetState()
		if isLeader2 {
			return true, fmt.Sprintf("node %d is leader in term %d", followerID, term2)
		}
		return false, "waiting for new leader in minority partition"
	}, timing.ElectionTimeout*2, "new leader election")

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

// testImmediateVotingDanger shows the specific danger scenario
func testImmediateVotingDanger(t *testing.T) {
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
	timing := DefaultTimingConfig()
	timing.ElectionTimeout = 500 * time.Millisecond
	leaderID := WaitForLeaderWithConfig(t, nodes, timing)
	if leaderID < 0 {
		t.Fatal("No leader elected")
	}
	leader := nodes[leaderID]

	// Build up a significant log
	for i := 0; i < 100; i++ {
		cmd := fmt.Sprintf("entry-%d", i)
		leader.Submit(cmd)
	}

	// Wait for replication
	WaitForCommitIndexWithConfig(t, nodes, 100, timing)

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
	configMarker, _, _ := leader.Submit("config-complete")
	WaitForCommitIndexWithConfig(t, nodes, configMarker, timing)

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
		Eventually(t, func() bool {
			return leader.GetCommitIndex() > originalCommit
		}, 2*time.Second, "new command to be committed")
		
		newCommit := leader.GetCommitIndex()
		if newCommit == originalCommit {
			t.Log("DANGER: New command was not committed!")
			t.Log("The cluster is effectively unavailable because empty-log nodes prevent progress")
		}
	}
}

// testVotingMemberSafetyAnalysis analyzes the safety implications of immediate voting rights
func testVotingMemberSafetyAnalysis(t *testing.T) {
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
	timing := DefaultTimingConfig()
	timing.ElectionTimeout = 500 * time.Millisecond
	leaderID := WaitForLeaderWithConfig(t, nodes, timing)
	if leaderID < 0 {
		t.Fatal("No leader elected")
	}
	leader := nodes[leaderID]

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
	WaitForCommitIndexWithConfig(t, nodes, 5, timing)
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
	markerIndex, _, _ := leader.Submit("config-marker")
	WaitForCommitIndexWithConfig(t, nodes, markerIndex, timing)

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
	Eventually(t, func() bool {
		// Check if term has increased in minority partition
		term1, _ := nodes[followerID].GetState()
		term2, _ := newNode.GetState()
		initialTerm, _ := leader.GetState()
		return term1 > initialTerm || term2 > initialTerm
	}, 1*time.Second, "election attempts in minority partition")

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

// testDangerOfImmediateVoting shows specific dangerous scenarios
func testDangerOfImmediateVoting(t *testing.T) {
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
	timing := DefaultTimingConfig()
	timing.ElectionTimeout = 500 * time.Millisecond
	leaderID := WaitForLeaderWithConfig(t, nodes, timing)
	if leaderID < 0 {
		t.Fatal("No leader elected")
	}
	leader := nodes[leaderID]

	// Build significant log
	t.Log("Building log with 50 entries...")
	for i := 0; i < 50; i++ {
		cmd := fmt.Sprintf("entry-%d", i)
		leader.Submit(cmd)
	}

	// Wait for replication
	WaitForCommitIndexWithConfig(t, nodes, 50, timing)

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
	configMarker, _, _ := leader.Submit("config-complete")
	WaitForCommitIndexWithConfig(t, nodes, configMarker, timing)

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
		Eventually(t, func() bool {
			return leader.GetCommitIndex() > originalCommit
		}, 2*time.Second, "new command to be committed")
		
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

// ========== Vote Denial Tests (from vote_denial_test.go) ==========

// testVoteDenialWithActiveLeader tests that followers deny votes when they have an active leader
func testVoteDenialWithActiveLeader(t *testing.T) {
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
			ElectionTimeoutMin: 200 * time.Millisecond,
			ElectionTimeoutMax: 400 * time.Millisecond,
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
	timing := DefaultTimingConfig()
	timing.ElectionTimeout = 500 * time.Millisecond
	WaitForLeaderWithConfig(t, nodes, timing)

	var leaderID int
	var followerNode Node
	var followerID int

	// Find leader and a follower
	for i, node := range nodes {
		if node.IsLeader() {
			leaderID = i
		} else {
			followerNode = node
			followerID = i
		}
	}

	t.Logf("Leader is node %d", leaderID)

	// Submit a command to ensure followers are receiving heartbeats
	leader := nodes[leaderID]
	idx, _, _ := leader.Submit("test command")
	WaitForCommitIndexWithConfig(t, nodes, idx, timing)

	// Now simulate a disruptive candidate by manually sending RequestVote
	// This simulates a node that hasn't heard from the leader trying to start an election
	disruptiveTerm := followerNode.GetCurrentTerm() + 1

	args := &RequestVoteArgs{
		Term:         disruptiveTerm,
		CandidateID:  3, // Non-existent node
		LastLogIndex: 0,
		LastLogTerm:  0,
	}

	reply := &RequestVoteReply{}
	err := registry.nodes[followerID].(RPCHandler).RequestVote(args, reply)
	if err != nil {
		t.Fatalf("RequestVote failed: %v", err)
	}

	// The follower should deny the vote if it has heard from the leader recently
	if reply.VoteGranted {
		t.Error("Follower should deny vote when it has an active leader")
	}

	// Verify the leader is still the leader
	if !nodes[leaderID].IsLeader() {
		t.Error("Leader should remain leader after vote denial")
	}
}

// testVoteGrantingAfterTimeout tests that followers grant votes after not hearing from leader
func testVoteGrantingAfterTimeout(t *testing.T) {
	// Create a 3-node cluster with ability to partition
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
	timing := DefaultTimingConfig()
	timing.ElectionTimeout = 500 * time.Millisecond
	WaitForLeaderWithConfig(t, nodes, timing)

	var leaderID int
	for i, node := range nodes {
		if node.IsLeader() {
			leaderID = i
			break
		}
	}

	t.Logf("Initial leader is node %d", leaderID)

	// Partition the leader from the other nodes
	for i := 0; i < 3; i++ {
		if i != leaderID {
			transports[leaderID].Block(i)
			transports[i].Block(leaderID)
		}
	}

	t.Log("Leader partitioned from followers")

	// Wait for election timeout
	Eventually(t, func() bool {
		// Check if followers are ready to grant votes (after election timeout)
		for i, node := range nodes {
			if i != leaderID {
				// Check if node has incremented term (indicating timeout)
				if node.GetCurrentTerm() > nodes[leaderID].GetCurrentTerm() {
					return true
				}
			}
		}
		return false
	}, timing.ElectionTimeout*2, "followers to timeout")

	// Now followers should grant votes to each other
	// Find a follower and check if it can win an election
	WaitForConditionWithProgress(t, func() (bool, string) {
		for j, node := range nodes {
			if j != leaderID && node.IsLeader() {
				return true, fmt.Sprintf("new leader elected: node %d", j)
			}
		}
		return false, "waiting for new leader"
	}, timing.ElectionTimeout*3, "new leader election")
	
	// If we got here, a new leader was elected successfully
}

// testVoteDenialPreventsUnnecessaryElections tests the vote denial optimization
func testVoteDenialPreventsUnnecessaryElections(t *testing.T) {
	// Create 5-node cluster
	nodes := make([]Node, 5)
	transports := make([]*partitionableTransport, 5)
	registry := &partitionRegistry{
		nodes: make(map[int]RPCHandler),
		mu:    sync.RWMutex{},
	}

	for i := 0; i < 5; i++ {
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

	// Wait for leader election
	timing := DefaultTimingConfig()
	timing.ElectionTimeout = 500 * time.Millisecond
	WaitForLeaderWithConfig(t, nodes, timing)

	// Find leader
	var leaderID int
	for i, node := range nodes {
		if node.IsLeader() {
			leaderID = i
			break
		}
	}

	t.Logf("Initial leader is node %d", leaderID)

	// Test: Temporarily partition one node and let it fall behind
	isolatedNode := (leaderID + 2) % 5
	for i := 0; i < 5; i++ {
		if i != isolatedNode {
			transports[isolatedNode].Block(i)
			transports[i].Block(isolatedNode)
		}
	}

	t.Logf("Isolated node %d", isolatedNode)

	// Submit commands while node is isolated
	leader := nodes[leaderID]
	for i := 0; i < 10; i++ {
		_, _, isLeader := leader.Submit(fmt.Sprintf("cmd-%d", i))
		if !isLeader {
			t.Fatal("Lost leadership")
		}
	}

	// Record current terms
	termsBeforeReconnect := make([]int, 5)
	for i, node := range nodes {
		termsBeforeReconnect[i] = node.GetCurrentTerm()
	}

	// Reconnect the isolated node
	for i := 0; i < 5; i++ {
		transports[isolatedNode].Unblock(i)
		transports[i].Unblock(isolatedNode)
	}

	t.Log("Reconnected isolated node")

	// The isolated node will try to start elections but should be denied
	// because other nodes have a more recent log from the current leader
	WaitForConditionWithProgress(t, func() (bool, string) {
		// Wait for isolated node to attempt election
		isolatedTerm := nodes[isolatedNode].GetCurrentTerm()
		return isolatedTerm > termsBeforeReconnect[isolatedNode], 
			fmt.Sprintf("isolated node term: %d (was %d)", isolatedTerm, termsBeforeReconnect[isolatedNode])
	}, timing.ElectionTimeout*2, "isolated node election attempt")

	// Check if unnecessary elections were prevented
	unnecessaryElections := false
	for i, node := range nodes {
		currentTerm := node.GetCurrentTerm()
		// Allow for at most one term increase (from isolated node's attempt)
		if currentTerm > termsBeforeReconnect[i]+1 {
			unnecessaryElections = true
			t.Logf("Node %d term increased from %d to %d",
				i, termsBeforeReconnect[i], currentTerm)
		}
	}

	if unnecessaryElections {
		t.Error("Vote denial should have prevented unnecessary term increases")
	}

	// Verify the same leader is still in charge
	currentLeaderFound := false
	for i, node := range nodes {
		if node.IsLeader() {
			if i == leaderID {
				currentLeaderFound = true
			} else {
				t.Errorf("Leadership changed from %d to %d unnecessarily", leaderID, i)
			}
		}
	}

	if !currentLeaderFound {
		t.Error("Original leader should still be leader")
	}

	// Verify cluster is still functional
	_, _, isLeader := leader.Submit("test-after-reconnect")
	if !isLeader {
		t.Error("Leader should still be able to replicate after vote denial")
	}
}