package raft

import (
	"context"
	"encoding/json"
	"math/rand"
	"testing"
	"time"
)

// TestVoteDenialBasic tests that followers deny votes when they have a current leader
func TestVoteDenialBasic(t *testing.T) {
	// Create a simple test to verify vote denial works
	rf := &Raft{
		currentTerm:        1,
		state:              Follower,
		votedFor:           nil,
		log:                []LogEntry{{Term: 0, Index: 0}},
		lastHeartbeat:      time.Now(), // Just received heartbeat
		electionTimeoutMin: 150 * time.Millisecond,
		electionTimeoutMax: 300 * time.Millisecond,
		rand:               rand.New(rand.NewSource(time.Now().UnixNano())),
		currentConfig: Configuration{
			Servers: []ServerConfiguration{
				{ID: 0, NonVoting: false},
				{ID: 1, NonVoting: false},
			},
		},
	}

	// Try to request vote immediately after heartbeat
	args := &RequestVoteArgs{
		Term:         2,
		CandidateID:  1,
		LastLogIndex: 0,
		LastLogTerm:  0,
	}

	reply := &RequestVoteReply{}
	err := rf.RequestVote(args, reply)
	if err != nil {
		t.Fatalf("RequestVote failed: %v", err)
	}

	// Vote should be denied
	if reply.VoteGranted {
		t.Error("Vote granted despite recent heartbeat from leader")
	}

	// Now simulate time passing without heartbeat
	rf.lastHeartbeat = time.Now().Add(-200 * time.Millisecond)

	// Try again
	args.Term = 3
	reply = &RequestVoteReply{}
	err = rf.RequestVote(args, reply)
	if err != nil {
		t.Fatalf("RequestVote failed: %v", err)
	}

	// Vote should now be granted
	if !reply.VoteGranted {
		t.Error("Vote denied despite no recent heartbeat")
	}
}

// TestNonVotingMemberBasic tests basic non-voting member functionality
func TestNonVotingMemberBasic(t *testing.T) {
	// Test getMajoritySize with non-voting members
	rf := &Raft{
		currentConfig: Configuration{
			Servers: []ServerConfiguration{
				{ID: 0, NonVoting: false},
				{ID: 1, NonVoting: false},
				{ID: 2, NonVoting: false},
				{ID: 3, NonVoting: true}, // Non-voting
			},
		},
	}

	// With 3 voting members, majority should be 2
	majority := rf.getMajoritySize()
	if majority != 2 {
		t.Errorf("Expected majority size 2, got %d", majority)
	}

	// Test isVotingMember
	if !rf.isVotingMember(0) {
		t.Error("Server 0 should be voting member")
	}
	if rf.isVotingMember(3) {
		t.Error("Server 3 should not be voting member")
	}
}

// TestUpdateCommitIndexWithConfigChange tests commit index updates during config changes
func TestUpdateCommitIndexWithConfigChange(t *testing.T) {
	// This tests the bounds checking we added
	rf := &Raft{
		peers:       []int{0, 1, 2},
		me:          0,
		currentTerm: 1,
		state:       Leader,
		log: []LogEntry{
			{Term: 0, Index: 0},
			{Term: 1, Index: 1, Command: "cmd1"},
		},
		commitIndex: 0,
		matchIndex:  []int{1, 0, 0}, // Array might be smaller after config change
		currentConfig: Configuration{
			Servers: []ServerConfiguration{
				{ID: 0, NonVoting: false},
				{ID: 1, NonVoting: false},
			},
		},
		applyCh:             make(chan LogEntry, 10),
		rand:                rand.New(rand.NewSource(time.Now().UnixNano())),
		electionTimeoutMin:  150 * time.Millisecond,
		electionTimeoutMax:  300 * time.Millisecond,
		heartbeatInterval:   50 * time.Millisecond,
		lastMajorityContact: time.Now(), // Leader just started
		lastPeerResponse:    make([]time.Time, 3),
		nextIndex:           []int{2, 2, 2},
	}

	// Initialize response times to simulate recent contact
	for i := range rf.lastPeerResponse {
		rf.lastPeerResponse[i] = time.Now()
	}

	// This should not panic even with mismatched array sizes
	rf.updateCommitIndex()

	// Commit index should remain 0 since we don't have majority
	if rf.commitIndex != 0 {
		t.Errorf("Expected commitIndex 0, got %d", rf.commitIndex)
	}
}

// TestConfigurationChangeIntegration tests configuration changes with the fixed implementation
func TestConfigurationChangeIntegration(t *testing.T) {
	// Create a 3-node cluster
	peers := []int{0, 1, 2}
	rafts := make([]*TestRaft, 3)
	applyChannels := make([]chan LogEntry, 3)
	transport := NewTestTransport()

	for i := 0; i < 3; i++ {
		applyChannels[i] = make(chan LogEntry, 100)
		rafts[i] = NewTestRaft(peers, i, applyChannels[i], transport)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	for i := 0; i < 3; i++ {
		rafts[i].Start(ctx)
	}
	defer func() {
		for i := 0; i < 3; i++ {
			rafts[i].Stop()
		}
	}()

	// Wait for leader election
	leaderIndex := WaitForLeader(t, rafts, 2*time.Second)
	leader := rafts[leaderIndex].Raft

	// Test adding a server
	newServer := ServerConfiguration{
		ID:        3,
		Address:   "localhost:8003",
		NonVoting: true, // Start as non-voting
	}

	// Manually add as non-voting first
	change := ConfigurationChange{
		Type:      AddNonVotingServer,
		Server:    newServer,
		OldConfig: leader.getCurrentConfiguration(),
		NewConfig: Configuration{
			Servers: append(leader.getCurrentConfiguration().Servers, newServer),
		},
	}

	changeData, err := json.Marshal(change)
	if err != nil {
		t.Fatalf("Failed to marshal config change: %v", err)
	}

	_, _, isLeader := leader.Submit(changeData)
	if !isLeader {
		t.Fatal("Lost leadership")
	}

	// Wait for configuration to be committed
	WaitForCondition(t, func() bool {
		leader.mu.Lock()
		defer leader.mu.Unlock()
		return len(leader.currentConfig.Servers) == 4
	}, 1*time.Second, "configuration should be updated")

	// Verify configuration was applied
	config := leader.getCurrentConfiguration()
	if len(config.Servers) != 4 {
		t.Errorf("Expected 4 servers, got %d", len(config.Servers))
	}
}
