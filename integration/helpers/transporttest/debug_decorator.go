package transporttest

import (
	"sync"

	"github.com/ueisele/raft"
)

// DebugDecorator adds debug logging to a transport.
// It implements the DebugCapable interface to allow detailed
// logging of all RPC traffic for debugging purposes.
type DebugDecorator struct {
	baseDecorator
	mu     sync.RWMutex
	logger raft.Logger
	nodeID int // ID of the node using this transport
}

// NewDebugDecorator creates a new debug decorator
func NewDebugDecorator(wrapped raft.Transport, nodeID int, logger raft.Logger) *DebugDecorator {
	return &DebugDecorator{
		baseDecorator: baseDecorator{wrapped: wrapped},
		nodeID:        nodeID,
		logger:        logger,
	}
}

// SetLogger sets the logger for debug output
func (d *DebugDecorator) SetLogger(logger raft.Logger) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.logger = logger
}

// log outputs a debug message if a logger is configured
func (d *DebugDecorator) log(format string, args ...interface{}) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if d.logger != nil {
		d.logger.Debug(format, args...)
	}
}

// SendRequestVote sends a RequestVote RPC with debug logging
func (d *DebugDecorator) SendRequestVote(serverID int, args *raft.RequestVoteArgs) (*raft.RequestVoteReply, error) {
	d.log("Node %d sending RequestVote to node %d: term=%d, candidateId=%d, lastLogIndex=%d, lastLogTerm=%d",
		d.nodeID, serverID, args.Term, args.CandidateID, args.LastLogIndex, args.LastLogTerm)

	reply, err := d.wrapped.SendRequestVote(serverID, args)

	if err != nil {
		d.log("Node %d RequestVote to node %d failed: %v", d.nodeID, serverID, err)
	} else if reply != nil {
		d.log("Node %d received RequestVote reply from node %d: granted=%v, term=%d",
			d.nodeID, serverID, reply.VoteGranted, reply.Term)
	}

	return reply, err
}

// SendAppendEntries sends an AppendEntries RPC with debug logging
func (d *DebugDecorator) SendAppendEntries(serverID int, args *raft.AppendEntriesArgs) (*raft.AppendEntriesReply, error) {
	d.log("Node %d sending AppendEntries to node %d: term=%d, leaderId=%d, prevLogIndex=%d, prevLogTerm=%d, entries=%d, leaderCommit=%d",
		d.nodeID, serverID, args.Term, args.LeaderID, args.PrevLogIndex, args.PrevLogTerm, len(args.Entries), args.LeaderCommit)

	reply, err := d.wrapped.SendAppendEntries(serverID, args)

	if err != nil {
		d.log("Node %d AppendEntries to node %d failed: %v", d.nodeID, serverID, err)
	} else if reply != nil {
		d.log("Node %d received AppendEntries reply from node %d: success=%v, term=%d",
			d.nodeID, serverID, reply.Success, reply.Term)
	}

	return reply, err
}

// SendInstallSnapshot sends an InstallSnapshot RPC with debug logging
func (d *DebugDecorator) SendInstallSnapshot(serverID int, args *raft.InstallSnapshotArgs) (*raft.InstallSnapshotReply, error) {
	d.log("Node %d sending InstallSnapshot to node %d: term=%d, leaderId=%d, lastIndex=%d, lastTerm=%d, dataSize=%d",
		d.nodeID, serverID, args.Term, args.LeaderID, args.LastIncludedIndex, args.LastIncludedTerm, len(args.Data))

	reply, err := d.wrapped.SendInstallSnapshot(serverID, args)

	if err != nil {
		d.log("Node %d InstallSnapshot to node %d failed: %v", d.nodeID, serverID, err)
	} else if reply != nil {
		d.log("Node %d received InstallSnapshot reply from node %d: term=%d",
			d.nodeID, serverID, reply.Term)
	}

	return reply, err
}
