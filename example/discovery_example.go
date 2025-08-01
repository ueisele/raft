package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/ueisele/raft"
	"github.com/ueisele/raft/persistence/json"
	"github.com/ueisele/raft/transport"
	"github.com/ueisele/raft/transport/http"
)

// Example of setting up a Raft cluster with proper peer discovery
func main() {
	// Define cluster configuration
	clusterPeers := map[int]string{
		0: "node0.example.com:8000",
		1: "node1.example.com:8001",
		2: "node2.example.com:8002",
	}

	// Get this node's ID from environment or configuration
	nodeID := 0 // In production, this would come from configuration

	// Create discovery mechanism
	discovery := transport.NewStaticPeerDiscovery(clusterPeers)

	// Create HTTP transport with discovery
	transportConfig := &transport.Config{
		ServerID:   nodeID,
		Address:    clusterPeers[nodeID],
		RPCTimeout: 5000, // 5 seconds
	}

	httpTransport, err := http.NewHTTPTransportWithDiscovery(transportConfig, discovery)
	if err != nil {
		log.Fatalf("Failed to create transport: %v", err)
	}

	// Alternative: Use the builder pattern
	httpTransportAlt, err := http.NewBuilder(nodeID, clusterPeers[nodeID]).
		WithTimeout(5 * time.Second).
		WithDiscovery(discovery).
		Build()
	if err != nil {
		log.Fatalf("Failed to build transport: %v", err)
	}

	// Create persistence
	persistenceConfig := &raft.PersistenceConfig{
		DataDir:  fmt.Sprintf("/var/lib/raft/node%d", nodeID),
		ServerID: nodeID,
	}

	persistence, err := json.NewJSONPersistence(persistenceConfig)
	if err != nil {
		log.Fatalf("Failed to create persistence: %v", err)
	}

	// Create state machine (application-specific)
	stateMachine := NewMyStateMachine()

	// Create Raft configuration
	raftConfig := &raft.Config{
		ID:                 nodeID,
		Peers:              []int{0, 1, 2},
		ElectionTimeoutMin: 500 * time.Millisecond,
		ElectionTimeoutMax: 1000 * time.Millisecond,
		HeartbeatInterval:  200 * time.Millisecond,
		MaxLogSize:         10000,
	}

	// Create Raft node
	node, err := raft.NewNode(raftConfig, httpTransport, persistence, stateMachine)
	if err != nil {
		log.Fatalf("Failed to create node: %v", err)
	}

	// Set RPC handler for the transport
	httpTransport.SetRPCHandler(node)

	// Start transport
	if err := httpTransport.Start(); err != nil {
		log.Fatalf("Failed to start transport: %v", err)
	}

	// Start Raft node
	ctx := context.Background()
	if err := node.Start(ctx); err != nil {
		log.Fatalf("Failed to start node: %v", err)
	}

	// Example: Dynamic peer updates (e.g., from a configuration service)
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				// In production, this would fetch from a configuration service
				// For now, just demonstrate the API
				updatedPeers := getUpdatedPeersFromConfigService()
				if updatedPeers != nil {
					discovery.UpdatePeers(updatedPeers)
					log.Println("Updated peer configuration")
				}
			}
		}
	}()

	// Run forever
	select {}
}

// Placeholder for state machine implementation
type MyStateMachine struct{}

func NewMyStateMachine() *MyStateMachine {
	return &MyStateMachine{}
}

func (sm *MyStateMachine) Apply(entry raft.LogEntry) interface{} {
	// Apply the command to your application state
	return nil
}

func (sm *MyStateMachine) Snapshot() ([]byte, error) {
	// Serialize your application state
	return []byte{}, nil
}

func (sm *MyStateMachine) Restore(data []byte) error {
	// Restore your application state from snapshot
	return nil
}

// Placeholder for configuration service integration
func getUpdatedPeersFromConfigService() map[int]string {
	// In production, this would:
	// 1. Connect to Consul, etcd, or another configuration service
	// 2. Fetch the current cluster membership
	// 3. Return the updated peer map
	return nil
}

// Example: Custom discovery implementation for cloud environments
type CloudDiscovery struct {
	cloudProvider string
	serviceName   string
}

func NewCloudDiscovery(provider, serviceName string) *CloudDiscovery {
	return &CloudDiscovery{
		cloudProvider: provider,
		serviceName:   serviceName,
	}
}

func (c *CloudDiscovery) GetPeerAddress(ctx context.Context, serverID int) (string, error) {
	// In production, this would:
	// 1. Query cloud provider API (AWS, GCP, Azure)
	// 2. Find instance by tags or metadata
	// 3. Return the instance's private IP and port
	
	switch c.cloudProvider {
	case "aws":
		// Use AWS SDK to query EC2 instances
		// Filter by tags: service=raft, server-id=X
		return c.getAWSInstanceAddress(ctx, serverID)
	case "gcp":
		// Use GCP SDK to query compute instances
		return c.getGCPInstanceAddress(ctx, serverID)
	case "k8s":
		// Use Kubernetes API to query pods
		return c.getK8sServiceAddress(ctx, serverID)
	default:
		return "", fmt.Errorf("unsupported cloud provider: %s", c.cloudProvider)
	}
}

func (c *CloudDiscovery) RefreshPeers(ctx context.Context) error {
	// Refresh cached peer information
	return nil
}

func (c *CloudDiscovery) Close() error {
	// Clean up any resources
	return nil
}

func (c *CloudDiscovery) getAWSInstanceAddress(ctx context.Context, serverID int) (string, error) {
	// Placeholder for AWS implementation
	return "", fmt.Errorf("AWS discovery not implemented")
}

func (c *CloudDiscovery) getGCPInstanceAddress(ctx context.Context, serverID int) (string, error) {
	// Placeholder for GCP implementation
	return "", fmt.Errorf("GCP discovery not implemented")
}

func (c *CloudDiscovery) getK8sServiceAddress(ctx context.Context, serverID int) (string, error) {
	// In Kubernetes, use predictable StatefulSet DNS names
	// e.g., raft-0.raft-service.default.svc.cluster.local
	return fmt.Sprintf("raft-%d.%s.default.svc.cluster.local:8000", serverID, c.serviceName), nil
}