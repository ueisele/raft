package client

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/ueisele/raft"
	"github.com/ueisele/raft/integration/helpers"
)

// TestExampleClientInteraction shows how clients should properly interact with Raft
func TestExampleClientInteraction(t *testing.T) {
	// Setup a 3-node cluster
	cluster := helpers.NewTestCluster(t, 3)

	// Start cluster
	if err := cluster.Start(); err != nil {
		t.Fatalf("Failed to start cluster: %v", err)
	}

	// Find the leader
	leaderID, err := cluster.WaitForLeader(2 * time.Second)
	if err != nil {
		t.Fatalf("No leader found: %v", err)
	}

	leader := cluster.Nodes[leaderID]

	// Example 1: Proper client pattern with commit waiting
	t.Run("ProperClientPattern", func(t *testing.T) {
		// Client submits command
		command := "SET x=1"
		index, term, isLeader := leader.Submit(command)
		if !isLeader {
			t.Fatalf("Failed to submit command: not leader")
		}

		t.Logf("Command submitted at index=%d, term=%d", index, term)

		// Client MUST wait for commit
		if err := cluster.WaitForCommitIndex(index, 5*time.Second); err != nil {
			t.Fatalf("Command not committed: %v", err)
		}

		t.Log("✓ Command successfully committed")
	})

	// Example 2: Handling leader changes
	t.Run("HandlingLeaderChanges", func(t *testing.T) {
		// Simulate leader change by stopping current leader
		leader.Stop()
		t.Logf("Stopped leader %d to simulate failure", leaderID)

		// Wait for new leader
		time.Sleep(500 * time.Millisecond)

		newLeaderID := -1
		for i, node := range cluster.Nodes {
			if i == leaderID {
				continue
			}
			_, isLeader := node.GetState()
			if isLeader {
				newLeaderID = i
				break
			}
		}

		if newLeaderID == -1 {
			t.Fatal("No new leader elected")
		}

		t.Logf("New leader: node %d", newLeaderID)

		// Client retries with new leader
		command := "SET y=2"
		index, _, isLeader := cluster.Nodes[newLeaderID].Submit(command)
		if !isLeader {
			t.Fatalf("Failed to submit to new leader: not leader")
		}

		// Wait for commit on remaining nodes
		activeNodes := make([]raft.Node, 0)
		for i, node := range cluster.Nodes {
			if i != leaderID {
				activeNodes = append(activeNodes, node)
			}
		}

		helpers.WaitForCommitIndex(t, activeNodes, index, 2*time.Second)
		t.Log("✓ Successfully handled leader change")
	})
}

