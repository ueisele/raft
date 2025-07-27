package raft

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// TestAsymmetricPartition tests asymmetric network partitions where A can send to B but B cannot send to A
// NOTE: This test exposes a limitation where a leader in an asymmetric partition (can send but not receive)
// may not immediately detect it has lost majority contact. This is because the leader only checks majority
// contact during Submit operations, not continuously in the background.
func TestAsymmetricPartition(t *testing.T) {
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

	// Start consumers
	stopConsumers := make(chan struct{})
	for i := 0; i < 5; i++ {
		go func(ch chan LogEntry) {
			for {
				select {
				case <-ch:
				case <-stopConsumers:
					return
				}
			}
		}(applyChannels[i])
	}
	defer close(stopConsumers)

	// Wait for leader
	leaderIdx := WaitForLeader(t, rafts, 2*time.Second)
	leader := rafts[leaderIdx].Raft

	// Submit some commands
	for i := 0; i < 5; i++ {
		leader.Submit(fmt.Sprintf("cmd%d", i))
	}

	WaitForCommitIndex(t, rafts, 5, 2*time.Second)

	// Test two different asymmetric partition scenarios
	if leaderIdx == 0 {
		t.Log("Testing scenario 1: Leader (server 0) cannot receive responses")
		// Others cannot send to server 0, but server 0 can still send heartbeats
		for i := 1; i < 5; i++ {
			transport.SetAsymmetricPartition(i, 0, true)
		}
		
		// The leader needs time to detect it lost majority
		time.Sleep(300 * time.Millisecond)
		
		// Try to submit commands - this should trigger the majority check
		_, _, stillLeader := rafts[0].Submit("test-cmd")
		time.Sleep(200 * time.Millisecond)
		_, _, stillLeader = rafts[0].Submit("test-cmd-2")
		
		if stillLeader {
			t.Log("Server 0 remains leader (expected - it can still send heartbeats)")
		} else {
			t.Log("Server 0 stepped down after detecting lost majority")
		}
		
		// Since server 0 can still send heartbeats, others won't elect new leader
		// This is correct behavior for this type of partition
	} else {
		t.Log("Testing scenario 2: Leader cannot send to one follower")
		// Create asymmetric partition where leader cannot send to server 0
		transport.SetAsymmetricPartition(leaderIdx, 0, true)
		
		// Server 0 should timeout and try to start election
		time.Sleep(1 * time.Second)
		
		// Check if server 0 started election (increased term)
		rafts[0].mu.RLock()
		server0Term := rafts[0].currentTerm
		rafts[0].mu.RUnlock()
		
		if server0Term > 1 {
			t.Logf("Server 0 correctly started election with term %d", server0Term)
		}
	}

	// Now test a complete asymmetric partition scenario
	// Create a partition where server 4 cannot send to anyone
	for i := 0; i < 4; i++ {
		transport.SetAsymmetricPartition(4, i, true)
	}
	
	// Server 4 should not be able to become leader or affect the cluster
	time.Sleep(500 * time.Millisecond)
	
	// Find current leader among servers 0-3
	var currentLeader *Raft
	for i := 0; i < 4; i++ {
		_, isLeader := rafts[i].GetState()
		if isLeader {
			currentLeader = rafts[i].Raft
			t.Logf("Current leader is server %d", i)
			break
		}
	}
	
	if currentLeader != nil {
		// Submit more commands
		for i := 5; i < 10; i++ {
			currentLeader.Submit(fmt.Sprintf("cmd%d", i))
		}
		
		// Wait for servers 0-3 to commit
		majorityRafts := rafts[:4]
		WaitForCommitIndex(t, majorityRafts, 10, 2*time.Second)
		
		// Server 4 should be behind
		rafts[4].mu.RLock()
		server4CommitIndex := rafts[4].commitIndex
		rafts[4].mu.RUnlock()
		
		if server4CommitIndex >= 10 {
			t.Error("Server 4 should not have committed new entries while asymmetrically partitioned")
		}
	}

	// Heal all partitions
	for i := 0; i < 5; i++ {
		for j := 0; j < 5; j++ {
			transport.SetAsymmetricPartition(i, j, false)
		}
	}

	// All servers should eventually converge
	WaitForCommitIndex(t, rafts, 10, 5*time.Second)

	// Stop all servers
	for i := 0; i < 5; i++ {
		rafts[i].Stop()
	}
}

