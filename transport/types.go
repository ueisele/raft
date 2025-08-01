package transport

import (
	"fmt"
)

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
