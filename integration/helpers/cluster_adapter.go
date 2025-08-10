package helpers

import (
	"github.com/ueisele/raft"
	"github.com/ueisele/raft/integration/helpers/transporttest"
)

// TestClusterAdapter adapts the old array-based TestCluster to the new TransportProvider interface
// This allows gradual migration while maintaining backward compatibility
type TestClusterAdapter struct {
	*TestCluster
}

// GetTransports implements the new TransportProvider interface for the old TestCluster
// It converts the array-based storage to a map, assuming nodeID == array index
func (a *TestClusterAdapter) GetTransports() map[int]raft.Transport {
	a.mu.RLock()
	defer a.mu.RUnlock()
	
	// Convert array to map, using index as nodeID
	// This is the fundamental limitation we're fixing - it assumes sequential IDs
	result := make(map[int]raft.Transport, len(a.Transports))
	for i, transport := range a.Transports {
		if transport != nil {
			result[i] = transport
		}
	}
	return result
}

// Adapt wraps an old TestCluster to implement the new interface
func Adapt(cluster *TestCluster) transporttest.TransportProvider {
	return &TestClusterAdapter{TestCluster: cluster}
}