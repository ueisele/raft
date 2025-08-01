package raft

import (
	"math/rand"
	"sync"
	"time"
)

// StateManager handles state transitions and timer management
// This implements the state machine shown in Figure 4 of the Raft paper
type StateManager struct {
	mu           sync.RWMutex
	serverID     int
	currentState State
	currentTerm  int
	votedFor     *int
	leaderID     *int

	// Timers
	electionTimer   *time.Timer
	heartbeatTicker *time.Ticker

	// Configuration
	electionTimeoutMin time.Duration
	electionTimeoutMax time.Duration
	heartbeatInterval  time.Duration
	config             *Config

	// Random source for election timeouts
	rand *rand.Rand

	// Callbacks for state transitions
	onBecomeFollower  func()
	onBecomeCandidate func()
	onBecomeLeader    func()
}

// NewStateManager creates a new state manager
func NewStateManager(id int, config *Config) *StateManager {
	sm := &StateManager{
		serverID:           id,
		currentState:       Follower,
		currentTerm:        0,
		votedFor:           nil,
		electionTimeoutMin: config.ElectionTimeoutMin,
		electionTimeoutMax: config.ElectionTimeoutMax,
		heartbeatInterval:  config.HeartbeatInterval,
		config:             config,
		rand:               rand.New(rand.NewSource(time.Now().UnixNano() + int64(id))),
	}

	// Start as follower with election timer
	sm.resetElectionTimer()

	return sm
}

// GetState returns the current state and term
func (sm *StateManager) GetState() (State, int) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.currentState, sm.currentTerm
}

// GetTerm returns the current term
func (sm *StateManager) GetTerm() int {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.currentTerm
}

// GetVotedFor returns who we voted for in the current term
func (sm *StateManager) GetVotedFor() *int {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	if sm.votedFor == nil {
		return nil
	}
	// Return a copy to prevent external modification
	vote := *sm.votedFor
	return &vote
}

// SetTerm updates the current term
func (sm *StateManager) SetTerm(term int) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if term > sm.currentTerm {
		sm.currentTerm = term
		sm.votedFor = nil
		// Higher term always causes reversion to follower
		if sm.currentState != Follower {
			sm.transitionToFollowerLocked()
		}
	}
}

// Vote records a vote for the given candidate
func (sm *StateManager) Vote(candidateID int) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.votedFor = &candidateID
}

// GetLeaderID returns the current leader ID (nil if unknown)
func (sm *StateManager) GetLeaderID() *int {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	if sm.leaderID == nil {
		return nil
	}
	// Return a copy to prevent external modification
	leader := *sm.leaderID
	return &leader
}

// SetLeaderID sets the current leader ID
func (sm *StateManager) SetLeaderID(leaderID *int) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.leaderID = leaderID
}

// BecomeFollower transitions to follower state
func (sm *StateManager) BecomeFollower(term int) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	sm.currentTerm = term
	sm.votedFor = nil
	sm.transitionToFollowerLocked()
}

// BecomeCandidate transitions to candidate state
func (sm *StateManager) BecomeCandidate() int {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	sm.currentState = Candidate
	sm.currentTerm++
	sm.votedFor = nil // Will vote for self in election logic
	sm.leaderID = nil // Clear leader ID when starting election

	// Stop any existing timers
	sm.stopTimersLocked()

	// Reset election timer for this election attempt
	sm.resetElectionTimer()

	// Call transition callback
	if sm.onBecomeCandidate != nil {
		sm.onBecomeCandidate()
	}

	return sm.currentTerm
}

// BecomeLeader transitions to leader state
func (sm *StateManager) BecomeLeader() {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	sm.currentState = Leader
	sm.leaderID = &sm.serverID // Set self as leader

	// Stop election timer
	if sm.electionTimer != nil {
		sm.electionTimer.Stop()
		sm.electionTimer = nil
	}

	// Start heartbeat ticker
	sm.heartbeatTicker = time.NewTicker(sm.heartbeatInterval)

	// Call transition callback
	if sm.onBecomeLeader != nil {
		sm.onBecomeLeader()
	}
}

// ResetElectionTimer resets the election timeout
func (sm *StateManager) ResetElectionTimer() {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.resetElectionTimer()
}

