package raft

import (
	"encoding/json"
	"os"
	"testing"
	"time"
)

// WaitForLeader waits for a leader to be elected in the cluster or times out
func WaitForLeader(t testing.TB, rafts []*TestRaft, timeout time.Duration) int {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		for i, rf := range rafts {
			_, isLeader := rf.Raft.GetState()
			if isLeader {
				return i
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("No leader elected within timeout")
	return -1
}

// WaitForCondition waits for a condition to become true or times out
func WaitForCondition(t *testing.T, condition func() bool, timeout time.Duration, msg string) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("Condition not met within timeout: %s", msg)
}

// WaitForNoLeader waits until there is no leader in the cluster
func WaitForNoLeader(t *testing.T, rafts []*TestRaft, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		hasLeader := false
		for _, rf := range rafts {
			_, isLeader := rf.Raft.GetState()
			if isLeader {
				hasLeader = true
				break
			}
		}
		if !hasLeader {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("Still has leader after timeout")
}

// WaitForCommitIndex waits for all servers to reach at least the given commit index
func WaitForCommitIndex(t *testing.T, rafts []*TestRaft, minIndex int, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		allReached := true
		for _, rf := range rafts {
			rf.Raft.mu.Lock()
			commitIndex := rf.Raft.commitIndex
			rf.Raft.mu.Unlock()
			if commitIndex < minIndex {
				allReached = false
				break
			}
		}
		if allReached {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("Not all servers reached commit index %d within timeout", minIndex)
}

// WaitForAppliedEntries waits for entries to be applied to the state machine
func WaitForAppliedEntries(t *testing.T, applyCh chan LogEntry, expected int, timeout time.Duration) []LogEntry {
	var entries []LogEntry
	deadline := time.Now().Add(timeout)

	for len(entries) < expected && time.Now().Before(deadline) {
		select {
		case entry := <-applyCh:
			entries = append(entries, entry)
		case <-time.After(10 * time.Millisecond):
			// Check again
		}
	}

	if len(entries) < expected {
		t.Fatalf("Only received %d entries, expected %d", len(entries), expected)
	}
	return entries
}

// WaitForTerm waits for a server to reach at least the given term
func WaitForTerm(t *testing.T, rf *Raft, minTerm int, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		term, _ := rf.GetState()
		if term >= minTerm {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	currentTerm, _ := rf.GetState()
	t.Fatalf("Server did not reach term %d (current: %d) within timeout", minTerm, currentTerm)
}

// WaitForServersToAgree waits for all servers to have the same log at the given index
func WaitForServersToAgree(t *testing.T, rafts []*TestRaft, index int, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		allAgree := true
		var firstEntry *LogEntry

		for i, rf := range rafts {
			rf.Raft.mu.Lock()
			if index <= 0 || index > len(rf.Raft.log) {
				rf.Raft.mu.Unlock()
				allAgree = false
				break
			}
			entry := rf.Raft.log[index-1]
			rf.Raft.mu.Unlock()

			if i == 0 {
				firstEntry = &entry
			} else if entry.Term != firstEntry.Term || entry.Command != firstEntry.Command {
				allAgree = false
				break
			}
		}

		if allAgree && firstEntry != nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("Servers did not agree on log entry at index %d within timeout", index)
}

// WaitForElection waits for an election to complete (new term with a leader)
func WaitForElection(t *testing.T, rafts []*TestRaft, oldTerm int, timeout time.Duration) (int, int) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		for i, rf := range rafts {
			term, isLeader := rf.Raft.GetState()
			if term > oldTerm && isLeader {
				return i, term
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("No new leader elected after term %d within timeout", oldTerm)
	return -1, -1
}

// CountVotingMembers counts the number of voting members in the current configuration
func CountVotingMembers(rf *Raft) int {
	rf.mu.Lock()
	defer rf.mu.Unlock()

	// For now, just count all peers as voting members
	// This can be enhanced when configuration management is exposed
	return len(rf.peers)
}

// GetLeaderID finds and returns the current leader's ID, or -1 if no leader
func GetLeaderID(rafts []*TestRaft) int {
	for i, rf := range rafts {
		_, isLeader := rf.Raft.GetState()
		if isLeader {
			return i
		}
	}
	return -1
}

// WaitForNewTerm waits for any server to advance to a new term with a leader
func WaitForNewTerm(t *testing.T, rafts []*TestRaft, oldTerm int, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		for _, rf := range rafts {
			term, _ := rf.Raft.GetState()
			if term > oldTerm {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("No server advanced beyond term %d within timeout", oldTerm)
}

// WaitForLogLength waits for a server to have at least the specified log length
func WaitForLogLength(t *testing.T, rf *Raft, minLength int, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		rf.mu.Lock()
		length := len(rf.log)
		rf.mu.Unlock()
		if length >= minLength {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	rf.mu.Lock()
	currentLength := len(rf.log)
	rf.mu.Unlock()
	t.Fatalf("Log length %d did not reach %d within timeout", currentLength, minLength)
}

// WaitForSnapshot waits for a server to create a snapshot
func WaitForSnapshot(t *testing.T, rf *Raft, minIndex int, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		// Check if snapshot exists
		// This is a placeholder - actual implementation depends on snapshot interface
		time.Sleep(10 * time.Millisecond)
	}
	t.Logf("Note: WaitForSnapshot needs implementation based on actual snapshot interface")
}

// WaitForStableCluster waits for the cluster to stabilize with a single leader
func WaitForStableCluster(t *testing.T, rafts []*TestRaft, timeout time.Duration) int {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		leaderCount := 0
		leaderID := -1
		sameTerm := true
		firstTerm := -1

		for i, rf := range rafts {
			term, isLeader := rf.Raft.GetState()
			if firstTerm == -1 {
				firstTerm = term
			} else if term != firstTerm {
				sameTerm = false
			}
			if isLeader {
				leaderCount++
				leaderID = i
			}
		}

		if leaderCount == 1 && sameTerm {
			return leaderID
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("Cluster did not stabilize with single leader within timeout")
	return -1
}

// DrainApplyChannel drains all pending entries from an apply channel
func DrainApplyChannel(applyCh chan LogEntry) {
	for {
		select {
		case <-applyCh:
			// Continue draining
		default:
			return
		}
	}
}

// CreateTestPersister creates a persister for testing with a temporary directory
func CreateTestPersister(t *testing.T, serverID int) (*Persister, func()) {
	tempDir, err := os.MkdirTemp("", "raft-test-")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}

	cleanup := func() {
		os.RemoveAll(tempDir)
	}

	return NewPersister(tempDir, serverID), cleanup
}

// CreateTestRaftWithPersistence creates a Raft instance with persistence enabled
func CreateTestRaftWithPersistence(t *testing.T, peers []int, me int, applyCh chan LogEntry) (*TestRaft, *Persister, func()) {
	persister, cleanup := CreateTestPersister(t, me)

	rf := NewRaft(peers, me, applyCh)
	rf.SetPersister(persister)

	transport := NewTestTransport()
	testRf := &TestRaft{
		Raft:      rf,
		transport: transport,
	}
	testRf.setupTestTransport()
	transport.RegisterServer(me, rf)

	return testRf, persister, cleanup
}

// SimulateCrashAndRecover simulates a server crash and recovery
func SimulateCrashAndRecover(t *testing.T, rf *TestRaft, persister *Persister, peers []int, applyCh chan LogEntry) *TestRaft {
	// Don't call Stop() here - the caller should have already stopped it
	// This avoids double-closing channels

	// Create a new instance with same ID and persister
	newRf := NewRaft(peers, rf.me, applyCh)
	newRf.SetPersister(persister)

	// Re-register with transport
	newTestRf := &TestRaft{
		Raft:      newRf,
		transport: rf.transport,
	}
	newTestRf.setupTestTransport()
	rf.transport.RegisterServer(rf.me, newRf)

	return newTestRf
}

// VerifyPersistentState verifies that persistent state matches expected values
func VerifyPersistentState(t *testing.T, persister *Persister, expectedTerm int, expectedVotedFor *int, minLogLength int) {
	term, votedFor, log, err := persister.LoadState()
	if err != nil {
		t.Fatalf("Failed to load state: %v", err)
	}

	if term != expectedTerm {
		t.Errorf("Expected term %d, got %d", expectedTerm, term)
	}

	if expectedVotedFor == nil && votedFor != nil {
		t.Errorf("Expected votedFor nil, got %d", *votedFor)
	} else if expectedVotedFor != nil && votedFor == nil {
		t.Errorf("Expected votedFor %d, got nil", *expectedVotedFor)
	} else if expectedVotedFor != nil && votedFor != nil && *expectedVotedFor != *votedFor {
		t.Errorf("Expected votedFor %d, got %d", *expectedVotedFor, *votedFor)
	}

	if len(log) < minLogLength {
		t.Errorf("Expected log length at least %d, got %d", minLogLength, len(log))
	}
}

// WaitForPersistence waits for state to be persisted
func WaitForPersistence(t *testing.T, persister *Persister, checkFunc func(term int, votedFor *int, log []LogEntry) bool, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		term, votedFor, log, err := persister.LoadState()
		if err == nil && checkFunc(term, votedFor, log) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("Persistence condition not met within timeout")
}

// CreateSnapshotData creates test snapshot data
func CreateSnapshotData(entries []LogEntry) []byte {
	data, _ := json.Marshal(entries)
	return data
}

// VerifySnapshot verifies that a snapshot exists with expected properties
func VerifySnapshot(t *testing.T, persister *Persister, expectedIndex int, expectedTerm int) {
	if !persister.HasSnapshot() {
		t.Fatal("Expected snapshot to exist")
	}

	_, lastIndex, lastTerm, err := persister.LoadSnapshot()
	if err != nil {
		t.Fatalf("Failed to load snapshot: %v", err)
	}

	if lastIndex != expectedIndex {
		t.Errorf("Expected snapshot index %d, got %d", expectedIndex, lastIndex)
	}

	if lastTerm != expectedTerm {
		t.Errorf("Expected snapshot term %d, got %d", expectedTerm, lastTerm)
	}
}

// CleanupTestPersistence removes all persistence files for a test
func CleanupTestPersistence(dataDir string) {
	os.RemoveAll(dataDir)
}

// WaitForSnapshotInstallation waits for a snapshot to be installed on a server
func WaitForSnapshotInstallation(t *testing.T, rf *Raft, minIndex int, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		rf.mu.RLock()
		lastSnapshotIndex := rf.lastSnapshotIndex
		rf.mu.RUnlock()

		if lastSnapshotIndex >= minIndex {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}

	rf.mu.RLock()
	currentIndex := rf.lastSnapshotIndex
	rf.mu.RUnlock()
	t.Fatalf("Snapshot not installed: expected index >= %d, got %d", minIndex, currentIndex)
}
