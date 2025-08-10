package transporttest

import (
	"fmt"
	"sync"

	"github.com/ueisele/raft"
)

// PartitionableDecorator adds network partition simulation to a transport.
// It implements the PartitionCapable interface to allow dynamic blocking
// of communication between nodes.
type PartitionableDecorator struct {
	baseDecorator
	mu      sync.RWMutex
	blocked map[int]bool
}

// NewPartitionableDecorator creates a new partitionable decorator
func NewPartitionableDecorator(wrapped raft.Transport) *PartitionableDecorator {
	return &PartitionableDecorator{
		baseDecorator: baseDecorator{wrapped: wrapped},
		blocked:       make(map[int]bool),
	}
}

// Block prevents communication with a specific server
func (d *PartitionableDecorator) Block(serverID int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.blocked[serverID] = true
}

// Unblock allows communication with a specific server
func (d *PartitionableDecorator) Unblock(serverID int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.blocked, serverID)
}

// BlockAll blocks communication with all servers
func (d *PartitionableDecorator) BlockAll() {
	d.mu.Lock()
	defer d.mu.Unlock()
	// We use -1 as a special marker to indicate "all servers"
	// This is more efficient than tracking all possible server IDs
	d.blocked[-1] = true
}

// UnblockAll unblocks communication with all servers
func (d *PartitionableDecorator) UnblockAll() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.blocked = make(map[int]bool)
}

// IsBlocked checks if communication with a server is blocked
func (d *PartitionableDecorator) IsBlocked(serverID int) bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	// Check if this specific server is blocked or if all servers are blocked
	return d.blocked[serverID] || d.blocked[-1]
}

// SendRequestVote sends a RequestVote RPC, failing if the target is partitioned
func (d *PartitionableDecorator) SendRequestVote(serverID int, args *raft.RequestVoteArgs) (*raft.RequestVoteReply, error) {
	if d.IsBlocked(serverID) {
		return nil, fmt.Errorf("network partition: cannot reach server %d", serverID)
	}
	return d.wrapped.SendRequestVote(serverID, args)
}

// SendAppendEntries sends an AppendEntries RPC, failing if the target is partitioned
func (d *PartitionableDecorator) SendAppendEntries(serverID int, args *raft.AppendEntriesArgs) (*raft.AppendEntriesReply, error) {
	if d.IsBlocked(serverID) {
		return nil, fmt.Errorf("network partition: cannot reach server %d", serverID)
	}
	return d.wrapped.SendAppendEntries(serverID, args)
}

// SendInstallSnapshot sends an InstallSnapshot RPC, failing if the target is partitioned
func (d *PartitionableDecorator) SendInstallSnapshot(serverID int, args *raft.InstallSnapshotArgs) (*raft.InstallSnapshotReply, error) {
	if d.IsBlocked(serverID) {
		return nil, fmt.Errorf("network partition: cannot reach server %d", serverID)
	}
	return d.wrapped.SendInstallSnapshot(serverID, args)
}
