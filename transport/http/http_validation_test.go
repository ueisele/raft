package http

import (
	"testing"

	"github.com/ueisele/raft"
	transportPkg "github.com/ueisele/raft/transport"
)

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

// TestHTTPTransport_StartWithoutDiscovery verifies that Start() fails without discovery
func TestHTTPTransport_StartWithoutDiscovery(t *testing.T) {
	config := &transportPkg.Config{
		ServerID:   1,
		Address:    "localhost:8001",
		RPCTimeout: 500,
	}

	transport := NewHTTPTransport(config)
	
	// Set handler but not discovery
	handler := &testRPCHandler{}
	transport.SetRPCHandler(handler)
	
	// Start should fail without discovery
	err := transport.Start()
	if err == nil {
		t.Error("expected Start() to fail without discovery")
	}
	
	expectedError := "peer discovery not set"
	if err.Error() != expectedError && !contains(err.Error(), expectedError) {
		t.Errorf("expected error containing %q, got %q", expectedError, err.Error())
	}
}

// TestHTTPTransport_StartWithoutHandler verifies that Start() fails without handler
func TestHTTPTransport_StartWithoutHandler(t *testing.T) {
	config := &transportPkg.Config{
		ServerID:   1,
		Address:    "localhost:8002",
		RPCTimeout: 500,
	}

	transport := NewHTTPTransport(config)
	
	// Set discovery but not handler
	discovery := &mockDiscovery{address: "localhost:8080"}
	transport.SetDiscovery(discovery)
	
	// Start should fail without handler
	err := transport.Start()
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

	transport := NewHTTPTransport(config)
	
	// Set both handler and discovery
	handler := &testRPCHandler{}
	transport.SetRPCHandler(handler)
	
	discovery := &mockDiscovery{address: "localhost:8080"}
	transport.SetDiscovery(discovery)
	
	// Start should succeed
	err := transport.Start()
	if err != nil {
		t.Errorf("expected Start() to succeed, got error: %v", err)
	}
	
	// Clean up
	transport.Stop()
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

// TestMigrationPath shows the migration path for existing code
func TestMigrationPath(t *testing.T) {
	config := &transportPkg.Config{
		ServerID:   1,
		Address:    "localhost:18101",
		RPCTimeout: 500,
	}

	t.Run("old_way_still_works", func(t *testing.T) {
		// Old way - still works but requires SetDiscovery before Start
		transport := NewHTTPTransport(config)
		transport.SetRPCHandler(&testRPCHandler{})
		
		// Forgot to set discovery? Start will catch it
		err := transport.Start()
		if err == nil {
			t.Error("expected Start to fail without discovery")
		}
		
		// Set discovery and try again
		transport.SetDiscovery(&mockDiscovery{address: "localhost:8080"})
		err = transport.Start()
		if err != nil {
			t.Errorf("expected Start to succeed after setting discovery: %v", err)
		}
		transport.Stop()
	})

	t.Run("new_way_recommended", func(t *testing.T) {
		// New way - discovery required at construction
		discovery := &mockDiscovery{address: "localhost:8080"}
		transport, err := NewHTTPTransportWithDiscovery(config, discovery)
		if err != nil {
			t.Fatalf("failed to create transport: %v", err)
		}
		
		transport.SetRPCHandler(&testRPCHandler{})
		
		// Start should work immediately
		err = transport.Start()
		if err != nil {
			t.Errorf("expected Start to succeed: %v", err)
		}
		transport.Stop()
	})
}

// Helper function
func contains(s, substr string) bool {
	return len(s) >= len(substr) && s[:len(substr)] == substr || len(s) > len(substr) && contains(s[1:], substr)
}