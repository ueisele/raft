package test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/ueisele/raft"
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

// SlowTimingConfig returns a slow timing configuration for loaded systems
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
// Leader Election Helpers
// ============================================================================

// WaitForLeader waits for a leader to be elected in the cluster
// Deprecated: Use WaitForLeaderWithConfig for better control
func WaitForLeader(t testing.TB, nodes []raft.Node, timeout time.Duration) int {
	config := &TimingConfig{
		PollInterval:    10 * time.Millisecond,
		ElectionTimeout: timeout,
	}
	return WaitForLeaderWithConfig(t, nodes, config)
}

// WaitForLeaderWithConfig waits for a leader with custom timing
func WaitForLeaderWithConfig(t testing.TB, nodes []raft.Node, config *TimingConfig) int {
	ctx, cancel := context.WithTimeout(context.Background(), config.ElectionTimeout)
	defer cancel()

	ticker := time.NewTicker(config.PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			t.Fatal("No leader elected within timeout")
			return -1
		case <-ticker.C:
			for i, node := range nodes {
				if node.IsLeader() {
					return i
				}
			}
		}
	}
}

// WaitForNoLeader waits until there is no leader in the cluster
func WaitForNoLeader(t *testing.T, nodes []raft.Node, timeout time.Duration) {
	config := DefaultTimingConfig()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	ticker := time.NewTicker(config.PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			t.Fatal("Still has leader after timeout")
			return
		case <-ticker.C:
			hasLeader := false
			for _, node := range nodes {
				if node.IsLeader() {
					hasLeader = true
					break
				}
			}
			if !hasLeader {
				return
			}
		}
	}
}

// WaitForStableLeader waits for a stable leader (no elections for a period)
func WaitForStableLeader(t *testing.T, nodes []raft.Node, config *TimingConfig) int {
	ctx, cancel := context.WithTimeout(context.Background(), config.StabilizationTimeout)
	defer cancel()

	ticker := time.NewTicker(config.PollInterval)
	defer ticker.Stop()

	var lastLeaderID int = -1
	var lastTerm int = -1
	var stableStart time.Time
	requiredStability := config.ElectionTimeout / 2

	for {
		select {
		case <-ctx.Done():
			t.Fatal("Cluster did not stabilize within timeout")
			return -1
		case <-ticker.C:
			leaderID := -1
			currentTerm := -1

			// Find current leader and term
			for i, node := range nodes {
				if node.IsLeader() {
					leaderID = i
					currentTerm = node.GetCurrentTerm()
					break
				}
			}

			// Check if leadership changed
			if leaderID != lastLeaderID || currentTerm != lastTerm {
				lastLeaderID = leaderID
				lastTerm = currentTerm
				stableStart = time.Now()
				if leaderID >= 0 {
					t.Logf("Leader changed to node %d in term %d", leaderID, currentTerm)
				}
			} else if leaderID >= 0 && time.Since(stableStart) >= requiredStability {
				// Leader has been stable for required duration
				return leaderID
			}
		}
	}
}

// WaitForElection waits for an election to complete (new term with a leader)
func WaitForElection(t *testing.T, nodes []raft.Node, oldTerm int, timeout time.Duration) (int, int) {
	config := DefaultTimingConfig()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	ticker := time.NewTicker(config.PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			t.Fatalf("No new leader elected after term %d within timeout", oldTerm)
			return -1, -1
		case <-ticker.C:
			for i, node := range nodes {
				term := node.GetCurrentTerm()
				if term > oldTerm && node.IsLeader() {
					return i, term
				}
			}
		}
	}
}

