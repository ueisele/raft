package helpers

import (
	"github.com/ueisele/raft"
	"github.com/ueisele/raft/integration/helpers/transporttest"
)

// Type aliases for backward compatibility and convenience
type (
	// NodeRegistry is exported from transporttest package
	NodeRegistry = transporttest.NodeRegistry
	// MultiNodeTransport is exported from transporttest package
	MultiNodeTransport = transporttest.MultiNodeTransport
)

// Constructor functions for backward compatibility
var (
	NewNodeRegistry           = transporttest.NewNodeRegistry
	NewNodeRegistryWithLogger = transporttest.NewNodeRegistryWithLogger
	NewMultiNodeTransport     = transporttest.NewMultiNodeTransport
)

// ========== Transport Decorator Options ==========

// WithPartitionableTransport adds partitioning capability to transports
func WithPartitionableTransport() ClusterOption {
	return WithTransportDecorators(func(nodeID int, wrapped raft.Transport) raft.Transport {
		return transporttest.NewPartitionableDecorator(wrapped)
	})
}

// WithFailureTransport adds failure simulation to transports
func WithFailureTransport(failureRate float64) ClusterOption {
	return WithTransportDecorators(func(nodeID int, wrapped raft.Transport) raft.Transport {
		return transporttest.NewFailureDecorator(wrapped, failureRate)
	})
}

// WithDebugTransport adds debug logging to transports
func WithDebugTransport(logger raft.Logger) ClusterOption {
	return WithTransportDecorators(func(nodeID int, wrapped raft.Transport) raft.Transport {
		return transporttest.NewDebugDecorator(wrapped, nodeID, logger)
	})
}

// WithDelayTransport adds delay capability to transports
func WithDelayTransport() ClusterOption {
	return WithTransportDecorators(func(nodeID int, wrapped raft.Transport) raft.Transport {
		return transporttest.NewDelayDecorator(wrapped)
	})
}

// ========== Helper Functions that need TestCluster ==========

// GetTransports implements the TestCluster interface for transporttest package
func (c *TestCluster) GetTransports() []raft.Transport {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.Transports
}

// GetTransportCapability returns a capability from a transport if it supports it
// This is a convenience wrapper that works with TestCluster
func GetTransportCapability[T any](cluster *TestCluster, nodeID int) (T, bool) {
	return transporttest.GetCapability[T](cluster.GetTransports(), nodeID)
}

// PartitionNode partitions a specific node from all others
func PartitionNode(cluster *TestCluster, nodeID int) error {
	return transporttest.PartitionNode(cluster, nodeID)
}

// HealPartition removes all network partitions
func HealPartition(cluster *TestCluster) {
	transporttest.HealPartition(cluster)
}

// CreatePartition creates a network partition between two groups
func CreatePartition(cluster *TestCluster, group1, group2 []int) error {
	return transporttest.CreatePartition(cluster, group1, group2)
}

// SetFailureRate sets the failure rate for all transports that support it
func SetFailureRate(cluster *TestCluster, rate float64) {
	transporttest.SetFailureRate(cluster, rate)
}

// GetFailureStats returns aggregated failure statistics from all transports
func GetFailureStats(cluster *TestCluster) (totalAttempts, totalFailures int64) {
	return transporttest.GetFailureStats(cluster)
}

// SetDebugLogger sets the logger for all transports that support it
func SetDebugLogger(cluster *TestCluster, logger raft.Logger) {
	transporttest.SetDebugLogger(cluster, logger)
}
