package raft

import (
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// MockTransport is a mock implementation of Transport for testing
type MockTransport struct {
	mu                     sync.Mutex
	serverID               int
	requestVoteCalls       []RequestVoteCall
	appendEntriesCalls     []AppendEntriesCall
	installSnapshotCalls   []InstallSnapshotCall
	requestVoteHandler     func(serverID int, args *RequestVoteArgs) (*RequestVoteReply, error)
	appendEntriesHandler   func(serverID int, args *AppendEntriesArgs) (*AppendEntriesReply, error)
	installSnapshotHandler func(serverID int, args *InstallSnapshotArgs) (*InstallSnapshotReply, error)
	handler                RPCHandler
	started                bool
	stopped                bool
}

// RequestVoteCall records a RequestVote RPC call
type RequestVoteCall struct {
	ServerID int
	Args     *RequestVoteArgs
}

// AppendEntriesCall records an AppendEntries RPC call
type AppendEntriesCall struct {
	ServerID int
	Args     *AppendEntriesArgs
}

// InstallSnapshotCall records an InstallSnapshot RPC call
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

	// Default response
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

	// Default response
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

	// Default response
	return &InstallSnapshotReply{
		Term: args.Term,
	}, nil
}

func (t *MockTransport) SetRPCHandler(handler RPCHandler) {
	t.mu.Lock()
	t.handler = handler
	t.mu.Unlock()
}

func (t *MockTransport) Start() error {
	t.mu.Lock()
	t.started = true
	t.mu.Unlock()
	return nil
}

func (t *MockTransport) Stop() error {
	t.mu.Lock()
	t.stopped = true
	t.mu.Unlock()
	return nil
}

func (t *MockTransport) GetAddress() string {
	return fmt.Sprintf("mock-transport-%d", t.serverID)
}

// Test helper methods
func (t *MockTransport) SetRequestVoteHandler(handler func(serverID int, args *RequestVoteArgs) (*RequestVoteReply, error)) {
	t.mu.Lock()
	t.requestVoteHandler = handler
	t.mu.Unlock()
}

func (t *MockTransport) SetAppendEntriesHandler(handler func(serverID int, args *AppendEntriesArgs) (*AppendEntriesReply, error)) {
	t.mu.Lock()
	t.appendEntriesHandler = handler
	t.mu.Unlock()
}

func (t *MockTransport) SetInstallSnapshotHandler(handler func(serverID int, args *InstallSnapshotArgs) (*InstallSnapshotReply, error)) {
	t.mu.Lock()
	t.installSnapshotHandler = handler
	t.mu.Unlock()
}

// Getters for test assertions
func (t *MockTransport) GetRequestVoteCalls() []RequestVoteCall {
	t.mu.Lock()
	defer t.mu.Unlock()
	calls := make([]RequestVoteCall, len(t.requestVoteCalls))
	copy(calls, t.requestVoteCalls)
	return calls
}

func (t *MockTransport) GetAppendEntriesCalls() []AppendEntriesCall {
	t.mu.Lock()
	defer t.mu.Unlock()
	calls := make([]AppendEntriesCall, len(t.appendEntriesCalls))
	copy(calls, t.appendEntriesCalls)
	return calls
}

func (t *MockTransport) GetInstallSnapshotCalls() []InstallSnapshotCall {
	t.mu.Lock()
	defer t.mu.Unlock()
	calls := make([]InstallSnapshotCall, len(t.installSnapshotCalls))
	copy(calls, t.installSnapshotCalls)
	return calls
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

// TestLogger provides a simple logger that outputs to the test logger
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

// SafeTestLogger provides a logger that checks if the test has completed
type SafeTestLogger struct {
	t    *testing.T
	done atomic.Bool
}

// NewSafeTestLogger creates a new safe test logger
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

// MockPersistence is a mock implementation of Persistence for testing
type MockPersistence struct {
	mu             sync.Mutex
	state          *PersistentState
	snapshot       *Snapshot
	saveCount      int
	loadCount      int
	saveStateCount int
	loadStateCount int
	saveDuration   time.Duration
	loadDuration   time.Duration
	failNextSave   bool
	failNextLoad   bool
}

func NewMockPersistence() *MockPersistence {
	return &MockPersistence{}
}

func (m *MockPersistence) SaveState(state *PersistentState) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.failNextSave {
		m.failNextSave = false
		return fmt.Errorf("mock save error")
	}

	m.saveCount++
	m.saveStateCount++

	// Simulate some work
	if m.saveDuration > 0 {
		time.Sleep(m.saveDuration)
	}

	// Deep copy the state
	m.state = &PersistentState{
		CurrentTerm: state.CurrentTerm,
		VotedFor:    state.VotedFor,
		Log:         make([]LogEntry, len(state.Log)),
		CommitIndex: state.CommitIndex,
	}
	copy(m.state.Log, state.Log)

	return nil
}

