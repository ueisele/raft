package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ueisele/raft"
	"github.com/ueisele/raft/integration/helpers"
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

	// Create peer discovery with all node addresses first
	ports, err := helpers.GetFreePorts(3)
	if err != nil {
		t.Fatalf("Failed to get free ports: %v", err)
	}

	peers := make(map[int]string)
	for i := 0; i < 3; i++ {
		peers[i] = fmt.Sprintf("localhost:%d", ports[i])
	}
	discovery := transport.NewStaticPeerDiscovery(peers)

	// Use test ports to avoid conflicts
	for i := 0; i < 3; i++ {
		// Create HTTP transport with discovery
		transportConfig := &transport.Config{
			ServerID:   i,
			Address:    peers[i],
			RPCTimeout: 1000, // 1 second
		}
		httpTrans, err := httpTransport.NewHTTPTransport(transportConfig, discovery)
		if err != nil {
			t.Fatalf("Failed to create transport %d: %v", i, err)
		}

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

	// Start all nodes (this will also start the transports internally)
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

	// Create peer discovery first
	ports, err := helpers.GetFreePorts(3)
	if err != nil {
		t.Fatalf("Failed to get free ports: %v", err)
	}

	peers := make(map[int]string)
	for i := 0; i < 3; i++ {
		peers[i] = fmt.Sprintf("localhost:%d", ports[i])
	}
	discovery := transport.NewStaticPeerDiscovery(peers)

	for i := 0; i < 3; i++ {
		// Create HTTP transport with short timeout for faster failure detection
		transportConfig := &transport.Config{
			ServerID:   i,
			Address:    peers[i],
			RPCTimeout: 100, // 100ms for faster tests
		}
		httpTrans, err := httpTransport.NewHTTPTransport(transportConfig, discovery)
		if err != nil {
			t.Fatalf("Failed to create transport %d: %v", i, err)
		}

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

	// Start all nodes (this will also start the transports internally)
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
	followerTransport := nodes[followerID].GetTransportHandler()
	if err := followerTransport.Stop(); err != nil {
		t.Logf("Warning: error stopping transport: %v", err)
	}

	// Give OS time to release the port
	time.Sleep(100 * time.Millisecond)

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
	if err := followerTransport.Start(); err != nil {
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

	// Create peer discovery first
	ports, err := helpers.GetFreePorts(3)
	if err != nil {
		t.Fatalf("Failed to get free ports: %v", err)
	}

	peers := make(map[int]string)
	for i := 0; i < 3; i++ {
		peers[i] = fmt.Sprintf("localhost:%d", ports[i])
	}
	discovery := transport.NewStaticPeerDiscovery(peers)

	for i := 0; i < 3; i++ {
		// Create HTTP transport
		transportConfig := &transport.Config{
			ServerID:   i,
			Address:    peers[i],
			RPCTimeout: 1000,
		}
		httpTrans, err := httpTransport.NewHTTPTransport(transportConfig, discovery)
		if err != nil {
			t.Fatalf("Failed to create transport %d: %v", i, err)
		}

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

	// Start all nodes (this will also start the transports internally)
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
	// Get a free port for this test
	ports, err := helpers.GetFreePorts(1)
	if err != nil {
		t.Fatalf("Failed to get free port: %v", err)
	}

	config := &transport.Config{
		ServerID:   1,
		Address:    fmt.Sprintf("localhost:%d", ports[0]),
		RPCTimeout: 500,
	}

	discovery := transport.NewStaticPeerDiscovery(map[int]string{1: fmt.Sprintf("localhost:%d", ports[0])})
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
	resp.Body.Close() //nolint:errcheck // test cleanup

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
	// Get a free port for this test
	ports, err := helpers.GetFreePorts(1)
	if err != nil {
		t.Fatalf("Failed to get free port: %v", err)
	}

	config := &transport.Config{
		ServerID:   1,
		Address:    fmt.Sprintf("localhost:%d", ports[0]),
		RPCTimeout: 500,
	}

	discovery := transport.NewStaticPeerDiscovery(map[int]string{1: fmt.Sprintf("localhost:%d", ports[0])})
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
	defer transport.Stop() //nolint:errcheck // test cleanup

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
	defer resp.Body.Close() //nolint:errcheck // test cleanup

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
	// Get a free port for this test
	ports, err := helpers.GetFreePorts(1)
	if err != nil {
		t.Fatalf("Failed to get free port: %v", err)
	}

	config := &transport.Config{
		ServerID:   1,
		Address:    fmt.Sprintf("localhost:%d", ports[0]),
		RPCTimeout: 500,
	}

	discovery := transport.NewStaticPeerDiscovery(map[int]string{1: fmt.Sprintf("localhost:%d", ports[0])})
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
	defer transport.Stop() //nolint:errcheck // test cleanup

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
	defer resp.Body.Close() //nolint:errcheck // test cleanup

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
	// Get a free port for this test
	ports, err := helpers.GetFreePorts(1)
	if err != nil {
		t.Fatalf("Failed to get free port: %v", err)
	}

	config := &transport.Config{
		ServerID:   1,
		Address:    fmt.Sprintf("localhost:%d", ports[0]),
		RPCTimeout: 500,
	}

	discovery := transport.NewStaticPeerDiscovery(map[int]string{1: fmt.Sprintf("localhost:%d", ports[0])})
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
	defer transport.Stop() //nolint:errcheck // test cleanup

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
	defer resp.Body.Close() //nolint:errcheck // test cleanup

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
	// Get a free port for this test
	ports, err := helpers.GetFreePorts(1)
	if err != nil {
		t.Fatalf("Failed to get free port: %v", err)
	}

	config := &transport.Config{
		ServerID:   1,
		Address:    fmt.Sprintf("localhost:%d", ports[0]),
		RPCTimeout: 500,
	}

	discovery := transport.NewStaticPeerDiscovery(map[int]string{1: fmt.Sprintf("localhost:%d", ports[0])})
	transport, err := httpTransport.NewHTTPTransport(config, discovery)
	if err != nil {
		t.Fatalf("failed to create transport: %v", err)
	}
	transport.SetRPCHandler(&MockRPCHandler{})

	if err := transport.Start(); err != nil {
		t.Fatalf("failed to start transport: %v", err)
	}
	defer transport.Stop() //nolint:errcheck // test cleanup

	// Give server time to start
	time.Sleep(100 * time.Millisecond)

	// Test GET request (should fail)
	resp, err := http.Get("http://" + transport.GetAddress() + "/raft/requestvote")
	if err != nil {
		t.Fatalf("failed to send request: %v", err)
	}
	defer resp.Body.Close() //nolint:errcheck // test cleanup

	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("expected status 405, got %d", resp.StatusCode)
	}
}

// TestHTTPTransport_HandleInvalidJSON tests handling invalid JSON requests
func TestHTTPTransport_HandleInvalidJSON(t *testing.T) {
	// Get a free port for this test
	ports, err := helpers.GetFreePorts(1)
	if err != nil {
		t.Fatalf("Failed to get free port: %v", err)
	}

	config := &transport.Config{
		ServerID:   1,
		Address:    fmt.Sprintf("localhost:%d", ports[0]),
		RPCTimeout: 500,
	}

	discovery := transport.NewStaticPeerDiscovery(map[int]string{1: fmt.Sprintf("localhost:%d", ports[0])})
	transport, err := httpTransport.NewHTTPTransport(config, discovery)
	if err != nil {
		t.Fatalf("failed to create transport: %v", err)
	}
	transport.SetRPCHandler(&MockRPCHandler{})

	if err := transport.Start(); err != nil {
		t.Fatalf("failed to start transport: %v", err)
	}
	defer transport.Stop() //nolint:errcheck // test cleanup

	// Give server time to start
	time.Sleep(100 * time.Millisecond)

	// Send invalid JSON
	resp, err := http.Post("http://"+transport.GetAddress()+"/raft/requestvote", "application/json", bytes.NewBufferString("invalid json"))
	if err != nil {
		t.Fatalf("failed to send request: %v", err)
	}
	defer resp.Body.Close() //nolint:errcheck // test cleanup

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", resp.StatusCode)
	}
}

// TestHTTPTransport_HandleRPCError tests handling RPC handler errors
func TestHTTPTransport_HandleRPCError(t *testing.T) {
	// Get a free port for this test
	ports, err := helpers.GetFreePorts(1)
	if err != nil {
		t.Fatalf("Failed to get free port: %v", err)
	}

	config := &transport.Config{
		ServerID:   1,
		Address:    fmt.Sprintf("localhost:%d", ports[0]),
		RPCTimeout: 500,
	}

	discovery := transport.NewStaticPeerDiscovery(map[int]string{1: fmt.Sprintf("localhost:%d", ports[0])})
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
	defer transport.Stop() //nolint:errcheck // test cleanup

	// Give server time to start
	time.Sleep(100 * time.Millisecond)

	// Send request
	args := raft.RequestVoteArgs{Term: 5}
	body, _ := json.Marshal(args)
	resp, err := http.Post("http://"+transport.GetAddress()+"/raft/requestvote", "application/json", bytes.NewBuffer(body))
	if err != nil {
		t.Fatalf("failed to send request: %v", err)
	}
	defer resp.Body.Close() //nolint:errcheck // test cleanup

	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", resp.StatusCode)
	}
}

