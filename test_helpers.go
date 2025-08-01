package raft

import (
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// MockTransport is a general-purpose transport mock for testing
type MockTransport struct {
	mu                     sync.Mutex
	serverID               int
	requestVoteHandler     func(serverID int, args *RequestVoteArgs) (*RequestVoteReply, error)
	appendEntriesHandler   func(serverID int, args *AppendEntriesArgs) (*AppendEntriesReply, error)
	installSnapshotHandler func(serverID int, args *InstallSnapshotArgs) (*InstallSnapshotReply, error)
	rpcHandler             RPCHandler
	started                bool

	// Track calls
	requestVoteCalls     []RequestVoteCall
	appendEntriesCalls   []AppendEntriesCall
	installSnapshotCalls []InstallSnapshotCall
}

type RequestVoteCall struct {
	ServerID int
	Args     *RequestVoteArgs
}

type AppendEntriesCall struct {
	ServerID int
	Args     *AppendEntriesArgs
}

type InstallSnapshotCall struct {
	ServerID int
	Args     *InstallSnapshotArgs
}

func NewMockTransport(serverID int) *MockTransport {
	return &MockTransport{
		serverID:             serverID,
		requestVoteCalls:     make([]RequestVoteCall, 0),
		appendEntriesCalls:   make([]AppendEntriesCall, 0),
		installSnapshotCalls: make([]InstallSnapshotCall, 0),
	}
}

func (t *MockTransport) SendRequestVote(serverID int, args *RequestVoteArgs) (*RequestVoteReply, error) {
	t.mu.Lock()
	t.requestVoteCalls = append(t.requestVoteCalls, RequestVoteCall{ServerID: serverID, Args: args})
	handler := t.requestVoteHandler
	t.mu.Unlock()

	if handler != nil {
		return handler(serverID, args)
	}

	return &RequestVoteReply{
		Term:        args.Term,
		VoteGranted: false,
	}, nil
}

func (t *MockTransport) SendAppendEntries(serverID int, args *AppendEntriesArgs) (*AppendEntriesReply, error) {
	t.mu.Lock()
	t.appendEntriesCalls = append(t.appendEntriesCalls, AppendEntriesCall{ServerID: serverID, Args: args})
	handler := t.appendEntriesHandler
	t.mu.Unlock()

	if handler != nil {
		return handler(serverID, args)
	}

	return &AppendEntriesReply{
		Term:    args.Term,
		Success: false,
	}, nil
}

func (t *MockTransport) SendInstallSnapshot(serverID int, args *InstallSnapshotArgs) (*InstallSnapshotReply, error) {
	t.mu.Lock()
	t.installSnapshotCalls = append(t.installSnapshotCalls, InstallSnapshotCall{ServerID: serverID, Args: args})
	handler := t.installSnapshotHandler
	t.mu.Unlock()

	if handler != nil {
		return handler(serverID, args)
	}

	return &InstallSnapshotReply{
		Term: args.Term,
	}, nil
}

func (t *MockTransport) SetRPCHandler(handler RPCHandler) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.rpcHandler = handler
}

func (t *MockTransport) Start() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.started = true
	return nil
}

func (t *MockTransport) Stop() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.started = false
	return nil
}

func (t *MockTransport) GetAddress() string {
	return fmt.Sprintf("mock-transport-%d", t.serverID)
}

// Handler setter methods
func (t *MockTransport) SetRequestVoteHandler(handler func(serverID int, args *RequestVoteArgs) (*RequestVoteReply, error)) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.requestVoteHandler = handler
}

func (t *MockTransport) SetAppendEntriesHandler(handler func(serverID int, args *AppendEntriesArgs) (*AppendEntriesReply, error)) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.appendEntriesHandler = handler
}

func (t *MockTransport) SetInstallSnapshotHandler(handler func(serverID int, args *InstallSnapshotArgs) (*InstallSnapshotReply, error)) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.installSnapshotHandler = handler
}

// Call tracking methods
func (t *MockTransport) GetRequestVoteCalls() []RequestVoteCall {
	t.mu.Lock()
	defer t.mu.Unlock()
	result := make([]RequestVoteCall, len(t.requestVoteCalls))
	copy(result, t.requestVoteCalls)
	return result
}

