package helpers

import (
	"context"
	"testing"
	"time"

	"github.com/ueisele/raft"
	"github.com/ueisele/raft/integration/helpers/transporttest"
)

// TestNode wraps a single Raft node for testing with automatic cleanup
type TestNode struct {
	Node         raft.Node
	Transport    raft.Transport
	Persistence  raft.Persistence
	StateMachine raft.StateMachine
	Config       *raft.Config
	t            *testing.T
	ctx          context.Context
	cancel       context.CancelFunc
}

// TestNodeOption configures a test node
type TestNodeOption func(*testNodeConfig)

type testNodeConfig struct {
	config       *raft.Config
	transport    raft.Transport
	persistence  raft.Persistence
	stateMachine raft.StateMachine
	autoStart    bool
}

// WithNodeConfig sets the Raft configuration
func WithNodeConfig(config *raft.Config) TestNodeOption {
	return func(c *testNodeConfig) {
		c.config = config
	}
}

// WithNodeTransport sets a custom transport
func WithNodeTransport(transport raft.Transport) TestNodeOption {
	return func(c *testNodeConfig) {
		c.transport = transport
	}
}

// WithNodePersistence sets persistence
func WithNodePersistence(persistence raft.Persistence) TestNodeOption {
	return func(c *testNodeConfig) {
		c.persistence = persistence
	}
}

// WithNodeStateMachine sets the state machine
func WithNodeStateMachine(stateMachine raft.StateMachine) TestNodeOption {
	return func(c *testNodeConfig) {
		c.stateMachine = stateMachine
	}
}

// WithAutoStart automatically starts the node after creation
func WithAutoStart() TestNodeOption {
	return func(c *testNodeConfig) {
		c.autoStart = true
	}
}

// NewTestNode creates a single test node with automatic cleanup
func NewTestNode(t *testing.T, nodeID int, peers []int, opts ...TestNodeOption) *TestNode {
	// Apply options
	cfg := &testNodeConfig{
		config: &raft.Config{
			ID:                 nodeID,
			Peers:              peers,
			ElectionTimeoutMin: 150 * time.Millisecond,
			ElectionTimeoutMax: 300 * time.Millisecond,
			HeartbeatInterval:  50 * time.Millisecond,
			Logger:             raft.NewTestLogger(t),
		},
	}

	for _, opt := range opts {
		opt(cfg)
	}

	// Create default components if not provided
	if cfg.transport == nil {
		cfg.transport = raft.NewMockTransport(nodeID)
	}
	if cfg.stateMachine == nil {
		cfg.stateMachine = raft.NewMockStateMachine()
	}

	// Create context
	ctx, cancel := context.WithCancel(context.Background())

	// Create node
	node, err := raft.NewNode(cfg.config, cfg.transport, cfg.persistence, cfg.stateMachine)
	if err != nil {
		t.Fatalf("Failed to create test node %d: %v", nodeID, err)
	}

	testNode := &TestNode{
		Node:         node,
		Transport:    cfg.transport,
		Persistence:  cfg.persistence,
		StateMachine: cfg.stateMachine,
		Config:       cfg.config,
		t:            t,
		ctx:          ctx,
		cancel:       cancel,
	}

	// Register cleanup - this happens automatically when test ends
	t.Cleanup(func() {
		testNode.Stop()
	})

	// Auto-start if requested
	if cfg.autoStart {
		if err := testNode.Start(); err != nil {
			t.Fatalf("Failed to auto-start test node %d: %v", nodeID, err)
		}
	}

	return testNode
}

// Start starts the test node
func (n *TestNode) Start() error {
	return n.Node.Start(n.ctx)
}

// Stop stops the test node
func (n *TestNode) Stop() {
	n.cancel()

	// Use a timeout context for stopping
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := n.Node.Stop(ctx); err != nil {
		// Log error but don't fail the test during cleanup
		n.t.Logf("Warning: failed to stop test node %d: %v", n.Config.ID, err)
	}
}

// WaitForLeader waits for this node to become leader
func (n *TestNode) WaitForLeader(timeout time.Duration) {
	n.t.Helper()
	WaitForLeader(n.t, []raft.Node{n.Node}, timeout)
}

// WaitForTerm waits for the node to reach a specific term
func (n *TestNode) WaitForTerm(targetTerm int, timeout time.Duration) {
	n.t.Helper()
	WaitForTerm(n.t, []raft.Node{n.Node}, targetTerm, timeout)
}

// WaitForFollower waits for the node to become a follower (not leader)
func (n *TestNode) WaitForFollower(timeout time.Duration) {
	n.t.Helper()
	WaitForFollower(n.t, []raft.Node{n.Node}, timeout)
}

// WaitForCommitIndex waits for the node to reach at least the specified commit index
func (n *TestNode) WaitForCommitIndex(index int, timeout time.Duration) {
	n.t.Helper()
	WaitForCommitIndex(n.t, []raft.Node{n.Node}, index, timeout)
}

// Submit submits a command to the node
func (n *TestNode) Submit(command interface{}) (int, int, error) {
	index, term, isLeader := n.Node.Submit(command)
	if !isLeader {
		return 0, 0, context.Canceled
	}
	return index, term, nil
}

// CreateTestNodeSet creates multiple test nodes that can communicate
// This is useful for tests that need nodes but not a full cluster
func CreateTestNodeSet(t *testing.T, count int, opts ...TestNodeOption) []*TestNode {
	// Create a registry for nodes to find each other
	registry := transporttest.NewNodeRegistry()

	// Create peer list
	peers := make([]int, count)
	for i := 0; i < count; i++ {
		peers[i] = i
	}

	// Create nodes
	nodes := make([]*TestNode, count)
	for i := 0; i < count; i++ {
		// Create transport that uses the registry
		transport := transporttest.NewMultiNodeTransport(i, registry)

		// Create node with transport
		nodeOpts := append([]TestNodeOption{
			WithNodeTransport(transport),
		}, opts...)

		nodes[i] = NewTestNode(t, i, peers, nodeOpts...)

		// Register with registry
		registry.Register(i, nodes[i].Node.(raft.RPCHandler))
	}

	return nodes
}

// CreateStandaloneTestNode creates a single node cluster for testing
func CreateStandaloneTestNode(t *testing.T, opts ...TestNodeOption) *TestNode {
	return NewTestNode(t, 0, []int{0}, opts...)
}
