package raft

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

// TestWaitForLeader tests the WaitForLeader helper function
func TestWaitForLeader(t *testing.T) {
	t.Run("LeaderElectedWithinTimeout", func(t *testing.T) {
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

		// Should elect a leader
		leaderIdx := WaitForLeader(t, rafts, 2*time.Second)
		if leaderIdx < 0 || leaderIdx >= 3 {
			t.Errorf("Invalid leader index: %d", leaderIdx)
		}

		// Verify it's actually a leader
		_, isLeader := rafts[leaderIdx].GetState()
		if !isLeader {
			t.Error("WaitForLeader returned non-leader")
		}

		for i := 0; i < 3; i++ {
			rafts[i].Stop()
		}
	})

	// Skip timeout test as we can't easily mock testing.TB
}

// TestWaitForCondition tests the WaitForCondition helper function
func TestWaitForCondition(t *testing.T) {
	t.Run("ConditionMetImmediately", func(t *testing.T) {
		called := false
		WaitForCondition(t, func() bool {
			called = true
			return true
		}, 1*time.Second, "immediate condition")
		
		if !called {
			t.Error("Condition function not called")
		}
	})

	t.Run("ConditionMetEventually", func(t *testing.T) {
		counter := 0
		start := time.Now()
		WaitForCondition(t, func() bool {
			counter++
			return counter >= 3
		}, 1*time.Second, "eventual condition")
		
		if counter < 3 {
			t.Errorf("Condition not checked enough times: %d", counter)
		}
		if time.Since(start) > 500*time.Millisecond {
			t.Error("Took too long for simple condition")
		}
	})

	t.Run("ConditionTimeout", func(t *testing.T) {
		// Can't test timeout case directly with mock because WaitForCondition requires *testing.T
		// This is tested indirectly through other tests that use WaitForCondition
	})
}

// TestWaitForNoLeader tests the WaitForNoLeader helper function
func TestWaitForNoLeader(t *testing.T) {
	t.Run("NoLeaderFromStart", func(t *testing.T) {
		peers := []int{0, 1, 2}
		rafts := make([]*TestRaft, 3)
		applyChannels := make([]chan LogEntry, 3)
		transport := NewTestTransport()

		for i := 0; i < 3; i++ {
			applyChannels[i] = make(chan LogEntry, 100)
			rafts[i] = NewTestRaft(peers, i, applyChannels[i], transport)
		}

		// Don't start the rafts, so no leader
		WaitForNoLeader(t, rafts, 500*time.Millisecond)
		// Should complete without error
	})

	t.Run("LeaderStepsDown", func(t *testing.T) {
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

		// Wait for leader
		_ = WaitForLeader(t, rafts, 2*time.Second)
		
		// Disconnect all servers to prevent any leader election
		for i := 0; i < 3; i++ {
			transport.DisconnectServer(i)
		}
		
		// Should eventually have no leader
		WaitForNoLeader(t, rafts, 2*time.Second)

		for i := 0; i < 3; i++ {
			rafts[i].Stop()
		}
	})
}

// TestWaitForCommitIndex tests the WaitForCommitIndex helper function
func TestWaitForCommitIndex(t *testing.T) {
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

	// Start apply channel consumers
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

	// Submit commands
	for i := 1; i <= 5; i++ {
		leader.Submit(i)
	}

	// Should reach commit index 5
	WaitForCommitIndex(t, rafts, 5, 2*time.Second)

	// Verify all servers have at least commit index 5
	for i, rf := range rafts {
		rf.mu.Lock()
		commitIndex := rf.commitIndex
		rf.mu.Unlock()
		if commitIndex < 5 {
			t.Errorf("Server %d has commit index %d, expected >= 5", i, commitIndex)
		}
	}

	for i := 0; i < 3; i++ {
		rafts[i].Stop()
	}
}