func (m *MockPersistence) LoadState() (*PersistentState, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.failNextLoad {
		m.failNextLoad = false
		return nil, fmt.Errorf("mock load error")
	}

	m.loadCount++
	m.loadStateCount++

	// Simulate some work
	if m.loadDuration > 0 {
		time.Sleep(m.loadDuration)
	}

	if m.state == nil {
		return nil, nil
	}

	// Deep copy the state
	state := &PersistentState{
		CurrentTerm: m.state.CurrentTerm,
		VotedFor:    m.state.VotedFor,
		Log:         make([]LogEntry, len(m.state.Log)),
		CommitIndex: m.state.CommitIndex,
	}
	copy(state.Log, m.state.Log)

	return state, nil
}

func (m *MockPersistence) SaveSnapshot(snapshot *Snapshot) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.failNextSave {
		m.failNextSave = false
		return fmt.Errorf("mock save snapshot error")
	}

	m.saveCount++

	// Deep copy the snapshot
	m.snapshot = &Snapshot{
		LastIncludedIndex: snapshot.LastIncludedIndex,
		LastIncludedTerm:  snapshot.LastIncludedTerm,
		Data:              make([]byte, len(snapshot.Data)),
	}
	copy(m.snapshot.Data, snapshot.Data)

	return nil
}

func (m *MockPersistence) LoadSnapshot() (*Snapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.failNextLoad {
		m.failNextLoad = false
		return nil, fmt.Errorf("mock load snapshot error")
	}

	m.loadCount++

	if m.snapshot == nil {
		return nil, nil
	}

	// Deep copy the snapshot
	snapshot := &Snapshot{
		LastIncludedIndex: m.snapshot.LastIncludedIndex,
		LastIncludedTerm:  m.snapshot.LastIncludedTerm,
		Data:              make([]byte, len(m.snapshot.Data)),
	}
	copy(snapshot.Data, m.snapshot.Data)

	return snapshot, nil
}

// HasSnapshot returns true if a snapshot exists
func (m *MockPersistence) HasSnapshot() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.snapshot != nil
}

// GetSaveCount returns the number of times Save was called
func (m *MockPersistence) GetSaveCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.saveCount
}

func (m *MockPersistence) GetLoadCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.loadCount
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

// MockStateMachine is a mock implementation of StateMachine for testing
type MockStateMachine struct {
	mu                 sync.RWMutex
	data               map[string]interface{}
	appliedCommands    []interface{}
	applyCount         int
	applyDelay         time.Duration
	failNextApply      bool
	snapshotFailOnNext bool
	restoreFailOnNext  bool
}

func NewMockStateMachine() *MockStateMachine {
	return &MockStateMachine{
		data:            make(map[string]interface{}),
		appliedCommands: make([]interface{}, 0),
	}
}

func (m *MockStateMachine) Apply(entry LogEntry) interface{} {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.failNextApply {
		m.failNextApply = false
		// Don't panic, just return nil
		return nil
	}

	// Simulate work
	if m.applyDelay > 0 {
		time.Sleep(m.applyDelay)
	}

	m.applyCount++
	m.appliedCommands = append(m.appliedCommands, entry.Command)

	// Handle different command types
	switch cmd := entry.Command.(type) {
	case string:
		parts := strings.Split(cmd, "=")
		if len(parts) == 2 && parts[0] == "SET" {
			key := strings.TrimSpace(parts[1])
			m.data[key] = entry.Index
			return fmt.Sprintf("OK:%d", entry.Index)
		}
		// Store command as-is
		m.data[fmt.Sprintf("cmd-%d", entry.Index)] = cmd
		return cmd
	case map[string]interface{}:
		// Handle map commands
		if key, ok := cmd["key"].(string); ok {
			if value, ok := cmd["value"]; ok {
				m.data[key] = value
				return fmt.Sprintf("SET %s=%v", key, value)
			}
		}
		return nil
	default:
		// Store any other command type
		m.data[fmt.Sprintf("cmd-%d", entry.Index)] = cmd
		return cmd
	}
}

func (m *MockStateMachine) Snapshot() ([]byte, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.snapshotFailOnNext {
		m.snapshotFailOnNext = false
		return nil, fmt.Errorf("mock snapshot error")
	}

	// Create a simple snapshot
	return []byte(fmt.Sprintf("snapshot:entries=%d", len(m.data))), nil
}

func (m *MockStateMachine) Restore(data []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.restoreFailOnNext {
		m.restoreFailOnNext = false
		return fmt.Errorf("mock restore error")
	}

	// Clear existing data
	m.data = make(map[string]interface{})
	m.appliedCommands = make([]interface{}, 0)
	// In a real implementation, we'd parse the snapshot data
	return nil
}

// GetData returns a copy of the state machine data
func (m *MockStateMachine) GetData() map[string]interface{} {
	m.mu.Lock()
	defer m.mu.Unlock()

	data := make(map[string]interface{})
	for k, v := range m.data {
		data[k] = v
	}
	return data
}

// GetApplyCount returns the number of times Apply was called
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
	commands := make([]interface{}, len(m.appliedCommands))
	copy(commands, m.appliedCommands)
	return commands
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
