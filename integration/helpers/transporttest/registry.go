package transporttest

import (
	"sync"

	"github.com/ueisele/raft"
)

// NodeRegistry manages node connections for testing.
// It provides a centralized registry where nodes can be registered
// and looked up by their ID, enabling in-memory communication
// between nodes in integration tests.
type NodeRegistry struct {
	mu     sync.RWMutex
	nodes  map[int]raft.RPCHandler
	logger raft.Logger // Optional logger for debug output
}

// NewNodeRegistry creates a new node registry
func NewNodeRegistry() *NodeRegistry {
	return &NodeRegistry{
		nodes: make(map[int]raft.RPCHandler),
	}
}

// NewNodeRegistryWithLogger creates a new node registry with debug logging
func NewNodeRegistryWithLogger(logger raft.Logger) *NodeRegistry {
	return &NodeRegistry{
		nodes:  make(map[int]raft.RPCHandler),
		logger: logger,
	}
}

// Register adds a node to the registry
func (r *NodeRegistry) Register(id int, handler raft.RPCHandler) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.nodes[id] = handler
	if r.logger != nil {
		r.logger.Debug("Registered node %d", id)
	}
}

// Unregister removes a node from the registry
func (r *NodeRegistry) Unregister(id int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.nodes, id)
	if r.logger != nil {
		r.logger.Debug("Unregistered node %d", id)
	}
}

// GetNode retrieves a node handler from the registry
func (r *NodeRegistry) GetNode(id int) (raft.RPCHandler, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	handler, exists := r.nodes[id]
	return handler, exists
}

// GetNodeCount returns the number of registered nodes
func (r *NodeRegistry) GetNodeCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.nodes)
}

// GetNodeIDs returns a slice of all registered node IDs
func (r *NodeRegistry) GetNodeIDs() []int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ids := make([]int, 0, len(r.nodes))
	for id := range r.nodes {
		ids = append(ids, id)
	}
	return ids
}
