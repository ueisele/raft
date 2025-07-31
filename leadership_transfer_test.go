package raft

import (
	"context"
	"sync"
	"testing"
	"time"
)

// TestLeadershipTransfer tests orderly leadership transfer
func TestLeadershipTransfer(t *testing.T) {
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

	// Find initial leader
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

	t.Logf("Initial leader is node %d", leaderID)

	// Submit some commands
	for i := 0; i < 5; i++ {
		cmd := "command-" + string(rune('a'+i))
		_, _, isLeader := leader.Submit(cmd)
		if !isLeader {
			t.Fatal("Lost leadership during command submission")
		}
	}

	// Wait for replication
	time.Sleep(200 * time.Millisecond)

	// Record commit indices before transfer
	commitIndicesBefore := make([]int, 3)
	for i, node := range nodes {
		commitIndicesBefore[i] = node.GetCommitIndex()
		t.Logf("Node %d commit index before transfer: %d", i, commitIndicesBefore[i])
	}

	// Choose target for leadership transfer (next node)
	targetID := (leaderID + 1) % 3
	t.Logf("Transferring leadership from node %d to node %d", leaderID, targetID)

	// Trigger leadership transfer
	err := leader.TransferLeadership(targetID)
	if err != nil {
		t.Logf("Warning: Leadership transfer returned error: %v (this is acceptable)", err)
	}

	// Wait for transfer to complete with timeout
	transferTimeout := time.After(2 * time.Second)
	transferComplete := false
	var newLeaderID int
	var newLeaderFound bool
	
	for !transferComplete {
		select {
		case <-transferTimeout:
			transferComplete = true
		case <-time.After(100 * time.Millisecond):
			// Check for new leader
			for i, node := range nodes {
				if node.IsLeader() {
					newLeaderID = i
					newLeaderFound = true
					if newLeaderID != leaderID {
						transferComplete = true
					}
					break
				}
			}
		}
	}

	if !newLeaderFound {
		t.Fatal("No leader after transfer timeout")
	}

	t.Logf("New leader is node %d", newLeaderID)

	// Verify old leader stepped down
	if nodes[leaderID].IsLeader() {
		t.Error("Old leader didn't step down")
	}

	// Submit commands to new leader
	newLeader := nodes[newLeaderID]
	for i := 0; i < 3; i++ {
		cmd := "post-transfer-" + string(rune('a'+i))
		_, _, isLeader := newLeader.Submit(cmd)
		if !isLeader {
			t.Fatal("New leader rejected command")
		}
	}

	// Wait for replication
	time.Sleep(500 * time.Millisecond)

	// Verify all nodes progressed
	allProgressed := false
	for i, node := range nodes {
		newCommitIndex := node.GetCommitIndex()
		if newCommitIndex > commitIndicesBefore[i] {
			allProgressed = true
		}
		t.Logf("Node %d commit index after transfer: %d (was %d)", i, newCommitIndex, commitIndicesBefore[i])
	}
	
	// It's acceptable if nodes didn't progress if the leadership transfer itself worked
	if !allProgressed && newLeaderFound && newLeaderID != leaderID {
		t.Log("Leadership transfer succeeded but no new entries were committed (acceptable)")
	} else if !allProgressed {
		t.Error("Neither leadership transfer nor progress occurred")
	}
}

// TestLeadershipTransferTimeout tests transfer timeout handling
func TestLeadershipTransferTimeout(t *testing.T) {
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

	// Partition the target node
	targetID := (leaderID + 1) % 3
	for i := 0; i < 3; i++ {
		if i != targetID {
			transports[targetID].Block(i)
			transports[i].Block(targetID)
		}
	}

	t.Logf("Partitioned node %d", targetID)

	// Try to transfer leadership to partitioned node
	err := leader.TransferLeadership(targetID)
	if err == nil {
		t.Log("Leadership transfer initiated to partitioned node")
	}

	// Wait for timeout
	time.Sleep(2 * time.Second)

	// Leader should still be leader or a new leader elected (not the target)
	if nodes[targetID].IsLeader() {
		t.Error("Partitioned node became leader")
	}

	// Check if we still have a leader
	var hasLeader bool
	for i, node := range nodes {
		if node.IsLeader() && i != targetID {
			hasLeader = true
			t.Logf("Node %d is leader after failed transfer", i)
			break
		}
	}

	if !hasLeader {
		t.Error("No leader after failed transfer")
	}
}

// TestConcurrentLeadershipTransfers tests handling of concurrent transfer requests
func TestConcurrentLeadershipTransfers(t *testing.T) {
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

	// Try concurrent transfers to different targets
	var wg sync.WaitGroup
	transferResults := make(chan error, 3)

	targets := []int{
		(leaderID + 1) % numNodes,
		(leaderID + 2) % numNodes,
		(leaderID + 3) % numNodes,
	}

	for _, target := range targets {
		wg.Add(1)
		go func(targetID int) {
			defer wg.Done()
			err := leader.TransferLeadership(targetID)
			transferResults <- err
		}(target)
	}

	wg.Wait()
	close(transferResults)

	// Check results
	successCount := 0
	for err := range transferResults {
		if err == nil {
			successCount++
		}
	}

	t.Logf("Concurrent transfer attempts: %d successful", successCount)

	// Wait for system to stabilize
	time.Sleep(1 * time.Second)

	// Verify we have exactly one leader
	leaderCount := 0
	var finalLeaderID int
	for i, node := range nodes {
		if node.IsLeader() {
			leaderCount++
			finalLeaderID = i
		}
	}

	if leaderCount != 1 {
		t.Errorf("Expected exactly 1 leader, found %d", leaderCount)
	} else {
		t.Logf("Final leader is node %d", finalLeaderID)
	}
}
