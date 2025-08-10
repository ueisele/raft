package transporttest

import (
	"time"
)

// DelayCapable indicates a transport can introduce artificial delays.
// This capability is useful for testing timing-related behaviors and
// race conditions in distributed systems.
type DelayCapable interface {
	// SetDelay sets the delay for RPCs to a specific node (-1 for all nodes)
	SetDelay(serverID int, delay time.Duration)
	// GetDelay returns the current delay for a specific node
	GetDelay(serverID int) time.Duration
	// ClearDelays removes all configured delays
	ClearDelays()
}

// SetDelay sets a delay for all transports in the cluster when communicating
// with the specified server. Use serverID -1 to set delay for all servers.
func SetDelay(cluster TestCluster, serverID int, delay time.Duration) {
	transports := cluster.GetTransports()
	for i := range transports {
		if delayCapable, ok := GetCapability[DelayCapable](transports, i); ok {
			delayCapable.SetDelay(serverID, delay)
		}
	}
}

// SetDelayBetween sets a delay for communication from one specific node to another.
// This allows for asymmetric delays in the network.
func SetDelayBetween(cluster TestCluster, fromNode, toNode int, delay time.Duration) {
	transports := cluster.GetTransports()
	if fromNode >= 0 && fromNode < len(transports) {
		if delayCapable, ok := GetCapability[DelayCapable](transports, fromNode); ok {
			delayCapable.SetDelay(toNode, delay)
		}
	}
}

// ClearAllDelays removes all delays from all transports in the cluster.
func ClearAllDelays(cluster TestCluster) {
	transports := cluster.GetTransports()
	for i := range transports {
		if delayCapable, ok := GetCapability[DelayCapable](transports, i); ok {
			delayCapable.ClearDelays()
		}
	}
}

// SimulateSlowNetwork adds a uniform delay to all communications in the cluster.
// This is useful for testing behavior under high-latency conditions.
func SimulateSlowNetwork(cluster TestCluster, delay time.Duration) {
	SetDelay(cluster, -1, delay)
}

// SimulateAsymmetricDelay creates an asymmetric delay where one node has
// slow outbound connections but normal inbound connections.
func SimulateAsymmetricDelay(cluster TestCluster, slowNode int, delay time.Duration) {
	transports := cluster.GetTransports()
	if slowNode >= 0 && slowNode < len(transports) {
		if delayCapable, ok := GetCapability[DelayCapable](transports, slowNode); ok {
			delayCapable.SetDelay(-1, delay)
		}
	}
}