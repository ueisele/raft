package raft

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// ============================================================================
// Timing Configuration
// ============================================================================

// TimingConfig provides configurable timing parameters for tests
type TimingConfig struct {
	// PollInterval is how often to check conditions
	PollInterval time.Duration
	// DefaultTimeout is the default timeout for operations
	DefaultTimeout time.Duration
	// ElectionTimeout is the timeout to wait for elections
	ElectionTimeout time.Duration
	// ReplicationTimeout is the timeout for replication
	ReplicationTimeout time.Duration
	// StabilizationTimeout is the timeout for cluster stabilization
	StabilizationTimeout time.Duration
}

// DefaultTimingConfig returns a default timing configuration
func DefaultTimingConfig() *TimingConfig {
	return &TimingConfig{
		PollInterval:         10 * time.Millisecond,
		DefaultTimeout:       5 * time.Second,
		ElectionTimeout:      2 * time.Second,
		ReplicationTimeout:   3 * time.Second,
		StabilizationTimeout: 5 * time.Second,
	}
}

// FastTimingConfig returns a fast timing configuration for quick tests
func FastTimingConfig() *TimingConfig {
	return &TimingConfig{
		PollInterval:         5 * time.Millisecond,
		DefaultTimeout:       1 * time.Second,
		ElectionTimeout:      500 * time.Millisecond,
		ReplicationTimeout:   500 * time.Millisecond,
		StabilizationTimeout: 1 * time.Second,
	}
}

// SlowTimingConfig returns a slow timing configuration for heavily loaded systems
func SlowTimingConfig() *TimingConfig {
	return &TimingConfig{
		PollInterval:         50 * time.Millisecond,
		DefaultTimeout:       30 * time.Second,
		ElectionTimeout:      10 * time.Second,
		ReplicationTimeout:   15 * time.Second,
		StabilizationTimeout: 30 * time.Second,
	}
}

// ============================================================================
// Core Wait Functions
// ============================================================================

// WaitForCondition waits for a condition to become true
func WaitForCondition(t *testing.T, condition func() bool, timeout time.Duration, description string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			t.Fatalf("Timeout waiting for %s after %v", description, timeout)
		case <-ticker.C:
			if condition() {
				return
			}
		}
	}
}

// WaitForConditionWithProgress waits for a condition with progress updates
func WaitForConditionWithProgress(t *testing.T, condition func() (bool, string), timeout time.Duration, description string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

	startTime := time.Now()
	lastLog := time.Now()
	logInterval := timeout / 10
	if logInterval < 100*time.Millisecond {
		logInterval = 100 * time.Millisecond
	}

	for {
		select {
		case <-ctx.Done():
			met, status := condition()
			elapsed := time.Since(startTime)
			t.Fatalf("Timeout waiting for %s after %v. Final status: %s (condition met: %v)",
				description, elapsed, status, met)
		case <-ticker.C:
			met, status := condition()
			if met {
				return
			}

			// Log progress periodically
			if time.Since(lastLog) >= logInterval {
				elapsed := time.Since(startTime)
				t.Logf("Progress [%s]: %s (elapsed: %v)", description, status, elapsed)
				lastLog = time.Now()
			}
		}
	}
}

// ============================================================================
// Leader Election Helpers
// ============================================================================

// WaitForLeader waits for exactly one leader to be elected
func WaitForLeader(t *testing.T, nodes []Node) int {
	t.Helper()
	return WaitForLeaderWithConfig(t, nodes, DefaultTimingConfig())
}

// WaitForLeaderWithConfig waits for exactly one leader with custom timing
func WaitForLeaderWithConfig(t *testing.T, nodes []Node, config *TimingConfig) int {
	t.Helper()
	var leaderID int = -1

	WaitForConditionWithProgress(t, func() (bool, string) {
		leaderCount := 0
		leaderID = -1
		for i, node := range nodes {
			if node != nil && node.IsLeader() {
				leaderCount++
				leaderID = i
			}
		}

		if leaderCount > 1 {
			t.Fatalf("Multiple leaders detected: %d", leaderCount)
		}

		return leaderCount == 1, fmt.Sprintf("%d leaders", leaderCount)
	}, config.ElectionTimeout, "leader election")

	return leaderID
}

// WaitForNewLeader waits for a new leader different from the old one
func WaitForNewLeader(t *testing.T, nodes []Node, oldLeaderID int) int {
	t.Helper()
	return WaitForNewLeaderWithConfig(t, nodes, oldLeaderID, DefaultTimingConfig())
}

// WaitForNewLeaderWithConfig waits for a new leader with custom timing
func WaitForNewLeaderWithConfig(t *testing.T, nodes []Node, oldLeaderID int, config *TimingConfig) int {
	t.Helper()
	var newLeaderID int = -1

	WaitForConditionWithProgress(t, func() (bool, string) {
		leaderCount := 0
		newLeaderID = -1
		for i, node := range nodes {
			if node != nil && node.IsLeader() {
				leaderCount++
				if i != oldLeaderID {
					newLeaderID = i
				}
			}
		}

		if leaderCount > 1 {
			t.Fatalf("Multiple leaders detected: %d", leaderCount)
		}

		hasNewLeader := newLeaderID != -1 && newLeaderID != oldLeaderID
		return hasNewLeader, fmt.Sprintf("leader count: %d, new leader: %d", leaderCount, newLeaderID)
	}, config.ElectionTimeout, "new leader election")

	return newLeaderID
}

