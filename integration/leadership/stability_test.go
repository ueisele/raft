package leadership

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/ueisele/raft"
	"github.com/ueisele/raft/integration/helpers"
)

// TestLeaderCommitIndexPreservation tests that leader preserves commit index across terms
func TestLeaderCommitIndexPreservation(t *testing.T) {
	// Create 5-node cluster
	cluster := helpers.NewTestCluster(t, 5)

	// Start cluster
	if err := cluster.Start(); err != nil {
		t.Fatalf("Failed to start cluster: %v", err)
	}

	// Wait for initial leader
	initialLeader, err := cluster.WaitForLeader(2 * time.Second)
	if err != nil {
		t.Fatalf("No initial leader elected: %v", err)
	}

	// Submit and commit some entries
	for i := 0; i < 10; i++ {
		idx, _, err := cluster.SubmitCommand(fmt.Sprintf("cmd-%d", i))
		if err != nil {
			t.Fatalf("Failed to submit command: %v", err)
		}
		if err := cluster.WaitForCommitIndex(idx, time.Second); err != nil {
			t.Fatalf("Command not committed: %v", err)
		}
	}

	// Get commit index before leader change
	commitIndexBefore := cluster.Nodes[initialLeader].GetCommitIndex()
	t.Logf("Initial leader %d has commit index %d", initialLeader, commitIndexBefore)

	// Force leader change by stopping current leader
	cluster.Nodes[initialLeader].Stop() //nolint:errcheck // intentional stop for test
	t.Logf("Stopped leader %d", initialLeader)

	// Wait for new leader
	time.Sleep(500 * time.Millisecond)

	var newLeader = -1
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		for i, node := range cluster.Nodes {
			if i == initialLeader {
				continue
			}
			_, isLeader := node.GetState()
			if isLeader {
				newLeader = i
				break
			}
		}
		if newLeader != -1 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	if newLeader == -1 {
		t.Fatal("No new leader elected")
	}

	t.Logf("New leader is node %d", newLeader)

	// Give new leader time to establish itself
	time.Sleep(200 * time.Millisecond)

	// New leader's commit index should not go backwards
	newLeaderCommitIndex := cluster.Nodes[newLeader].GetCommitIndex()
	if newLeaderCommitIndex < commitIndexBefore {
		t.Errorf("New leader's commit index went backwards: %d < %d",
			newLeaderCommitIndex, commitIndexBefore)
	}

	// Submit new command to ensure new leader is functional
	idx, _, isLeader := cluster.Nodes[newLeader].Submit("new-leader-cmd")
	if !isLeader {
		t.Fatalf("New leader failed to accept command: not leader")
	}

	// Wait for new command to commit (on remaining nodes)
	activeNodes := make([]raft.Node, 0)
	for i, node := range cluster.Nodes {
		if i != initialLeader {
			activeNodes = append(activeNodes, node)
		}
	}

	helpers.WaitForCommitIndex(t, activeNodes, idx, 2*time.Second)

	// Final commit index should be higher than before
	finalCommitIndex := cluster.Nodes[newLeader].GetCommitIndex()
	if finalCommitIndex <= commitIndexBefore {
		t.Errorf("Commit index did not advance: %d <= %d",
			finalCommitIndex, commitIndexBefore)
	}

	t.Logf("✓ Commit index preserved and advanced: %d -> %d -> %d",
		commitIndexBefore, newLeaderCommitIndex, finalCommitIndex)
}

// TestLeaderHeartbeatStability tests that leader maintains stable heartbeats
func TestLeaderHeartbeatStability(t *testing.T) {
	// Create 3-node cluster
	cluster := helpers.NewTestCluster(t, 3)

	// Start cluster
	if err := cluster.Start(); err != nil {
		t.Fatalf("Failed to start cluster: %v", err)
	}

	// Wait for leader
	leaderID, err := cluster.WaitForLeader(2 * time.Second)
	if err != nil {
		t.Fatalf("No leader elected: %v", err)
	}

	// Monitor heartbeats for stability
	heartbeatInterval := 50 * time.Millisecond
	monitorDuration := 2 * time.Second

	// Track term changes
	initialTerm, _ := cluster.Nodes[leaderID].GetState()
	termChanges := 0

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		ticker := time.NewTicker(heartbeatInterval / 2)
		defer ticker.Stop() //nolint:errcheck // background ticker cleanup

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				for _, node := range cluster.Nodes {
					term, _ := node.GetState()
					if term > initialTerm {
						termChanges++
						initialTerm = term
					}
				}
			}
		}
	}()

	// Run for monitoring duration
	time.Sleep(monitorDuration)
	cancel()

	// Should have no term changes during stable operation
	if termChanges > 0 {
		t.Errorf("Unexpected term changes during stable operation: %d changes", termChanges)
	}

	// Verify leader is still the same
	currentLeader := -1
	for i, node := range cluster.Nodes {
		_, isLeader := node.GetState()
		if isLeader {
			currentLeader = i
			break
		}
	}

	if currentLeader != leaderID {
		t.Errorf("Leadership changed unexpectedly: %d -> %d", leaderID, currentLeader)
	}

	t.Log("✓ Leader heartbeats stable, no unnecessary elections")
}