// WaitForStableCluster waits for the cluster to stabilize with a single leader
func WaitForStableCluster(t *testing.T, nodes []raft.Node, timeout time.Duration) int {
	config := DefaultTimingConfig()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	ticker := time.NewTicker(config.PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			t.Fatal("Cluster did not stabilize with single leader within timeout")
			return -1
		case <-ticker.C:
			leaderCount := 0
			leaderID := -1
			sameTerm := true
			firstTerm := -1

			for i, node := range nodes {
				term := node.GetCurrentTerm()
				if firstTerm == -1 {
					firstTerm = term
				} else if term != firstTerm {
					sameTerm = false
				}
				if node.IsLeader() {
					leaderCount++
					leaderID = i
				}
			}

			if leaderCount == 1 && sameTerm {
				return leaderID
			}
		}
	}
}

// GetLeaderID finds and returns the current leader's ID, or -1 if no leader
func GetLeaderID(nodes []raft.Node) int {
	for i, node := range nodes {
		if node.IsLeader() {
			return i
		}
	}
	return -1
}

// ============================================================================
// Replication and State Helpers
// ============================================================================

// WaitForCommitIndex waits for all nodes to reach at least the given commit index
// Deprecated: Use WaitForCommitIndexWithConfig for better control
func WaitForCommitIndex(t *testing.T, nodes []raft.Node, minIndex int, timeout time.Duration) {
	config := &TimingConfig{
		PollInterval:       10 * time.Millisecond,
		ReplicationTimeout: timeout,
	}
	WaitForCommitIndexWithConfig(t, nodes, minIndex, config)
}

// WaitForCommitIndexWithConfig waits for commit index with custom timing
func WaitForCommitIndexWithConfig(t *testing.T, nodes []raft.Node, minIndex int, config *TimingConfig) {
	ctx, cancel := context.WithTimeout(context.Background(), config.ReplicationTimeout)
	defer cancel()

	ticker := time.NewTicker(config.PollInterval)
	defer ticker.Stop()

	lastProgress := time.Now()
	lastMin := -1

	for {
		select {
		case <-ctx.Done():
			// Provide detailed error information
			var status []string
			for i, node := range nodes {
				status = append(status, fmt.Sprintf("node%d: commit=%d", i, node.GetCommitIndex()))
			}
			t.Fatalf("Not all nodes reached commit index %d within timeout. Status: %v", minIndex, status)
			return
		case <-ticker.C:
			allReached := true
			currentMin := minIndex

			for _, node := range nodes {
				commitIndex := node.GetCommitIndex()
				if commitIndex < minIndex {
					allReached = false
					if commitIndex < currentMin {
						currentMin = commitIndex
					}
				}
			}

			// Check for progress
			if currentMin > lastMin {
				lastMin = currentMin
				lastProgress = time.Now()
			} else if time.Since(lastProgress) > config.ReplicationTimeout/2 {
				// No progress for half the timeout, log warning
				t.Logf("WARNING: No progress in replication for %v. Min commit index: %d, target: %d",
					time.Since(lastProgress), currentMin, minIndex)
			}

			if allReached {
				return
			}
		}
	}
}

// WaitForReplication waits for a command to be replicated to a majority
func WaitForReplication(t *testing.T, nodes []raft.Node, index int, config *TimingConfig) {
	ctx, cancel := context.WithTimeout(context.Background(), config.ReplicationTimeout)
	defer cancel()

	ticker := time.NewTicker(config.PollInterval)
	defer ticker.Stop()

	majoritySize := (len(nodes) / 2) + 1

	for {
		select {
		case <-ctx.Done():
			t.Fatalf("Command at index %d not replicated to majority within timeout", index)
			return
		case <-ticker.C:
			replicatedCount := 0
			for _, node := range nodes {
				if node.GetCommitIndex() >= index {
					replicatedCount++
				}
			}

			if replicatedCount >= majoritySize {
				return
			}
		}
	}
}

// WaitForLogLength waits for a node to have at least the specified log length
func WaitForLogLength(t *testing.T, node raft.Node, minLength int, timeout time.Duration) {
	config := DefaultTimingConfig()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	ticker := time.NewTicker(config.PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			t.Fatalf("Log length did not reach %d within timeout", minLength)
			return
		case <-ticker.C:
			if node.GetLogLength() >= minLength {
				return
			}
		}
	}
}

