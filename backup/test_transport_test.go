package raft

import (
	"context"
	"testing"
	"time"
)

// TestNewTestTransport tests creating a new test transport
func TestNewTestTransport(t *testing.T) {
	transport := NewTestTransport()

	if transport == nil {
		t.Fatal("NewTestTransport returned nil")
	}

	if transport.servers == nil {
		t.Error("servers map not initialized")
	}

	if transport.disconnectedServers == nil {
		t.Error("disconnectedServers map not initialized")
	}

	if transport.partitions == nil {
		t.Error("partitions map not initialized")
	}

	if transport.asymmetricPartitions == nil {
		t.Error("asymmetricPartitions map not initialized")
	}
}

// TestRegisterServer tests server registration
func TestRegisterServer(t *testing.T) {
	transport := NewTestTransport()

	// Create a test Raft instance
	rf := NewRaft([]int{0, 1, 2}, 0, make(chan LogEntry))

	// Register the server
	transport.RegisterServer(0, rf)

	// Verify registration
	transport.mu.RLock()
	registered := transport.servers[0]
	transport.mu.RUnlock()

	if registered != rf {
		t.Error("Server not properly registered")
	}

	// Register another server with same ID (should overwrite)
	rf2 := NewRaft([]int{0, 1, 2}, 0, make(chan LogEntry))
	transport.RegisterServer(0, rf2)

	transport.mu.RLock()
	registered = transport.servers[0]
	transport.mu.RUnlock()

	if registered != rf2 {
		t.Error("Server registration should overwrite existing")
	}
}

// TestSendRequestVoteBasic tests basic RequestVote RPC
func TestSendRequestVoteBasic(t *testing.T) {
	transport := NewTestTransport()

	// Create and register servers
	peers := []int{0, 1, 2}
	rafts := make([]*Raft, 3)
	for i := 0; i < 3; i++ {
		rafts[i] = NewRaft(peers, i, make(chan LogEntry))
		transport.RegisterServer(i, rafts[i])
	}

	// Create RequestVote args
	args := &RequestVoteArgs{
		Term:         1,
		CandidateID:  0,
		LastLogIndex: 0,
		LastLogTerm:  0,
	}
	reply := &RequestVoteReply{}

	// Send RequestVote from 0 to 1
	success := transport.SendRequestVote(0, 1, args, reply)

	if !success {
		t.Error("RequestVote should succeed between connected servers")
	}

	// Try to send to non-existent server
	success = transport.SendRequestVote(0, 5, args, reply)

	if success {
		t.Error("RequestVote to non-existent server should fail")
	}
}

// TestSendAppendEntriesBasic tests basic AppendEntries RPC
func TestSendAppendEntriesBasic(t *testing.T) {
	transport := NewTestTransport()

	// Create and register servers
	peers := []int{0, 1, 2}
	rafts := make([]*Raft, 3)
	for i := 0; i < 3; i++ {
		rafts[i] = NewRaft(peers, i, make(chan LogEntry))
		transport.RegisterServer(i, rafts[i])
	}

	// Create AppendEntries args
	args := &AppendEntriesArgs{
		Term:         1,
		LeaderID:     0,
		PrevLogIndex: 0,
		PrevLogTerm:  0,
		Entries:      []LogEntry{},
		LeaderCommit: 0,
	}
	reply := &AppendEntriesReply{}

	// Send AppendEntries from 0 to 1
	success := transport.SendAppendEntries(0, 1, args, reply)

	if !success {
		t.Error("AppendEntries should succeed between connected servers")
	}

	// Try to send to non-existent server
	success = transport.SendAppendEntries(0, 5, args, reply)

	if success {
		t.Error("AppendEntries to non-existent server should fail")
	}
}