func (t *MockTransport) GetAppendEntriesCalls() []AppendEntriesCall {
	t.mu.Lock()
	defer t.mu.Unlock()
	result := make([]AppendEntriesCall, len(t.appendEntriesCalls))
	copy(result, t.appendEntriesCalls)
	return result
}

func (t *MockTransport) GetInstallSnapshotCalls() []InstallSnapshotCall {
	t.mu.Lock()
	defer t.mu.Unlock()
	result := make([]InstallSnapshotCall, len(t.installSnapshotCalls))
	copy(result, t.installSnapshotCalls)
	return result
}

// ClearCalls clears all recorded calls
func (t *MockTransport) ClearCalls() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.requestVoteCalls = make([]RequestVoteCall, 0)
	t.appendEntriesCalls = make([]AppendEntriesCall, 0)
	t.installSnapshotCalls = make([]InstallSnapshotCall, 0)
}

// ========== Test loggers ==========

// TestLogger is a logger that writes to testing.T
type TestLogger struct {
	t *testing.T
}

// NewTestLogger creates a new test logger
func NewTestLogger(t *testing.T) *TestLogger {
	return &TestLogger{t: t}
}

func (l *TestLogger) Debug(format string, args ...interface{}) {
	l.t.Logf("[DEBUG] "+format, args...)
}

func (l *TestLogger) Info(format string, args ...interface{}) {
	l.t.Logf("[INFO] "+format, args...)
}

func (l *TestLogger) Warn(format string, args ...interface{}) {
	l.t.Logf("[WARN] "+format, args...)
}

func (l *TestLogger) Error(format string, args ...interface{}) {
	l.t.Errorf("[ERROR] "+format, args...)
}

// SafeTestLogger is a thread-safe test logger
type SafeTestLogger struct {
	t    *testing.T
	done atomic.Bool
}

func NewSafeTestLogger(t *testing.T) *SafeTestLogger {
	return &SafeTestLogger{t: t}
}

func (l *SafeTestLogger) Stop() {
	l.done.Store(true)
}

func (l *SafeTestLogger) Debug(format string, args ...interface{}) {
	if !l.done.Load() {
		l.t.Logf("[DEBUG] "+format, args...)
	}
}

func (l *SafeTestLogger) Info(format string, args ...interface{}) {
	if !l.done.Load() {
		l.t.Logf("[INFO] "+format, args...)
	}
}

func (l *SafeTestLogger) Warn(format string, args ...interface{}) {
	if !l.done.Load() {
		l.t.Logf("[WARN] "+format, args...)
	}
}

func (l *SafeTestLogger) Error(format string, args ...interface{}) {
	if !l.done.Load() {
		l.t.Errorf("[ERROR] "+format, args...)
	}
}

// MockPersistence is a general-purpose persistence mock for testing
type MockPersistence struct {
	mu             sync.Mutex
	state          *PersistentState
	snapshot       *Snapshot
	saveStateCount int
	loadStateCount int
	saveSnapCount  int
	loadSnapCount  int
	failNextSave   bool
	failNextLoad   bool
}

func NewMockPersistence() *MockPersistence {
	return &MockPersistence{}
}

func (m *MockPersistence) SaveState(state *PersistentState) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.saveStateCount++

	if m.failNextSave {
		m.failNextSave = false
		return fmt.Errorf("mock save failure")
	}

	// Deep copy the state
	if state != nil {
		m.state = &PersistentState{
			CurrentTerm: state.CurrentTerm,
			VotedFor:    state.VotedFor,
			CommitIndex: state.CommitIndex,
		}
		if state.Log != nil {
			m.state.Log = make([]LogEntry, len(state.Log))
			copy(m.state.Log, state.Log)
		}
	}

	return nil
}

func (m *MockPersistence) LoadState() (*PersistentState, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.loadStateCount++

	if m.failNextLoad {
		m.failNextLoad = false
		return nil, fmt.Errorf("mock load failure")
	}

	if m.state == nil {
		return nil, nil
	}

	// Deep copy the state
	result := &PersistentState{
		CurrentTerm: m.state.CurrentTerm,
		VotedFor:    m.state.VotedFor,
		CommitIndex: m.state.CommitIndex,
	}
	if m.state.Log != nil {
		result.Log = make([]LogEntry, len(m.state.Log))
		copy(result.Log, m.state.Log)
	}

	return result, nil
}

