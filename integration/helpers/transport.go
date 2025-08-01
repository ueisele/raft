package helpers

import (
	"fmt"
	"sync"

	"github.com/ueisele/raft"
)

// ========== Multi-node transport for integration tests ==========

// MultiNodeTransport allows nodes to communicate in integration tests
type MultiNodeTransport struct {
	id       int
	registry *NodeRegistry
	handler  raft.RPCHandler
}

// NodeRegistry manages node connections for testing
type NodeRegistry struct {
	mu    sync.RWMutex
	nodes map[int]raft.RPCHandler
}

// NewNodeRegistry creates a new node registry
func NewNodeRegistry() *NodeRegistry {
	return &NodeRegistry{
		nodes: make(map[int]raft.RPCHandler),
	}
}

// Register adds a node to the registry
func (r *NodeRegistry) Register(id int, handler raft.RPCHandler) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.nodes[id] = handler
}

// NewMultiNodeTransport creates a new multi-node transport
func NewMultiNodeTransport(id int, registry *NodeRegistry) *MultiNodeTransport {
	return &MultiNodeTransport{
		id:       id,
		registry: registry,
	}
}

func (t *MultiNodeTransport) SendRequestVote(serverID int, args *raft.RequestVoteArgs) (*raft.RequestVoteReply, error) {
	t.registry.mu.RLock()
	handler, exists := t.registry.nodes[serverID]
	t.registry.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("node %d not found", serverID)
	}

	reply := &raft.RequestVoteReply{}
	err := handler.RequestVote(args, reply)
	return reply, err
}

func (t *MultiNodeTransport) SendAppendEntries(serverID int, args *raft.AppendEntriesArgs) (*raft.AppendEntriesReply, error) {
	t.registry.mu.RLock()
	handler, exists := t.registry.nodes[serverID]
	t.registry.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("node %d not found", serverID)
	}

	reply := &raft.AppendEntriesReply{}
	err := handler.AppendEntries(args, reply)
	return reply, err
}

func (t *MultiNodeTransport) SendInstallSnapshot(serverID int, args *raft.InstallSnapshotArgs) (*raft.InstallSnapshotReply, error) {
	t.registry.mu.RLock()
	handler, exists := t.registry.nodes[serverID]
	t.registry.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("node %d not found", serverID)
	}

	reply := &raft.InstallSnapshotReply{}
	err := handler.InstallSnapshot(args, reply)
	return reply, err
}

func (t *MultiNodeTransport) SetRPCHandler(handler raft.RPCHandler) {
	t.handler = handler
}

func (t *MultiNodeTransport) Start() error {
	return nil
}

func (t *MultiNodeTransport) Stop() error {
	return nil
}

func (t *MultiNodeTransport) GetAddress() string {
	return fmt.Sprintf("node-%d", t.id)
}

// ========== Debug transport with logging ==========

// DebugTransport wraps a transport with debug logging
type DebugTransport struct {
	id       int
	registry *DebugNodeRegistry
	handler  raft.RPCHandler
	logger   raft.Logger
}

// DebugNodeRegistry is a node registry with debug logging
type DebugNodeRegistry struct {
	mu     sync.RWMutex
	nodes  map[int]raft.RPCHandler
	logger raft.Logger
}

// NewDebugNodeRegistry creates a new debug node registry
func NewDebugNodeRegistry(logger raft.Logger) *DebugNodeRegistry {
	return &DebugNodeRegistry{
		nodes:  make(map[int]raft.RPCHandler),
		logger: logger,
	}
}

// Register adds a node to the debug registry
func (r *DebugNodeRegistry) Register(id int, handler raft.RPCHandler) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.nodes[id] = handler
	if r.logger != nil {
		r.logger.Debug("Registered node %d", id)
	}
}

// NewDebugTransport creates a new debug transport
func NewDebugTransport(id int, registry *DebugNodeRegistry, logger raft.Logger) *DebugTransport {
	return &DebugTransport{
		id:       id,
		registry: registry,
		logger:   logger,
	}
}

func (t *DebugTransport) SendRequestVote(serverID int, args *raft.RequestVoteArgs) (*raft.RequestVoteReply, error) {
	if t.logger != nil {
		t.logger.Debug("Node %d sending RequestVote to node %d: term=%d", t.id, serverID, args.Term)
	}

	t.registry.mu.RLock()
	handler, exists := t.registry.nodes[serverID]
	t.registry.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("node %d not found", serverID)
	}

	reply := &raft.RequestVoteReply{}
	err := handler.RequestVote(args, reply)
	
	if t.logger != nil && err == nil {
		t.logger.Debug("Node %d received RequestVote reply from node %d: granted=%v, term=%d", 
			t.id, serverID, reply.VoteGranted, reply.Term)
	}
	
	return reply, err
}

func (t *DebugTransport) SendAppendEntries(serverID int, args *raft.AppendEntriesArgs) (*raft.AppendEntriesReply, error) {
	if t.logger != nil {
		t.logger.Debug("Node %d sending AppendEntries to node %d: term=%d, prevLogIndex=%d, entries=%d", 
			t.id, serverID, args.Term, args.PrevLogIndex, len(args.Entries))
	}

	t.registry.mu.RLock()
	handler, exists := t.registry.nodes[serverID]
	t.registry.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("node %d not found", serverID)
	}

	reply := &raft.AppendEntriesReply{}
	err := handler.AppendEntries(args, reply)
	
	if t.logger != nil && err == nil {
		t.logger.Debug("Node %d received AppendEntries reply from node %d: success=%v, term=%d", 
			t.id, serverID, reply.Success, reply.Term)
	}
	
	return reply, err
}

