package transport

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/ueisele/raft"
	"github.com/ueisele/raft/transport"
	httpTransport "github.com/ueisele/raft/transport/http"
)

// testStateMachine is a simple state machine for testing
type testStateMachine struct {
	mu              sync.Mutex
	appliedCommands []interface{}
}

func (sm *testStateMachine) Apply(entry raft.LogEntry) interface{} {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	sm.appliedCommands = append(sm.appliedCommands, entry.Command)
	return entry.Command
}

func (sm *testStateMachine) Snapshot() ([]byte, error) {
	return []byte("snapshot"), nil
}

func (sm *testStateMachine) Restore(snapshot []byte) error {
	return nil
}

// TestHTTPTransportBasicCluster tests basic cluster operations with HTTP transport
func TestHTTPTransportBasicCluster(t *testing.T) {
	// Create a 3-node cluster using HTTP transport
	nodes := make([]raft.Node, 3)
	transports := make([]*httpTransport.HTTPTransport, 3)

	// Use default ports that HTTPTransport expects (8000 + serverID)
	for i := 0; i < 3; i++ {
		// Create HTTP transport
		transportConfig := &transport.Config{
			ServerID:   i,
			Address:    fmt.Sprintf("localhost:%d", 8000+i),
			RPCTimeout: 1000, // 1 second
		}
		httpTrans := httpTransport.NewHTTPTransport(transportConfig)
		transports[i] = httpTrans

		// Create persistence
		persistence := raft.NewMockPersistence()

		// Create state machine
		stateMachine := &testStateMachine{
			appliedCommands: make([]interface{}, 0),
		}

		// Create Raft config
		config := &raft.Config{
			ID:                 i,
			Peers:              []int{0, 1, 2},
			ElectionTimeoutMin: 150 * time.Millisecond,
			ElectionTimeoutMax: 300 * time.Millisecond,
			HeartbeatInterval:  50 * time.Millisecond,
			MaxLogSize:         1000,
		}

		// Create node
		node, err := raft.NewNode(config, httpTrans, persistence, stateMachine)
		if err != nil {
			t.Fatalf("Failed to create node %d: %v", i, err)
		}
		nodes[i] = node

		// Start transport
		if err := httpTrans.Start(); err != nil {
			t.Fatalf("Failed to start transport %d: %v", i, err)
		}
	}

	// Start all nodes
	ctx := context.Background()
	for i, node := range nodes {
		if err := node.Start(ctx); err != nil {
			t.Fatalf("Failed to start node %d: %v", i, err)
		}
	}

	// Clean up
	defer func() {
		for _, node := range nodes {
			node.Stop()
		}
		for _, trans := range transports {
			trans.Stop()
		}
	}()

	// Wait for leader election
	time.Sleep(1 * time.Second)

	// Find the leader
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

	// Submit a command
	command := "test-command"
	index, term, isLeader := nodes[leaderID].Submit(command)
	if !isLeader {
		t.Fatal("Expected node to be leader")
	}

	t.Logf("Submitted command at index %d, term %d", index, term)

	// Wait for replication
	time.Sleep(500 * time.Millisecond)

	// Verify all nodes have the command
	for i, node := range nodes {
		entry := node.GetLogEntry(index)
		if entry == nil {
			t.Errorf("Node %d missing log entry at index %d", i, index)
			continue
		}
		if entry.Command != command {
			t.Errorf("Node %d has wrong command: got %v, want %v", i, entry.Command, command)
		}
	}
}

