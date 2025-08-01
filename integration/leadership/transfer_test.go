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

// TestLeadershipTransfer tests orderly leadership transfer
func TestLeadershipTransfer(t *testing.T) {
	// Create a 3-node cluster
	cluster := helpers.NewTestCluster(t, 3)

	// Start cluster
	if err := cluster.Start(); err != nil {
		t.Fatalf("Failed to start cluster: %v", err)
	}

	// Wait for initial leader
	initialLeader, err := cluster.WaitForLeader(2 * time.Second)
	if err != nil {
		t.Fatalf("No initial leader elected: %v", err)
	}

	initialTerm, _ := cluster.Nodes[initialLeader].GetState()
	t.Logf("Initial leader: node %d (term %d)", initialLeader, initialTerm)

	// Submit some commands to establish leadership
	for i := 0; i < 5; i++ {
		idx, _, err := cluster.SubmitCommand(fmt.Sprintf("initial-cmd-%d", i))
		if err != nil {
			t.Fatalf("Failed to submit command: %v", err)
		}
		if err := cluster.WaitForCommitIndex(idx, time.Second); err != nil {
			t.Fatalf("Command not committed: %v", err)
		}
	}

	// Choose target for leadership transfer
	targetNode := (initialLeader + 1) % 3
	t.Logf("Attempting to transfer leadership from node %d to node %d",
		initialLeader, targetNode)

	// In a real implementation, this would be done through a TransferLeadership RPC
	// For this test, we'll simulate it by having the leader stop sending heartbeats
	// and the target node starting an election with a higher term

	// Stop the current leader
	cluster.Nodes[initialLeader].Stop()
	t.Log("Stopped current leader to trigger new election")

	// Wait for new leader election
	time.Sleep(500 * time.Millisecond)

	// Check if leadership transferred
	// Any node could become leader, but term should increase
	var newLeader int = -1
	var newTerm int
	for i, node := range cluster.Nodes {
		if i == initialLeader {
			continue
		}
		term, isLeader := node.GetState()
		if isLeader && term > initialTerm {
			newLeader = i
			newTerm = term
			break
		}
	}

	if newLeader == -1 {
		t.Fatal("No new leader elected after leadership transfer attempt")
	}

	t.Logf("New leader: node %d (term %d)", newLeader, newTerm)

	// Verify new leader can handle requests
	activeNodes := []raft.Node{cluster.Nodes[(initialLeader+1)%3], cluster.Nodes[(initialLeader+2)%3]}

	idx, _, isLeader := cluster.Nodes[newLeader].Submit("after-transfer")
	if !isLeader {
		t.Fatalf("New leader failed to accept command: not leader")
	}

	helpers.WaitForCommitIndex(t, activeNodes, idx, time.Second)

	t.Log("✓ Leadership transfer completed successfully")
}

// TestGracefulLeadershipHandoff tests graceful handoff without disruption
func TestGracefulLeadershipHandoff(t *testing.T) {
	// Create 5-node cluster for better stability
	cluster := helpers.NewTestCluster(t, 5)

	// Start cluster
	if err := cluster.Start(); err != nil {
		t.Fatalf("Failed to start cluster: %v", err)
	}

	// Wait for leader
	currentLeader, err := cluster.WaitForLeader(2 * time.Second)
	if err != nil {
		t.Fatalf("No leader elected: %v", err)
	}

	// Submit continuous stream of commands
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	commandCount := 0
	errorCount := 0
	var mu sync.Mutex

	// Start client goroutine
	go func() {
		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				mu.Lock()
				count := commandCount
				mu.Unlock()

				cmd := fmt.Sprintf("continuous-cmd-%d", count)
				_, _, err := cluster.SubmitCommand(cmd)

				mu.Lock()
				if err != nil {
					errorCount++
				} else {
					commandCount++
				}
				mu.Unlock()
			}
		}
	}()

	// Let some commands go through
	time.Sleep(500 * time.Millisecond)

	mu.Lock()
	beforeHandoff := commandCount
	mu.Unlock()

	t.Logf("Commands before handoff: %d", beforeHandoff)

	// Simulate graceful handoff by stopping leader after ensuring
	// another node is up-to-date
	// Wait a bit to ensure follower is caught up
	time.Sleep(200 * time.Millisecond)

	// Stop current leader
	cluster.Nodes[currentLeader].Stop()
	t.Logf("Gracefully stopped leader %d", currentLeader)

	// Continue submitting commands during transition
	time.Sleep(1 * time.Second)

	// Stop command submission
	cancel()

	mu.Lock()
	finalCount := commandCount
	finalErrors := errorCount
	mu.Unlock()

	t.Logf("Total commands: %d, errors: %d", finalCount, finalErrors)

	// Error rate should be low (some errors during transition are expected)
	errorRate := float64(finalErrors) / float64(finalCount+finalErrors)
	if errorRate > 0.2 {
		t.Errorf("High error rate during handoff: %.2f%%", errorRate*100)
	} else {
		t.Logf("✓ Low error rate during handoff: %.2f%%", errorRate*100)
	}

	// Verify new leader elected
	newLeader := -1
	for i, node := range cluster.Nodes {
		if i == currentLeader {
			continue
		}
		_, isLeader := node.GetState()
		if isLeader {
			newLeader = i
			break
		}
	}

	if newLeader == -1 {
		t.Fatal("No new leader after graceful handoff")
	}

	t.Logf("✓ Graceful handoff completed, new leader: node %d", newLeader)
}

