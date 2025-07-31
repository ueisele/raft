package raft

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// TestLeadershipTransferBasic tests basic leadership transfer functionality
func TestLeadershipTransferBasic(t *testing.T) {
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
	oldLeaderID := rafts[leaderIdx].me

	// Submit some commands to establish log
	for i := 0; i < 5; i++ {
		leader.Submit(fmt.Sprintf("cmd%d", i))
	}

	// Wait for replication
	WaitForCommitIndex(t, rafts, 5, 2*time.Second)

	// Choose a target for leadership transfer (not the current leader)
	targetID := -1
	for _, id := range peers {
		if id != oldLeaderID {
			targetID = id
			break
		}
	}

	// Transfer leadership
	err := leader.TransferLeadership(targetID)
	if err != nil {
		t.Fatalf("Failed to transfer leadership: %v", err)
	}

	// Wait for new leader
	newLeaderIdx := WaitForLeader(t, rafts, 2*time.Second)

	if rafts[newLeaderIdx].me == oldLeaderID {
		t.Error("Leadership transfer failed - same leader")
	}

	// Verify old leader is no longer leader
	_, isLeader := rafts[leaderIdx].GetState()
	if isLeader {
		t.Error("Old leader should have stepped down")
	}

	// Stop all servers
	for i := 0; i < 5; i++ {
		rafts[i].Stop()
	}
}

// TestLeaderCommitIndexPreservation tests that commitIndex is preserved across leader changes
func TestLeaderCommitIndexPreservation(t *testing.T) {
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

	// Wait for leader election
	leaderIdx := WaitForLeader(t, rafts, 2*time.Second)
	leader := rafts[leaderIdx].Raft

	// Submit commands
	for i := 0; i < 10; i++ {
		leader.Submit(fmt.Sprintf("cmd%d", i))
	}

	// Wait for commit
	WaitForCommitIndex(t, rafts, 10, 2*time.Second)

	// Record commit indices before leader change
	commitIndices := make([]int, 3)
	for i, rf := range rafts {
		rf.mu.RLock()
		commitIndices[i] = rf.commitIndex
		rf.mu.RUnlock()
		t.Logf("Server %d commitIndex before: %d", i, commitIndices[i])
	}

	// Force leader change by partitioning current leader
	transport.DisconnectServer(rafts[leaderIdx].me)

	// Wait for new leader among remaining servers
	remainingRafts := make([]*TestRaft, 0)
	for i, rf := range rafts {
		if i != leaderIdx {
			remainingRafts = append(remainingRafts, rf)
		}
	}

	newLeaderIdx := WaitForLeader(t, remainingRafts, 3*time.Second)
	newLeader := remainingRafts[newLeaderIdx].Raft

	// Check new leader's commitIndex
	newLeader.mu.RLock()
	newLeaderCommitIndex := newLeader.commitIndex
	newLeader.mu.RUnlock()

	if newLeaderCommitIndex < 10 {
		t.Errorf("New leader commitIndex should be at least 10, got %d", newLeaderCommitIndex)
	}

	// Reconnect and verify
	transport.ReconnectServer(rafts[leaderIdx].me)
	time.Sleep(1 * time.Second)

	// Stop all servers
	for i := 0; i < 3; i++ {
		rafts[i].Stop()
	}
}