// TestSendInstallSnapshotBasic tests basic InstallSnapshot RPC
func TestSendInstallSnapshotBasic(t *testing.T) {
	transport := NewTestTransport()

	// Create and register servers
	peers := []int{0, 1, 2}
	rafts := make([]*Raft, 3)
	for i := 0; i < 3; i++ {
		rafts[i] = NewRaft(peers, i, make(chan LogEntry))
		transport.RegisterServer(i, rafts[i])
	}

	// Create InstallSnapshot args
	args := &InstallSnapshotArgs{
		Term:              1,
		LeaderID:          0,
		LastIncludedIndex: 5,
		LastIncludedTerm:  1,
		Data:              []byte("snapshot data"),
	}
	reply := &InstallSnapshotReply{}

	// Send InstallSnapshot from 0 to 1
	success := transport.SendInstallSnapshot(0, 1, args, reply)

	if !success {
		t.Error("InstallSnapshot should succeed between connected servers")
	}

	// Try to send to non-existent server
	success = transport.SendInstallSnapshot(0, 5, args, reply)

	if success {
		t.Error("InstallSnapshot to non-existent server should fail")
	}
}

// TestDisconnectServer tests server disconnection
func TestDisconnectServer(t *testing.T) {
	transport := NewTestTransport()

	// Create and register servers
	peers := []int{0, 1, 2}
	rafts := make([]*Raft, 3)
	for i := 0; i < 3; i++ {
		rafts[i] = NewRaft(peers, i, make(chan LogEntry))
		transport.RegisterServer(i, rafts[i])
	}

	// Disconnect server 1
	transport.DisconnectServer(1)

	args := &RequestVoteArgs{Term: 1, CandidateID: 0}
	reply := &RequestVoteReply{}

	// Try to send from disconnected server
	success := transport.SendRequestVote(1, 2, args, reply)
	if success {
		t.Error("RPC from disconnected server should fail")
	}

	// Try to send to disconnected server
	success = transport.SendRequestVote(0, 1, args, reply)
	if success {
		t.Error("RPC to disconnected server should fail")
	}

	// Communication between other servers should still work
	success = transport.SendRequestVote(0, 2, args, reply)
	if !success {
		t.Error("RPC between connected servers should succeed")
	}
}

// TestReconnectServer tests server reconnection
func TestReconnectServer(t *testing.T) {
	transport := NewTestTransport()

	// Create and register servers
	peers := []int{0, 1, 2}
	rafts := make([]*Raft, 3)
	for i := 0; i < 3; i++ {
		rafts[i] = NewRaft(peers, i, make(chan LogEntry))
		transport.RegisterServer(i, rafts[i])
	}

	// Disconnect and then reconnect server 1
	transport.DisconnectServer(1)
	transport.ReconnectServer(1)

	args := &RequestVoteArgs{Term: 1, CandidateID: 0}
	reply := &RequestVoteReply{}

	// Should be able to communicate again
	success := transport.SendRequestVote(0, 1, args, reply)
	if !success {
		t.Error("RPC to reconnected server should succeed")
	}

	success = transport.SendRequestVote(1, 0, args, reply)
	if !success {
		t.Error("RPC from reconnected server should succeed")
	}
}

// TestDisconnectPair tests pairwise partition
func TestDisconnectPair(t *testing.T) {
	transport := NewTestTransport()

	// Create and register servers
	peers := []int{0, 1, 2}
	rafts := make([]*Raft, 3)
	for i := 0; i < 3; i++ {
		rafts[i] = NewRaft(peers, i, make(chan LogEntry))
		transport.RegisterServer(i, rafts[i])
	}

	// Disconnect servers 0 and 1
	transport.DisconnectPair(0, 1)

	args := &RequestVoteArgs{Term: 1, CandidateID: 0}
	reply := &RequestVoteReply{}

	// Cannot communicate between 0 and 1
	success := transport.SendRequestVote(0, 1, args, reply)
	if success {
		t.Error("RPC from 0 to 1 should fail")
	}

	success = transport.SendRequestVote(1, 0, args, reply)
	if success {
		t.Error("RPC from 1 to 0 should fail")
	}

	// But can communicate with server 2
	success = transport.SendRequestVote(0, 2, args, reply)
	if !success {
		t.Error("RPC from 0 to 2 should succeed")
	}

	success = transport.SendRequestVote(1, 2, args, reply)
	if !success {
		t.Error("RPC from 1 to 2 should succeed")
	}

	success = transport.SendRequestVote(2, 0, args, reply)
	if !success {
		t.Error("RPC from 2 to 0 should succeed")
	}

	success = transport.SendRequestVote(2, 1, args, reply)
	if !success {
		t.Error("RPC from 2 to 1 should succeed")
	}
}

