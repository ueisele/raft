package json

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"sync"

	"github.com/ueisele/raft"
	"github.com/ueisele/raft/persistence"
)

// JSONPersistence implements Persistence interface using JSON files
type JSONPersistence struct {
	mu       sync.Mutex
	dataDir  string
	serverID int
}

// NewJSONPersistence creates a new JSON persistence
func NewJSONPersistence(config *persistence.Config) (*JSONPersistence, error) {
	// Ensure data directory exists
	if err := os.MkdirAll(config.DataDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create data directory: %v", err)
	}

	return &JSONPersistence{
		dataDir:  config.DataDir,
		serverID: config.ServerID,
	}, nil
}

// SaveState saves the persistent state
func (p *JSONPersistence) SaveState(state *raft.PersistentState) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return &persistence.PersistenceError{Op: "save", Err: err}
	}

	// Write to temporary file first for atomicity
	filename := p.getStateFilename()
	tempFilename := filename + ".tmp"

	if err := ioutil.WriteFile(tempFilename, data, 0644); err != nil {
		return &persistence.PersistenceError{Op: "save", Err: err}
	}

	// Atomic rename
	if err := os.Rename(tempFilename, filename); err != nil {
		return &persistence.PersistenceError{Op: "save", Err: err}
	}

	return nil
}

// LoadState loads the persistent state
func (p *JSONPersistence) LoadState() (*raft.PersistentState, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	filename := p.getStateFilename()

	data, err := ioutil.ReadFile(filename)
	if err != nil {
		if os.IsNotExist(err) {
			// No state file exists - return nil
			return nil, nil
		}
		return nil, &persistence.PersistenceError{Op: "load", Err: err}
	}

	var state raft.PersistentState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, &persistence.PersistenceError{Op: "load", Err: err}
	}

	// Ensure log has at least the dummy entry
	if len(state.Log) == 0 {
		state.Log = []raft.LogEntry{{Term: 0, Index: 0}}
	}

	return &state, nil
}

// SaveSnapshot saves a snapshot
func (p *JSONPersistence) SaveSnapshot(snapshot *raft.Snapshot) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	// Create a wrapper for JSON encoding
	wrapper := struct {
		LastIncludedIndex int    `json:"lastIncludedIndex"`
		LastIncludedTerm  int    `json:"lastIncludedTerm"`
		Data              []byte `json:"data"`
	}{
		LastIncludedIndex: snapshot.LastIncludedIndex,
		LastIncludedTerm:  snapshot.LastIncludedTerm,
		Data:              snapshot.Data,
	}

	data, err := json.MarshalIndent(wrapper, "", "  ")
	if err != nil {
		return &persistence.PersistenceError{Op: "save", Err: err}
	}

	// Write to temporary file first
	filename := p.getSnapshotFilename()
	tempFilename := filename + ".tmp"

	if err := ioutil.WriteFile(tempFilename, data, 0644); err != nil {
		return &persistence.PersistenceError{Op: "save", Err: err}
	}

	// Atomic rename
	if err := os.Rename(tempFilename, filename); err != nil {
		return &persistence.PersistenceError{Op: "save", Err: err}
	}

	return nil
}

// LoadSnapshot loads the latest snapshot
func (p *JSONPersistence) LoadSnapshot() (*raft.Snapshot, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	filename := p.getSnapshotFilename()

	data, err := ioutil.ReadFile(filename)
	if err != nil {
		if os.IsNotExist(err) {
			// No snapshot exists
			return nil, nil
		}
		return nil, &persistence.PersistenceError{Op: "load", Err: err}
	}

	var wrapper struct {
		LastIncludedIndex int    `json:"lastIncludedIndex"`
		LastIncludedTerm  int    `json:"lastIncludedTerm"`
		Data              []byte `json:"data"`
	}

	if err := json.Unmarshal(data, &wrapper); err != nil {
		return nil, &persistence.PersistenceError{Op: "load", Err: err}
	}

	return &raft.Snapshot{
		Data:              wrapper.Data,
		LastIncludedIndex: wrapper.LastIncludedIndex,
		LastIncludedTerm:  wrapper.LastIncludedTerm,
	}, nil
}

// HasSnapshot returns true if a snapshot exists
func (p *JSONPersistence) HasSnapshot() bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	filename := p.getSnapshotFilename()
	_, err := os.Stat(filename)
	return err == nil
}

// DeleteSnapshot removes the current snapshot
func (p *JSONPersistence) DeleteSnapshot() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	filename := p.getSnapshotFilename()
	err := os.Remove(filename)
	if os.IsNotExist(err) {
		return nil // Already deleted
	}

	if err != nil {
		return &persistence.PersistenceError{Op: "delete", Err: err}
	}

	return nil
}

// getStateFilename returns the filename for persistent state
func (p *JSONPersistence) getStateFilename() string {
	return filepath.Join(p.dataDir, fmt.Sprintf("raft-state-%d.json", p.serverID))
}

// getSnapshotFilename returns the filename for snapshots
func (p *JSONPersistence) getSnapshotFilename() string {
	return filepath.Join(p.dataDir, fmt.Sprintf("raft-snapshot-%d.json", p.serverID))
}
