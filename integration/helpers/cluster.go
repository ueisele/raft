package helpers

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/ueisele/raft"
)

// TestCluster manages a cluster of Raft nodes for testing
type TestCluster struct {
	Nodes         []raft.Node
	Transports    []raft.Transport
	Persistences  []raft.Persistence
	StateMachines []raft.StateMachine
	Registry      interface{} // Can be NodeRegistry, DebugNodeRegistry, or PartitionRegistry
	t             *testing.T
	ctx           context.Context
	cancel        context.CancelFunc
}

// ClusterOption configures a test cluster
type ClusterOption func(*clusterConfig)

type clusterConfig struct {
	electionTimeoutMin time.Duration
	electionTimeoutMax time.Duration
	heartbeatInterval  time.Duration
	useDebugTransport  bool
	usePartitionable   bool
	logger             raft.Logger
	persistence        []raft.Persistence
	stateMachines      []raft.StateMachine
	maxLogSize         int
	autoStart          bool
}

// WithElectionTimeout sets the election timeout range
func WithElectionTimeout(min, max time.Duration) ClusterOption {
	return func(c *clusterConfig) {
		c.electionTimeoutMin = min
		c.electionTimeoutMax = max
	}
}

// WithHeartbeatInterval sets the heartbeat interval
func WithHeartbeatInterval(interval time.Duration) ClusterOption {
	return func(c *clusterConfig) {
		c.heartbeatInterval = interval
	}
}

// WithDebugTransport enables debug transport with logging
func WithDebugTransport(logger raft.Logger) ClusterOption {
	return func(c *clusterConfig) {
		c.useDebugTransport = true
		c.logger = logger
	}
}

// WithPartitionableTransport enables partitionable transport
func WithPartitionableTransport() ClusterOption {
	return func(c *clusterConfig) {
		c.usePartitionable = true
	}
}

// WithPersistence sets persistence for nodes
func WithPersistence(persistence []raft.Persistence) ClusterOption {
	return func(c *clusterConfig) {
		c.persistence = persistence
	}
}

// WithStateMachines sets state machines for nodes
func WithStateMachines(stateMachines []raft.StateMachine) ClusterOption {
	return func(c *clusterConfig) {
		c.stateMachines = stateMachines
	}
}

// WithMaxLogSize sets the max log size before snapshot
func WithMaxLogSize(size int) ClusterOption {
	return func(c *clusterConfig) {
		c.maxLogSize = size
	}
}

// WithClusterAutoStart automatically starts all nodes after creation
func WithClusterAutoStart() ClusterOption {
	return func(c *clusterConfig) {
		c.autoStart = true
	}
}

// NewTestCluster creates a new test cluster
func NewTestCluster(t *testing.T, size int, opts ...ClusterOption) *TestCluster {
	// Apply options
	config := &clusterConfig{
		electionTimeoutMin: 150 * time.Millisecond,
		electionTimeoutMax: 300 * time.Millisecond,
		heartbeatInterval:  50 * time.Millisecond,
	}
	for _, opt := range opts {
		opt(config)
	}

	// Create context
	ctx, cancel := context.WithCancel(context.Background())

	// Create cluster
	cluster := &TestCluster{
		Nodes:         make([]raft.Node, size),
		Transports:    make([]raft.Transport, size),
		Persistences:  make([]raft.Persistence, size),
		StateMachines: make([]raft.StateMachine, size),
		t:             t,
		ctx:           ctx,
		cancel:        cancel,
	}

	// Create registry based on transport type
	var registry interface{}
	if config.usePartitionable {
		registry = NewPartitionRegistry()
		cluster.Registry = registry
	} else if config.useDebugTransport {
		registry = NewDebugNodeRegistry(config.logger)
		cluster.Registry = registry
	} else {
		registry = NewNodeRegistry()
		cluster.Registry = registry
	}

	// Create nodes
	peers := make([]int, size)
	for i := 0; i < size; i++ {
		peers[i] = i
	}

	for i := 0; i < size; i++ {
		// Create config
		nodeConfig := &raft.Config{
			ID:                 i,
			Peers:              peers,
			ElectionTimeoutMin: config.electionTimeoutMin,
			ElectionTimeoutMax: config.electionTimeoutMax,
			HeartbeatInterval:  config.heartbeatInterval,
		}

		if config.logger != nil {
			nodeConfig.Logger = config.logger
		}

		if config.maxLogSize > 0 {
			nodeConfig.MaxLogSize = config.maxLogSize
		}

		// Create transport
		var transport raft.Transport
		if config.usePartitionable {
			transport = NewPartitionableTransport(i, registry.(*PartitionRegistry))
		} else if config.useDebugTransport {
			transport = NewDebugTransport(i, registry.(*DebugNodeRegistry), config.logger)
		} else {
			transport = NewMultiNodeTransport(i, registry.(*NodeRegistry))
		}
		cluster.Transports[i] = transport

		// Get persistence and state machine
		var persistence raft.Persistence
		if config.persistence != nil && i < len(config.persistence) {
			persistence = config.persistence[i]
		} else {
			persistence = raft.NewMockPersistence()
		}
		cluster.Persistences[i] = persistence

		var stateMachine raft.StateMachine
		if config.stateMachines != nil && i < len(config.stateMachines) {
			stateMachine = config.stateMachines[i]
		} else {
			stateMachine = raft.NewMockStateMachine()
		}
		cluster.StateMachines[i] = stateMachine

		// Create node
		node, err := raft.NewNode(nodeConfig, transport, persistence, stateMachine)
		if err != nil {
			t.Fatalf("Failed to create node %d: %v", i, err)
		}
		cluster.Nodes[i] = node

		// Register with registry
		switch r := registry.(type) {
		case *NodeRegistry:
			r.Register(i, node.(raft.RPCHandler))
		case *DebugNodeRegistry:
			r.Register(i, node.(raft.RPCHandler))
		case *PartitionRegistry:
			r.Register(i, node.(raft.RPCHandler))
		}
	}

	// Register cleanup
	t.Cleanup(func() {
		cluster.Stop()
	})

	// Auto-start if requested
	if config.autoStart {
		if err := cluster.Start(); err != nil {
			t.Fatalf("Failed to auto-start cluster: %v", err)
		}
	}

	return cluster
}

