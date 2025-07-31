package raft

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

// TestDebugMultiNode tests multi-node with detailed logging
func TestDebugMultiNode(t *testing.T) {
	// Enable debug logging
	logger := newTestLogger(t)

	// Create 3 nodes
	nodes := make([]Node, 3)
	registry := &debugNodeRegistry{
		nodes:  make(map[int]RPCHandler),
		logger: logger,
	}

	for i := 0; i < 3; i++ {
		config := &Config{
			ID:                 i,
			Peers:              []int{0, 1, 2},
			ElectionTimeoutMin: 150 * time.Millisecond,
			ElectionTimeoutMax: 300 * time.Millisecond,
			HeartbeatInterval:  50 * time.Millisecond,
			Logger:             logger,
		}

		transport := &debugTransport{
			id:       i,
			registry: registry,
			logger:   logger,
		}

		stateMachine := &testStateMachine{
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
		logger.Info("Starting node %d", i)
		if err := node.Start(ctx); err != nil {
			t.Fatalf("Failed to start node %d: %v", i, err)
		}
		defer node.Stop()
	}

	// Wait for leader election
	time.Sleep(1 * time.Second)

	// Check final state
	for i, node := range nodes {
		term, isLeader := node.GetState()
		logger.Info("Node %d: term=%d, isLeader=%v", i, term, isLeader)
	}
}

type testLogger struct {
	t      *testing.T
	mu     sync.RWMutex
	closed bool
}

func newTestLogger(t *testing.T) *testLogger {
	logger := &testLogger{t: t}
	// Register cleanup to mark logger as closed when test ends
	t.Cleanup(func() {
		logger.mu.Lock()
		logger.closed = true
		logger.mu.Unlock()
	})
	return logger
}

func (l *testLogger) log(level, format string, args ...interface{}) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	
	// Don't log if test has completed
	if l.closed {
		return
	}
	
	// Use a defer to catch any panic from t.Logf
	defer func() {
		if r := recover(); r != nil {
			// Silently ignore logging after test completion
		}
	}()
	
	l.t.Logf(level+" "+format, args...)
}

func (l *testLogger) Debug(format string, args ...interface{}) {
	l.log("[DEBUG]", format, args...)
}

func (l *testLogger) Info(format string, args ...interface{}) {
	l.log("[INFO]", format, args...)
}

func (l *testLogger) Warn(format string, args ...interface{}) {
	l.log("[WARN]", format, args...)
}

func (l *testLogger) Error(format string, args ...interface{}) {
	l.log("[ERROR]", format, args...)
}

type debugNodeRegistry struct {
	mu     sync.RWMutex
	nodes  map[int]RPCHandler
	logger Logger
}

type debugTransport struct {
	id       int
	registry *debugNodeRegistry
	handler  RPCHandler
	logger   Logger
}

func (t *debugTransport) SendRequestVote(serverID int, args *RequestVoteArgs) (*RequestVoteReply, error) {
	t.logger.Debug("Node %d sending RequestVote to node %d: term=%d", t.id, serverID, args.Term)

	t.registry.mu.RLock()
	handler, exists := t.registry.nodes[serverID]
	t.registry.mu.RUnlock()

	if !exists {
		t.logger.Debug("Node %d not found", serverID)
		return nil, fmt.Errorf("node %d not found", serverID)
	}

	reply := &RequestVoteReply{}
	err := handler.RequestVote(args, reply)

	t.logger.Debug("Node %d received RequestVote reply from node %d: granted=%v, term=%d",
		t.id, serverID, reply.VoteGranted, reply.Term)

	return reply, err
}

func (t *debugTransport) SendAppendEntries(serverID int, args *AppendEntriesArgs) (*AppendEntriesReply, error) {
	t.registry.mu.RLock()
	handler, exists := t.registry.nodes[serverID]
	t.registry.mu.RUnlock()

	if !exists {
		if t.logger != nil {
			t.logger.Debug("SendAppendEntries: node %d not found in registry", serverID)
		}
		return nil, fmt.Errorf("node %d not found", serverID)
	}

	reply := &AppendEntriesReply{}
	err := handler.AppendEntries(args, reply)
	return reply, err
}

func (t *debugTransport) SendInstallSnapshot(serverID int, args *InstallSnapshotArgs) (*InstallSnapshotReply, error) {
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

func (t *debugTransport) SetRPCHandler(handler RPCHandler) {
	t.handler = handler
}

func (t *debugTransport) Start() error {
	return nil
}

func (t *debugTransport) Stop() error {
	return nil
}

func (t *debugTransport) GetAddress() string {
	return fmt.Sprintf("node-%d", t.id)
}