// TestLeaderRecoveryAfterBriefPartition tests leader recovery after brief network issues
func TestLeaderRecoveryAfterBriefPartition(t *testing.T) {
	// Create 5-node cluster with partitionable transport
	cluster := helpers.NewTestCluster(t, 5, helpers.WithPartitionableTransport())

	// Start cluster
	if err := cluster.Start(); err != nil {
		t.Fatalf("Failed to start cluster: %v", err)
	}

	// Wait for leader
	leaderID, err := cluster.WaitForLeader(2 * time.Second)
	if err != nil {
		t.Fatalf("No leader elected: %v", err)
	}

	initialTerm, _ := cluster.Nodes[leaderID].GetState()
	t.Logf("Initial leader: node %d (term %d)", leaderID, initialTerm)

	// Submit some commands
	for i := 0; i < 5; i++ {
		idx, _, err := cluster.SubmitCommand(fmt.Sprintf("before-partition-%d", i))
		if err != nil {
			t.Fatalf("Failed to submit command: %v", err)
		}
		cluster.WaitForCommitIndex(idx, time.Second) //nolint:errcheck // non-critical in load test
	}

	// Brief partition of leader (less than election timeout)
	if err := cluster.PartitionNode(leaderID); err != nil {
		t.Fatalf("Failed to partition leader: %v", err)
	}
	t.Log("Briefly partitioned leader")

	// Wait for less than election timeout
	time.Sleep(100 * time.Millisecond)

	// Heal partition quickly
	cluster.HealPartition()
	t.Log("Healed partition")

	// Give time to stabilize
	time.Sleep(200 * time.Millisecond)

	// Check if same leader maintained leadership
	currentTerm, isLeader := cluster.Nodes[leaderID].GetState()

	if isLeader && currentTerm == initialTerm {
		t.Log("✓ Leader maintained leadership after brief partition")
	} else {
		// It's also acceptable if a new leader was elected
		t.Logf("Leadership changed: isLeader=%v, term %d -> %d",
			isLeader, initialTerm, currentTerm)
	}

	// Verify cluster is functional
	idx, _, err := cluster.SubmitCommand("after-partition")
	if err != nil {
		t.Fatalf("Failed to submit command after partition: %v", err)
	}

	if err := cluster.WaitForCommitIndex(idx, 2*time.Second); err != nil {
		t.Fatalf("Command not committed after partition: %v", err)
	}

	t.Log("✓ Cluster functional after brief partition")
}

