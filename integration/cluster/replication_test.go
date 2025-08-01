package cluster

import (
	"fmt"
	"testing"
	"time"

	"github.com/ueisele/raft"
	"github.com/ueisele/raft/integration/helpers"
)

// TestLogReplication tests basic log replication
func TestLogReplication(t *testing.T) {
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

	// Submit multiple commands
	commands := []string{"cmd1", "cmd2", "cmd3", "cmd4", "cmd5"}
	var indices []int

	for _, cmd := range commands {
		idx, term, err := cluster.SubmitCommand(cmd)
		if err != nil {
			t.Fatalf("Failed to submit command %s: %v", cmd, err)
		}
		indices = append(indices, idx)
		t.Logf("Submitted %s at index %d, term %d", cmd, idx, term)
	}

	// Wait for all commands to be committed
	lastIndex := indices[len(indices)-1]
	if err := cluster.WaitForCommitIndex(lastIndex, 2*time.Second); err != nil {
		t.Fatalf("Commands not committed: %v", err)
	}

	// Verify all nodes have the same log
	for i, node := range cluster.Nodes {
		commitIndex := node.GetCommitIndex()
		if commitIndex < lastIndex {
			t.Errorf("Node %d commit index %d < expected %d", i, commitIndex, lastIndex)
		}

		// Verify log entries
		for j, expectedCmd := range commands {
			entry := node.GetLogEntry(indices[j])
			if entry == nil {
				t.Errorf("Node %d missing log entry at index %d", i, indices[j])
				continue
			}
			
			if cmd, ok := entry.Command.(string); !ok || cmd != expectedCmd {
				t.Errorf("Node %d has wrong command at index %d: got %v, want %s", 
					i, indices[j], entry.Command, expectedCmd)
			}
		}
	}
}

// TestReplicationWithFollowerFailure tests replication when a follower fails
func TestReplicationWithFollowerFailure(t *testing.T) {
	// Create a 5-node cluster
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

	// Partition one follower
	followerID := (leaderID + 1) % 5
	if err := cluster.PartitionNode(followerID); err != nil {
		t.Fatalf("Failed to partition node: %v", err)
	}
	t.Logf("Partitioned follower node %d", followerID)

	// Submit commands (should still succeed with 4 out of 5 nodes)
	for i := 0; i < 10; i++ {
		cmd := fmt.Sprintf("cmd-%d", i)
		idx, _, err := cluster.SubmitCommand(cmd)
		if err != nil {
			t.Fatalf("Failed to submit command: %v", err)
		}
		
		// Wait for commit on active nodes only
		activeNodes := make([]raft.Node, 0, len(cluster.Nodes)-1)
		for j, node := range cluster.Nodes {
			if j != followerID {
				activeNodes = append(activeNodes, node)
			}
		}
		
		helpers.WaitForCommitIndex(t, activeNodes, idx, time.Second)
	}

	// Get commit index before healing
	commitIndexBeforeHeal := cluster.Nodes[leaderID].GetCommitIndex()

	// Heal the partition
	cluster.HealPartition()
	t.Log("Healed partition")

	// Wait for the previously partitioned node to catch up
	helpers.WaitForConditionWithProgress(t, func() (bool, string) {
		followerCommit := cluster.Nodes[followerID].GetCommitIndex()
		return followerCommit >= commitIndexBeforeHeal, 
			fmt.Sprintf("follower commit index %d, need %d", followerCommit, commitIndexBeforeHeal)
	}, 5*time.Second, "follower catch-up")

	// Verify the follower has caught up
	followerCommit := cluster.Nodes[followerID].GetCommitIndex()
	if followerCommit < commitIndexBeforeHeal {
		t.Errorf("Follower didn't catch up: commit index %d < %d", 
			followerCommit, commitIndexBeforeHeal)
	}
}

// TestReplicationConsistency tests that logs remain consistent across failures
func TestReplicationConsistency(t *testing.T) {
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
	numCommands := 20
	for i := 0; i < numCommands; i++ {
		cmd := fmt.Sprintf("consistent-cmd-%d", i)
		idx, _, err := cluster.SubmitCommand(cmd)
		if err != nil {
			t.Fatalf("Failed to submit command %d: %v", i, err)
		}
		
		// Wait for commit
		if err := cluster.WaitForCommitIndex(idx, time.Second); err != nil {
			t.Fatalf("Command %d not committed: %v", i, err)
		}
	}

	// Verify log consistency using assertions
	commitIndex := cluster.Nodes[0].GetCommitIndex()
	helpers.AssertLogConsistency(t, cluster.Nodes, commitIndex)

	// Also verify state machine consistency
	// All nodes should have applied the same commands in the same order
	for i := 1; i <= commitIndex; i++ {
		var referenceEntry *raft.LogEntry
		
		for j, node := range cluster.Nodes {
			entry := node.GetLogEntry(i)
			if entry == nil {
				t.Errorf("Node %d missing entry at index %d", j, i)
				continue
			}
			
			if referenceEntry == nil {
				referenceEntry = entry
			} else {
				// Compare with reference
				if entry.Term != referenceEntry.Term {
					t.Errorf("Inconsistent term at index %d: node 0 has %d, node %d has %d",
						i, referenceEntry.Term, j, entry.Term)
				}
				
				refCmd, _ := referenceEntry.Command.(string)
				nodeCmd, _ := entry.Command.(string)
				if refCmd != nodeCmd {
					t.Errorf("Inconsistent command at index %d: node 0 has %s, node %d has %s",
						i, refCmd, j, nodeCmd)
				}
			}
		}
	}
}