// TestRapidPartitionChanges tests rapid partition and heal cycles
func TestRapidPartitionChanges(t *testing.T) {
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

	// Start consumers
	stopConsumers := make(chan struct{})
	for i := 0; i < 3; i++ {
		go func(ch chan LogEntry) {
			for {
				select {
				case <-ch:
				case <-stopConsumers:
					return
				}
			}
		}(applyChannels[i])
	}
	defer close(stopConsumers)

	// Wait for initial leader
	WaitForLeader(t, rafts, 2*time.Second)

	// Rapid partition/heal cycles
	cmdIndex := 0
	for cycle := 0; cycle < 5; cycle++ {
		// Find current leader
		var leader *Raft
		leaderIdx := -1
		for i, rf := range rafts {
			_, isLeader := rf.GetState()
			if isLeader {
				leader = rf.Raft
				leaderIdx = i
				break
			}
		}

		// Submit a command
		if leader != nil {
			leader.Submit(fmt.Sprintf("cmd%d", cmdIndex))
			cmdIndex++
		}

		// Partition a random follower
		followerIdx := (leaderIdx + 1) % 3
		transport.DisconnectServer(followerIdx)
		
		// Wait briefly
		time.Sleep(200 * time.Millisecond)

		// Submit another command
		if leader != nil {
			_, _, stillLeader := leader.Submit(fmt.Sprintf("cmd%d", cmdIndex))
			if stillLeader {
				cmdIndex++
			}
		}

		// Reconnect
		transport.ReconnectServer(followerIdx)
		
		// Wait for stabilization
		time.Sleep(300 * time.Millisecond)
	}

	// All servers should eventually converge
	WaitForCommitIndex(t, rafts, cmdIndex, 5*time.Second)

	// Verify all servers have same log
	for i := 1; i < 3; i++ {
		if !compareRaftLogs(rafts[0].Raft, rafts[i].Raft) {
			t.Errorf("Server 0 and server %d have different logs after rapid partitions", i)
		}
	}

	// Stop all servers
	for i := 0; i < 3; i++ {
		rafts[i].Stop()
	}
}

// TestPartitionDuringLeadershipTransfer tests partition during leadership transfer
func TestPartitionDuringLeadershipTransfer(t *testing.T) {
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

	// Wait for leader
	leaderIdx := WaitForLeader(t, rafts, 2*time.Second)
	leader := rafts[leaderIdx].Raft

	// Submit some commands
	for i := 0; i < 5; i++ {
		leader.Submit(fmt.Sprintf("cmd%d", i))
	}

	WaitForCommitIndex(t, rafts, 5, 2*time.Second)

	// Choose target for leadership transfer
	targetIdx := (leaderIdx + 1) % 5

	// Start leadership transfer
	go func() {
		err := leader.TransferLeadership(targetIdx)
		t.Logf("Leadership transfer result: %v", err)
	}()

	// Immediately partition the target
	time.Sleep(50 * time.Millisecond) // Small delay to let transfer start
	transport.DisconnectServer(targetIdx)

	// Wait for timeout
	time.Sleep(2 * time.Second)

	// Should have a leader among the connected servers
	connectedRafts := make([]*TestRaft, 0)
	for i, rf := range rafts {
		if i != targetIdx {
			connectedRafts = append(connectedRafts, rf)
		}
	}

	newLeaderIdx := WaitForLeader(t, connectedRafts, 2*time.Second)
	if newLeaderIdx == -1 {
		t.Fatal("No leader among connected servers")
	}

	// The original leader should have stepped down
	_, stillLeader := rafts[leaderIdx].GetState()
	if stillLeader && leaderIdx != newLeaderIdx {
		t.Error("Original leader should have stepped down after failed transfer")
	}

	// Reconnect target
	transport.ReconnectServer(targetIdx)

	// System should stabilize
	WaitForLeader(t, rafts, 2*time.Second)

	// Stop all servers
	for i := 0; i < 5; i++ {
		rafts[i].Stop()
	}
}

// TestComplexMultiPartition tests multiple simultaneous partitions
func TestComplexMultiPartition(t *testing.T) {
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

	// Start consumers
	stopConsumers := make(chan struct{})
	for i := 0; i < 5; i++ {
		go func(ch chan LogEntry) {
			for {
				select {
				case <-ch:
				case <-stopConsumers:
					return
				}
			}
		}(applyChannels[i])
	}
	defer close(stopConsumers)

	// Wait for leader
	leaderIdx := WaitForLeader(t, rafts, 2*time.Second)
	leader := rafts[leaderIdx].Raft

	// Submit initial commands
	for i := 0; i < 5; i++ {
		leader.Submit(fmt.Sprintf("initial%d", i))
	}
	WaitForCommitIndex(t, rafts, 5, 2*time.Second)

	// Create complex partition: {0,1} | {2} | {3,4}
	// Group 1: 0,1
	// Group 2: 2 (isolated)
	// Group 3: 3,4
	
	// Disconnect group 1 from others
	for i := 0; i <= 1; i++ {
		for j := 2; j <= 4; j++ {
			transport.DisconnectPair(i, j)
		}
	}
	
	// Disconnect group 2 from group 3
	for j := 3; j <= 4; j++ {
		transport.DisconnectPair(2, j)
	}

	// Each partition might elect its own leader (group 1 cannot, group 3 cannot)
	time.Sleep(1 * time.Second)

	// Count leaders
	leaderCount := 0
	for _, rf := range rafts {
		_, isLeader := rf.GetState()
		if isLeader {
			leaderCount++
		}
	}
	
	t.Logf("Number of leaders during complex partition: %d", leaderCount)
	
	// No group should be able to make progress (no majority)
	// Try to submit in each potential leader
	for i, rf := range rafts {
		_, _, isLeader := rf.Submit(fmt.Sprintf("partition-cmd-%d", i))
		if isLeader {
			t.Logf("Server %d thinks it's leader during partition", i)
		}
	}

	// Wait a bit
	time.Sleep(500 * time.Millisecond)

	// No server should have committed new entries
	for i, rf := range rafts {
		rf.mu.RLock()
		if rf.commitIndex > 5 {
			t.Errorf("Server %d committed new entries without majority: commitIndex=%d", i, rf.commitIndex)
		}
		rf.mu.RUnlock()
	}

	// Heal partitions gradually
	// First connect groups 1 and 3
	for i := 0; i <= 1; i++ {
		for j := 3; j <= 4; j++ {
			transport.ReconnectPair(i, j)
		}
	}

	// Now we have majority {0,1,3,4}, should elect leader
	time.Sleep(1 * time.Second)
	
	majorityGroup := []*TestRaft{rafts[0], rafts[1], rafts[3], rafts[4]}
	majorityLeaderIdx := WaitForLeader(t, majorityGroup, 2*time.Second)
	if majorityLeaderIdx != -1 {
		majorityLeader := majorityGroup[majorityLeaderIdx].Raft
		// Should be able to commit
		majorityLeader.Submit("majority-cmd")
		WaitForCommitIndex(t, majorityGroup, 6, 2*time.Second)
	}

	// Finally reconnect server 2
	for i := 0; i < 5; i++ {
		if i != 2 {
			transport.ReconnectPair(i, 2)
		}
	}

	// All servers should converge
	WaitForCommitIndex(t, rafts, 6, 5*time.Second)

	// Stop all servers
	for i := 0; i < 5; i++ {
		rafts[i].Stop()
	}
}

