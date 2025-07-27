package raft

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"time"
)

// PersistentState represents the persistent state of a Raft server
type PersistentState struct {
	CurrentTerm int        `json:"currentTerm"`
	VotedFor    *int       `json:"votedFor"`
	Log         []LogEntry `json:"log"`
}

// Persister handles persistence of Raft state to disk
type Persister struct {
	dataDir  string
	serverID int
}

// NewPersister creates a new persister for the given server
func NewPersister(dataDir string, serverID int) *Persister {
	return &Persister{
		dataDir:  dataDir,
		serverID: serverID,
	}
}

// SaveState saves the persistent state to disk
func (p *Persister) SaveState(currentTerm int, votedFor *int, log []LogEntry) error {
	state := PersistentState{
		CurrentTerm: currentTerm,
		VotedFor:    votedFor,
		Log:         log,
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal state: %v", err)
	}

	// Ensure data directory exists
	if err := os.MkdirAll(p.dataDir, 0755); err != nil {
		return fmt.Errorf("failed to create data directory: %v", err)
	}

	// Write to temporary file first, then rename for atomic write
	filename := p.getStateFilename()
	tempFilename := filename + ".tmp"

	if err := ioutil.WriteFile(tempFilename, data, 0644); err != nil {
		return fmt.Errorf("failed to write state file: %v", err)
	}

	if err := os.Rename(tempFilename, filename); err != nil {
		return fmt.Errorf("failed to rename state file: %v", err)
	}

	return nil
}

// LoadState loads the persistent state from disk
func (p *Persister) LoadState() (int, *int, []LogEntry, error) {
	filename := p.getStateFilename()

	data, err := ioutil.ReadFile(filename)
	if err != nil {
		if os.IsNotExist(err) {
			// File doesn't exist, return initial state
			log := []LogEntry{{Term: 0, Index: 0}} // dummy entry at index 0
			return 0, nil, log, nil
		}
		return 0, nil, nil, fmt.Errorf("failed to read state file: %v", err)
	}

	var state PersistentState
	if err := json.Unmarshal(data, &state); err != nil {
		return 0, nil, nil, fmt.Errorf("failed to unmarshal state: %v", err)
	}

	// Ensure log has at least the dummy entry
	if len(state.Log) == 0 {
		state.Log = []LogEntry{{Term: 0, Index: 0}}
	}

	return state.CurrentTerm, state.VotedFor, state.Log, nil
}

// SaveSnapshot saves a snapshot to disk
func (p *Persister) SaveSnapshot(snapshot []byte, lastIncludedIndex, lastIncludedTerm int) error {
	snapshotData := map[string]interface{}{
		"lastIncludedIndex": lastIncludedIndex,
		"lastIncludedTerm":  lastIncludedTerm,
		"data":              snapshot,
	}

	data, err := json.MarshalIndent(snapshotData, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal snapshot: %v", err)
	}

	// Ensure data directory exists
	if err := os.MkdirAll(p.dataDir, 0755); err != nil {
		return fmt.Errorf("failed to create data directory: %v", err)
	}

	// Write to temporary file first, then rename for atomic write
	filename := p.getSnapshotFilename()
	tempFilename := filename + ".tmp"

	if err := ioutil.WriteFile(tempFilename, data, 0644); err != nil {
		return fmt.Errorf("failed to write snapshot file: %v", err)
	}

	if err := os.Rename(tempFilename, filename); err != nil {
		return fmt.Errorf("failed to rename snapshot file: %v", err)
	}

	return nil
}

