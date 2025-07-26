package raft

import (
	"context"
	"fmt"
	"math/rand"
	"sync"
	"testing"
	"time"
)

// TestElectionSafety tests that at most one leader can be elected in a given term
func TestElectionSafety(t *testing.T) {
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

	// Run for multiple terms to test election safety
	for round := 0; round < 10; round++ {
		// Wait for leader election
		time.Sleep(500 * time.Millisecond)

		// Check election safety: at most one leader per term
		leadersByTerm := make(map[int][]int)
		
		for i, rf := range rafts {
			rf.mu.RLock()
			term := rf.currentTerm
			state := rf.state
			rf.mu.RUnlock()
			
			if state == Leader {
				leadersByTerm[term] = append(leadersByTerm[term], i)
			}
		}

		for term, leaders := range leadersByTerm {
			if len(leaders) > 1 {
				t.Fatalf("Election Safety violated: multiple leaders %v in term %d", leaders, term)
			}
		}

		// Randomly crash the leader to trigger new election
		if len(leadersByTerm) > 0 {
			for _, leaders := range leadersByTerm {
				if len(leaders) > 0 {
					leaderIdx := leaders[0]
					transport.DisconnectServer(leaderIdx)
					time.Sleep(100 * time.Millisecond)
					transport.ReconnectServer(leaderIdx)
				}
			}
		}
	}

	// Stop all instances
	for i := 0; i < 5; i++ {
		rafts[i].Stop()
	}
}

// TestLeaderAppendOnly tests that a leader never overwrites or deletes entries in its log
func TestLeaderAppendOnly(t *testing.T) {
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

	// Wait for leader election
	time.Sleep(1 * time.Second)

	// Find leader and submit commands
	var leader *Raft
	var leaderIdx int
	for i, rf := range rafts {
		_, isLeader := rf.GetState()
		if isLeader {
			leader = rf.Raft
			leaderIdx = i
			break
		}
	}

	if leader == nil {
		t.Fatal("No leader found")
	}

	// Submit commands and track log entries
	commands := []string{"cmd1", "cmd2", "cmd3", "cmd4", "cmd5"}
	var previousLogEntries []LogEntry

	for i, cmd := range commands {
		leader.Submit(cmd)
		time.Sleep(100 * time.Millisecond)

		// Capture current log state
		leader.mu.RLock()
		currentLog := make([]LogEntry, len(leader.log))
		copy(currentLog, leader.log)
		leader.mu.RUnlock()

		// Verify append-only property
		if i > 0 {
			if len(currentLog) < len(previousLogEntries) {
				t.Fatal("Leader Append-Only violated: log length decreased")
			}
			
			// Check that previous entries are unchanged
			for j := 0; j < len(previousLogEntries); j++ {
				if j >= len(currentLog) || currentLog[j] != previousLogEntries[j] {
					t.Fatalf("Leader Append-Only violated: entry at index %d changed", j)
				}
			}
		}

		previousLogEntries = currentLog
	}

	// Test leader change scenario
	transport.DisconnectServer(leaderIdx)
	time.Sleep(2 * time.Second)

	// Find new leader
	var newLeader *Raft
	for i, rf := range rafts {
		if i != leaderIdx {
			_, isLeader := rf.GetState()
			if isLeader {
				newLeader = rf.Raft
				break
			}
		}
	}

	if newLeader != nil {
		// Submit more commands to new leader
		newLeader.Submit("cmd6")
		time.Sleep(500 * time.Millisecond)
		
		// Verify new leader also follows append-only
		newLeader.mu.RLock()
		newLogLen := len(newLeader.log)
		newLeader.mu.RUnlock()
		
		if newLogLen < len(previousLogEntries) {
			t.Fatal("New leader violated append-only property")
		}
	}

	// Stop all instances
	for i := 0; i < 3; i++ {
		rafts[i].Stop()
	}
}

// TestLogMatching tests the Log Matching Property
func TestLogMatching(t *testing.T) {
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

	// Wait for leader election
	time.Sleep(1 * time.Second)

	// Find leader
	var leader *Raft
	for _, rf := range rafts {
		_, isLeader := rf.GetState()
		if isLeader {
			leader = rf.Raft
			break
		}
	}

	if leader == nil {
		t.Fatal("No leader found")
	}

	// Submit commands
	commands := []string{"cmd1", "cmd2", "cmd3", "cmd4", "cmd5"}
	for _, cmd := range commands {
		leader.Submit(cmd)
		time.Sleep(50 * time.Millisecond)
	}

	// Wait for replication
	time.Sleep(2 * time.Second)

	// Verify Log Matching Property
	// If two entries in different logs have the same index and term,
	// then they store the same command and logs are identical up to that point
	serverLogs := make([][]LogEntry, 5)
	for i, rf := range rafts {
		rf.mu.RLock()
		serverLogs[i] = make([]LogEntry, len(rf.log))
		copy(serverLogs[i], rf.log)
		rf.mu.RUnlock()
	}

	// Check log matching across all pairs of servers
	for i := 0; i < 5; i++ {
		for j := i + 1; j < 5; j++ {
			log1, log2 := serverLogs[i], serverLogs[j]
			minLen := len(log1)
			if len(log2) < minLen {
				minLen = len(log2)
			}

			for k := 0; k < minLen; k++ {
				entry1, entry2 := log1[k], log2[k]
				if entry1.Term == entry2.Term && entry1.Index == entry2.Index {
					// Same term and index, must have same command
					if entry1.Command != entry2.Command {
						t.Fatalf("Log Matching violated: servers %d and %d have different commands at index %d", i, j, k)
					}
					
					// All preceding entries must be identical
					for l := 0; l < k; l++ {
						if log1[l] != log2[l] {
							t.Fatalf("Log Matching violated: servers %d and %d differ at index %d", i, j, l)
						}
					}
				}
			}
		}
	}

	// Stop all instances
	for i := 0; i < 5; i++ {
		rafts[i].Stop()
	}
}