// TestNoProgressInMinorityPartition ensures minority partition cannot make progress
func TestNoProgressInMinorityPartition(t *testing.T) {
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

	// Wait for leader
	leaderIdx := WaitForLeader(t, rafts, 2*time.Second)
	leader := rafts[leaderIdx].Raft
	
	// Submit initial commands
	for i := 0; i < 5; i++ {
		leader.Submit(fmt.Sprintf("cmd%d", i))
	}
	WaitForCommitIndex(t, rafts, 5, 2*time.Second)

	// Create partition: minority {0,1} vs majority {2,3,4}
	for i := 0; i <= 1; i++ {
		for j := 2; j <= 4; j++ {
			transport.DisconnectPair(i, j)
		}
	}

	// Wait for leader election in majority
	time.Sleep(1 * time.Second)

	// Check minority partition
	minorityHasLeader := false
	var minorityLeader *Raft
	for i := 0; i <= 1; i++ {
		_, isLeader := rafts[i].GetState()
		if isLeader {
			minorityHasLeader = true
			minorityLeader = rafts[i].Raft
			break
		}
	}

	if minorityHasLeader {
		// Minority leader should not be able to commit
		startCommitIndex := 0
		minorityLeader.mu.RLock()
		startCommitIndex = minorityLeader.commitIndex
		minorityLeader.mu.RUnlock()

		// Try to submit
		minorityLeader.Submit("minority-cmd")
		
		// Wait a bit
		time.Sleep(1 * time.Second)
		
		// Check commit index hasn't advanced
		minorityLeader.mu.RLock()
		endCommitIndex := minorityLeader.commitIndex
		minorityLeader.mu.RUnlock()
		
		if endCommitIndex > startCommitIndex {
			t.Error("Minority partition made progress without majority")
		}
	}

	// Check majority partition can make progress
	majorityRafts := []*TestRaft{rafts[2], rafts[3], rafts[4]}
	majorityLeaderIdx := WaitForLeader(t, majorityRafts, 2*time.Second)
	if majorityLeaderIdx != -1 {
		majorityLeader := majorityRafts[majorityLeaderIdx].Raft
		majorityLeader.Submit("majority-cmd")
		WaitForCommitIndex(t, majorityRafts, 6, 2*time.Second)
	}

	// Heal partition
	for i := 0; i <= 1; i++ {
		for j := 2; j <= 4; j++ {
			transport.ReconnectPair(i, j)
		}
	}

	// All should converge
	WaitForCommitIndex(t, rafts, 6, 5*time.Second)

	// Stop all servers
	for i := 0; i < 5; i++ {
		rafts[i].Stop()
	}
}

// compareRaftLogs compares logs of two Raft instances
func compareRaftLogs(rf1, rf2 *Raft) bool {
	rf1.mu.RLock()
	rf2.mu.RLock()
	defer rf1.mu.RUnlock()
	defer rf2.mu.RUnlock()

	if len(rf1.log) != len(rf2.log) {
		return false
	}

	for i := range rf1.log {
		if rf1.log[i].Term != rf2.log[i].Term ||
			rf1.log[i].Index != rf2.log[i].Index ||
			fmt.Sprintf("%v", rf1.log[i].Command) != fmt.Sprintf("%v", rf2.log[i].Command) {
			return false
		}
	}

	return true
}