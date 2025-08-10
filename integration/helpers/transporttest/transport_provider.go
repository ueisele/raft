package transporttest

import "github.com/ueisele/raft"

// TransportProvider is an interface for accessing transports in a test cluster.
// It provides a way for capability helper functions to access the transports
// without depending on the specific cluster implementation.
//
// GetTransports returns a map of all transports keyed by node ID.
// This properly handles non-sequential node IDs and node removal.
// The returned map is a copy and can be safely modified by the caller.
type TransportProvider interface {
	// GetTransports returns a map of all transports keyed by node ID
	// The returned map is a copy and can be safely modified
	GetTransports() map[int]raft.Transport
}
