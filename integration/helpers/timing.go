package helpers

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/ueisele/raft"
)

// TimingConfig holds timing parameters for tests
type TimingConfig struct {
	ElectionTimeout   time.Duration
	HeartbeatInterval time.Duration
	RequestTimeout    time.Duration
	WaitInterval      time.Duration
}

// DefaultTimingConfig returns default timing configuration for tests
func DefaultTimingConfig() TimingConfig {
	return TimingConfig{
		ElectionTimeout:   500 * time.Millisecond,
		HeartbeatInterval: 50 * time.Millisecond,
		RequestTimeout:    1 * time.Second,
		WaitInterval:      10 * time.Millisecond,
	}
}

// FastTimingConfig returns fast timing configuration for quick tests
func FastTimingConfig() TimingConfig {
	return TimingConfig{
		ElectionTimeout:   100 * time.Millisecond,
		HeartbeatInterval: 10 * time.Millisecond,
		RequestTimeout:    200 * time.Millisecond,
		WaitInterval:      5 * time.Millisecond,
	}
}

// WaitForCondition waits for a condition to become true
func WaitForCondition(t *testing.T, condition func() bool, timeout time.Duration, description string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	for {
		if condition() {
			return
		}

		select {
		case <-ctx.Done():
			t.Fatalf("Timeout waiting for %s after %v", description, timeout)
			return
		case <-time.After(10 * time.Millisecond):
			// Continue checking
		}
	}
}

// WaitForConditionWithProgress waits for a condition with progress updates
func WaitForConditionWithProgress(t *testing.T, condition func() (bool, string), timeout time.Duration, description string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	startTime := time.Now()
	lastLog := time.Now()
	logInterval := timeout / 10
	if logInterval > time.Second {
		logInterval = time.Second
	}

	for {
		done, progress := condition()
		if done {
			t.Logf("%s completed in %v", description, time.Since(startTime))
			return
		}

		if time.Since(lastLog) >= logInterval {
			elapsed := time.Since(startTime)
			t.Logf("Progress [%s]: %s (elapsed: %v)", description, progress, elapsed)
			lastLog = time.Now()
		}

		select {
		case <-ctx.Done():
			elapsed := time.Since(startTime)
			_, finalProgress := condition()
			t.Logf("Timeout waiting for %s after %v. Final status: %s (condition met: false)",
				description, elapsed, finalProgress)
			t.FailNow()
			return
		case <-time.After(10 * time.Millisecond):
			// Continue checking
		}
	}
}

// WaitForLeader waits for a leader to be elected
// Uses WaitForCondition since this is a binary state (has single leader vs not)
func WaitForLeader(t *testing.T, nodes []raft.Node, timeout time.Duration) int {
	t.Helper()
	var leaderID = -1
	WaitForCondition(t, func() bool {
		leaderCount := 0
		leaderID = -1
		for i, node := range nodes {
			if node.IsLeader() {
				leaderID = i
				leaderCount++
			}
		}
		return leaderCount == 1
	}, timeout, "leader election")
	return leaderID
}

// WaitForFollower waits for all nodes to become followers (no leader)
// Uses WaitForCondition since this is a binary state (leader vs follower)
func WaitForFollower(t *testing.T, nodes []raft.Node, timeout time.Duration) {
	t.Helper()
	WaitForCondition(t, func() bool {
		for _, node := range nodes {
			if node.IsLeader() {
				return false
			}
		}
		return true
	}, timeout, "all nodes to become followers")
}

// WaitForCommitIndex waits for nodes to reach a specific commit index
func WaitForCommitIndex(t *testing.T, nodes []raft.Node, targetIndex int, timeout time.Duration) {
	t.Helper()
	WaitForConditionWithProgress(t, func() (bool, string) {
		minIndex := int(^uint(0) >> 1) // MaxInt
		maxIndex := 0
		indices := make([]int, len(nodes))

		for i, node := range nodes {
			index := node.GetCommitIndex()
			indices[i] = index
			if index < minIndex {
				minIndex = index
			}
			if index > maxIndex {
				maxIndex = index
			}
		}

		allReached := minIndex >= targetIndex
		progress := fmt.Sprintf("commit indices: %v (min: %d, max: %d, target: %d)",
			indices, minIndex, maxIndex, targetIndex)

		return allReached, progress
	}, timeout, "commit index")
}

// WaitForServers waits for a specific server configuration
// Uses WaitForConditionWithProgress since server counts are measurable
func WaitForServers(t *testing.T, nodes []raft.Node, expectedServers []int, timeout time.Duration) {
	t.Helper()
	WaitForConditionWithProgress(t, func() (bool, string) {
		for i, node := range nodes {
			config := node.GetConfiguration()
			if len(config.Servers) != len(expectedServers) {
				return false, fmt.Sprintf("node %d has %d servers, expected %d",
					i, len(config.Servers), len(expectedServers))
			}

			// Check if all expected servers are present
			serverMap := make(map[int]bool)
			for _, server := range config.Servers {
				serverMap[server.ID] = true
			}

			for _, expectedID := range expectedServers {
				if !serverMap[expectedID] {
					return false, fmt.Sprintf("node %d missing server %d", i, expectedID)
				}
			}
		}
		return true, "all nodes have expected servers"
	}, timeout, "server configuration")
}

// WaitForTerm waits for nodes to reach at least a specific term
// Uses WaitForConditionWithProgress since term numbers are measurable progress
func WaitForTerm(t *testing.T, nodes []raft.Node, targetTerm int, timeout time.Duration) {
	t.Helper()
	WaitForConditionWithProgress(t, func() (bool, string) {
		minTerm := int(^uint(0) >> 1) // MaxInt
		maxTerm := 0
		terms := make([]int, len(nodes))

		for i, node := range nodes {
			term, _ := node.GetState()
			terms[i] = term
			if term < minTerm {
				minTerm = term
			}
			if term > maxTerm {
				maxTerm = term
			}
		}

		allReached := minTerm >= targetTerm
		progress := fmt.Sprintf("terms: %v (min: %d, max: %d, target: %d)",
			terms, minTerm, maxTerm, targetTerm)

		return allReached, progress
	}, timeout, fmt.Sprintf("reach term %d", targetTerm))
}

// Eventually asserts that a condition eventually becomes true
func Eventually(t *testing.T, condition func() bool, timeout time.Duration, message string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	for {
		if condition() {
			return
		}

		select {
		case <-ctx.Done():
			t.Errorf("Eventually failed: %s", message)
			return
		case <-time.After(10 * time.Millisecond):
			// Continue checking
		}
	}
}

// Consistently asserts that a condition remains true for a duration
func Consistently(t *testing.T, condition func() bool, duration time.Duration, message string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), duration)
	defer cancel()

	for {
		if !condition() {
			t.Errorf("Consistently failed: %s", message)
			return
		}

		select {
		case <-ctx.Done():
			// Successfully remained true for the entire duration
			return
		case <-time.After(10 * time.Millisecond):
			// Continue checking
		}
	}
}

// ExpectError is a helper that expects an error to occur
func ExpectError(t *testing.T, err error, context string) {
	t.Helper()
	if err == nil {
		t.Errorf("Expected error for %s, but got nil", context)
	}
}

// ExpectNoError is a helper that expects no error
func ExpectNoError(t *testing.T, err error, context string) {
	t.Helper()
	if err != nil {
		t.Errorf("Unexpected error for %s: %v", context, err)
	}
}
