package transport

import (
	"context"
	"testing"
	"time"
)

func TestStaticPeerDiscovery(t *testing.T) {
	peers := map[int]string{
		1: "node1.example.com:8001",
		2: "node2.example.com:8002",
		3: "node3.example.com:8003",
	}

	discovery := NewStaticPeerDiscovery(peers)

	tests := []struct {
		name     string
		serverID int
		wantAddr string
		wantErr  bool
	}{
		{
			name:     "existing peer 1",
			serverID: 1,
			wantAddr: "node1.example.com:8001",
			wantErr:  false,
		},
		{
			name:     "existing peer 2",
			serverID: 2,
			wantAddr: "node2.example.com:8002",
			wantErr:  false,
		},
		{
			name:     "existing peer 3",
			serverID: 3,
			wantAddr: "node3.example.com:8003",
			wantErr:  false,
		},
		{
			name:     "non-existent peer",
			serverID: 99,
			wantAddr: "",
			wantErr:  true,
		},
	}

	ctx := context.Background()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			addr, err := discovery.GetPeerAddress(ctx, tt.serverID)
			if (err != nil) != tt.wantErr {
				t.Errorf("GetPeerAddress() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if addr != tt.wantAddr {
				t.Errorf("GetPeerAddress() = %v, want %v", addr, tt.wantAddr)
			}
		})
	}
}

func TestStaticPeerDiscovery_UpdatePeers(t *testing.T) {
	// Start with initial peers
	initialPeers := map[int]string{
		1: "node1.example.com:8001",
		2: "node2.example.com:8002",
	}
	discovery := NewStaticPeerDiscovery(initialPeers)

	ctx := context.Background()

	// Verify initial state
	addr, err := discovery.GetPeerAddress(ctx, 1)
	if err != nil || addr != "node1.example.com:8001" {
		t.Errorf("Initial peer 1: got %v, %v", addr, err)
	}

	// Update peers
	newPeers := map[int]string{
		2: "node2-new.example.com:8002", // Changed address
		3: "node3.example.com:8003",     // New peer
		// Note: peer 1 is removed
	}
	discovery.UpdatePeers(newPeers)

	// Verify peer 1 is gone
	_, err = discovery.GetPeerAddress(ctx, 1)
	if err == nil {
		t.Error("Expected error for removed peer 1")
	}

	// Verify peer 2 has new address
	addr, err = discovery.GetPeerAddress(ctx, 2)
	if err != nil || addr != "node2-new.example.com:8002" {
		t.Errorf("Updated peer 2: got %v, %v", addr, err)
	}

	// Verify peer 3 is added
	addr, err = discovery.GetPeerAddress(ctx, 3)
	if err != nil || addr != "node3.example.com:8003" {
		t.Errorf("New peer 3: got %v, %v", addr, err)
	}
}

func TestStaticPeerDiscovery_EmptyAddress(t *testing.T) {
	peers := map[int]string{
		1: "", // Empty address
	}
	discovery := NewStaticPeerDiscovery(peers)

	ctx := context.Background()
	_, err := discovery.GetPeerAddress(ctx, 1)
	if err == nil {
		t.Error("Expected error for empty address")
	}
}

func TestStaticPeerDiscovery_RefreshPeers(t *testing.T) {
	discovery := NewStaticPeerDiscovery(map[int]string{})

	ctx := context.Background()
	err := discovery.RefreshPeers(ctx)
	if err != nil {
		t.Errorf("RefreshPeers() should not return error for static discovery: %v", err)
	}
}

func TestStaticPeerDiscovery_Close(t *testing.T) {
	discovery := NewStaticPeerDiscovery(map[int]string{})

	err := discovery.Close()
	if err != nil {
		t.Errorf("Close() should not return error for static discovery: %v", err)
	}
}

func TestStaticPeerDiscovery_GetPeers(t *testing.T) {
	peers := map[int]string{
		1: "node1:8001",
		2: "node2:8002",
	}
	discovery := NewStaticPeerDiscovery(peers)

	gotPeers := discovery.GetPeers()

	// Verify we got a copy, not the original map
	if len(gotPeers) != len(peers) {
		t.Errorf("GetPeers() returned %d peers, want %d", len(gotPeers), len(peers))
	}

	for id, addr := range peers {
		if gotPeers[id] != addr {
			t.Errorf("GetPeers()[%d] = %v, want %v", id, gotPeers[id], addr)
		}
	}

	// Modify returned map should not affect internal state
	gotPeers[1] = "modified"

	ctx := context.Background()
	addr, _ := discovery.GetPeerAddress(ctx, 1)
	if addr != "node1:8001" {
		t.Error("Internal state was modified by changing returned map")
	}
}

func TestStaticPeerDiscovery_ContextCancellation(t *testing.T) {
	discovery := NewStaticPeerDiscovery(map[int]string{1: "node1:8001"})

	// Create a context that's already cancelled
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// GetPeerAddress should still work with cancelled context for static discovery
	addr, err := discovery.GetPeerAddress(ctx, 1)
	if err != nil || addr != "node1:8001" {
		t.Errorf("GetPeerAddress with cancelled context: got %v, %v", addr, err)
	}
}

func TestStaticPeerDiscovery_ConcurrentAccess(t *testing.T) {
	peers := map[int]string{
		1: "node1:8001",
		2: "node2:8002",
		3: "node3:8003",
	}
	discovery := NewStaticPeerDiscovery(peers)

	// Run concurrent reads and writes
	done := make(chan bool)

	// Reader goroutines
	for i := 0; i < 10; i++ {
		go func(id int) {
			ctx := context.Background()
			for j := 0; j < 100; j++ {
				serverID := (j % 3) + 1
				discovery.GetPeerAddress(ctx, serverID) //nolint:errcheck // test mock
				discovery.GetPeers()
			}
			done <- true
		}(i)
	}

	// Writer goroutines
	for i := 0; i < 5; i++ {
		go func(id int) {
			for j := 0; j < 50; j++ {
				newPeers := map[int]string{
					1: "updated1:8001",
					2: "updated2:8002",
					3: "updated3:8003",
				}
				discovery.UpdatePeers(newPeers)
				time.Sleep(time.Microsecond)
			}
			done <- true
		}(i)
	}

	// Wait for all goroutines
	for i := 0; i < 15; i++ {
		<-done
	}

	// Verify discovery is still functional
	ctx := context.Background()
	_, err := discovery.GetPeerAddress(ctx, 1)
	if err != nil {
		t.Errorf("Discovery broken after concurrent access: %v", err)
	}
}
