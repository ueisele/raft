package safety

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/ueisele/raft/integration/helpers"
)

// TestElectionSafety verifies that at most one leader can be elected in a given term
func TestElectionSafety(t *testing.T) {
	// Create a 5-node cluster
	cluster := helpers.NewTestCluster(t, 5)

	// Start cluster
	if err := cluster.Start(); err != nil {
		t.Fatalf("Failed to start cluster: %v", err)
	}

	// Run for several terms
	testDuration := 3 * time.Second
	termLeaders := make(map[int][]int) // term -> list of leader IDs
	var mu sync.Mutex

	// Monitor leadership changes
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				for i, node := range cluster.Nodes {
					term, isLeader := node.GetState()
					if isLeader {
						mu.Lock()
						if termLeaders[term] == nil {
							termLeaders[term] = make([]int, 0)
						}
						// Check if this leader is already recorded for this term
						found := false
						for _, id := range termLeaders[term] {
							if id == i {
								found = true
								break
							}
						}
						if !found {
							termLeaders[term] = append(termLeaders[term], i)
						}
						mu.Unlock()
					}
				}
			}
		}
	}()

	// Run test
	time.Sleep(testDuration)
	close(done)

	// Verify election safety
	mu.Lock()
	defer mu.Unlock()

	for term, leaders := range termLeaders {
		if len(leaders) > 1 {
			t.Errorf("Election safety violated: term %d had multiple leaders: %v", term, leaders)
		} else if len(leaders) == 1 {
			t.Logf("Term %d: leader was node %d ✓", term, leaders[0])
		}
	}
}

// TestLogMatching verifies the Log Matching property
func TestLogMatching(t *testing.T) {
	// Create a 3-node cluster
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

	// Submit commands
	numCommands := 10
	for i := 0; i < numCommands; i++ {
		idx, _, err := cluster.SubmitCommand(fmt.Sprintf("cmd-%d", i))
		if err != nil {
			t.Fatalf("Failed to submit command %d: %v", i, err)
		}
		// Wait for replication
		if err := cluster.WaitForCommitIndex(idx, time.Second); err != nil {
			t.Fatalf("Command %d not committed: %v", i, err)
		}
	}

	// Verify log matching property
	// If two logs contain an entry with the same index and term,
	// then the logs are identical up through that index
	for i := 1; i <= numCommands; i++ {
		var referenceTerm int
		var referenceCmd string
		
		// Get reference from first node
		entry := cluster.Nodes[0].GetLogEntry(i)
		if entry != nil {
			referenceTerm = entry.Term
			referenceCmd, _ = entry.Command.(string)
		}

		// Compare with other nodes
		for j := 1; j < len(cluster.Nodes); j++ {
			entry := cluster.Nodes[j].GetLogEntry(i)
			if entry == nil {
				t.Errorf("Node %d missing entry at index %d", j, i)
				continue
			}

			if entry.Term != referenceTerm {
				t.Errorf("Log matching violated at index %d: node 0 has term %d, node %d has term %d",
					i, referenceTerm, j, entry.Term)
			}

			cmd, _ := entry.Command.(string)
			if cmd != referenceCmd {
				t.Errorf("Log matching violated at index %d: different commands", i)
			}
		}
	}

	t.Log("✓ Log matching property verified")
}