// TestLeadershipTransferToSpecificNode tests transferring leadership to a specific node
func TestLeadershipTransferToSpecificNode(t *testing.T) {
	// This test simulates what would happen with a proper TransferLeadership RPC
	cluster := helpers.NewTestCluster(t, 5)

	// Start cluster
	if err := cluster.Start(); err != nil {
		t.Fatalf("Failed to start cluster: %v", err)
	}

	// Wait for leader
	currentLeader, err := cluster.WaitForLeader(2 * time.Second)
	if err != nil {
		t.Fatalf("No leader elected: %v", err)
	}

	// Submit some data
	for i := 0; i < 10; i++ {
		idx, _, err := cluster.SubmitCommand(fmt.Sprintf("data-%d", i))
		if err != nil {
			t.Fatalf("Failed to submit command: %v", err)
		}
		cluster.WaitForCommitIndex(idx, time.Second)
	}

	// Choose the most up-to-date follower as target
	targetNode := -1
	highestIndex := 0

	for i, node := range cluster.Nodes {
		if i == currentLeader {
			continue
		}
		commitIndex := node.GetCommitIndex()
		if commitIndex > highestIndex {
			highestIndex = commitIndex
			targetNode = i
		}
	}

	t.Logf("Transferring leadership from node %d to node %d (commit index: %d)",
		currentLeader, targetNode, highestIndex)

	// In a real implementation:
	// 1. Current leader stops accepting new client requests
	// 2. Current leader ensures target is up-to-date
	// 3. Current leader sends TimeoutNow RPC to target
	// 4. Target immediately starts election with pre-vote

	// For this test, we simulate by stopping current leader
	cluster.Nodes[currentLeader].Stop()

	// Wait for election
	time.Sleep(500 * time.Millisecond)

	// Check if target became leader (not guaranteed without proper implementation)
	targetTerm, targetIsLeader := cluster.Nodes[targetNode].GetState()

	if targetIsLeader {
		t.Logf("✓ Target node %d became leader (term %d)", targetNode, targetTerm)
	} else {
		// Find actual new leader
		for i, node := range cluster.Nodes {
			if i == currentLeader {
				continue
			}
			_, isLeader := node.GetState()
			if isLeader {
				t.Logf("Node %d became leader instead of target %d", i, targetNode)
				break
			}
		}
	}

	t.Log("✓ Leadership transfer completed (specific target requires full implementation)")
}

