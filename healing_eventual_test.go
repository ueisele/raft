package raft

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	// "github.com/ueisele/raft/test" - removed to avoid import cycle
)

// TestClusterEventualHealing tests if a cluster heals without intervention
func TestClusterEventualHealing(t *testing.T) {
	// Create 3-node cluster
	nodes := make([]Node, 3)
	registry := &debugNodeRegistry{
		nodes:  make(map[int]RPCHandler),
		logger: newTestLogger(t),
	}

	for i := 0; i < 3; i++ {
		config := &Config{
			ID:                 i,
			Peers:              []int{0, 1, 2},
			ElectionTimeoutMin: 150 * time.Millisecond,
			ElectionTimeoutMax: 300 * time.Millisecond,
			HeartbeatInterval:  50 * time.Millisecond,
			Logger:             newTestLogger(t),
		}

		transport := &debugTransport{
			id:       i,
			registry: registry,
			logger:   newTestLogger(t),
		}

		stateMachine := &testStateMachine{
			mu:   sync.Mutex{},
			data: make(map[string]string),
		}

		node, err := NewNode(config, transport, nil, stateMachine)
		if err != nil {
			t.Fatalf("Failed to create node %d: %v", i, err)
		}

		nodes[i] = node
		registry.nodes[i] = node.(RPCHandler)
	}

	// Start all nodes
	ctx := context.Background()
	for i, node := range nodes {
		if err := node.Start(ctx); err != nil {
			t.Fatalf("Failed to start node %d: %v", i, err)
		}
		defer node.Stop()
	}

	// Wait for leader election
	timing := DefaultTimingConfig()
	timing.ElectionTimeout = 500 * time.Millisecond
	leaderID := WaitForLeaderWithConfig(t, nodes, timing)
	if leaderID < 0 {
		t.Fatal("No leader elected")
	}
	leader := nodes[leaderID]

	t.Logf("Initial leader is node %d", leaderID)

	// Submit 3 commands
	var indices []int
	for i := 0; i < 3; i++ {
		index, _, isLeader := leader.Submit(fmt.Sprintf("command-%d", i+1))
		if !isLeader {
			t.Fatal("Not leader when submitting")
		}
		indices = append(indices, index)
		t.Logf("Submitted command-%d at index %d", i+1, index)
		time.Sleep(10 * time.Millisecond) // Small delay between commands for ordering
	}

	// Wait for replication
	if len(indices) > 0 {
		WaitForCommitIndexWithConfig(t, nodes, indices[len(indices)-1], timing)
	}

	// Now cause leadership changes by stopping the leader
	t.Logf("Stopping leader node %d", leaderID)
	nodes[leaderID].Stop()

	// Wait for new leader
	var newLeader Node
	var newLeaderID int
	WaitForConditionWithProgress(t, func() (bool, string) {
		for i, node := range nodes {
			if i != leaderID && node.IsLeader() {
				newLeader = node
				newLeaderID = i
				return true, fmt.Sprintf("new leader is node %d", i)
			}
		}
		return false, "waiting for new leader"
	}, timing.ElectionTimeout*2, "new leader election")

	if newLeader == nil {
		t.Fatal("No new leader elected")
	}

	t.Logf("New leader is node %d", newLeaderID)

	// Submit a command with new leader to try to force progress
	finalIndex, _, isLeader := newLeader.Submit("final-command")
	if !isLeader {
		t.Fatal("Not leader when submitting")
	}
	t.Logf("Submitted final-command at index %d", finalIndex)

	// Now wait and see if all nodes converge WITHOUT any intervention
	t.Log("Waiting for cluster to heal naturally...")

	var lastLogState string
	stuckCount := 0
	lastProgressTime := time.Now()

	WaitForConditionWithProgress(t, func() (bool, string) {

		// Get state of remaining nodes
		states := make(map[int]string)
		commitIndices := make(map[int]int)
		logLengths := make(map[int]int)

		for i, node := range nodes {
			if i == leaderID {
				continue // Skip stopped node
			}
			commitIndex := node.GetCommitIndex()
			logLength := node.GetLogLength()
			states[i] = fmt.Sprintf("commit=%d,len=%d", commitIndex, logLength)
			commitIndices[i] = commitIndex
			logLengths[i] = logLength
		}

		// Check if converged
		converged := true
		var firstState string
		for _, state := range states {
			if firstState == "" {
				firstState = state
			} else if state != firstState {
				converged = false
				break
			}
		}

		// Check if all have the final command
		allHaveFinal := true
		for _, commitIdx := range commitIndices {
			if commitIdx < finalIndex {
				allHaveFinal = false
				break
			}
		}

		currentLogState := fmt.Sprintf("%v", states)
		if currentLogState == lastLogState {
			stuckCount++
		} else {
			stuckCount = 0
			lastProgressTime = time.Now()
		}
		lastLogState = currentLogState

		// Log progress periodically
		if time.Since(lastProgressTime) > 5*time.Second {
			// Check who is leader
			var currentLeaderID *int
			for i, node := range nodes {
				if i != leaderID && node.IsLeader() {
					id := i
					currentLeaderID = &id
					break
				}
			}
			if currentLeaderID != nil {
				// Leader info already logged in status
			}
			lastProgressTime = time.Now()
		}

		if converged && allHaveFinal {
			return true, fmt.Sprintf("cluster converged with all nodes having final command")
		}

		return false, fmt.Sprintf("states: %v, converged: %v, allHaveFinal: %v", states, converged, allHaveFinal)
	}, 30*time.Second, "cluster natural healing")

	t.Log("SUCCESS: Cluster converged naturally")
}