// TestLeaderCompleteness verifies that committed entries appear in all future leaders' logs
func TestLeaderCompleteness(t *testing.T) {
	// Create a 5-node cluster with partitionable transport
	cluster := helpers.NewTestCluster(t, 5, helpers.WithPartitionableTransport())

	// Start cluster
	if err := cluster.Start(); err != nil {
		t.Fatalf("Failed to start cluster: %v", err)
	}

	// Wait for initial leader
	initialLeader, err := cluster.WaitForLeader(2 * time.Second)
	if err != nil {
		t.Fatalf("No leader elected: %v", err)
	}

	// Submit and commit some entries
	committedEntries := make(map[int]string) // index -> command
	for i := 0; i < 5; i++ {
		cmd := fmt.Sprintf("committed-cmd-%d", i)
		idx, _, err := cluster.SubmitCommand(cmd)
		if err != nil {
			t.Fatalf("Failed to submit command: %v", err)
		}
		committedEntries[idx] = cmd
		
		// Wait for commit
		if err := cluster.WaitForCommitIndex(idx, time.Second); err != nil {
			t.Fatalf("Command not committed: %v", err)
		}
	}

	commitIndex := cluster.Nodes[initialLeader].GetCommitIndex()
	t.Logf("Initial leader %d committed up to index %d", initialLeader, commitIndex)

	// Force leader change by partitioning current leader
	if err := cluster.PartitionNode(initialLeader); err != nil {
		t.Fatalf("Failed to partition leader: %v", err)
	}

	// Wait for new leader among remaining nodes
	time.Sleep(500 * time.Millisecond) // Allow election timeout

	var newLeader int = -1
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

	t.Logf("New leader elected: node %d", newLeader)

	// Verify new leader has all committed entries
	for idx, expectedCmd := range committedEntries {
		entry := cluster.Nodes[newLeader].GetLogEntry(idx)
		if entry == nil {
			t.Errorf("New leader missing committed entry at index %d", idx)
			continue
		}
		
		cmd, _ := entry.Command.(string)
		if cmd != expectedCmd {
			t.Errorf("New leader has different entry at index %d: got %s, want %s", 
				idx, cmd, expectedCmd)
		}
	}

	t.Log("✓ Leader completeness property verified")

	// Heal partition
	cluster.HealPartition()

	// Submit more commands with new leader
	for i := 0; i < 3; i++ {
		cmd := fmt.Sprintf("new-leader-cmd-%d", i)
		_, _, isLeader := cluster.Nodes[newLeader].Submit(cmd)
		if !isLeader {
			t.Logf("Note: Command submission failed (expected if leader changed)")
		}
	}
}

// TestStateMachineSafety verifies that all state machines execute the same commands in the same order
func TestStateMachineSafety(t *testing.T) {
	// Create a 3-node cluster
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

	// Submit a series of commands
	commands := []string{
		"set x 1",
		"set y 2", 
		"set z 3",
		"increment x",
		"increment y",
		"delete z",
	}

	lastIndex := 0
	for _, cmd := range commands {
		idx, _, err := cluster.SubmitCommand(cmd)
		if err != nil {
			t.Fatalf("Failed to submit command '%s': %v", cmd, err)
		}
		lastIndex = idx
	}

	// Wait for all commands to be committed
	if err := cluster.WaitForCommitIndex(lastIndex, 2*time.Second); err != nil {
		t.Fatalf("Commands not committed: %v", err)
	}

	// Give a bit more time for application
	time.Sleep(100 * time.Millisecond)

	// Verify all nodes have same commit index
	commitIndices := make([]int, len(cluster.Nodes))
	for i, node := range cluster.Nodes {
		commitIndices[i] = node.GetCommitIndex()
		t.Logf("Node %d commit index: %d", i, commitIndices[i])
	}

	// All should have same commit index
	for i := 1; i < len(commitIndices); i++ {
		if commitIndices[i] != commitIndices[0] {
			t.Errorf("Commit index mismatch: node 0 has %d, node %d has %d",
				commitIndices[0], i, commitIndices[i])
		}
	}

	// Verify log entries are identical up to commit index
	helpers.AssertLogConsistency(t, cluster.Nodes, commitIndices[0])

	t.Log("✓ State machine safety verified")
}

// TestSplitVoteScenario tests that the cluster handles split votes correctly
func TestSplitVoteScenario(t *testing.T) {
	// Create a 5-node cluster (odd number prevents ties)
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

	initialTerm, _ := cluster.Nodes[initialLeader].GetState()
	t.Logf("Initial leader: node %d, term %d", initialLeader, initialTerm)

	// Stop the leader to trigger election
	cluster.Nodes[initialLeader].Stop()

	// Wait for new election (should resolve despite potential split votes)
	time.Sleep(500 * time.Millisecond)

	// Check that eventually a new leader is elected
	deadline := time.Now().Add(5 * time.Second)
	var newLeader int = -1
	var newTerm int

	for time.Now().Before(deadline) {
		for i, node := range cluster.Nodes {
			if i == initialLeader {
				continue
			}
			term, isLeader := node.GetState()
			if isLeader && term > initialTerm {
				newLeader = i
				newTerm = term
				goto found
			}
		}
		time.Sleep(100 * time.Millisecond)
	}

found:
	if newLeader == -1 {
		t.Fatal("No new leader elected after stopping initial leader")
	}

	t.Logf("New leader elected: node %d, term %d", newLeader, newTerm)
	t.Log("✓ Cluster recovered from potential split vote scenario")
}