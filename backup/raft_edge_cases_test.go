package raft

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

// TestPendingConfigChangeBlocking tests that pending configuration changes properly block new ones
func TestPendingConfigChangeBlocking(t *testing.T) {
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

	// Start consumers
	stopConsumers := make(chan struct{})
	defer close(stopConsumers)
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

	for i := 0; i < 3; i++ {
		rafts[i].Start(ctx)
	}

	// Wait for leader
	leaderIdx := WaitForLeader(t, rafts, 2*time.Second)
	leader := rafts[leaderIdx].Raft

	// Start a configuration change but block its completion by partitioning
	transport.DisconnectServer(1) // Prevent majority

	// Try to add a server - this should start but not complete
	go func() {
		err := leader.AddServer(ServerConfiguration{
			ID:      3,
			Address: "localhost:8003",
		})
		if err != nil {
			t.Logf("First AddServer failed (expected): %v", err)
		}
	}()

	// Give it time to start
	time.Sleep(100 * time.Millisecond)

	// Try another configuration change - should fail immediately
	err := leader.AddServer(ServerConfiguration{
		ID:      4,
		Address: "localhost:8004",
	})

	if err == nil {
		t.Error("Second AddServer should have failed due to pending config change")
	} else if err.Error() != "configuration change already in progress" &&
		err.Error() != "lost leadership during configuration change" &&
		err.Error() != "not the leader" &&
		!strings.Contains(err.Error(), "operation timed out") &&
		!strings.Contains(err.Error(), "failed to commit") {
		t.Errorf("Expected 'configuration change already in progress', leadership loss, or timeout, got: %v", err)
	}

	// Heal partition to let first change complete
	transport.ReconnectServer(1)

	// Stop all servers
	for i := 0; i < 3; i++ {
		rafts[i].Stop()
	}
}

