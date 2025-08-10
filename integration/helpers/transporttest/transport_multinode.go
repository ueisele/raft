package transporttest

import (
	"fmt"

	"github.com/ueisele/raft"
)

// MultiNodeTransport allows nodes to communicate directly in memory for integration tests.
// It uses a NodeRegistry to look up target nodes and invoke their RPC handlers directly,
// simulating network communication without actual network overhead.
type MultiNodeTransport struct {
	id       int
	registry *NodeRegistry
	handler  raft.RPCHandler
}

// NewMultiNodeTransport creates a new multi-node transport for the specified node ID
func NewMultiNodeTransport(id int, registry *NodeRegistry) *MultiNodeTransport {
	return &MultiNodeTransport{
		id:       id,
		registry: registry,
	}
}

// SendRequestVote sends a RequestVote RPC to the target node
func (t *MultiNodeTransport) SendRequestVote(serverID int, args *raft.RequestVoteArgs) (*raft.RequestVoteReply, error) {
	handler, exists := t.registry.GetNode(serverID)
	if !exists {
		return nil, fmt.Errorf("node %d not found", serverID)
	}

	reply := &raft.RequestVoteReply{}
	err := handler.RequestVote(args, reply)
	return reply, err
}

// SendAppendEntries sends an AppendEntries RPC to the target node
func (t *MultiNodeTransport) SendAppendEntries(serverID int, args *raft.AppendEntriesArgs) (*raft.AppendEntriesReply, error) {
	handler, exists := t.registry.GetNode(serverID)
	if !exists {
		return nil, fmt.Errorf("node %d not found", serverID)
	}

	reply := &raft.AppendEntriesReply{}
	err := handler.AppendEntries(args, reply)
	return reply, err
}

// SendInstallSnapshot sends an InstallSnapshot RPC to the target node
func (t *MultiNodeTransport) SendInstallSnapshot(serverID int, args *raft.InstallSnapshotArgs) (*raft.InstallSnapshotReply, error) {
	handler, exists := t.registry.GetNode(serverID)
	if !exists {
		return nil, fmt.Errorf("node %d not found", serverID)
	}

	reply := &raft.InstallSnapshotReply{}
	err := handler.InstallSnapshot(args, reply)
	return reply, err
}

// SetRPCHandler sets the RPC handler for this transport
func (t *MultiNodeTransport) SetRPCHandler(handler raft.RPCHandler) {
	t.handler = handler
}

// Start starts the transport (no-op for in-memory transport)
func (t *MultiNodeTransport) Start() error {
	return nil
}

// Stop stops the transport (no-op for in-memory transport)
func (t *MultiNodeTransport) Stop() error {
	return nil
}

// GetAddress returns a string representation of this node's address
func (t *MultiNodeTransport) GetAddress() string {
	return fmt.Sprintf("node-%d", t.id)
}
