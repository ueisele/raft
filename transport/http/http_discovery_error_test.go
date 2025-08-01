package http

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ueisele/raft"
	"github.com/ueisele/raft/transport"
)

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

func TestHTTPTransport_DiscoveryError(t *testing.T) {
	config := &transport.Config{
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

	// Test RequestVote with discovery error
	t.Run("RequestVote", func(t *testing.T) {
		args := &raft.RequestVoteArgs{Term: 1}
		_, err := httpTransport.SendRequestVote(1, args)
		
		if err == nil {
			t.Error("expected error when discovery fails")
		}
		
		// Check that error contains both server ID and original error
		errStr := err.Error()
		if !strings.Contains(errStr, "server 1") {
			t.Errorf("error should mention server ID: %v", err)
		}
		if !strings.Contains(errStr, "peer not found") {
			t.Errorf("error should contain original discovery error: %v", err)
		}
		
		// Verify it's a TransportError
		if _, ok := err.(*transport.TransportError); !ok {
			t.Errorf("expected TransportError, got %T", err)
		}
	})

	// Test AppendEntries with discovery error
	t.Run("AppendEntries", func(t *testing.T) {
		args := &raft.AppendEntriesArgs{Term: 1, LeaderID: 0}
		_, err := httpTransport.SendAppendEntries(2, args)
		
		if err == nil {
			t.Error("expected error when discovery fails")
		}
		
		// Verify it's a TransportError with correct server ID
		transportErr, ok := err.(*transport.TransportError)
		if !ok {
			t.Fatalf("expected TransportError, got %T", err)
		}
		if transportErr.ServerID != 2 {
			t.Errorf("expected ServerID 2, got %d", transportErr.ServerID)
		}
	})

	// Test InstallSnapshot with discovery error
	t.Run("InstallSnapshot", func(t *testing.T) {
		args := &raft.InstallSnapshotArgs{Term: 1, LeaderID: 0}
		_, err := httpTransport.SendInstallSnapshot(3, args)
		
		if err == nil {
			t.Error("expected error when discovery fails")
		}
		
		// Verify error details
		if !strings.Contains(err.Error(), "failed to get address for server 3") {
			t.Errorf("error should mention failed address lookup: %v", err)
		}
	})
}

func TestHTTPTransport_DiscoveryTimeout(t *testing.T) {
	config := &transport.Config{
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
	args := &raft.RequestVoteArgs{Term: 1}
	_, err = httpTransport.SendRequestVote(1, args)
	
	if err == nil {
		t.Error("expected error when discovery times out")
	}
	
	// Check that the timeout error is wrapped properly
	if !strings.Contains(err.Error(), "context deadline exceeded") {
		t.Errorf("error should contain timeout information: %v", err)
	}
}

// TestHTTPTransport_ContextCancellation tests that context cancellation works properly
func TestHTTPTransport_ContextCancellation(t *testing.T) {
	// Create a slow mock server that takes longer than the timeout
	slowServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Sleep for 200ms - longer than our 100ms timeout
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer slowServer.Close()

	config := &transport.Config{
		ServerID:   0,
		Address:    "localhost:8000",
		RPCTimeout: 100, // 100ms timeout
	}

	// Parse the test server address
	addr := slowServer.Listener.Addr().String()
	discovery := &mockDiscovery{address: addr}
	
	httpTransport, err := NewHTTPTransport(config, discovery)
	if err != nil {
		t.Fatalf("failed to create transport: %v", err)
	}

	// Test that RPC times out properly
	args := &raft.RequestVoteArgs{Term: 1}
	_, err = httpTransport.SendRequestVote(1, args)
	
	if err == nil {
		t.Error("expected timeout error when server is slow")
	}
	
	// Verify it's a timeout error
	if !strings.Contains(err.Error(), "context deadline exceeded") {
		t.Errorf("expected context deadline exceeded error, got: %v", err)
	}
}

