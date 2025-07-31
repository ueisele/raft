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
		// Wait for stable cluster with leader
		WaitForStableCluster(t, rafts, 1*time.Second)

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
					// Brief delay for disconnect to take effect
					time.Sleep(50 * time.Millisecond)
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
	leaderIdx := WaitForLeader(t, rafts, 2*time.Second)
	leader := rafts[leaderIdx].Raft

	// Submit commands and track log entries
	commands := []string{"cmd1", "cmd2", "cmd3", "cmd4", "cmd5"}
	var previousLogEntries []LogEntry

	for i, cmd := range commands {
		leader.Submit(cmd)

		// Wait for command to be in log
		WaitForCondition(t, func() bool {
			leader.mu.RLock()
			defer leader.mu.RUnlock()
			return len(leader.log) > len(previousLogEntries)
		}, 500*time.Millisecond, fmt.Sprintf("command %d should be added to log", i))

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

	// Wait for new leader
	WaitForCondition(t, func() bool {
		for i, rf := range rafts {
			if i != leaderIdx {
				_, isLeader := rf.GetState()
				if isLeader {
					return true
				}
			}
		}
		return false
	}, 3*time.Second, "new leader should be elected")

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
		// Get new leader's current log length
		newLeader.mu.RLock()
		initialNewLeaderLogLen := len(newLeader.log)
		newLeader.mu.RUnlock()

		// Submit more commands to new leader
		newLeader.Submit("cmd6")

		// Wait for command to be added
		WaitForCondition(t, func() bool {
			newLeader.mu.RLock()
			defer newLeader.mu.RUnlock()
			return len(newLeader.log) > initialNewLeaderLogLen
		}, 5*time.Second, "new leader should add command")

		// Verify new leader also follows append-only
		newLeader.mu.RLock()
		newLogLen := len(newLeader.log)
		newLeader.mu.RUnlock()

		if newLogLen <= initialNewLeaderLogLen {
			t.Fatal("New leader failed to append new command")
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
	leaderIdx := WaitForLeader(t, rafts, 2*time.Second)
	leader := rafts[leaderIdx].Raft

	// Submit commands
	commands := []string{"cmd1", "cmd2", "cmd3", "cmd4", "cmd5"}
	for _, cmd := range commands {
		leader.Submit(cmd)
	}

	// Wait for replication
	WaitForCommitIndex(t, rafts, len(commands), 3*time.Second)

	// Verify Log Matching Property
	for i := 1; i <= len(commands); i++ {
		// Get entry at index i from each server
		entries := make([]*LogEntry, 0)
		for _, rf := range rafts {
			rf.mu.RLock()
			entry := rf.getLogEntry(i)
			if entry != nil {
				entries = append(entries, entry)
			}
			rf.mu.RUnlock()
		}

		// All entries at the same index should be identical
		if len(entries) > 0 {
			firstEntry := entries[0]
			for j := 1; j < len(entries); j++ {
				if entries[j].Term != firstEntry.Term || entries[j].Command != firstEntry.Command {
					t.Fatalf("Log Matching violated at index %d: entries differ", i)
				}
			}
		}
	}

	// Test with partitions
	// Manually disconnect servers to create partition: {0,1} vs {2,3,4}
	for i := 0; i < 2; i++ {
		for j := 2; j < 5; j++ {
			transport.DisconnectPair(i, j)
		}
	}

	// Submit command to minority partition (should not commit)
	if leaderIdx < 2 {
		leader.Submit("minority-cmd")
	}

	// Wait a bit for any potential replication
	time.Sleep(200 * time.Millisecond)

	// Heal partition by reconnecting
	for i := 0; i < 2; i++ {
		for j := 2; j < 5; j++ {
			transport.ReconnectPair(i, j)
		}
	}

	// Give network time to reconnect
	time.Sleep(200 * time.Millisecond)

	// Wait for cluster to stabilize
	WaitForStableCluster(t, rafts, 5*time.Second)

	// Verify logs still match
	WaitForCondition(t, func() bool {
		// Check if all servers have matching logs
		// First, find the highest log index across all servers
		maxIndex := 0
		for _, rf := range rafts {
			rf.mu.RLock()
			lastIndex := rf.getLastLogIndex()
			if lastIndex > maxIndex {
				maxIndex = lastIndex
			}
			rf.mu.RUnlock()
		}

		// Check that all servers have the same log up to their last index
		for idx := 1; idx <= maxIndex; idx++ {
			var firstEntry *LogEntry
			for _, rf := range rafts {
				rf.mu.RLock()
				entry := rf.getLogEntry(idx)
				if entry != nil {
					if firstEntry == nil {
						firstEntry = entry
					} else if entry.Term != firstEntry.Term || entry.Command != firstEntry.Command {
						rf.mu.RUnlock()
						return false
					}
				}
				rf.mu.RUnlock()
			}
		}
		return true
	}, 10*time.Second, "logs should converge after partition heals")

	// Stop all instances
	for i := 0; i < 5; i++ {
		rafts[i].Stop()
	}
}

// TestStateMachineSafety tests the State Machine Safety property
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
		leaderIdx := WaitForLeader(t, rafts, 2*time.Second)
		leader := rafts[leaderIdx].Raft

		// Submit some commands
		for i := 0; i < 3; i++ {
			cmd := fmt.Sprintf("cmd%d-%d", round, i)
			_, _, isLeader := leader.Submit(cmd)

			// If not leader anymore, find new leader and retry
			if !isLeader {
				leaderIdx = WaitForLeader(t, rafts, 2*time.Second)
				leader = rafts[leaderIdx].Raft
				_, _, isLeader = leader.Submit(cmd)
				if !isLeader {
					// Skip this command if we still can't submit
					continue
				}
			}

			// Wait for command to be added to log
			WaitForCondition(t, func() bool {
				leader.mu.RLock()
				defer leader.mu.RUnlock()
				// Check if command was added
				for _, entry := range leader.log {
					if entry.Command == cmd {
						return true
					}
				}
				return false
			}, 2*time.Second, fmt.Sprintf("command %s should be added", cmd))
		}

		// Randomly partition the network
		if rand.Float32() < 0.5 {
			// Disconnect the leader
			transport.DisconnectServer(leaderIdx)
			// Brief delay for disconnect
			time.Sleep(50 * time.Millisecond)
			transport.ReconnectServer(leaderIdx)
		}
	}

	// Wait for final stabilization
	WaitForStableCluster(t, rafts, 4*time.Second)

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
	leaderIdx := WaitForLeader(t, rafts, 2*time.Second)
	leader := rafts[leaderIdx].Raft

	// Disconnect one follower to make its log fall behind
	laggedServerIdx := (leaderIdx + 1) % 3
	transport.DisconnectServer(laggedServerIdx)

	// Submit commands while one server is disconnected
	commands := []string{"cmd1", "cmd2", "cmd3", "cmd4", "cmd5"}
	for _, cmd := range commands {
		leader.Submit(cmd)
	}

	// Wait for replication to up-to-date servers
	upToDateServers := make([]*TestRaft, 0)
	for i, rf := range rafts {
		if i != laggedServerIdx {
			upToDateServers = append(upToDateServers, rf)
		}
	}
	WaitForCommitIndex(t, upToDateServers, len(commands), 2*time.Second)

	// Now disconnect leader and reconnect lagged server
	transport.DisconnectServer(leaderIdx)
	transport.ReconnectServer(laggedServerIdx)

	// The lagged server should not become leader
	// Wait to see if any election happens
	WaitForCondition(t, func() bool {
		// Check if we have a new leader that's not the lagged server
		for i, rf := range rafts {
			if i != laggedServerIdx {
				_, isLeader := rf.GetState()
				if isLeader {
					return true
				}
			}
		}
		return false
	}, 3*time.Second, "up-to-date server should become leader")

	// Verify the lagged server is not leader
	_, laggedIsLeader := rafts[laggedServerIdx].GetState()
	if laggedIsLeader {
		t.Fatal("Leader Election Restriction violated: out-of-date server became leader")
	}

	// Reconnect all servers
	transport.ReconnectServer(leaderIdx)

	// Wait for cluster to stabilize
	WaitForStableCluster(t, rafts, 3*time.Second)

	// Verify logs eventually converge
	WaitForCondition(t, func() bool {
		// Check all servers have same log length
		firstLen := len(rafts[0].log)
		for _, rf := range rafts[1:] {
			rf.mu.RLock()
			length := len(rf.log)
			rf.mu.RUnlock()
			if length != firstLen {
				return false
			}
		}
		return true
	}, 5*time.Second, "all logs should converge")

	// Stop all instances
	for i := 0; i < 3; i++ {
		rafts[i].Stop()
	}
}
