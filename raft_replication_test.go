package raft

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

// TestLogReplicationWithFailures tests log replication under various failure scenarios
func TestLogReplicationWithFailures(t *testing.T) {
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

	// Test 1: Submit commands with some followers disconnected
	transport.DisconnectServer((leaderIdx + 1) % 5)
	
	commands1 := []string{"cmd1", "cmd2", "cmd3"}
	for _, cmd := range commands1 {
		leader.Submit(cmd)
		time.Sleep(100 * time.Millisecond)
	}

	// Reconnect and wait for catch-up
	transport.ReconnectServer((leaderIdx + 1) % 5)
	time.Sleep(2 * time.Second)

	// Verify all servers have consistent logs
	serverLogs := make([][]LogEntry, 5)
	for i, rf := range rafts {
		rf.mu.RLock()
		serverLogs[i] = make([]LogEntry, len(rf.log))
		copy(serverLogs[i], rf.log)
		rf.mu.RUnlock()
	}

	// Check log consistency
	expectedLength := len(commands1) + 1 // +1 for dummy entry
	for i, log := range serverLogs {
		if len(log) < expectedLength {
			t.Errorf("Server %d has incomplete log: length %d, expected at least %d", i, len(log), expectedLength)
		}
	}

	// Test 2: Network partition
	// Partition the cluster: [0,1] vs [2,3,4]
	majority := []int{2, 3, 4}
	minority := []int{0, 1}
	
	// If leader is in minority, it should step down
	if leaderIdx < 2 {
		// Disconnect minority from majority
		for _, minor := range minority {
			for _, major := range majority {
				transport.DisconnectPair(minor, major)
			}
		}
		
		time.Sleep(3 * time.Second)
		
		// Check that minority leader stepped down
		_, stillLeader := rafts[leaderIdx].GetState()
		if stillLeader {
			t.Error("Leader in minority partition should have stepped down")
		}
		
		// Check that majority partition elected new leader
		majorityHasLeader := false
		for _, serverIdx := range majority {
			_, isLeader := rafts[serverIdx].GetState()
			if isLeader {
				majorityHasLeader = true
				break
			}
		}
		
		if !majorityHasLeader {
			t.Error("Majority partition should have elected a new leader")
		}
	}

	// Heal partition
	for _, minor := range minority {
		for _, major := range majority {
			transport.ReconnectPair(minor, major)
		}
	}

	time.Sleep(2 * time.Second)

	// Stop all instances
	for i := 0; i < 5; i++ {
		rafts[i].Stop()
	}
}

