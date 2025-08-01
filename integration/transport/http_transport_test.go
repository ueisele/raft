package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
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
	basePort := 19000

	// Create peer discovery with all node addresses first
	peers := make(map[int]string)
	for i := 0; i < 3; i++ {
		peers[i] = fmt.Sprintf("localhost:%d", basePort+i)
	}
	discovery := transport.NewStaticPeerDiscovery(peers)

	// Use test ports to avoid conflicts
	for i := 0; i < 3; i++ {
		// Create HTTP transport with discovery
		transportConfig := &transport.Config{
			ServerID:   i,
			Address:    fmt.Sprintf("localhost:%d", basePort+i),
			RPCTimeout: 1000, // 1 second
		}
		httpTrans, err := httpTransport.NewHTTPTransport(transportConfig, discovery)
		if err != nil {
			t.Fatalf("Failed to create transport %d: %v", i, err)
		}
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
	}

	// Start all transports
	for i, trans := range transports {
		if err := trans.Start(); err != nil {
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
	basePort := 19100

	// Create peer discovery first
	peers := make(map[int]string)
	for i := 0; i < 3; i++ {
		peers[i] = fmt.Sprintf("localhost:%d", basePort+i)
	}
	discovery := transport.NewStaticPeerDiscovery(peers)

	for i := 0; i < 3; i++ {
		// Create HTTP transport with short timeout for faster failure detection
		transportConfig := &transport.Config{
			ServerID:   i,
			Address:    fmt.Sprintf("localhost:%d", basePort+i),
			RPCTimeout: 100, // 100ms for faster tests
		}
		httpTrans, err := httpTransport.NewHTTPTransport(transportConfig, discovery)
		if err != nil {
			t.Fatalf("Failed to create transport %d: %v", i, err)
		}
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
	}

	// Start all transports
	for i, trans := range transports {
		if err := trans.Start(); err != nil {
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
	basePort := 19200

	// Create peer discovery first
	peers := make(map[int]string)
	for i := 0; i < 3; i++ {
		peers[i] = fmt.Sprintf("localhost:%d", basePort+i)
	}
	discovery := transport.NewStaticPeerDiscovery(peers)

	for i := 0; i < 3; i++ {
		// Create HTTP transport
		transportConfig := &transport.Config{
			ServerID:   i,
			Address:    fmt.Sprintf("localhost:%d", basePort+i),
			RPCTimeout: 1000,
		}
		httpTrans, err := httpTransport.NewHTTPTransport(transportConfig, discovery)
		if err != nil {
			t.Fatalf("Failed to create transport %d: %v", i, err)
		}
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
	}

	// Start all transports
	for i, trans := range transports {
		if err := trans.Start(); err != nil {
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

// MockRPCHandler implements raft.RPCHandler for testing
type MockRPCHandler struct {
	requestVoteFunc     func(*raft.RequestVoteArgs, *raft.RequestVoteReply) error
	appendEntriesFunc   func(*raft.AppendEntriesArgs, *raft.AppendEntriesReply) error
	installSnapshotFunc func(*raft.InstallSnapshotArgs, *raft.InstallSnapshotReply) error
}

func (m *MockRPCHandler) RequestVote(args *raft.RequestVoteArgs, reply *raft.RequestVoteReply) error {
	if m.requestVoteFunc != nil {
		return m.requestVoteFunc(args, reply)
	}
	return nil
}

func (m *MockRPCHandler) AppendEntries(args *raft.AppendEntriesArgs, reply *raft.AppendEntriesReply) error {
	if m.appendEntriesFunc != nil {
		return m.appendEntriesFunc(args, reply)
	}
	return nil
}

func (m *MockRPCHandler) InstallSnapshot(args *raft.InstallSnapshotArgs, reply *raft.InstallSnapshotReply) error {
	if m.installSnapshotFunc != nil {
		return m.installSnapshotFunc(args, reply)
	}
	return nil
}

// TestHTTPTransport_StartStop tests starting and stopping real HTTP servers
func TestHTTPTransport_StartStop(t *testing.T) {
	config := &transport.Config{
		ServerID:   1,
		Address:    "localhost:18001",
		RPCTimeout: 500,
	}

	discovery := transport.NewStaticPeerDiscovery(map[int]string{1: "localhost:8001"})
	transport, err := httpTransport.NewHTTPTransport(config, discovery)
	if err != nil {
		t.Fatalf("failed to create transport: %v", err)
	}
	handler := &MockRPCHandler{}

	// Test starting without handler
	err = transport.Start()
	if err == nil {
		t.Error("expected error when starting without handler")
	}

	// Set handler and start
	transport.SetRPCHandler(handler)
	err = transport.Start()
	if err != nil {
		t.Fatalf("failed to start transport: %v", err)
	}

	// Give server time to start
	time.Sleep(100 * time.Millisecond)

	// Test that server is listening
	resp, err := http.Get("http://" + transport.GetAddress() + "/raft/requestvote")
	if err != nil {
		t.Fatalf("failed to connect to server: %v", err)
	}
	resp.Body.Close()

	// Stop transport
	err = transport.Stop()
	if err != nil {
		t.Fatalf("failed to stop transport: %v", err)
	}

	// Test stopping already stopped transport
	err = transport.Stop()
	if err != nil {
		t.Errorf("unexpected error stopping already stopped transport: %v", err)
	}
}

// TestHTTPTransport_HandleRequestVote tests handling RequestVote RPCs with real server
func TestHTTPTransport_HandleRequestVote(t *testing.T) {
	config := &transport.Config{
		ServerID:   1,
		Address:    "localhost:18002",
		RPCTimeout: 500,
	}

	discovery := transport.NewStaticPeerDiscovery(map[int]string{1: "localhost:8001"})
	transport, err := httpTransport.NewHTTPTransport(config, discovery)
	if err != nil {
		t.Fatalf("failed to create transport: %v", err)
	}
	
	handler := &MockRPCHandler{
		requestVoteFunc: func(args *raft.RequestVoteArgs, reply *raft.RequestVoteReply) error {
			if args.Term != 5 || args.CandidateID != 2 {
				t.Errorf("unexpected args: %+v", args)
			}
			reply.Term = 5
			reply.VoteGranted = true
			return nil
		},
	}

	transport.SetRPCHandler(handler)
	if err := transport.Start(); err != nil {
		t.Fatalf("failed to start transport: %v", err)
	}
	defer transport.Stop()

	// Give server time to start
	time.Sleep(100 * time.Millisecond)

	// Send request
	args := raft.RequestVoteArgs{
		Term:        5,
		CandidateID: 2,
	}
	
	body, _ := json.Marshal(args)
	resp, err := http.Post("http://"+transport.GetAddress()+"/raft/requestvote", "application/json", bytes.NewBuffer(body))
	if err != nil {
		t.Fatalf("failed to send request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}

	var reply raft.RequestVoteReply
	if err := json.NewDecoder(resp.Body).Decode(&reply); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if reply.Term != 5 || !reply.VoteGranted {
		t.Errorf("unexpected reply: %+v", reply)
	}
}

// TestHTTPTransport_HandleAppendEntries tests handling AppendEntries RPCs with real server
func TestHTTPTransport_HandleAppendEntries(t *testing.T) {
	config := &transport.Config{
		ServerID:   1,
		Address:    "localhost:18003",
		RPCTimeout: 500,
	}

	discovery := transport.NewStaticPeerDiscovery(map[int]string{1: "localhost:8001"})
	transport, err := httpTransport.NewHTTPTransport(config, discovery)
	if err != nil {
		t.Fatalf("failed to create transport: %v", err)
	}
	
	handler := &MockRPCHandler{
		appendEntriesFunc: func(args *raft.AppendEntriesArgs, reply *raft.AppendEntriesReply) error {
			if args.Term != 5 || args.LeaderID != 2 {
				t.Errorf("unexpected args: %+v", args)
			}
			reply.Term = 5
			reply.Success = true
			return nil
		},
	}

	transport.SetRPCHandler(handler)
	if err := transport.Start(); err != nil {
		t.Fatalf("failed to start transport: %v", err)
	}
	defer transport.Stop()

	// Give server time to start
	time.Sleep(100 * time.Millisecond)

	// Send request
	args := raft.AppendEntriesArgs{
		Term:     5,
		LeaderID: 2,
	}
	
	body, _ := json.Marshal(args)
	resp, err := http.Post("http://"+transport.GetAddress()+"/raft/appendentries", "application/json", bytes.NewBuffer(body))
	if err != nil {
		t.Fatalf("failed to send request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}

	var reply raft.AppendEntriesReply
	if err := json.NewDecoder(resp.Body).Decode(&reply); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if reply.Term != 5 || !reply.Success {
		t.Errorf("unexpected reply: %+v", reply)
	}
}

// TestHTTPTransport_HandleInstallSnapshot tests handling InstallSnapshot RPCs with real server
func TestHTTPTransport_HandleInstallSnapshot(t *testing.T) {
	config := &transport.Config{
		ServerID:   1,
		Address:    "localhost:18004",
		RPCTimeout: 500,
	}

	discovery := transport.NewStaticPeerDiscovery(map[int]string{1: "localhost:8001"})
	transport, err := httpTransport.NewHTTPTransport(config, discovery)
	if err != nil {
		t.Fatalf("failed to create transport: %v", err)
	}
	
	handler := &MockRPCHandler{
		installSnapshotFunc: func(args *raft.InstallSnapshotArgs, reply *raft.InstallSnapshotReply) error {
			if args.Term != 5 || args.LeaderID != 2 {
				t.Errorf("unexpected args: %+v", args)
			}
			reply.Term = 5
			return nil
		},
	}

	transport.SetRPCHandler(handler)
	if err := transport.Start(); err != nil {
		t.Fatalf("failed to start transport: %v", err)
	}
	defer transport.Stop()

	// Give server time to start
	time.Sleep(100 * time.Millisecond)

	// Send request
	args := raft.InstallSnapshotArgs{
		Term:     5,
		LeaderID: 2,
		Data:     []byte("test"),
	}
	
	body, _ := json.Marshal(args)
	resp, err := http.Post("http://"+transport.GetAddress()+"/raft/installsnapshot", "application/json", bytes.NewBuffer(body))
	if err != nil {
		t.Fatalf("failed to send request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}

	var reply raft.InstallSnapshotReply
	if err := json.NewDecoder(resp.Body).Decode(&reply); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if reply.Term != 5 {
		t.Errorf("unexpected reply: %+v", reply)
	}
}

// TestHTTPTransport_HandleInvalidMethod tests handling invalid HTTP methods
func TestHTTPTransport_HandleInvalidMethod(t *testing.T) {
	config := &transport.Config{
		ServerID:   1,
		Address:    "localhost:18005",
		RPCTimeout: 500,
	}

	discovery := transport.NewStaticPeerDiscovery(map[int]string{1: "localhost:8001"})
	transport, err := httpTransport.NewHTTPTransport(config, discovery)
	if err != nil {
		t.Fatalf("failed to create transport: %v", err)
	}
	transport.SetRPCHandler(&MockRPCHandler{})
	
	if err := transport.Start(); err != nil {
		t.Fatalf("failed to start transport: %v", err)
	}
	defer transport.Stop()

	// Give server time to start
	time.Sleep(100 * time.Millisecond)

	// Test GET request (should fail)
	resp, err := http.Get("http://" + transport.GetAddress() + "/raft/requestvote")
	if err != nil {
		t.Fatalf("failed to send request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("expected status 405, got %d", resp.StatusCode)
	}
}

// TestHTTPTransport_HandleInvalidJSON tests handling invalid JSON requests
func TestHTTPTransport_HandleInvalidJSON(t *testing.T) {
	config := &transport.Config{
		ServerID:   1,
		Address:    "localhost:18006",
		RPCTimeout: 500,
	}

	discovery := transport.NewStaticPeerDiscovery(map[int]string{1: "localhost:8001"})
	transport, err := httpTransport.NewHTTPTransport(config, discovery)
	if err != nil {
		t.Fatalf("failed to create transport: %v", err)
	}
	transport.SetRPCHandler(&MockRPCHandler{})
	
	if err := transport.Start(); err != nil {
		t.Fatalf("failed to start transport: %v", err)
	}
	defer transport.Stop()

	// Give server time to start
	time.Sleep(100 * time.Millisecond)

	// Send invalid JSON
	resp, err := http.Post("http://"+transport.GetAddress()+"/raft/requestvote", "application/json", bytes.NewBufferString("invalid json"))
	if err != nil {
		t.Fatalf("failed to send request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", resp.StatusCode)
	}
}

// TestHTTPTransport_HandleRPCError tests handling RPC handler errors
func TestHTTPTransport_HandleRPCError(t *testing.T) {
	config := &transport.Config{
		ServerID:   1,
		Address:    "localhost:18007",
		RPCTimeout: 500,
	}

	discovery := transport.NewStaticPeerDiscovery(map[int]string{1: "localhost:8001"})
	transport, err := httpTransport.NewHTTPTransport(config, discovery)
	if err != nil {
		t.Fatalf("failed to create transport: %v", err)
	}
	
	handler := &MockRPCHandler{
		requestVoteFunc: func(args *raft.RequestVoteArgs, reply *raft.RequestVoteReply) error {
			return fmt.Errorf("handler error")
		},
	}

	transport.SetRPCHandler(handler)
	if err := transport.Start(); err != nil {
		t.Fatalf("failed to start transport: %v", err)
	}
	defer transport.Stop()

	// Give server time to start
	time.Sleep(100 * time.Millisecond)

	// Send request
	args := raft.RequestVoteArgs{Term: 5}
	body, _ := json.Marshal(args)
	resp, err := http.Post("http://"+transport.GetAddress()+"/raft/requestvote", "application/json", bytes.NewBuffer(body))
	if err != nil {
		t.Fatalf("failed to send request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", resp.StatusCode)
	}
}