// TestHTTPTransport_WithStaticDiscovery tests static peer discovery integration
func TestHTTPTransport_WithStaticDiscovery(t *testing.T) {
	// Set up mock servers for testing
	servers := make([]*httptest.Server, 3)
	for i := range servers {
		serverID := i
		servers[i] = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/raft/requestvote" {
				var args raft.RequestVoteArgs
				json.NewDecoder(r.Body).Decode(&args) //nolint:errcheck // test mock handler

				reply := raft.RequestVoteReply{
					Term:        args.Term,
					VoteGranted: true,
				}
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(reply) //nolint:errcheck // test mock response //nolint:errcheck // test mock response
			}
		}))
		defer servers[serverID].Close()
	}

	// Create peer mapping
	peers := make(map[int]string)
	for i, server := range servers {
		peers[i] = server.Listener.Addr().String()
	}

	// Create transport with static discovery
	config := &transport.Config{
		ServerID:   0,
		Address:    peers[0],
		RPCTimeout: 500,
	}

	httpTrans, err := httpTransport.NewHTTPTransportWithStaticPeers(config, peers)
	if err != nil {
		t.Fatalf("failed to create transport: %v", err)
	}

	// Test sending RPC to each peer
	for peerID := 1; peerID < 3; peerID++ {
		args := &raft.RequestVoteArgs{
			Term:        1,
			CandidateID: 0,
		}

		reply, err := httpTrans.SendRequestVote(peerID, args)
		if err != nil {
			t.Errorf("failed to send request to peer %d: %v", peerID, err)
			continue
		}

		if !reply.VoteGranted {
			t.Errorf("expected vote to be granted by peer %d", peerID)
		}
	}
}

