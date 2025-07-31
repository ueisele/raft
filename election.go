package raft

import (
	"sync"
	"time"
)

// ElectionManager handles leader election logic
// Implements Section 5.2 of the Raft paper
type ElectionManager struct {
	mu       sync.Mutex
	serverID int
	peers    []int

	// Dependencies
	state      *StateManager
	logManager *LogManager
	transport  Transport
	config     *Config

	// Election state
	votesReceived   map[int]bool
	electionStarted time.Time
	
	// Heartbeat tracking for vote denial optimization
	lastHeartbeat   time.Time
}

// NewElectionManager creates a new election manager
func NewElectionManager(serverID int, peers []int, state *StateManager, logManager *LogManager, transport Transport, config *Config) *ElectionManager {
	return &ElectionManager{
		serverID:   serverID,
		peers:      peers,
		state:      state,
		logManager: logManager,
		transport:  transport,
		config:     config,
	}
}

// StartElection starts a new election
// Called when election timeout fires
// Returns true if won the election
func (em *ElectionManager) StartElection() bool {
	em.mu.Lock()

	// Transition to candidate and increment term
	currentTerm := em.state.BecomeCandidate()

	// Vote for self
	em.state.Vote(em.serverID)
	em.votesReceived = map[int]bool{em.serverID: true}
	em.electionStarted = time.Now()

	// Log election start
	if em.config.Logger != nil {
		em.config.Logger.Info("Server %d starting election for term %d", em.serverID, currentTerm)
	}

	lastIndex := em.logManager.GetLastIndex()
	lastTerm := em.logManager.GetLastTerm()
	em.mu.Unlock()

	// Request votes from all other servers in parallel
	votesReceived := 1 // Already voted for self
	votesNeeded := (len(em.peers) / 2) + 1

	// Check if already have enough votes (single node case)
	if votesReceived >= votesNeeded {
		return true
	}

	// Use a channel to collect vote results
	type voteResult struct {
		peer    int
		granted bool
		term    int
	}
	resultChan := make(chan voteResult, len(em.peers))

	if em.config.Logger != nil {
		em.config.Logger.Debug("Starting election with peers: %v", em.peers)
	}
	
	for _, peerID := range em.peers {
		if peerID == em.serverID {
			continue
		}

		go func(peer int) {
			args := &RequestVoteArgs{
				Term:         currentTerm,
				CandidateID:  em.serverID,
				LastLogIndex: lastIndex,
				LastLogTerm:  lastTerm,
			}

			reply, err := em.transport.SendRequestVote(peer, args)
			if err != nil {
				if em.config.Logger != nil {
					em.config.Logger.Debug("Failed to send RequestVote to %d: %v", peer, err)
				}
				resultChan <- voteResult{peer: peer, granted: false, term: currentTerm}
				return
			}

			resultChan <- voteResult{peer: peer, granted: reply.VoteGranted, term: reply.Term}
		}(peerID)
	}

	// Collect votes with timeout
	timer := time.NewTimer(em.config.ElectionTimeoutMin)
	defer timer.Stop()
	
	for i := 0; i < len(em.peers)-1; i++ {
		select {
		case result := <-resultChan:
			// Check if we discovered a higher term
			if result.term > currentTerm {
				em.state.SetTerm(result.term)
				return false
			}

			// Count vote if granted
			if result.granted && result.term == currentTerm {
				votesReceived++
				if votesReceived >= votesNeeded {
					// Won the election!
					return true
				}
			}

		case <-timer.C:
			// Election timeout - lost election
			return false
		}
	}

	// Did not receive enough votes
	return false
}

