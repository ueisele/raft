package raft

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// MockTransport is a general-purpose transport mock for testing
type MockTransport struct {
	mu                       sync.Mutex
	serverID                 int
	requestVoteHandler       func(serverID int, args *RequestVoteArgs) (*RequestVoteReply, error)
	appendEntriesHandler     func(serverID int, args *AppendEntriesArgs) (*AppendEntriesReply, error)
	installSnapshotHandler   func(serverID int, args *InstallSnapshotArgs) (*InstallSnapshotReply, error)
	rpcHandler               RPCHandler
	started                  bool
	
	// Track calls
	requestVoteCalls       []RequestVoteCall
	appendEntriesCalls     []AppendEntriesCall
	installSnapshotCalls   []InstallSnapshotCall
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

func (m *MockTransport) SendRequestVote(serverID int, args *RequestVoteArgs) (*RequestVoteReply, error) {
	m.mu.Lock()
	m.requestVoteCalls = append(m.requestVoteCalls, RequestVoteCall{ServerID: serverID, Args: args})
	handler := m.requestVoteHandler
	m.mu.Unlock()

	if handler != nil {
		return handler(serverID, args)
	}

	// Default response - vote not granted
	return &RequestVoteReply{Term: args.Term, VoteGranted: false}, nil
}

func (m *MockTransport) SendAppendEntries(serverID int, args *AppendEntriesArgs) (*AppendEntriesReply, error) {
	m.mu.Lock()
	m.appendEntriesCalls = append(m.appendEntriesCalls, AppendEntriesCall{ServerID: serverID, Args: args})
	handler := m.appendEntriesHandler
	m.mu.Unlock()

	if handler != nil {
		return handler(serverID, args)
	}

	// Default response - success
	return &AppendEntriesReply{Term: args.Term, Success: true}, nil
}

func (m *MockTransport) SendInstallSnapshot(serverID int, args *InstallSnapshotArgs) (*InstallSnapshotReply, error) {
	m.mu.Lock()
	m.installSnapshotCalls = append(m.installSnapshotCalls, InstallSnapshotCall{ServerID: serverID, Args: args})
	handler := m.installSnapshotHandler
	m.mu.Unlock()

	if handler != nil {
		return handler(serverID, args)
	}

	// Default response - success
	return &InstallSnapshotReply{Term: args.Term}, nil
}

func (m *MockTransport) SetRPCHandler(handler RPCHandler) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rpcHandler = handler
}

func (m *MockTransport) Start() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.started = true
	return nil
}

func (m *MockTransport) Stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.started = false
	return nil
}

func (m *MockTransport) GetAddress() string {
	return fmt.Sprintf("mock://server%d", m.serverID)
}

// Set custom handlers
func (m *MockTransport) SetRequestVoteHandler(handler func(serverID int, args *RequestVoteArgs) (*RequestVoteReply, error)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.requestVoteHandler = handler
}

func (m *MockTransport) SetAppendEntriesHandler(handler func(serverID int, args *AppendEntriesArgs) (*AppendEntriesReply, error)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.appendEntriesHandler = handler
}

func (m *MockTransport) SetInstallSnapshotHandler(handler func(serverID int, args *InstallSnapshotArgs) (*InstallSnapshotReply, error)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.installSnapshotHandler = handler
}

// Get call history
func (m *MockTransport) GetRequestVoteCalls() []RequestVoteCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]RequestVoteCall{}, m.requestVoteCalls...)
}

func (m *MockTransport) GetAppendEntriesCalls() []AppendEntriesCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]AppendEntriesCall{}, m.appendEntriesCalls...)
}

func (m *MockTransport) GetInstallSnapshotCalls() []InstallSnapshotCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]InstallSnapshotCall{}, m.installSnapshotCalls...)
}

// ClearCalls clears all recorded calls
func (m *MockTransport) ClearCalls() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.requestVoteCalls = m.requestVoteCalls[:0]
	m.appendEntriesCalls = m.appendEntriesCalls[:0]
	m.installSnapshotCalls = m.installSnapshotCalls[:0]
}

// MockStateMachine for testing
type MockStateMachine struct {
	mu          sync.Mutex
	appliedLogs []LogEntry
	state       map[string]interface{}
}

func NewMockStateMachine() *MockStateMachine {
	return &MockStateMachine{
		appliedLogs: make([]LogEntry, 0),
		state:       make(map[string]interface{}),
	}
}