func (m *MockPersistence) SaveSnapshot(snapshot *Snapshot) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.saveSnapCount++

	if m.failNextSave {
		m.failNextSave = false
		return fmt.Errorf("mock snapshot save failure")
	}

	if snapshot != nil {
		m.snapshot = &Snapshot{
			LastIncludedIndex: snapshot.LastIncludedIndex,
			LastIncludedTerm:  snapshot.LastIncludedTerm,
			Data:              make([]byte, len(snapshot.Data)),
		}
		copy(m.snapshot.Data, snapshot.Data)
	}

	return nil
}

func (m *MockPersistence) LoadSnapshot() (*Snapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.loadSnapCount++

	if m.failNextLoad {
		m.failNextLoad = false
		return nil, fmt.Errorf("mock snapshot load failure")
	}

	if m.snapshot == nil {
		return nil, nil
	}

	result := &Snapshot{
		LastIncludedIndex: m.snapshot.LastIncludedIndex,
		LastIncludedTerm:  m.snapshot.LastIncludedTerm,
		Data:              make([]byte, len(m.snapshot.Data)),
	}
	copy(result.Data, m.snapshot.Data)

	return result, nil
}

func (m *MockPersistence) HasSnapshot() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.snapshot != nil
}

// Helper methods for testing
func (m *MockPersistence) GetSaveCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.saveStateCount
}

func (m *MockPersistence) GetLoadCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.loadStateCount
}

func (m *MockPersistence) FailNextSave() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.failNextSave = true
}

func (m *MockPersistence) FailNextLoad() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.failNextLoad = true
}

// MockStateMachine is a test implementation of StateMachine
type MockStateMachine struct {
	mu                 sync.RWMutex
	state              map[string]interface{}
	applyCount         int
	applyDelay         time.Duration
	failNextApply      bool
	captureAppliedCmds []interface{}
}

func NewMockStateMachine() *MockStateMachine {
	return &MockStateMachine{
		state:              make(map[string]interface{}),
		captureAppliedCmds: make([]interface{}, 0),
	}
}

func (m *MockStateMachine) Apply(entry LogEntry) interface{} {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.applyDelay > 0 {
		time.Sleep(m.applyDelay)
	}

	m.applyCount++
	m.captureAppliedCmds = append(m.captureAppliedCmds, entry.Command)

	if m.failNextApply {
		m.failNextApply = false
		return fmt.Errorf("mock apply failure")
	}

	// Simple key-value store simulation
	if cmd, ok := entry.Command.(string); ok {
		parts := strings.SplitN(cmd, " ", 3)
		if len(parts) >= 2 {
			switch parts[0] {
			case "SET":
				if len(parts) == 3 {
					key := parts[1]
					value := parts[2]
					m.state[key] = value
					return fmt.Sprintf("OK: %s=%s", key, value)
				}
			case "GET":
				key := parts[1]
				if value, exists := m.state[key]; exists {
					return value
				}
				return nil
			case "DELETE":
				key := parts[1]
				delete(m.state, key)
				return "OK"
			}
		}
	}

	// Store any command type
	if key, ok := entry.Command.(string); ok {
		m.state[key] = entry.Command
	}

	return entry.Command
}

func (m *MockStateMachine) Snapshot() ([]byte, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Simple snapshot: just count of entries
	return []byte(fmt.Sprintf("entries:%d", len(m.state))), nil
}

func (m *MockStateMachine) Restore(data []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.state = make(map[string]interface{})
	// For simplicity, we just clear the state
	return nil
}

// GetData returns the current state map (for testing)
func (m *MockStateMachine) GetData() map[string]interface{} {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Return a copy to avoid external modifications
	result := make(map[string]interface{})
	for k, v := range m.state {
		result[k] = v
	}
	return result
}

// Helper methods for testing
func (m *MockStateMachine) GetApplyCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.applyCount
}

func (m *MockStateMachine) SetApplyDelay(delay time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.applyDelay = delay
}

