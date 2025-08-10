package transporttest

import (
	"fmt"
)

// PartitionCapable indicates a transport can simulate network partitions.
// This capability allows tests to simulate network failures and partitions
// between nodes in a cluster.
type PartitionCapable interface {
	// Block prevents communication with a specific server
	Block(serverID int)
	// Unblock allows communication with a specific server
	Unblock(serverID int)
	// BlockAll blocks communication with all servers
	BlockAll()
	// UnblockAll unblocks communication with all servers
	UnblockAll()
	// IsBlocked checks if communication with a server is blocked
	IsBlocked(serverID int) bool
}

// PartitionNode partitions a specific node from all others.
// The node will not be able to send or receive messages from any other node.
func PartitionNode(provider TransportProvider, nodeID int) error {
	// First, block the node from sending to others
	if partition, ok := GetCapability[PartitionCapable](provider, nodeID); ok {
		partition.BlockAll()
	} else {
		return fmt.Errorf("node %d transport does not support partitioning", nodeID)
	}

	// Then, block all other nodes from sending to this node
	transports := provider.GetTransports()
	for id := range transports {
		if id != nodeID {
			if partition, ok := GetCapability[PartitionCapable](provider, id); ok {
				partition.Block(nodeID)
			}
		}
	}

	return nil
}

// HealPartition removes all network partitions in the cluster.
// All nodes will be able to communicate with each other again.
func HealPartition(provider TransportProvider) {
	transports := provider.GetTransports()
	for id := range transports {
		if partition, ok := GetCapability[PartitionCapable](provider, id); ok {
			partition.UnblockAll()
		}
	}
}

// CreatePartition creates a network partition between two groups of nodes.
// Nodes in group1 cannot communicate with nodes in group2 and vice versa.
// Nodes within the same group can still communicate with each other.
func CreatePartition(provider TransportProvider, group1, group2 []int) error {
	// Block communication from group1 to group2
	for _, id1 := range group1 {
		if partition, ok := GetCapability[PartitionCapable](provider, id1); ok {
			for _, id2 := range group2 {
				partition.Block(id2)
			}
		} else {
			return fmt.Errorf("node %d transport does not support partitioning", id1)
		}
	}

	// Block communication from group2 to group1
	for _, id2 := range group2 {
		if partition, ok := GetCapability[PartitionCapable](provider, id2); ok {
			for _, id1 := range group1 {
				partition.Block(id1)
			}
		} else {
			return fmt.Errorf("node %d transport does not support partitioning", id2)
		}
	}

	return nil
}
