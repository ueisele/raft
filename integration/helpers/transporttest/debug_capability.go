package transporttest

import "github.com/ueisele/raft"

// DebugCapable indicates a transport can provide debug logging.
// This capability allows tests to enable detailed logging of all
// RPC traffic for debugging purposes.
type DebugCapable interface {
	// SetLogger sets the logger for debug output
	SetLogger(logger raft.Logger)
}

// SetDebugLogger sets the logger for all transports that support debug logging.
// This is useful for enabling detailed RPC logging across all nodes in a test cluster.
func SetDebugLogger(cluster TestCluster, logger raft.Logger) {
	transports := cluster.GetTransports()
	for i := range transports {
		if debug, ok := GetCapability[DebugCapable](transports, i); ok {
			debug.SetLogger(logger)
		}
	}
}

// EnableDebugLogging is a convenience function to enable debug logging
// with a test logger for all transports that support it.
func EnableDebugLogging(cluster TestCluster, logger raft.Logger) {
	SetDebugLogger(cluster, logger)
}