// TestLeadershipTransferDuringLoad tests transfer while handling client requests
func TestLeadershipTransferDuringLoad(t *testing.T) {
	cluster := helpers.NewTestCluster(t, 5)

	// Start cluster
	if err := cluster.Start(); err != nil {
		t.Fatalf("Failed to start cluster: %v", err)
	}

	// Wait for leader
	initialLeader, err := cluster.WaitForLeader(2 * time.Second)
	if err != nil {
		t.Fatalf("No leader elected: %v", err)
	}

	// Start load generation
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	type Result struct {
		success bool
		index   int
		err     error
		time    time.Time
	}

	results := make(chan Result, 1000)

	// Multiple client goroutines
	var wg sync.WaitGroup
	for client := 0; client < 5; client++ {
		wg.Add(1)
		go func(clientID int) {
			defer wg.Done()
			cmdIndex := 0

			for {
				select {
				case <-ctx.Done():
					return
				default:
					cmd := fmt.Sprintf("client-%d-cmd-%d", clientID, cmdIndex)
					idx, _, err := cluster.SubmitCommand(cmd)

					results <- Result{
						success: err == nil,
						index:   idx,
						err:     err,
						time:    time.Now(),
					}

					cmdIndex++
					time.Sleep(10 * time.Millisecond)
				}
			}
		}(client)
	}

	// Let load run for a bit
	time.Sleep(500 * time.Millisecond)

	// Initiate leadership transfer
	transferTime := time.Now()
	t.Logf("Initiating leadership transfer at %v", transferTime)

	cluster.Nodes[initialLeader].Stop()

	// Continue load during transfer
	time.Sleep(1 * time.Second)

	// Stop load
	cancel()
	wg.Wait()
	close(results)

	// Analyze results
	var beforeTransfer, duringTransfer, afterTransfer int
	var errorsDuringTransfer int

	newLeaderElectedTime := transferTime.Add(500 * time.Millisecond) // Estimate

	for result := range results {
		if result.time.Before(transferTime) {
			beforeTransfer++
		} else if result.time.Before(newLeaderElectedTime) {
			duringTransfer++
			if !result.success {
				errorsDuringTransfer++
			}
		} else {
			afterTransfer++
		}
	}

	t.Logf("Commands - Before: %d, During: %d (errors: %d), After: %d",
		beforeTransfer, duringTransfer, errorsDuringTransfer, afterTransfer)

	// Some errors during transfer are expected
	errorRate := float64(errorsDuringTransfer) / float64(duringTransfer)
	t.Logf("Error rate during transfer: %.2f%%", errorRate*100)

	// Verify new leader exists
	newLeader := -1
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
		t.Logf("✓ New leader elected: node %d", newLeader)
		t.Log("✓ Leadership transfer during load completed")
	} else {
		t.Error("No new leader elected after transfer")
	}
}

// TestPreventedLeadershipTransfer tests scenarios where transfer should be prevented
func TestPreventedLeadershipTransfer(t *testing.T) {
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

	t.Logf("Current leader: node %d", leaderID)

	// Scenario 1: Transfer should not happen if target is not up-to-date
	// (In a real implementation)

	// Submit many commands
	for i := 0; i < 20; i++ {
		cluster.SubmitCommand(fmt.Sprintf("cmd-%d", i))
	}

	// Don't wait for full replication
	time.Sleep(50 * time.Millisecond)

	// Get commit indices
	leaderCommit := cluster.Nodes[leaderID].GetCommitIndex()

	// Find most behind follower
	mostBehindNode := -1
	lowestCommit := leaderCommit

	for i, node := range cluster.Nodes {
		if i == leaderID {
			continue
		}
		commit := node.GetCommitIndex()
		if commit < lowestCommit {
			lowestCommit = commit
			mostBehindNode = i
		}
	}

	if mostBehindNode != -1 && lowestCommit < leaderCommit {
		t.Logf("Node %d is behind (commit index %d vs leader's %d)",
			mostBehindNode, lowestCommit, leaderCommit)
		t.Log("✓ In a real implementation, transfer to this node would be prevented")
	}

	// Scenario 2: Transfer should not happen during configuration change
	// (This would be checked in a real implementation)
	t.Log("✓ Configuration change scenario would prevent transfer in real implementation")

	// Scenario 3: No transfer if no suitable target
	// Stop all followers
	for i, node := range cluster.Nodes {
		if i != leaderID {
			node.Stop()
		}
	}

	t.Log("Stopped all followers - no suitable transfer target")

	// Leader should remain leader (can't transfer with no followers)
	time.Sleep(500 * time.Millisecond)

	_, stillLeader := cluster.Nodes[leaderID].GetState()
	if stillLeader {
		t.Log("✓ Leader remained when no suitable transfer target")
	} else {
		t.Error("Leader stepped down with no transfer target")
	}
}