func (m *MockStateMachine) Apply(entry LogEntry) interface{} {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.appliedLogs = append(m.appliedLogs, entry)
	return nil
}

func (m *MockStateMachine) Snapshot() ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	// Simple snapshot - just return the number of applied logs
	return []byte(fmt.Sprintf("snapshot-%d", len(m.appliedLogs))), nil
}

func (m *MockStateMachine) Restore(snapshot []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	// Reset state
	m.appliedLogs = make([]LogEntry, 0)
	m.state = make(map[string]interface{})
	return nil
}

func (m *MockStateMachine) GetAppliedLogs() []LogEntry {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]LogEntry{}, m.appliedLogs...)
}

// TestLogger for testing
type TestLogger struct {
	t *testing.T
}

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
	l.t.Logf("[ERROR] "+format, args...)
}

// SafeTestLogger wraps testing.T and handles logging after test completion
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
	mu              sync.Mutex
	state           *PersistentState
	snapshot        *Snapshot
	saveStateCount  int
	loadStateCount  int
	saveSnapCount   int
	loadSnapCount   int
	failNextSave    bool
	failNextLoad    bool
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
		return fmt.Errorf("mock save state error")
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
	
	m.loadStateCount++
	
	if m.failNextLoad {
		m.failNextLoad = false
		return nil, fmt.Errorf("mock load state error")
	}
	
	return m.state, nil
}

func (m *MockPersistence) SaveSnapshot(snapshot *Snapshot) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	
	m.saveSnapCount++
	
	if m.failNextSave {
		m.failNextSave = false
		return fmt.Errorf("mock save snapshot error")
	}
	
	// Deep copy the snapshot
	m.snapshot = &Snapshot{
		Data:              make([]byte, len(snapshot.Data)),
		LastIncludedIndex: snapshot.LastIncludedIndex,
		LastIncludedTerm:  snapshot.LastIncludedTerm,
	}
	copy(m.snapshot.Data, snapshot.Data)
	
	return nil
}

func (m *MockPersistence) LoadSnapshot() (*Snapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	
	m.loadSnapCount++
	
	if m.failNextLoad {
		m.failNextLoad = false
		return nil, fmt.Errorf("mock load snapshot error")
	}
	
	return m.snapshot, nil
}

func (m *MockPersistence) HasSnapshot() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.snapshot != nil
}

// MockSnapshotProvider is a general-purpose snapshot provider mock for testing
type MockSnapshotProvider struct {
	mu       sync.Mutex
	snapshot *Snapshot
	err      error
	calls    int
}

func NewMockSnapshotProvider() *MockSnapshotProvider {
	return &MockSnapshotProvider{}
}

func (m *MockSnapshotProvider) GetLatestSnapshot() (*Snapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls++
	return m.snapshot, m.err
}

func (m *MockSnapshotProvider) SetSnapshot(snapshot *Snapshot) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.snapshot = snapshot
}

func (m *MockSnapshotProvider) SetError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.err = err
}

func (m *MockSnapshotProvider) GetCalls() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.calls
}

// MockMetrics is a general-purpose metrics mock for testing
type MockMetrics struct {
	mu             sync.Mutex
	elections      []electionRecord
	heartbeats     []heartbeatRecord
	appends        []int
	commits        []int
	snapshots      []snapshotRecord
}

type electionRecord struct {
	won      bool
	duration time.Duration
}

type heartbeatRecord struct {
	peer     int
	success  bool
	duration time.Duration
}

type snapshotRecord struct {
	size     int
	duration time.Duration
}

func NewMockMetrics() *MockMetrics {
	return &MockMetrics{
		elections:  make([]electionRecord, 0),
		heartbeats: make([]heartbeatRecord, 0),
		appends:    make([]int, 0),
		commits:    make([]int, 0),
		snapshots:  make([]snapshotRecord, 0),
	}
}

func (m *MockMetrics) RecordElection(won bool, duration time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.elections = append(m.elections, electionRecord{won: won, duration: duration})
}

func (m *MockMetrics) RecordHeartbeat(peer int, success bool, duration time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.heartbeats = append(m.heartbeats, heartbeatRecord{peer: peer, success: success, duration: duration})
}

func (m *MockMetrics) RecordLogAppend(entries int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.appends = append(m.appends, entries)
}

func (m *MockMetrics) RecordCommit(index int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.commits = append(m.commits, index)
}

func (m *MockMetrics) RecordSnapshot(size int, duration time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.snapshots = append(m.snapshots, snapshotRecord{size: size, duration: duration})
}