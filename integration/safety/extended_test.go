package safety

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/ueisele/raft"
	"github.com/ueisele/raft/integration/helpers"
)

// TestLeaderAppendOnly verifies that a leader only appends to its log, never overwrites
func TestLeaderAppendOnly(t *testing.T) {
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

	leader := cluster.Nodes[leaderID]

	// Track log entries as leader appends
	logSnapshots := make([][]raft.LogEntry, 0)

	// Helper to snapshot current log
	snapshotLog := func() []raft.LogEntry {
		// Get log length first
		lastIndex := 0
		for i := 1; ; i++ {
			if entry := leader.GetLogEntry(i); entry != nil {
				lastIndex = i
			} else {
				break
			}
		}

		// Copy log entries
		entries := make([]raft.LogEntry, lastIndex)
		for i := 1; i <= lastIndex; i++ {
			if entry := leader.GetLogEntry(i); entry != nil {
				entries[i-1] = *entry
			}
		}
		return entries
	}

	// Submit commands and snapshot after each
	commands := []string{"cmd1", "cmd2", "cmd3", "cmd4", "cmd5"}
	for _, cmd := range commands {
		// Take snapshot before
		beforeSnapshot := snapshotLog()
		logSnapshots = append(logSnapshots, beforeSnapshot)

		// Submit command
		_, _, isLeader := leader.Submit(cmd)
		if !isLeader {
			t.Fatalf("Failed to submit command %s: not leader", cmd)
		}

		// Give time for local append
		time.Sleep(50 * time.Millisecond)
	}

	// Take final snapshot
	finalSnapshot := snapshotLog()
	logSnapshots = append(logSnapshots, finalSnapshot)

	// Verify append-only property
	for i := 1; i < len(logSnapshots); i++ {
		prev := logSnapshots[i-1]
		curr := logSnapshots[i]

		// Current log should be at least as long as previous
		if len(curr) < len(prev) {
			t.Errorf("Log shrunk from %d to %d entries", len(prev), len(curr))
		}

		// All previous entries should remain unchanged
		for j := 0; j < len(prev); j++ {
			if j >= len(curr) {
				t.Errorf("Entry at index %d disappeared", j+1)
				continue
			}

			if prev[j].Term != curr[j].Term {
				t.Errorf("Entry at index %d changed term from %d to %d",
					j+1, prev[j].Term, curr[j].Term)
			}

			prevCmd, _ := prev[j].Command.(string)
			currCmd, _ := curr[j].Command.(string)
			if prevCmd != currCmd {
				t.Errorf("Entry at index %d changed command from %s to %s",
					j+1, prevCmd, currCmd)
			}
		}
	}

	t.Log("✓ Leader append-only property verified")
}

// TestConcurrentClientRequests tests handling of concurrent client requests
func TestConcurrentClientRequests(t *testing.T) {
	// Create 5-node cluster for better stability
	cluster := helpers.NewTestCluster(t, 5)

	// Start cluster
	if err := cluster.Start(); err != nil {
		t.Fatalf("Failed to start cluster: %v", err)
	}

	// Wait for leader
	_, err := cluster.WaitForLeader(2 * time.Second)
	if err != nil {
		t.Fatalf("No leader elected: %v", err)
	}

	// Submit many concurrent requests
	numClients := 10
	numRequestsPerClient := 10
	var wg sync.WaitGroup
	results := make(chan string, numClients*numRequestsPerClient)
	failures := make(chan string, numClients*numRequestsPerClient)

	for client := 0; client < numClients; client++ {
		wg.Add(1)
		go func(clientID int) {
			defer wg.Done()
			for req := 0; req < numRequestsPerClient; req++ {
				cmd := fmt.Sprintf("client-%d-req-%d", clientID, req)
				_, _, err := cluster.SubmitCommand(cmd)
				if err != nil {
					failures <- cmd
				} else {
					results <- cmd
				}
				// Small random delay
				time.Sleep(time.Duration(clientID*10) * time.Microsecond)
			}
		}(client)
	}

	// Wait for all clients
	wg.Wait()
	close(results)
	close(failures)

	// Check results
	successCount := len(results)
	errorCount := len(failures)

	t.Logf("Concurrent requests: %d succeeded, %d failed", successCount, errorCount)

	if errorCount > numClients*numRequestsPerClient/10 {
		t.Errorf("Too many errors: %d out of %d requests failed",
			errorCount, numClients*numRequestsPerClient)
	}

	// Verify cluster is still functional
	testCmd := "final-test-command"
	idx, _, err := cluster.SubmitCommand(testCmd)
	if err != nil {
		t.Fatalf("Failed to submit test command after concurrent load: %v", err)
	}

	if err := cluster.WaitForCommitIndex(idx, 2*time.Second); err != nil {
		t.Fatalf("Test command not committed: %v", err)
	}

	t.Log("✓ Cluster handled concurrent requests successfully")
}

