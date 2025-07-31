package raft

import (
	"context"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	// "github.com/ueisele/raft/test" - removed to avoid import cycle
)

// Global storage for persistence data in tests
var persistenceStore = struct {
	mu    sync.Mutex
	data  map[string]*PersistentState
	snaps map[string]*Snapshot
}{
	data:  make(map[string]*PersistentState),
	snaps: make(map[string]*Snapshot),
}

// filePersistence is a simple file-based persistence for testing
type filePersistence struct {
	dataDir string
}

func newFilePersistence(dataDir string) *filePersistence {
	return &filePersistence{
		dataDir: dataDir,
	}
}

func (p *filePersistence) SaveState(state *PersistentState) error {
	persistenceStore.mu.Lock()
	defer persistenceStore.mu.Unlock()

	if state != nil {
		// Make a deep copy to avoid mutation
		stateCopy := PersistentState{
			CurrentTerm: state.CurrentTerm,
			VotedFor:    state.VotedFor,
			CommitIndex: state.CommitIndex,
		}
		// Deep copy the log entries
		if state.Log != nil {
			stateCopy.Log = make([]LogEntry, len(state.Log))
			copy(stateCopy.Log, state.Log)
		}
		persistenceStore.data[p.dataDir] = &stateCopy
	}
	return nil
}

func (p *filePersistence) LoadState() (*PersistentState, error) {
	persistenceStore.mu.Lock()
	defer persistenceStore.mu.Unlock()

	if state, exists := persistenceStore.data[p.dataDir]; exists {
		// Return a deep copy to avoid mutation
		stateCopy := PersistentState{
			CurrentTerm: state.CurrentTerm,
			VotedFor:    state.VotedFor,
			CommitIndex: state.CommitIndex,
		}
		// Deep copy the log entries
		if state.Log != nil {
			stateCopy.Log = make([]LogEntry, len(state.Log))
			copy(stateCopy.Log, state.Log)
		}
		return &stateCopy, nil
	}
	// Return empty state if nothing was saved
	return &PersistentState{}, nil
}

func (p *filePersistence) SaveSnapshot(snapshot *Snapshot) error {
	persistenceStore.mu.Lock()
	defer persistenceStore.mu.Unlock()
	
	if snapshot != nil {
		// Make a copy to avoid mutation
		snapCopy := *snapshot
		persistenceStore.snaps[p.dataDir] = &snapCopy
	}
	return nil
}

func (p *filePersistence) LoadSnapshot() (*Snapshot, error) {
	persistenceStore.mu.Lock()
	defer persistenceStore.mu.Unlock()
	
	if snap, exists := persistenceStore.snaps[p.dataDir]; exists {
		// Return a copy to avoid mutation
		snapCopy := *snap
		return &snapCopy, nil
	}
	return nil, nil
}

func (p *filePersistence) HasSnapshot() bool {
	persistenceStore.mu.Lock()
	defer persistenceStore.mu.Unlock()
	
	_, exists := persistenceStore.snaps[p.dataDir]
	return exists
}

