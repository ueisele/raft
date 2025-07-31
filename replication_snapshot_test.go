package raft

import (
	"bytes"
	"fmt"
	"testing"
	"time"
)


// TestSnapshotSending tests the snapshot sending functionality
func TestSnapshotSending(t *testing.T) {
	// Create test components
	config := &Config{
		ID:                 1,
		Peers:              []int{1, 2, 3},
		ElectionTimeoutMin: 150 * time.Millisecond,
		ElectionTimeoutMax: 300 * time.Millisecond,
		HeartbeatInterval:  50 * time.Millisecond,
		Logger:             NewTestLogger(t),
	}

	transport := NewMockTransport(1)
	state := NewStateManager(1, config)
	state.BecomeLeader()
	logManager := NewLogManager()
	stateMachine := &MockStateMachine{}

	// Create test snapshot
	testData := make([]byte, 100*1024) // 100KB test data
	for i := range testData {
		testData[i] = byte(i % 256)
	}

	snapshotProvider := NewMockSnapshotProvider()
	snapshotProvider.SetSnapshot(&Snapshot{
		Data:              testData,
		LastIncludedIndex: 100,
		LastIncludedTerm:  5,
	})

	applyNotify := make(chan struct{}, 1)
	rm := NewReplicationManager(1, []int{1, 2, 3}, state, logManager, transport, config, stateMachine, snapshotProvider, applyNotify)
	rm.BecomeLeader()

	// Test successful snapshot sending
	t.Run("successful snapshot transfer", func(t *testing.T) {
		// Set up transport to accept snapshot chunks
		transport.SetInstallSnapshotHandler(func(serverID int, args *InstallSnapshotArgs) (*InstallSnapshotReply, error) {
			// Verify chunk data
			expectedOffset := args.Offset
			expectedData := testData[expectedOffset : expectedOffset+len(args.Data)]
			if !bytes.Equal(args.Data, expectedData) {
				t.Errorf("Chunk data mismatch at offset %d", args.Offset)
			}

			return &InstallSnapshotReply{
				Term: args.Term,
			}, nil
		})

		// Send snapshot
		rm.sendSnapshot(2)

		// Verify snapshot was sent
		if snapshotProvider.GetCalls() != 1 {
			t.Errorf("Expected 1 call to GetLatestSnapshot, got %d", snapshotProvider.GetCalls())
		}

		// Verify state was updated
		rm.mu.Lock()
		if rm.nextIndex[2] != 101 { // LastIncludedIndex + 1
			t.Errorf("Expected nextIndex[2] = 101, got %d", rm.nextIndex[2])
		}
		if rm.matchIndex[2] != 100 {
			t.Errorf("Expected matchIndex[2] = 100, got %d", rm.matchIndex[2])
		}
		rm.mu.Unlock()
	})

	// Test snapshot sending with no snapshot available
	t.Run("no snapshot available", func(t *testing.T) {
		snapshotProvider.SetSnapshot(nil)
		snapshotProvider.SetError(fmt.Errorf("no snapshot available"))

		rm.mu.Lock()
		rm.nextIndex[2] = 50 // Set to middle of log
		rm.mu.Unlock()

		rm.sendSnapshot(2)

		// Should fall back to sending from beginning
		rm.mu.Lock()
		if rm.nextIndex[2] != 1 {
			t.Errorf("Expected nextIndex[2] = 1 after snapshot failure, got %d", rm.nextIndex[2])
		}
		rm.mu.Unlock()
	})

	// Test snapshot sending with RPC failure
	t.Run("RPC failure during snapshot transfer", func(t *testing.T) {
		snapshotProvider.SetSnapshot(&Snapshot{
			Data:              testData,
			LastIncludedIndex: 100,
			LastIncludedTerm:  5,
		})
		snapshotProvider.SetError(nil)

		transport.SetInstallSnapshotHandler(func(serverID int, args *InstallSnapshotArgs) (*InstallSnapshotReply, error) {
			// Fail on second chunk
			if args.Offset > 0 {
				return nil, fmt.Errorf("network error")
			}
			return &InstallSnapshotReply{Term: args.Term}, nil
		})

		rm.sendSnapshot(2)

		// nextIndex should not be updated after failure
		rm.mu.Lock()
		if rm.nextIndex[2] == 101 {
			t.Errorf("nextIndex should not be updated after RPC failure")
		}
		rm.mu.Unlock()
	})

	// Test snapshot sending with higher term reply
	t.Run("higher term in reply", func(t *testing.T) {
		snapshotProvider.SetSnapshot(&Snapshot{
			Data:              []byte("small snapshot"),
			LastIncludedIndex: 100,
			LastIncludedTerm:  5,
		})
		snapshotProvider.SetError(nil)

		transport.SetInstallSnapshotHandler(func(serverID int, args *InstallSnapshotArgs) (*InstallSnapshotReply, error) {
			return &InstallSnapshotReply{
				Term: args.Term + 1, // Higher term
			}, nil
		})

		currentTerm := state.GetCurrentTerm()
		rm.sendSnapshot(2)

		// Should have stepped down
		if state.GetCurrentTerm() <= currentTerm {
			t.Errorf("Expected to step down to higher term")
		}
	})
}

