package raft

import (
	"testing"
	"time"
)

// electionTestTransport wraps MockTransport for election-specific test behavior
type electionTestTransport struct {
	*MockTransport
	responses map[int]*RequestVoteReply
	errors    map[int]error
}

func newElectionTestTransport(serverID int) *electionTestTransport {
	mt := &electionTestTransport{
		MockTransport: NewMockTransport(serverID),
		responses:     make(map[int]*RequestVoteReply),
		errors:        make(map[int]error),
	}
	
	// Set up request vote handler
	mt.SetRequestVoteHandler(func(sid int, args *RequestVoteArgs) (*RequestVoteReply, error) {
		if err, ok := mt.errors[sid]; ok {
			return nil, err
		}
		
		if reply, ok := mt.responses[sid]; ok {
			return reply, nil
		}
		
		// Default response - vote not granted
		return &RequestVoteReply{Term: args.Term, VoteGranted: false}, nil
	})
	
	return mt
}

// TestElectionManagerStartElection tests starting an election
func TestElectionManagerStartElection(t *testing.T) {
	// Create config
	config := &Config{
		ID:                 1,
		Peers:              []int{1, 2, 3, 4, 5},
		ElectionTimeoutMin: time.Millisecond * 100,
		ElectionTimeoutMax: time.Millisecond * 200,
		HeartbeatInterval:  time.Millisecond * 50,
	}

	// Create dependencies
	state := NewStateManager(1, config)
	log := NewLogManager()
	transport := newElectionTestTransport(1)

	// Add some log entries
	log.AppendEntries(0, 0, []LogEntry{
		{Term: 1, Index: 1, Command: "cmd1"},
		{Term: 1, Index: 2, Command: "cmd2"},
	})

	// Create election manager
	em := NewElectionManager(config.ID, config.Peers, state, log, transport, config)

	// Set up responses - grant votes from majority
	// StartElection will increment term to 1, so responses should be for term 1
	transport.responses[2] = &RequestVoteReply{Term: 1, VoteGranted: true}
	transport.responses[3] = &RequestVoteReply{Term: 1, VoteGranted: true}
	transport.responses[4] = &RequestVoteReply{Term: 1, VoteGranted: false}
	transport.responses[5] = &RequestVoteReply{Term: 1, VoteGranted: false}

	// Start election
	won := em.StartElection()

	// Should win with 3/5 votes (self + 2 others)
	if !won {
		t.Error("Should have won election with majority votes")
	}

	// Note: StartElection doesn't transition to Leader - caller should do that
	// The state should still be Candidate
	currentState, _ := state.GetState()
	if currentState != Candidate {
		t.Error("Should still be candidate after election (caller transitions to leader)")
	}

	// Verify RequestVote was sent to peers (might not send to all if won early)
	if len(transport.GetRequestVoteCalls()) < 2 || len(transport.GetRequestVoteCalls()) > 4 {
		t.Errorf("Expected 2-4 RequestVote calls, got %d", len(transport.GetRequestVoteCalls()))
	}

	// Verify RequestVote arguments
	for _, call := range transport.GetRequestVoteCalls() {
		if call.Args.Term != 1 {
			t.Errorf("RequestVote term should be 1, got %d", call.Args.Term)
		}
		if call.Args.CandidateID != 1 {
			t.Errorf("RequestVote candidate ID should be 1, got %d", call.Args.CandidateID)
		}
		if call.Args.LastLogIndex != 2 {
			t.Errorf("RequestVote last log index should be 2, got %d", call.Args.LastLogIndex)
		}
		if call.Args.LastLogTerm != 1 {
			t.Errorf("RequestVote last log term should be 1, got %d", call.Args.LastLogTerm)
		}
	}
}

