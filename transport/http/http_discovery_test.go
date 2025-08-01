package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ueisele/raft"
	"github.com/ueisele/raft/transport"
)

func TestHTTPTransport_WithStaticDiscovery(t *testing.T) {
	// Set up mock servers for testing
	servers := make([]*httptest.Server, 3)
	for i := range servers {
		serverID := i
		servers[i] = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/raft/requestvote" {
				var args raft.RequestVoteArgs
				json.NewDecoder(r.Body).Decode(&args)
				
				reply := raft.RequestVoteReply{
					Term:        args.Term,
					VoteGranted: true,
				}
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(reply)
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
	
	httpTransport, err := NewHTTPTransportWithStaticPeers(config, peers)
	if err != nil {
		t.Fatalf("failed to create transport: %v", err)
	}

	// Test sending RPC to each peer
	for peerID := 1; peerID < 3; peerID++ {
		args := &raft.RequestVoteArgs{
			Term:        1,
			CandidateID: 0,
		}
		
		reply, err := httpTransport.SendRequestVote(peerID, args)
		if err != nil {
			t.Errorf("failed to send request to peer %d: %v", peerID, err)
			continue
		}
		
		if !reply.VoteGranted {
			t.Errorf("expected vote to be granted by peer %d", peerID)
		}
	}
}

func TestHTTPTransport_WithBuilder(t *testing.T) {
	// Set up test server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reply := raft.AppendEntriesReply{
			Term:    1,
			Success: true,
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(reply)
	}))
	defer server.Close()

	// Create discovery
	peers := map[int]string{
		1: server.Listener.Addr().String(),
	}
	discovery := transport.NewStaticPeerDiscovery(peers)

	// Build transport
	httpTransport, err := NewBuilder(0, "localhost:8000").
		WithDiscovery(discovery).
		WithTimeout(1000 * 1000000). // 1 second in nanoseconds
		Build()
	
	if err != nil {
		t.Fatalf("failed to build transport: %v", err)
	}

	// Test RPC
	args := &raft.AppendEntriesArgs{
		Term:     1,
		LeaderID: 0,
	}
	
	reply, err := httpTransport.SendAppendEntries(1, args)
	if err != nil {
		t.Fatalf("failed to send append entries: %v", err)
	}
	
	if !reply.Success {
		t.Error("expected append entries to succeed")
	}
}

func TestHTTPTransport_NilDiscoveryError(t *testing.T) {
	// Create transport with nil discovery should fail
	config := &transport.Config{
		ServerID:   0,
		Address:    "localhost:8000",
		RPCTimeout: 100,
	}
	
	_, err := NewHTTPTransport(config, nil)
	if err == nil {
		t.Error("expected error when creating transport with nil discovery")
	}
}

func TestHTTPTransport_DynamicDiscoveryUpdate(t *testing.T) {
	// Create initial server
	server1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reply := raft.RequestVoteReply{Term: 1, VoteGranted: true}
		json.NewEncoder(w).Encode(reply)
	}))
	defer server1.Close()

	// Create discovery with initial peer
	discovery := transport.NewStaticPeerDiscovery(map[int]string{
		1: server1.Listener.Addr().String(),
	})

	// Create transport
	config := &transport.Config{
		ServerID:   0,
		Address:    "localhost:8000",
		RPCTimeout: 500,
	}
	
	httpTransport, err := NewHTTPTransport(config, discovery)
	if err != nil {
		t.Fatalf("failed to create transport: %v", err)
	}

	// Send initial RPC - should work
	args := &raft.RequestVoteArgs{Term: 1}
	_, err = httpTransport.SendRequestVote(1, args)
	if err != nil {
		t.Errorf("failed to send to initial peer: %v", err)
	}

	// Create new server
	server2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reply := raft.RequestVoteReply{Term: 2, VoteGranted: false}
		json.NewEncoder(w).Encode(reply)
	}))
	defer server2.Close()

	// Update discovery with new peer configuration
	discovery.UpdatePeers(map[int]string{
		2: server2.Listener.Addr().String(),
		// Note: peer 1 is removed
	})

	// Try to send to old peer - should fail
	_, err = httpTransport.SendRequestVote(1, args)
	if err == nil {
		t.Error("expected error for removed peer")
	}

	// Send to new peer - should work
	reply, err := httpTransport.SendRequestVote(2, args)
	if err != nil {
		t.Errorf("failed to send to new peer: %v", err)
	}
	if reply.Term != 2 {
		t.Errorf("unexpected term from new peer: %d", reply.Term)
	}
}

// MockDiscovery is a test implementation of PeerDiscovery
type MockDiscovery struct {
	peers      map[int]string
	getCallCount int
	refreshCallCount int
}

func (m *MockDiscovery) GetPeerAddress(ctx context.Context, serverID int) (string, error) {
	m.getCallCount++
	addr, ok := m.peers[serverID]
	if !ok {
		return "", context.DeadlineExceeded // Simulate timeout
	}
	return addr, nil
}

func (m *MockDiscovery) RefreshPeers(ctx context.Context) error {
	m.refreshCallCount++
	return nil
}

func (m *MockDiscovery) Close() error {
	return nil
}

func TestHTTPTransport_CustomDiscovery(t *testing.T) {
	// Set up test server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reply := raft.InstallSnapshotReply{Term: 1}
		json.NewEncoder(w).Encode(reply)
	}))
	defer server.Close()

	// Create custom discovery
	discovery := &MockDiscovery{
		peers: map[int]string{
			1: server.Listener.Addr().String(),
		},
	}

	// Create transport
	config := &transport.Config{
		ServerID:   0,
		Address:    "localhost:8000",
		RPCTimeout: 500,
	}
	
	httpTransport, err := NewHTTPTransport(config, discovery)
	if err != nil {
		t.Fatalf("failed to create transport: %v", err)
	}

	// Send RPC
	args := &raft.InstallSnapshotArgs{
		Term:     1,
		LeaderID: 0,
		Data:     []byte("test"),
	}
	
	_, err = httpTransport.SendInstallSnapshot(1, args)
	if err != nil {
		t.Errorf("failed to send install snapshot: %v", err)
	}

	// Verify discovery was called
	if discovery.getCallCount != 1 {
		t.Errorf("expected 1 discovery call, got %d", discovery.getCallCount)
	}

	// Test with non-existent peer
	_, err = httpTransport.SendInstallSnapshot(99, args)
	if err == nil {
		t.Error("expected error for non-existent peer")
	}
}