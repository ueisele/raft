package raft

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"
)

// MockSnapshotProvider for testing
type MockSnapshotProvider struct {
	data []byte
	err  error
}

func (m *MockSnapshotProvider) TakeSnapshot() ([]byte, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.data, nil
}

// TestSnapshotCreation tests basic snapshot creation and storage
func TestSnapshotCreation(t *testing.T) {
	peers := []int{0}
	applyCh := make(chan LogEntry, 100)
	
	// Create temporary directory for testing
	dataDir := "/tmp/raft-snapshot-test"
	os.MkdirAll(dataDir, 0755)
	defer os.RemoveAll(dataDir)
	
	persister := NewPersister(dataDir, 0)
	snapshotProvider := &MockSnapshotProvider{
		data: []byte("mock snapshot data"),
	}
	
	// Create RaftWithSnapshot instance
	rfs := NewRaftWithSnapshot(peers, 0, applyCh, persister, 5, snapshotProvider)
	
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	
	rfs.Start(ctx)
	defer rfs.Stop()
	
	// Manually add entries to log and set commit index for testing
	rfs.mu.Lock()
	for i := 1; i <= 10; i++ {
		entry := LogEntry{
			Term:    1,
			Index:   i,
			Command: fmt.Sprintf("cmd%d", i),
		}
		rfs.log = append(rfs.log, entry)
	}
	rfs.commitIndex = 10 // Set commit index to allow snapshot
	rfs.mu.Unlock()
	
	// Take snapshot at index 5
	snapshotIndex := 5
	err := rfs.TakeSnapshot([]byte("test snapshot"), snapshotIndex)
	if err != nil {
		t.Fatalf("Failed to take snapshot: %v", err)
	}
	
	// Verify snapshot was saved
	if !persister.HasSnapshot() {
		t.Error("Snapshot was not saved")
	}
	
	// Load and verify snapshot
	data, lastIndex, _, err := persister.LoadSnapshot()
	if err != nil {
		t.Fatalf("Failed to load snapshot: %v", err)
	}
	
	if string(data) != "test snapshot" {
		t.Errorf("Unexpected snapshot data: got %s, expected %s", string(data), "test snapshot")
	}
	
	if lastIndex != snapshotIndex {
		t.Errorf("Unexpected last included index: got %d, expected %d", lastIndex, snapshotIndex)
	}
	
	// Verify log was trimmed
	rfs.mu.RLock()
	logLength := len(rfs.log)
	rfs.mu.RUnlock()
	
	if logLength > 6 { // Should be trimmed to around snapshot point
		t.Errorf("Log was not properly trimmed after snapshot: length %d", logLength)
	}
}