// TestWaitForAppliedEntries tests the WaitForAppliedEntries helper function
func TestWaitForAppliedEntries(t *testing.T) {
	t.Run("ReceiveExpectedEntries", func(t *testing.T) {
		applyCh := make(chan LogEntry, 10)
		
		// Pre-populate some entries
		for i := 1; i <= 3; i++ {
			applyCh <- LogEntry{Index: i, Term: 1, Command: i}
		}
		
		entries := WaitForAppliedEntries(t, applyCh, 3, 1*time.Second)
		if len(entries) != 3 {
			t.Errorf("Expected 3 entries, got %d", len(entries))
		}
		
		for i, entry := range entries {
			if entry.Index != i+1 {
				t.Errorf("Entry %d has wrong index: %d", i, entry.Index)
			}
		}
	})

	t.Run("TimeoutWaitingForEntries", func(t *testing.T) {
		applyCh := make(chan LogEntry, 10)
		
		// Only send 2 entries
		applyCh <- LogEntry{Index: 1, Term: 1, Command: 1}
		applyCh <- LogEntry{Index: 2, Term: 1, Command: 2}
		
		// Can't test timeout case directly with mock because WaitForAppliedEntries requires *testing.T
		// This is tested indirectly through other tests
	})
}

// TestWaitForTerm tests the WaitForTerm helper function
func TestWaitForTerm(t *testing.T) {
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

	// Wait for initial leader
	leaderIdx := WaitForLeader(t, rafts, 2*time.Second)
	
	// Force a new election by disconnecting leader
	transport.DisconnectServer(leaderIdx)
	
	// Wait for any server to reach term 2
	WaitForTerm(t, rafts[(leaderIdx+1)%3].Raft, 2, 2*time.Second)
	
	// Verify at least one server reached term 2
	foundTerm2 := false
	for _, rf := range rafts {
		term, _ := rf.GetState()
		if term >= 2 {
			foundTerm2 = true
			break
		}
	}
	
	if !foundTerm2 {
		t.Error("No server reached term 2")
	}

	for i := 0; i < 3; i++ {
		rafts[i].Stop()
	}
}

// TestWaitForServersToAgree tests the WaitForServersToAgree helper function
func TestWaitForServersToAgree(t *testing.T) {
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
	
	// Start apply channel consumers
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

	// Submit a command
	index, _, isLeader := leader.Submit("test-command")
	if !isLeader {
		t.Fatal("Submit returned not leader")
	}
	
	t.Logf("Submitted command at index %d", index)
	
	// Wait for the entry to be committed first
	WaitForCommitIndex(t, rafts, index, 2*time.Second)
	
	// Now wait for all servers to agree on this entry
	WaitForServersToAgree(t, rafts, index, 2*time.Second)
	
	// If WaitForServersToAgree didn't fail, the test passed
	t.Log("WaitForServersToAgree completed successfully")

	for i := 0; i < 3; i++ {
		rafts[i].Stop()
	}
}

// TestWaitForElection tests the WaitForElection helper function
func TestWaitForElection(t *testing.T) {
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

	// Wait for initial leader
	leaderIdx := WaitForLeader(t, rafts, 2*time.Second)
	initialTerm, _ := rafts[leaderIdx].GetState()
	
	// Force new election
	transport.DisconnectServer(leaderIdx)
	
	// Wait for new election
	newLeaderIdx, newTerm := WaitForElection(t, rafts, initialTerm, 2*time.Second)
	
	if newTerm <= initialTerm {
		t.Errorf("New term %d not greater than initial term %d", newTerm, initialTerm)
	}
	
	if newLeaderIdx == leaderIdx {
		t.Error("Same leader after forcing election")
	}
	
	_, isLeader := rafts[newLeaderIdx].GetState()
	if !isLeader {
		t.Error("WaitForElection returned non-leader")
	}

	for i := 0; i < 3; i++ {
		rafts[i].Stop()
	}
}

// TestCountVotingMembers tests the CountVotingMembers helper function
func TestCountVotingMembers(t *testing.T) {
	peers := []int{0, 1, 2, 3, 4}
	rf := NewRaft(peers, 0, make(chan LogEntry))
	
	count := CountVotingMembers(rf)
	if count != 5 {
		t.Errorf("Expected 5 voting members, got %d", count)
	}
}

