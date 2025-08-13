package helpers

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/ueisele/raft"
	"github.com/ueisele/raft/integration/helpers/transporttest"
	jsonpersistence "github.com/ueisele/raft/persistence"
	"github.com/ueisele/raft/persistence/json"
)

// TestCluster manages a cluster of Raft nodes for testing
type TestCluster struct {
	mu            sync.RWMutex
	nodes         map[int]raft.Node
	transports    map[int]raft.Transport
	persistences  map[int]raft.Persistence
	stateMachines map[int]raft.StateMachine
	Registry      *transporttest.NodeRegistry
	config        clusterConfig
	t             *testing.T
	ctx           context.Context
	cancel        context.CancelFunc

	// Compatibility fields for tests that still use array-style access
	// These are maintained in sync with the maps above
	Nodes      []raft.Node
	Transports []raft.Transport
}

// ClusterOption configures a test cluster
type ClusterOption func(*clusterConfig)

type clusterConfig struct {
	electionTimeoutMin time.Duration
	electionTimeoutMax time.Duration
	heartbeatInterval  time.Duration

	maxLogSize int

	logger raft.Logger

	// Factory functions for creating components per node
	transportFactory    func(nodeID int, registry *transporttest.NodeRegistry) (raft.Transport, error)
	persistenceFactory  func(nodeID int) (raft.Persistence, error)
	stateMachineFactory func(nodeID int) (raft.StateMachine, error)

	// Decorators for transport
	transportDecorators []func(nodeID int, wrapped raft.Transport) raft.Transport

	autoStart bool
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

// WithMaxLogSize sets the max log size before snapshot
func WithMaxLogSize(size int) ClusterOption {
	return func(c *clusterConfig) {
		c.maxLogSize = size
	}
}

// WithLogger sets the logger for nodes
func WithLogger(logger raft.Logger) ClusterOption {
	return func(c *clusterConfig) {
		c.logger = logger
	}
}

// WithTransportFactory sets a custom transport factory
func WithTransportFactory(factory func(nodeID int, registry *transporttest.NodeRegistry) (raft.Transport, error)) ClusterOption {
	return func(c *clusterConfig) {
		c.transportFactory = factory
	}
}

// WithTransportDecorators adds decorators to wrap the base transport
func WithTransportDecorators(decorators ...func(nodeID int, wrapped raft.Transport) raft.Transport) ClusterOption {
	return func(c *clusterConfig) {
		c.transportDecorators = append(c.transportDecorators, decorators...)
	}
}

// WithPersistenceFactory sets a factory for creating persistence per node
func WithPersistenceFactory(factory func(nodeID int) (raft.Persistence, error)) ClusterOption {
	return func(c *clusterConfig) {
		c.persistenceFactory = factory
	}
}

// WithMockPersistence uses mock persistence for all nodes
func WithMockPersistence() ClusterOption {
	return WithPersistenceFactory(func(nodeID int) (raft.Persistence, error) {
		return raft.NewMockPersistence(), nil
	})
}

// WithJSONPersistence uses JSON persistence with a base directory
func WithJSONPersistence(baseDir string) ClusterOption {
	return WithPersistenceFactory(func(nodeID int) (raft.Persistence, error) {
		nodeDir := fmt.Sprintf("%s/node-%d", baseDir, nodeID)
		return json.NewJSONPersistence(&jsonpersistence.Config{
			DataDir:  nodeDir,
			ServerID: nodeID,
		})
	})
}

// WithStateMachineFactory sets a factory for creating state machines per node
func WithStateMachineFactory(factory func(nodeID int) (raft.StateMachine, error)) ClusterOption {
	return func(c *clusterConfig) {
		c.stateMachineFactory = factory
	}
}

// WithMockStateMachine uses mock state machine for all nodes
func WithMockStateMachine() ClusterOption {
	return WithStateMachineFactory(func(nodeID int) (raft.StateMachine, error) {
		return raft.NewMockStateMachine(), nil
	})
}

// WithClusterAutoStart automatically starts all nodes after creation
func WithClusterAutoStart() ClusterOption {
	return func(c *clusterConfig) {
		c.autoStart = true
	}
}

// NewTestClusterOfSize creates a test cluster with sequential node IDs from 0 to size-1
// This is a convenience function for tests that don't need specific node IDs
func NewTestClusterOfSize(t *testing.T, size int, opts ...ClusterOption) *TestCluster {
	nodeIDs := make([]int, size)
	for i := 0; i < size; i++ {
		nodeIDs[i] = i
	}
	return NewTestCluster(t, nodeIDs, opts...)
}

// NewTestCluster creates a new test cluster
func NewTestCluster(t *testing.T, nodeIDs []int, opts ...ClusterOption) *TestCluster {
	// Apply options
	config := clusterConfig{
		electionTimeoutMin: 150 * time.Millisecond,
		electionTimeoutMax: 300 * time.Millisecond,
		heartbeatInterval:  50 * time.Millisecond,
	}
	for _, opt := range opts {
		opt(&config)
	}

	// Create context
	ctx, cancel := context.WithCancel(context.Background())

	// Create cluster with maps
	cluster := &TestCluster{
		nodes:         make(map[int]raft.Node),
		transports:    make(map[int]raft.Transport),
		persistences:  make(map[int]raft.Persistence),
		stateMachines: make(map[int]raft.StateMachine),
		Registry:      transporttest.NewNodeRegistryWithLogger(config.logger),
		config:        config,
		t:             t,
		ctx:           ctx,
		cancel:        cancel,
	}

	// Create nodes with specified IDs
	for _, nodeID := range nodeIDs {
		_, err := cluster.addNode(nodeID, nodeIDs, false)
		if err != nil {
			t.Fatalf("Failed to create node %d: %v", nodeID, err)
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

	// Sync Nodes array for compatibility
	cluster.syncNodesArray()

	return cluster
}

// syncNodesArray updates the Nodes and Transports arrays to match the maps
// Must be called with mu held
func (c *TestCluster) syncNodesArray() {
	// Find max node ID to determine array size
	maxID := -1
	for id := range c.nodes {
		if id > maxID {
			maxID = id
		}
	}

	// Create arrays with nil entries
	if maxID >= 0 {
		c.Nodes = make([]raft.Node, maxID+1)
		c.Transports = make([]raft.Transport, maxID+1)
		for id, node := range c.nodes {
			c.Nodes[id] = node
		}
		for id, transport := range c.transports {
			c.Transports[id] = transport
		}
	} else {
		c.Nodes = nil
		c.Transports = nil
	}
}

// NodeCount returns the number of nodes in the cluster
func (c *TestCluster) NodeCount() int {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return len(c.nodes)
}

// NodeIDs returns all node IDs in the cluster
func (c *TestCluster) NodeIDs() []int {
	c.mu.RLock()
	defer c.mu.RUnlock()

	ids := make([]int, 0, len(c.nodes))
	for id := range c.nodes {
		ids = append(ids, id)
	}
	return ids
}

// GetNodes returns a map of all nodes in the cluster
// This properly preserves the node ID to node mapping
func (c *TestCluster) GetNodes() map[int]raft.Node {
	c.mu.RLock()
	defer c.mu.RUnlock()

	// Return a copy to prevent external modifications
	result := make(map[int]raft.Node, len(c.nodes))
	for k, v := range c.nodes {
		result[k] = v
	}
	return result
}

// GetNodesSlice returns all nodes as a slice for use with assertion helpers
// The nodes are returned in an unspecified order
func (c *TestCluster) GetNodesSlice() []raft.Node {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return slices.Collect(maps.Values(c.nodes))
}

// GetNode returns a specific node by ID
func (c *TestCluster) GetNode(nodeID int) (raft.Node, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	node, ok := c.nodes[nodeID]
	return node, ok
}

// GetTransports returns a map of all transports in the cluster
// This properly preserves the node ID to transport mapping
func (c *TestCluster) GetTransports() map[int]raft.Transport {
	c.mu.RLock()
	defer c.mu.RUnlock()

	// Return a copy to prevent external modifications
	result := make(map[int]raft.Transport, len(c.transports))
	for k, v := range c.transports {
		result[k] = v
	}
	return result
}

// GetTransportsSlice returns all transports as a slice
// The transports are returned in an unspecified order
func (c *TestCluster) GetTransportsSlice() []raft.Transport {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return slices.Collect(maps.Values(c.transports))
}

// GetTransport returns the transport for a specific node
func (c *TestCluster) GetTransport(nodeID int) (raft.Transport, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	transport, ok := c.transports[nodeID]
	return transport, ok
}

// GetPersistences returns a map of all persistences in the cluster
// This properly preserves the node ID to persistences mapping
func (c *TestCluster) GetPersistences() map[int]raft.Persistence {
	c.mu.RLock()
	defer c.mu.RUnlock()

	// Return a copy to prevent external modifications
	result := make(map[int]raft.Persistence, len(c.persistences))
	for k, v := range c.persistences {
		result[k] = v
	}
	return result
}

// GetPersistence returns the persistence for a specific node
func (c *TestCluster) GetPersistence(nodeID int) (raft.Persistence, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	persistence, ok := c.persistences[nodeID]
	return persistence, ok
}

// GetStateMachines returns a map of all stateMachines in the cluster
// This properly preserves the node ID to stateMachines mapping
func (c *TestCluster) GetStateMachines() map[int]raft.StateMachine {
	c.mu.RLock()
	defer c.mu.RUnlock()

	// Return a copy to prevent external modifications
	result := make(map[int]raft.StateMachine, len(c.stateMachines))
	for k, v := range c.stateMachines {
		result[k] = v
	}
	return result
}

// GetStateMachine returns the state machine for a specific node
func (c *TestCluster) GetStateMachine(nodeID int) (raft.StateMachine, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	sm, ok := c.stateMachines[nodeID]
	return sm, ok
}

// Start starts all nodes in the cluster
func (c *TestCluster) Start() error {
	c.mu.RLock()
	defer c.mu.RUnlock()

	for nodeID, node := range c.nodes {
		if err := node.Start(c.ctx); err != nil {
			return fmt.Errorf("failed to start node %d: %w", nodeID, err)
		}
	}
	return nil
}

// Stop stops all nodes in the cluster
func (c *TestCluster) Stop() {
	c.mu.RLock()
	defer c.mu.RUnlock()

	c.cancel()
	// Use a timeout context for stopping nodes
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	for nodeID, node := range c.nodes {
		if err := node.Stop(ctx); err != nil {
			// Log error but continue stopping other nodes
			c.t.Logf("Warning: failed to stop node %d: %v", nodeID, err)
		}
	}
}

// GetLeader returns the current leader node and its ID
func (c *TestCluster) GetLeader() (raft.Node, int) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	for nodeID, node := range c.nodes {
		if node.IsLeader() {
			return node, nodeID
		}
	}
	return nil, -1
}

// AddNode dynamically adds a new node to the cluster
func (c *TestCluster) AddNode(nodeID int, peers []int) (raft.Node, error) {
	return c.addNode(nodeID, peers, c.config.autoStart)
}

// addNode is the internal implementation
func (c *TestCluster) addNode(nodeID int, peers []int, autoStart bool) (raft.Node, error) {
	// Check if node already exists
	c.mu.RLock()
	if _, exists := c.nodes[nodeID]; exists {
		c.mu.RUnlock()
		return nil, fmt.Errorf("node %d already exists", nodeID)
	}
	c.mu.RUnlock()

	// Create node config
	nodeConfig := &raft.Config{
		ID:                 nodeID,
		Peers:              peers,
		ElectionTimeoutMin: c.config.electionTimeoutMin,
		ElectionTimeoutMax: c.config.electionTimeoutMax,
		HeartbeatInterval:  c.config.heartbeatInterval,
	}

	if c.config.logger != nil {
		nodeConfig.Logger = c.config.logger
	}

	if c.config.maxLogSize > 0 {
		nodeConfig.MaxLogSize = c.config.maxLogSize
	}

	// Create transport
	var transport raft.Transport
	var err error
	if c.config.transportFactory != nil {
		transport, err = c.config.transportFactory(nodeID, c.Registry)
		if err != nil {
			return nil, fmt.Errorf("failed to create transport for node %d: %w", nodeID, err)
		}
	} else {
		transport = transporttest.NewMultiNodeTransport(nodeID, c.Registry)
	}

	// Apply decorators
	for _, decorator := range c.config.transportDecorators {
		transport = decorator(nodeID, transport)
	}

	// Create persistence
	var persistence raft.Persistence
	if c.config.persistenceFactory != nil {
		persistence, err = c.config.persistenceFactory(nodeID)
		if err != nil {
			return nil, fmt.Errorf("failed to create persistence for node %d: %w", nodeID, err)
		}
	} else {
		persistence = raft.NewMockPersistence()
	}

	// Create state machine
	var stateMachine raft.StateMachine
	if c.config.stateMachineFactory != nil {
		stateMachine, err = c.config.stateMachineFactory(nodeID)
		if err != nil {
			return nil, fmt.Errorf("failed to create state machine for node %d: %w", nodeID, err)
		}
	} else {
		stateMachine = raft.NewMockStateMachine()
	}

	// Create node
	node, err := raft.NewNode(nodeConfig, transport, persistence, stateMachine)
	if err != nil {
		return nil, fmt.Errorf("failed to create node %d: %w", nodeID, err)
	}

	// Add to cluster's maps
	c.mu.Lock()
	c.Registry.Register(nodeID, node.(raft.RPCHandler))
	c.nodes[nodeID] = node
	c.transports[nodeID] = transport
	c.persistences[nodeID] = persistence
	c.stateMachines[nodeID] = stateMachine
	c.syncNodesArray()
	c.mu.Unlock()

	// Start if requested
	if autoStart {
		if err := node.Start(c.ctx); err != nil {
			return nil, fmt.Errorf("failed to start node %d: %w", nodeID, err)
		}
	}

	return node, nil
}

// RemoveNode removes a node from the cluster
// This now works correctly regardless of which node is removed
func (c *TestCluster) RemoveNode(nodeID int) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Check if node exists
	if _, exists := c.nodes[nodeID]; !exists {
		return fmt.Errorf("node %d does not exist", nodeID)
	}

	// Unregister from registry
	c.Registry.Unregister(nodeID)

	// Remove from all maps
	delete(c.nodes, nodeID)
	delete(c.transports, nodeID)
	delete(c.persistences, nodeID)
	delete(c.stateMachines, nodeID)
	c.syncNodesArray()

	return nil
}

// StartNode starts a specific node
func (c *TestCluster) StartNode(nodeID int) error {
	c.mu.RLock()
	node, ok := c.nodes[nodeID]
	c.mu.RUnlock()

	if !ok {
		return fmt.Errorf("node %d not found", nodeID)
	}

	return node.Start(c.ctx)
}

// StopNode stops a specific node
func (c *TestCluster) StopNode(nodeID int) error {
	c.mu.RLock()
	node, ok := c.nodes[nodeID]
	c.mu.RUnlock()

	if !ok {
		return fmt.Errorf("node %d not found", nodeID)
	}

	// Use a timeout context for stopping
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	return node.Stop(ctx)
}

// RestartNode stops and restarts a specific node with the same configuration
func (c *TestCluster) RestartNode(nodeID int) error {
	// Stop the node first
	if err := c.StopNode(nodeID); err != nil {
		return fmt.Errorf("failed to stop node %d: %w", nodeID, err)
	}

	// Get current peers
	peers := c.NodeIDs()

	// Remove old node from maps
	c.mu.Lock()
	c.Registry.Unregister(nodeID)
	delete(c.nodes, nodeID)
	delete(c.transports, nodeID)
	delete(c.persistences, nodeID)
	delete(c.stateMachines, nodeID)
	c.syncNodesArray()
	c.mu.Unlock()

	// Recreate the node with same configuration
	_, err := c.addNode(nodeID, peers, true)
	if err != nil {
		return fmt.Errorf("failed to restart node %d: %w", nodeID, err)
	}

	return nil
}

// SubmitToNode submits a command to a specific node
func (c *TestCluster) SubmitToNode(command interface{}, nodeID int) (int, int, error) {
	node, ok := c.GetNode(nodeID)
	if !ok {
		return 0, 0, fmt.Errorf("node %d not found", nodeID)
	}

	index, term, isLeader := node.Submit(command)
	if !isLeader {
		return 0, 0, fmt.Errorf("node %d is not leader", nodeID)
	}

	return index, term, nil
}

// SubmitToLeader submits a command to the current leader (renamed from SubmitCommand)
func (c *TestCluster) SubmitToLeader(command interface{}) (int, int, error) {
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

// SubmitCommand is deprecated, use SubmitToLeader instead
// Deprecated: Use SubmitToLeader
func (c *TestCluster) SubmitCommand(command interface{}) (int, int, error) {
	return c.SubmitToLeader(command)
}

// WaitForLeader waits for a leader to be elected
func (c *TestCluster) WaitForLeader(timeout time.Duration) (int, error) {
	c.t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if leader, id := c.GetLeader(); leader != nil {
			return id, nil
		}
		time.Sleep(10 * time.Millisecond)
	}

	return -1, fmt.Errorf("timeout waiting for leader")
}

// WaitForCommitIndex waits for all nodes to reach at least the specified commit index
func (c *TestCluster) WaitForCommitIndex(index int, timeout time.Duration) error {
	c.t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		c.mu.RLock()
		allReached := true
		for nodeID, node := range c.nodes {
			commitIndex := node.GetCommitIndex()
			if commitIndex < index {
				c.t.Logf("Node %d commit index %d < %d", nodeID, commitIndex, index)
				allReached = false
				break
			}
		}
		c.mu.RUnlock()

		if allReached {
			return nil
		}
		time.Sleep(10 * time.Millisecond)
	}

	return fmt.Errorf("timeout waiting for commit index %d", index)
}
