package helpers

import (
	"reflect"
	"testing"

	"github.com/ueisele/raft"
)

// AssertLeaderCount verifies exactly one leader exists
func AssertLeaderCount(t *testing.T, nodes []raft.Node) int {
	t.Helper()
	leaderCount := 0
	leaderID := -1

	for i, node := range nodes {
		if node.IsLeader() {
			leaderCount++
			leaderID = i
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
	for i, node := range nodes {
		if node.IsLeader() {
			t.Errorf("Expected no leader, but node %d is leader", i)
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
	for i, node := range nodes {
		term := node.GetCurrentTerm()
		if term != expectedTerm {
			t.Errorf("Node %d has term %d, expected %d", i, term, expectedTerm)
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
	for i, node := range nodes {
		index := node.GetCommitIndex()
		if index < minIndex {
			t.Errorf("Node %d has commit index %d, expected at least %d", i, index, minIndex)
		}
	}
}

// AssertConfiguration verifies nodes have the expected configuration
func AssertConfiguration(t *testing.T, nodes []raft.Node, expectedServers []int) {
	t.Helper()
	for i, node := range nodes {
		config := node.GetConfiguration()
		if len(config.Servers) != len(expectedServers) {
			t.Errorf("Node %d has %d servers, expected %d",
				i, len(config.Servers), len(expectedServers))
			continue
		}

		// Check server IDs
		serverMap := make(map[int]bool)
		for _, server := range config.Servers {
			serverMap[server.ID] = true
		}

		for _, expectedID := range expectedServers {
			if !serverMap[expectedID] {
				t.Errorf("Node %d missing server %d in configuration", i, expectedID)
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
	leadersByTerm := make(map[int][]int)

	for i, node := range nodes {
		if node.IsLeader() {
			term := node.GetCurrentTerm()
			leadersByTerm[term] = append(leadersByTerm[term], i)
		}
	}

	for term, leaders := range leadersByTerm {
		if len(leaders) > 1 {
			t.Errorf("Term %d has %d leaders: %v (violates election safety)",
				term, len(leaders), leaders)
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

	for i := 1; i <= upToIndex; i++ {
		referenceEntry := referenceNode.GetLogEntry(i)
		if referenceEntry == nil {
			t.Errorf("Reference node missing log entry at index %d", i)
			continue
		}

		for j := 1; j < len(nodes); j++ {
			entry := nodes[j].GetLogEntry(i)
			if entry == nil {
				t.Errorf("Node %d missing log entry at index %d", j, i)
				continue
			}

			if entry.Term != referenceEntry.Term {
				t.Errorf("Log inconsistency at index %d: node 0 has term %d, node %d has term %d",
					i, referenceEntry.Term, j, entry.Term)
			}

			if !reflect.DeepEqual(entry.Command, referenceEntry.Command) {
				t.Errorf("Log inconsistency at index %d: commands differ between node 0 and node %d",
					i, j)
			}
		}
	}
}