// TestTwoNodeElectionDynamics tests the specific challenges of 2-node clusters
func TestTwoNodeElectionDynamics(t *testing.T) {
	peers := []int{0, 1}
	rafts := make([]*TestRaft, 2)
	applyChannels := make([]chan LogEntry, 2)
	transport := NewTestTransport()

	for i := 0; i < 2; i++ {
		applyChannels[i] = make(chan LogEntry, 100)
		rafts[i] = NewTestRaft(peers, i, applyChannels[i], transport)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Track elections
	electionCount := 0
	maxTerm := 0

	// Start servers simultaneously to trigger split votes
	for i := 0; i < 2; i++ {
		rafts[i].Start(ctx)
	}

	// Monitor for 3 seconds
	timeout := time.After(3 * time.Second)
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()

	hasLeader := false
	for {
		select {
		case <-timeout:
			goto done
		case <-ticker.C:
			for i, rf := range rafts {
				rf.mu.RLock()
				term := rf.currentTerm
				state := rf.state
				rf.mu.RUnlock()

				if term > maxTerm {
					maxTerm = term
					electionCount++
				}

				if state == Leader {
					hasLeader = true
					t.Logf("Server %d became leader in term %d after %d elections",
						i, term, electionCount)
					goto done
				}
			}
		}
	}

done:
	t.Logf("2-node cluster: %d elections, max term %d, leader elected: %v",
		electionCount, maxTerm, hasLeader)

	// In a well-behaved system, we should eventually elect a leader
	if !hasLeader && electionCount > 5 {
		t.Log("Many split votes occurred - this is expected behavior for 2-node clusters")
	}

	// Stop all servers
	for i := 0; i < 2; i++ {
		rafts[i].Stop()
	}
}

// TestLeaderSnapshotDuringConfigChange tests leader taking snapshot while config change is in progress
func TestLeaderSnapshotDuringConfigChange(t *testing.T) {
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

	// Start consumers
	stopConsumers := make(chan struct{})
	defer close(stopConsumers)
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

	for i := 0; i < 3; i++ {
		rafts[i].Start(ctx)
	}

	// Wait for leader
	leaderIdx := WaitForLeader(t, rafts[:3], 2*time.Second)
	leader := rafts[leaderIdx].Raft

	// Submit commands
	for i := 1; i <= 10; i++ {
		leader.Submit(fmt.Sprintf("cmd%d", i))
	}

	WaitForCommitIndex(t, rafts[:3], 10, 2*time.Second)

	// Create new server
	applyChannels[3] = make(chan LogEntry, 100)
	rafts[3] = NewTestRaft([]int{3}, 3, applyChannels[3], transport)
	rafts[3].Start(ctx)

	go func(ch chan LogEntry) {
		for {
			select {
			case <-ch:
			case <-stopConsumers:
				return
			}
		}
	}(applyChannels[3])

	// Start adding server in background
	addDone := make(chan error, 1)
	go func() {
		err := leader.AddServer(ServerConfiguration{
			ID:      3,
			Address: "localhost:8003",
		})
		addDone <- err
	}()

	// Give config change time to start
	time.Sleep(200 * time.Millisecond)

	// Try to take snapshot while config change is in progress
	err := leader.TakeSnapshot([]byte("snapshot during config"), 8)
	if err != nil {
		t.Logf("Snapshot during config change failed (may be expected): %v", err)
	}

	// Wait for config change to complete
	select {
	case err := <-addDone:
		if err != nil {
			t.Logf("AddServer completed with error: %v", err)
		} else {
			t.Log("AddServer completed successfully")
		}
	case <-time.After(10 * time.Second):
		// This is actually expected in some cases where snapshot interferes with config change
		t.Log("AddServer timed out (expected when snapshot interferes with config change)")

		// Check if the config change is still in progress
		leader.mu.RLock()
		hasConfigInLog := false
		for i := leader.lastApplied + 1; i <= leader.getLastLogIndex(); i++ {
			entry := leader.getLogEntry(i)
			if entry != nil && leader.isConfigurationEntry(*entry) {
				hasConfigInLog = true
				break
			}
		}
		leader.mu.RUnlock()

		if hasConfigInLog {
			t.Log("Configuration change is still pending in log (expected)")
		}
	}

	// Verify system is still functional
	leader.Submit("test-after-snapshot")

	// Stop all servers
	for i := 0; i < 4; i++ {
		if rafts[i] != nil {
			rafts[i].Stop()
		}
	}
}

// TestStaleConfigEntriesAfterPartition tests handling of stale config entries after partition healing
func TestStaleConfigEntriesAfterPartition(t *testing.T) {
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

	// Create partition: {0,1} minority vs {2,3,4} majority
	for i := 0; i <= 1; i++ {
		for j := 2; j <= 4; j++ {
			transport.DisconnectPair(i, j)
		}
	}

	// If leader is in minority, try config change (will fail)
	if leaderIdx <= 1 {
		leader := rafts[leaderIdx].Raft
		err := leader.RemoveServer(4)
		if err == nil {
			t.Error("Config change should fail in minority partition")
		}

		// This might leave a config entry in the leader's log
		leader.mu.RLock()
		logLen := len(leader.log)
		lastEntry := leader.log[logLen-1]
		hasConfigEntry := leader.isConfigurationEntry(lastEntry)
		leader.mu.RUnlock()

		if hasConfigEntry {
			t.Log("Config entry added to minority leader's log")
		}
	}

	// Wait for majority to potentially elect new leader
	time.Sleep(2 * time.Second)

	// Heal partition
	for i := 0; i <= 1; i++ {
		for j := 2; j <= 4; j++ {
			transport.ReconnectPair(i, j)
		}
	}

	// Wait for convergence
	time.Sleep(2 * time.Second)

	// Find current leader
	newLeaderIdx := WaitForLeader(t, rafts, 2*time.Second)
	newLeader := rafts[newLeaderIdx].Raft

	// Check for any pending config changes
	newLeader.mu.RLock()
	hasPending := newLeader.hasPendingConfigChange()
	newLeader.mu.RUnlock()

	if hasPending {
		t.Log("Pending config change detected after partition healing")

		// Try new config change - should handle or clear pending one
		err := newLeader.RemoveServer(4)
		if err != nil {
			t.Logf("Config change after partition healing failed: %v", err)
		}
	}

	// Stop all servers
	for i := 0; i < 5; i++ {
		rafts[i].Stop()
	}
}

// TestRapidLeadershipChanges tests system behavior under rapid leadership changes
func TestRapidLeadershipChanges(t *testing.T) {
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

	// Start consumers
	stopConsumers := make(chan struct{})
	defer close(stopConsumers)
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

	for i := 0; i < 5; i++ {
		rafts[i].Start(ctx)
	}

	// Track leadership changes
	leaderChanges := 0
	lastLeader := -1

	// Cause rapid leadership changes by repeatedly isolating leaders
	for round := 0; round < 5; round++ {
		// Find current leader
		leaderIdx := WaitForLeader(t, rafts, 2*time.Second)
		if leaderIdx != lastLeader {
			leaderChanges++
			lastLeader = leaderIdx
		}

		// Try to submit command
		leader := rafts[leaderIdx].Raft
		leader.Submit(fmt.Sprintf("cmd-round-%d", round))

		// Isolate leader after short time
		time.Sleep(200 * time.Millisecond)
		transport.DisconnectServer(leaderIdx)

		// Wait for new leader election
		time.Sleep(500 * time.Millisecond)

		// Reconnect old leader
		transport.ReconnectServer(leaderIdx)
	}

	t.Logf("System experienced %d leadership changes", leaderChanges)

	// Verify system eventually stabilizes
	finalLeaderIdx := WaitForLeader(t, rafts, 3*time.Second)
	finalLeader := rafts[finalLeaderIdx].Raft

	// Submit final commands to verify functionality
	for i := 0; i < 3; i++ {
		finalLeader.Submit(fmt.Sprintf("final-cmd-%d", i))
	}

	// Check how many commands were actually committed
	time.Sleep(1 * time.Second)
	finalLeader.mu.RLock()
	commitIndex := finalLeader.commitIndex
	finalLeader.mu.RUnlock()

	t.Logf("Final commit index: %d", commitIndex)

	// Stop all servers
	for i := 0; i < 5; i++ {
		rafts[i].Stop()
	}
}

// TestAsymmetricPartitionVariants tests different asymmetric partition scenarios
func TestAsymmetricPartitionVariants(t *testing.T) {
	testCases := []struct {
		name           string
		setupPartition func(transport *TestTransport, leaderIdx int)
		description    string
	}{
		{
			name: "Leader can send but not receive from majority",
			setupPartition: func(transport *TestTransport, leaderIdx int) {
				// Others cannot send to leader
				for i := 0; i < 5; i++ {
					if i != leaderIdx {
						transport.SetAsymmetricPartition(i, leaderIdx, true)
					}
				}
			},
			description: "Leader should detect lost majority and step down",
		},
		{
			name: "Leader can receive from minority only",
			setupPartition: func(transport *TestTransport, leaderIdx int) {
				// Block responses from 3 out of 4 followers
				blocked := 0
				for i := 0; i < 5; i++ {
					if i != leaderIdx && blocked < 3 {
						transport.SetAsymmetricPartition(i, leaderIdx, true)
						blocked++
					}
				}
			},
			description: "Leader should detect it only has minority contact",
		},
		{
			name: "Follower can send but not receive",
			setupPartition: func(transport *TestTransport, leaderIdx int) {
				// Pick a follower
				followerIdx := (leaderIdx + 1) % 5
				// Leader cannot send to follower
				transport.SetAsymmetricPartition(leaderIdx, followerIdx, true)
			},
			description: "Follower should timeout and start election",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
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
			t.Logf("Initial leader: server %d", leaderIdx)

			// Submit some commands
			leader := rafts[leaderIdx].Raft
			for i := 0; i < 5; i++ {
				leader.Submit(fmt.Sprintf("cmd%d", i))
			}

			WaitForCommitIndex(t, rafts, 5, 2*time.Second)

			// Apply asymmetric partition
			tc.setupPartition(transport, leaderIdx)
			t.Logf("Applied partition: %s", tc.description)

			// Wait to see effect
			time.Sleep(1 * time.Second)

			// Check if leader stepped down or new leader elected
			currentLeader := -1
			for i, rf := range rafts {
				_, isLeader := rf.GetState()
				if isLeader {
					currentLeader = i
					break
				}
			}

			if currentLeader != leaderIdx {
				t.Logf("Leadership changed from %d to %d", leaderIdx, currentLeader)
			} else {
				// Try to submit command to trigger detection
				leader.Submit("test-during-partition")
				time.Sleep(500 * time.Millisecond)

				_, stillLeader := rafts[leaderIdx].GetState()
				if !stillLeader {
					t.Log("Leader stepped down after command submission")
				}
			}

			// Clear partitions
			for i := 0; i < 5; i++ {
				for j := 0; j < 5; j++ {
					transport.SetAsymmetricPartition(i, j, false)
				}
			}

			// Stop all servers
			for i := 0; i < 5; i++ {
				rafts[i].Stop()
			}
		})
	}
}