// TestReconnectPair tests removing pairwise partition
func TestReconnectPair(t *testing.T) {
	transport := NewTestTransport()

	// Create and register servers
	peers := []int{0, 1, 2}
	rafts := make([]*Raft, 3)
	for i := 0; i < 3; i++ {
		rafts[i] = NewRaft(peers, i, make(chan LogEntry))
		transport.RegisterServer(i, rafts[i])
	}

	// Disconnect and then reconnect servers 0 and 1
	transport.DisconnectPair(0, 1)
	transport.ReconnectPair(0, 1)

	args := &RequestVoteArgs{Term: 1, CandidateID: 0}
	reply := &RequestVoteReply{}

	// Should be able to communicate again
	success := transport.SendRequestVote(0, 1, args, reply)
	if !success {
		t.Error("RPC from 0 to 1 should succeed after reconnect")
	}

	success = transport.SendRequestVote(1, 0, args, reply)
	if !success {
		t.Error("RPC from 1 to 0 should succeed after reconnect")
	}
}

// TestAsymmetricPartitionTransport tests one-way network partition
func TestAsymmetricPartitionTransport(t *testing.T) {
	transport := NewTestTransport()

	// Create and register servers
	peers := []int{0, 1, 2}
	rafts := make([]*Raft, 3)
	for i := 0; i < 3; i++ {
		rafts[i] = NewRaft(peers, i, make(chan LogEntry))
		transport.RegisterServer(i, rafts[i])
	}

	// Create asymmetric partition: 0 cannot send to 1
	transport.SetAsymmetricPartition(0, 1, true)

	args := &RequestVoteArgs{Term: 1, CandidateID: 0, LastLogIndex: 0, LastLogTerm: 0}
	reply := &RequestVoteReply{}

	// 0 cannot send to 1
	success := transport.SendRequestVote(0, 1, args, reply)
	if success {
		t.Error("RPC from 0 to 1 should fail")
	}

	// 1 can send to 0, but the response from 0 to 1 will be blocked
	// because we have an asymmetric partition from 0 to 1
	args2 := &RequestVoteArgs{Term: 1, CandidateID: 1, LastLogIndex: 0, LastLogTerm: 0}
	reply2 := &RequestVoteReply{}
	success = transport.SendRequestVote(1, 0, args2, reply2)
	if success {
		t.Error("RPC from 1 to 0 should fail because response is blocked by asymmetric partition")
	}

	// But 1 can communicate with 2
	args3 := &RequestVoteArgs{Term: 1, CandidateID: 1, LastLogIndex: 0, LastLogTerm: 0}
	reply3 := &RequestVoteReply{}
	success = transport.SendRequestVote(1, 2, args3, reply3)
	if !success {
		t.Error("RPC from 1 to 2 should succeed")
	}

	// Remove the partition
	transport.SetAsymmetricPartition(0, 1, false)

	// Now 0 can send to 1
	reply4 := &RequestVoteReply{}
	success = transport.SendRequestVote(0, 1, args, reply4)
	if !success {
		t.Error("RPC from 0 to 1 should succeed after partition removed")
	}

	// And 1 can send to 0 with response
	reply5 := &RequestVoteReply{}
	success = transport.SendRequestVote(1, 0, args2, reply5)
	if !success {
		t.Error("RPC from 1 to 0 should succeed after partition removed")
	}
}

