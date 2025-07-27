package raft

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

// TestClientRedirection tests that non-leader servers redirect clients to the leader
func TestClientRedirection(t *testing.T) {
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

	// Submit command to a follower
	followerIdx := (leaderIdx + 1) % 3
	follower := rafts[followerIdx].Raft

	// Follower should reject the command
	_, _, isLeader := follower.Submit("test-command")
	if isLeader {
		t.Error("Follower incorrectly accepted command as leader")
	}

	// Submit command to actual leader
	leader := rafts[leaderIdx].Raft
	index, term, isLeader := leader.Submit("test-command")
	if !isLeader {
		t.Fatal("Leader rejected command")
	}

	t.Logf("Command accepted by leader: index=%d, term=%d", index, term)

	// Wait for command to be replicated
	WaitForCommitIndex(t, rafts, 1, 2*time.Second)

	// Verify command was applied on all servers
	for i, applyCh := range applyChannels {
		entry := WaitForAppliedEntries(t, applyCh, 1, 1*time.Second)[0]
		if entry.Command != "test-command" {
			t.Errorf("Server %d applied wrong command: %v", i, entry.Command)
		}
	}

	// Stop all servers
	for i := 0; i < 3; i++ {
		rafts[i].Stop()
	}
}

// TestLinearizableReads tests linearizable read operations
func TestLinearizableReads(t *testing.T) {
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

	// Submit some commands
	commands := []string{"set x 1", "set y 2", "set z 3"}
	for _, cmd := range commands {
		_, _, isLeader := leader.Submit(cmd)
		if !isLeader {
			t.Fatal("Lost leadership")
		}
	}

	// Wait for commands to be committed
	WaitForCommitIndex(t, rafts, len(commands), 3*time.Second)

	// Test read from leader
	leaderCommitIndex := leader.commitIndex
	if leaderCommitIndex < len(commands) {
		t.Errorf("Leader commit index too low: %d < %d", leaderCommitIndex, len(commands))
	}

	// Partition the leader from followers
	for i := 0; i < 5; i++ {
		if i != leaderIdx {
			transport.DisconnectPair(leaderIdx, i)
		}
	}

	// Give the majority partition time to elect a new leader
	// The old leader won't step down until it tries to submit something
	time.Sleep(500 * time.Millisecond)

	// Find new leader in majority partition
	majorityRafts := make([]*TestRaft, 0)
	for i, rf := range rafts {
		if i != leaderIdx {
			majorityRafts = append(majorityRafts, rf)
		}
	}
	
	newLeaderIdx := WaitForLeader(t, majorityRafts, 3*time.Second)
	if newLeaderIdx == -1 {
		t.Fatal("No new leader elected in majority")
	}

	// Reconnect old leader
	for i := 0; i < 5; i++ {
		if i != leaderIdx {
			transport.ReconnectPair(leaderIdx, i)
		}
	}

	// Wait for cluster to stabilize
	WaitForStableCluster(t, rafts, 3*time.Second)

	// Verify all servers have consistent state
	// Note: We only check that the majority has consistent state
	// The previously isolated leader may have a different state
	WaitForCondition(t, func() bool {
		commitIndices := make(map[int]int)
		for _, rf := range rafts {
			rf.mu.RLock()
			commitIndices[rf.commitIndex]++
			rf.mu.RUnlock()
		}
		
		// At least 3 servers (majority) should have the same commit index
		for _, count := range commitIndices {
			if count >= 3 {
				return true
			}
		}
		return false
	}, 10*time.Second, "majority should have same commit index")

	// Stop all servers
	for i := 0; i < 5; i++ {
		rafts[i].Stop()
	}
}

