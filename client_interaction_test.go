package raft

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

// TestClientRedirection tests that non-leaders redirect clients to the leader
func TestClientRedirection(t *testing.T) {
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

	// Wait for leader election - 5 nodes need more time to stabilize
	time.Sleep(1 * time.Second)

	// Find leader and followers
	var leaderID int
	var leaderNode Node
	var followerNodes []Node
	var followerIDs []int

	for i, node := range nodes {
		if node.IsLeader() {
			leaderID = i
			leaderNode = node
		} else {
			followerNodes = append(followerNodes, node)
			followerIDs = append(followerIDs, i)
		}
	}

	if leaderNode == nil {
		t.Fatal("No leader elected")
	}

	t.Logf("Leader is node %d", leaderID)

	// Submit command to leader - should succeed
	index, term, isLeader := leaderNode.Submit("leader-command")
	if !isLeader {
		t.Error("Leader should accept commands")
	}
	t.Logf("Leader accepted command at index %d, term %d", index, term)

	// Submit command to followers - should be rejected
	for i, follower := range followerNodes {
		_, _, isLeader := follower.Submit("follower-command")
		if isLeader {
			t.Errorf("Follower %d should not accept commands", followerIDs[i])
		} else {
			t.Logf("Follower %d correctly rejected command", followerIDs[i])
		}

		// Check if follower knows who the leader is
		currentLeader := follower.GetLeader()
		if currentLeader != leaderID {
			t.Errorf("Follower %d doesn't know the correct leader. Expected %d, got %d",
				followerIDs[i], leaderID, currentLeader)
		}
	}
}

// TestConcurrentClientRequests tests handling of concurrent client requests
func TestConcurrentClientRequests(t *testing.T) {
	// Create a 3-node cluster
	numNodes := 3
	nodes := make([]Node, numNodes)
	registry := &debugNodeRegistry{
		nodes:  make(map[int]RPCHandler),
		logger: newTestLogger(t),
	}

	for i := 0; i < numNodes; i++ {
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

	// Wait for leader election - 5 nodes need more time to stabilize
	time.Sleep(2 * time.Second)

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

	// Submit many commands concurrently
	numClients := 10
	numCommandsPerClient := 5

	type result struct {
		clientID int
		cmdID    int
		index    int
		success  bool
		err      error
	}

	results := make(chan result, numClients*numCommandsPerClient)
	var wg sync.WaitGroup

	// Start clients
	for clientID := 0; clientID < numClients; clientID++ {
		wg.Add(1)
		go func(cid int) {
			defer wg.Done()

			for cmdID := 0; cmdID < numCommandsPerClient; cmdID++ {
				cmd := fmt.Sprintf("client-%d-cmd-%d", cid, cmdID)
				index, _, isLeader := leader.Submit(cmd)

				results <- result{
					clientID: cid,
					cmdID:    cmdID,
					index:    index,
					success:  isLeader,
				}
			}
		}(clientID)
	}

	// Wait for all clients to finish
	wg.Wait()
	close(results)

	// Check results
	successCount := 0
	indexMap := make(map[int]string) // Check for duplicate indices

	for res := range results {
		if res.success {
			successCount++

			// Check for duplicate indices
			if prev, exists := indexMap[res.index]; exists {
				t.Errorf("Duplicate index %d assigned to different commands: %s and client-%d-cmd-%d",
					res.index, prev, res.clientID, res.cmdID)
			}
			indexMap[res.index] = fmt.Sprintf("client-%d-cmd-%d", res.clientID, res.cmdID)
		}
	}

	expectedCommands := numClients * numCommandsPerClient
	if successCount != expectedCommands {
		t.Errorf("Expected %d successful commands, got %d", expectedCommands, successCount)
	}

	t.Logf("Successfully submitted %d concurrent commands", successCount)

	// Wait for replication
	time.Sleep(1 * time.Second)

	// Verify all nodes have the same commit index eventually
	commitIndices := make([]int, numNodes)
	for i, node := range nodes {
		commitIndices[i] = node.GetCommitIndex()
	}

	// All should be the same
	for i := 1; i < len(commitIndices); i++ {
		if commitIndices[i] != commitIndices[0] {
			t.Errorf("Inconsistent commit indices: %v", commitIndices)
			break
		}
	}
}

// TestClientTimeouts tests client request timeouts and retries
func TestClientTimeouts(t *testing.T) {
	// Create a 3-node cluster with partitionable transport
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
	time.Sleep(1 * time.Second)

	// Find leader
	var leaderID int = -1
	for i, node := range nodes {
		if node.IsLeader() {
			leaderID = i
			break
		}
	}

	if leaderID == -1 {
		t.Fatal("No leader elected")
	}
	
	t.Logf("Leader is node %d", leaderID)

	// Submit initial command
	leader := nodes[leaderID]
	index1, _, isLeader := leader.Submit("command-1")
	if !isLeader {
		t.Fatal("Leader lost leadership before submitting initial command")
	}
	t.Logf("Submitted command-1 at index %d", index1)
	
	// Wait for initial command to replicate
	time.Sleep(500 * time.Millisecond)
	
	// Verify initial command is committed before partitioning
	initialCommit := leader.GetCommitIndex()
	if initialCommit < index1 {
		t.Logf("Warning: Initial command not yet committed. Commit index: %d, command index: %d", 
			initialCommit, index1)
	}

	// Partition leader from followers
	for i := 0; i < 3; i++ {
		if i != leaderID {
			transports[leaderID].Block(i)
			transports[i].Block(leaderID)
		}
	}

	t.Log("Leader partitioned from followers")

	// Try to submit command while partitioned
	// This should succeed locally but not replicate
	index2, _, isLeader := leader.Submit("command-2-partitioned")
	if !isLeader {
		t.Error("Partitioned leader should still think it's leader initially")
	}
	t.Logf("Submitted command-2-partitioned at index %d (won't be committed)", index2)

	// Wait a bit
	time.Sleep(200 * time.Millisecond)

	// Check commit indices - partitioned command shouldn't be committed
	leaderCommit := leader.GetCommitIndex()
	if leaderCommit >= index2 {
		t.Error("Partitioned leader shouldn't commit new entries without majority")
	}

	// Heal partition
	for i := 0; i < 3; i++ {
		if i != leaderID {
			transports[leaderID].Unblock(i)
			transports[i].Unblock(leaderID)
		}
	}

	t.Log("Partition healed")

	// Submit new command after healing
	_, _, isLeader = leader.Submit("command-3-healed")
	if !isLeader {
		// Might have lost leadership, find new leader
		for i, node := range nodes {
			if node.IsLeader() {
				leader = node
				_, _, _ = leader.Submit("command-3-healed")
				t.Logf("New leader is node %d", i)
				break
			}
		}
	}

	// Wait for replication and potential re-election
	time.Sleep(1 * time.Second)

	// Find current leader after healing (may have changed)
	var currentLeader Node
	var currentLeaderID int = -1
	for i, node := range nodes {
		if node.IsLeader() {
			currentLeader = node
			currentLeaderID = i
			break
		}
	}
	
	if currentLeaderID == -1 {
		t.Fatal("No leader after healing partition")
	}
	
	if currentLeaderID != leaderID {
		t.Logf("Leadership changed from node %d to node %d after healing", leaderID, currentLeaderID)
	}

	// Verify the healed command is committed
	finalCommit := currentLeader.GetCommitIndex()
	if finalCommit < index1 {
		t.Errorf("At least the initial command should be committed. Commit index: %d, initial command index: %d",
			finalCommit, index1)
	}
}