// TestLogReplicationUnderPartitions tests log replication during network partitions
func TestLogReplicationUnderPartitions(t *testing.T) {
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

	// Submit initial commands
	for i := 0; i < 5; i++ {
		idx, _, err := cluster.SubmitCommand(fmt.Sprintf("before-partition-%d", i))
		if err != nil {
			t.Fatalf("Failed to submit command: %v", err)
		}
		if err := cluster.WaitForCommitIndex(idx, time.Second); err != nil {
			t.Logf("Warning: Failed to wait for commit index %d: %v", idx, err)
		}
	}

	initialCommitIndex := cluster.Nodes[leaderID].GetCommitIndex()

	// Create partition: leader + 1 node in minority
	minorityNodes := []int{leaderID, (leaderID + 1) % 5}
	majorityNodes := []int{}
	for i := 0; i < 5; i++ {
		if i != leaderID && i != (leaderID+1)%5 {
			majorityNodes = append(majorityNodes, i)
		}
	}

	// Partition the network
	for _, minority := range minorityNodes {
		if err := cluster.PartitionNode(minority); err != nil {
			t.Fatalf("Failed to partition node %d: %v", minority, err)
		}
	}

	t.Logf("Created partition: minority=%v, majority=%v", minorityNodes, majorityNodes)

	// Try to submit to old leader (should fail or not commit)
	_, _, isLeader := cluster.Nodes[leaderID].Submit("during-partition-minority")
	if isLeader {
		// Command accepted but shouldn't commit without majority
		time.Sleep(500 * time.Millisecond)
		newCommitIndex := cluster.Nodes[leaderID].GetCommitIndex()
		if newCommitIndex > initialCommitIndex {
			t.Error("Minority partition committed new entries!")
		}
	}

	// Wait for new leader in majority
	time.Sleep(500 * time.Millisecond)

	var newLeaderID int = -1
	for _, nodeID := range majorityNodes {
		_, isLeader := cluster.Nodes[nodeID].GetState()
		if isLeader {
			newLeaderID = nodeID
			break
		}
	}

	if newLeaderID == -1 {
		t.Fatal("No leader elected in majority partition")
	}

	t.Logf("New leader in majority: node %d", newLeaderID)

	// Submit commands to majority partition
	majorityCommands := []string{}
	for i := 0; i < 5; i++ {
		cmd := fmt.Sprintf("majority-partition-%d", i)
		majorityCommands = append(majorityCommands, cmd)
		idx, _, isLeader := cluster.Nodes[newLeaderID].Submit(cmd)
		if !isLeader {
			t.Fatalf("Failed to submit to majority leader: not leader")
		}

		// Wait for commit in majority nodes only
		majorityNodesList := make([]raft.Node, len(majorityNodes))
		for i, id := range majorityNodes {
			majorityNodesList[i] = cluster.Nodes[id]
		}
		helpers.WaitForCommitIndex(t, majorityNodesList, idx, time.Second)
	}

	// Heal partition
	cluster.HealPartition()
	t.Log("Healed partition")

	// Wait for minority nodes to catch up
	time.Sleep(2 * time.Second)

	// Verify all nodes have the same log
	finalCommitIndex := cluster.Nodes[newLeaderID].GetCommitIndex()
	helpers.AssertLogConsistency(t, cluster.Nodes, finalCommitIndex)

	// Verify old leader's uncommitted entries were replaced
	for _, cmd := range majorityCommands {
		found := false
		for i := 1; i <= finalCommitIndex; i++ {
			entry := cluster.Nodes[leaderID].GetLogEntry(i)
			if entry != nil {
				if entryCmd, ok := entry.Command.(string); ok && entryCmd == cmd {
					found = true
					break
				}
			}
		}
		if !found {
			t.Errorf("Old leader missing majority command: %s", cmd)
		}
	}

	t.Log("✓ Log replication correct under partitions")
}

