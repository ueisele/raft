package raft

import (
	"context"
	"sync"
	"testing"
	"time"
)

// TestSimultaneousConfigChanges tests handling of concurrent configuration changes
func TestSimultaneousConfigChanges(t *testing.T) {
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
	for _, node := range nodes {
		if node.IsLeader() {
			leader = node
			break
		}
	}

	if leader == nil {
		t.Fatal("No leader elected")
	}

	// Try to add multiple servers simultaneously
	var wg sync.WaitGroup
	results := make(chan error, 2)

	wg.Add(2)
	go func() {
		defer wg.Done()
		err := leader.AddServer(3, "server-3:8003", true)
		results <- err
	}()

	go func() {
		defer wg.Done()
		err := leader.AddServer(4, "server-4:8004", true)
		results <- err
	}()

	wg.Wait()
	close(results)

	// At least one should fail (can't have concurrent config changes)
	successCount := 0
	for err := range results {
		if err == nil {
			successCount++
		}
	}

	if successCount > 1 {
		t.Error("Multiple simultaneous configuration changes succeeded - should only allow one")
	}

	t.Logf("Correctly rejected simultaneous config changes (successes: %d)", successCount)
}

// TestConfigChangesDuringPartition tests configuration changes during network partition
func TestConfigChangesDuringPartition(t *testing.T) {
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

	// Wait for leader election
	time.Sleep(500 * time.Millisecond)

	// Find leader
	var leaderID int
	var leader Node
	for i, node := range nodes {
		if node.IsLeader() {
			leaderID = i
			leader = node
			break
		}
	}

	if leader == nil {
		t.Fatal("No leader elected")
	}

	t.Logf("Leader is node %d", leaderID)

	// Create partition: leader + 1 node | rest
	// This gives leader minority
	for i := 2; i < numNodes; i++ {
		transports[leaderID].Block(i)
		transports[i].Block(leaderID)
		transports[1].Block(i)
		transports[i].Block(1)
	}

	t.Log("Created minority partition with leader")

	// Try to add server while in minority
	err := leader.AddServer(5, "server-5:8005", true)
	if err == nil {
		t.Log("Configuration change accepted in minority partition (will not commit)")
	} else {
		t.Logf("Configuration change failed in minority: %v", err)
	}

	// Heal partition
	for i := 0; i < numNodes; i++ {
		for j := 0; j < numNodes; j++ {
			transports[i].Unblock(j)
		}
	}

	t.Log("Partition healed")

	// Wait for stabilization
	time.Sleep(1 * time.Second)

	// Find new leader (might have changed)
	var newLeader Node
	for _, node := range nodes {
		if node.IsLeader() {
			newLeader = node
			break
		}
	}

	if newLeader == nil {
		t.Fatal("No leader after healing partition")
	}

	// Now configuration change should succeed with a different server
	// (server 5 might still be pending from the previous attempt)
	err = newLeader.AddServer(6, "server-6:8006", true)
	if err != nil {
		t.Errorf("Configuration change failed after healing: %v", err)
	} else {
		t.Log("Configuration change succeeded after healing")
	}
}