// TestClientRetryLogic demonstrates proper client retry patterns
func TestClientRetryLogic(t *testing.T) {
	cluster := helpers.NewTestCluster(t, 5)

	if err := cluster.Start(); err != nil {
		t.Fatalf("Failed to start cluster: %v", err)
	}

	// Simulate a smart client that tracks leader
	type SmartClient struct {
		cluster      *helpers.TestCluster
		lastLeaderID int
		mu           sync.Mutex
	}

	client := &SmartClient{
		cluster:      cluster,
		lastLeaderID: -1,
	}

	// Find initial leader
	leaderID, err := cluster.WaitForLeader(2 * time.Second)
	if err != nil {
		t.Fatalf("No initial leader: %v", err)
	}
	client.lastLeaderID = leaderID

	// Submit with retry logic
	submitWithRetry := func(command string) (int, int, error) {
		maxRetries := 5
		retryDelay := 100 * time.Millisecond

		for attempt := 0; attempt < maxRetries; attempt++ {
			client.mu.Lock()
			targetNode := client.lastLeaderID
			client.mu.Unlock()

			if targetNode == -1 {
				// Find any leader
				for i, node := range cluster.Nodes {
					_, isLeader := node.GetState()
					if isLeader {
						targetNode = i
						client.mu.Lock()
						client.lastLeaderID = i
						client.mu.Unlock()
						break
					}
				}
			}

			if targetNode == -1 {
				t.Logf("Attempt %d: No leader found, retrying...", attempt)
				time.Sleep(retryDelay)
				continue
			}

			index, term, isLeader := cluster.Nodes[targetNode].Submit(command)
			if isLeader {
				return index, term, nil
			}

			t.Logf("Attempt %d: Submit failed: not leader", attempt)

			// Clear cached leader on error
			client.mu.Lock()
			client.lastLeaderID = -1
			client.mu.Unlock()

			time.Sleep(retryDelay)
			retryDelay *= 2 // Exponential backoff
		}

		return 0, 0, fmt.Errorf("failed after %d retries", maxRetries)
	}

	// Test retry logic
	t.Run("RetryOnFailure", func(t *testing.T) {
		// Submit command
		idx, term, err := submitWithRetry("retry-test-1")
		if err != nil {
			t.Fatalf("Failed to submit with retry: %v", err)
		}

		t.Logf("Command submitted: index=%d, term=%d", idx, term)

		// Verify commit
		if err := cluster.WaitForCommitIndex(idx, 2*time.Second); err != nil {
			t.Fatalf("Command not committed: %v", err)
		}

		t.Log("✓ Retry logic worked correctly")
	})

	// Test retry during leader failure
	t.Run("RetryDuringLeaderFailure", func(t *testing.T) {
		// Start submitting command
		done := make(chan struct{})
		var submitErr error
		var submitIdx int

		go func() {
			submitIdx, _, submitErr = submitWithRetry("retry-during-failure")
			close(done)
		}()

		// Cause leader failure during submit
		time.Sleep(50 * time.Millisecond)
		client.mu.Lock()
		if client.lastLeaderID != -1 {
			cluster.Nodes[client.lastLeaderID].Stop()
			t.Logf("Stopped leader %d during submit", client.lastLeaderID)
		}
		client.mu.Unlock()

		// Wait for submit to complete
		<-done

		if submitErr != nil {
			t.Fatalf("Submit failed even with retry: %v", submitErr)
		}

		t.Logf("Submit succeeded despite leader failure: index=%d", submitIdx)
		t.Log("✓ Client successfully retried during leader failure")
	})
}