// LoadSnapshot loads a snapshot from disk
func (p *Persister) LoadSnapshot() ([]byte, int, int, error) {
	filename := p.getSnapshotFilename()

	data, err := ioutil.ReadFile(filename)
	if err != nil {
		if os.IsNotExist(err) {
			// No snapshot exists
			return nil, 0, 0, nil
		}
		return nil, 0, 0, fmt.Errorf("failed to read snapshot file: %v", err)
	}

	var snapshotData map[string]interface{}
	if err := json.Unmarshal(data, &snapshotData); err != nil {
		return nil, 0, 0, fmt.Errorf("failed to unmarshal snapshot: %v", err)
	}

	lastIncludedIndex := int(snapshotData["lastIncludedIndex"].(float64))
	lastIncludedTerm := int(snapshotData["lastIncludedTerm"].(float64))
	
	// Handle the snapshot data which could be base64 encoded
	var snapshot []byte
	switch data := snapshotData["data"].(type) {
	case string:
		// Try base64 decode first, fallback to direct conversion
		if decoded, err := base64.StdEncoding.DecodeString(data); err == nil {
			snapshot = decoded
		} else {
			snapshot = []byte(data)
		}
	case []interface{}:
		// Handle JSON array representation of bytes
		snapshot = make([]byte, len(data))
		for i, v := range data {
			snapshot[i] = byte(v.(float64))
		}
	default:
		return nil, 0, 0, fmt.Errorf("unexpected data type for snapshot data")
	}

	return snapshot, lastIncludedIndex, lastIncludedTerm, nil
}

// HasSnapshot checks if a snapshot exists
func (p *Persister) HasSnapshot() bool {
	filename := p.getSnapshotFilename()
	_, err := os.Stat(filename)
	return err == nil
}

// DeleteSnapshot deletes the snapshot file
func (p *Persister) DeleteSnapshot() error {
	filename := p.getSnapshotFilename()
	err := os.Remove(filename)
	if os.IsNotExist(err) {
		return nil // Already deleted
	}
	return err
}

// getStateFilename returns the filename for the persistent state
func (p *Persister) getStateFilename() string {
	return filepath.Join(p.dataDir, fmt.Sprintf("raft-state-%d.json", p.serverID))
}

// getSnapshotFilename returns the filename for the snapshot
func (p *Persister) getSnapshotFilename() string {
	return filepath.Join(p.dataDir, fmt.Sprintf("raft-snapshot-%d.json", p.serverID))
}

// Update the Raft struct to use the persister
func (rf *Raft) SetPersister(persister *Persister) {
	rf.persister = persister
	
	// Load existing state if available
	if persister != nil {
		currentTerm, votedFor, log, err := persister.LoadState()
		if err == nil {
			rf.mu.Lock()
			rf.currentTerm = currentTerm
			rf.votedFor = votedFor
			rf.log = log
			rf.mu.Unlock()
		}
		
		// Load snapshot if available
		if persister.HasSnapshot() {
			snapshotData, lastIncludedIndex, lastIncludedTerm, err := persister.LoadSnapshot()
			if err == nil && snapshotData != nil {
				rf.mu.Lock()
				rf.lastSnapshotIndex = lastIncludedIndex
				rf.lastSnapshotTerm = lastIncludedTerm
				// Update lastApplied and commitIndex if they're behind the snapshot
				if rf.lastApplied < lastIncludedIndex {
					rf.lastApplied = lastIncludedIndex
				}
				if rf.commitIndex < lastIncludedIndex {
					rf.commitIndex = lastIncludedIndex
				}
				// Send snapshot to application
				rf.mu.Unlock()
				
				// Apply the snapshot to the state machine
				if rf.applyCh != nil {
					go func() {
						select {
						case rf.applyCh <- LogEntry{
							Term:    lastIncludedTerm,
							Index:   lastIncludedIndex,
							Command: snapshotData,
						}:
						case <-time.After(1 * time.Second):
							// Server failed to send snapshot to apply channel
						}
					}()
				}
			}
		}
	}
}

// Update the persist method to use the persister
func (rf *Raft) persistWithPersister() {
	if rf.persister != nil {
		rf.persister.SaveState(rf.currentTerm, rf.votedFor, rf.log)
	}
}