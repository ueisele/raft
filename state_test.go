package raft

import (
	"testing"
	"time"
)

// TestStateManagerTransitions tests state transitions
func TestStateManagerTransitions(t *testing.T) {
	config := &Config{
		ID:                 1,
		ElectionTimeoutMin: 100 * time.Millisecond,
		ElectionTimeoutMax: 200 * time.Millisecond,
		HeartbeatInterval:  50 * time.Millisecond,
	}
	sm := NewStateManager(1, config)

	// Initial state should be follower
	state, _ := sm.GetState()
	if state != Follower {
		t.Errorf("Initial state should be Follower, got %v", state)
	}

	// Test transition to candidate
	sm.BecomeCandidate()
	state, _ = sm.GetState()
	if state != Candidate {
		t.Errorf("State should be Candidate after BecomeCandidate, got %v", state)
	}
	if sm.GetCurrentTerm() != 1 {
		t.Errorf("Term should be 1 after BecomeCandidate, got %d", sm.GetCurrentTerm())
	}
	// VotedFor is cleared when becoming candidate - will be set during election
	if sm.GetVotedFor() != nil {
		t.Errorf("VotedFor should be nil after BecomeCandidate, got %v", *sm.GetVotedFor())
	}

	// Test transition to leader
	sm.BecomeLeader()
	state, _ = sm.GetState()
	if state != Leader {
		t.Errorf("State should be Leader after BecomeLeader, got %v", state)
	}

	// Test transition to follower
	sm.BecomeFollower(3)
	state, _ = sm.GetState()
	if state != Follower {
		t.Errorf("State should be Follower after BecomeFollower, got %v", state)
	}
	if sm.GetCurrentTerm() != 3 {
		t.Errorf("Term should be 3 after BecomeFollower, got %d", sm.GetCurrentTerm())
	}
	if sm.GetVotedFor() != nil {
		t.Errorf("VotedFor should be nil after BecomeFollower with nil leader")
	}
}

// TestStateManagerTermUpdates tests term updates
func TestStateManagerTermUpdates(t *testing.T) {
	config := &Config{
		ID:                 1,
		ElectionTimeoutMin: 100 * time.Millisecond,
		ElectionTimeoutMax: 200 * time.Millisecond,
		HeartbeatInterval:  50 * time.Millisecond,
	}
	sm := NewStateManager(1, config)

	// Test with higher term (should update)
	sm.BecomeLeader() // Become leader first
	initialTerm := sm.GetCurrentTerm()
	sm.BecomeFollower(initialTerm + 5)

	if sm.GetCurrentTerm() != initialTerm+5 {
		t.Errorf("Term should be %d after update, got %d", initialTerm+5, sm.GetCurrentTerm())
	}
	state, _ := sm.GetState()
	if state != Follower {
		t.Errorf("State should be Follower after term update, got %v", state)
	}
	if sm.GetVotedFor() != nil {
		t.Error("VotedFor should be nil after term update")
	}
}

// TestStateManagerVoting tests voting behavior
func TestStateManagerVoting(t *testing.T) {
	config := &Config{
		ID:                 1,
		ElectionTimeoutMin: 100 * time.Millisecond,
		ElectionTimeoutMax: 200 * time.Millisecond,
		HeartbeatInterval:  50 * time.Millisecond,
	}
	sm := NewStateManager(1, config)

	// Test voting in current term
	sm.SetVotedFor(2)
	if sm.GetVotedFor() == nil || *sm.GetVotedFor() != 2 {
		t.Error("VotedFor should be 2 after voting")
	}

	// Test voting updates with new term
	sm.BecomeFollower(2)
	if sm.GetVotedFor() != nil {
		t.Error("VotedFor should be nil after new term")
	}

	sm.SetVotedFor(3)
	if sm.GetVotedFor() == nil || *sm.GetVotedFor() != 3 {
		t.Error("VotedFor should be 3 after voting in new term")
	}
}

/*
// TestStateManagerTimers tests election timer behavior
func TestStateManagerTimers(t *testing.T) {
	config := &Config{
		ID:                 1,
		ElectionTimeoutMin: 50 * time.Millisecond,
		ElectionTimeoutMax: 100 * time.Millisecond,
		HeartbeatInterval:  25 * time.Millisecond,
	}
	sm := NewStateManager(1, config)

	// Test ResetElectionTimer
	sm.ResetElectionTimer()
	deadline1 := sm.GetElectionDeadline()
	time.Sleep(time.Millisecond * 10)
	sm.ResetElectionTimer()
	deadline2 := sm.GetElectionDeadline()

	if !deadline2.After(deadline1) {
		t.Error("Election deadline should be extended after reset")
	}

	// Test StopElectionTimer
	sm.StopElectionTimer()
	deadline3 := sm.GetElectionDeadline()
	if !deadline3.IsZero() {
		t.Error("Election deadline should be zero after stop")
	}

	// Test heartbeat timer (when leader)
	sm.BecomeLeader()
	heartbeat := sm.GetHeartbeatTimer()
	if heartbeat == nil {
		t.Error("Heartbeat timer should not be nil for leader")
	}

	// Heartbeat timer should be nil for non-leaders
	sm.BecomeFollower(2, nil)
	heartbeat = sm.GetHeartbeatTimer()
	if heartbeat != nil {
		t.Error("Heartbeat timer should be nil for follower")
	}
}
*/

// TestStateManagerLeaderID tests leader ID tracking
func TestStateManagerLeaderID(t *testing.T) {
	config := &Config{
		ID:                 1,
		ElectionTimeoutMin: 100 * time.Millisecond,
		ElectionTimeoutMax: 200 * time.Millisecond,
		HeartbeatInterval:  50 * time.Millisecond,
	}
	sm := NewStateManager(1, config)

	// Initially no leader
	if sm.GetLeaderID() != nil {
		t.Error("Initially should have no leader")
	}

	// Set leader when becoming follower
	leaderID := 2
	sm.BecomeFollower(1)
	sm.SetLeaderID(&leaderID)
	if sm.GetLeaderID() == nil || *sm.GetLeaderID() != 2 {
		t.Error("Leader ID should be 2")
	}

	// Leader ID should be self when leader
	sm.BecomeLeader()
	if sm.GetLeaderID() == nil || *sm.GetLeaderID() != 1 {
		t.Error("Leader ID should be self (1) when leader")
	}

	// Clear leader when becoming candidate
	sm.BecomeCandidate()
	if sm.GetLeaderID() != nil {
		t.Error("Leader ID should be nil when candidate")
	}
}
