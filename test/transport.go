package test

import (
	"fmt"
	"sync"

	"github.com/ueisele/raft"
)

// InMemoryTransport provides in-memory RPC transport for testing
type InMemoryTransport struct {
	mu                   sync.RWMutex
	servers              map[int]raft.RPCHandler
	disconnectedServers  map[int]bool         // Servers that are completely disconnected
	partitions           map[int]map[int]bool // [from][to] -> blocked (symmetric)
	asymmetricPartitions map[int]map[int]bool // [from][to] -> blocked (one-way)
	handler              raft.RPCHandler      // reference to handler interface
	serverID             int
}

// NewInMemoryTransport creates a new test transport
func NewInMemoryTransport(serverID int) *InMemoryTransport {
	return &InMemoryTransport{
		serverID:             serverID,
		servers:              make(map[int]raft.RPCHandler),
		disconnectedServers:  make(map[int]bool),
		partitions:           make(map[int]map[int]bool),
		asymmetricPartitions: make(map[int]map[int]bool),
	}
}

// RegisterServer registers a Raft node with the transport
func (t *InMemoryTransport) RegisterServer(id int, server raft.RPCHandler) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.servers[id] = server
}

// SetRPCHandler sets the RPC handler for incoming requests
func (t *InMemoryTransport) SetRPCHandler(handler raft.RPCHandler) {
	t.handler = handler
}

// Start starts the transport (no-op for in-memory)
func (t *InMemoryTransport) Start() error {
	return nil
}

// Stop stops the transport (no-op for in-memory)
func (t *InMemoryTransport) Stop() error {
	return nil
}

// GetAddress returns the address this transport is listening on
func (t *InMemoryTransport) GetAddress() string {
	return ""
}

// SendRequestVote sends a RequestVote RPC to the target server
func (t *InMemoryTransport) SendRequestVote(serverID int, args *raft.RequestVoteArgs) (*raft.RequestVoteReply, error) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	// Check if either server is disconnected
	if t.disconnectedServers[t.serverID] || t.disconnectedServers[serverID] {
		return nil, fmt.Errorf("server %d disconnected", serverID)
	}

	// Check for symmetric partition
	if fromPartitions, exists := t.partitions[t.serverID]; exists {
		if fromPartitions[serverID] {
			return nil, fmt.Errorf("network partition between %d and %d", t.serverID, serverID)
		}
	}

	// Check for asymmetric partition on the forward path
	if asymPartitions, exists := t.asymmetricPartitions[t.serverID]; exists {
		if asymPartitions[serverID] {
			return nil, fmt.Errorf("asymmetric partition from %d to %d", t.serverID, serverID)
		}
	}

	target, exists := t.servers[serverID]
	if !exists {
		return nil, fmt.Errorf("server %d not found", serverID)
	}

	// Simulate network call
	var reply raft.RequestVoteReply
	err := target.RequestVote(args, &reply)
	if err != nil {
		return nil, err
	}

	// Check for asymmetric partition on the return path (response blocked)
	if asymPartitions, exists := t.asymmetricPartitions[serverID]; exists {
		if asymPartitions[t.serverID] {
			// The RPC was processed but the response is blocked
			return nil, fmt.Errorf("response blocked from %d to %d", serverID, t.serverID)
		}
	}

	return &reply, nil
}