// TestHTTPTransport_DynamicDiscoveryUpdate tests dynamic discovery updates
func TestHTTPTransport_DynamicDiscoveryUpdate(t *testing.T) {
	// Create initial server
	server1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reply := raft.RequestVoteReply{Term: 1, VoteGranted: true}
		json.NewEncoder(w).Encode(reply) //nolint:errcheck // test mock response
	}))
	defer server1.Close()

	// Create discovery with initial peer
	discovery := transport.NewStaticPeerDiscovery(map[int]string{
		1: server1.Listener.Addr().String(),
	})

	// Get a free port for the local transport
	localPorts, err := helpers.GetFreePorts(1)
	if err != nil {
		t.Fatalf("Failed to get free port: %v", err)
	}

	// Create transport
	config := &transport.Config{
		ServerID:   0,
		Address:    fmt.Sprintf("localhost:%d", localPorts[0]),
		RPCTimeout: 500,
	}

	httpTrans, err := httpTransport.NewHTTPTransport(config, discovery)
	if err != nil {
		t.Fatalf("failed to create transport: %v", err)
	}

	// Send initial RPC - should work
	args := &raft.RequestVoteArgs{Term: 1}
	_, err = httpTrans.SendRequestVote(1, args)
	if err != nil {
		t.Errorf("failed to send to initial peer: %v", err)
	}

	// Create new server
	server2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reply := raft.RequestVoteReply{Term: 2, VoteGranted: false}
		json.NewEncoder(w).Encode(reply) //nolint:errcheck // test mock response
	}))
	defer server2.Close()

	// Update discovery with new peer configuration
	discovery.UpdatePeers(map[int]string{
		2: server2.Listener.Addr().String(),
		// Note: peer 1 is removed
	})

	// Try to send to old peer - should fail
	_, err = httpTrans.SendRequestVote(1, args)
	if err == nil {
		t.Error("expected error for removed peer")
	}

	// Send to new peer - should work
	reply, err := httpTrans.SendRequestVote(2, args)
	if err != nil {
		t.Errorf("failed to send to new peer: %v", err)
	}
	if reply.Term != 2 {
		t.Errorf("unexpected term from new peer: %d", reply.Term)
	}
}