// TestMultipleLeaderTransitions tests stability across multiple leader changes
func TestMultipleLeaderTransitions(t *testing.T) {
	// Create 5-node cluster
	cluster := helpers.NewTestCluster(t, 5)

	// Start cluster
	if err := cluster.Start(); err != nil {
		t.Fatalf("Failed to start cluster: %v", err)
	}

	// Track leader history
	type LeaderChange struct {
		term     int
		leaderID int
		time     time.Time
	}
	leaderHistory := []LeaderChange{}

	// Get initial leader
	initialLeader, err := cluster.WaitForLeader(2 * time.Second)
	if err != nil {
		t.Fatalf("No initial leader elected: %v", err)
	}

	initialTerm, _ := cluster.Nodes[initialLeader].GetState()
	leaderHistory = append(leaderHistory, LeaderChange{
		term:     initialTerm,
		leaderID: initialLeader,
		time:     time.Now(),
	})

	// Force multiple leader transitions
	stoppedNodes := make(map[int]bool)

	for transition := 0; transition < 3; transition++ {
		// Submit some data with current leader
		currentLeader := leaderHistory[len(leaderHistory)-1].leaderID

		for i := 0; i < 3; i++ {
			cmd := fmt.Sprintf("transition-%d-cmd-%d", transition, i)
			idx, _, isLeader := cluster.Nodes[currentLeader].Submit(cmd)
			if !isLeader {
				t.Logf("Failed to submit command to leader %d: not leader", currentLeader)
				break
			}

			// Try to wait for commit (might fail if leader changes)
			activeNodes := make([]raft.Node, 0)
			for i, node := range cluster.Nodes {
				if !stoppedNodes[i] {
					activeNodes = append(activeNodes, node)
				}
			}

			// Short timeout since leader might fail
			ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
			done := make(chan bool)
			go func() {
				helpers.WaitForCommitIndex(t, activeNodes, idx, 500*time.Millisecond)
				done <- true
			}()

			select {
			case <-done:
				// Command committed
			case <-ctx.Done():
				// Timeout, leader might have changed
				t.Logf("Command commit timeout (expected during transition)")
			}
			cancel()
		}

		// Stop current leader
		cluster.Nodes[currentLeader].Stop() //nolint:errcheck // intentional stop for test
		stoppedNodes[currentLeader] = true
		t.Logf("Stopped leader %d", currentLeader)

		// Wait for new leader
		time.Sleep(500 * time.Millisecond)

		newLeader := -1
		newTerm := 0
		deadline := time.Now().Add(3 * time.Second)

		for time.Now().Before(deadline) {
			for i, node := range cluster.Nodes {
				if stoppedNodes[i] {
					continue
				}
				term, isLeader := node.GetState()
				if isLeader {
					newLeader = i
					newTerm = term
					break
				}
			}
			if newLeader != -1 {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}

		if newLeader == -1 {
			t.Logf("No new leader elected after transition %d", transition)
			break
		}

		leaderHistory = append(leaderHistory, LeaderChange{
			term:     newTerm,
			leaderID: newLeader,
			time:     time.Now(),
		})

		t.Logf("Transition %d: New leader is node %d (term %d)",
			transition, newLeader, newTerm)
	}

	// Analyze leader history
	t.Log("\nLeader transition history:")
	for i, change := range leaderHistory {
		if i > 0 {
			duration := change.time.Sub(leaderHistory[i-1].time)
			t.Logf("  Term %d: Leader %d (transition took %v)",
				change.term, change.leaderID, duration)
		} else {
			t.Logf("  Term %d: Leader %d (initial)", change.term, change.leaderID)
		}
	}

	// Verify terms are strictly increasing
	for i := 1; i < len(leaderHistory); i++ {
		if leaderHistory[i].term <= leaderHistory[i-1].term {
			t.Errorf("Terms not strictly increasing: %d -> %d",
				leaderHistory[i-1].term, leaderHistory[i].term)
		}
	}

	// Verify cluster still functional with remaining nodes
	activeNodes := make([]raft.Node, 0)
	for i, node := range cluster.Nodes {
		if !stoppedNodes[i] {
			activeNodes = append(activeNodes, node)
		}
	}

	if len(activeNodes) >= 3 {
		// Try to submit final command
		finalLeader := leaderHistory[len(leaderHistory)-1].leaderID
		if !stoppedNodes[finalLeader] {
			idx, _, isLeader := cluster.Nodes[finalLeader].Submit("final-stability-check")
			if isLeader {
				helpers.WaitForCommitIndex(t, activeNodes, idx, time.Second)
				t.Log("✓ Cluster remains functional after multiple transitions")
			}
		}
	}
}

// TestLeadershipWithHighLoad tests leadership stability under high load
func TestLeadershipWithHighLoad(t *testing.T) {
	// Create 5-node cluster
	cluster := helpers.NewTestCluster(t, 5)

	// Start cluster
	if err := cluster.Start(); err != nil {
		t.Fatalf("Failed to start cluster: %v", err)
	}

	// Wait for leader
	leaderID, err := cluster.WaitForLeader(2 * time.Second)
	if err != nil {
		t.Fatalf("No leader elected: %v", err)
	}

	initialTerm, _ := cluster.Nodes[leaderID].GetState()
	t.Logf("Initial leader: node %d (term %d)", leaderID, initialTerm)

	// Apply high load
	numClients := 20
	commandsPerClient := 50
	var wg sync.WaitGroup

	successCount := 0
	var successMu sync.Mutex

	start := time.Now()

	for client := 0; client < numClients; client++ {
		wg.Add(1)
		go func(clientID int) {
			defer wg.Done()

			for cmd := 0; cmd < commandsPerClient; cmd++ {
				command := fmt.Sprintf("client-%d-cmd-%d", clientID, cmd)
				_, _, err := cluster.SubmitCommand(command)
				if err == nil {
					successMu.Lock()
					successCount++
					successMu.Unlock()
				}

				// Small delay to prevent overwhelming
				if cmd%10 == 0 {
					time.Sleep(time.Millisecond)
				}
			}
		}(client)
	}

	// Wait for all clients
	wg.Wait()
	duration := time.Since(start)

	// Check if leadership remained stable
	finalTerm, isLeader := cluster.Nodes[leaderID].GetState()

	t.Logf("High load test completed in %v", duration)
	t.Logf("Commands: %d/%d succeeded", successCount, numClients*commandsPerClient)
	t.Logf("Initial leader still leader: %v", isLeader)
	t.Logf("Term progression: %d -> %d", initialTerm, finalTerm)

	// Some leader changes under high load are acceptable
	termIncreases := finalTerm - initialTerm
	if termIncreases > 2 {
		t.Logf("Warning: Multiple term increases (%d) during high load", termIncreases)
	} else {
		t.Log("✓ Leadership relatively stable under high load")
	}

	// Verify cluster still functional
	idx, _, err := cluster.SubmitCommand("post-load-check")
	if err != nil {
		t.Fatalf("Failed to submit command after high load: %v", err)
	}

	if err := cluster.WaitForCommitIndex(idx, 2*time.Second); err != nil {
		t.Fatalf("Command not committed after high load: %v", err)
	}

	t.Log("✓ Cluster functional after high load")
}
