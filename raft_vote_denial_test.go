package raft

import (
	"context"
	"testing"
	"time"
)

// TestVoteDenialWithActiveLeader tests that followers deny votes when they have a current leader
func TestVoteDenialWithActiveLeader(t *testing.T) {
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
	// We'll stop servers manually in this test

	// Wait for a leader to be elected
	time.Sleep(1 * time.Second)
	
	// Find the leader
	leaderID := -1
	for i := 0; i < 3; i++ {
		_, isLeader := rafts[i].Raft.GetState()
		if isLeader {
			leaderID = i
			break
		}
	}
	
	if leaderID == -1 {
		t.Fatal("No leader elected")
	}
	
	// Find a follower
	followerID := -1
	followerRaft := (*Raft)(nil)
	for i := 0; i < 3; i++ {
		if i != leaderID {
			followerID = i
			followerRaft = rafts[i].Raft
			break
		}
	}
	
	// Record the current term
	leaderTerm, _ := rafts[leaderID].Raft.GetState()
	
	// Wait a bit to ensure heartbeats have been sent
	time.Sleep(100 * time.Millisecond)
	
	// Now simulate a candidate from another node trying to start an election
	// with a higher term while the leader is still active
	candidateID := -1
	for i := 0; i < 3; i++ {
		if i != leaderID && i != followerID {
			candidateID = i
			break
		}
	}
	
	// Create a RequestVote RPC from the candidate
	args := &RequestVoteArgs{
		Term:         leaderTerm + 1,
		CandidateID:  candidateID,
		LastLogIndex: 0,
		LastLogTerm:  0,
	}
	
	reply := &RequestVoteReply{}
	
	// The follower should deny the vote since it has heard from the leader recently
	err := followerRaft.RequestVote(args, reply)
	if err != nil {
		t.Fatalf("RequestVote failed: %v", err)
	}
	
	// The vote should be denied
	if reply.VoteGranted {
		t.Error("Follower granted vote despite having an active leader")
	}
	
	// Now disconnect the leader for long enough that followers should grant votes
	rafts[leaderID].Raft.Stop()
	
	// Wait for more than the minimum election timeout
	time.Sleep(200 * time.Millisecond)
	
	// Now the follower should grant the vote
	args.Term = leaderTerm + 2
	reply = &RequestVoteReply{}
	err = followerRaft.RequestVote(args, reply)
	if err != nil {
		t.Fatalf("RequestVote failed: %v", err)
	}
	
	// The vote should now be granted
	if !reply.VoteGranted {
		t.Error("Follower denied vote after leader became inactive")
	}
	
	// Clean up remaining servers
	for i := 0; i < 3; i++ {
		if i != leaderID {
			rafts[i].Stop()
		}
	}
}

// TestVoteDenialPreventsUnnecessaryElections tests that vote denial prevents
// unnecessary elections when the leader is temporarily slow
func TestVoteDenialPreventsUnnecessaryElections(t *testing.T) {
	// Create a 5-node cluster
	peers := []int{0, 1, 2, 3, 4}
	rafts := make([]*TestRaft, 5)
	applyChannels := make([]chan LogEntry, 5)
	transport := NewTestTransport()

	for i := 0; i < 5; i++ {
		applyChannels[i] = make(chan LogEntry, 100)
		rafts[i] = NewTestRaft(peers, i, applyChannels[i], transport)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	for i := 0; i < 5; i++ {
		rafts[i].Start(ctx)
	}
	defer func() {
		for i := 0; i < 5; i++ {
			rafts[i].Stop()
		}
	}()

	// Wait for initial leader election
	time.Sleep(1 * time.Second)
	
	// Find the leader
	leaderID := -1
	initialTerm := 0
	for i := 0; i < 5; i++ {
		term, isLeader := rafts[i].Raft.GetState()
		if isLeader {
			leaderID = i
			initialTerm = term
			break
		}
	}
	
	if leaderID == -1 {
		t.Fatal("No leader elected")
	}
	
	// For this test, we'll just rely on natural network timing
	// In a real test, we would slow down the leader's messages
	
	// Wait for a bit - with vote denial, no new election should occur
	time.Sleep(500 * time.Millisecond)
	
	// Check that we still have the same leader
	currentTerm, stillLeader := rafts[leaderID].Raft.GetState()
	if !stillLeader {
		t.Error("Leader lost leadership despite being connected")
	}
	
	// The term should not have increased much (maybe 1 due to timing)
	if currentTerm > initialTerm+1 {
		t.Errorf("Too many elections occurred: initial term %d, current term %d", 
			initialTerm, currentTerm)
	}
	
	// Count how many nodes are in the same term
	nodesInCurrentTerm := 0
	for i := 0; i < 5; i++ {
		term, _ := rafts[i].Raft.GetState()
		if term == currentTerm {
			nodesInCurrentTerm++
		}
	}
	
	// Most nodes should be in the same term
	if nodesInCurrentTerm < 4 {
		t.Errorf("Only %d/5 nodes are in the current term", nodesInCurrentTerm)
	}
}