// HandleRequestVote handles incoming RequestVote RPC
func (em *ElectionManager) HandleRequestVote(args *RequestVoteArgs, reply *RequestVoteReply) {
	state, currentTerm := em.state.GetState()
	reply.Term = currentTerm
	reply.VoteGranted = false

	// Reply false if term < currentTerm (5.1)
	if args.Term < currentTerm {
		return
	}

	// If RPC request or response contains term T > currentTerm:
	// set currentTerm = T, convert to follower (5.1)
	if args.Term > currentTerm {
		em.state.SetTerm(args.Term)
		currentTerm = args.Term
		reply.Term = currentTerm
	}

	// Check if we can grant vote
	votedFor := em.state.GetVotedFor()
	canVote := votedFor == nil || *votedFor == args.CandidateID

	// Check if candidate's log is at least as up-to-date as receiver's log (5.4)
	logIsUpToDate := em.logManager.IsUpToDate(args.LastLogIndex, args.LastLogTerm)

	if canVote && logIsUpToDate {
		// Additional check: if we've heard from current leader recently, don't vote
		// This helps prevent unnecessary elections when the network is slow
		if state == Follower && em.shouldDenyVote() {
			if em.config.Logger != nil {
				em.config.Logger.Debug("Denying vote to %d - have current leader", args.CandidateID)
			}
			return
		}

		reply.VoteGranted = true
		em.state.Vote(args.CandidateID)
		em.state.ResetElectionTimer()

		if em.config.Logger != nil {
			em.config.Logger.Debug("Granted vote to %d for term %d", args.CandidateID, args.Term)
		}
	}
}

// handleVoteReply processes a vote reply
func (em *ElectionManager) handleVoteReply(peer int, args *RequestVoteArgs, reply *RequestVoteReply, votesChan chan<- bool) {
	em.mu.Lock()
	defer em.mu.Unlock()

	state, currentTerm := em.state.GetState()

	// Check if reply contains higher term
	if reply.Term > currentTerm {
		em.state.SetTerm(reply.Term)
		return
	}

	// Only process if we're still a candidate in the same term
	if state != Candidate || args.Term != currentTerm {
		return
	}

	if reply.VoteGranted {
		em.votesReceived[peer] = true
		votesChan <- true

		if em.config.Logger != nil {
			em.config.Logger.Debug("Received vote from %d", peer)
		}
	}
}

// countVotes counts votes and transitions to leader if won
func (em *ElectionManager) countVotes(electionTerm int, votesChan <-chan bool) {
	votes := 1 // Already voted for self
	needed := (len(em.peers) / 2) + 1

	for range votesChan {
		votes++

		// Check if we have majority
		if votes >= needed {
			em.mu.Lock()
			state, currentTerm := em.state.GetState()

			// Verify we're still candidate in the same term
			if state == Candidate && currentTerm == electionTerm {
				// Won the election!
				em.state.BecomeLeader()

				if em.config.Logger != nil {
					em.config.Logger.Info("Server %d won election for term %d with %d votes",
						em.serverID, electionTerm, votes)
				}

				// Record election metrics
				if em.config.Metrics != nil {
					em.config.Metrics.RecordElection(true, time.Since(em.electionStarted))
				}
			}
			em.mu.Unlock()
			return
		}
	}

	// Lost election (split vote or not enough votes)
	if em.config.Logger != nil {
		em.config.Logger.Debug("Server %d lost election for term %d with %d votes (needed %d)",
			em.serverID, electionTerm, votes, needed)
	}

	if em.config.Metrics != nil {
		em.config.Metrics.RecordElection(false, time.Since(em.electionStarted))
	}
}

// shouldDenyVote checks if we should deny a vote because we have a current leader
// This implements the optimization mentioned at the end of Section 6
func (em *ElectionManager) shouldDenyVote() bool {
	// Check if we've received a recent heartbeat from a leader
	// If so, we should deny votes to prevent disrupting a working cluster
	heartbeatTimeout := em.config.ElectionTimeoutMin / 2
	return time.Since(em.lastHeartbeat) < heartbeatTimeout
}

// RecordHeartbeat records that we received a heartbeat from the leader
func (em *ElectionManager) RecordHeartbeat() {
	em.mu.Lock()
	defer em.mu.Unlock()
	em.lastHeartbeat = time.Now()
}

// UpdatePeers updates the peer list (used for configuration changes)
func (em *ElectionManager) UpdatePeers(peers []int) {
	em.mu.Lock()
	defer em.mu.Unlock()
	em.peers = peers
}