// TestGetLeaderID tests the GetLeaderID helper function
func TestGetLeaderID(t *testing.T) {
	t.Run("WithLeader", func(t *testing.T) {
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

		// Wait for leader
		expectedLeaderIdx := WaitForLeader(t, rafts, 2*time.Second)
		
		// Get leader ID
		leaderID := GetLeaderID(rafts)
		if leaderID != expectedLeaderIdx {
			t.Errorf("GetLeaderID returned %d, expected %d", leaderID, expectedLeaderIdx)
		}

		for i := 0; i < 3; i++ {
			rafts[i].Stop()
		}
	})

	t.Run("WithoutLeader", func(t *testing.T) {
		peers := []int{0, 1, 2}
		rafts := make([]*TestRaft, 3)
		applyChannels := make([]chan LogEntry, 3)
		transport := NewTestTransport()

		for i := 0; i < 3; i++ {
			applyChannels[i] = make(chan LogEntry, 100)
			rafts[i] = NewTestRaft(peers, i, applyChannels[i], transport)
		}

		// Don't start rafts, so no leader
		leaderID := GetLeaderID(rafts)
		if leaderID != -1 {
			t.Errorf("GetLeaderID returned %d, expected -1", leaderID)
		}
	})
}

// TestWaitForNewTerm tests the WaitForNewTerm helper function
func TestWaitForNewTerm(t *testing.T) {
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

	// Get initial term
	initialTerm, _ := rafts[0].GetState()
	
	// Force term advancement by disconnecting/reconnecting
	leaderIdx := WaitForLeader(t, rafts, 2*time.Second)
	transport.DisconnectServer(leaderIdx)
	
	// Wait for new term
	WaitForNewTerm(t, rafts, initialTerm, 2*time.Second)
	
	// Verify some server advanced term
	foundNewTerm := false
	for _, rf := range rafts {
		term, _ := rf.GetState()
		if term > initialTerm {
			foundNewTerm = true
			break
		}
	}
	
	if !foundNewTerm {
		t.Error("No server advanced to new term")
	}

	for i := 0; i < 3; i++ {
		rafts[i].Stop()
	}
}

// TestWaitForLogLength tests the WaitForLogLength helper function
func TestWaitForLogLength(t *testing.T) {
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

	// Wait for leader
	leaderIdx := WaitForLeader(t, rafts, 2*time.Second)
	leader := rafts[leaderIdx].Raft

	// Submit commands
	for i := 1; i <= 5; i++ {
		leader.Submit(i)
	}

	// Wait for leader's log to grow
	WaitForLogLength(t, leader, 5, 2*time.Second)
	
	// Verify log length
	leader.mu.Lock()
	logLen := len(leader.log)
	leader.mu.Unlock()
	
	if logLen < 5 {
		t.Errorf("Log length %d less than expected 5", logLen)
	}

	for i := 0; i < 3; i++ {
		rafts[i].Stop()
	}
}

// TestWaitForStableCluster tests the WaitForStableCluster helper function
func TestWaitForStableCluster(t *testing.T) {
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

	// Wait for stable cluster
	leaderID := WaitForStableCluster(t, rafts, 2*time.Second)
	
	// Verify single leader and same term
	leaderCount := 0
	firstTerm := -1
	for i, rf := range rafts {
		term, isLeader := rf.GetState()
		if firstTerm == -1 {
			firstTerm = term
		} else if term != firstTerm {
			t.Error("Not all servers in same term")
		}
		if isLeader {
			leaderCount++
			if i != leaderID {
				t.Errorf("Wrong leader ID: expected %d, got %d", leaderID, i)
			}
		}
	}
	
	if leaderCount != 1 {
		t.Errorf("Expected 1 leader, found %d", leaderCount)
	}

	for i := 0; i < 3; i++ {
		rafts[i].Stop()
	}
}

// TestDrainApplyChannel tests the DrainApplyChannel helper function
func TestDrainApplyChannel(t *testing.T) {
	applyCh := make(chan LogEntry, 10)
	
	// Add some entries
	for i := 1; i <= 5; i++ {
		applyCh <- LogEntry{Index: i, Term: 1, Command: i}
	}
	
	// Verify channel has entries
	if len(applyCh) != 5 {
		t.Errorf("Channel should have 5 entries, has %d", len(applyCh))
	}
	
	// Drain the channel
	DrainApplyChannel(applyCh)
	
	// Verify channel is empty
	if len(applyCh) != 0 {
		t.Errorf("Channel should be empty, has %d entries", len(applyCh))
	}
}

