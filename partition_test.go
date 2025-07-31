package raft

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

// TestAsymmetricPartition tests asymmetric network partitions where A can send to B but B cannot send to A
func TestAsymmetricPartition(t *testing.T) {
	// Create test infrastructure
	numNodes := 3
	nodes := make([]Node, numNodes)
	transports := make([]*asymmetricTransport, numNodes)
	registry := &asymmetricRegistry{
		nodes: make(map[int]RPCHandler),
		mu:    sync.RWMutex{},
	}

	for i := 0; i < numNodes; i++ {
		config := &Config{
			ID:                 i,
			Peers:              []int{0, 1, 2},
			ElectionTimeoutMin: 150 * time.Millisecond,
			ElectionTimeoutMax: 300 * time.Millisecond,
			HeartbeatInterval:  50 * time.Millisecond,
		}

		transport := &asymmetricTransport{
			id:              i,
			registry:        registry,
			blockedIncoming: make(map[int]bool),
			blockedOutgoing: make(map[int]bool),
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
	var leaderID int
	for i, node := range nodes {
		if node.IsLeader() {
			leaderID = i
			break
		}
	}

	t.Logf("Initial leader is node %d", leaderID)

	// Create asymmetric partition: leader can send to follower 1, but follower 1 cannot respond
	followerID := (leaderID + 1) % numNodes
	transports[followerID].BlockOutgoingTo(leaderID)

	t.Logf("Created asymmetric partition: node %d cannot send to node %d", followerID, leaderID)

	// Submit a command
	leader := nodes[leaderID]
	index, term, isLeader := leader.Submit("test-command")
	if !isLeader {
		t.Fatal("Node is not leader")
	}

	t.Logf("Submitted command at index %d, term %d", index, term)

	// Wait to see if the system remains stable
	time.Sleep(1 * time.Second)

	// Check if we still have exactly one leader
	leaderCount := 0
	for i, node := range nodes {
		if node.IsLeader() {
			leaderCount++
			t.Logf("Node %d is leader", i)
		}
	}

	if leaderCount != 1 {
		t.Errorf("Expected exactly 1 leader, found %d", leaderCount)
	}

	// Remove partition
	transports[followerID].UnblockOutgoingTo(leaderID)

	// Wait for system to stabilize
	time.Sleep(500 * time.Millisecond)

	// Verify all nodes eventually have the same commit index
	var commitIndices []int
	for i, node := range nodes {
		idx := node.GetCommitIndex()
		commitIndices = append(commitIndices, idx)
		t.Logf("Node %d commit index: %d", i, idx)
	}

	// Check if all commit indices are the same
	allSame := true
	for i := 1; i < len(commitIndices); i++ {
		if commitIndices[i] != commitIndices[0] {
			allSame = false
			break
		}
	}

	if !allSame {
		t.Errorf("Nodes have different commit indices after partition healed: %v", commitIndices)
	}
}

// TestCompletePartition tests complete network partition splitting the cluster
func TestCompletePartition(t *testing.T) {
	// Create a 5-node cluster
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
	var leaderID int
	for i, node := range nodes {
		if node.IsLeader() {
			leaderID = i
			t.Logf("Initial leader is node %d", i)
			break
		}
	}

	// Create partition: [0,1,2] | [3,4]
	// Majority partition should be able to make progress
	for i := 0; i <= 2; i++ {
		for j := 3; j <= 4; j++ {
			transports[i].Block(j)
			transports[j].Block(i)
		}
	}

	t.Log("Created partition: [0,1,2] | [3,4]")

	// Determine which partition has the leader
	if leaderID <= 2 {
		t.Log("Majority partition has the leader")
	} else {
		t.Log("Minority partition has the leader")
	}

	// Wait for elections to settle
	time.Sleep(1 * time.Second)

	// Check leadership in both partitions
	var majorityLeader int
	majorityLeaderCount := 0
	minorityLeaderCount := 0

	for i := 0; i <= 2; i++ {
		if nodes[i].IsLeader() {
			majorityLeader = i
			majorityLeaderCount++
		}
	}

	for i := 3; i <= 4; i++ {
		if nodes[i].IsLeader() {
			minorityLeaderCount++
		}
	}

	// Majority partition should have exactly one leader
	if majorityLeaderCount != 1 {
		t.Errorf("Majority partition should have exactly 1 leader, has %d", majorityLeaderCount)
	} else {
		t.Logf("Majority partition leader: node %d", majorityLeader)
	}

	// Minority partition should have no leader
	if minorityLeaderCount != 0 {
		t.Errorf("Minority partition should have 0 leaders, has %d", minorityLeaderCount)
	}

	// Try to submit command to majority leader
	if majorityLeaderCount == 1 {
		index, term, isLeader := nodes[majorityLeader].Submit("majority-command")
		if !isLeader {
			t.Error("Majority leader rejected command")
		} else {
			t.Logf("Majority leader accepted command at index %d, term %d", index, term)
		}
	}

	// Try to submit command to any node in minority (should fail)
	for i := 3; i <= 4; i++ {
		_, _, isLeader := nodes[i].Submit("minority-command")
		if isLeader {
			t.Errorf("Minority node %d incorrectly accepted command as leader", i)
		}
	}

	// Heal partition
	for i := 0; i <= 2; i++ {
		for j := 3; j <= 4; j++ {
			transports[i].Unblock(j)
			transports[j].Unblock(i)
		}
	}

	t.Log("Partition healed")

	// Wait for convergence
	time.Sleep(1 * time.Second)

	// Should have exactly one leader
	finalLeaderCount := 0
	var finalLeader int
	for i, node := range nodes {
		if node.IsLeader() {
			finalLeader = i
			finalLeaderCount++
		}
	}

	if finalLeaderCount != 1 {
		t.Errorf("After healing, expected 1 leader, found %d", finalLeaderCount)
	} else {
		t.Logf("Final leader after healing: node %d", finalLeader)
	}
}

// asymmetricTransport allows asymmetric partitions
type asymmetricTransport struct {
	id              int
	registry        *asymmetricRegistry
	handler         RPCHandler
	mu              sync.Mutex
	blockedIncoming map[int]bool
	blockedOutgoing map[int]bool
}

type asymmetricRegistry struct {
	mu    sync.RWMutex
	nodes map[int]RPCHandler
}

func (t *asymmetricTransport) BlockIncomingFrom(serverID int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.blockedIncoming[serverID] = true
}

func (t *asymmetricTransport) BlockOutgoingTo(serverID int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.blockedOutgoing[serverID] = true
}

func (t *asymmetricTransport) UnblockIncomingFrom(serverID int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.blockedIncoming, serverID)
}

func (t *asymmetricTransport) UnblockOutgoingTo(serverID int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.blockedOutgoing, serverID)
}

