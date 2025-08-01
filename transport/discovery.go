package transport

import (
	"context"
	"fmt"
	"sync"
)

// PeerDiscovery provides a pluggable interface for discovering peer addresses in a Raft cluster.
// Implementations can use static configuration, DNS, service registries, or other mechanisms.
type PeerDiscovery interface {
	// GetPeerAddress returns the network address for a given server ID.
	// It should return an error if the server ID is unknown or if discovery fails.
	GetPeerAddress(ctx context.Context, serverID int) (string, error)

	// RefreshPeers updates the peer list if dynamic discovery is used.
	// For static configurations, this may be a no-op.
	RefreshPeers(ctx context.Context) error

	// Close releases any resources held by the discovery mechanism.
	Close() error
}

// StaticPeerDiscovery implements PeerDiscovery using a fixed configuration.
// This is the simplest and most common approach for production deployments.
type StaticPeerDiscovery struct {
	peers map[int]string
	mu    sync.RWMutex
}

// NewStaticPeerDiscovery creates a new static peer discovery instance.
// The peers map should contain serverID -> address mappings.
func NewStaticPeerDiscovery(peers map[int]string) *StaticPeerDiscovery {
	peersCopy := make(map[int]string, len(peers))
	for id, addr := range peers {
		peersCopy[id] = addr
	}
	return &StaticPeerDiscovery{
		peers: peersCopy,
	}
}

// GetPeerAddress returns the configured address for the given server ID.
func (s *StaticPeerDiscovery) GetPeerAddress(ctx context.Context, serverID int) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	addr, ok := s.peers[serverID]
	if !ok {
		return "", fmt.Errorf("unknown server ID: %d", serverID)
	}
	if addr == "" {
		return "", fmt.Errorf("empty address for server ID: %d", serverID)
	}
	return addr, nil
}

// UpdatePeers allows updating the peer configuration at runtime.
// This is useful for dynamic cluster membership changes.
func (s *StaticPeerDiscovery) UpdatePeers(peers map[int]string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.peers = make(map[int]string, len(peers))
	for id, addr := range peers {
		s.peers[id] = addr
	}
}

// RefreshPeers is a no-op for static discovery since peers are configured manually.
func (s *StaticPeerDiscovery) RefreshPeers(ctx context.Context) error {
	// No-op for static configuration
	return nil
}

// Close is a no-op for static discovery as there are no resources to release.
func (s *StaticPeerDiscovery) Close() error {
	// No-op for static configuration
	return nil
}

// GetPeers returns a copy of the current peer configuration.
// This is useful for debugging and monitoring.
func (s *StaticPeerDiscovery) GetPeers() map[int]string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	peersCopy := make(map[int]string, len(s.peers))
	for id, addr := range s.peers {
		peersCopy[id] = addr
	}
	return peersCopy
}