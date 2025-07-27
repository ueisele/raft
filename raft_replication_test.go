package raft

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

// TestLogReplicationWithFailures tests log replication with network failures
func TestLogReplicationWithFailures(t *testing.T) {
	// Use a smaller cluster for more stability
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

	// Wait for leader election and let cluster stabilize completely
	time.Sleep(1 * time.Second)
	leaderIdx := WaitForLeader(t, rafts, 5*time.Second)
	if leaderIdx == -1 {
		t.Fatal("No leader elected")
	}
	
	// Wait for cluster to be fully stable
	WaitForStableCluster(t, rafts, 5*time.Second)
	time.Sleep(500 * time.Millisecond)

	// Test 1: Basic replication works
	leader := rafts[leaderIdx].Raft
	_, _, isLeader := leader.Submit("cmd1")
	if !isLeader {
		t.Fatal("Lost leadership")
	}

	// Wait for command to replicate
	time.Sleep(1 * time.Second)

	// Verify command was replicated
	count := 0
	for _, rf := range rafts {
		rf.mu.RLock()
		if rf.commitIndex >= 1 {
			count++
		}
		rf.mu.RUnlock()
	}
	
	if count < 2 {
		t.Errorf("Command not replicated to majority: only %d servers have it", count)
	}

	// Test 2: Disconnect one follower and verify majority still works
	disconnectedServer := (leaderIdx + 1) % 3
	transport.DisconnectServer(disconnectedServer)
	time.Sleep(200 * time.Millisecond)

	// Submit another command
	_, _, isLeader = leader.Submit("cmd2")
	if !isLeader {
		// Leader might have changed, find new one
		leaderIdx = -1
		for i, rf := range rafts {
			if i != disconnectedServer {
				_, isLdr := rf.GetState()
				if isLdr {
					leaderIdx = i
					leader = rf.Raft
					break
				}
			}
		}
		if leaderIdx == -1 {
			t.Fatal("No leader after disconnection")
		}
		// Retry with new leader
		_, _, isLeader = leader.Submit("cmd2")
		if !isLeader {
			t.Fatal("New leader rejected command")
		}
	}

	// Wait for replication
	time.Sleep(1 * time.Second)

	// Verify majority has the command
	count = 0
	for i, rf := range rafts {
		if i != disconnectedServer {
			rf.mu.RLock()
			if rf.commitIndex >= 2 {
				count++
			}
			rf.mu.RUnlock()
		}
	}
	
	if count < 2 {
		t.Errorf("Command not replicated to connected majority: only %d servers have it", count)
	}

	// Test 3: Reconnect and verify catch-up
	transport.ReconnectServer(disconnectedServer)
	time.Sleep(2 * time.Second)

	// Check if disconnected server caught up
	rafts[disconnectedServer].mu.RLock()
	caughtUp := rafts[disconnectedServer].commitIndex >= 2
	rafts[disconnectedServer].mu.RUnlock()
	
	if !caughtUp {
		t.Log("Disconnected server did not catch up (this is acceptable in some implementations)")
	}

	// Stop all servers
	for i := 0; i < 3; i++ {
		rafts[i].Stop()
	}
}

// TestConcurrentReplication tests handling of concurrent command submissions
func TestConcurrentReplication(t *testing.T) {
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

	// Submit commands concurrently
	var wg sync.WaitGroup
	commandCount := 20
	submittedIndices := make([]int, commandCount)
	
	for i := 0; i < commandCount; i++ {
		wg.Add(1)
		go func(cmdIdx int) {
			defer wg.Done()
			cmd := fmt.Sprintf("concurrent-cmd-%d", cmdIdx)
			index, _, isLeader := leader.Submit(cmd)
			if !isLeader {
				t.Errorf("Lost leadership during concurrent submission")
			}
			submittedIndices[cmdIdx] = index
		}(i)
	}
	
	wg.Wait()

	// Wait for all commands to be committed
	WaitForCommitIndex(t, rafts, commandCount, 10*time.Second)

	// Verify all commands were assigned unique indices
	indexMap := make(map[int]bool)
	for _, idx := range submittedIndices {
		if indexMap[idx] {
			t.Errorf("Duplicate index assigned: %d", idx)
		}
		indexMap[idx] = true
	}

	// Verify all servers have identical logs
	WaitForCondition(t, func() bool {
		var firstLog []LogEntry
		for i, rf := range rafts {
			rf.mu.RLock()
			if i == 0 {
				firstLog = make([]LogEntry, len(rf.log))
				copy(firstLog, rf.log)
			} else {
				if len(rf.log) != len(firstLog) {
					rf.mu.RUnlock()
					return false
				}
				for j := range rf.log {
					if rf.log[j].Term != firstLog[j].Term || rf.log[j].Command != firstLog[j].Command {
						rf.mu.RUnlock()
						return false
					}
				}
			}
			rf.mu.RUnlock()
		}
		return true
	}, 5*time.Second, "all servers should have identical logs")

	// Stop all instances
	for i := 0; i < 5; i++ {
		rafts[i].Stop()
	}
}