// TestElectionManagerLoseElection tests losing an election
func TestElectionManagerLoseElection(t *testing.T) {
	// Create config
	config := &Config{
		ID:                 1,
		Peers:              []int{1, 2, 3, 4, 5},
		ElectionTimeoutMin: time.Millisecond * 100,
		ElectionTimeoutMax: time.Millisecond * 200,
		HeartbeatInterval:  time.Millisecond * 50,
	}

	// Create dependencies
	state := NewStateManager(1, config)
	log := NewLogManager()
	transport := newElectionTestTransport(1)

	// Create election manager
	em := NewElectionManager(config.ID, config.Peers, state, log, transport, config)

	// Set up responses - only get 1 vote (not enough for majority)
	transport.responses[2] = &RequestVoteReply{Term: 1, VoteGranted: true}
	transport.responses[3] = &RequestVoteReply{Term: 1, VoteGranted: false}
	transport.responses[4] = &RequestVoteReply{Term: 1, VoteGranted: false}
	transport.responses[5] = &RequestVoteReply{Term: 1, VoteGranted: false}

	// Start election
	won := em.StartElection()

	// Should lose with only 2/5 votes (self + 1 other)
	if won {
		t.Error("Should have lost election without majority votes")
	}

	// Should remain candidate after losing
	currentState, _ := state.GetState()
	if currentState != Candidate {
		t.Error("Should remain candidate after losing election")
	}
}

// TestElectionManagerHigherTerm tests discovering higher term during election
func TestElectionManagerHigherTerm(t *testing.T) {
	// Create config
	config := &Config{
		ID:                 1,
		Peers:              []int{1, 2, 3},
		ElectionTimeoutMin: time.Millisecond * 100,
		ElectionTimeoutMax: time.Millisecond * 200,
		HeartbeatInterval:  time.Millisecond * 50,
	}

	// Create dependencies
	state := NewStateManager(1, config)
	log := NewLogManager()
	transport := newElectionTestTransport(1)

	// Create election manager
	em := NewElectionManager(config.ID, config.Peers, state, log, transport, config)

	// Set up response with higher term
	// Make sure both peers return higher term to ensure we see it
	transport.responses[2] = &RequestVoteReply{Term: 5, VoteGranted: false}
	transport.responses[3] = &RequestVoteReply{Term: 5, VoteGranted: false}

	// Start election
	won := em.StartElection()

	// Should lose election
	if won {
		t.Error("Should lose election when discovering higher term")
	}

	// Should update to higher term and become follower
	if state.GetCurrentTerm() != 5 {
		t.Errorf("Should update to term 5, got %d", state.GetCurrentTerm())
	}
	currentState, _ := state.GetState()
	if currentState != Follower {
		t.Error("Should become follower when discovering higher term")
	}
}

// TestElectionManagerHandleRequestVote tests handling vote requests
func TestElectionManagerHandleRequestVote(t *testing.T) {
	// Create config
	config := &Config{
		ID:                 1,
		Peers:              []int{1, 2, 3},
		ElectionTimeoutMin: time.Millisecond * 100,
		ElectionTimeoutMax: time.Millisecond * 200,
		HeartbeatInterval:  time.Millisecond * 50,
	}

	// Create dependencies
	state := NewStateManager(1, config)
	log := NewLogManager()
	transport := newElectionTestTransport(1)

	// Add some log entries
	log.AppendEntries(0, 0, []LogEntry{
		{Term: 1, Index: 1, Command: "cmd1"},
		{Term: 2, Index: 2, Command: "cmd2"},
	})

	// Create election manager
	em := NewElectionManager(config.ID, config.Peers, state, log, transport, config)

	// Test 1: Vote for candidate with up-to-date log
	args := &RequestVoteArgs{
		Term:         3,
		CandidateID:  2,
		LastLogIndex: 2,
		LastLogTerm:  2,
	}
	reply := &RequestVoteReply{}

	em.HandleRequestVote(args, reply)

	if !reply.VoteGranted {
		t.Error("Should grant vote to candidate with up-to-date log")
	}
	if reply.Term != 3 {
		t.Errorf("Reply term should be 3, got %d", reply.Term)
	}
	if state.GetCurrentTerm() != 3 {
		t.Error("Should update to candidate's term")
	}

	// Test 2: Reject vote for candidate with outdated log
	args2 := &RequestVoteArgs{
		Term:         3,
		CandidateID:  3,
		LastLogIndex: 1,
		LastLogTerm:  1,
	}
	reply2 := &RequestVoteReply{}

	em.HandleRequestVote(args2, reply2)

	if reply2.VoteGranted {
		t.Error("Should not grant vote to candidate with outdated log")
	}

	// Test 3: Reject vote from old term
	args3 := &RequestVoteArgs{
		Term:         2,
		CandidateID:  3,
		LastLogIndex: 2,
		LastLogTerm:  2,
	}
	reply3 := &RequestVoteReply{}

	em.HandleRequestVote(args3, reply3)

	if reply3.VoteGranted {
		t.Error("Should not grant vote from old term")
	}
	if reply3.Term != 3 {
		t.Errorf("Reply should contain current term 3, got %d", reply3.Term)
	}
}