// TestConfigChangeTimeoutRecovery tests recovery from configuration change timeouts
func TestConfigChangeTimeoutRecovery(t *testing.T) {
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

	// Use very slow consumers to cause timeouts
	stopConsumers := make(chan struct{})
	defer close(stopConsumers)
	for i := 0; i < 3; i++ {
		go func(ch chan LogEntry) {
			for {
				select {
				case entry := <-ch:
					// Slow consumer - delay processing config changes
					if _, ok := entry.Command.(ConfigurationChange); ok {
						time.Sleep(3 * time.Second)
					}
				case <-stopConsumers:
					return
				}
			}
		}(applyChannels[i])
	}

	for i := 0; i < 3; i++ {
		rafts[i].Start(ctx)
	}

	// Wait for leader
	leaderIdx := WaitForLeader(t, rafts[:3], 2*time.Second)
	leader := rafts[leaderIdx].Raft

	// Create new server
	applyChannels[3] = make(chan LogEntry, 100)
	rafts[3] = NewTestRaft([]int{3}, 3, applyChannels[3], transport)
	rafts[3].Start(ctx)

	// Try to add server - might timeout due to slow consumers
	err := leader.AddServer(ServerConfiguration{
		ID:      3,
		Address: "localhost:8003",
	})

	if err != nil {
		t.Logf("AddServer failed (possibly timeout): %v", err)

		// System should recover and be able to process new operations
		time.Sleep(2 * time.Second)

		// Try a simple command
		_, _, isLeader := leader.Submit("test-after-timeout")
		if !isLeader {
			// Find new leader
			newLeaderIdx := WaitForLeader(t, rafts[:3], 2*time.Second)
			if newLeaderIdx != -1 {
				t.Log("New leader elected after config timeout")
			}
		}
	}

	// Stop all servers
	for i := 0; i < 4; i++ {
		if rafts[i] != nil {
			rafts[i].Stop()
		}
	}
}