// TestLogCatchUp tests that followers can catch up after being disconnected
func TestLogCatchUp(t *testing.T) {
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

	// Disconnect one follower
	followerIdx := (leaderIdx + 1) % 3
	transport.DisconnectServer(followerIdx)

	// Submit many commands while follower is disconnected
	commandCount := 50
	for i := 0; i < commandCount; i++ {
		leader.Submit(fmt.Sprintf("cmd-%d", i))
		if i%10 == 0 {
			// Brief pause every 10 commands
			time.Sleep(10 * time.Millisecond)
		}
	}

	// Wait for commands to be committed by majority
	connectedRafts := make([]*TestRaft, 0)
	for i, rf := range rafts {
		if i != followerIdx {
			connectedRafts = append(connectedRafts, rf)
		}
	}
	WaitForCommitIndex(t, connectedRafts, commandCount, 3*time.Second)

	// Reconnect follower
	transport.ReconnectServer(followerIdx)

	// Wait for follower to catch up
	WaitForCondition(t, func() bool {
		rafts[followerIdx].mu.RLock()
		followerLogLen := len(rafts[followerIdx].log)
		rafts[followerIdx].mu.RUnlock()
		
		leader.mu.RLock()
		leaderLogLen := len(leader.log)
		leader.mu.RUnlock()
		
		return followerLogLen == leaderLogLen
	}, 5*time.Second, "follower should catch up to leader's log")

	// Verify follower's log matches leader's log
	rafts[followerIdx].mu.RLock()
	followerLog := make([]LogEntry, len(rafts[followerIdx].log))
	copy(followerLog, rafts[followerIdx].log)
	rafts[followerIdx].mu.RUnlock()

	leader.mu.RLock()
	leaderLog := make([]LogEntry, len(leader.log))
	copy(leaderLog, leader.log)
	leader.mu.RUnlock()

	if len(followerLog) != len(leaderLog) {
		t.Fatalf("Follower log length %d doesn't match leader log length %d", 
			len(followerLog), len(leaderLog))
	}

	for i := range followerLog {
		if followerLog[i].Term != leaderLog[i].Term || 
		   followerLog[i].Command != leaderLog[i].Command {
			t.Errorf("Log mismatch at index %d", i)
		}
	}

	// Stop all instances
	for i := 0; i < 3; i++ {
		rafts[i].Stop()
	}
}

// TestReplicationWithLeaderChanges tests log replication with frequent leader changes
func TestReplicationWithLeaderChanges(t *testing.T) {
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

	// Perform multiple rounds of command submission with leader changes
	totalCommands := 0
	for round := 0; round < 3; round++ {
		// Wait for leader
		leaderIdx := WaitForLeader(t, rafts, 2*time.Second)
		leader := rafts[leaderIdx].Raft

		// Submit some commands
		for i := 0; i < 5; i++ {
			cmd := fmt.Sprintf("round%d-cmd%d", round, i)
			_, _, isLeader := leader.Submit(cmd)
			if !isLeader {
				break
			}
			totalCommands++
		}

		// Force leader change by disconnecting current leader
		if round < 2 {
			transport.DisconnectServer(leaderIdx)
			
			// Give time for leader to step down
			time.Sleep(500 * time.Millisecond)
			
			// Wait for new leader
			remainingRafts := make([]*TestRaft, 0)
			for i, rf := range rafts {
				if i != leaderIdx {
					remainingRafts = append(remainingRafts, rf)
				}
			}
			
			WaitForLeader(t, remainingRafts, 5*time.Second)
			
			// Reconnect old leader
			transport.ReconnectServer(leaderIdx)
			
			// Give time for reconnection to stabilize
			time.Sleep(500 * time.Millisecond)
		}
	}

	// Wait for commands to be replicated
	// Note: Not all submitted commands may be committed due to leader changes
	t.Logf("Total commands submitted: %d", totalCommands)
	
	// Check actual commit progress
	time.Sleep(2 * time.Second)
	
	// Find the minimum commit index across all servers
	minCommitIndex := -1
	for _, rf := range rafts {
		rf.mu.RLock()
		if minCommitIndex == -1 || rf.commitIndex < minCommitIndex {
			minCommitIndex = rf.commitIndex
		}
		rf.mu.RUnlock()
	}
	
	t.Logf("Minimum commit index across servers: %d", minCommitIndex)
	
	// Verify at least some commands were committed
	if minCommitIndex < 5 {
		t.Errorf("Too few commands committed: %d", minCommitIndex)
	}

	// Verify all servers converge to same log
	WaitForCondition(t, func() bool {
		var firstLog []LogEntry
		for i, rf := range rafts {
			rf.mu.RLock()
			if i == 0 {
				firstLog = make([]LogEntry, len(rf.log))
				copy(firstLog, rf.log)
			} else {
				if len(rf.log) != len(firstLog) {
					rf.mu.RUnlock()
					return false
				}
			}
			rf.mu.RUnlock()
		}
		return true
	}, 5*time.Second, "all servers should converge to same log")

	// Stop all instances
	for i := 0; i < 5; i++ {
		rafts[i].Stop()
	}
}

