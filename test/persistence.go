package test

import (
	"fmt"
	"sync"

	"github.com/ueisele/raft"
)

// InMemoryPersistence implements Persistence interface for testing
type InMemoryPersistence struct {
	mu        sync.Mutex
	state     *raft.PersistentState
	snapshot  *raft.Snapshot
	saveCount int
	loadCount int
	failNext  bool // Used to simulate failures
}

// NewInMemoryPersistence creates a new in-memory persistence for testing
func NewInMemoryPersistence() *InMemoryPersistence {
	return &InMemoryPersistence{}
}

// SaveState saves the persistent state
func (p *InMemoryPersistence) SaveState(state *raft.PersistentState) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.failNext {
		p.failNext = false
		return fmt.Errorf("persistence save error: simulated failure")
	}

	p.saveCount++

	// Deep copy the state
	newState := &raft.PersistentState{
		CurrentTerm: state.CurrentTerm,
		VotedFor:    state.VotedFor,
		Log:         make([]raft.LogEntry, len(state.Log)),
	}
	copy(newState.Log, state.Log)

	p.state = newState
	return nil
}

// LoadState loads the persistent state
func (p *InMemoryPersistence) LoadState() (*raft.PersistentState, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.failNext {
		p.failNext = false
		return nil, fmt.Errorf("persistence load error: simulated failure")
	}

	p.loadCount++

	if p.state == nil {
		return nil, nil
	}

	// Deep copy the state
	loadedState := &raft.PersistentState{
		CurrentTerm: p.state.CurrentTerm,
		VotedFor:    p.state.VotedFor,
		Log:         make([]raft.LogEntry, len(p.state.Log)),
	}
	copy(loadedState.Log, p.state.Log)

	return loadedState, nil
}

// SaveSnapshot saves a snapshot
func (p *InMemoryPersistence) SaveSnapshot(snapshot *raft.Snapshot) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.failNext {
		p.failNext = false
		return fmt.Errorf("persistence save error: simulated snapshot failure")
	}

	// Deep copy the snapshot
	newSnapshot := &raft.Snapshot{
		Data:              make([]byte, len(snapshot.Data)),
		LastIncludedIndex: snapshot.LastIncludedIndex,
		LastIncludedTerm:  snapshot.LastIncludedTerm,
	}
	copy(newSnapshot.Data, snapshot.Data)

	p.snapshot = newSnapshot
	return nil
}

// LoadSnapshot loads the latest snapshot
func (p *InMemoryPersistence) LoadSnapshot() (*raft.Snapshot, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.failNext {
		p.failNext = false
		return nil, fmt.Errorf("persistence load error: simulated snapshot load failure")
	}

	if p.snapshot == nil {
		return nil, nil
	}

	// Deep copy the snapshot
	loadedSnapshot := &raft.Snapshot{
		Data:              make([]byte, len(p.snapshot.Data)),
		LastIncludedIndex: p.snapshot.LastIncludedIndex,
		LastIncludedTerm:  p.snapshot.LastIncludedTerm,
	}
	copy(loadedSnapshot.Data, p.snapshot.Data)

	return loadedSnapshot, nil
}

// HasSnapshot returns true if a snapshot exists
func (p *InMemoryPersistence) HasSnapshot() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.snapshot != nil
}

// DeleteSnapshot removes the current snapshot
func (p *InMemoryPersistence) DeleteSnapshot() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.failNext {
		p.failNext = false
		return fmt.Errorf("persistence delete error: simulated delete failure")
	}

	p.snapshot = nil
	return nil
}

// Test helper methods

// SetFailNext makes the next operation fail
func (p *InMemoryPersistence) SetFailNext() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.failNext = true
}

// GetSaveCount returns the number of times SaveState was called
func (p *InMemoryPersistence) GetSaveCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.saveCount
}

// GetLoadCount returns the number of times LoadState was called
func (p *InMemoryPersistence) GetLoadCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.loadCount
}

// GetState returns the current saved state (for testing)
func (p *InMemoryPersistence) GetState() *raft.PersistentState {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.state
}

// GetSnapshot returns the current saved snapshot (for testing)
func (p *InMemoryPersistence) GetSnapshot() *raft.Snapshot {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.snapshot
}
