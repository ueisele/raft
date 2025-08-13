package helpers

import (
	"reflect"
	"testing"

	"github.com/ueisele/raft"
)

// AssertLeaderCount verifies exactly one leader exists and returns its ID
func AssertLeaderCount(t *testing.T, nodes []raft.Node) int {
	t.Helper()
	leaderCount := 0
	leaderID := -1
	
	for _, node := range nodes {
		if node.IsLeader() {
			leaderCount++
			leaderID = node.GetID()
		}
	}

	if leaderCount != 1 {
		t.Errorf("Expected exactly 1 leader, found %d", leaderCount)
	}

	return leaderID
}

// AssertLeaderCountMap verifies exactly one leader exists and returns its ID
func AssertLeaderCountMap(t *testing.T, nodes map[int]raft.Node) int {
	t.Helper()
	leaderCount := 0
	leaderID := -1

	for nodeID, node := range nodes {
		if node.IsLeader() {
			leaderCount++
			leaderID = nodeID
		}
	}

	if leaderCount != 1 {
		t.Errorf("Expected exactly 1 leader, found %d", leaderCount)
	}

	return leaderID
}

// AssertNoLeader verifies no leader exists
func AssertNoLeader(t *testing.T, nodes []raft.Node) {
	t.Helper()
	for _, node := range nodes {
		if node.IsLeader() {
			t.Errorf("Expected no leader, but node %d is leader", node.GetID())
			return
		}
	}
}

// AssertSameTerm verifies all nodes have the same term
func AssertSameTerm(t *testing.T, nodes []raft.Node) int {
	t.Helper()
	if len(nodes) == 0 {
		return 0
	}

	expectedTerm := nodes[0].GetCurrentTerm()
	for _, node := range nodes {
		term := node.GetCurrentTerm()
		if term != expectedTerm {
			t.Errorf("Node %d has term %d, expected %d", node.GetID(), term, expectedTerm)
		}
	}

	return expectedTerm
}

// AssertCommitIndex verifies a node has reached a specific commit index
func AssertCommitIndex(t *testing.T, node raft.Node, expectedIndex int) {
	t.Helper()
	actualIndex := node.GetCommitIndex()
	if actualIndex != expectedIndex {
		t.Errorf("Expected commit index %d, got %d", expectedIndex, actualIndex)
	}
}

// AssertMinCommitIndex verifies all nodes have at least a minimum commit index
func AssertMinCommitIndex(t *testing.T, nodes []raft.Node, minIndex int) {
	t.Helper()
	for _, node := range nodes {
		index := node.GetCommitIndex()
		if index < minIndex {
			t.Errorf("Node %d has commit index %d, expected at least %d", node.GetID(), index, minIndex)
		}
	}
}

// AssertConfiguration verifies nodes have the expected configuration  
func AssertConfiguration(t *testing.T, nodes []raft.Node, expectedServers []int) {
	t.Helper()
	for _, node := range nodes {
		config := node.GetConfiguration()
		if len(config.Servers) != len(expectedServers) {
			t.Errorf("Node %d has %d servers, expected %d", node.GetID(),
				len(config.Servers), len(expectedServers))
			continue
		}

		// Check server IDs
		serverMap := make(map[int]bool)
		for _, server := range config.Servers {
			serverMap[server.ID] = true
		}

		for _, expectedID := range expectedServers {
			if !serverMap[expectedID] {
				t.Errorf("Node %d missing server %d in configuration", node.GetID(), expectedID)
			}
		}
	}
}

// AssertStateMachineContent verifies state machine content
func AssertStateMachineContent(t *testing.T, sm raft.StateMachine, key string, expectedValue interface{}) {
	t.Helper()

	// Try to cast to MockStateMachine to check content
	if mockSM, ok := sm.(*raft.MockStateMachine); ok {
		data := mockSM.GetData()
		if value, exists := data[key]; !exists {
			t.Errorf("Key %s not found in state machine", key)
		} else if !reflect.DeepEqual(value, expectedValue) {
			t.Errorf("Key %s has value %v, expected %v", key, value, expectedValue)
		}
	} else {
		t.Logf("Warning: Cannot verify state machine content (not a MockStateMachine)")
	}
}