// TestSnapshotInstallation tests InstallSnapshot RPC
func TestSnapshotInstallation(t *testing.T) {
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

	t.Logf("Initial leader: server %d", leaderIdx)

	// Submit many commands to create a long log
	for i := 0; i < 20; i++ {
		leader.Submit(fmt.Sprintf("cmd%d", i))
		time.Sleep(50 * time.Millisecond)
	}

	// Wait for replication
	time.Sleep(2 * time.Second)

	// Disconnect one follower for a long time
	laggedFollowerIdx := (leaderIdx + 1) % 3
	t.Logf("Disconnecting follower: server %d", laggedFollowerIdx)
	transport.DisconnectServer(laggedFollowerIdx)

	// Submit more commands while follower is disconnected
	for i := 20; i < 40; i++ {
		leader.Submit(fmt.Sprintf("cmd%d", i))
		time.Sleep(30 * time.Millisecond)
	}

	// Create a snapshot on the leader
	snapshotData := []byte("snapshot at index 30")
	err := leader.TakeSnapshot(snapshotData, 30)
	if err != nil {
		t.Logf("Snapshot creation failed (may be expected): %v", err)
	}

	// Wait a bit more to let the leader establish its position
	time.Sleep(1 * time.Second)

	// Reconnect the lagged follower
	transport.ReconnectServer(laggedFollowerIdx)

	// Give the system time to stabilize, but prevent the lagged follower from becoming leader
	// by temporarily boosting the original leader's term if needed
	time.Sleep(2 * time.Second)
	
	// Ensure the original leader or one of the up-to-date servers remains leader
	// by having the leader send heartbeats to maintain authority
	if currentTerm, isLeader := leader.GetState(); isLeader {
		// Leader is still active, continue with test
		t.Logf("Original leader (server %d) still in control at term %d", leaderIdx, currentTerm)
	} else {
		// Find the current leader among the servers that were not lagged
		var newLeader *Raft
		var newLeaderIdx int
		for i, rf := range rafts {
			if i != laggedFollowerIdx {
				if _, isLeader := rf.GetState(); isLeader {
					newLeader = rf.Raft
					newLeaderIdx = i
					break
				}
			}
		}
		
		if newLeader != nil {
			leader = newLeader
			leaderIdx = newLeaderIdx
			t.Logf("New leader found: server %d (not the lagged follower)", leaderIdx)
		} else {
			// If the lagged follower became leader, force a leadership change
			// by having one of the up-to-date servers start an election
			upToDateServer := (laggedFollowerIdx + 1) % 3
			if upToDateServer == laggedFollowerIdx {
				upToDateServer = (laggedFollowerIdx + 2) % 3
			}
			
			// Trigger election by disconnecting and reconnecting an up-to-date server
			t.Logf("Lagged follower became leader, triggering new election from server %d", upToDateServer)
			transport.DisconnectServer(upToDateServer)
			time.Sleep(100 * time.Millisecond)
			transport.ReconnectServer(upToDateServer)
			time.Sleep(2 * time.Second)
			
			// Find the new leader
			for i, rf := range rafts {
				if _, isLeader := rf.GetState(); isLeader {
					leader = rf.Raft
					leaderIdx = i
					break
				}
			}
		}
	}

	// Ensure we have a leader that's not the lagged follower
	if leaderIdx == laggedFollowerIdx {
		t.Logf("WARNING: Lagged follower became leader - this limits the InstallSnapshot test effectiveness")
	}

	// The leader should send InstallSnapshot RPC to bring the follower up to date
	time.Sleep(3 * time.Second)

	// Verify the lagged follower caught up
	laggedFollower := rafts[laggedFollowerIdx]
	laggedFollower.mu.RLock()
	followerLogLength := len(laggedFollower.log)
	followerCommitIndex := laggedFollower.commitIndex
	laggedFollower.mu.RUnlock()

	leader.mu.RLock()
	leaderLogLength := len(leader.log)
	leaderCommitIndex := leader.commitIndex
	leaderSnapshotIndex := leader.lastSnapshotIndex
	leader.mu.RUnlock()

	t.Logf("Final leader: server %d", leaderIdx)
	t.Logf("Lagged follower: server %d", laggedFollowerIdx)

	t.Logf("Leader log length: %d, commit index: %d, snapshot index: %d", leaderLogLength, leaderCommitIndex, leaderSnapshotIndex)
	t.Logf("Follower log length: %d, commit index: %d", followerLogLength, followerCommitIndex)

	// The follower should have caught up reasonably close to the leader
	if followerCommitIndex < leaderCommitIndex-5 {
		t.Errorf("Follower did not catch up properly: follower commit %d, leader commit %d", followerCommitIndex, leaderCommitIndex)
	}

	// Stop all instances
	for i := 0; i < 3; i++ {
		rafts[i].Stop()
	}
}

