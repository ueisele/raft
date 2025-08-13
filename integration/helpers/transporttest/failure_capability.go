package transporttest

// FailureCapable indicates a transport can simulate random failures.
// This capability allows tests to inject network failures at a specified rate
// to test the resilience of the Raft implementation.
type FailureCapable interface {
	// SetFailureRate sets the probability of failure (0.0 to 1.0)
	SetFailureRate(rate float64)
	// GetFailureRate returns the current failure rate
	GetFailureRate() float64
	// GetStats returns the number of attempts and failures
	GetStats() (attempts, failures int64)
	// ResetStats resets the statistics counters
	ResetStats()
}

// SetFailureRate sets the failure rate for all transports that support it.
// The rate should be between 0.0 (no failures) and 1.0 (always fail).
func SetFailureRate(provider TransportProvider, rate float64) {
	transports := provider.GetTransports()
	for id := range transports {
		if failure, ok := GetCapability[FailureCapable](provider, id); ok {
			failure.SetFailureRate(rate)
		}
	}
}

// GetFailureStats returns aggregated failure statistics from all transports.
// It sums up the total attempts and failures across all nodes that support
// the FailureCapable interface.
func GetFailureStats(provider TransportProvider) (totalAttempts, totalFailures int64) {
	transports := provider.GetTransports()
	for id := range transports {
		if failure, ok := GetCapability[FailureCapable](provider, id); ok {
			attempts, failures := failure.GetStats()
			totalAttempts += attempts
			totalFailures += failures
		}
	}
	return totalAttempts, totalFailures
}

// ResetFailureStats resets failure statistics for all transports that support it.
func ResetFailureStats(provider TransportProvider) {
	transports := provider.GetTransports()
	for id := range transports {
		if failure, ok := GetCapability[FailureCapable](provider, id); ok {
			failure.ResetStats()
		}
	}
}