// SendAppendEntries sends an AppendEntries RPC to the target server
func (t *InMemoryTransport) SendAppendEntries(serverID int, args *raft.AppendEntriesArgs) (*raft.AppendEntriesReply, error) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	// Check if either server is disconnected
	if t.disconnectedServers[t.serverID] || t.disconnectedServers[serverID] {
		return nil, fmt.Errorf("server %d disconnected", serverID)
	}

	// Check for symmetric partition
	if fromPartitions, exists := t.partitions[t.serverID]; exists {
		if fromPartitions[serverID] {
			return nil, fmt.Errorf("network partition between %d and %d", t.serverID, serverID)
		}
	}

	// Check for asymmetric partition on the forward path
	if asymPartitions, exists := t.asymmetricPartitions[t.serverID]; exists {
		if asymPartitions[serverID] {
			return nil, fmt.Errorf("asymmetric partition from %d to %d", t.serverID, serverID)
		}
	}

	target, exists := t.servers[serverID]
	if !exists {
		return nil, fmt.Errorf("server %d not found", serverID)
	}

	// Simulate network call
	var reply raft.AppendEntriesReply
	err := target.AppendEntries(args, &reply)
	if err != nil {
		return nil, err
	}

	// Check for asymmetric partition on the return path (response blocked)
	if asymPartitions, exists := t.asymmetricPartitions[serverID]; exists {
		if asymPartitions[t.serverID] {
			// The RPC was processed but the response is blocked
			return nil, fmt.Errorf("response blocked from %d to %d", serverID, t.serverID)
		}
	}

	return &reply, nil
}

// SendInstallSnapshot sends an InstallSnapshot RPC to the target server
func (t *InMemoryTransport) SendInstallSnapshot(serverID int, args *raft.InstallSnapshotArgs) (*raft.InstallSnapshotReply, error) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	// Check if either server is disconnected
	if t.disconnectedServers[t.serverID] || t.disconnectedServers[serverID] {
		return nil, fmt.Errorf("server %d disconnected", serverID)
	}

	// Check for symmetric partition
	if fromPartitions, exists := t.partitions[t.serverID]; exists {
		if fromPartitions[serverID] {
			return nil, fmt.Errorf("network partition between %d and %d", t.serverID, serverID)
		}
	}

	// Check for asymmetric partition on the forward path
	if asymPartitions, exists := t.asymmetricPartitions[t.serverID]; exists {
		if asymPartitions[serverID] {
			return nil, fmt.Errorf("asymmetric partition from %d to %d", t.serverID, serverID)
		}
	}

	target, exists := t.servers[serverID]
	if !exists {
		return nil, fmt.Errorf("server %d not found", serverID)
	}

	// Simulate network call
	var reply raft.InstallSnapshotReply
	err := target.InstallSnapshot(args, &reply)
	if err != nil {
		return nil, err
	}

	// Check for asymmetric partition on the return path (response blocked)
	if asymPartitions, exists := t.asymmetricPartitions[serverID]; exists {
		if asymPartitions[t.serverID] {
			// The RPC was processed but the response is blocked
			return nil, fmt.Errorf("response blocked from %d to %d", serverID, t.serverID)
		}
	}

	return &reply, nil
}

// DisconnectServer simulates a server going offline
func (t *InMemoryTransport) DisconnectServer(serverID int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.disconnectedServers[serverID] = true
}

// ReconnectServer simulates a server coming back online
func (t *InMemoryTransport) ReconnectServer(serverID int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.disconnectedServers, serverID)
}

// DisconnectPair creates a partition between two specific servers
func (t *InMemoryTransport) DisconnectPair(server1, server2 int) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.partitions[server1] == nil {
		t.partitions[server1] = make(map[int]bool)
	}
	if t.partitions[server2] == nil {
		t.partitions[server2] = make(map[int]bool)
	}

	t.partitions[server1][server2] = true
	t.partitions[server2][server1] = true
}

// ReconnectPair removes a partition between two specific servers
func (t *InMemoryTransport) ReconnectPair(server1, server2 int) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.partitions[server1] != nil {
		delete(t.partitions[server1], server2)
	}
	if t.partitions[server2] != nil {
		delete(t.partitions[server2], server1)
	}
}

// SetAsymmetricPartition creates or removes a one-way partition
// If set is true, from cannot send to to (but to can still send to from)
func (t *InMemoryTransport) SetAsymmetricPartition(from, to int, set bool) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if set {
		if t.asymmetricPartitions[from] == nil {
			t.asymmetricPartitions[from] = make(map[int]bool)
		}
		t.asymmetricPartitions[from][to] = true
	} else {
		if t.asymmetricPartitions[from] != nil {
			delete(t.asymmetricPartitions[from], to)
		}
	}
}
