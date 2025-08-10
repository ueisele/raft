package helpers

import (
	"net"
)

// GetFreePorts allocates n free ports and returns them.
// It tries to minimize race conditions by allocating all ports at once.
// Note: The port is released when this function returns, so there's a small
// chance another process could claim it before your server starts.
func GetFreePorts(n int) ([]int, error) {
	listeners := make([]net.Listener, n)
	ports := make([]int, n)

	// First, open all listeners
	for i := 0; i < n; i++ {
		listener, err := net.Listen("tcp", "localhost:0")
		if err != nil {
			// Clean up any listeners we already opened
			for j := 0; j < i; j++ {
				listeners[j].Close() //nolint:errcheck // cleanup on error
			}
			return nil, err
		}
		listeners[i] = listener
		ports[i] = listener.Addr().(*net.TCPAddr).Port
	}

	// Then close them all
	for i := 0; i < n; i++ {
		listeners[i].Close() //nolint:errcheck // cleanup on error
	}

	return ports, nil
}

// Note: MultiNodeTransport and NodeRegistry have been moved to transporttest package
// See integration/helpers/transporttest/multi_node.go for the implementations