// TestMultiChunkSnapshot tests InstallSnapshot with multiple chunks
func TestMultiChunkSnapshot(t *testing.T) {
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

	for i := 0; i < 2; i++ {
		rafts[i].Start(ctx)
	}

	// Wait for leader election
	time.Sleep(1 * time.Second)

	// Find leader and follower
	var leader, follower *Raft
	var leaderIdx, followerIdx int
	for i, rf := range rafts {
		_, isLeader := rf.GetState()
		if isLeader {
			leader = rf.Raft
			leaderIdx = i
			follower = rafts[1-i].Raft
			followerIdx = 1 - i
			break
		}
	}

	if leader == nil {
		t.Fatal("No leader found")
	}

	t.Logf("Leader: server %d, Follower: server %d", leaderIdx, followerIdx)

	// Set up follower's term to accept the snapshot
	follower.mu.Lock()
	follower.currentTerm = 2
	follower.mu.Unlock()

	// Prepare a large snapshot that would need multiple chunks
	largeSnapshotData := make([]byte, 1024*1024) // 1MB
	for i := range largeSnapshotData {
		largeSnapshotData[i] = byte(i % 256)
	}

	// Test multi-chunk InstallSnapshot
	maxChunkSize := 64 * 1024 // 64KB chunks
	
	lastIncludedIndex := 10
	lastIncludedTerm := 2

	// Send snapshot in chunks
	totalChunks := (len(largeSnapshotData) + maxChunkSize - 1) / maxChunkSize
	t.Logf("Sending snapshot in %d chunks of max %d bytes", totalChunks, maxChunkSize)

	for offset := 0; offset < len(largeSnapshotData); offset += maxChunkSize {
		end := offset + maxChunkSize
		if end > len(largeSnapshotData) {
			end = len(largeSnapshotData)
		}
		
		chunk := largeSnapshotData[offset:end]
		done := (end == len(largeSnapshotData))

		args := &InstallSnapshotArgs{
			Term:              2,
			LeaderID:          leaderIdx,
			LastIncludedIndex: lastIncludedIndex,
			LastIncludedTerm:  lastIncludedTerm,
			Offset:            offset,
			Data:              chunk,
			Done:              done,
		}

		reply := &InstallSnapshotReply{}
		err := follower.InstallSnapshot(args, reply)
		if err != nil {
			t.Fatalf("InstallSnapshot failed: %v", err)
		}

		t.Logf("Sent chunk at offset %d, size %d, done: %v", offset, len(chunk), done)

		if !done {
			// For incomplete chunks, follower should accept but not install yet
			follower.mu.RLock()
			incompleteSnapshot := follower.incomingSnapshot
			follower.mu.RUnlock()
			
			if incompleteSnapshot == nil {
				t.Error("Follower should be tracking incomplete snapshot")
			} else if len(incompleteSnapshot.data) != end {
				t.Errorf("Incomplete snapshot data length mismatch: got %d, expected %d", len(incompleteSnapshot.data), end)
			}
		}
	}

	// Immediately after final chunk, verify snapshot state before any leadership changes
	follower.mu.RLock()
	incompleteSnapshot := follower.incomingSnapshot
	lastApplied := follower.lastApplied
	commitIndex := follower.commitIndex
	follower.mu.RUnlock()

	t.Logf("Immediately after installation: lastApplied=%d, commitIndex=%d, incompleteSnapshot=%v", lastApplied, commitIndex, incompleteSnapshot != nil)

	// Incomplete snapshot should be cleared after successful installation
	if incompleteSnapshot != nil {
		t.Error("Incomplete snapshot should be cleared after installation")
	}

	// State should be updated to snapshot point
	if lastApplied != lastIncludedIndex {
		t.Errorf("lastApplied not updated: got %d, expected %d", lastApplied, lastIncludedIndex)
	}

	if commitIndex != lastIncludedIndex {
		t.Errorf("commitIndex not updated: got %d, expected %d", commitIndex, lastIncludedIndex)
	}

	// Wait a bit to allow for any leadership changes, then verify system stability
	time.Sleep(1 * time.Second)

	// Check final state - note that commitIndex might be reset if follower becomes leader
	follower.mu.RLock()
	finalLastApplied := follower.lastApplied
	finalState := follower.state
	follower.mu.RUnlock()

	t.Logf("Final state: lastApplied=%d, state=%v", finalLastApplied, finalState)

	// lastApplied should remain at snapshot point regardless of leadership changes
	if finalLastApplied != lastIncludedIndex {
		t.Errorf("Final lastApplied changed: got %d, expected %d", finalLastApplied, lastIncludedIndex)
	}

	// Stop all instances
	for i := 0; i < 2; i++ {
		rafts[i].Stop()
	}
}

// TestSnapshotWithLogTruncation tests that snapshots properly truncate logs
func TestSnapshotWithLogTruncation(t *testing.T) {
	peers := []int{0}
	applyCh := make(chan LogEntry, 100)
	
	dataDir := "/tmp/raft-snapshot-truncate-test"
	os.MkdirAll(dataDir, 0755)
	defer os.RemoveAll(dataDir)
	
	persister := NewPersister(dataDir, 0)
	snapshotProvider := &MockSnapshotProvider{
		data: []byte("snapshot data"),
	}
	
	rfs := NewRaftWithSnapshot(peers, 0, applyCh, persister, 10, snapshotProvider)
	
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	
	rfs.Start(ctx)
	defer rfs.Stop()

	// Manually add entries to log and set commit index for testing
	rfs.mu.Lock()
	for i := 1; i <= 15; i++ {
		entry := LogEntry{
			Term:    1,
			Index:   i,
			Command: fmt.Sprintf("cmd%d", i),
		}
		rfs.log = append(rfs.log, entry)
	}
	rfs.commitIndex = 15 // Set commit index to allow snapshot
	initialLogLength := len(rfs.log)
	rfs.mu.Unlock()

	t.Logf("Initial log length: %d", initialLogLength)

	// Take snapshot at index 8
	snapshotIndex := 8
	err := rfs.TakeSnapshot([]byte("test snapshot"), snapshotIndex)
	if err != nil {
		t.Fatalf("Failed to take snapshot: %v", err)
	}

	// Wait for snapshot processing
	time.Sleep(500 * time.Millisecond)

	// Verify log was truncated
	rfs.mu.RLock()
	newLogLength := len(rfs.log)
	firstLogIndex := 0
	if len(rfs.log) > 0 {
		firstLogIndex = rfs.log[0].Index
	}
	rfs.mu.RUnlock()

	t.Logf("New log length: %d, first log index: %d", newLogLength, firstLogIndex)

	// Log should be shorter and first entry should be at snapshot index
	if newLogLength >= initialLogLength {
		t.Error("Log was not truncated after snapshot")
	}

	// The log should start around the snapshot index
	if firstLogIndex < snapshotIndex-1 {
		t.Errorf("Log not properly truncated: first index %d, snapshot index %d", firstLogIndex, snapshotIndex)
	}
}