func (m *MockStateMachine) FailNextApply() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.failNextApply = true
}

func (m *MockStateMachine) GetAppliedCommands() []interface{} {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]interface{}, len(m.captureAppliedCmds))
	copy(result, m.captureAppliedCmds)
	return result
}

// ========== Test helpers for multi-node clusters ==========

type nodeRegistry struct {
	mu    sync.RWMutex
	nodes map[int]RPCHandler
}

type multiNodeTransport struct {
	id       int
	registry *nodeRegistry
	handler  RPCHandler
}

func (t *multiNodeTransport) SendRequestVote(serverID int, args *RequestVoteArgs) (*RequestVoteReply, error) {
	t.registry.mu.RLock()
	handler, exists := t.registry.nodes[serverID]
	t.registry.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("node %d not found", serverID)
	}

	reply := &RequestVoteReply{}
	err := handler.RequestVote(args, reply)
	return reply, err
}

func (t *multiNodeTransport) SendAppendEntries(serverID int, args *AppendEntriesArgs) (*AppendEntriesReply, error) {
	t.registry.mu.RLock()
	handler, exists := t.registry.nodes[serverID]
	t.registry.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("node %d not found", serverID)
	}

	reply := &AppendEntriesReply{}
	err := handler.AppendEntries(args, reply)
	return reply, err
}

func (t *multiNodeTransport) SendInstallSnapshot(serverID int, args *InstallSnapshotArgs) (*InstallSnapshotReply, error) {
	t.registry.mu.RLock()
	handler, exists := t.registry.nodes[serverID]
	t.registry.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("node %d not found", serverID)
	}

	reply := &InstallSnapshotReply{}
	err := handler.InstallSnapshot(args, reply)
	return reply, err
}

func (t *multiNodeTransport) SetRPCHandler(handler RPCHandler) {
	t.handler = handler
}

func (t *multiNodeTransport) Start() error {
	return nil
}

func (t *multiNodeTransport) Stop() error {
	return nil
}

func (t *multiNodeTransport) GetAddress() string {
	return fmt.Sprintf("node-%d", t.id)
}

// ========== Debug test helpers (from debug_multi_test.go) ==========

// For backward compatibility
type testLogger = TestLogger

func newTestLogger(t *testing.T) *testLogger {
	return NewTestLogger(t)
}

type debugNodeRegistry struct {
	mu     sync.RWMutex
	nodes  map[int]RPCHandler
	logger Logger
}

type debugTransport struct {
	id       int
	registry *debugNodeRegistry
	handler  RPCHandler
	logger   Logger
}

func (t *debugTransport) SendRequestVote(serverID int, args *RequestVoteArgs) (*RequestVoteReply, error) {
	t.logger.Debug("Node %d sending RequestVote to node %d: term=%d", t.id, serverID, args.Term)

	t.registry.mu.RLock()
	handler, exists := t.registry.nodes[serverID]
	t.registry.mu.RUnlock()

	if !exists {
		t.logger.Debug("Node %d not found", serverID)
		return nil, fmt.Errorf("node %d not found", serverID)
	}

	reply := &RequestVoteReply{}
	err := handler.RequestVote(args, reply)

	t.logger.Debug("Node %d received RequestVote reply from node %d: granted=%v, term=%d",
		t.id, serverID, reply.VoteGranted, reply.Term)

	return reply, err
}

func (t *debugTransport) SendAppendEntries(serverID int, args *AppendEntriesArgs) (*AppendEntriesReply, error) {
	t.registry.mu.RLock()
	handler, exists := t.registry.nodes[serverID]
	t.registry.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("node %d not found", serverID)
	}

	reply := &AppendEntriesReply{}
	err := handler.AppendEntries(args, reply)
	return reply, err
}

func (t *debugTransport) SendInstallSnapshot(serverID int, args *InstallSnapshotArgs) (*InstallSnapshotReply, error) {
	t.registry.mu.RLock()
	handler, exists := t.registry.nodes[serverID]
	t.registry.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("node %d not found", serverID)
	}

	reply := &InstallSnapshotReply{}
	err := handler.InstallSnapshot(args, reply)
	return reply, err
}