// WaitForNoLeader waits until there is no leader
func WaitForNoLeader(t *testing.T, nodes []Node) {
	t.Helper()
	WaitForNoLeaderWithConfig(t, nodes, DefaultTimingConfig())
}

// WaitForNoLeaderWithConfig waits until there is no leader with custom timing
func WaitForNoLeaderWithConfig(t *testing.T, nodes []Node, config *TimingConfig) {
	t.Helper()
	WaitForConditionWithProgress(t, func() (bool, string) {
		leaderCount := 0
		for _, node := range nodes {
			if node != nil && node.IsLeader() {
				leaderCount++
			}
		}
		return leaderCount == 0, fmt.Sprintf("%d leaders", leaderCount)
	}, config.ElectionTimeout, "no leader")
}

// ============================================================================
// Replication Helpers
// ============================================================================

// WaitForCommitIndex waits for all nodes to reach at least the given commit index
func WaitForCommitIndex(t *testing.T, nodes []Node, targetIndex int) {
	t.Helper()
	WaitForCommitIndexWithConfig(t, nodes, targetIndex, DefaultTimingConfig())
}

// WaitForCommitIndexWithConfig waits for commit index with custom timing
func WaitForCommitIndexWithConfig(t *testing.T, nodes []Node, targetIndex int, config *TimingConfig) {
	t.Helper()
	WaitForConditionWithProgress(t, func() (bool, string) {
		minCommit := -1
		maxCommit := -1
		commitIndices := make([]int, 0, len(nodes))

		for _, node := range nodes {
			if node == nil {
				continue
			}

			commitIndex := node.GetCommitIndex()
			commitIndices = append(commitIndices, commitIndex)

			if minCommit == -1 || commitIndex < minCommit {
				minCommit = commitIndex
			}
			if commitIndex > maxCommit {
				maxCommit = commitIndex
			}
		}

		allReached := minCommit >= targetIndex
		status := fmt.Sprintf("commit indices: %v (min: %d, max: %d, target: %d)",
			commitIndices, minCommit, maxCommit, targetIndex)

		return allReached, status
	}, config.ReplicationTimeout, "replication")
}

// WaitForLogReplication waits for all nodes to have the same log length
func WaitForLogReplication(t *testing.T, nodes []Node) {
	t.Helper()
	WaitForLogReplicationWithConfig(t, nodes, DefaultTimingConfig())
}

// WaitForLogReplicationWithConfig waits for log replication with custom timing
func WaitForLogReplicationWithConfig(t *testing.T, nodes []Node, config *TimingConfig) {
	t.Helper()
	WaitForConditionWithProgress(t, func() (bool, string) {
		var expectedLength int
		first := true
		logLengths := make([]int, 0, len(nodes))

		for _, node := range nodes {
			if node == nil {
				continue
			}

			length := node.GetLogLength()
			logLengths = append(logLengths, length)

			if first {
				expectedLength = length
				first = false
			} else if length != expectedLength {
				return false, fmt.Sprintf("log lengths differ: %v", logLengths)
			}
		}

		return true, fmt.Sprintf("all nodes at log length %d", expectedLength)
	}, config.ReplicationTimeout, "log replication")
}

// ============================================================================
// Stability Helpers
// ============================================================================

// WaitForStableLeader waits for a stable leader that remains leader
func WaitForStableLeader(t *testing.T, nodes []Node, config *TimingConfig) int {
	t.Helper()
	// First wait for a leader
	leaderID := WaitForLeaderWithConfig(t, nodes, config)

	// Then ensure it remains stable
	Consistently(t, func() bool {
		currentLeaderCount := 0
		currentLeaderID := -1
		for i, node := range nodes {
			if node != nil && node.IsLeader() {
				currentLeaderCount++
				currentLeaderID = i
			}
		}
		return currentLeaderCount == 1 && currentLeaderID == leaderID
	}, 500*time.Millisecond, "leader should remain stable")

	return leaderID
}

// ============================================================================
// Assertion Helpers
// ============================================================================

// Eventually asserts that a condition becomes true within the timeout
func Eventually(t *testing.T, condition func() bool, timeout time.Duration, description string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			t.Fatalf("Condition never became true: %s", description)
		case <-ticker.C:
			if condition() {
				return
			}
		}
	}
}

// Consistently asserts that a condition remains true for the duration
func Consistently(t *testing.T, condition func() bool, duration time.Duration, description string) {
	t.Helper()
	end := time.Now().Add(duration)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

	for time.Now().Before(end) {
		if !condition() {
			t.Fatalf("Condition became false: %s", description)
		}
		<-ticker.C
	}
}

// ============================================================================
// Retry Helpers
// ============================================================================

// RetryWithBackoff retries an operation with exponential backoff
func RetryWithBackoff(t *testing.T, operation func() error, maxAttempts int, initialDelay time.Duration) {
	t.Helper()
	delay := initialDelay

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		err := operation()
		if err == nil {
			return
		}

		if attempt == maxAttempts {
			t.Fatalf("Operation failed after %d attempts: %v", maxAttempts, err)
		}

		t.Logf("Attempt %d failed: %v. Retrying in %v...", attempt, err, delay)
		time.Sleep(delay)
		delay *= 2
	}
}
