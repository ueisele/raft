package raft

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

// TestMultiNodeElection tests leader election with 3 nodes
func TestMultiNodeElection(t *testing.T) {
	// Create 3 nodes
	nodes := make([]Node, 3)
	transports := make([]*multiNodeTransport, 3)

	// Create transports that can communicate
	registry := &nodeRegistry{
		nodes: make(map[int]RPCHandler),
	}

	for i := 0; i < 3; i++ {
		config := &Config{
			ID:                 i,
			Peers:              []int{0, 1, 2},
			ElectionTimeoutMin: 150 * time.Millisecond,
			ElectionTimeoutMax: 300 * time.Millisecond,
			HeartbeatInterval:  50 * time.Millisecond,
		}

		transport := &multiNodeTransport{
			id:       i,
			registry: registry,
		}
		transports[i] = transport

		stateMachine := &testStateMachine{
			data: make(map[string]string),
		}

		node, err := NewNode(config, transport, nil, stateMachine)
		if err != nil {
			t.Fatalf("Failed to create node %d: %v", i, err)
		}

		nodes[i] = node
	}

	// Register all nodes with their transports before starting
	for i, node := range nodes {
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
	var leader Node
	var leaderID int
	leaderFound := false

	for attempt := 0; attempt < 20 && !leaderFound; attempt++ {
		time.Sleep(100 * time.Millisecond)

		leaderCount := 0
		var terms []int
		for i, node := range nodes {
			term, isLeader := node.GetState()
			terms = append(terms, term)
			if isLeader {
				leaderCount++
				leader = node
				leaderID = i
			}
		}

		t.Logf("Attempt %d: terms=%v, leaders=%d", attempt+1, terms, leaderCount)

		if leaderCount == 1 {
			leaderFound = true
			t.Logf("Leader elected: node %d", leaderID)
		} else if leaderCount > 1 {
			t.Fatalf("Multiple leaders detected: %d", leaderCount)
		}
	}

	if !leaderFound {
		t.Fatal("No leader elected within timeout")
	}

	// Test command submission
	index, term, isLeader := leader.Submit("test command")
	if !isLeader {
		t.Fatal("Leader lost leadership during submission")
	}

	t.Logf("Command submitted at index %d, term %d", index, term)

	// Wait for replication
	time.Sleep(500 * time.Millisecond)

	// Check all nodes have same commit index
	for i, node := range nodes {
		commitIndex := node.GetCommitIndex()
		if commitIndex != index {
			t.Errorf("Node %d has commit index %d, expected %d", i, commitIndex, index)
		}
	}
}

// Multi-node transport that allows nodes to communicate
type multiNodeTransport struct {
	id       int
	registry *nodeRegistry
	handler  RPCHandler
}

type nodeRegistry struct {
	mu    sync.RWMutex
	nodes map[int]RPCHandler
}

func (t *multiNodeTransport) SendRequestVote(serverID int, args *RequestVoteArgs) (*RequestVoteReply, error) {
	t.registry.mu.RLock()
	handler, exists := t.registry.nodes[serverID]
	t.registry.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("node %d not found", serverID)
	}

	reply := &RequestVoteReply{}
	err := handler.RequestVote(args, reply)
	return reply, err
}

func (t *multiNodeTransport) SendAppendEntries(serverID int, args *AppendEntriesArgs) (*AppendEntriesReply, error) {
	t.registry.mu.RLock()
	handler, exists := t.registry.nodes[serverID]
	t.registry.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("node %d not found", serverID)
	}

	reply := &AppendEntriesReply{}
	err := handler.AppendEntries(args, reply)
	return reply, err
}

func (t *multiNodeTransport) SendInstallSnapshot(serverID int, args *InstallSnapshotArgs) (*InstallSnapshotReply, error) {
	t.registry.mu.RLock()
	handler, exists := t.registry.nodes[serverID]
	t.registry.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("node %d not found", serverID)
	}

	reply := &InstallSnapshotReply{}
	err := handler.InstallSnapshot(args, reply)
	return reply, err
}

func (t *multiNodeTransport) SetRPCHandler(handler RPCHandler) {
	t.handler = handler
}

func (t *multiNodeTransport) Start() error {
	return nil
}

func (t *multiNodeTransport) Stop() error {
	return nil
}

func (t *multiNodeTransport) GetAddress() string {
	return fmt.Sprintf("node-%d", t.id)
}