// TestHTTPTransport_SendRequestVote tests sending RequestVote RPC
func TestHTTPTransport_SendRequestVote(t *testing.T) {
	// Get a free port for the local transport
	localPorts, err := helpers.GetFreePorts(1)
	if err != nil {
		t.Fatalf("Failed to get free port: %v", err)
	}

	// Set up mock server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/raft/requestvote" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}

		var args raft.RequestVoteArgs
		if err := json.NewDecoder(r.Body).Decode(&args); err != nil {
			t.Errorf("failed to decode request: %v", err)
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		// Verify request
		if args.Term != 5 || args.CandidateID != 1 {
			t.Errorf("unexpected args: %+v", args)
		}

		reply := raft.RequestVoteReply{
			Term:        5,
			VoteGranted: true,
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(reply) //nolint:errcheck // test mock response
	}))
	defer server.Close()

	// Create transport
	config := &transport.Config{
		ServerID:   1,
		Address:    fmt.Sprintf("localhost:%d", localPorts[0]),
		RPCTimeout: 500,
	}
	discovery := transport.NewStaticPeerDiscovery(map[int]string{
		2: server.Listener.Addr().String(),
	})
	httpTrans, err := httpTransport.NewHTTPTransport(config, discovery)
	if err != nil {
		t.Fatalf("failed to create transport: %v", err)
	}

	// Send request
	args := &raft.RequestVoteArgs{
		Term:         5,
		CandidateID:  1,
		LastLogIndex: 10,
		LastLogTerm:  4,
	}

	reply, err := httpTrans.SendRequestVote(2, args)
	if err != nil {
		t.Fatalf("failed to send request vote: %v", err)
	}

	if reply.Term != 5 || !reply.VoteGranted {
		t.Errorf("unexpected reply: %+v", reply)
	}
}

// TestHTTPTransport_SendAppendEntries tests sending AppendEntries RPC
func TestHTTPTransport_SendAppendEntries(t *testing.T) {
	// Get a free port for the local transport
	localPorts, err := helpers.GetFreePorts(1)
	if err != nil {
		t.Fatalf("Failed to get free port: %v", err)
	}

	// Set up mock server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/raft/appendentries" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}

		var args raft.AppendEntriesArgs
		if err := json.NewDecoder(r.Body).Decode(&args); err != nil {
			t.Errorf("failed to decode request: %v", err)
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		// Verify request
		if args.Term != 5 || args.LeaderID != 1 || len(args.Entries) != 2 {
			t.Errorf("unexpected args: %+v", args)
		}

		reply := raft.AppendEntriesReply{
			Term:    5,
			Success: true,
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(reply) //nolint:errcheck // test mock response
	}))
	defer server.Close()

	// Create transport
	config := &transport.Config{
		ServerID:   1,
		Address:    fmt.Sprintf("localhost:%d", localPorts[0]),
		RPCTimeout: 500,
	}
	discovery := transport.NewStaticPeerDiscovery(map[int]string{
		2: server.Listener.Addr().String(),
	})
	httpTrans, err := httpTransport.NewHTTPTransport(config, discovery)
	if err != nil {
		t.Fatalf("failed to create transport: %v", err)
	}

	// Send request
	args := &raft.AppendEntriesArgs{
		Term:         5,
		LeaderID:     1,
		PrevLogIndex: 10,
		PrevLogTerm:  4,
		Entries: []raft.LogEntry{
			{Index: 11, Term: 5, Command: "cmd1"},
			{Index: 12, Term: 5, Command: "cmd2"},
		},
		LeaderCommit: 10,
	}

	reply, err := httpTrans.SendAppendEntries(2, args)
	if err != nil {
		t.Fatalf("failed to send append entries: %v", err)
	}

	if reply.Term != 5 || !reply.Success {
		t.Errorf("unexpected reply: %+v", reply)
	}
}