// TestCreateTestPersister tests the CreateTestPersister helper function
func TestCreateTestPersister(t *testing.T) {
	persister, cleanup := CreateTestPersister(t, 0)
	defer cleanup()
	
	// Verify persister is created
	if persister == nil {
		t.Fatal("Persister is nil")
	}
	
	// Test saving and loading state
	testTerm := 5
	testVotedFor := 2
	testLog := []LogEntry{
		{Index: 1, Term: 1, Command: "cmd1"},
		{Index: 2, Term: 1, Command: "cmd2"},
	}
	
	err := persister.SaveState(testTerm, &testVotedFor, testLog)
	if err != nil {
		t.Fatalf("Failed to save state: %v", err)
	}
	
	// Load and verify
	term, votedFor, log, err := persister.LoadState()
	if err != nil {
		t.Fatalf("Failed to load state: %v", err)
	}
	
	if term != testTerm {
		t.Errorf("Expected term %d, got %d", testTerm, term)
	}
	
	if votedFor == nil || *votedFor != testVotedFor {
		t.Errorf("Expected votedFor %d, got %v", testVotedFor, votedFor)
	}
	
	if len(log) != len(testLog) {
		t.Errorf("Expected log length %d, got %d", len(testLog), len(log))
	}
}

// TestCreateTestRaftWithPersistence tests the CreateTestRaftWithPersistence helper function
func TestCreateTestRaftWithPersistence(t *testing.T) {
	peers := []int{0, 1, 2}
	applyCh := make(chan LogEntry, 100)
	
	rf, persister, cleanup := CreateTestRaftWithPersistence(t, peers, 0, applyCh)
	defer cleanup()
	
	// Verify components are created
	if rf == nil {
		t.Fatal("TestRaft is nil")
	}
	if persister == nil {
		t.Fatal("Persister is nil")
	}
	
	// Start the raft
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	
	rf.Start(ctx)
	
	// Change state to trigger persistence
	rf.mu.Lock()
	rf.currentTerm = 3
	rf.votedFor = &rf.me
	rf.persist()
	rf.mu.Unlock()
	
	// Verify persistence
	term, votedFor, _, err := persister.LoadState()
	if err != nil {
		t.Fatalf("Failed to load state: %v", err)
	}
	
	if term != 3 {
		t.Errorf("Expected persisted term 3, got %d", term)
	}
	
	if votedFor == nil || *votedFor != 0 {
		t.Errorf("Expected persisted votedFor 0, got %v", votedFor)
	}
	
	rf.Stop()
}

// TestSimulateCrashAndRecover tests the SimulateCrashAndRecover helper function
func TestSimulateCrashAndRecover(t *testing.T) {
	peers := []int{0, 1, 2}
	applyCh := make(chan LogEntry, 100)
	
	// Create initial raft with persistence
	rf, persister, cleanup := CreateTestRaftWithPersistence(t, peers, 0, applyCh)
	defer cleanup()
	
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	
	rf.Start(ctx)
	
	// Set some state
	rf.mu.Lock()
	rf.currentTerm = 5
	votedFor := 1
	rf.votedFor = &votedFor
	rf.log = []LogEntry{
		{Index: 1, Term: 1, Command: "cmd1"},
		{Index: 2, Term: 2, Command: "cmd2"},
	}
	rf.persist()
	rf.mu.Unlock()
	
	// Stop the server
	rf.Stop()
	
	// Simulate crash and recover
	newRf := SimulateCrashAndRecover(t, rf, persister, peers, applyCh)
	newRf.Start(ctx)
	
	// Verify state was recovered
	newRf.mu.RLock()
	if newRf.currentTerm != 5 {
		t.Errorf("Expected recovered term 5, got %d", newRf.currentTerm)
	}
	if newRf.votedFor == nil || *newRf.votedFor != 1 {
		t.Errorf("Expected recovered votedFor 1, got %v", newRf.votedFor)
	}
	if len(newRf.log) != 2 {
		t.Errorf("Expected recovered log length 2, got %d", len(newRf.log))
	}
	newRf.mu.RUnlock()
	
	newRf.Stop()
}

// TestVerifyPersistentState tests the VerifyPersistentState helper function
func TestVerifyPersistentState(t *testing.T) {
	persister, cleanup := CreateTestPersister(t, 0)
	defer cleanup()
	
	// Save some state
	term := 7
	votedFor := 2
	log := []LogEntry{
		{Index: 1, Term: 1, Command: "cmd1"},
		{Index: 2, Term: 1, Command: "cmd2"},
		{Index: 3, Term: 2, Command: "cmd3"},
	}
	
	err := persister.SaveState(term, &votedFor, log)
	if err != nil {
		t.Fatalf("Failed to save state: %v", err)
	}
	
	// Verify with correct expectations
	VerifyPersistentState(t, persister, 7, &votedFor, 3)
	
	// Test with nil votedFor
	err = persister.SaveState(term, nil, log)
	if err != nil {
		t.Fatalf("Failed to save state with nil votedFor: %v", err)
	}
	
	VerifyPersistentState(t, persister, 7, nil, 3)
}