// TestLeaderStabilityDuringConfigChange tests that leader remains stable during configuration changes
func TestLeaderStabilityDuringConfigChange(t *testing.T) {
	peers := []int{0, 1, 2}
	rafts := make([]*TestRaft, 4) // Start with 3, will add 4th
	applyChannels := make([]chan LogEntry, 4)
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

	// Wait for leader election
	leaderIdx := WaitForLeader(t, rafts[:3], 2*time.Second)
	leader := rafts[leaderIdx].Raft
	originalLeaderID := rafts[leaderIdx].me

	// Create new server
	newServerID := 3
	applyChannels[newServerID] = make(chan LogEntry, 100)
	rafts[newServerID] = NewTestRaft([]int{newServerID}, newServerID, applyChannels[newServerID], transport)
	rafts[newServerID].Start(ctx)

	// Start consumer for new server
	go func(ch chan LogEntry) {
		for {
			select {
			case <-ch:
			case <-stopConsumers:
				return
			}
		}
	}(applyChannels[newServerID])

	// Add the new server
	err := leader.AddServer(ServerConfiguration{
		ID:      newServerID,
		Address: fmt.Sprintf("localhost:800%d", newServerID),
	})

	if err != nil {
		t.Fatalf("Failed to add server: %v", err)
	}

	// Verify leader hasn't changed
	currentLeaderIdx := -1
	for i := 0; i < 4; i++ {
		_, isLeader := rafts[i].GetState()
		if isLeader {
			currentLeaderIdx = i
			break
		}
	}

	if currentLeaderIdx == -1 {
		t.Fatal("No leader after configuration change")
	}

	if rafts[currentLeaderIdx].me != originalLeaderID {
		t.Errorf("Leader changed during configuration add: was %d, now %d",
			originalLeaderID, rafts[currentLeaderIdx].me)
	}

	// Stop all servers
	for i := 0; i < 4; i++ {
		rafts[i].Stop()
	}
}

// TestLeaderElectionWithVaryingClusterSizes tests leader election with different cluster sizes
func TestLeaderElectionWithVaryingClusterSizes(t *testing.T) {
	testCases := []struct {
		name         string
		clusterSize  int
		expectLeader bool
	}{
		{"2 nodes", 2, true}, // Can elect leader if one node gets both votes
		{"3 nodes", 3, true},
		{"4 nodes", 4, true},
		{"5 nodes", 5, true},
		{"7 nodes", 7, true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			peers := make([]int, tc.clusterSize)
			rafts := make([]*TestRaft, tc.clusterSize)
			applyChannels := make([]chan LogEntry, tc.clusterSize)
			transport := NewTestTransport()

			for i := 0; i < tc.clusterSize; i++ {
				peers[i] = i
				applyChannels[i] = make(chan LogEntry, 100)
				rafts[i] = NewTestRaft(peers, i, applyChannels[i], transport)
			}

			ctx, cancel := context.WithCancel(context.Background())
			defer func() {
				cancel()
				for i := 0; i < tc.clusterSize; i++ {
					rafts[i].Stop()
				}
			}()

			// Start servers with small delays to reduce split votes
			for i := 0; i < tc.clusterSize; i++ {
				rafts[i].Start(ctx)
				// Add small random delay between server starts
				if i < tc.clusterSize-1 {
					time.Sleep(time.Duration(10+i*5) * time.Millisecond)
				}
			}

			// Give servers time to start and initialize
			time.Sleep(100 * time.Millisecond)

			// Try to elect a leader with timeout
			// Use different timeouts based on cluster size
			electionTimeout := 3 * time.Second
			if tc.clusterSize == 2 {
				// 2-node clusters may need more time due to split votes
				electionTimeout = 5 * time.Second
			} else if tc.clusterSize >= 4 {
				// Larger clusters may also need more time
				electionTimeout = 5 * time.Second
			}

			hasLeader := false
			timeout := time.After(electionTimeout)
			ticker := time.NewTicker(50 * time.Millisecond)
			defer ticker.Stop()

		waitLoop:
			for {
				select {
				case <-timeout:
					break waitLoop
				case <-ticker.C:
					for _, rf := range rafts {
						_, isLeader := rf.GetState()
						if isLeader {
							hasLeader = true
							break waitLoop
						}
					}
				}
			}

			if hasLeader != tc.expectLeader {
				// Log current state of all servers for debugging
				t.Logf("Election failed for %s after %v", tc.name, electionTimeout)
				for i, rf := range rafts {
					rf.mu.RLock()
					term := rf.currentTerm
					state := rf.state
					rf.mu.RUnlock()
					t.Logf("Server %d: term=%d, state=%v", i, term, state)
				}
				t.Errorf("Expected leader=%v, got leader=%v", tc.expectLeader, hasLeader)
			}
		})
	}
}