// TestRemoveCurrentLeader tests removing the current leader from configuration
func TestRemoveCurrentLeader(t *testing.T) {
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
	var leaderID int
	var leader Node
	for i, node := range nodes {
		if node.IsLeader() {
			leaderID = i
			leader = node
			break
		}
	}

	if leader == nil {
		t.Fatal("No leader elected")
	}

	t.Logf("Current leader is node %d", leaderID)

	// Try to remove the leader itself
	err := leader.RemoveServer(leaderID)
	if err != nil {
		t.Logf("Removing current leader returned error: %v", err)
	}

	// Wait for configuration to be committed and propagated
	time.Sleep(1 * time.Second)
	
	// Check that configuration has been updated on all nodes
	for i, node := range nodes {
		n := node.(*raftNode)
		n.mu.RLock()
		config := n.configuration.GetConfiguration()
		commitIndex := n.log.GetCommitIndex()
		lastApplied := n.log.GetLastApplied()
		n.mu.RUnlock()
		t.Logf("Node %d config after removal: %v (commitIndex=%d, lastApplied=%d)", i, config.Servers, commitIndex, lastApplied)
	}

	// Wait for transition and new election
	t.Log("Waiting for new leader election...")
	time.Sleep(2 * time.Second)

	// Check that removed leader is no longer leader
	if nodes[leaderID].IsLeader() {
		t.Error("Removed node is still leader")
	}

	// Check that we have a new leader from remaining nodes
	var newLeaderFound bool
	var newLeaderID int
	for i, node := range nodes {
		if i != leaderID && node.IsLeader() {
			newLeaderFound = true
			newLeaderID = i
			t.Logf("New leader is node %d", i)
			break
		}
	}

	if !newLeaderFound {
		// Let's give it more time and check again
		time.Sleep(2 * time.Second)
		for i, node := range nodes {
			if i != leaderID && node.IsLeader() {
				newLeaderFound = true
				newLeaderID = i
				t.Logf("New leader is node %d (after additional wait)", i)
				break
			}
		}
	}

	if !newLeaderFound {
		// Log the state of all nodes for debugging
		for i, node := range nodes {
			state, _ := node.GetState()
			n := node.(*raftNode)
			n.mu.RLock()
			config := n.configuration.GetConfiguration()
			// Also check election manager peers
			electionPeers := make([]int, len(n.election.peers))
			copy(electionPeers, n.election.peers)
			n.mu.RUnlock()
			t.Logf("Node %d state: %v, IsLeader: %v, Config: %v, ElectionPeers: %v", i, state, node.IsLeader(), config.Servers, electionPeers)
		}
		t.Error("No new leader elected after removing old leader")
	} else {
		// Verify the new leader can accept commands
		_, _, isLeader := nodes[newLeaderID].Submit("test-after-removal")
		if !isLeader {
			t.Error("New leader cannot accept commands")
		}
	}
}

// TestAddNonVotingServer tests adding non-voting servers
func TestAddNonVotingServer(t *testing.T) {
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
			MaxLogSize:         1000, // Prevent premature snapshots
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

	// Add non-voting server
	err := leader.AddServer(3, "server-3:8003", false)
	if err != nil {
		t.Errorf("Failed to add non-voting server: %v", err)
	}

	// Wait for configuration change to be committed
	time.Sleep(1 * time.Second)

	// Get configuration
	config := leader.GetConfiguration()
	t.Logf("Configuration after add: %v", config.Servers)
	t.Logf("Configuration index: %d, commit index: %d", config.Index, leader.GetCommitIndex())
	
	// Log detailed server info
	for _, server := range config.Servers {
		t.Logf("Server %d: Address=%s, Voting=%v", server.ID, server.Address, server.Voting)
	}

	// Verify server was added as non-voting
	found := false
	for _, server := range config.Servers {
		if server.ID == 3 {
			found = true
			if server.Voting {
				t.Error("Server was added as voting, expected non-voting")
			}
			break
		}
	}

	if !found {
		t.Error("Non-voting server was not added to configuration")
	}

	// Wait a bit more for configuration to stabilize across all nodes
	time.Sleep(1 * time.Second)

	// Re-check who is the leader in case it changed
	var currentLeader Node
	for _, node := range nodes {
		if node.IsLeader() {
			currentLeader = node
			break
		}
	}
	
	if currentLeader == nil {
		t.Fatal("No leader after configuration change")
	}
	
	if currentLeader != leader {
		t.Log("Leader changed after configuration change")
		leader = currentLeader
	}

	// Verify quorum size didn't change (still 2 out of 3 voting members)
	// Submit a command and verify it gets committed
	index, _, isLeader := leader.Submit("test-command")
	if !isLeader {
		t.Fatal("Lost leadership")
	}
	t.Logf("Submitted command at index %d", index)

	// Wait for replication with retries
	committed := false
	for i := 0; i < 10; i++ {
		time.Sleep(500 * time.Millisecond)
		commitIndex := leader.GetCommitIndex()
		t.Logf("Attempt %d: commit index = %d, waiting for index %d", i+1, commitIndex, index)
		if commitIndex >= index {
			committed = true
			break
		}
	}

	if !committed {
		t.Errorf("Command not committed with non-voting server. Index: %d, Final Commit: %d",
			index, leader.GetCommitIndex())
	}
}