// AssertEventuallyTrue asserts a condition becomes true within timeout
func AssertEventuallyTrue(t *testing.T, condition func() bool, message string) {
	t.Helper()
	Eventually(t, condition, DefaultTimingConfig().ElectionTimeout*2, message)
}

// AssertConsistentlyTrue asserts a condition remains true for a duration
func AssertConsistentlyTrue(t *testing.T, condition func() bool, message string) {
	t.Helper()
	Consistently(t, condition, DefaultTimingConfig().ElectionTimeout, message)
}

// AssertElectionSafety verifies at most one leader per term
func AssertElectionSafety(t *testing.T, nodes []raft.Node) {
	t.Helper()
	leadersByTerm := make(map[int]int) // term -> count of leaders

	for _, node := range nodes {
		if node.IsLeader() {
			term := node.GetCurrentTerm()
			leadersByTerm[term]++
		}
	}

	for term, count := range leadersByTerm {
		if count > 1 {
			t.Errorf("Term %d has %d leaders (violates election safety)",
				term, count)
		}
	}
}

// AssertLogConsistency verifies logs are consistent across nodes
func AssertLogConsistency(t *testing.T, nodes []raft.Node, upToIndex int) {
	t.Helper()
	if len(nodes) < 2 {
		return
	}

	// Use first node as reference
	referenceNode := nodes[0]
	referenceID := referenceNode.GetID()

	for i := 1; i <= upToIndex; i++ {
		referenceEntry := referenceNode.GetLogEntry(i)
		if referenceEntry == nil {
			t.Errorf("Reference node %d missing log entry at index %d", referenceID, i)
			continue
		}

		for _, node := range nodes[1:] {
			entry := node.GetLogEntry(i)
			if entry == nil {
				t.Errorf("Node %d missing log entry at index %d", node.GetID(), i)
				continue
			}

			if entry.Term != referenceEntry.Term {
				t.Errorf("Log inconsistency at index %d: node %d has term %d, node %d has term %d",
					i, referenceID, referenceEntry.Term, node.GetID(), entry.Term)
			}

			if !reflect.DeepEqual(entry.Command, referenceEntry.Command) {
				t.Errorf("Log inconsistency at index %d: commands differ between node %d and node %d",
					i, referenceID, node.GetID())
			}
		}
	}
}

// AssertClusterConsistency verifies that cluster nodes have consistent state
// It checks commit indices and log consistency up to the minimum commit index
func AssertClusterConsistency(t *testing.T, nodes []raft.Node) {
	t.Helper()

	// Get commit indices
	commitIndices := make([]int, 0, len(nodes))
	for _, node := range nodes {
		commitIndex := node.GetCommitIndex()
		commitIndices = append(commitIndices, commitIndex)
		t.Logf("Node %d commit index: %d", node.GetID(), commitIndex)
	}

	// Find max and min commit index
	maxCommit := 0
	minCommit := 0
	if len(commitIndices) > 0 {
		minCommit = commitIndices[0]
		maxCommit = commitIndices[0]
	}

	for _, commit := range commitIndices {
		if commit > maxCommit {
			maxCommit = commit
		}
		if commit < minCommit {
			minCommit = commit
		}
	}

	// Verify logs are consistent up to min commit index
	if minCommit > 0 {
		AssertLogConsistency(t, nodes, minCommit)
		t.Logf("✓ Logs consistent up to index %d", minCommit)
	}

	// Log the range for debugging
	if maxCommit != minCommit {
		t.Logf("Note: Commit indices range from %d to %d", minCommit, maxCommit)
	}
}