// WaitForTerm waits for a node to reach at least the given term
func WaitForTerm(t *testing.T, node raft.Node, minTerm int, timeout time.Duration) {
	config := DefaultTimingConfig()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	ticker := time.NewTicker(config.PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			t.Fatalf("Node did not reach term %d within timeout", minTerm)
			return
		case <-ticker.C:
			if node.GetCurrentTerm() >= minTerm {
				return
			}
		}
	}
}

// ============================================================================
// Generic Condition Helpers
// ============================================================================

// WaitForCondition waits for a condition to become true or times out
// Deprecated: Use WaitForConditionWithProgress for better visibility
func WaitForCondition(t *testing.T, condition func() bool, timeout time.Duration, msg string) {
	config := DefaultTimingConfig()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	ticker := time.NewTicker(config.PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			t.Fatalf("Condition not met within timeout: %s", msg)
			return
		case <-ticker.C:
			if condition() {
				return
			}
		}
	}
}

// WaitForConditionWithProgress waits for a condition with progress tracking
func WaitForConditionWithProgress(t *testing.T, condition func() (bool, string), timeout time.Duration, msg string) {
	config := DefaultTimingConfig()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	ticker := time.NewTicker(config.PollInterval)
	defer ticker.Stop()

	lastStatus := ""
	lastChange := time.Now()

	for {
		select {
		case <-ctx.Done():
			t.Fatalf("Condition not met within timeout: %s. Last status: %s", msg, lastStatus)
			return
		case <-ticker.C:
			met, status := condition()

			// Track status changes
			if status != lastStatus {
				lastStatus = status
				lastChange = time.Now()
				t.Logf("Progress: %s", status)
			} else if time.Since(lastChange) > timeout/3 {
				// No change for 1/3 of timeout
				t.Logf("WARNING: No progress for %v. Status: %s", time.Since(lastChange), status)
			}

			if met {
				return
			}
		}
	}
}

// Eventually runs a function repeatedly until it returns true or times out
func Eventually(t *testing.T, f func() bool, timeout time.Duration, msg string) {
	config := DefaultTimingConfig()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	ticker := time.NewTicker(config.PollInterval)
	defer ticker.Stop()

	attempts := 0
	for {
		select {
		case <-ctx.Done():
			t.Fatalf("Eventually failed after %d attempts: %s", attempts, msg)
			return
		case <-ticker.C:
			attempts++
			if f() {
				if attempts > 1 {
					t.Logf("Eventually succeeded after %d attempts: %s", attempts, msg)
				}
				return
			}
		}
	}
}

// Consistently checks that a condition remains true for a duration
func Consistently(t *testing.T, f func() bool, duration time.Duration, msg string) {
	config := DefaultTimingConfig()
	end := time.Now().Add(duration)
	ticker := time.NewTicker(config.PollInterval)
	defer ticker.Stop()

	checks := 0
	for time.Now().Before(end) {
		select {
		case <-ticker.C:
			checks++
			if !f() {
				t.Fatalf("Consistently failed after %d checks: %s", checks, msg)
				return
			}
		}
	}

	t.Logf("Consistently passed with %d checks: %s", checks, msg)
}

// ============================================================================
// Retry Helpers
// ============================================================================

// RetryWithBackoff retries an operation with exponential backoff
func RetryWithBackoff(t *testing.T, operation func() error, maxRetries int, initialDelay time.Duration) error {
	delay := initialDelay
	var lastErr error

	for i := 0; i < maxRetries; i++ {
		if err := operation(); err == nil {
			if i > 0 {
				t.Logf("Operation succeeded after %d retries", i)
			}
			return nil
		} else {
			lastErr = err
			if i < maxRetries-1 {
				t.Logf("Attempt %d failed: %v, retrying in %v", i+1, err, delay)
				time.Sleep(delay)
				delay *= 2
			}
		}
	}

	return fmt.Errorf("operation failed after %d attempts: %w", maxRetries, lastErr)
}