// TestAsymmetricPartitionResponse tests asymmetric partition on response path
func TestAsymmetricPartitionResponse(t *testing.T) {
	transport := NewTestTransport()

	// Create and register servers with started Raft instances
	peers := []int{0, 1, 2}
	rafts := make([]*TestRaft, 3)
	for i := 0; i < 3; i++ {
		rafts[i] = NewTestRaft(peers, i, make(chan LogEntry, 100), transport)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	for i := 0; i < 3; i++ {
		rafts[i].Start(ctx)
	}

	// Wait a bit for initialization
	time.Sleep(100 * time.Millisecond)

	// Create asymmetric partition: 1 cannot send responses back to 0
	transport.SetAsymmetricPartition(1, 0, true)

	args := &RequestVoteArgs{Term: 10, CandidateID: 0, LastLogIndex: 0, LastLogTerm: 0}
	reply := &RequestVoteReply{}

	// 0 sends to 1, but won't get response
	success := transport.SendRequestVote(0, 1, args, reply)
	if success {
		t.Error("RPC should fail when response is blocked")
	}

	// Verify that server 1 actually processed the request by checking its term
	rafts[1].mu.RLock()
	term1 := rafts[1].currentTerm
	rafts[1].mu.RUnlock()

	if term1 < 10 {
		t.Errorf("Server 1 should have updated term to at least 10, got %d", term1)
	}

	// Stop all servers
	for i := 0; i < 3; i++ {
		rafts[i].Stop()
	}
}

// TestNewTestRaft tests creating a new TestRaft instance
func TestNewTestRaft(t *testing.T) {
	transport := NewTestTransport()
	peers := []int{0, 1, 2}
	applyCh := make(chan LogEntry, 100)

	testRaft := NewTestRaft(peers, 0, applyCh, transport)

	if testRaft == nil {
		t.Fatal("NewTestRaft returned nil")
	}

	if testRaft.Raft == nil {
		t.Fatal("TestRaft.Raft is nil")
	}

	if testRaft.transport != transport {
		t.Error("TestRaft transport not set correctly")
	}

	// Verify server is registered with transport
	transport.mu.RLock()
	registered := transport.servers[0]
	transport.mu.RUnlock()

	if registered != testRaft.Raft {
		t.Error("TestRaft not registered with transport")
	}

	// Verify RPC methods are overridden
	if testRaft.Raft.sendRequestVote == nil {
		t.Error("sendRequestVote not overridden")
	}

	if testRaft.Raft.sendAppendEntries == nil {
		t.Error("sendAppendEntries not overridden")
	}

	if testRaft.Raft.sendInstallSnapshotFn == nil {
		t.Error("sendInstallSnapshotFn not overridden")
	}
}

// TestComplexPartitionScenario tests a complex partition scenario
func TestComplexPartitionScenario(t *testing.T) {
	transport := NewTestTransport()

	// Create 5 servers
	peers := []int{0, 1, 2, 3, 4}
	rafts := make([]*Raft, 5)
	for i := 0; i < 5; i++ {
		rafts[i] = NewRaft(peers, i, make(chan LogEntry))
		transport.RegisterServer(i, rafts[i])
	}

	// Create a complex partition:
	// - Server 0 is completely disconnected
	// - Servers 1 and 2 cannot communicate
	// - Server 3 can send to 4 but not receive responses

	transport.DisconnectServer(0)
	transport.DisconnectPair(1, 2)
	transport.SetAsymmetricPartition(4, 3, true)

	args := &RequestVoteArgs{Term: 1, CandidateID: 1}
	reply := &RequestVoteReply{}

	// Test various communication paths
	testCases := []struct {
		from     int
		to       int
		expected bool
		desc     string
	}{
		{0, 1, false, "from disconnected server"},
		{1, 0, false, "to disconnected server"},
		{1, 2, false, "between partitioned pair"},
		{2, 1, false, "between partitioned pair (reverse)"},
		{1, 3, true, "from 1 to 3 should work"},
		{3, 4, false, "from 3 to 4 should fail because response from 4 to 3 is blocked"},
		{4, 3, false, "from 4 to 3 blocked by asymmetric partition"},
		{3, 1, true, "from 3 to 1 should work"},
	}

	for _, tc := range testCases {
		success := transport.SendRequestVote(tc.from, tc.to, args, reply)
		if success != tc.expected {
			t.Errorf("%s: expected %v, got %v", tc.desc, tc.expected, success)
		}
	}
}

// TestConcurrentAccess tests concurrent access to transport
func TestConcurrentAccess(t *testing.T) {
	transport := NewTestTransport()

	// Create and register servers
	peers := []int{0, 1, 2}
	rafts := make([]*Raft, 3)
	for i := 0; i < 3; i++ {
		rafts[i] = NewRaft(peers, i, make(chan LogEntry))
		transport.RegisterServer(i, rafts[i])
	}

	// Run concurrent operations
	done := make(chan bool, 10)

	// Concurrent disconnects/reconnects
	go func() {
		for i := 0; i < 100; i++ {
			transport.DisconnectServer(0)
			transport.ReconnectServer(0)
		}
		done <- true
	}()

	// Concurrent partition operations
	go func() {
		for i := 0; i < 100; i++ {
			transport.DisconnectPair(1, 2)
			transport.ReconnectPair(1, 2)
		}
		done <- true
	}()

	// Concurrent asymmetric partitions
	go func() {
		for i := 0; i < 100; i++ {
			transport.SetAsymmetricPartition(0, 1, true)
			transport.SetAsymmetricPartition(0, 1, false)
		}
		done <- true
	}()

	// Concurrent RPCs
	for j := 0; j < 3; j++ {
		go func(from int) {
			args := &RequestVoteArgs{Term: 1, CandidateID: from}
			reply := &RequestVoteReply{}
			for i := 0; i < 100; i++ {
				to := (from + 1) % 3
				transport.SendRequestVote(from, to, args, reply)
			}
			done <- true
		}(j)
	}

	// Wait for all goroutines to complete
	for i := 0; i < 6; i++ {
		<-done
	}

	// If we get here without deadlock or panic, the test passes
}

// TestRPCFailureScenarios tests various RPC failure scenarios
func TestRPCFailureScenarios(t *testing.T) {
	t.Run("RequestVote failures", func(t *testing.T) {
		transport := NewTestTransport()

		// No servers registered
		args := &RequestVoteArgs{Term: 1, CandidateID: 0}
		reply := &RequestVoteReply{}

		success := transport.SendRequestVote(0, 1, args, reply)
		if success {
			t.Error("RequestVote should fail when no servers registered")
		}
	})

	t.Run("AppendEntries failures", func(t *testing.T) {
		transport := NewTestTransport()

		// No servers registered
		args := &AppendEntriesArgs{Term: 1, LeaderID: 0}
		reply := &AppendEntriesReply{}

		success := transport.SendAppendEntries(0, 1, args, reply)
		if success {
			t.Error("AppendEntries should fail when no servers registered")
		}
	})

	t.Run("InstallSnapshot failures", func(t *testing.T) {
		transport := NewTestTransport()

		// No servers registered
		args := &InstallSnapshotArgs{Term: 1, LeaderID: 0}
		reply := &InstallSnapshotReply{}

		success := transport.SendInstallSnapshot(0, 1, args, reply)
		if success {
			t.Error("InstallSnapshot should fail when no servers registered")
		}
	})
}

// TestTransportWithFullRaftIntegration tests transport with full Raft integration
func TestTransportWithFullRaftIntegration(t *testing.T) {
	transport := NewTestTransport()

	// Create TestRaft instances
	peers := []int{0, 1, 2}
	rafts := make([]*TestRaft, 3)
	applyChannels := make([]chan LogEntry, 3)

	for i := 0; i < 3; i++ {
		applyChannels[i] = make(chan LogEntry, 100)
		rafts[i] = NewTestRaft(peers, i, applyChannels[i], transport)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start all servers
	for i := 0; i < 3; i++ {
		rafts[i].Start(ctx)
	}

	// Wait for leader election
	time.Sleep(500 * time.Millisecond)

	// Find the leader
	leaderIdx := -1
	for i := 0; i < 3; i++ {
		_, isLeader := rafts[i].GetState()
		if isLeader {
			leaderIdx = i
			break
		}
	}

	if leaderIdx == -1 {
		t.Fatal("No leader elected")
	}

	t.Logf("Leader elected: server %d", leaderIdx)

	// Test partition scenarios
	t.Run("Leader isolation", func(t *testing.T) {
		// Isolate the leader
		transport.DisconnectServer(leaderIdx)

		// Wait for new leader
		time.Sleep(1 * time.Second)

		// Check for new leader among remaining servers
		newLeaderFound := false
		for i := 0; i < 3; i++ {
			if i != leaderIdx {
				_, isLeader := rafts[i].GetState()
				if isLeader {
					newLeaderFound = true
					t.Logf("New leader elected: server %d", i)
					break
				}
			}
		}

		if !newLeaderFound {
			t.Error("No new leader elected after leader isolation")
		}

		// Reconnect old leader
		transport.ReconnectServer(leaderIdx)
	})

	// Stop all servers
	for i := 0; i < 3; i++ {
		rafts[i].Stop()
	}
}
