package helpers

import (
	"fmt"

	"github.com/ueisele/raft"
	"github.com/ueisele/raft/integration/helpers/transporttest"
	"github.com/ueisele/raft/transport"
	httpTransport "github.com/ueisele/raft/transport/http"
)

// WithHTTPTransport creates a cluster option for using HTTP transport
func WithHTTPTransport(ports []int) ClusterOption {
	if len(ports) == 0 {
		panic("WithHTTPTransport requires at least one port")
	}

	// Create peer discovery with all node addresses
	peers := make(map[int]string)
	for i, port := range ports {
		peers[i] = fmt.Sprintf("localhost:%d", port)
	}
	discovery := transport.NewStaticPeerDiscovery(peers)

	return WithTransportFactory(func(nodeID int, registry *transporttest.NodeRegistry) (raft.Transport, error) {
		if nodeID >= len(ports) {
			return nil, fmt.Errorf("node ID %d exceeds available ports", nodeID)
		}

		config := &transport.Config{
			ServerID:   nodeID,
			Address:    peers[nodeID],
			RPCTimeout: 1000, // 1 second timeout
		}

		trans, err := httpTransport.NewHTTPTransport(config, discovery)
		if err != nil {
			return nil, fmt.Errorf("failed to create HTTP transport for node %d: %w", nodeID, err)
		}

		return trans, nil
	})
}