// Start starts all nodes in the cluster
func (c *TestCluster) Start() error {
	for i, node := range c.Nodes {
		if err := node.Start(c.ctx); err != nil {
			return fmt.Errorf("failed to start node %d: %w", i, err)
		}
	}
	return nil
}

// Stop stops all nodes in the cluster
func (c *TestCluster) Stop() {
	c.cancel()
	// Use a timeout context for stopping nodes
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	for i, node := range c.Nodes {
		if err := node.Stop(ctx); err != nil {
			// Log error but continue stopping other nodes
			c.t.Logf("Warning: failed to stop node %d: %v", i, err)
		}
	}
}

// WaitForLeader waits for a leader to be elected and returns its ID
func (c *TestCluster) WaitForLeader(timeout time.Duration) (int, error) {
	c.t.Helper()
	leaderID := WaitForLeader(c.t, c.Nodes, timeout)
	return leaderID, nil
}

// GetLeader returns the current leader node and its ID
func (c *TestCluster) GetLeader() (raft.Node, int) {
	for i, node := range c.Nodes {
		if node.IsLeader() {
			return node, i
		}
	}
	return nil, -1
}

// PartitionNode isolates a node from the cluster (requires partitionable transport)
func (c *TestCluster) PartitionNode(nodeID int) error {
	transport, ok := c.Transports[nodeID].(*PartitionableTransport)
	if !ok {
		return fmt.Errorf("node %d does not have partitionable transport", nodeID)
	}
	transport.BlockAll()

	// Also block this node from all other nodes
	for i, t := range c.Transports {
		if i != nodeID {
			if pt, ok := t.(*PartitionableTransport); ok {
				pt.Block(nodeID)
			}
		}
	}
	return nil
}

// HealPartition removes all network partitions
func (c *TestCluster) HealPartition() {
	for _, t := range c.Transports {
		if pt, ok := t.(*PartitionableTransport); ok {
			pt.UnblockAll()
		}
	}
}

// CreatePartition creates a network partition between two groups
func (c *TestCluster) CreatePartition(group1, group2 []int) {
	// Block communication from group1 to group2
	for _, id1 := range group1 {
		if pt, ok := c.Transports[id1].(*PartitionableTransport); ok {
			for _, id2 := range group2 {
				pt.Block(id2)
			}
		}
	}

	// Block communication from group2 to group1
	for _, id2 := range group2 {
		if pt, ok := c.Transports[id2].(*PartitionableTransport); ok {
			for _, id1 := range group1 {
				pt.Block(id1)
			}
		}
	}
}

// WaitForCommitIndex waits for all nodes to reach at least the specified commit index
func (c *TestCluster) WaitForCommitIndex(index int, timeout time.Duration) error {
	c.t.Helper()
	WaitForCommitIndex(c.t, c.Nodes, index, timeout)
	return nil
}

// SubmitCommand submits a command to the leader
func (c *TestCluster) SubmitCommand(command interface{}) (int, int, error) {
	leader, leaderID := c.GetLeader()
	if leader == nil {
		return 0, -1, fmt.Errorf("no leader available")
	}

	index, term, isLeader := leader.Submit(command)
	if !isLeader {
		return 0, -1, fmt.Errorf("node %d is no longer leader", leaderID)
	}

	return index, term, nil
}

// GetLeaderNode returns the current leader node (nil if no leader)
func (c *TestCluster) GetLeaderNode() raft.Node {
	leader, _ := c.GetLeader()
	return leader
}

// WaitForStableCluster waits for the cluster to stabilize with a leader
func (c *TestCluster) WaitForStableCluster(timeout time.Duration) {
	c.t.Helper()
	// Wait for a leader to be elected
	WaitForLeader(c.t, c.Nodes, timeout)
}

// GetPersistence returns the persistence for a specific node
func (c *TestCluster) GetPersistence(nodeID int) raft.Persistence {
	if nodeID >= 0 && nodeID < len(c.Persistences) {
		return c.Persistences[nodeID]
	}
	return nil
}

// GetStateMachine returns the state machine for a specific node
func (c *TestCluster) GetStateMachine(nodeID int) *raft.MockStateMachine {
	if nodeID >= 0 && nodeID < len(c.StateMachines) {
		if sm, ok := c.StateMachines[nodeID].(*raft.MockStateMachine); ok {
			return sm
		}
	}
	return nil
}
