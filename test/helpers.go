package test

import (
	"testing"
	"time"

	"github.com/ueisele/raft"
)

// WaitForLeader waits for a leader to be elected in the cluster or times out
func WaitForLeader(t testing.TB, nodes []raft.Node, timeout time.Duration) int {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		for i, node := range nodes {
			if node.IsLeader() {
				return i
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("No leader elected within timeout")
	return -1
}

// WaitForCondition waits for a condition to become true or times out
func WaitForCondition(t *testing.T, condition func() bool, timeout time.Duration, msg string) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("Condition not met within timeout: %s", msg)
}

// WaitForNoLeader waits until there is no leader in the cluster
func WaitForNoLeader(t *testing.T, nodes []raft.Node, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
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
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("Still has leader after timeout")
}

// WaitForTerm waits for a node to reach at least the given term
func WaitForTerm(t *testing.T, node raft.Node, minTerm int, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if node.GetCurrentTerm() >= minTerm {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("Node did not reach term %d within timeout", minTerm)
}

// WaitForCommitIndex waits for all nodes to reach at least the given commit index
func WaitForCommitIndex(t *testing.T, nodes []raft.Node, minIndex int, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		allReached := true
		for _, node := range nodes {
			if node.GetCommitIndex() < minIndex {
				allReached = false
				break
			}
		}
		if allReached {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("Not all nodes reached commit index %d within timeout", minIndex)
}

// WaitForLogLength waits for a node to have at least the specified log length
func WaitForLogLength(t *testing.T, node raft.Node, minLength int, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if node.GetLogLength() >= minLength {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("Log length did not reach %d within timeout", minLength)
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

// WaitForElection waits for an election to complete (new term with a leader)
func WaitForElection(t *testing.T, nodes []raft.Node, oldTerm int, timeout time.Duration) (int, int) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		for i, node := range nodes {
			term := node.GetCurrentTerm()
			if term > oldTerm && node.IsLeader() {
				return i, term
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("No new leader elected after term %d within timeout", oldTerm)
	return -1, -1
}

// WaitForStableCluster waits for the cluster to stabilize with a single leader
func WaitForStableCluster(t *testing.T, nodes []raft.Node, timeout time.Duration) int {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
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
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("Cluster did not stabilize with single leader within timeout")
	return -1
}
