package transporttest

import (
	"sync"
	"time"

	"github.com/ueisele/raft"
)

// DelayDecorator adds configurable delays to transport operations.
// It can simulate network latency for specific nodes or all nodes.
type DelayDecorator struct {
	baseDecorator
	mu     sync.RWMutex
	delays map[int]time.Duration // serverID -> delay, -1 for all
}

// NewDelayDecorator creates a new delay decorator that wraps the given transport.
func NewDelayDecorator(wrapped raft.Transport) *DelayDecorator {
	return &DelayDecorator{
		baseDecorator: baseDecorator{wrapped: wrapped},
		delays:        make(map[int]time.Duration),
	}
}

// getDelay returns the delay for a specific server.
// It checks for a specific delay first, then falls back to the global delay.
func (d *DelayDecorator) getDelay(serverID int) time.Duration {
	d.mu.RLock()
	defer d.mu.RUnlock()

	// Check for specific delay for this server
	if delay, ok := d.delays[serverID]; ok {
		return delay
	}

	// Check for global delay (serverID -1)
	if delay, ok := d.delays[-1]; ok {
		return delay
	}

	return 0
}

// applyDelay sleeps for the configured delay if any.
func (d *DelayDecorator) applyDelay(serverID int) {
	if delay := d.getDelay(serverID); delay > 0 {
		time.Sleep(delay)
	}
}

// SendRequestVote sends RequestVote RPC with optional delay.
func (d *DelayDecorator) SendRequestVote(serverID int, args *raft.RequestVoteArgs) (*raft.RequestVoteReply, error) {
	d.applyDelay(serverID)
	return d.wrapped.SendRequestVote(serverID, args)
}

// SendAppendEntries sends AppendEntries RPC with optional delay.
func (d *DelayDecorator) SendAppendEntries(serverID int, args *raft.AppendEntriesArgs) (*raft.AppendEntriesReply, error) {
	d.applyDelay(serverID)
	return d.wrapped.SendAppendEntries(serverID, args)
}

// SendInstallSnapshot sends InstallSnapshot RPC with optional delay.
func (d *DelayDecorator) SendInstallSnapshot(serverID int, args *raft.InstallSnapshotArgs) (*raft.InstallSnapshotReply, error) {
	d.applyDelay(serverID)
	return d.wrapped.SendInstallSnapshot(serverID, args)
}

// SetDelay implements DelayCapable interface.
// Sets the delay for RPCs to a specific node (-1 for all nodes).
func (d *DelayDecorator) SetDelay(serverID int, delay time.Duration) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if delay <= 0 {
		delete(d.delays, serverID)
	} else {
		d.delays[serverID] = delay
	}
}

// GetDelay implements DelayCapable interface.
// Returns the current delay for a specific node.
func (d *DelayDecorator) GetDelay(serverID int) time.Duration {
	return d.getDelay(serverID)
}

// ClearDelays implements DelayCapable interface.
// Removes all configured delays.
func (d *DelayDecorator) ClearDelays() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.delays = make(map[int]time.Duration)
}