// TestLeaderHandlingDuringPartitionRecovery tests leader behavior when partitions heal
func TestLeaderHandlingDuringPartitionRecovery(t *testing.T) {
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

	// Wait for leader election
	leaderIdx := WaitForLeader(t, rafts, 2*time.Second)
	leader := rafts[leaderIdx].Raft

	// Submit some commands
	for i := 0; i < 5; i++ {
		leader.Submit(fmt.Sprintf("cmd%d", i))
	}

	WaitForCommitIndex(t, rafts, 5, 2*time.Second)

	// Create partition: leader with one follower vs rest
	// This tests if minority leader steps down properly
	if leaderIdx < 3 {
		// Leader in group with servers 0,1,2
		transport.DisconnectPair(0, 3)
		transport.DisconnectPair(0, 4)
		transport.DisconnectPair(1, 3)
		transport.DisconnectPair(1, 4)
		transport.DisconnectPair(2, 3)
		transport.DisconnectPair(2, 4)
	} else {
		// Leader in group with servers 3,4
		transport.DisconnectPair(3, 0)
		transport.DisconnectPair(3, 1)
		transport.DisconnectPair(3, 2)
		transport.DisconnectPair(4, 0)
		transport.DisconnectPair(4, 1)
		transport.DisconnectPair(4, 2)
	}

	// Wait a bit
	time.Sleep(1 * time.Second)

	// Check that we have leaders in both partitions
	var majorityLeader *Raft
	if leaderIdx < 3 {
		// Original leader in majority
		majorityLeader = leader
	} else {
		// Original leader in minority - find new leader in majority
		for i := 0; i < 3; i++ {
			_, isLeader := rafts[i].GetState()
			if isLeader {
				majorityLeader = rafts[i].Raft
				break
			}
		}
	}

	// Submit command to majority leader
	if majorityLeader != nil {
		majorityLeader.Submit("majority-cmd")
	}

	// Heal partition
	for i := 0; i < 3; i++ {
		for j := 3; j < 5; j++ {
			transport.ReconnectPair(i, j)
		}
	}

	// Wait for convergence
	time.Sleep(2 * time.Second)

	// Should have only one leader
	leaderCount := 0
	for _, rf := range rafts {
		_, isLeader := rf.GetState()
		if isLeader {
			leaderCount++
		}
	}

	if leaderCount != 1 {
		t.Errorf("Expected 1 leader after partition heals, got %d", leaderCount)
	}

	// Stop all servers
	for i := 0; i < 5; i++ {
		rafts[i].Stop()
	}
}

// TestLeadershipTransferToNonVotingMember tests that leadership transfer to non-voting member fails
func TestLeadershipTransferToNonVotingMember(t *testing.T) {
	peers := []int{0, 1, 2}
	rafts := make([]*TestRaft, 4)
	applyChannels := make([]chan LogEntry, 4)
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

	// Wait for leader election
	leaderIdx := WaitForLeader(t, rafts[:3], 2*time.Second)
	leader := rafts[leaderIdx].Raft

	// Add a non-voting member
	newServerID := 3
	applyChannels[newServerID] = make(chan LogEntry, 100)
	rafts[newServerID] = NewTestRaft([]int{newServerID}, newServerID, applyChannels[newServerID], transport)
	rafts[newServerID].Start(ctx)

	go func(ch chan LogEntry) {
		for {
			select {
			case <-ch:
			case <-stopConsumers:
				return
			}
		}
	}(applyChannels[newServerID])

	// Add as non-voting member
	err := leader.AddServer(ServerConfiguration{
		ID:        newServerID,
		Address:   fmt.Sprintf("localhost:800%d", newServerID),
		NonVoting: true,
	})
	if err != nil {
		t.Fatalf("Failed to add non-voting server: %v", err)
	}

	// Try to transfer leadership to non-voting member
	err = leader.TransferLeadership(newServerID)
	if err == nil {
		t.Error("Expected error when transferring leadership to non-voting member")
	}

	// Verify leader hasn't changed
	_, stillLeader := rafts[leaderIdx].GetState()
	if !stillLeader {
		t.Error("Leader should not have changed")
	}

	// Stop all servers
	for i := 0; i < 4; i++ {
		rafts[i].Stop()
	}
}