func (t *debugTransport) SetRPCHandler(handler RPCHandler) {
	t.handler = handler
}

func (t *debugTransport) Start() error {
	return nil
}

func (t *debugTransport) Stop() error {
	return nil
}

func (t *debugTransport) GetAddress() string {
	return fmt.Sprintf("debug-node-%d", t.id)
}

// ========== Partitionable transport ==========

type partitionRegistry struct {
	mu    sync.RWMutex
	nodes map[int]RPCHandler
}

type partitionableTransport struct {
	id       int
	registry *partitionRegistry
	handler  RPCHandler
	mu       sync.Mutex
	blocked  map[int]bool
}

func (t *partitionableTransport) Block(serverID int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.blocked[serverID] = true
}

func (t *partitionableTransport) Unblock(serverID int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.blocked, serverID)
}

func (t *partitionableTransport) BlockAll() {
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

func (t *partitionableTransport) UnblockAll() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.blocked = make(map[int]bool)
}

func (t *partitionableTransport) SendRequestVote(serverID int, args *RequestVoteArgs) (*RequestVoteReply, error) {
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

	reply := &RequestVoteReply{}
	err := handler.RequestVote(args, reply)
	return reply, err
}

func (t *partitionableTransport) SendAppendEntries(serverID int, args *AppendEntriesArgs) (*AppendEntriesReply, error) {
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

	reply := &AppendEntriesReply{}
	err := handler.AppendEntries(args, reply)
	return reply, err
}

func (t *partitionableTransport) SendInstallSnapshot(serverID int, args *InstallSnapshotArgs) (*InstallSnapshotReply, error) {
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

	reply := &InstallSnapshotReply{}
	err := handler.InstallSnapshot(args, reply)
	return reply, err
}

func (t *partitionableTransport) SetRPCHandler(handler RPCHandler) {
	t.handler = handler
}

func (t *partitionableTransport) Start() error {
	return nil
}

func (t *partitionableTransport) Stop() error {
	return nil
}

func (t *partitionableTransport) GetAddress() string {
	return fmt.Sprintf("partition-transport-%d", t.id)
}

// ========== Simple test transport for unit tests ==========

// testTransport is a minimal transport for unit testing
type testTransport struct {
	responses map[int]*RequestVoteReply
	handler   RPCHandler
}

func (t *testTransport) SendRequestVote(serverID int, args *RequestVoteArgs) (*RequestVoteReply, error) {
	if reply, ok := t.responses[serverID]; ok {
		return reply, nil
	}
	return &RequestVoteReply{Term: args.Term, VoteGranted: false}, nil
}

func (t *testTransport) SendAppendEntries(serverID int, args *AppendEntriesArgs) (*AppendEntriesReply, error) {
	return &AppendEntriesReply{Term: args.Term, Success: false}, nil
}

func (t *testTransport) SendInstallSnapshot(serverID int, args *InstallSnapshotArgs) (*InstallSnapshotReply, error) {
	return &InstallSnapshotReply{Term: args.Term}, nil
}

func (t *testTransport) SetRPCHandler(handler RPCHandler) {
	t.handler = handler
}

func (t *testTransport) Start() error {
	return nil
}

func (t *testTransport) Stop() error {
	return nil
}

func (t *testTransport) GetAddress() string {
	return "test-transport"
}

// ========== Simple test state machine for unit tests ==========

// testStateMachine is a minimal state machine for unit testing
type testStateMachine struct {
	mu   sync.RWMutex
	data map[string]string
}

func (s *testStateMachine) Apply(entry LogEntry) interface{} {
	s.mu.Lock()
	defer s.mu.Unlock()

	if cmd, ok := entry.Command.(string); ok {
		parts := strings.Split(cmd, "=")
		if len(parts) == 2 && parts[0] == "SET" {
			s.data[parts[1]] = parts[1]
			return parts[1]
		}
	}
	return nil
}

func (s *testStateMachine) Snapshot() ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Simple snapshot: just count entries
	return []byte(fmt.Sprintf("entries:%d", len(s.data))), nil
}

func (s *testStateMachine) Restore(data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.data = make(map[string]string)
	// Simple restore: just clear data
	return nil
}