// TestHTTPTransportNetworkFailure tests HTTP transport behavior during network failures
func TestHTTPTransportNetworkFailure(t *testing.T) {
	// Create a 3-node cluster
	nodes := make([]raft.Node, 3)
	transports := make([]*httpTransport.HTTPTransport, 3)

	for i := 0; i < 3; i++ {
		// Create HTTP transport with short timeout for faster failure detection
		transportConfig := &transport.Config{
			ServerID:   i,
			Address:    fmt.Sprintf("localhost:%d", 8000+i),
			RPCTimeout: 100, // 100ms for faster tests
		}
		httpTrans := httpTransport.NewHTTPTransport(transportConfig)
		transports[i] = httpTrans

		// Create persistence
		persistence := raft.NewMockPersistence()

		// Create state machine
		stateMachine := &testStateMachine{
			appliedCommands: make([]interface{}, 0),
		}

		// Create Raft config
		config := &raft.Config{
			ID:                 i,
			Peers:              []int{0, 1, 2},
			ElectionTimeoutMin: 150 * time.Millisecond,
			ElectionTimeoutMax: 300 * time.Millisecond,
			HeartbeatInterval:  50 * time.Millisecond,
			MaxLogSize:         1000,
		}

		// Create node
		node, err := raft.NewNode(config, httpTrans, persistence, stateMachine)
		if err != nil {
			t.Fatalf("Failed to create node %d: %v", i, err)
		}
		nodes[i] = node

		// Start transport
		if err := httpTrans.Start(); err != nil {
			t.Fatalf("Failed to start transport %d: %v", i, err)
		}
	}

	// Start all nodes
	ctx := context.Background()
	for i, node := range nodes {
		if err := node.Start(ctx); err != nil {
			t.Fatalf("Failed to start node %d: %v", i, err)
		}
	}

	// Clean up
	defer func() {
		for _, node := range nodes {
			node.Stop()
		}
		for _, trans := range transports {
			trans.Stop()
		}
	}()

	// Wait for leader election
	time.Sleep(1 * time.Second)

	// Find the leader
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

	// Stop one follower's transport to simulate network failure
	followerID := (leaderID + 1) % 3
	t.Logf("Stopping transport for follower %d", followerID)
	transports[followerID].Stop()

	// Submit a command - should still work with 2 nodes
	command := "command-during-failure"
	index, _, isLeader := nodes[leaderID].Submit(command)
	if !isLeader {
		t.Fatal("Expected node to still be leader")
	}

	// Wait for replication to working node
	time.Sleep(500 * time.Millisecond)

	// Check that the working follower has the command
	workingFollowerID := (leaderID + 2) % 3
	entry := nodes[workingFollowerID].GetLogEntry(index)
	if entry == nil || entry.Command != command {
		t.Error("Working follower doesn't have the command")
	}

	// The disconnected follower should not have the command
	entry = nodes[followerID].GetLogEntry(index)
	if entry != nil {
		t.Error("Disconnected follower unexpectedly has the command")
	}

	// Restart the follower's transport
	t.Logf("Restarting transport for follower %d", followerID)
	if err := transports[followerID].Start(); err != nil {
		t.Fatalf("Failed to restart transport: %v", err)
	}

	// Wait for the follower to catch up
	time.Sleep(1 * time.Second)

	// Now the follower should have caught up
	entry = nodes[followerID].GetLogEntry(index)
	if entry == nil || entry.Command != command {
		t.Error("Reconnected follower failed to catch up")
	}
}

// TestHTTPTransportHighLoad tests HTTP transport under high load
func TestHTTPTransportHighLoad(t *testing.T) {
	// Create a 3-node cluster
	nodes := make([]raft.Node, 3)
	transports := make([]*httpTransport.HTTPTransport, 3)

	for i := 0; i < 3; i++ {
		// Create HTTP transport
		transportConfig := &transport.Config{
			ServerID:   i,
			Address:    fmt.Sprintf("localhost:%d", 8000+i),
			RPCTimeout: 1000,
		}
		httpTrans := httpTransport.NewHTTPTransport(transportConfig)
		transports[i] = httpTrans

		// Create persistence
		persistence := raft.NewMockPersistence()

		// Create state machine
		stateMachine := &testStateMachine{
			appliedCommands: make([]interface{}, 0),
		}

		// Create Raft config
		config := &raft.Config{
			ID:                 i,
			Peers:              []int{0, 1, 2},
			ElectionTimeoutMin: 150 * time.Millisecond,
			ElectionTimeoutMax: 300 * time.Millisecond,
			HeartbeatInterval:  50 * time.Millisecond,
			MaxLogSize:         10000,
		}

		// Create node
		node, err := raft.NewNode(config, httpTrans, persistence, stateMachine)
		if err != nil {
			t.Fatalf("Failed to create node %d: %v", i, err)
		}
		nodes[i] = node

		// Start transport
		if err := httpTrans.Start(); err != nil {
			t.Fatalf("Failed to start transport %d: %v", i, err)
		}
	}

	// Start all nodes
	ctx := context.Background()
	for i, node := range nodes {
		if err := node.Start(ctx); err != nil {
			t.Fatalf("Failed to start node %d: %v", i, err)
		}
	}

	// Clean up
	defer func() {
		for _, node := range nodes {
			node.Stop()
		}
		for _, trans := range transports {
			trans.Stop()
		}
	}()

	// Wait for leader election
	time.Sleep(1 * time.Second)

	// Find the leader
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

	// Submit many commands rapidly
	numCommands := 100
	startTime := time.Now()

	for i := 0; i < numCommands; i++ {
		command := fmt.Sprintf("command-%d", i)
		_, _, isLeader := nodes[leaderID].Submit(command)
		if !isLeader {
			t.Fatalf("Lost leadership during high load at command %d", i)
		}
	}

	submitDuration := time.Since(startTime)
	t.Logf("Submitted %d commands in %v", numCommands, submitDuration)

	// Wait for replication
	time.Sleep(2 * time.Second)

	// Verify all nodes have all commands
	for nodeID, node := range nodes {
		commitIndex := node.GetCommitIndex()
		if commitIndex < numCommands {
			t.Errorf("Node %d only committed %d/%d commands", nodeID, commitIndex, numCommands)
		}
	}

	// Calculate throughput
	throughput := float64(numCommands) / submitDuration.Seconds()
	t.Logf("Throughput: %.2f commands/second", throughput)
}