// ForceElectionTimeout forces the election timer to expire soon
func (sm *StateManager) ForceElectionTimeout() {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	// Stop the current timer
	if sm.electionTimer != nil {
		sm.electionTimer.Stop()
	}

	// Set a very short timeout to trigger election quickly
	sm.electionTimer = time.NewTimer(50 * time.Millisecond)
}

// GetElectionTimer returns the election timer channel
func (sm *StateManager) GetElectionTimer() <-chan time.Time {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	if sm.electionTimer == nil {
		// Return a channel that never fires instead of nil to avoid panic
		return make(<-chan time.Time)
	}
	return sm.electionTimer.C
}

// GetHeartbeatTicker returns the heartbeat ticker channel
func (sm *StateManager) GetHeartbeatTicker() <-chan time.Time {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	if sm.heartbeatTicker == nil {
		// Return a channel that never fires instead of nil to avoid panic
		return make(<-chan time.Time)
	}
	return sm.heartbeatTicker.C
}

// GetCurrentTerm returns the current term
func (sm *StateManager) GetCurrentTerm() int {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.currentTerm
}

// SetCurrentTerm sets the current term
func (sm *StateManager) SetCurrentTerm(term int) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.currentTerm = term
}

// GetElectionTimeout returns a random election timeout
func (sm *StateManager) GetElectionTimeout() time.Duration {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	diff := sm.electionTimeoutMax - sm.electionTimeoutMin
	return sm.electionTimeoutMin + time.Duration(sm.rand.Int63n(int64(diff)))
}

// SetVotedFor sets the candidate we voted for
func (sm *StateManager) SetVotedFor(candidateID int) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.votedFor = &candidateID
}

// SetOnBecomeFollower sets the callback for becoming a follower
func (sm *StateManager) SetOnBecomeFollower(fn func()) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.onBecomeFollower = fn
}

// SetOnBecomeCandidate sets the callback for becoming a candidate
func (sm *StateManager) SetOnBecomeCandidate(fn func()) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.onBecomeCandidate = fn
}

// SetOnBecomeLeader sets the callback for becoming a leader
func (sm *StateManager) SetOnBecomeLeader(fn func()) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.onBecomeLeader = fn
}

// Stop stops all timers
func (sm *StateManager) Stop() {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.stopTimersLocked()
	// Also clear state to prevent any further operations
	sm.currentState = Follower
	sm.leaderID = nil
}

// Internal methods (assume lock is held)

func (sm *StateManager) transitionToFollowerLocked() {
	sm.currentState = Follower

	// Clear leader ID when becoming follower (unless set by AppendEntries)
	// This will be set again when we receive AppendEntries from new leader
	sm.leaderID = nil

	// Stop any leader-specific timers
	if sm.heartbeatTicker != nil {
		sm.heartbeatTicker.Stop()
		sm.heartbeatTicker = nil
	}

	// Reset election timer
	sm.resetElectionTimer()

	// Call transition callback
	if sm.onBecomeFollower != nil {
		sm.onBecomeFollower()
	}
}

func (sm *StateManager) resetElectionTimer() {
	if sm.electionTimer != nil {
		sm.electionTimer.Stop()
	}

	// Randomized timeout as per Raft paper Section 5.2
	var timeout time.Duration
	if sm.electionTimeoutMax > sm.electionTimeoutMin {
		timeout = sm.electionTimeoutMin +
			time.Duration(sm.rand.Int63n(int64(sm.electionTimeoutMax-sm.electionTimeoutMin)))
	} else {
		// If min == max, use that value without randomization
		timeout = sm.electionTimeoutMin
	}
	sm.electionTimer = time.NewTimer(timeout)

	if sm.config != nil && sm.config.Logger != nil {
		sm.config.Logger.Debug("Node %d reset election timer: %v", sm.serverID, timeout)
	}
}

func (sm *StateManager) stopTimersLocked() {
	if sm.electionTimer != nil {
		sm.electionTimer.Stop()
		sm.electionTimer = nil
	}

	if sm.heartbeatTicker != nil {
		sm.heartbeatTicker.Stop()
		sm.heartbeatTicker = nil
	}
}