func (t *asymmetricTransport) SendRequestVote(serverID int, args *RequestVoteArgs) (*RequestVoteReply, error) {
	t.mu.Lock()
	blocked := t.blockedOutgoing[serverID]
	t.mu.Unlock()

	if blocked {
		return nil, fmt.Errorf("asymmetric partition: cannot send to server %d", serverID)
	}

	t.registry.mu.RLock()
	handler, exists := t.registry.nodes[serverID]
	t.registry.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("server %d not found", serverID)
	}

	// Check if target has blocked incoming from us
	targetTransport := t.registry.getTransport(serverID)
	if targetTransport != nil && targetTransport.isBlockedIncoming(t.id) {
		return nil, fmt.Errorf("asymmetric partition: server %d blocking incoming from %d", serverID, t.id)
	}

	reply := &RequestVoteReply{}
	err := handler.RequestVote(args, reply)
	return reply, err
}

func (t *asymmetricTransport) SendAppendEntries(serverID int, args *AppendEntriesArgs) (*AppendEntriesReply, error) {
	t.mu.Lock()
	blocked := t.blockedOutgoing[serverID]
	t.mu.Unlock()

	if blocked {
		return nil, fmt.Errorf("asymmetric partition: cannot send to server %d", serverID)
	}

	t.registry.mu.RLock()
	handler, exists := t.registry.nodes[serverID]
	t.registry.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("server %d not found", serverID)
	}

	// Check if target has blocked incoming from us
	targetTransport := t.registry.getTransport(serverID)
	if targetTransport != nil && targetTransport.isBlockedIncoming(t.id) {
		return nil, fmt.Errorf("asymmetric partition: server %d blocking incoming from %d", serverID, t.id)
	}

	reply := &AppendEntriesReply{}
	err := handler.AppendEntries(args, reply)
	return reply, err
}

func (t *asymmetricTransport) SendInstallSnapshot(serverID int, args *InstallSnapshotArgs) (*InstallSnapshotReply, error) {
	t.mu.Lock()
	blocked := t.blockedOutgoing[serverID]
	t.mu.Unlock()

	if blocked {
		return nil, fmt.Errorf("asymmetric partition: cannot send to server %d", serverID)
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

func (t *asymmetricTransport) isBlockedIncoming(from int) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.blockedIncoming[from]
}

func (t *asymmetricTransport) SetRPCHandler(handler RPCHandler) {
	t.handler = handler
	t.registry.registerTransport(t.id, t)
}

func (t *asymmetricTransport) Start() error {
	return nil
}

func (t *asymmetricTransport) Stop() error {
	return nil
}

func (t *asymmetricTransport) GetAddress() string {
	return fmt.Sprintf("asymmetric-transport-%d", t.id)
}

var transports = make(map[int]*asymmetricTransport)
var transportsMu sync.RWMutex

func (r *asymmetricRegistry) registerTransport(id int, t *asymmetricTransport) {
	transportsMu.Lock()
	defer transportsMu.Unlock()
	transports[id] = t
}

func (r *asymmetricRegistry) getTransport(id int) *asymmetricTransport {
	transportsMu.RLock()
	defer transportsMu.RUnlock()
	return transports[id]
}