// TestHTTPTransport_SendInstallSnapshot tests sending InstallSnapshot RPC
func TestHTTPTransport_SendInstallSnapshot(t *testing.T) {
	// Get a free port for the local transport
	localPorts, err := helpers.GetFreePorts(1)
	if err != nil {
		t.Fatalf("Failed to get free port: %v", err)
	}

	// Set up mock server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/raft/installsnapshot" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}

		var args raft.InstallSnapshotArgs
		if err := json.NewDecoder(r.Body).Decode(&args); err != nil {
			t.Errorf("failed to decode request: %v", err)
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		// Verify request
		if args.Term != 5 || args.LeaderID != 1 || len(args.Data) != 4 {
			t.Errorf("unexpected args: %+v", args)
		}

		reply := raft.InstallSnapshotReply{
			Term: 5,
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(reply) //nolint:errcheck // test mock response
	}))
	defer server.Close()

	// Create transport
	config := &transport.Config{
		ServerID:   1,
		Address:    fmt.Sprintf("localhost:%d", localPorts[0]),
		RPCTimeout: 500,
	}
	discovery := transport.NewStaticPeerDiscovery(map[int]string{
		2: server.Listener.Addr().String(),
	})
	httpTrans, err := httpTransport.NewHTTPTransport(config, discovery)
	if err != nil {
		t.Fatalf("failed to create transport: %v", err)
	}

	// Send request
	args := &raft.InstallSnapshotArgs{
		Term:              5,
		LeaderID:          1,
		LastIncludedIndex: 10,
		LastIncludedTerm:  4,
		Data:              []byte("test"),
	}

	reply, err := httpTrans.SendInstallSnapshot(2, args)
	if err != nil {
		t.Fatalf("failed to send install snapshot: %v", err)
	}

	if reply.Term != 5 {
		t.Errorf("unexpected reply: %+v", reply)
	}
}

// TestHTTPTransport_SendRPCError tests RPC error handling
func TestHTTPTransport_SendRPCError(t *testing.T) {
	tests := []struct {
		name           string
		serverResponse func(w http.ResponseWriter, r *http.Request)
		expectedError  string
	}{
		{
			name: "server returns 500",
			serverResponse: func(w http.ResponseWriter, r *http.Request) {
				http.Error(w, "internal error", http.StatusInternalServerError)
			},
			expectedError: "RPC failed with status 500",
		},
		{
			name: "server returns invalid JSON",
			serverResponse: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte("invalid json")) //nolint:errcheck // test error response
			},
			expectedError: "failed to decode response",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Get a free port for this sub-test
			localPorts, err := helpers.GetFreePorts(1)
			if err != nil {
				t.Fatalf("Failed to get free port: %v", err)
			}

			// Set up mock server
			server := httptest.NewServer(http.HandlerFunc(tt.serverResponse))
			defer server.Close()

			// Create transport
			config := &transport.Config{
				ServerID:   1,
				Address:    fmt.Sprintf("localhost:%d", localPorts[0]),
				RPCTimeout: 500,
			}
			discovery := transport.NewStaticPeerDiscovery(map[int]string{
				2: server.Listener.Addr().String(),
			})
			httpTrans, err := httpTransport.NewHTTPTransport(config, discovery)
			if err != nil {
				t.Fatalf("failed to create transport: %v", err)
			}

			// Send request
			args := &raft.RequestVoteArgs{
				Term:        5,
				CandidateID: 1,
			}

			_, err = httpTrans.SendRequestVote(2, args)
			if err == nil {
				t.Error("expected error but got none")
			}

			transportErr, ok := err.(*transport.TransportError)
			if !ok {
				t.Errorf("expected TransportError, got %T", err)
			}

			if transportErr.ServerID != 2 {
				t.Errorf("expected ServerID 2, got %d", transportErr.ServerID)
			}
		})
	}
}