// TestPersistenceWithCrash tests that state is correctly persisted and recovered after crash
func TestPersistenceWithCrash(t *testing.T) {
	// Clear persistence store
	persistenceStore.mu.Lock()
	persistenceStore.data = make(map[string]*PersistentState)
	persistenceStore.snaps = make(map[string]*Snapshot)
	persistenceStore.mu.Unlock()
	
	// Create temporary directory for persistence
	tempDir, err := ioutil.TempDir("", "raft-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create a single node with persistence
	nodeID := 0
	config := &Config{
		ID:                 nodeID,
		Peers:              []int{0},
		ElectionTimeoutMin: 150 * time.Millisecond,
		ElectionTimeoutMax: 300 * time.Millisecond,
		HeartbeatInterval:  50 * time.Millisecond,
	}

	transport := &testTransport{
		responses: make(map[int]*RequestVoteReply),
	}

	stateMachine := &testStateMachine{
		mu:   sync.Mutex{},
		data: make(map[string]string),
	}

	// Create persistence
	persistence := newFilePersistence(filepath.Join(tempDir, fmt.Sprintf("node-%d", nodeID)))

	// Create and start node
	node, err := NewNode(config, transport, persistence, stateMachine)
	if err != nil {
		t.Fatalf("Failed to create node: %v", err)
	}

	ctx := context.Background()
	if err := node.Start(ctx); err != nil {
		t.Fatalf("Failed to start node: %v", err)
	}

	// Wait for node to become leader
	timing := DefaultTimingConfig()
	timing.ElectionTimeout = 300 * time.Millisecond
	leaderID := WaitForLeaderWithConfig(t, []Node{node}, timing)
	if leaderID < 0 {
		t.Fatal("Node should be leader")
	}

	// Submit some commands
	commands := []string{"cmd1", "cmd2", "cmd3"}
	lastIndex := 0
	for _, cmd := range commands {
		index, _, isLeader := node.Submit(cmd)
		if !isLeader {
			t.Fatal("Lost leadership")
		}
		lastIndex = index
		t.Logf("Submitted %s at index %d", cmd, index)
	}

	// Get current term
	currentTerm, _ := node.GetState()

	// Stop the node (simulate crash)
	node.Stop()
	t.Log("Node stopped (simulated crash)")

	// Create new node with same persistence (simulate restart)
	transport2 := &testTransport{
		responses: make(map[int]*RequestVoteReply),
	}

	stateMachine2 := &testStateMachine{
		mu:   sync.Mutex{},
		data: make(map[string]string),
	}

	node2, err := NewNode(config, transport2, persistence, stateMachine2)
	if err != nil {
		t.Fatalf("Failed to create node after restart: %v", err)
	}

	if err := node2.Start(ctx); err != nil {
		t.Fatalf("Failed to start node after restart: %v", err)
	}
	defer node2.Stop()

	// Wait for recovery
	WaitForConditionWithProgress(t, func() (bool, string) {
		term, _ := node2.GetState()
		logLen := node2.GetLogLength()
		return term >= currentTerm && logLen >= lastIndex,
			fmt.Sprintf("term=%d (need >=%d), log=%d (need >=%d)", term, currentTerm, logLen, lastIndex)
	}, timing.ElectionTimeout, "node recovery")

	// Check that term was preserved (should be at least as high)
	newTerm, _ := node2.GetState()
	if newTerm < currentTerm {
		t.Errorf("Term decreased after restart: was %d, now %d", currentTerm, newTerm)
	}

	// Check that log length is preserved
	logLength := node2.GetLogLength()
	if logLength < lastIndex {
		t.Errorf("Log length decreased after restart: expected at least %d, got %d", lastIndex, logLength)
	}

	t.Logf("Successfully recovered: term=%d, log length=%d", newTerm, logLength)
}

// TestPersistenceWithMultipleNodes tests persistence with multiple nodes and crashes
func TestPersistenceWithMultipleNodes(t *testing.T) {
	// Clear persistence store
	persistenceStore.mu.Lock()
	persistenceStore.data = make(map[string]*PersistentState)
	persistenceStore.snaps = make(map[string]*Snapshot)
	persistenceStore.mu.Unlock()
	
	// Create temporary directory for persistence
	tempDir, err := ioutil.TempDir("", "raft-test-multi-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create 3 nodes
	numNodes := 3
	nodes := make([]Node, numNodes)
	persistences := make([]Persistence, numNodes)

	registry := &debugNodeRegistry{
		nodes:  make(map[int]RPCHandler),
		logger: newTestLogger(t),
	}

	// Create nodes with persistence
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

		// Create persistence for each node
		persistence := newFilePersistence(filepath.Join(tempDir, fmt.Sprintf("node-%d", i)))
		persistences[i] = persistence

		node, err := NewNode(config, transport, persistence, stateMachine)
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
	}

	// Wait for leader election
	timing := DefaultTimingConfig()
	timing.ElectionTimeout = 500 * time.Millisecond
	leaderID := WaitForLeaderWithConfig(t, nodes, timing)
	if leaderID < 0 {
		t.Fatal("No leader elected")
	}

	t.Logf("Initial leader is node %d", leaderID)

	// Submit commands
	leader := nodes[leaderID]
	for i := 0; i < 5; i++ {
		cmd := fmt.Sprintf("command-%d", i)
		index, _, isLeader := leader.Submit(cmd)
		if !isLeader {
			t.Fatal("Lost leadership")
		}
		t.Logf("Submitted %s at index %d", cmd, index)
	}

	// Wait for replication
	WaitForCommitIndexWithConfig(t, nodes, 5, timing)

	// Record commit indices
	commitIndices := make([]int, numNodes)
	for i, node := range nodes {
		commitIndices[i] = node.GetCommitIndex()
		t.Logf("Node %d commit index: %d", i, commitIndices[i])
	}

	// Stop all nodes
	for _, node := range nodes {
		node.Stop()
	}

	t.Log("All nodes stopped")

	// Restart all nodes
	newNodes := make([]Node, numNodes)
	newRegistry := &debugNodeRegistry{
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
			registry: newRegistry,
			logger:   newTestLogger(t),
		}

		stateMachine := &testStateMachine{
			mu:   sync.Mutex{},
			data: make(map[string]string),
		}

		// Reuse persistence
		node, err := NewNode(config, transport, persistences[i], stateMachine)
		if err != nil {
			t.Fatalf("Failed to recreate node %d: %v", i, err)
		}

		newNodes[i] = node
		newRegistry.nodes[i] = node.(RPCHandler)
	}

	// Start all nodes again
	for i, node := range newNodes {
		if err := node.Start(ctx); err != nil {
			t.Fatalf("Failed to restart node %d: %v", i, err)
		}
	}

	// Wait for election and stabilization
	newLeaderID := WaitForLeaderWithConfig(t, newNodes, timing)
	if newLeaderID < 0 {
		t.Fatal("No leader after restart")
	}

	// Check that nodes recovered their state
	for i, node := range newNodes {
		newCommitIndex := node.GetCommitIndex()
		// Commit index should be at least what it was before
		if newCommitIndex < commitIndices[i] {
			t.Errorf("Node %d: commit index decreased from %d to %d",
				i, commitIndices[i], newCommitIndex)
		}
		t.Logf("Node %d recovered with commit index %d", i, newCommitIndex)
	}

	// Find new leader and submit more commands
	var newLeader Node
	if newLeaderID >= 0 && newLeaderID < len(newNodes) {
		newLeader = newNodes[newLeaderID]
	}

	if newLeader == nil {
		t.Fatal("No leader after restart")
	}

	t.Logf("New leader after restart is node %d", newLeaderID)

	// Submit more commands to verify system is working
	var lastIndex int
	for i := 0; i < 3; i++ {
		cmd := fmt.Sprintf("post-restart-command-%d", i)
		index, _, isLeader := newLeader.Submit(cmd)
		if !isLeader {
			t.Fatal("Lost leadership after restart")
		}
		t.Logf("Submitted %s at index %d", cmd, index)
		lastIndex = index
	}

	// Wait for replication
	WaitForCommitIndexWithConfig(t, newNodes, lastIndex, timing)

	// Verify all nodes have same commit index
	var finalCommitIndices []int
	for i, node := range newNodes {
		idx := node.GetCommitIndex()
		finalCommitIndices = append(finalCommitIndices, idx)
		t.Logf("Node %d final commit index: %d", i, idx)
	}

	// All should be the same
	for i := 1; i < len(finalCommitIndices); i++ {
		if finalCommitIndices[i] != finalCommitIndices[0] {
			t.Errorf("Nodes have different commit indices: %v", finalCommitIndices)
			break
		}
	}
	
	// Stop all nodes explicitly before test ends
	for _, node := range newNodes {
		node.Stop()
	}
	
	// Small delay for cleanup
	time.Sleep(50 * time.Millisecond)
}