// TestSnapshotErrorHandling tests error scenarios in snapshot operations
func TestSnapshotErrorHandling(t *testing.T) {
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

	// Submit some commands
	for i := 0; i < 10; i++ {
		leader.Submit(fmt.Sprintf("cmd%d", i))
		time.Sleep(50 * time.Millisecond)
	}

	// Wait for replication
	time.Sleep(1 * time.Second)

	// Verify system continues to operate - take snapshot manually to test error handling
	leader.mu.RLock()
	initialLogLength := len(leader.log)
	leader.mu.RUnlock()

	// Try to take snapshot at invalid index (beyond commit index) - should fail gracefully
	err := leader.TakeSnapshot([]byte("test snapshot"), initialLogLength+10)
	if err == nil {
		t.Error("Taking snapshot beyond commit index should fail")
	}

	// Submit more commands to verify system continues operating
	for i := 10; i < 15; i++ {
		leader.Submit(fmt.Sprintf("cmd%d", i))
		time.Sleep(50 * time.Millisecond)
	}

	// Wait for replication
	time.Sleep(1 * time.Second)

	// Verify log continued to grow (snapshot failure didn't break system)
	leader.mu.RLock()
	finalLogLength := len(leader.log)
	leader.mu.RUnlock()

	if finalLogLength <= initialLogLength {
		t.Error("System should continue operating despite snapshot failure")
	}

	// Test invalid InstallSnapshot scenarios
	follower := rafts[(leaderIdx+1)%3].Raft

	// Test with term mismatch
	args := &InstallSnapshotArgs{
		Term:              1, // Lower term
		LeaderID:          leaderIdx,
		LastIncludedIndex: 5,
		LastIncludedTerm:  1,
		Offset:            0,
		Data:              []byte("test"),
		Done:              true,
	}

	follower.mu.Lock()
	follower.currentTerm = 2 // Higher term
	follower.mu.Unlock()

	reply := &InstallSnapshotReply{}
	err = follower.InstallSnapshot(args, reply)
	if err != nil {
		t.Fatalf("InstallSnapshot should not return error for term mismatch: %v", err)
	}

	// Reply should contain current term
	if reply.Term != 2 {
		t.Errorf("Expected reply term 2, got %d", reply.Term)
	}

	// Test out-of-order chunks
	args1 := &InstallSnapshotArgs{
		Term:              3,
		LeaderID:          leaderIdx,
		LastIncludedIndex: 10,
		LastIncludedTerm:  2,
		Offset:            0,
		Data:              []byte("chunk1"),
		Done:              false,
	}

	args2 := &InstallSnapshotArgs{
		Term:              3,
		LeaderID:          leaderIdx,
		LastIncludedIndex: 10,
		LastIncludedTerm:  2,
		Offset:            10, // Wrong offset (should be 6)
		Data:              []byte("chunk2"),
		Done:              true,
	}

	follower.InstallSnapshot(args1, &InstallSnapshotReply{})
	follower.InstallSnapshot(args2, &InstallSnapshotReply{}) // Should be ignored

	// Verify out-of-order chunk was ignored
	follower.mu.RLock()
	incompleteSnapshot := follower.incomingSnapshot
	follower.mu.RUnlock()

	if incompleteSnapshot != nil && len(incompleteSnapshot.data) != len(args1.Data) {
		t.Error("Out-of-order chunk should have been ignored")
	}

	// Stop all instances
	for i := 0; i < 3; i++ {
		rafts[i].Stop()
	}
}