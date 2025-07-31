package persistence

import (
	raft "github.com/ueisele/raft"
)

// Persistence defines the interface for persistent storage
// Implementations might use files, databases, or other storage backends
type Persistence interface {
	StatePersistence
	SnapshotPersistence
}

// StatePersistence handles persistent state storage
// This includes currentTerm, votedFor, and log entries
type StatePersistence interface {
	// SaveState atomically saves the persistent state
	// Must be durable before returning
	SaveState(state *raft.PersistentState) error

	// LoadState loads the persistent state
	// Returns nil if no state exists (fresh start)
	LoadState() (*raft.PersistentState, error)
}

// SnapshotPersistence handles snapshot storage
type SnapshotPersistence interface {
	// SaveSnapshot atomically saves a snapshot
	// Must be durable before returning
	SaveSnapshot(snapshot *raft.Snapshot) error

	// LoadSnapshot loads the latest snapshot
	// Returns nil if no snapshot exists
	LoadSnapshot() (*raft.Snapshot, error)

	// HasSnapshot returns true if a snapshot exists
	HasSnapshot() bool

	// DeleteSnapshot removes the current snapshot
	// Used when a newer snapshot replaces it
	DeleteSnapshot() error
}

// Config holds persistence configuration
type Config struct {
	// DataDir is the directory for persistent data
	DataDir string

	// ServerID identifies this server's data
	ServerID int
}

// Error types for persistence failures
type PersistenceError struct {
	Op  string // "save" or "load"
	Err error
}

func (e *PersistenceError) Error() string {
	return "persistence " + e.Op + " error: " + e.Err.Error()
}
