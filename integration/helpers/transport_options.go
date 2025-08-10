package helpers

import (
	"github.com/ueisele/raft"
	"github.com/ueisele/raft/integration/helpers/transporttest"
)

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

// WithDelayTransport adds delay capability to transports
func WithDelayTransport() ClusterOption {
	return WithTransportDecorators(func(nodeID int, wrapped raft.Transport) raft.Transport {
		return transporttest.NewDelayDecorator(wrapped)
	})
}

// WithDebugTransport adds debug logging to transports
func WithDebugTransport(logger raft.Logger) ClusterOption {
	return WithTransportDecorators(func(nodeID int, wrapped raft.Transport) raft.Transport {
		return transporttest.NewDebugDecorator(wrapped, nodeID, logger)
	})
}
