package main

import (
	"context"
	"fmt"
	"log"

	"github.com/ueisele/raft/transport"
	"github.com/ueisele/raft/transport/http"
)

// This example shows how to migrate from the old HTTPTransport API to the new one

func main() {
	// Example configuration
	config := &transport.Config{
		ServerID:   0,
		Address:    "localhost:8000",
		RPCTimeout: 5000,
	}

	// Define your cluster peers
	peers := map[int]string{
		0: "localhost:8000",
		1: "localhost:8001",
		2: "localhost:8002",
	}

	// Example 1: Old way (will be deprecated in v1.0.0)
	oldExample(config, peers)

	// Example 2: New way - Option A: Using NewHTTPTransportWithDiscovery
	newExampleWithDiscovery(config, peers)

	// Example 3: New way - Option B: Using NewHTTPTransportWithStaticPeers
	newExampleWithStaticPeers(config, peers)

	// Example 4: New way - Option C: Using Builder pattern
	newExampleWithBuilder(config, peers)
}

func oldExample(config *transport.Config, peers map[int]string) {
	fmt.Println("=== Old Way (Deprecated) ===")
	
	// Create transport without discovery
	transport := http.NewHTTPTransport(config)
	
	// Discovery must be set before Start() - easy to forget!
	discovery := transport.NewStaticPeerDiscovery(peers)
	transport.SetDiscovery(discovery)
	
	// This would fail if SetDiscovery was forgotten
	if err := transport.Start(); err != nil {
		log.Printf("Failed to start: %v", err)
		return
	}
	
	transport.Stop()
	fmt.Println("Old way completed (but please migrate!)\n")
}

func newExampleWithDiscovery(config *transport.Config, peers map[int]string) {
	fmt.Println("=== New Way - Option A: NewHTTPTransportWithDiscovery ===")
	
	// Create discovery first
	discovery := transport.NewStaticPeerDiscovery(peers)
	
	// Create transport with discovery - fail fast if misconfigured
	transport, err := http.NewHTTPTransportWithDiscovery(config, discovery)
	if err != nil {
		log.Printf("Failed to create transport: %v", err)
		return
	}
	
	// Start is guaranteed to have discovery
	if err := transport.Start(); err != nil {
		log.Printf("Failed to start: %v", err)
		return
	}
	
	transport.Stop()
	fmt.Println("New way with discovery completed\n")
}

func newExampleWithStaticPeers(config *transport.Config, peers map[int]string) {
	fmt.Println("=== New Way - Option B: NewHTTPTransportWithStaticPeers ===")
	
	// Convenience method for the common static peers case
	transport, err := http.NewHTTPTransportWithStaticPeers(config, peers)
	if err != nil {
		log.Printf("Failed to create transport: %v", err)
		return
	}
	
	if err := transport.Start(); err != nil {
		log.Printf("Failed to start: %v", err)
		return
	}
	
	transport.Stop()
	fmt.Println("New way with static peers completed\n")
}

func newExampleWithBuilder(config *transport.Config, peers map[int]string) {
	fmt.Println("=== New Way - Option C: Builder Pattern ===")
	
	// Using builder for more complex configurations
	discovery := transport.NewStaticPeerDiscovery(peers)
	
	transport, err := http.NewBuilder(config.ServerID, config.Address).
		WithDiscovery(discovery).
		WithTimeout(5 * 1000 * 1000 * 1000). // 5 seconds in nanoseconds
		Build()
		
	if err != nil {
		log.Printf("Failed to build transport: %v", err)
		return
	}
	
	if err := transport.Start(); err != nil {
		log.Printf("Failed to start: %v", err)
		return
	}
	
	transport.Stop()
	fmt.Println("New way with builder completed\n")
}

// Example: Migrating test code
func migrateTestCode() {
	// Old test pattern
	/*
	transport := http.NewHTTPTransport(config)
	transport.SetDiscovery(&mockDiscovery{
		addresses: map[int]string{
			1: "test-server:8001",
		},
	})
	*/

	// New test pattern
	config := &transport.Config{ServerID: 0, Address: "localhost:8000"}
	discovery := &mockDiscovery{
		addresses: map[int]string{
			1: "test-server:8001",
		},
	}
	
	transport, err := http.NewHTTPTransportWithDiscovery(config, discovery)
	if err != nil {
		log.Fatalf("Failed to create transport: %v", err)
	}
	
	// Use transport in tests...
	_ = transport
}

// Mock discovery for testing
type mockDiscovery struct {
	addresses map[int]string
}

func (m *mockDiscovery) GetPeerAddress(ctx context.Context, serverID int) (string, error) {
	addr, ok := m.addresses[serverID]
	if !ok {
		return "", fmt.Errorf("server %d not found", serverID)
	}
	return addr, nil
}

func (m *mockDiscovery) RefreshPeers(ctx context.Context) error {
	return nil
}

func (m *mockDiscovery) Close() error {
	return nil
}