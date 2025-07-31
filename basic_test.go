package raft

import (
	"context"
	"sync"
	"testing"
	"time"
)

// TestBasicNodeCreation tests creating a single node
func TestBasicNodeCreation(t *testing.T) {
	// Create configuration
	config := &Config{
		ID:                 1,
		Peers:              []int{1},
		ElectionTimeoutMin: 100 * time.Millisecond,
		ElectionTimeoutMax: 200 * time.Millisecond,
		HeartbeatInterval:  50 * time.Millisecond,
	}

	// Create simple in-memory transport
	transport := &testTransport{
		responses: make(map[int]*RequestVoteReply),
	}

	// Create simple state machine
	stateMachine := &testStateMachine{
		data: make(map[string]string),
	}

	// Create node
	node, err := NewNode(config, transport, nil, stateMachine)
	if err != nil {
		t.Fatalf("Failed to create node: %v", err)
	}

	// Start node
	ctx := context.Background()
	if err := node.Start(ctx); err != nil {
		t.Fatalf("Failed to start node: %v", err)
	}
	defer node.Stop()

	// Give it time to run multiple election cycles
	for i := 0; i < 10; i++ {
		time.Sleep(100 * time.Millisecond)
		term, isLeader := node.GetState()
		t.Logf("Attempt %d: term=%d, isLeader=%v", i+1, term, isLeader)
		if isLeader {
			t.Log("Node became leader!")
			return
		}
	}

	// Final check
	term, isLeader := node.GetState()
	t.Logf("Final state: term=%d, isLeader=%v", term, isLeader)

	// Single node should become leader
	if !isLeader {
		t.Error("Single node should become leader")
	}
}

// Simple test transport
type testTransport struct {
	responses map[int]*RequestVoteReply
	handler   RPCHandler
}

func (t *testTransport) SendRequestVote(serverID int, args *RequestVoteArgs) (*RequestVoteReply, error) {
	if reply, ok := t.responses[serverID]; ok {
		return reply, nil
	}
	return &RequestVoteReply{Term: args.Term, VoteGranted: false}, nil
}

func (t *testTransport) SendAppendEntries(serverID int, args *AppendEntriesArgs) (*AppendEntriesReply, error) {
	return &AppendEntriesReply{Term: args.Term, Success: false}, nil
}

func (t *testTransport) SendInstallSnapshot(serverID int, args *InstallSnapshotArgs) (*InstallSnapshotReply, error) {
	return &InstallSnapshotReply{Term: args.Term}, nil
}

func (t *testTransport) SetRPCHandler(handler RPCHandler) {
	t.handler = handler
}

func (t *testTransport) Start() error {
	return nil
}

func (t *testTransport) Stop() error {
	return nil
}

func (t *testTransport) GetAddress() string {
	return "test://localhost"
}

// Simple test state machine
type testStateMachine struct {
	mu   sync.Mutex
	data map[string]string
}

func (sm *testStateMachine) Apply(entry LogEntry) interface{} {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	// Simple key-value store
	if cmd, ok := entry.Command.(string); ok {
		sm.data[cmd] = cmd
		return cmd
	}
	return nil
}

func (sm *testStateMachine) Snapshot() ([]byte, error) {
	return []byte("snapshot"), nil
}

func (sm *testStateMachine) Restore(snapshot []byte) error {
	return nil
}
