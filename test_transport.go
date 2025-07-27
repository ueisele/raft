package raft

import (
	"sync"
)

// TestTransport provides in-memory RPC transport for testing
type TestTransport struct {
	mu               sync.RWMutex
	servers          map[int]*Raft
	disconnectedServers map[int]bool           // Servers that are completely disconnected
	partitions       map[int]map[int]bool     // [from][to] -> blocked (symmetric)
	asymmetricPartitions map[int]map[int]bool // [from][to] -> blocked (one-way)
}

// NewTestTransport creates a new test transport
func NewTestTransport() *TestTransport {
	return &TestTransport{
		servers:             make(map[int]*Raft),
		disconnectedServers: make(map[int]bool),
		partitions:          make(map[int]map[int]bool),
		asymmetricPartitions: make(map[int]map[int]bool),
	}
}

// RegisterServer registers a Raft server with the transport
func (t *TestTransport) RegisterServer(id int, server *Raft) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.servers[id] = server
}

// SendRequestVote sends a RequestVote RPC to the target server
func (t *TestTransport) SendRequestVote(from, to int, args *RequestVoteArgs, reply *RequestVoteReply) bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	
	// Check if either server is disconnected
	if t.disconnectedServers[from] || t.disconnectedServers[to] {
		return false
	}
	
	// Check for symmetric partition
	if fromPartitions, exists := t.partitions[from]; exists {
		if fromPartitions[to] {
			return false
		}
	}
	
	// Check for asymmetric partition on the forward path
	if asymPartitions, exists := t.asymmetricPartitions[from]; exists {
		if asymPartitions[to] {
			return false
		}
	}
	
	target, exists := t.servers[to]
	if !exists {
		return false
	}
	
	// Simulate network call
	err := target.RequestVote(args, reply)
	if err != nil {
		return false
	}
	
	// Check for asymmetric partition on the return path (response blocked)
	if asymPartitions, exists := t.asymmetricPartitions[to]; exists {
		if asymPartitions[from] {
			// The RPC was processed but the response is blocked
			return false
		}
	}
	
	return true
}

// SendAppendEntries sends an AppendEntries RPC to the target server
func (t *TestTransport) SendAppendEntries(from, to int, args *AppendEntriesArgs, reply *AppendEntriesReply) bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	
	// Check if either server is disconnected
	if t.disconnectedServers[from] || t.disconnectedServers[to] {
		return false
	}
	
	// Check for symmetric partition
	if fromPartitions, exists := t.partitions[from]; exists {
		if fromPartitions[to] {
			return false
		}
	}
	
	// Check for asymmetric partition on the forward path
	if asymPartitions, exists := t.asymmetricPartitions[from]; exists {
		if asymPartitions[to] {
			return false
		}
	}
	
	target, exists := t.servers[to]
	if !exists {
		return false
	}
	
	// Simulate network call
	err := target.AppendEntries(args, reply)
	if err != nil {
		return false
	}
	
	// Check for asymmetric partition on the return path (response blocked)
	if asymPartitions, exists := t.asymmetricPartitions[to]; exists {
		if asymPartitions[from] {
			// The RPC was processed but the response is blocked
			return false
		}
	}
	
	return true
}

// SendInstallSnapshot sends an InstallSnapshot RPC to the target server
func (t *TestTransport) SendInstallSnapshot(from, to int, args *InstallSnapshotArgs, reply *InstallSnapshotReply) bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	
	// Check if either server is disconnected
	if t.disconnectedServers[from] || t.disconnectedServers[to] {
		return false
	}
	
	// Check for symmetric partition
	if fromPartitions, exists := t.partitions[from]; exists {
		if fromPartitions[to] {
			return false
		}
	}
	
	// Check for asymmetric partition on the forward path
	if asymPartitions, exists := t.asymmetricPartitions[from]; exists {
		if asymPartitions[to] {
			return false
		}
	}
	
	target, exists := t.servers[to]
	if !exists {
		return false
	}
	
	// Simulate network call
	err := target.InstallSnapshot(args, reply)
	if err != nil {
		return false
	}
	
	// Check for asymmetric partition on the return path (response blocked)
	if asymPartitions, exists := t.asymmetricPartitions[to]; exists {
		if asymPartitions[from] {
			// The RPC was processed but the response is blocked
			return false
		}
	}
	
	return true
}