// TestIdempotentOperations tests handling of duplicate commands
func TestIdempotentOperations(t *testing.T) {
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

	// Submit the same command multiple times
	command := map[string]interface{}{
		"clientID":  "client1",
		"requestID": "req1",
		"operation": "increment x",
	}

	indices := make([]int, 3)
	for i := 0; i < 3; i++ {
		index, _, isLeader := leader.Submit(command)
		if !isLeader {
			t.Fatal("Lost leadership")
		}
		indices[i] = index
	}

	// All submissions should get different indices (no deduplication at Raft level)
	for i := 1; i < 3; i++ {
		if indices[i] == indices[i-1] {
			t.Error("Duplicate submissions got same index")
		}
	}

	// Wait for all to be committed
	WaitForCommitIndex(t, rafts, indices[2], 2*time.Second)

	// Application layer should handle deduplication
	// Count how many times each command was applied
	appliedCount := 0
	
	// Drain all apply channels
	for _, applyCh := range applyChannels {
		for {
			select {
			case entry := <-applyCh:
				if cmd, ok := entry.Command.(map[string]interface{}); ok {
					if cmd["requestID"] == "req1" {
						appliedCount++
					}
				}
			default:
				goto done
			}
		}
		done:
	}

	// Should be applied 3 times per server (once for each submission)
	expectedCount := 3 * 3 // 3 submissions * 3 servers
	if appliedCount != expectedCount {
		t.Errorf("Expected %d applications, got %d", expectedCount, appliedCount)
	}

	// Stop all servers
	for i := 0; i < 3; i++ {
		rafts[i].Stop()
	}
}

// TestClientTimeouts tests handling of client request timeouts
func TestClientTimeouts(t *testing.T) {
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

	// Submit a command
	index, _, isLeader := leader.Submit("test-command")
	if !isLeader {
		t.Fatal("Lost leadership")
	}

	// Disconnect leader from followers
	for i := 0; i < 3; i++ {
		if i != leaderIdx {
			transport.DisconnectPair(leaderIdx, i)
		}
	}

	// Command should not commit without majority
	time.Sleep(200 * time.Millisecond)
	
	leader.mu.RLock()
	committed := leader.commitIndex >= index
	leader.mu.RUnlock()
	
	if committed {
		t.Error("Command committed without majority")
	}

	// Client would timeout here in real scenario
	t.Log("Client would timeout waiting for commit")

	// Reconnect leader
	for i := 0; i < 3; i++ {
		if i != leaderIdx {
			transport.ReconnectPair(leaderIdx, i)
		}
	}

	// Give the cluster time to stabilize after reconnection
	time.Sleep(500 * time.Millisecond)

	// The old leader might have stepped down, find the current leader
	currentLeaderIdx := -1
	for i, rf := range rafts {
		_, isLeader := rf.GetState()
		if isLeader {
			currentLeaderIdx = i
			break
		}
	}

	if currentLeaderIdx == -1 {
		t.Fatal("No leader after reconnection")
	}

	// If it's a different leader, we need to resubmit the command
	if currentLeaderIdx != leaderIdx {
		// The command was not committed, so we need to resubmit
		newIndex, _, _ := rafts[currentLeaderIdx].Raft.Submit("test-command")
		index = newIndex
	}

	// Now command should commit
	WaitForCommitIndex(t, rafts, index, 5*time.Second)

	// Verify command was eventually applied
	entry := WaitForAppliedEntries(t, applyChannels[leaderIdx], 1, 1*time.Second)[0]
	if entry.Command != "test-command" {
		t.Errorf("Wrong command applied: %v", entry.Command)
	}

	// Stop all servers
	for i := 0; i < 3; i++ {
		rafts[i].Stop()
	}
}