// TestClientLinearizability demonstrates linearizable reads
func TestClientLinearizability(t *testing.T) {
	cluster := helpers.NewTestCluster(t, 3, helpers.WithPartitionableTransport())

	if err := cluster.Start(); err != nil {
		t.Fatalf("Failed to start cluster: %v", err)
	}

	leaderID, err := cluster.WaitForLeader(2 * time.Second)
	if err != nil {
		t.Fatalf("No leader found: %v", err)
	}

	// Write a value
	writeCmd := "SET key=value1"
	idx, _, isLeader := cluster.Nodes[leaderID].Submit(writeCmd)
	if !isLeader {
		t.Fatalf("Failed to write: not leader")
	}

	// Wait for commit
	if err := cluster.WaitForCommitIndex(idx, time.Second); err != nil {
		t.Fatalf("Write not committed: %v", err)
	}

	t.Log("Initial write committed")

	// Demonstrate linearizable read patterns
	t.Run("LinearizableRead", func(t *testing.T) {
		// Pattern 1: Read from leader (always linearizable)
		// In a real system, this would involve:
		// 1. Leader confirms it's still leader (heartbeat to majority)
		// 2. Leader reads from state machine

		commitIndex := cluster.Nodes[leaderID].GetCommitIndex()
		t.Logf("Leader read at commit index: %d", commitIndex)

		// Pattern 2: Read from follower (requires read index)
		// In a real system:
		// 1. Follower asks leader for read index
		// 2. Follower waits until its state machine reaches that index
		// 3. Follower reads from state machine

		followerID := (leaderID + 1) % 3
		followerCommit := cluster.Nodes[followerID].GetCommitIndex()

		if followerCommit < commitIndex {
			t.Logf("Follower behind: commit=%d, need=%d", followerCommit, commitIndex)

			// Wait for follower to catch up
			helpers.WaitForConditionWithProgress(t, func() (bool, string) {
				fc := cluster.Nodes[followerID].GetCommitIndex()
				return fc >= commitIndex, fmt.Sprintf("follower commit: %d", fc)
			}, 2*time.Second, "follower catch-up")
		}

		t.Log("✓ Linearizable read patterns demonstrated")
	})

	// Demonstrate stale reads
	t.Run("StaleReads", func(t *testing.T) {
		// Partition a follower
		isolatedFollower := (leaderID + 2) % 3
		if err := cluster.PartitionNode(isolatedFollower); err != nil {
			t.Fatalf("Failed to partition node: %v", err)
		}
		t.Logf("Partitioned follower %d", isolatedFollower)

		// Write new value
		writeCmd := "SET key=value2"
		idx, _, isLeader := cluster.Nodes[leaderID].Submit(writeCmd)
		if !isLeader {
			t.Fatalf("Failed to write: not leader")
		}

		// Wait for commit on connected nodes
		connectedNodes := make([]raft.Node, 0)
		for i, node := range cluster.Nodes {
			if i != isolatedFollower {
				connectedNodes = append(connectedNodes, node)
			}
		}

		helpers.WaitForCommitIndex(t, connectedNodes, idx, time.Second)

		// Isolated follower has stale data
		isolatedCommit := cluster.Nodes[isolatedFollower].GetCommitIndex()
		currentCommit := cluster.Nodes[leaderID].GetCommitIndex()

		if isolatedCommit < currentCommit {
			t.Logf("✓ Isolated follower has stale data: commit=%d vs current=%d",
				isolatedCommit, currentCommit)
		}

		// Heal partition
		cluster.HealPartition()

		// Wait for follower to catch up
		helpers.WaitForConditionWithProgress(t, func() (bool, string) {
			fc := cluster.Nodes[isolatedFollower].GetCommitIndex()
			return fc >= currentCommit, fmt.Sprintf("isolated follower commit: %d", fc)
		}, 2*time.Second, "isolated follower catch-up")

		t.Log("✓ Follower caught up after partition heal")
	})
}

// TestClientBatching demonstrates how clients can batch commands
func TestClientBatching(t *testing.T) {
	cluster := helpers.NewTestCluster(t, 3)

	if err := cluster.Start(); err != nil {
		t.Fatalf("Failed to start cluster: %v", err)
	}

	leaderID, err := cluster.WaitForLeader(2 * time.Second)
	if err != nil {
		t.Fatalf("No leader found: %v", err)
	}

	// Simulate client batching
	t.Run("CommandBatching", func(t *testing.T) {
		// Batch of commands
		batch := []string{
			"SET a=1",
			"SET b=2",
			"SET c=3",
			"SET d=4",
			"SET e=5",
		}

		// Submit batch concurrently
		type result struct {
			index   int
			success bool
		}

		results := make(chan result, len(batch))

		for _, cmd := range batch {
			go func(command string) {
				idx, _, isLeader := cluster.Nodes[leaderID].Submit(command)
				results <- result{index: idx, success: isLeader}
			}(cmd)
		}

		// Collect results
		indices := make([]int, 0, len(batch))
		for i := 0; i < len(batch); i++ {
			r := <-results
			if !r.success {
				t.Errorf("Failed to submit command: not leader")
			} else {
				indices = append(indices, r.index)
			}
		}

		// Wait for highest index to be committed
		maxIndex := 0
		for _, idx := range indices {
			if idx > maxIndex {
				maxIndex = idx
			}
		}

		if err := cluster.WaitForCommitIndex(maxIndex, 2*time.Second); err != nil {
			t.Fatalf("Batch not fully committed: %v", err)
		}

		t.Logf("✓ Batch of %d commands committed successfully", len(batch))
	})

	// Demonstrate pipelining
	t.Run("CommandPipelining", func(t *testing.T) {
		// Pipeline commands without waiting for each commit
		numCommands := 20
		indices := make([]int, numCommands)

		start := time.Now()
		for i := 0; i < numCommands; i++ {
			cmd := fmt.Sprintf("PIPELINE-%d", i)
			idx, _, isLeader := cluster.Nodes[leaderID].Submit(cmd)
			if !isLeader {
				t.Fatalf("Failed to submit pipelined command: not leader")
			}
			indices[i] = idx
		}
		submitDuration := time.Since(start)

		// Now wait for all to commit
		if err := cluster.WaitForCommitIndex(indices[numCommands-1], 2*time.Second); err != nil {
			t.Fatalf("Pipeline not committed: %v", err)
		}
		totalDuration := time.Since(start)

		t.Logf("✓ Pipelined %d commands in %v (submit: %v)",
			numCommands, totalDuration, submitDuration)
	})
}

