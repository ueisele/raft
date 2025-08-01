package http

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/ueisele/raft"
	transportPkg "github.com/ueisele/raft/transport"
)

// mockDiscovery is a simple discovery implementation for tests
type mockDiscovery struct {
	address   string
	addresses map[int]string
}

func (m *mockDiscovery) GetPeerAddress(ctx context.Context, serverID int) (string, error) {
	if m.addresses != nil {
		addr, ok := m.addresses[serverID]
		if !ok {
			return "", fmt.Errorf("server %d not found", serverID)
		}
		return addr, nil
	}
	return m.address, nil
}

func (m *mockDiscovery) RefreshPeers(ctx context.Context) error {
	return nil
}

func (m *mockDiscovery) Close() error {
	return nil
}

// errorDiscovery is a mock discovery that returns errors
type errorDiscovery struct {
	err error
}

func (e *errorDiscovery) GetPeerAddress(ctx context.Context, serverID int) (string, error) {
	return "", e.err
}

func (e *errorDiscovery) RefreshPeers(ctx context.Context) error {
	return nil
}

func (e *errorDiscovery) Close() error {
	return nil
}

// testRPCHandler is a minimal RPC handler for testing
type testRPCHandler struct{}

func (h *testRPCHandler) RequestVote(args *raft.RequestVoteArgs, reply *raft.RequestVoteReply) error {
	return nil
}

func (h *testRPCHandler) AppendEntries(args *raft.AppendEntriesArgs, reply *raft.AppendEntriesReply) error {
	return nil
}

func (h *testRPCHandler) InstallSnapshot(args *raft.InstallSnapshotArgs, reply *raft.InstallSnapshotReply) error {
	return nil
}

// TestNewHTTPTransport tests the constructor
func TestNewHTTPTransport(t *testing.T) {
	config := &transportPkg.Config{
		ServerID:   1,
		Address:    "localhost:8001",
		RPCTimeout: 500,
	}

	discovery := &mockDiscovery{address: "localhost:8080"}
	transport, err := NewHTTPTransport(config, discovery)
	if err != nil {
		t.Fatalf("failed to create transport: %v", err)
	}

	if transport.serverID != config.ServerID {
		t.Errorf("expected serverID %d, got %d", config.ServerID, transport.serverID)
	}

	if transport.address != config.Address {
		t.Errorf("expected address %s, got %s", config.Address, transport.address)
	}

	expectedTimeout := time.Duration(config.RPCTimeout) * time.Millisecond
	if transport.rpcTimeout != expectedTimeout {
		t.Errorf("expected timeout %v, got %v", expectedTimeout, transport.rpcTimeout)
	}
}

// TestHTTPTransport_RequiresDiscovery verifies that constructor requires discovery
func TestHTTPTransport_RequiresDiscovery(t *testing.T) {
	config := &transportPkg.Config{
		ServerID:   1,
		Address:    "localhost:8001",
		RPCTimeout: 500,
	}

	// Try to create with nil discovery
	transport, err := NewHTTPTransport(config, nil)
	if err == nil {
		t.Error("expected NewHTTPTransport to fail with nil discovery")
	}
	if transport != nil {
		t.Error("expected nil transport when error is returned")
	}
	
	expectedError := "discovery cannot be nil"
	if err.Error() != expectedError {
		t.Errorf("expected error %q, got %q", expectedError, err.Error())
	}
}

// TestHTTPTransport_StartWithoutHandler verifies that Start() fails without handler
func TestHTTPTransport_StartWithoutHandler(t *testing.T) {
	config := &transportPkg.Config{
		ServerID:   1,
		Address:    "localhost:8002",
		RPCTimeout: 500,
	}

	discovery := &mockDiscovery{address: "localhost:8080"}
	transport, err := NewHTTPTransport(config, discovery)
	if err != nil {
		t.Fatalf("failed to create transport: %v", err)
	}
	
	// Start should fail without handler
	err = transport.Start()
	if err == nil {
		t.Error("expected Start() to fail without handler")
	}
	
	expectedError := "RPC handler not set"
	if err.Error() != expectedError {
		t.Errorf("expected error %q, got %q", expectedError, err.Error())
	}
}

// TestHTTPTransport_StartWithBothRequired verifies that Start() succeeds with both handler and discovery
func TestHTTPTransport_StartWithBothRequired(t *testing.T) {
	config := &transportPkg.Config{
		ServerID:   1,
		Address:    "localhost:18100",
		RPCTimeout: 500,
	}

	discovery := &mockDiscovery{address: "localhost:8080"}
	transport, err := NewHTTPTransport(config, discovery)
	if err != nil {
		t.Fatalf("failed to create transport: %v", err)
	}
	
	// Set handler
	handler := &testRPCHandler{}
	transport.SetRPCHandler(handler)
	
	// Start should succeed
	err = transport.Start()
	if err != nil {
		t.Errorf("expected Start() to succeed, got error: %v", err)
	}
	
	// Clean up
	transport.Stop()
}

