package test

import (
	"context"
	"testing"
	"time"

	"github.com/ueisele/raft"
)

// TestCluster represents a test Raft cluster
type TestCluster struct {
	Nodes         []raft.Node
	Transports    []*InMemoryTransport
	Persistences  []*InMemoryPersistence
	StateMachines []*SimpleStateMachine
	Config        *raft.Config
}

// ClusterConfig holds configuration for creating a test cluster
type ClusterConfig struct {
	Size               int
	ElectionTimeoutMin time.Duration
	ElectionTimeoutMax time.Duration
	HeartbeatInterval  time.Duration
	MaxLogSize         int
	WithPersistence    bool
}

// DefaultClusterConfig returns a default cluster configuration
func DefaultClusterConfig(size int) *ClusterConfig {
	return &ClusterConfig{
		Size:               size,
		ElectionTimeoutMin: 150 * time.Millisecond,
		ElectionTimeoutMax: 300 * time.Millisecond,
		HeartbeatInterval:  50 * time.Millisecond,
		MaxLogSize:         1000,
		WithPersistence:    true,
	}
}

// NewTestCluster creates a new test cluster
func NewTestCluster(t *testing.T, config *ClusterConfig) *TestCluster {
	cluster := &TestCluster{
		Nodes:         make([]raft.Node, config.Size),
		Transports:    make([]*InMemoryTransport, config.Size),
		Persistences:  make([]*InMemoryPersistence, config.Size),
		StateMachines: make([]*SimpleStateMachine, config.Size),
	}

	// Create peers list
	peers := make([]int, config.Size)
	for i := 0; i < config.Size; i++ {
		peers[i] = i
	}

	// Create nodes
	for i := 0; i < config.Size; i++ {
		// Create transport
		transport := NewInMemoryTransport(i)
		cluster.Transports[i] = transport

		// Create persistence
		var persistence raft.Persistence
		if config.WithPersistence {
			p := NewInMemoryPersistence()
			cluster.Persistences[i] = p
			persistence = p
		}

		// Create state machine
		stateMachine := NewSimpleStateMachine()
		cluster.StateMachines[i] = stateMachine

		// Create Raft config
		raftConfig := &raft.Config{
			ID:                 i,
			Peers:              peers,
			ElectionTimeoutMin: config.ElectionTimeoutMin,
			ElectionTimeoutMax: config.ElectionTimeoutMax,
			HeartbeatInterval:  config.HeartbeatInterval,
			MaxLogSize:         config.MaxLogSize,
		}

		// Create node
		node, err := raft.NewNode(raftConfig, transport, persistence, stateMachine)
		if err != nil {
			t.Fatalf("Failed to create node %d: %v", i, err)
		}

		cluster.Nodes[i] = node
	}

	// Register all nodes with each transport
	for i := 0; i < config.Size; i++ {
		for j := 0; j < config.Size; j++ {
			cluster.Transports[i].RegisterServer(j, cluster.Nodes[j].(raft.RPCHandler))
		}
	}

	cluster.Config = &raft.Config{
		ElectionTimeoutMin: config.ElectionTimeoutMin,
		ElectionTimeoutMax: config.ElectionTimeoutMax,
		HeartbeatInterval:  config.HeartbeatInterval,
		MaxLogSize:         config.MaxLogSize,
	}

	return cluster
}

// Start starts all nodes in the cluster
func (c *TestCluster) Start(ctx context.Context) error {
	for _, node := range c.Nodes {
		if err := node.Start(ctx); err != nil {
			return err
		}
	}
	return nil
}

// Stop stops all nodes in the cluster
func (c *TestCluster) Stop() {
	for _, node := range c.Nodes {
		node.Stop()
	}
}

// DisconnectNode disconnects a specific node from the cluster
func (c *TestCluster) DisconnectNode(nodeID int) {
	if nodeID < 0 || nodeID >= len(c.Transports) {
		return
	}
	for _, transport := range c.Transports {
		transport.DisconnectServer(nodeID)
	}
}

// ReconnectNode reconnects a specific node to the cluster
func (c *TestCluster) ReconnectNode(nodeID int) {
	if nodeID < 0 || nodeID >= len(c.Transports) {
		return
	}
	for _, transport := range c.Transports {
		transport.ReconnectServer(nodeID)
	}
}

// CreatePartition creates a network partition between two groups
func (c *TestCluster) CreatePartition(group1, group2 []int) {
	for _, id1 := range group1 {
		for _, id2 := range group2 {
			if id1 < len(c.Transports) && id2 < len(c.Transports) {
				c.Transports[id1].DisconnectPair(id1, id2)
			}
		}
	}
}

// HealPartition removes a network partition between two groups
func (c *TestCluster) HealPartition(group1, group2 []int) {
	for _, id1 := range group1 {
		for _, id2 := range group2 {
			if id1 < len(c.Transports) && id2 < len(c.Transports) {
				c.Transports[id1].ReconnectPair(id1, id2)
			}
		}
	}
}

// GetLeader returns the current leader node, or nil if no leader
func (c *TestCluster) GetLeader() (raft.Node, int) {
	for i, node := range c.Nodes {
		if node.IsLeader() {
			return node, i
		}
	}
	return nil, -1
}

// SubmitCommand submits a command to the leader
func (c *TestCluster) SubmitCommand(command interface{}) (int, int, bool) {
	leader, _ := c.GetLeader()
	if leader == nil {
		return -1, -1, false
	}
	return leader.Submit(command)
}

// WaitForLeaderElection waits for a leader to be elected
func (c *TestCluster) WaitForLeaderElection(t *testing.T, timeout time.Duration) int {
	return WaitForLeader(t, c.Nodes, timeout)
}

// WaitForReplication waits for a command to be replicated to all nodes
func (c *TestCluster) WaitForReplication(t *testing.T, minIndex int, timeout time.Duration) {
	WaitForCommitIndex(t, c.Nodes, minIndex, timeout)
}

// GetAppliedCount returns the total number of applied entries across all nodes
func (c *TestCluster) GetAppliedCount() int {
	count := 0
	for _, sm := range c.StateMachines {
		count += sm.GetAppliedCount()
	}
	return count
}

// VerifyConsistency verifies that all nodes have consistent state
func (c *TestCluster) VerifyConsistency(t *testing.T) {
	if len(c.Nodes) == 0 {
		return
	}

	// Get the first node's state as reference
	refNode := c.Nodes[0]
	refCommitIndex := refNode.GetCommitIndex()
	refTerm := refNode.GetCurrentTerm()

	// Compare with all other nodes
	for i := 1; i < len(c.Nodes); i++ {
		node := c.Nodes[i]
		commitIndex := node.GetCommitIndex()
		term := node.GetCurrentTerm()

		// Allow some difference in commit index due to replication lag
		if commitIndex < refCommitIndex-1 || commitIndex > refCommitIndex+1 {
			t.Errorf("Node %d has inconsistent commit index: %d vs %d", i, commitIndex, refCommitIndex)
		}

		// Terms should be close
		if term < refTerm-1 || term > refTerm+1 {
			t.Errorf("Node %d has inconsistent term: %d vs %d", i, term, refTerm)
		}
	}
}