// TestClientSessionManagement demonstrates session-based client interactions
func TestClientSessionManagement(t *testing.T) {
	cluster := helpers.NewTestCluster(t, 5)

	if err := cluster.Start(); err != nil {
		t.Fatalf("Failed to start cluster: %v", err)
	}

	// Wait for leader election
	_, err := cluster.WaitForLeader(2 * time.Second)
	if err != nil {
		t.Fatalf("No leader elected: %v", err)
	}

	// Simulate client with session ID
	type ClientSession struct {
		sessionID   string
		sequenceNum int
		mu          sync.Mutex
	}

	session := &ClientSession{
		sessionID:   "client-123",
		sequenceNum: 0,
	}

	// Submit command with session info
	submitWithSession := func(command string) (int, error) {
		session.mu.Lock()
		session.sequenceNum++
		seqNum := session.sequenceNum
		session.mu.Unlock()

		// In a real system, command would include session ID and sequence number
		fullCommand := fmt.Sprintf("%s [session=%s,seq=%d]", command, session.sessionID, seqNum)

		// Find leader and submit
		for _, node := range cluster.Nodes {
			idx, _, isLeader := node.Submit(fullCommand)
			if isLeader {
				return idx, nil
			}
		}

		return 0, fmt.Errorf("no leader found")
	}

	// Test idempotent operations
	t.Run("IdempotentOperations", func(t *testing.T) {
		// Submit command
		idx1, err := submitWithSession("CREATE_USER alice")
		if err != nil {
			t.Fatalf("Failed to submit: %v", err)
		}

		// Wait for commit
		if err := cluster.WaitForCommitIndex(idx1, time.Second); err != nil {
			t.Fatalf("Command not committed: %v", err)
		}

		// Simulate duplicate submission (network retry)
		// In a real system, this would be detected and return cached result
		idx2, err := submitWithSession("CREATE_USER alice")
		if err != nil {
			t.Fatalf("Failed to submit duplicate: %v", err)
		}

		t.Logf("Original index: %d, Duplicate index: %d", idx1, idx2)
		t.Log("✓ In a real system, duplicate would be detected via session tracking")
	})

	// Test session expiration handling
	t.Run("SessionExpiration", func(t *testing.T) {
		// Simulate long-running session
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		commandCount := 0
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop() //nolint:errcheck // background ticker cleanup

		for {
			select {
			case <-ctx.Done():
				t.Logf("✓ Session processed %d commands before expiration", commandCount)
				return
			case <-ticker.C:
				cmd := fmt.Sprintf("SESSION_CMD_%d", commandCount)
				_, err := submitWithSession(cmd)
				if err != nil {
					t.Logf("Command failed after %d successful commands: %v", commandCount, err)
					return
				}
				commandCount++
			}
		}
	})
}