// TestElectionManagerLogComparison tests log comparison for elections
func TestElectionManagerLogComparison(t *testing.T) {
	// Create config
	config := &Config{
		ID:                 1,
		Peers:              []int{1, 2, 3},
		ElectionTimeoutMin: time.Millisecond * 100,
		ElectionTimeoutMax: time.Millisecond * 200,
		HeartbeatInterval:  time.Millisecond * 50,
	}

	// Create dependencies
	state := NewStateManager(1, config)
	log := NewLogManager()
	transport := newElectionTestTransport(1)

	// Create election manager
	em := NewElectionManager(config.ID, config.Peers, state, log, transport, config)

	// Test various log comparison scenarios
	testCases := []struct {
		name               string
		localLog           []LogEntry
		candidateLastIndex int
		candidateLastTerm  int
		shouldGrant        bool
	}{
		{
			name:               "Empty logs",
			localLog:           []LogEntry{},
			candidateLastIndex: 0,
			candidateLastTerm:  0,
			shouldGrant:        true,
		},
		{
			name: "Candidate has longer log",
			localLog: []LogEntry{
				{Term: 1, Index: 1},
			},
			candidateLastIndex: 2,
			candidateLastTerm:  1,
			shouldGrant:        true,
		},
		{
			name: "Same length, candidate has higher term",
			localLog: []LogEntry{
				{Term: 1, Index: 1},
				{Term: 1, Index: 2},
			},
			candidateLastIndex: 2,
			candidateLastTerm:  2,
			shouldGrant:        true,
		},
		{
			name: "Candidate has shorter log",
			localLog: []LogEntry{
				{Term: 1, Index: 1},
				{Term: 2, Index: 2},
			},
			candidateLastIndex: 1,
			candidateLastTerm:  1,
			shouldGrant:        false,
		},
		{
			name: "Same length, candidate has lower term",
			localLog: []LogEntry{
				{Term: 1, Index: 1},
				{Term: 2, Index: 2},
			},
			candidateLastIndex: 2,
			candidateLastTerm:  1,
			shouldGrant:        false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Reset log
			log = NewLogManager()
			if len(tc.localLog) > 0 {
				log.AppendEntries(0, 0, tc.localLog)
			}
			em.logManager = log

			// Reset state for new term
			state.SetTerm(3)

			args := &RequestVoteArgs{
				Term:         3,
				CandidateID:  2,
				LastLogIndex: tc.candidateLastIndex,
				LastLogTerm:  tc.candidateLastTerm,
			}
			reply := &RequestVoteReply{}

			em.HandleRequestVote(args, reply)

			if reply.VoteGranted != tc.shouldGrant {
				t.Errorf("Expected vote granted = %v, got %v", tc.shouldGrant, reply.VoteGranted)
			}
		})
	}
}
