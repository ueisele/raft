package raft

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	// "github.com/ueisele/raft/test" - removed to avoid import cycle
)

// TestVoteDenialWithActiveLeader tests that followers deny votes when they have an active leader
func TestVoteDenialWithActiveLeader(t *testing.T) {
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

// TestVoteGrantingAfterTimeout tests that followers grant votes after not hearing from leader
func TestVoteGrantingAfterTimeout(t *testing.T) {
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

// partitionableTransport is a transport that can simulate network partitions
type partitionableTransport struct {
	id       int
	registry *partitionRegistry
	handler  RPCHandler
	mu       sync.Mutex
	blocked  map[int]bool
}

type partitionRegistry struct {
	mu    sync.RWMutex
	nodes map[int]RPCHandler
}

func (t *partitionableTransport) Block(serverID int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.blocked[serverID] = true
}

func (t *partitionableTransport) Unblock(serverID int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.blocked, serverID)
}

func (t *partitionableTransport) SendRequestVote(serverID int, args *RequestVoteArgs) (*RequestVoteReply, error) {
	t.mu.Lock()
	blocked := t.blocked[serverID]
	t.mu.Unlock()

	if blocked {
		return nil, fmt.Errorf("network partition: cannot reach server %d", serverID)
	}

	t.registry.mu.RLock()
	handler, exists := t.registry.nodes[serverID]
	t.registry.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("server %d not found", serverID)
	}

	reply := &RequestVoteReply{}
	err := handler.RequestVote(args, reply)
	return reply, err
}

func (t *partitionableTransport) SendAppendEntries(serverID int, args *AppendEntriesArgs) (*AppendEntriesReply, error) {
	t.mu.Lock()
	blocked := t.blocked[serverID]
	t.mu.Unlock()

	if blocked {
		return nil, fmt.Errorf("network partition: cannot reach server %d", serverID)
	}

	t.registry.mu.RLock()
	handler, exists := t.registry.nodes[serverID]
	t.registry.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("server %d not found", serverID)
	}

	reply := &AppendEntriesReply{}
	err := handler.AppendEntries(args, reply)
	return reply, err
}

func (t *partitionableTransport) SendInstallSnapshot(serverID int, args *InstallSnapshotArgs) (*InstallSnapshotReply, error) {
	t.mu.Lock()
	blocked := t.blocked[serverID]
	t.mu.Unlock()

	if blocked {
		return nil, fmt.Errorf("network partition: cannot reach server %d", serverID)
	}

	t.registry.mu.RLock()
	handler, exists := t.registry.nodes[serverID]
	t.registry.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("server %d not found", serverID)
	}

	reply := &InstallSnapshotReply{}
	err := handler.InstallSnapshot(args, reply)
	return reply, err
}

func (t *partitionableTransport) SetRPCHandler(handler RPCHandler) {
	t.handler = handler
}

func (t *partitionableTransport) Start() error {
	return nil
}

func (t *partitionableTransport) Stop() error {
	return nil
}

func (t *partitionableTransport) GetAddress() string {
	return fmt.Sprintf("partition-transport-%d", t.id)
}

// TestVoteDenialPreventsUnnecessaryElections tests the vote denial optimization
func TestVoteDenialPreventsUnnecessaryElections(t *testing.T) {
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
