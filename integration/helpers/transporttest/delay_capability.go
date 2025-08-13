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
func SetDelay(provider TransportProvider, serverID int, delay time.Duration) {
	transports := provider.GetTransports()
	for id := range transports {
		if delayCapable, ok := GetCapability[DelayCapable](provider, id); ok {
			delayCapable.SetDelay(serverID, delay)
		}
	}
}

// SetDelayBetween sets a delay for communication from one specific node to another.
// This allows for asymmetric delays in the network.
func SetDelayBetween(provider TransportProvider, fromNode, toNode int, delay time.Duration) {
	if delayCapable, ok := GetCapability[DelayCapable](provider, fromNode); ok {
		delayCapable.SetDelay(toNode, delay)
	}
}

// ClearAllDelays removes all delays from all transports in the cluster.
func ClearAllDelays(provider TransportProvider) {
	transports := provider.GetTransports()
	for id := range transports {
		if delayCapable, ok := GetCapability[DelayCapable](provider, id); ok {
			delayCapable.ClearDelays()
		}
	}
}

// SimulateSlowNetwork adds a uniform delay to all communications in the cluster.
// This is useful for testing behavior under high-latency conditions.
func SimulateSlowNetwork(provider TransportProvider, delay time.Duration) {
	SetDelay(provider, -1, delay)
}

// SimulateAsymmetricDelay creates an asymmetric delay where one node has
// slow outbound connections but normal inbound connections.
func SimulateAsymmetricDelay(provider TransportProvider, slowNode int, delay time.Duration) {
	if delayCapable, ok := GetCapability[DelayCapable](provider, slowNode); ok {
		delayCapable.SetDelay(-1, delay)
	}
}