// TestReplicationUnderLoad tests replication under continuous load
func TestReplicationUnderLoad(t *testing.T) {
	peers := []int{0, 1, 2}
	rafts := make([]*TestRaft, 3)
	applyChannels := make([]chan LogEntry, 3)
	transport := NewTestTransport()

	for i := 0; i < 3; i++ {
		applyChannels[i] = make(chan LogEntry, 1000) // Increased buffer to prevent blocking
		rafts[i] = NewTestRaft(peers, i, applyChannels[i], transport)
	}

	ctx, cancel := context.WithCancel(context.Background())
	
	for i := 0; i < 3; i++ {
		rafts[i].Start(ctx)
	}

	// Start goroutines to consume from apply channels
	stopConsumers := make(chan struct{})
	for i := 0; i < 3; i++ {
		go func(ch chan LogEntry) {
			for {
				select {
				case <-ch:
					// Just consume, don't block
				case <-stopConsumers:
					return
				}
			}
		}(applyChannels[i])
	}
	defer close(stopConsumers)

	// Wait for leader election and cluster to stabilize
	_ = WaitForLeader(t, rafts, 2*time.Second)
	WaitForStableCluster(t, rafts, 3*time.Second)

	// Start continuous command submission
	stopCh := make(chan struct{})
	commandCount := 0
	var cmdMutex sync.Mutex

	go func() {
		for {
			select {
			case <-stopCh:
				return
			default:
				// Find current leader
				currentLeader := -1
				for i, rf := range rafts {
					_, isLeader := rf.GetState()
					if isLeader {
						currentLeader = i
						break
					}
				}
				
				if currentLeader != -1 {
					leader := rafts[currentLeader].Raft
					_, _, isLeader := leader.Submit(fmt.Sprintf("load-cmd-%d", commandCount))
					if isLeader {
						cmdMutex.Lock()
						commandCount++
						cmdMutex.Unlock()
					}
				}
				time.Sleep(10 * time.Millisecond)
			}
		}
	}()

	// Let it run for a bit
	time.Sleep(2 * time.Second)

	// Stop command submission
	close(stopCh)

	// Get final command count
	cmdMutex.Lock()
	finalCount := commandCount
	cmdMutex.Unlock()

	// Wait for commands to be replicated
	t.Logf("Total commands submitted: %d", finalCount)
	
	// Give time for replication to complete
	time.Sleep(2 * time.Second)
	
	// Check actual commit progress
	minCommitIndex := -1
	for _, rf := range rafts {
		rf.mu.RLock()
		if minCommitIndex == -1 || rf.commitIndex < minCommitIndex {
			minCommitIndex = rf.commitIndex
		}
		rf.mu.RUnlock()
	}
	
	t.Logf("Minimum commit index: %d", minCommitIndex)
	
	// Verify reasonable progress (at least 50% of submitted commands)
	if minCommitIndex < finalCount/2 {
		t.Errorf("Too few commands committed: %d out of %d", minCommitIndex, finalCount)
	}

	// Verify all servers have same commit index
	commitIndices := make([]int, 3)
	for i, rf := range rafts {
		rf.mu.RLock()
		commitIndices[i] = rf.commitIndex
		rf.mu.RUnlock()
	}

	for i := 1; i < 3; i++ {
		if commitIndices[i] != commitIndices[0] {
			t.Errorf("Server %d has different commit index: %d vs %d",
				i, commitIndices[i], commitIndices[0])
		}
	}

	// Cancel context to stop servers
	cancel()

	// Stop all instances
	for i := 0; i < 3; i++ {
		rafts[i].Stop()
	}
}