// TestLeaderCompleteness tests that if a log entry is committed in a given term,
// then that entry will be present in the logs of the leaders for all higher-numbered terms
func TestLeaderCompleteness(t *testing.T) {
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

	var committedEntries []LogEntry
	leaderHistory := make(map[int][]LogEntry) // term -> committed entries

	// Run multiple leader elections and track committed entries
	for round := 0; round < 5; round++ {
		// Wait for leader election
		time.Sleep(1 * time.Second)

		// Find current leader
		var leader *Raft
		var leaderIdx int
		var currentTerm int
		
		for i, rf := range rafts {
			term, isLeader := rf.GetState()
			if isLeader {
				leader = rf.Raft
				leaderIdx = i
				currentTerm = term
				break
			}
		}

		if leader == nil {
			continue
		}

		// Submit commands and track what gets committed
		commands := []string{fmt.Sprintf("term%d-cmd1", currentTerm), fmt.Sprintf("term%d-cmd2", currentTerm)}
		
		for _, cmd := range commands {
			index, term, isLeader := leader.Submit(cmd)
			if !isLeader {
				continue
			}
			
			// Wait for potential commitment
			time.Sleep(500 * time.Millisecond)
			
			// Check if entry is committed (simplified - in real implementation would check commitIndex)
			leader.mu.RLock()
			if index <= len(leader.log) && leader.commitIndex >= index {
				entry := LogEntry{Term: term, Index: index, Command: cmd}
				committedEntries = append(committedEntries, entry)
				leaderHistory[currentTerm] = append(leaderHistory[currentTerm], entry)
			}
			leader.mu.RUnlock()
		}

		// Force leader change by disconnecting current leader
		transport.DisconnectServer(leaderIdx)
		time.Sleep(100 * time.Millisecond)
		transport.ReconnectServer(leaderIdx)
	}

	// Wait for final leader election
	time.Sleep(2 * time.Second)

	// Find final leader and verify it has all previously committed entries
	var finalLeader *Raft
	var finalTerm int
	
	for _, rf := range rafts {
		term, isLeader := rf.GetState()
		if isLeader {
			finalLeader = rf.Raft
			finalTerm = term
			break
		}
	}

	if finalLeader != nil {
		finalLeader.mu.RLock()
		finalLog := make([]LogEntry, len(finalLeader.log))
		copy(finalLog, finalLeader.log)
		finalLeader.mu.RUnlock()

		// Check Leader Completeness: final leader should have all committed entries from previous terms
		for term, entries := range leaderHistory {
			if term < finalTerm {
				for _, committedEntry := range entries {
					found := false
					for _, logEntry := range finalLog {
						if logEntry.Term == committedEntry.Term && 
						   logEntry.Index == committedEntry.Index && 
						   logEntry.Command == committedEntry.Command {
							found = true
							break
						}
					}
					if !found {
						t.Fatalf("Leader Completeness violated: final leader missing committed entry %+v from term %d", committedEntry, term)
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

// TestCommitmentFromPreviousTerms tests the restriction that leaders cannot 
// immediately conclude that entries from previous terms are committed
func TestCommitmentFromPreviousTerms(t *testing.T) {
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

	// Find initial leader (S1)
	var leader1 *Raft
	var leader1Idx int
	for i, rf := range rafts {
		_, isLeader := rf.GetState()
		if isLeader {
			leader1 = rf.Raft
			leader1Idx = i
			break
		}
	}

	if leader1 == nil {
		t.Fatal("No initial leader found")
	}

	// Disconnect one follower
	disconnectedIdx := (leader1Idx + 1) % 3
	transport.DisconnectServer(disconnectedIdx)

	// Submit entry that gets replicated to only one follower
	leader1.Submit("entry-from-term-2")
	time.Sleep(500 * time.Millisecond)

	// Crash the leader
	transport.DisconnectServer(leader1Idx)

	// Reconnect the previously disconnected server
	transport.ReconnectServer(disconnectedIdx)

	// Wait for new election - the disconnected server should become leader
	time.Sleep(3 * time.Second)

	var leader2 *Raft
	var leader2Idx int
	for i, rf := range rafts {
		if i != leader1Idx {
			_, isLeader := rf.GetState()
			if isLeader {
				leader2 = rf.Raft
				leader2Idx = i
				break
			}
		}
	}

	if leader2 != nil {
		// New leader submits entry
		leader2.Submit("entry-from-term-3")
		time.Sleep(500 * time.Millisecond)

		// Crash new leader and restore original
		transport.DisconnectServer(leader2Idx)
		transport.ReconnectServer(leader1Idx)

		time.Sleep(3 * time.Second)

		// Find the current leader and verify it doesn't commit previous term entries without current term entries
		time.Sleep(100 * time.Millisecond) // Give time for any pending operations
		
		var currentLeader *Raft
		var currentLeaderIdx int
		for i, rf := range rafts {
			_, isLeader := rf.GetState()
			if isLeader {
				currentLeader = rf.Raft
				currentLeaderIdx = i
				break
			}
		}
		
		if currentLeader == nil {
			t.Log("No leader found, skipping test validation")
			return
		}
		
		currentLeader.mu.RLock()
		currentTerm := currentLeader.currentTerm
		commitIndex := currentLeader.commitIndex
		log := currentLeader.log
		state := currentLeader.state
		currentLeader.mu.RUnlock()
		
		t.Logf("Current leader (server %d): state=%v, term=%d, commitIndex=%d", currentLeaderIdx, state, currentTerm, commitIndex)

		// Verify that old term entries are not immediately committed
		for i := 1; i <= commitIndex && i < len(log); i++ {
			entry := log[i]
			if entry.Term < currentTerm {
				// This is acceptable only if there's also a current term entry committed
				hasCurrentTermCommitted := false
				for j := i + 1; j <= commitIndex && j < len(log); j++ {
					if log[j].Term == currentTerm {
						hasCurrentTermCommitted = true
						break
					}
				}
				if !hasCurrentTermCommitted {
					t.Errorf("Leader committed entry from previous term %d without current term entry", entry.Term)
				}
			}
		}
	}

	// Stop all instances
	for i := 0; i < 3; i++ {
		rafts[i].Stop()
	}
}

// TestLogInconsistencyResolution tests how Raft resolves log inconsistencies
func TestLogInconsistencyResolution(t *testing.T) {
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

	// Create log inconsistencies through network partitions
	// Wait for initial leader
	time.Sleep(1 * time.Second)

	var initialLeader *Raft
	var initialLeaderIdx int
	for i, rf := range rafts {
		_, isLeader := rf.GetState()
		if isLeader {
			initialLeader = rf.Raft
			initialLeaderIdx = i
			break
		}
	}

	if initialLeader == nil {
		t.Fatal("No initial leader found")
	}

	// Create partition: isolate leader with one follower, leave others
	isolatedFollower := (initialLeaderIdx + 1) % 5
	others := []int{}
	for i := 0; i < 5; i++ {
		if i != initialLeaderIdx && i != isolatedFollower {
			others = append(others, i)
		}
	}

	// Isolate leader and one follower
	for _, other := range others {
		transport.DisconnectPair(initialLeaderIdx, other)
		transport.DisconnectPair(isolatedFollower, other)
	}

	// Leader submits entries but they don't get committed (no majority)
	initialLeader.Submit("isolated-cmd1")
	initialLeader.Submit("isolated-cmd2")
	time.Sleep(1 * time.Second)

	// Meanwhile, partition majority elects new leader
	time.Sleep(3 * time.Second)

	var majorityLeader *Raft
	for _, idx := range others {
		_, isLeader := rafts[idx].GetState()
		if isLeader {
			majorityLeader = rafts[idx].Raft
			break
		}
	}

	if majorityLeader != nil {
		// Majority leader submits different entries
		majorityLeader.Submit("majority-cmd1")
		majorityLeader.Submit("majority-cmd2")
		time.Sleep(1 * time.Second)
	}

	// Heal partition
	for _, other := range others {
		transport.ReconnectPair(initialLeaderIdx, other)
		transport.ReconnectPair(isolatedFollower, other)
	}

	// Wait for log consistency to be restored
	time.Sleep(3 * time.Second)

	// Verify all servers eventually have consistent logs
	var finalLogs [][]LogEntry
	for i, rf := range rafts {
		rf.mu.RLock()
		log := make([]LogEntry, len(rf.log))
		copy(log, rf.log)
		rf.mu.RUnlock()
		finalLogs = append(finalLogs, log)
		t.Logf("Server %d final log length: %d", i, len(log))
	}

	// Check that majority's log won (Raft should have resolved inconsistency)
	// All servers should have the same committed entries
	maxCommittedIndex := 0
	for _, rf := range rafts {
		rf.mu.RLock()
		if rf.commitIndex > maxCommittedIndex {
			maxCommittedIndex = rf.commitIndex
		}
		rf.mu.RUnlock()
	}

	// Verify consistency among committed entries
	if maxCommittedIndex > 0 {
		for i := 1; i <= maxCommittedIndex; i++ {
			var referenceEntry LogEntry
			var referenceSet bool
			
			for serverIdx, log := range finalLogs {
				if i < len(log) {
					if !referenceSet {
						referenceEntry = log[i]
						referenceSet = true
					} else if log[i] != referenceEntry {
						t.Fatalf("Log inconsistency not resolved: server %d has different entry at index %d", serverIdx, i)
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

// TestRaftWithConcurrentOperations tests Raft under high concurrency
func TestRaftWithConcurrentOperations(t *testing.T) {
	peers := []int{0, 1, 2, 3, 4}
	rafts := make([]*TestRaft, 5)
	applyChannels := make([]chan LogEntry, 5)
	transport := NewTestTransport()

	for i := 0; i < 5; i++ {
		applyChannels[i] = make(chan LogEntry, 1000)
		rafts[i] = NewTestRaft(peers, i, applyChannels[i], transport)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	for i := 0; i < 5; i++ {
		rafts[i].Start(ctx)
	}

	// Wait for leader election
	time.Sleep(2 * time.Second)

	var wg sync.WaitGroup
	numClients := 10
	commandsPerClient := 20

	// Simulate multiple clients submitting commands concurrently
	for clientId := 0; clientId < numClients; clientId++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			
			for cmdNum := 0; cmdNum < commandsPerClient; cmdNum++ {
				// Find current leader
				var leader *Raft
				for _, rf := range rafts {
					_, isLeader := rf.GetState()
					if isLeader {
						leader = rf.Raft
						break
					}
				}
				
				if leader != nil {
					cmd := fmt.Sprintf("client%d-cmd%d", id, cmdNum)
					leader.Submit(cmd)
				}
				
				// Random small delay to simulate real client behavior
				time.Sleep(time.Duration(10+id*2) * time.Millisecond)
			}
		}(clientId)
	}

	// Concurrently introduce network failures
	go func() {
		for i := 0; i < 5; i++ {
			time.Sleep(2 * time.Second)
			
			// Randomly disconnect/reconnect a server
			serverIdx := i % 5
			transport.DisconnectServer(serverIdx)
			time.Sleep(500 * time.Millisecond)
			transport.ReconnectServer(serverIdx)
		}
	}()

	// Wait for all clients to finish
	wg.Wait()

	// Allow time for final convergence
	time.Sleep(5 * time.Second)

	// Verify system consistency after concurrent operations
	// All servers should eventually have consistent committed entries
	var serverCommitIndices []int
	for i, rf := range rafts {
		rf.mu.RLock()
		commitIndex := rf.commitIndex
		rf.mu.RUnlock()
		serverCommitIndices = append(serverCommitIndices, commitIndex)
		t.Logf("Server %d commit index: %d", i, commitIndex)
	}

	// Check that committed entries are consistent across servers
	minCommitIndex := serverCommitIndices[0]
	for _, commitIndex := range serverCommitIndices[1:] {
		if commitIndex < minCommitIndex {
			minCommitIndex = commitIndex
		}
	}

	if minCommitIndex > 0 {
		var referenceLogs []LogEntry
		rafts[0].mu.RLock()
		for i := 1; i <= minCommitIndex && i < len(rafts[0].log); i++ {
			referenceLogs = append(referenceLogs, rafts[0].log[i])
		}
		rafts[0].mu.RUnlock()

		// Verify all servers have same committed entries
		for serverIdx := 1; serverIdx < 5; serverIdx++ {
			rafts[serverIdx].mu.RLock()
			for i, refEntry := range referenceLogs {
				logIndex := i + 1
				if logIndex < len(rafts[serverIdx].log) {
					if rafts[serverIdx].log[logIndex] != refEntry {
						rafts[serverIdx].mu.RUnlock()
						t.Fatalf("Inconsistent committed entry at index %d between server 0 and server %d", logIndex, serverIdx)
					}
				}
			}
			rafts[serverIdx].mu.RUnlock()
		}
	}

	// Stop all instances
	for i := 0; i < 5; i++ {
		rafts[i].Stop()
	}
}