// TestRapidLeadershipChanges tests safety under rapid leadership changes
func TestRapidLeadershipChanges(t *testing.T) {
	// Create 5-node cluster
	cluster := helpers.NewTestCluster(t, 5)

	// Start cluster
	if err := cluster.Start(); err != nil {
		t.Fatalf("Failed to start cluster: %v", err)
	}

	// Wait for initial leader
	initialLeader, err := cluster.WaitForLeader(2 * time.Second)
	if err != nil {
		t.Fatalf("No leader elected: %v", err)
	}

	// Track leaders and terms
	leaderChanges := []struct {
		term     int
		leaderID int
	}{}

	currentTerm, _ := cluster.Nodes[initialLeader].GetState()
	leaderChanges = append(leaderChanges, struct {
		term     int
		leaderID int
	}{currentTerm, initialLeader})

	// Force rapid leader changes
	for i := 0; i < 3; i++ {
		// Submit a command with current leader
		cmd := fmt.Sprintf("leader-%d-cmd", len(leaderChanges)-1)
		if _, _, err := cluster.SubmitCommand(cmd); err != nil {
			t.Logf("Failed to submit command during leader change: %v", err)
		}

		// Stop current leader
		currentLeader := leaderChanges[len(leaderChanges)-1].leaderID
		cluster.Nodes[currentLeader].Stop()
		t.Logf("Stopped leader %d", currentLeader)

		// Wait for new leader
		time.Sleep(500 * time.Millisecond)

		newLeader := -1
		newTerm := 0
		for j, node := range cluster.Nodes {
			if j == currentLeader {
				continue
			}
			term, isLeader := node.GetState()
			if isLeader && term > currentTerm {
				newLeader = j
				newTerm = term
				break
			}
		}

		if newLeader == -1 {
			t.Logf("Warning: No new leader elected in round %d", i)
			break
		}

		currentTerm = newTerm
		leaderChanges = append(leaderChanges, struct {
			term     int
			leaderID int
		}{newTerm, newLeader})

		t.Logf("New leader: node %d (term %d)", newLeader, newTerm)
	}

	// Verify safety properties held throughout
	t.Logf("Total leader changes: %d", len(leaderChanges))

	// Check terms are monotonically increasing
	for i := 1; i < len(leaderChanges); i++ {
		if leaderChanges[i].term <= leaderChanges[i-1].term {
			t.Errorf("Terms not monotonically increasing: %d -> %d",
				leaderChanges[i-1].term, leaderChanges[i].term)
		}
	}

	// Verify only one leader per term
	termLeaders := make(map[int][]int)
	for _, change := range leaderChanges {
		termLeaders[change.term] = append(termLeaders[change.term], change.leaderID)
	}

	for term, leaders := range termLeaders {
		if len(leaders) > 1 {
			t.Errorf("Multiple leaders in term %d: %v", term, leaders)
		}
	}

	t.Log("✓ Safety maintained during rapid leadership changes")
}

// TestCommitIndexMonotonicity verifies commit index never decreases
func TestCommitIndexMonotonicity(t *testing.T) {
	// Create 3-node cluster
	cluster := helpers.NewTestCluster(t, 3)

	// Start cluster
	if err := cluster.Start(); err != nil {
		t.Fatalf("Failed to start cluster: %v", err)
	}

	// Wait for leader
	_, err := cluster.WaitForLeader(2 * time.Second)
	if err != nil {
		t.Fatalf("No leader elected: %v", err)
	}

	// Monitor commit indices
	commitHistory := make(map[int][]int) // nodeID -> commit index history
	var mu sync.Mutex

	// Start monitoring
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop() //nolint:errcheck // background ticker cleanup

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				mu.Lock()
				for i, node := range cluster.Nodes {
					commitIndex := node.GetCommitIndex()
					if commitHistory[i] == nil {
						commitHistory[i] = make([]int, 0)
					}

					// Only append if changed
					if len(commitHistory[i]) == 0 ||
						commitHistory[i][len(commitHistory[i])-1] != commitIndex {
						commitHistory[i] = append(commitHistory[i], commitIndex)
					}
				}
				mu.Unlock()
			}
		}
	}()

	// Submit commands over time
	for i := 0; i < 20; i++ {
		cluster.SubmitCommand(fmt.Sprintf("cmd-%d", i))
		time.Sleep(50 * time.Millisecond)
	}

	// Stop monitoring
	cancel()

	// Verify monotonicity
	mu.Lock()
	defer mu.Unlock()

	for nodeID, history := range commitHistory {
		for i := 1; i < len(history); i++ {
			if history[i] < history[i-1] {
				t.Errorf("Node %d: commit index decreased from %d to %d",
					nodeID, history[i-1], history[i])
			}
		}
		t.Logf("Node %d commit index progression: %v", nodeID, history)
	}

	t.Log("✓ Commit index monotonicity verified")
}
