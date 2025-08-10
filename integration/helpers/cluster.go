package helpers

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/ueisele/raft"
	jsonpersistence "github.com/ueisele/raft/persistence"
	"github.com/ueisele/raft/persistence/json"
)

// TestCluster manages a cluster of Raft nodes for testing
type TestCluster struct {
	mu            sync.RWMutex // Protects Nodes, Transports, Persistences, StateMachines slices
	Nodes         []raft.Node
	Transports    []raft.Transport
	Persistences  []raft.Persistence
	StateMachines []raft.StateMachine
	Registry      *NodeRegistry // Single registry type for all transports
	config        clusterConfig
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

	maxLogSize int

	logger raft.Logger

	// Factory functions for creating components per node
	transportFactory    func(nodeID int, registry *NodeRegistry) (raft.Transport, error)
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

// WithLogger sets the logger for nodes
func WithLogger(logger raft.Logger) ClusterOption {
	return func(c *clusterConfig) {
		c.logger = logger
	}
}

// WithPersistenceFactory sets a factory for creating persistence per node
func WithPersistenceFactory(factory func(nodeID int) (raft.Persistence, error)) ClusterOption {
	return func(c *clusterConfig) {
		c.persistenceFactory = factory
	}
}

// WithStateMachineFactory sets a factory for creating state machines per node
func WithStateMachineFactory(factory func(nodeID int) (raft.StateMachine, error)) ClusterOption {
	return func(c *clusterConfig) {
		c.stateMachineFactory = factory
	}
}

// WithMockPersistence uses mock persistence for all nodes
func WithMockPersistence() ClusterOption {
	return WithPersistenceFactory(func(nodeID int) (raft.Persistence, error) {
		return raft.NewMockPersistence(), nil
	})
}

// WithMockStateMachine uses mock state machine for all nodes
func WithMockStateMachine() ClusterOption {
	return WithStateMachineFactory(func(nodeID int) (raft.StateMachine, error) {
		return raft.NewMockStateMachine(), nil
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

// Backward compatibility: convert old-style persistence array to factory
func WithPersistence(persistence []raft.Persistence) ClusterOption {
	return WithPersistenceFactory(func(nodeID int) (raft.Persistence, error) {
		if nodeID < len(persistence) {
			return persistence[nodeID], nil
		}
		return raft.NewMockPersistence(), nil
	})
}

// Backward compatibility: convert old-style state machine array to factory
func WithStateMachines(stateMachines []raft.StateMachine) ClusterOption {
	return WithStateMachineFactory(func(nodeID int) (raft.StateMachine, error) {
		if nodeID < len(stateMachines) {
			return stateMachines[nodeID], nil
		}
		return raft.NewMockStateMachine(), nil
	})
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

// WithTransportFactory sets a custom transport factory
func WithTransportFactory(factory func(nodeID int, registry *NodeRegistry) (raft.Transport, error)) ClusterOption {
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

// NewTestCluster creates a new test cluster
func NewTestCluster(t *testing.T, size int, opts ...ClusterOption) *TestCluster {
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

	// Create cluster
	cluster := &TestCluster{
		Nodes:         make([]raft.Node, 0, size),
		Transports:    make([]raft.Transport, 0, size),
		Persistences:  make([]raft.Persistence, 0, size),
		StateMachines: make([]raft.StateMachine, 0, size),
		config:        config,
		t:             t,
		ctx:           ctx,
		cancel:        cancel,
	}

	// Create registry
	cluster.Registry = NewNodeRegistryWithLogger(config.logger)

	// Create nodes
	peers := make([]int, size)
	for i := 0; i < size; i++ {
		peers[i] = i
	}

	for i := 0; i < size; i++ {
		_, err := cluster.addNode(i, peers, false)
		if err != nil {
			t.Fatalf("Failed to create node %d: %v", i, err)
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
	c.mu.RLock()
	defer c.mu.RUnlock()

	for i, node := range c.Nodes {
		if err := node.Start(c.ctx); err != nil {
			return fmt.Errorf("failed to start node %d: %w", i, err)
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

	for i, node := range c.Nodes {
		if err := node.Stop(ctx); err != nil {
			// Log error but continue stopping other nodes
			c.t.Logf("Warning: failed to stop node %d: %v", i, err)
		}
	}
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

// GetLeader returns the current leader node and its ID
func (c *TestCluster) GetLeader() (raft.Node, int) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	for i, node := range c.Nodes {
		if node.IsLeader() {
			return node, i
		}
	}
	return nil, -1
}

// GetLeaderNode returns the current leader node (nil if no leader)
func (c *TestCluster) GetLeaderNode() raft.Node {
	leader, _ := c.GetLeader()
	return leader
}

// GetPersistence returns the persistence for a specific node
func (c *TestCluster) GetPersistence(nodeID int) raft.Persistence {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if nodeID >= 0 && nodeID < len(c.Persistences) {
		return c.Persistences[nodeID]
	}
	return nil
}

// GetStateMachine returns the state machine for a specific node
func (c *TestCluster) GetStateMachine(nodeID int) *raft.MockStateMachine {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if nodeID >= 0 && nodeID < len(c.StateMachines) {
		if sm, ok := c.StateMachines[nodeID].(*raft.MockStateMachine); ok {
			return sm
		}
	}
	return nil
}

// AddNode dynamically adds a new node to the cluster.
// The node is created with the given ID and started automatically if auto start has been enabled on the cluster.
// Returns the created node.
func (c *TestCluster) AddNode(nodeID int) (raft.Node, error) {
	var peers []int // Empty initially, will be updated via AddServer
	return c.addNode(nodeID, peers, c.config.autoStart)
}

// AddNode dynamically adds a new node to the cluster.
// The node is created with the given ID and given peers.
// Returns the created node.
func (c *TestCluster) addNode(nodeID int, peers []int, autoStart bool) (raft.Node, error) {
	// Create node config with empty peers (will be updated when added to cluster)
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

	// Create base transport
	var transport raft.Transport
	var err error
	// Use custom factory if provided
	if c.config.transportFactory != nil {
		transport, err = c.config.transportFactory(nodeID, c.Registry)
		if err != nil {
			return nil, fmt.Errorf("failed to create transport for node %d: %w", nodeID, err)
		}
	} else {
		// Create base transport
		transport = NewMultiNodeTransport(nodeID, c.Registry)
	}
	// Apply any additional decorators from options
	for _, decorator := range c.config.transportDecorators {
		transport = decorator(nodeID, transport)
	}

	// Create persistence using factory
	var persistence raft.Persistence
	if c.config.persistenceFactory != nil {
		persistence, err = c.config.persistenceFactory(nodeID)
		if err != nil {
			return nil, fmt.Errorf("failed to create persistence for node %d: %w", nodeID, err)
		}
	} else {
		persistence = raft.NewMockPersistence()
	}

	// Create state machine using factory
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

	// Expand cluster's slices to include this node
	// Protected by mutex to prevent concurrent modifications
	c.mu.Lock()
	c.Registry.Register(nodeID, node.(raft.RPCHandler))
	c.Nodes = append(c.Nodes, node)
	c.Transports = append(c.Transports, transport)
	c.Persistences = append(c.Persistences, persistence)
	c.StateMachines = append(c.StateMachines, stateMachine)
	c.mu.Unlock()

	// Start the node
	if autoStart {
		if err := node.Start(c.ctx); err != nil {
			return nil, fmt.Errorf("failed to start node %d: %w", nodeID, err)
		}
	}

	return node, nil
}

// RemoveNode removes a node from the cluster's tracking (does not stop it)
// This is useful after calling RemoveServer on the Raft cluster
func (c *TestCluster) RemoveNode(nodeID int) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Unregister the node from the registry
	c.Registry.Unregister(nodeID)

	// Find and remove the node from our tracking
	// Note: We don't stop the node here - that should be done separately
	// This just removes it from the cluster's node list
	newNodes := make([]raft.Node, 0, len(c.Nodes))
	newTransports := make([]raft.Transport, 0, len(c.Transports))
	newPersistences := make([]raft.Persistence, 0, len(c.Persistences))
	newStateMachines := make([]raft.StateMachine, 0, len(c.StateMachines))

	for i, node := range c.Nodes {
		// Check if this is the node to remove
		// We need to get the node's ID from its config
		if node != nil {
			// Try to identify the node by checking if it matches the nodeID
			// This is a bit tricky since we don't have direct access to the ID
			// We'll keep all nodes except the one at the position matching nodeID
			// This assumes nodes are added in order, which is true for our test setup
			if i != nodeID {
				newNodes = append(newNodes, node)
				if i < len(c.Transports) {
					newTransports = append(newTransports, c.Transports[i])
				}
				if i < len(c.Persistences) {
					newPersistences = append(newPersistences, c.Persistences[i])
				}
				if i < len(c.StateMachines) {
					newStateMachines = append(newStateMachines, c.StateMachines[i])
				}
			}
		}
	}

	c.Nodes = newNodes
	c.Transports = newTransports
	c.Persistences = newPersistences
	c.StateMachines = newStateMachines
}

// WaitForLeader waits for a leader to be elected and returns its ID
func (c *TestCluster) WaitForLeader(timeout time.Duration) (int, error) {
	c.t.Helper()
	c.mu.RLock()
	nodes := c.Nodes
	c.mu.RUnlock()
	leaderID := WaitForLeader(c.t, nodes, timeout)
	return leaderID, nil
}

// WaitForCommitIndex waits for all nodes to reach at least the specified commit index
func (c *TestCluster) WaitForCommitIndex(index int, timeout time.Duration) error {
	c.t.Helper()
	c.mu.RLock()
	nodes := c.Nodes
	c.mu.RUnlock()
	WaitForCommitIndex(c.t, nodes, index, timeout)
	return nil
}

// WaitForStableCluster waits for the cluster to stabilize with a leader
func (c *TestCluster) WaitForStableCluster(timeout time.Duration) {
	c.t.Helper()
	// Wait for a leader to be elected
	c.mu.RLock()
	nodes := c.Nodes
	c.mu.RUnlock()
	WaitForLeader(c.t, nodes, timeout)
}