// TestHTTPTransport_NetworkError tests network error handling
func TestHTTPTransport_NetworkError(t *testing.T) {
	// Get a free port for the local transport
	localPorts, err := helpers.GetFreePorts(1)
	if err != nil {
		t.Fatalf("Failed to get free port: %v", err)
	}

	config := &transport.Config{
		ServerID:   1,
		Address:    fmt.Sprintf("localhost:%d", localPorts[0]),
		RPCTimeout: 100, // Short timeout
	}

	discovery := transport.NewStaticPeerDiscovery(map[int]string{
		2: "localhost:19999", // Non-existent port
	})
	httpTrans, err := httpTransport.NewHTTPTransport(config, discovery)
	if err != nil {
		t.Fatalf("failed to create transport: %v", err)
	}

	args := &raft.RequestVoteArgs{Term: 5}
	_, err = httpTrans.SendRequestVote(2, args)

	if err == nil {
		t.Error("expected error for network failure")
	}

	transportErr, ok := err.(*transport.TransportError)
	if !ok {
		t.Errorf("expected TransportError, got %T", err)
	}

	if transportErr.ServerID != 2 {
		t.Errorf("expected ServerID 2, got %d", transportErr.ServerID)
	}
}

// TestHTTPTransport_Timeout tests timeout handling
func TestHTTPTransport_Timeout(t *testing.T) {
	// Get a free port for the local transport
	localPorts, err := helpers.GetFreePorts(1)
	if err != nil {
		t.Fatalf("Failed to get free port: %v", err)
	}

	// Create a slow server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond) // Sleep longer than timeout
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	config := &transport.Config{
		ServerID:   1,
		Address:    fmt.Sprintf("localhost:%d", localPorts[0]),
		RPCTimeout: 50, // Very short timeout
	}

	discovery := transport.NewStaticPeerDiscovery(map[int]string{
		2: server.Listener.Addr().String(),
	})
	httpTrans, err := httpTransport.NewHTTPTransport(config, discovery)
	if err != nil {
		t.Fatalf("failed to create transport: %v", err)
	}

	args := &raft.RequestVoteArgs{Term: 5}
	_, err = httpTrans.SendRequestVote(2, args)

	if err == nil {
		t.Error("expected timeout error")
	}

	_, ok := err.(*transport.TransportError)
	if !ok {
		t.Errorf("expected TransportError, got %T", err)
	}

	// Check that it's a timeout error
	errStr := err.Error()
	if !strings.Contains(errStr, "context deadline exceeded") &&
		!strings.Contains(errStr, "Client.Timeout exceeded") {
		t.Errorf("expected timeout error, got %v", err)
	}
}

// TestHTTPTransport_ContextCancellation tests that context cancellation works properly
func TestHTTPTransport_ContextCancellation(t *testing.T) {
	// Get a free port for the local transport
	localPorts, err := helpers.GetFreePorts(1)
	if err != nil {
		t.Fatalf("Failed to get free port: %v", err)
	}

	// Create a slow mock server that takes longer than the timeout
	slowServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Sleep for 200ms - longer than our 100ms timeout
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer slowServer.Close()

	config := &transport.Config{
		ServerID:   0,
		Address:    fmt.Sprintf("localhost:%d", localPorts[0]),
		RPCTimeout: 100, // 100ms timeout
	}

	// Parse the test server address
	addr := slowServer.Listener.Addr().String()
	discovery := transport.NewStaticPeerDiscovery(map[int]string{
		1: addr,
	})

	httpTrans, err := httpTransport.NewHTTPTransport(config, discovery)
	if err != nil {
		t.Fatalf("failed to create transport: %v", err)
	}

	// Test that RPC times out properly
	args := &raft.RequestVoteArgs{Term: 1}
	_, err = httpTrans.SendRequestVote(1, args)

	if err == nil {
		t.Error("expected timeout error when server is slow")
	}

	// Verify it's a timeout error
	if !strings.Contains(err.Error(), "context deadline exceeded") {
		t.Errorf("expected context deadline exceeded error, got: %v", err)
	}
}
