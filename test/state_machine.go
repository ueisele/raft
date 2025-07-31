package test

import (
	"encoding/json"
	"sync"
	"time"

	"github.com/ueisele/raft"
)

// SimpleStateMachine is a simple key-value state machine for testing
type SimpleStateMachine struct {
	mu            sync.RWMutex
	data          map[string]string
	appliedCount  int
	appliedChan   chan raft.LogEntry // Channel to notify when entries are applied
	snapshotCount int
	restoreCount  int
}

// NewSimpleStateMachine creates a new simple state machine
func NewSimpleStateMachine() *SimpleStateMachine {
	return &SimpleStateMachine{
		data:        make(map[string]string),
		appliedChan: make(chan raft.LogEntry, 100),
	}
}

// Apply implements StateMachine.Apply
func (sm *SimpleStateMachine) Apply(entry raft.LogEntry) interface{} {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	sm.appliedCount++

	// Send to applied channel for tests to verify
	select {
	case sm.appliedChan <- entry:
	default:
		// Channel full, ignore
	}

	// Handle different command types
	switch cmd := entry.Command.(type) {
	case string:
		// Simple string commands for basic tests
		sm.data["cmd"] = cmd
		return cmd

	case map[string]interface{}:
		// Map commands for more complex tests
		if op, ok := cmd["op"].(string); ok {
			switch op {
			case "set":
				key, _ := cmd["key"].(string)
				value, _ := cmd["value"].(string)
				sm.data[key] = value
				return "OK"
			case "delete":
				key, _ := cmd["key"].(string)
				delete(sm.data, key)
				return "OK"
			}
		}
		return "UNKNOWN"

	default:
		return "INVALID"
	}
}

// Snapshot implements StateMachine.Snapshot
func (sm *SimpleStateMachine) Snapshot() ([]byte, error) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	sm.snapshotCount++

	snapshot := struct {
		Data         map[string]string `json:"data"`
		AppliedCount int               `json:"applied_count"`
	}{
		Data:         sm.data,
		AppliedCount: sm.appliedCount,
	}

	return json.Marshal(snapshot)
}

// Restore implements StateMachine.Restore
func (sm *SimpleStateMachine) Restore(snapshotData []byte) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	sm.restoreCount++

	var snapshot struct {
		Data         map[string]string `json:"data"`
		AppliedCount int               `json:"applied_count"`
	}

	if err := json.Unmarshal(snapshotData, &snapshot); err != nil {
		return err
	}

	sm.data = make(map[string]string)
	for k, v := range snapshot.Data {
		sm.data[k] = v
	}
	sm.appliedCount = snapshot.AppliedCount

	return nil
}

// Test helper methods

// Get returns a value from the state machine
func (sm *SimpleStateMachine) Get(key string) (string, bool) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	value, exists := sm.data[key]
	return value, exists
}

// GetAll returns all key-value pairs
func (sm *SimpleStateMachine) GetAll() map[string]string {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	result := make(map[string]string)
	for k, v := range sm.data {
		result[k] = v
	}
	return result
}

// GetAppliedCount returns the number of entries applied
func (sm *SimpleStateMachine) GetAppliedCount() int {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.appliedCount
}

// GetAppliedChan returns the channel for applied entries
func (sm *SimpleStateMachine) GetAppliedChan() <-chan raft.LogEntry {
	return sm.appliedChan
}

// GetSnapshotCount returns the number of snapshots taken
func (sm *SimpleStateMachine) GetSnapshotCount() int {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.snapshotCount
}

// GetRestoreCount returns the number of restores performed
func (sm *SimpleStateMachine) GetRestoreCount() int {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.restoreCount
}

// WaitForApplied waits for a specific number of entries to be applied
func (sm *SimpleStateMachine) WaitForApplied(count int, timeout <-chan time.Time) bool {
	for {
		select {
		case <-sm.appliedChan:
			if sm.GetAppliedCount() >= count {
				return true
			}
		case <-timeout:
			return false
		}
	}
}