func (t *DebugTransport) SendInstallSnapshot(serverID int, args *raft.InstallSnapshotArgs) (*raft.InstallSnapshotReply, error) {
	if t.logger != nil {
		t.logger.Debug("Node %d sending InstallSnapshot to node %d: term=%d, lastIndex=%d", 
			t.id, serverID, args.Term, args.LastIncludedIndex)
	}

	t.registry.mu.RLock()
	handler, exists := t.registry.nodes[serverID]
	t.registry.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("node %d not found", serverID)
	}

	reply := &raft.InstallSnapshotReply{}
	err := handler.InstallSnapshot(args, reply)
	
	if t.logger != nil && err == nil {
		t.logger.Debug("Node %d received InstallSnapshot reply from node %d: term=%d", 
			t.id, serverID, reply.Term)
	}
	
	return reply, err
}

func (t *DebugTransport) SetRPCHandler(handler raft.RPCHandler) {
	t.handler = handler
}

func (t *DebugTransport) Start() error {
	return nil
}

func (t *DebugTransport) Stop() error {
	return nil
}

func (t *DebugTransport) GetAddress() string {
	return fmt.Sprintf("debug-node-%d", t.id)
}

// ========== Partitionable transport for fault injection ==========

// PartitionableTransport simulates network partitions
type PartitionableTransport struct {
	id       int
	registry *PartitionRegistry
	handler  raft.RPCHandler
	mu       sync.Mutex
	blocked  map[int]bool
}

// PartitionRegistry manages partitionable connections
type PartitionRegistry struct {
	mu    sync.RWMutex
	nodes map[int]raft.RPCHandler
}

// NewPartitionRegistry creates a new partition registry
func NewPartitionRegistry() *PartitionRegistry {
	return &PartitionRegistry{
		nodes: make(map[int]raft.RPCHandler),
	}
}

// Register adds a node to the partition registry
func (r *PartitionRegistry) Register(id int, handler raft.RPCHandler) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.nodes[id] = handler
}

// NewPartitionableTransport creates a new partitionable transport
func NewPartitionableTransport(id int, registry *PartitionRegistry) *PartitionableTransport {
	return &PartitionableTransport{
		id:       id,
		registry: registry,
		blocked:  make(map[int]bool),
	}
}

// Block prevents communication with a specific server
func (t *PartitionableTransport) Block(serverID int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.blocked[serverID] = true
}

// Unblock allows communication with a specific server
func (t *PartitionableTransport) Unblock(serverID int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.blocked, serverID)
}

// BlockAll blocks communication with all servers
func (t *PartitionableTransport) BlockAll() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.registry.mu.RLock()
	for id := range t.registry.nodes {
		if id != t.id {
			t.blocked[id] = true
		}
	}
	t.registry.mu.RUnlock()
}

// UnblockAll unblocks communication with all servers
func (t *PartitionableTransport) UnblockAll() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.blocked = make(map[int]bool)
}

func (t *PartitionableTransport) SendRequestVote(serverID int, args *raft.RequestVoteArgs) (*raft.RequestVoteReply, error) {
	t.mu.Lock()
	blocked := t.blocked[serverID]
	t.mu.Unlock()

	if blocked {
		return nil, fmt.Errorf("network partition: cannot reach server %d", serverID)
	}

	t.registry.mu.RLock()
	handler, exists := t.registry.nodes[serverID]
	t.registry.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("server %d not found", serverID)
	}

	reply := &raft.RequestVoteReply{}
	err := handler.RequestVote(args, reply)
	return reply, err
}

func (t *PartitionableTransport) SendAppendEntries(serverID int, args *raft.AppendEntriesArgs) (*raft.AppendEntriesReply, error) {
	t.mu.Lock()
	blocked := t.blocked[serverID]
	t.mu.Unlock()

	if blocked {
		return nil, fmt.Errorf("network partition: cannot reach server %d", serverID)
	}

	t.registry.mu.RLock()
	handler, exists := t.registry.nodes[serverID]
	t.registry.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("server %d not found", serverID)
	}

	reply := &raft.AppendEntriesReply{}
	err := handler.AppendEntries(args, reply)
	return reply, err
}

func (t *PartitionableTransport) SendInstallSnapshot(serverID int, args *raft.InstallSnapshotArgs) (*raft.InstallSnapshotReply, error) {
	t.mu.Lock()
	blocked := t.blocked[serverID]
	t.mu.Unlock()

	if blocked {
		return nil, fmt.Errorf("network partition: cannot reach server %d", serverID)
	}

	t.registry.mu.RLock()
	handler, exists := t.registry.nodes[serverID]
	t.registry.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("server %d not found", serverID)
	}

	reply := &raft.InstallSnapshotReply{}
	err := handler.InstallSnapshot(args, reply)
	return reply, err
}

func (t *PartitionableTransport) SetRPCHandler(handler raft.RPCHandler) {
	t.handler = handler
}

func (t *PartitionableTransport) Start() error {
	return nil
}

func (t *PartitionableTransport) Stop() error {
	return nil
}

func (t *PartitionableTransport) GetAddress() string {
	return fmt.Sprintf("partition-transport-%d", t.id)
}