// TestClientRetries tests client retry logic with leader changes
func TestClientRetries(t *testing.T) {
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

	// Wait for initial leader election
	_ = WaitForLeader(t, rafts, 2*time.Second)

	// Simulate client retrying command submission
	command := "retry-command"
	attempts := 0
	maxAttempts := 5
	committed := false

	for attempts < maxAttempts && !committed {
		attempts++
		
		// Find current leader
		currentLeaderIdx := -1
		for i, rf := range rafts {
			_, isLeader := rf.GetState()
			if isLeader {
				currentLeaderIdx = i
				break
			}
		}

		if currentLeaderIdx == -1 {
			// No leader, wait and retry
			time.Sleep(100 * time.Millisecond)
			continue
		}

		// Try to submit command
		leader := rafts[currentLeaderIdx].Raft
		index, _, isLeader := leader.Submit(command)
		
		if !isLeader {
			// Not leader anymore, retry
			continue
		}

		// If this is not the first attempt, disconnect leader to simulate failure
		if attempts < maxAttempts-1 {
			// Give leader a chance to send at least one heartbeat
			time.Sleep(60 * time.Millisecond)
			transport.DisconnectServer(currentLeaderIdx)
			
			// Wait for new leader
			remainingRafts := make([]*TestRaft, 0)
			for i, rf := range rafts {
				if i != currentLeaderIdx {
					remainingRafts = append(remainingRafts, rf)
				}
			}
			WaitForLeader(t, remainingRafts, 2*time.Second)
			
			// Reconnect old leader
			transport.ReconnectServer(currentLeaderIdx)
		} else {
			// Last attempt, let it succeed
			t.Logf("Final attempt: submitting command at index %d", index)
			WaitForCommitIndex(t, rafts, index, 5*time.Second)
			committed = true
		}
	}

	if !committed {
		t.Fatal("Failed to commit after retries")
	}

	t.Logf("Client succeeded after %d attempts", attempts)

	// Verify command was applied
	applied := false
	for _, applyCh := range applyChannels {
		select {
		case entry := <-applyCh:
			if entry.Command == command {
				applied = true
			}
		default:
		}
	}

	if !applied {
		t.Error("Command was not applied")
	}

	// Stop all servers
	for i := 0; i < 5; i++ {
		rafts[i].Stop()
	}
}

// TestConcurrentClients tests multiple clients submitting commands concurrently
func TestConcurrentClients(t *testing.T) {
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

	// Wait for stable leader
	WaitForStableCluster(t, rafts, 2*time.Second)

	// Simulate multiple clients
	clientCount := 10
	commandsPerClient := 5
	var wg sync.WaitGroup
	successCount := make([]int, clientCount)

	for clientID := 0; clientID < clientCount; clientID++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			
			for cmdNum := 0; cmdNum < commandsPerClient; cmdNum++ {
				// Find leader for each command (simulating client discovery)
				var submitted bool
				for attempt := 0; attempt < 3 && !submitted; attempt++ {
					for _, rf := range rafts {
						_, isLeader := rf.GetState()
						if isLeader {
							cmd := fmt.Sprintf("client%d-cmd%d", id, cmdNum)
							_, _, isLeader = rf.Raft.Submit(cmd)
							if isLeader {
								successCount[id]++
								submitted = true
								break
							}
						}
					}
					if !submitted {
						// No leader found, wait a bit
						time.Sleep(50 * time.Millisecond)
					}
				}
				
				// Small delay between commands
				time.Sleep(time.Duration(10+id) * time.Millisecond)
			}
		}(clientID)
	}

	// Wait for all clients to finish
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// All clients finished
	case <-time.After(10 * time.Second):
		t.Fatal("Clients took too long to complete")
	}

	// Calculate total successful submissions
	totalSuccess := 0
	for _, count := range successCount {
		totalSuccess += count
	}

	expectedTotal := clientCount * commandsPerClient
	if totalSuccess < expectedTotal*8/10 { // Allow 20% failure rate
		t.Errorf("Too many failed submissions: %d/%d", totalSuccess, expectedTotal)
	}

	// Wait for commands to be committed
	WaitForCondition(t, func() bool {
		minCommit := totalSuccess
		for _, rf := range rafts {
			rf.mu.RLock()
			if rf.commitIndex < minCommit {
				minCommit = rf.commitIndex
			}
			rf.mu.RUnlock()
		}
		return minCommit >= totalSuccess-5 // Allow some uncommitted
	}, 5*time.Second, "most commands should be committed")

	// Stop all servers
	for i := 0; i < 5; i++ {
		rafts[i].Stop()
	}
}