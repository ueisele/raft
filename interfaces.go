package raft

import (
	"context"
	"time"
)

// StateMachine is implemented by the application using Raft
// This is the main integration point between Raft and the application
type StateMachine interface {
	// Apply is called when a log entry is committed
	// The implementation should apply the command and return the result
	Apply(entry LogEntry) interface{}

	// Snapshot asks the state machine to create a snapshot of its current state
	// This is used for log compaction (Section 7 of Raft paper)
	Snapshot() ([]byte, error)

	// Restore is called to restore the state machine from a snapshot
	// The state machine should clear its state and restore from the snapshot data
	Restore(snapshot []byte) error
}

// Node is the main interface exposed to users of the library
type Node interface {
	// Submit submits a command to the Raft cluster
	// Returns (index, term, isLeader)
	Submit(command interface{}) (int, int, bool)

	// GetState returns current term and whether this server is the leader
	GetState() (int, bool)

	// Start starts the Raft node
	Start(ctx context.Context) error

	// Stop gracefully shuts down the Raft node
	Stop()

	// IsLeader returns true if this node is the current leader
	IsLeader() bool

	// GetCurrentTerm returns the current term
	GetCurrentTerm() int

	// GetCommitIndex returns the current commit index
	GetCommitIndex() int

	// GetLogLength returns the current log length
	GetLogLength() int

	// GetLogEntry returns the log entry at the specified index
	GetLogEntry(index int) *LogEntry

	// GetTransportHandler returns the transport handler for this node
	GetTransportHandler() Transport

	// AddServer adds a new server to the cluster (leader only)
	// WARNING: Setting voting=true adds server as voting member immediately,
	// which can be dangerous. Use AddServerSafely for safer operation.
	AddServer(id int, address string, voting bool) error

	// AddServerSafely adds a new server as non-voting and automatically
	// promotes to voting after it has caught up with the log (leader only)
	AddServerSafely(id int, address string) error

	// RemoveServer removes a server from the cluster (leader only)
	RemoveServer(id int) error

	// GetConfiguration returns the current cluster configuration
	GetConfiguration() *ClusterConfiguration

	// GetServerProgress returns the catch-up progress of a non-voting server
	GetServerProgress(id int) *ServerProgress

	// TransferLeadership attempts to transfer leadership to the specified server
	TransferLeadership(targetID int) error

	// GetLeader returns the ID of the current leader (-1 if unknown)
	GetLeader() int
}

// RaftNode is an alias for Node (for backwards compatibility)
type RaftNode = Node

// Transport handles network communication between Raft nodes
// This abstraction allows different implementations (HTTP, gRPC, etc.)
type Transport interface {
	// SendRequestVote sends a RequestVote RPC to the specified server
	SendRequestVote(serverID int, args *RequestVoteArgs) (*RequestVoteReply, error)

	// SendAppendEntries sends an AppendEntries RPC to the specified server
	SendAppendEntries(serverID int, args *AppendEntriesArgs) (*AppendEntriesReply, error)

	// SendInstallSnapshot sends an InstallSnapshot RPC to the specified server
	SendInstallSnapshot(serverID int, args *InstallSnapshotArgs) (*InstallSnapshotReply, error)

	// SetRPCHandler sets the RPC handler for incoming requests
	SetRPCHandler(handler RPCHandler)

	// Start starts listening for incoming RPCs
	Start() error

	// Stop stops the transport
	Stop() error

	// GetAddress returns the address this transport is listening on
	GetAddress() string
}

// RPCHandler handles incoming RPC requests
type RPCHandler interface {
	// RequestVote handles incoming RequestVote RPCs
	RequestVote(args *RequestVoteArgs, reply *RequestVoteReply) error

	// AppendEntries handles incoming AppendEntries RPCs
	AppendEntries(args *AppendEntriesArgs, reply *AppendEntriesReply) error

	// InstallSnapshot handles incoming InstallSnapshot RPCs
	InstallSnapshot(args *InstallSnapshotArgs, reply *InstallSnapshotReply) error
}

// StatePersistence handles persistent state storage
// Implementations might use files, databases, etc.
type StatePersistence interface {
	// SaveState saves the persistent state
	SaveState(state *PersistentState) error

	// LoadState loads the persistent state
	// Returns nil state if no state exists
	LoadState() (*PersistentState, error)
}

// SnapshotPersistence handles snapshot storage
type SnapshotPersistence interface {
	// SaveSnapshot saves a snapshot
	SaveSnapshot(snapshot *Snapshot) error

	// LoadSnapshot loads the latest snapshot
	// Returns nil if no snapshot exists
	LoadSnapshot() (*Snapshot, error)

	// HasSnapshot returns true if a snapshot exists
	HasSnapshot() bool
}

// Persistence combines state and snapshot persistence
type Persistence interface {
	StatePersistence
	SnapshotPersistence
}

// Logger interface for custom logging (optional)
type Logger interface {
	Debug(format string, args ...interface{})
	Info(format string, args ...interface{})
	Warn(format string, args ...interface{})
	Error(format string, args ...interface{})
}

// Metrics interface for monitoring (optional)
type Metrics interface {
	RecordElection(won bool, duration time.Duration)
	RecordHeartbeat(peer int, success bool, duration time.Duration)
	RecordLogAppend(entries int)
	RecordCommit(index int)
	RecordSnapshot(size int, duration time.Duration)
}

// SnapshotProvider provides access to snapshots for replication
type SnapshotProvider interface {
	// GetLatestSnapshot returns the latest snapshot
	// Returns nil if no snapshot is available
	GetLatestSnapshot() (*Snapshot, error)
}

// Config holds the configuration for a Raft node.
// Default values will be applied for any zero values:
//   - MaxLogSize: 10000 (entries before snapshot)
//   - ElectionTimeoutMin: 150ms
//   - ElectionTimeoutMax: 300ms
//   - HeartbeatInterval: 50ms
type Config struct {
	// ID is this server's unique identifier
	ID int

	// Peers is the list of all servers in the cluster (including this one)
	Peers []int

	// ElectionTimeoutMin is the minimum election timeout (defaults to 150ms)
	ElectionTimeoutMin time.Duration

	// ElectionTimeoutMax is the maximum election timeout (defaults to 300ms)
	ElectionTimeoutMax time.Duration

	// HeartbeatInterval is the interval between heartbeats (defaults to 50ms)
	HeartbeatInterval time.Duration

	// MaxLogSize is the maximum number of log entries before taking a snapshot (defaults to 10000)
	MaxLogSize int

	// Logger for debug output (optional)
	Logger Logger

	// Metrics for monitoring (optional)
	Metrics Metrics
}
