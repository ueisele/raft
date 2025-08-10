package transporttest

// GetCapability returns a capability from a transport if it supports it.
// It will recursively unwrap decorators to find the capability.
// This is useful for accessing specific capabilities like PartitionCapable
// or FailureCapable from potentially decorated transport.
func GetCapability[T any](provider TransportProvider, nodeID int) (T, bool) {
	transports := provider.GetTransports()
	transport, ok := transports[nodeID]
	if !ok {
		var zero T
		return zero, false
	}

	// Check if transport directly supports the capability
	if capability, ok := transport.(T); ok {
		return capability, ok
	}

	// Try to unwrap decorators to find the capability
	for {
		if decorator, ok := transport.(Decorator); ok {
			wrapped := decorator.Unwrap()
			if capability, ok := wrapped.(T); ok {
				return capability, true
			}
			transport = wrapped
		} else {
			break
		}
	}
	
	var zero T
	return zero, false
}
