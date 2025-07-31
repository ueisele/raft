package transport

import (
	"fmt"

	raft "github.com/ueisele/raft"
)

// Transport defines the interface for network communication between Raft nodes
// This abstraction allows different implementations (HTTP, gRPC, in-memory for testing)
type Transport interface {
	// SendRequestVote sends a RequestVote RPC to the specified server
	// Returns the reply or an error if the RPC failed
	SendRequestVote(serverID int, args *raft.RequestVoteArgs) (*raft.RequestVoteReply, error)

	// SendAppendEntries sends an AppendEntries RPC to the specified server
	// Returns the reply or an error if the RPC failed
	SendAppendEntries(serverID int, args *raft.AppendEntriesArgs) (*raft.AppendEntriesReply, error)

	// SendInstallSnapshot sends an InstallSnapshot RPC to the specified server
	// Returns the reply or an error if the RPC failed
	SendInstallSnapshot(serverID int, args *raft.InstallSnapshotArgs) (*raft.InstallSnapshotReply, error)

	// SetRPCHandler sets the RPC handler for incoming requests
	// The handler should implement the three RPC methods
	SetRPCHandler(handler RPCHandler)

	// Start starts the transport layer (e.g., HTTP server)
	Start() error

	// Stop gracefully shuts down the transport
	Stop() error

	// GetAddress returns the address this transport is listening on
	GetAddress() string
}

// RPCHandler handles incoming RPC requests
// This interface is implemented by the Raft node
type RPCHandler interface {
	// RequestVote handles incoming RequestVote RPCs
	RequestVote(args *raft.RequestVoteArgs, reply *raft.RequestVoteReply) error

	// AppendEntries handles incoming AppendEntries RPCs
	AppendEntries(args *raft.AppendEntriesArgs, reply *raft.AppendEntriesReply) error

	// InstallSnapshot handles incoming InstallSnapshot RPCs
	InstallSnapshot(args *raft.InstallSnapshotArgs, reply *raft.InstallSnapshotReply) error
}

// Config holds transport configuration
type Config struct {
	// ServerID is this server's ID
	ServerID int

	// Address to listen on (e.g., "localhost:8080")
	Address string

	// Timeout for RPC calls
	RPCTimeout int // milliseconds
}

// Error types for transport failures
type TransportError struct {
	ServerID int
	Err      error
}

func (e *TransportError) Error() string {
	return fmt.Sprintf("transport error to server %d: %v", e.ServerID, e.Err)
}