// TestStateMachineSafety tests that servers never apply different commands at the same index
func TestStateMachineSafety(t *testing.T) {
	peers := []int{0, 1, 2, 3, 4}
	rafts := make([]*TestRaft, 5)
	applyChannels := make([]chan LogEntry, 5)
	transport := NewTestTransport()

	// Track applied commands by index across all servers
	appliedCommands := make(map[int]map[int]interface{}) // [server][index] -> command
	var appliedMutex sync.Mutex

	for i := 0; i < 5; i++ {
		applyChannels[i] = make(chan LogEntry, 100)
		rafts[i] = NewTestRaft(peers, i, applyChannels[i], transport)
		appliedCommands[i] = make(map[int]interface{})

		// Monitor applied commands for each server
		go func(serverIdx int, applyCh chan LogEntry) {
			for entry := range applyCh {
				appliedMutex.Lock()
				appliedCommands[serverIdx][entry.Index] = entry.Command
				appliedMutex.Unlock()
			}
		}(i, applyChannels[i])
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	for i := 0; i < 5; i++ {
		rafts[i].Start(ctx)
	}

	// Submit commands with network partitions and leader changes
	for round := 0; round < 5; round++ {
		// Wait for leader election
		time.Sleep(1 * time.Second)

		// Find leader
		var leader *Raft
		var leaderIdx int
		for i, rf := range rafts {
			_, isLeader := rf.GetState()
			if isLeader {
				leader = rf.Raft
				leaderIdx = i
				break
			}
		}

		if leader != nil {
			// Submit some commands
			for i := 0; i < 3; i++ {
				cmd := fmt.Sprintf("cmd%d-%d", round, i)
				leader.Submit(cmd)
				time.Sleep(100 * time.Millisecond)
			}

			// Randomly partition the network
			if rand.Float32() < 0.5 {
				// Disconnect the leader
				transport.DisconnectServer(leaderIdx)
				time.Sleep(500 * time.Millisecond)
				transport.ReconnectServer(leaderIdx)
			}
		}
	}

	// Wait for final stabilization
	time.Sleep(3 * time.Second)

	// Check State Machine Safety Property
	appliedMutex.Lock()
	defer appliedMutex.Unlock()

	// For each index, verify all servers that applied a command have the same command
	allIndices := make(map[int]bool)
	for _, serverCommands := range appliedCommands {
		for index := range serverCommands {
			allIndices[index] = true
		}
	}

	for index := range allIndices {
		commands := make(map[interface{}][]int) // command -> list of servers
		
		for serverIdx, serverCommands := range appliedCommands {
			if command, exists := serverCommands[index]; exists {
				commands[command] = append(commands[command], serverIdx)
			}
		}

		if len(commands) > 1 {
			t.Fatalf("State Machine Safety violated at index %d: different commands applied by different servers: %v", index, commands)
		}
	}

	// Stop all instances
	for i := 0; i < 5; i++ {
		rafts[i].Stop()
	}
}

// TestLeaderElectionRestriction tests that candidates with out-of-date logs cannot win elections
func TestLeaderElectionRestriction(t *testing.T) {
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

	// Wait for initial leader election
	time.Sleep(1 * time.Second)

	// Find leader
	var leader *Raft
	var leaderIdx int
	for i, rf := range rafts {
		_, isLeader := rf.GetState()
		if isLeader {
			leader = rf.Raft
			leaderIdx = i
			break
		}
	}

	if leader == nil {
		t.Fatal("No leader found")
	}

	// Disconnect one follower to make its log fall behind
	laggedServerIdx := (leaderIdx + 1) % 3
	transport.DisconnectServer(laggedServerIdx)

	// Submit commands while one server is disconnected
	commands := []string{"cmd1", "cmd2", "cmd3", "cmd4", "cmd5"}
	for _, cmd := range commands {
		leader.Submit(cmd)
		time.Sleep(100 * time.Millisecond)
	}

	// Wait for replication to connected servers
	time.Sleep(1 * time.Second)

	// Now disconnect the leader and reconnect the lagged server
	transport.DisconnectServer(leaderIdx)
	transport.ReconnectServer(laggedServerIdx)

	// Wait for election timeout and new election
	time.Sleep(3 * time.Second)

	// Check that the lagged server did not become leader
	_, isLaggedLeader := rafts[laggedServerIdx].GetState()
	if isLaggedLeader {
		t.Fatal("Election restriction violated: server with out-of-date log became leader")
	}

	// Verify that a server with up-to-date log became leader
	hasNewLeader := false
	for i, rf := range rafts {
		if i != leaderIdx && i != laggedServerIdx {
			_, isLeader := rf.GetState()
			if isLeader {
				hasNewLeader = true
				break
			}
		}
	}

	if !hasNewLeader {
		t.Log("No new leader elected, which is acceptable if majority cannot be formed")
	}

	// Reconnect original leader to allow full recovery
	transport.ReconnectServer(leaderIdx)
	time.Sleep(2 * time.Second)

	// Stop all instances
	for i := 0; i < 3; i++ {
		rafts[i].Stop()
	}
}