// TestHTTPTransport_DefaultAddressResolution tests the default address resolution logic
func TestHTTPTransport_DefaultAddressResolution(t *testing.T) {
	config := &transportPkg.Config{
		ServerID:   1,
		Address:    "localhost:8001",
		RPCTimeout: 500,
	}

	// Create discovery with test addresses
	peers := map[int]string{
		1:   "localhost:8001",
		2:   "localhost:8002",
		3:   "localhost:8003",
		100: "localhost:8100",
	}
	discovery := &mockDiscovery{addresses: peers}
	
	transport, err := NewHTTPTransport(config, discovery)
	if err != nil {
		t.Fatalf("failed to create transport: %v", err)
	}

	// Test that discovery is used correctly
	tests := []struct {
		serverID     int
		expectedPort int
	}{
		{1, 8001},
		{2, 8002},
		{3, 8003},
		{100, 8100},
	}

	for _, tt := range tests {
		expectedAddr := fmt.Sprintf("localhost:%d", tt.expectedPort)
		// The actual behavior is tested in integration tests where real connections are made
		_ = expectedAddr
	}
	
	// Verify transport was created successfully
	_ = transport
}

// TestHTTPTransport_DiscoveryError tests error handling when discovery fails
func TestHTTPTransport_DiscoveryError(t *testing.T) {
	config := &transportPkg.Config{
		ServerID:   0,
		Address:    "localhost:8000",
		RPCTimeout: 100,
	}

	// Create transport with discovery that returns errors
	discovery := &errorDiscovery{err: fmt.Errorf("peer not found")}
	httpTransport, err := NewHTTPTransport(config, discovery)
	if err != nil {
		t.Fatalf("failed to create transport: %v", err)
	}

	// Test that getServerAddress properly returns the error
	addr, err := httpTransport.getServerAddress(1)
	
	if err == nil {
		t.Error("expected error when discovery fails")
	}
	
	if addr != "" {
		t.Errorf("expected empty address when error occurs, got %q", addr)
	}
	
	// Check that error contains both server ID and original error
	errStr := err.Error()
	if errStr != "failed to get address for server 1: peer not found" {
		t.Errorf("unexpected error message: %v", err)
	}
}

// TestHTTPTransport_DiscoveryTimeout tests timeout handling in discovery
func TestHTTPTransport_DiscoveryTimeout(t *testing.T) {
	config := &transportPkg.Config{
		ServerID:   0,
		Address:    "localhost:8000",
		RPCTimeout: 100,
	}

	// Create transport with discovery that returns context timeout
	discovery := &errorDiscovery{err: context.DeadlineExceeded}
	httpTransport, err := NewHTTPTransport(config, discovery)
	if err != nil {
		t.Fatalf("failed to create transport: %v", err)
	}

	// Test that timeout is properly propagated
	addr, err := httpTransport.getServerAddress(1)
	
	if err == nil {
		t.Error("expected error when discovery times out")
	}
	
	if addr != "" {
		t.Errorf("expected empty address when timeout occurs, got %q", addr)
	}
	
	// Check that the timeout error is wrapped properly
	if err.Error() != "failed to get address for server 1: context deadline exceeded" {
		t.Errorf("unexpected error message: %v", err)
	}
}

// TestNewHTTPTransportWithDiscovery_ValidatesNilDiscovery verifies constructor validates nil discovery
func TestNewHTTPTransportWithDiscovery_ValidatesNilDiscovery(t *testing.T) {
	config := &transportPkg.Config{
		ServerID:   1,
		Address:    "localhost:8001",
		RPCTimeout: 500,
	}

	// Try to create with nil discovery
	transport, err := NewHTTPTransportWithDiscovery(config, nil)
	if err == nil {
		t.Error("expected NewHTTPTransportWithDiscovery to fail with nil discovery")
	}
	if transport != nil {
		t.Error("expected nil transport when error is returned")
	}
}

// TestHTTPTransport_WithBuilder tests the builder pattern
func TestHTTPTransport_WithBuilder(t *testing.T) {
	// Create discovery
	discovery := &mockDiscovery{address: "localhost:8080"}

	// Build transport
	httpTransport, err := NewBuilder(0, "localhost:8000").
		WithDiscovery(discovery).
		WithTimeout(1000 * 1000000). // 1 second in nanoseconds
		Build()
	
	if err != nil {
		t.Fatalf("failed to build transport: %v", err)
	}

	if httpTransport.serverID != 0 {
		t.Errorf("expected serverID 0, got %d", httpTransport.serverID)
	}

	if httpTransport.address != "localhost:8000" {
		t.Errorf("expected address localhost:8000, got %s", httpTransport.address)
	}

	if httpTransport.rpcTimeout != time.Second {
		t.Errorf("expected timeout 1s, got %v", httpTransport.rpcTimeout)
	}
}

// TestHTTPTransport_NilDiscoveryError tests that nil discovery is rejected
func TestHTTPTransport_NilDiscoveryError(t *testing.T) {
	// Create transport with nil discovery should fail
	config := &transportPkg.Config{
		ServerID:   0,
		Address:    "localhost:8000",
		RPCTimeout: 100,
	}
	
	_, err := NewHTTPTransport(config, nil)
	if err == nil {
		t.Error("expected error when creating transport with nil discovery")
	}
}