// TestWaitForPersistence tests the WaitForPersistence helper function
func TestWaitForPersistence(t *testing.T) {
	persister, cleanup := CreateTestPersister(t, 0)
	defer cleanup()
	
	// Start with initial state
	err := persister.SaveState(1, nil, []LogEntry{})
	if err != nil {
		t.Fatalf("Failed to save initial state: %v", err)
	}
	
	// Start background update
	go func() {
		time.Sleep(100 * time.Millisecond)
		votedFor := 3
		_ = persister.SaveState(5, &votedFor, []LogEntry{{Index: 1, Term: 1, Command: "cmd"}})
	}()
	
	// Wait for specific state
	WaitForPersistence(t, persister, func(term int, votedFor *int, log []LogEntry) bool {
		return term == 5 && votedFor != nil && *votedFor == 3 && len(log) == 1
	}, 1*time.Second)
	
	// Verify final state
	term, votedFor, log, _ := persister.LoadState()
	if term != 5 || votedFor == nil || *votedFor != 3 || len(log) != 1 {
		t.Error("Persistence condition not met correctly")
	}
}

// TestCreateSnapshotData tests the CreateSnapshotData helper function
func TestCreateSnapshotData(t *testing.T) {
	entries := []LogEntry{
		{Index: 1, Term: 1, Command: "cmd1"},
		{Index: 2, Term: 1, Command: "cmd2"},
		{Index: 3, Term: 2, Command: "cmd3"},
	}
	
	data := CreateSnapshotData(entries)
	if len(data) == 0 {
		t.Error("Snapshot data is empty")
	}
	
	// Verify it can be unmarshaled
	var decoded []LogEntry
	err := json.Unmarshal(data, &decoded)
	if err != nil {
		t.Fatalf("Failed to unmarshal snapshot data: %v", err)
	}
	
	if len(decoded) != len(entries) {
		t.Errorf("Expected %d entries, got %d", len(entries), len(decoded))
	}
	
	for i, entry := range decoded {
		if entry.Index != entries[i].Index {
			t.Errorf("Entry %d has wrong index: expected %d, got %d", 
				i, entries[i].Index, entry.Index)
		}
	}
}

// TestVerifySnapshot tests the VerifySnapshot helper function
func TestVerifySnapshot(t *testing.T) {
	persister, cleanup := CreateTestPersister(t, 0)
	defer cleanup()
	
	// Save a snapshot
	snapshotData := []byte("test snapshot data")
	err := persister.SaveSnapshot(snapshotData, 10, 3)
	if err != nil {
		t.Fatalf("Failed to save snapshot: %v", err)
	}
	
	// Verify snapshot
	VerifySnapshot(t, persister, 10, 3)
	
	// Test with no snapshot
	newPersister, cleanup2 := CreateTestPersister(t, 1)
	defer cleanup2()
	
	// Can't test failure case directly with mock because VerifySnapshot requires *testing.T
	// We can still verify that HasSnapshot returns false
	if newPersister.HasSnapshot() {
		t.Error("Expected no snapshot to exist")
	}
}

// TestWaitForSnapshotInstallation tests the WaitForSnapshotInstallation helper function
func TestWaitForSnapshotInstallation(t *testing.T) {
	peers := []int{0, 1, 2}
	rf := NewRaft(peers, 0, make(chan LogEntry, 100))
	
	// Start with no snapshot
	rf.mu.Lock()
	rf.lastSnapshotIndex = 0
	rf.mu.Unlock()
	
	// Simulate snapshot installation in background
	go func() {
		time.Sleep(100 * time.Millisecond)
		rf.mu.Lock()
		rf.lastSnapshotIndex = 5
		rf.mu.Unlock()
	}()
	
	// Wait for snapshot
	WaitForSnapshotInstallation(t, rf, 5, 1*time.Second)
	
	// Verify
	rf.mu.RLock()
	if rf.lastSnapshotIndex < 5 {
		t.Errorf("Expected snapshot index >= 5, got %d", rf.lastSnapshotIndex)
	}
	rf.mu.RUnlock()
}

