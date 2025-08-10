package transporttest

import (
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"

	"github.com/ueisele/raft"
)

// FailureDecorator adds random failure simulation to a transport.
// It implements the FailureCapable interface to allow injecting
// network failures at a configurable rate.
type FailureDecorator struct {
	baseDecorator
	mu          sync.RWMutex
	failureRate float64
	attempts    int64
	failures    int64
}

// NewFailureDecorator creates a new failure decorator with the specified failure rate
func NewFailureDecorator(wrapped raft.Transport, failureRate float64) *FailureDecorator {
	return &FailureDecorator{
		baseDecorator: baseDecorator{wrapped: wrapped},
		failureRate:   failureRate,
	}
}

// SetFailureRate sets the probability of failure (0.0 to 1.0)
func (d *FailureDecorator) SetFailureRate(rate float64) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.failureRate = rate
}

// GetFailureRate returns the current failure rate
func (d *FailureDecorator) GetFailureRate() float64 {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.failureRate
}

// GetStats returns the number of attempts and failures
func (d *FailureDecorator) GetStats() (attempts, failures int64) {
	return atomic.LoadInt64(&d.attempts), atomic.LoadInt64(&d.failures)
}

// ResetStats resets the statistics counters
func (d *FailureDecorator) ResetStats() {
	atomic.StoreInt64(&d.attempts, 0)
	atomic.StoreInt64(&d.failures, 0)
}

// shouldFail determines if the current operation should fail based on the failure rate
func (d *FailureDecorator) shouldFail() bool {
	d.mu.RLock()
	rate := d.failureRate
	d.mu.RUnlock()
	return rand.Float64() < rate
}

// SendRequestVote sends a RequestVote RPC, potentially failing based on the failure rate
func (d *FailureDecorator) SendRequestVote(serverID int, args *raft.RequestVoteArgs) (*raft.RequestVoteReply, error) {
	atomic.AddInt64(&d.attempts, 1)
	if d.shouldFail() {
		atomic.AddInt64(&d.failures, 1)
		return nil, fmt.Errorf("simulated network failure")
	}
	return d.wrapped.SendRequestVote(serverID, args)
}

// SendAppendEntries sends an AppendEntries RPC, potentially failing based on the failure rate
func (d *FailureDecorator) SendAppendEntries(serverID int, args *raft.AppendEntriesArgs) (*raft.AppendEntriesReply, error) {
	atomic.AddInt64(&d.attempts, 1)
	if d.shouldFail() {
		atomic.AddInt64(&d.failures, 1)
		return nil, fmt.Errorf("simulated network failure")
	}
	return d.wrapped.SendAppendEntries(serverID, args)
}

// SendInstallSnapshot sends an InstallSnapshot RPC, potentially failing based on the failure rate
func (d *FailureDecorator) SendInstallSnapshot(serverID int, args *raft.InstallSnapshotArgs) (*raft.InstallSnapshotReply, error) {
	atomic.AddInt64(&d.attempts, 1)
	if d.shouldFail() {
		atomic.AddInt64(&d.failures, 1)
		return nil, fmt.Errorf("simulated network failure")
	}
	return d.wrapped.SendInstallSnapshot(serverID, args)
}