// TestSnapshotChunking tests that large snapshots are properly chunked
func TestSnapshotChunking(t *testing.T) {
	config := &Config{
		ID:                 1,
		Peers:              []int{1, 2},
		ElectionTimeoutMin: 150 * time.Millisecond,
		ElectionTimeoutMax: 300 * time.Millisecond,
		HeartbeatInterval:  50 * time.Millisecond,
		Logger:             NewTestLogger(t),
	}

	transport := NewMockTransport(1)
	state := NewStateManager(1, config)
	state.BecomeLeader()
	logManager := NewLogManager()
	stateMachine := &MockStateMachine{}

	// Create large snapshot (100KB)
	largeData := make([]byte, 100*1024)
	for i := range largeData {
		largeData[i] = byte(i % 256)
	}

	snapshotProvider := NewMockSnapshotProvider()
	snapshotProvider.SetSnapshot(&Snapshot{
		Data:              largeData,
		LastIncludedIndex: 200,
		LastIncludedTerm:  10,
	})

	applyNotify := make(chan struct{}, 1)
	rm := NewReplicationManager(1, []int{1, 2}, state, logManager, transport, config, stateMachine, snapshotProvider, applyNotify)
	rm.BecomeLeader()

	// Track chunks received
	var chunks []InstallSnapshotArgs
	transport.SetInstallSnapshotHandler(func(serverID int, args *InstallSnapshotArgs) (*InstallSnapshotReply, error) {
		// Copy args to avoid data race
		chunk := *args
		chunk.Data = make([]byte, len(args.Data))
		copy(chunk.Data, args.Data)
		chunks = append(chunks, chunk)
		return &InstallSnapshotReply{Term: args.Term}, nil
	})

	// Send snapshot
	rm.sendSnapshot(2)

	// Verify chunking
	if len(chunks) < 2 {
		t.Fatalf("Expected multiple chunks for 100KB snapshot, got %d", len(chunks))
	}

	// Verify first chunk
	if chunks[0].Offset != 0 {
		t.Errorf("First chunk should have offset 0, got %d", chunks[0].Offset)
	}
	if chunks[0].Done {
		t.Errorf("First chunk should not be marked as done")
	}

	// Verify last chunk
	lastChunk := chunks[len(chunks)-1]
	if !lastChunk.Done {
		t.Errorf("Last chunk should be marked as done")
	}

	// Verify all data was sent
	receivedData := make([]byte, 0, len(largeData))
	for _, chunk := range chunks {
		receivedData = append(receivedData, chunk.Data...)
	}
	if !bytes.Equal(receivedData, largeData) {
		t.Errorf("Received data doesn't match original snapshot")
	}

	// Verify all chunks have correct metadata
	for _, chunk := range chunks {
		if chunk.LastIncludedIndex != 200 {
			t.Errorf("Expected LastIncludedIndex = 200, got %d", chunk.LastIncludedIndex)
		}
		if chunk.LastIncludedTerm != 10 {
			t.Errorf("Expected LastIncludedTerm = 10, got %d", chunk.LastIncludedTerm)
		}
	}
}

// TestSnapshotReplicationIntegration tests snapshot sending in replication flow
func TestSnapshotReplicationIntegration(t *testing.T) {
	config := &Config{
		ID:                 1,
		Peers:              []int{1, 2, 3},
		ElectionTimeoutMin: 150 * time.Millisecond,
		ElectionTimeoutMax: 300 * time.Millisecond,
		HeartbeatInterval:  50 * time.Millisecond,
		Logger:             NewTestLogger(t),
	}

	transport := NewMockTransport(1)
	state := NewStateManager(1, config)
	state.BecomeLeader()
	logManager := NewLogManager()

	// Add some entries to log
	for i := 1; i <= 50; i++ {
		logManager.AppendEntries(i-1, 0, []LogEntry{{
			Index:   i,
			Term:    1,
			Command: fmt.Sprintf("cmd%d", i),
		}})
	}

	stateMachine := &MockStateMachine{}
	snapshotProvider := NewMockSnapshotProvider()
	snapshotProvider.SetSnapshot(&Snapshot{
		Data:              []byte("snapshot data"),
		LastIncludedIndex: 40,
		LastIncludedTerm:  1,
	})

	applyNotify := make(chan struct{}, 1)
	rm := NewReplicationManager(1, []int{1, 2, 3}, state, logManager, transport, config, stateMachine, snapshotProvider, applyNotify)
	rm.BecomeLeader()

	// Simulate peer 2 being far behind (nextIndex = 10, but log starts at 41 due to snapshot)
	rm.mu.Lock()
	rm.nextIndex[2] = 10
	rm.mu.Unlock()

	// Clear entries before snapshot to simulate that they're not available
	// This simulates the case where log has been compacted
	// In a real scenario, GetEntry would return nil for compacted entries

	// Track if snapshot was sent
	snapshotSent := false
	transport.SetInstallSnapshotHandler(func(serverID int, args *InstallSnapshotArgs) (*InstallSnapshotReply, error) {
		snapshotSent = true
		return &InstallSnapshotReply{Term: args.Term}, nil
	})

	// Trigger replication
	rm.replicateToPeer(2)

	// Verify snapshot was sent
	if !snapshotSent {
		t.Error("Expected snapshot to be sent when log entry is not available")
	}

	// Verify nextIndex was updated
	rm.mu.Lock()
	if rm.nextIndex[2] != 41 { // LastIncludedIndex + 1
		t.Errorf("Expected nextIndex[2] = 41 after snapshot, got %d", rm.nextIndex[2])
	}
	rm.mu.Unlock()
}