// TestRaft extends Raft with test-specific transport
type TestRaft struct {
	*Raft
	transport *TestTransport
}

// NewTestRaft creates a new Raft instance for testing
func NewTestRaft(peers []int, me int, applyCh chan LogEntry, transport *TestTransport) *TestRaft {
	rf := NewRaft(peers, me, applyCh)
	testRf := &TestRaft{
		Raft:      rf,
		transport: transport,
	}
	
	// Override RPC methods to use test transport
	testRf.setupTestTransport()
	
	// Register with transport
	transport.RegisterServer(me, rf)
	
	return testRf
}

// setupTestTransport overrides the RPC methods to use test transport
func (tr *TestRaft) setupTestTransport() {
	// We need to replace the sendRequestVote and sendAppendEntries methods
	// Since Go doesn't support method overriding directly, we'll modify the Raft struct
	tr.Raft.sendRequestVote = tr.testSendRequestVote
	tr.Raft.sendAppendEntries = tr.testSendAppendEntries
	tr.Raft.sendInstallSnapshotFn = tr.testSendInstallSnapshot
}

// testSendRequestVote implements RequestVote RPC using test transport
func (tr *TestRaft) testSendRequestVote(peer int, args *RequestVoteArgs, reply *RequestVoteReply) bool {
	return tr.transport.SendRequestVote(tr.me, peer, args, reply)
}

// testSendAppendEntries implements AppendEntries RPC using test transport
func (tr *TestRaft) testSendAppendEntries(peer int, args *AppendEntriesArgs, reply *AppendEntriesReply) bool {
	return tr.transport.SendAppendEntries(tr.me, peer, args, reply)
}

// testSendInstallSnapshot implements InstallSnapshot RPC using test transport
func (tr *TestRaft) testSendInstallSnapshot(peer int, args *InstallSnapshotArgs, reply *InstallSnapshotReply) bool {
	return tr.transport.SendInstallSnapshot(tr.me, peer, args, reply)
}

// DisconnectServer simulates a server going offline
func (t *TestTransport) DisconnectServer(serverID int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.disconnectedServers[serverID] = true
}

// ReconnectServer simulates a server coming back online
func (t *TestTransport) ReconnectServer(serverID int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.disconnectedServers, serverID)
}

// DisconnectPair creates a partition between two specific servers
func (t *TestTransport) DisconnectPair(server1, server2 int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	
	if t.partitions[server1] == nil {
		t.partitions[server1] = make(map[int]bool)
	}
	if t.partitions[server2] == nil {
		t.partitions[server2] = make(map[int]bool)
	}
	
	t.partitions[server1][server2] = true
	t.partitions[server2][server1] = true
}

// ReconnectPair removes a partition between two specific servers
func (t *TestTransport) ReconnectPair(server1, server2 int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	
	if t.partitions[server1] != nil {
		delete(t.partitions[server1], server2)
	}
	if t.partitions[server2] != nil {
		delete(t.partitions[server2], server1)
	}
}

// SetAsymmetricPartition creates or removes a one-way partition
// If set is true, from cannot send to to (but to can still send to from)
func (t *TestTransport) SetAsymmetricPartition(from, to int, set bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	
	if set {
		if t.asymmetricPartitions[from] == nil {
			t.asymmetricPartitions[from] = make(map[int]bool)
		}
		t.asymmetricPartitions[from][to] = true
	} else {
		if t.asymmetricPartitions[from] != nil {
			delete(t.asymmetricPartitions[from], to)
		}
	}
}