package basic

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/ueisele/raft"
)

// TestBasicNodeCreation tests creating a single node
func TestBasicNodeCreation(t *testing.T) {
	// Create configuration
	config := &raft.Config{
		ID:                 1,
		Peers:              []int{1},
		ElectionTimeoutMin: 100 * time.Millisecond,
		ElectionTimeoutMax: 200 * time.Millisecond,
		HeartbeatInterval:  50 * time.Millisecond,
	}

	// Create simple in-memory transport
	transport := &testTransport{
		responses: make(map[int]*raft.RequestVoteReply),
	}

	// Create simple state machine
	stateMachine := &testStateMachine{
		data: make(map[string]string),
	}

	// Create node
	node, err := raft.NewNode(config, transport, nil, stateMachine)
	if err != nil {
		t.Fatalf("Failed to create node: %v", err)
	}

	// Start node
	ctx := context.Background()
	if err := node.Start(ctx); err != nil {
		t.Fatalf("Failed to start node: %v", err)
	}
	defer node.Stop() //nolint:errcheck // test cleanup

	// Wait for the single node to become leader using proper synchronization
	// Single node should become leader quickly (within a few election timeouts)
	timeout := 2 * config.ElectionTimeoutMax
	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop() //nolint:errcheck // background ticker cleanup

	attempts := 0
	for time.Now().Before(deadline) {
		<-ticker.C
		attempts++
		term, isLeader := node.GetState()
		if isLeader {
				t.Logf("Node became leader after %d attempts (term=%d)", attempts, term)
				return
			}
			if attempts%10 == 0 {
				t.Logf("Still waiting... attempt %d: term=%d, isLeader=%v", attempts, term, isLeader)
		}
	}

	// If we get here, node failed to become leader
	term, isLeader := node.GetState()
	t.Fatalf("Single node failed to become leader within %v. Final state: term=%d, isLeader=%v",
		timeout, term, isLeader)
}

// Simple test transport
type testTransport struct {
	responses map[int]*raft.RequestVoteReply
	handler   raft.RPCHandler
}

func (t *testTransport) SendRequestVote(serverID int, args *raft.RequestVoteArgs) (*raft.RequestVoteReply, error) {
	if reply, ok := t.responses[serverID]; ok {
		return reply, nil
	}
	return &raft.RequestVoteReply{Term: args.Term, VoteGranted: false}, nil
}

func (t *testTransport) SendAppendEntries(serverID int, args *raft.AppendEntriesArgs) (*raft.AppendEntriesReply, error) {
	return &raft.AppendEntriesReply{Term: args.Term, Success: false}, nil
}

func (t *testTransport) SendInstallSnapshot(serverID int, args *raft.InstallSnapshotArgs) (*raft.InstallSnapshotReply, error) {
	return &raft.InstallSnapshotReply{Term: args.Term}, nil
}

func (t *testTransport) SetRPCHandler(handler raft.RPCHandler) {
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

func (sm *testStateMachine) Apply(entry raft.LogEntry) interface{} {
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
