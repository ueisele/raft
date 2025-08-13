package helpers_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/ueisele/raft"
	"github.com/ueisele/raft/integration/helpers"
	"github.com/ueisele/raft/integration/helpers/transporttest"
)

// Example_basicCluster demonstrates creating a simple cluster for testing
func Example_basicCluster() {
	// In a real test, use t *testing.T from the test function
	t := &testing.T{}

	// Create a 3-node cluster that automatically starts and cleans up
	cluster := helpers.NewTestCluster(t, []int{0, 1, 2}, helpers.WithClusterAutoStart())

	// Wait for a leader to be elected
	leaderID, _ := cluster.WaitForLeader(2 * time.Second)
	fmt.Printf("Leader elected: node %d\n", leaderID)

	// Submit a command through the leader
	index, term, _ := cluster.SubmitToLeader("my-command")
	fmt.Printf("Command submitted at index %d, term %d\n", index, term)

	// Wait for replication to all nodes
	cluster.WaitForCommitIndex(index, time.Second)
	fmt.Println("Command replicated to all nodes")

	// Cluster automatically stops when test ends
	// No manual cleanup needed!
}

// Example_singleNodeCluster demonstrates a single-node cluster (useful for unit tests)
func Example_singleNodeCluster() {
	t := &testing.T{}

	// Create a single-node cluster - it will become leader immediately
	cluster := helpers.NewTestCluster(t, []int{0}, helpers.WithClusterAutoStart())

	// Single node becomes leader quickly
	cluster.WaitForLeader(500 * time.Millisecond)

	// Submit commands - no replication needed in single-node mode
	for i := 0; i < 5; i++ {
		cluster.SubmitToLeader(fmt.Sprintf("cmd-%d", i))
	}

	fmt.Println("Single-node cluster processed commands")
}

// Example_nodeManagement demonstrates individual node control
func Example_nodeManagement() {
	t := &testing.T{}

	// Create cluster without auto-start
	cluster := helpers.NewTestCluster(t, []int{0, 1, 2})

	// Start nodes individually
	cluster.StartNode(0)
	cluster.StartNode(1)
	cluster.StartNode(2)

	// Wait for leader election
	leaderID, _ := cluster.WaitForLeader(2 * time.Second)

	// Stop a follower
	followerID := (leaderID + 1) % 3
	cluster.StopNode(followerID)
	fmt.Printf("Stopped follower node %d\n", followerID)

	// Cluster still works with 2 nodes
	cluster.SubmitToLeader("works-with-2-nodes")

	// Restart the stopped node
	cluster.RestartNode(followerID)
	fmt.Printf("Restarted node %d\n", followerID)

	// Node catches up automatically
	cluster.WaitForCommitIndex(1, time.Second)
}

// Example_customConfiguration demonstrates advanced cluster configuration
func Example_customConfiguration() {
	t := &testing.T{}

	// Create cluster with custom timeouts and persistence
	cluster := helpers.NewTestCluster(t, []int{0, 1, 2},
		// Custom election timeout
		helpers.WithElectionTimeout(200*time.Millisecond, 400*time.Millisecond),
		// Custom heartbeat interval
		helpers.WithHeartbeatInterval(100*time.Millisecond),
		// Use JSON persistence
		helpers.WithJSONPersistence("/tmp/raft-test"),
		// Custom state machine factory
		helpers.WithStateMachineFactory(func(nodeID int) (raft.StateMachine, error) {
			return &customStateMachine{id: nodeID}, nil
		}),
		// Auto-start all nodes
		helpers.WithClusterAutoStart(),
	)

	cluster.WaitForLeader(time.Second)
	fmt.Println("Custom cluster ready")
}

// Example_transportDecorators demonstrates adding network behavior decorators
func Example_transportDecorators() {
	t := &testing.T{}

	// Create cluster with network delays and failures
	cluster := helpers.NewTestCluster(t, []int{0, 1, 2},
		// Add 50ms delay to all messages
		helpers.WithTransportDecorators(
			func(nodeID int, wrapped raft.Transport) raft.Transport {
				// For simplicity, we'll use a different approach
				// The delay decorator is more complex and requires specific setup
				return wrapped // Just return wrapped for the example
			},
		),
		// Add 10% failure rate
		helpers.WithTransportDecorators(
			func(nodeID int, wrapped raft.Transport) raft.Transport {
				return transporttest.NewFailureDecorator(wrapped, 0.1)
			},
		),
		helpers.WithClusterAutoStart(),
	)

	// Even with delays and failures, cluster should work
	cluster.WaitForLeader(3 * time.Second)
	fmt.Println("Cluster working despite network issues")
}

// Example_dynamicMembership demonstrates adding/removing nodes
func Example_dynamicMembership() {
	t := &testing.T{}

	// Start with 3 nodes
	cluster := helpers.NewTestCluster(t, []int{0, 1, 2}, helpers.WithClusterAutoStart())
	cluster.WaitForLeader(time.Second)

	// Add a new node
	newNode, _ := cluster.AddNode(3, []int{0, 1, 2, 3})
	newNode.Start(context.Background())
	fmt.Println("Added node 3 to cluster")

	// Remove a node
	cluster.RemoveNode(1)
	fmt.Println("Removed node 1 from cluster")

	// Cluster adjusts automatically
	cluster.SubmitToLeader("works-with-changed-membership")
}

// Example_submitToSpecificNode demonstrates submitting to specific nodes
func Example_submitToSpecificNode() {
	t := &testing.T{}

	cluster := helpers.NewTestCluster(t, []int{0, 1, 2}, helpers.WithClusterAutoStart())
	leaderID, _ := cluster.WaitForLeader(time.Second)

	// Submit to leader directly
	index, term, err := cluster.SubmitToNode("cmd", leaderID)
	if err == nil {
		fmt.Printf("Submitted to leader: index=%d, term=%d\n", index, term)
	}

	// Try to submit to follower (will fail)
	followerID := (leaderID + 1) % 3
	_, _, err = cluster.SubmitToNode("cmd", followerID)
	if err != nil {
		fmt.Printf("Cannot submit to follower %d: %v\n", followerID, err)
	}
}

// Example_clusterInspection demonstrates inspecting cluster state
func Example_clusterInspection() {
	t := &testing.T{}

	cluster := helpers.NewTestCluster(t, []int{0, 1, 2}, helpers.WithClusterAutoStart())
	cluster.WaitForLeader(time.Second)

	// Get all nodes
	nodes := cluster.GetNodes()
	for nodeID, node := range nodes {
		term, isLeader := node.GetState()
		commitIndex := node.GetCommitIndex()
		fmt.Printf("Node %d: term=%d, leader=%v, commitIndex=%d\n",
			nodeID, term, isLeader, commitIndex)
	}

	// Get specific node
	if node, ok := cluster.GetNode(0); ok {
		fmt.Printf("Node 0 exists: %v\n", node.IsLeader())
	}

	// Get transports for network manipulation
	transports := cluster.GetTransports()
	fmt.Printf("Cluster has %d transports\n", len(transports))

	// Get persistences for debugging
	persistences := cluster.GetPersistences()
	fmt.Printf("Cluster has %d persistences\n", len(persistences))
}

// Example_testPattern demonstrates a common test pattern
func Example_testPattern() {
	t := &testing.T{}

	// Setup: Create and start cluster
	cluster := helpers.NewTestCluster(t, []int{0, 1, 2}, helpers.WithClusterAutoStart())

	// Ensure leader exists
	_, err := cluster.WaitForLeader(2 * time.Second)
	if err != nil {
		t.Fatalf("No leader elected: %v", err)
	}

	// Test: Submit commands
	var lastIndex int
	for i := 0; i < 10; i++ {
		index, _, err := cluster.SubmitToLeader(fmt.Sprintf("cmd-%d", i))
		if err != nil {
			t.Fatalf("Failed to submit: %v", err)
		}
		lastIndex = index
	}

	// Verify: Wait for replication
	if err := cluster.WaitForCommitIndex(lastIndex, 2*time.Second); err != nil {
		t.Fatalf("Commands not replicated: %v", err)
	}

	// Additional verification
	nodes := cluster.GetNodes()
	for nodeID, node := range nodes {
		if node.GetCommitIndex() < lastIndex {
			t.Errorf("Node %d behind: commitIndex=%d, expected>=%d",
				nodeID, node.GetCommitIndex(), lastIndex)
		}
	}

	fmt.Printf("Test passed: %d commands replicated\n", lastIndex)
	// Cleanup: Automatic when test ends
}

// Example_faultTolerance demonstrates testing fault tolerance
func Example_faultTolerance() {
	t := &testing.T{}

	cluster := helpers.NewTestCluster(t, []int{0, 1, 2, 3, 4}, helpers.WithClusterAutoStart())

	// Get initial leader
	initialLeader, _ := cluster.WaitForLeader(time.Second)

	// Submit some commands
	cluster.SubmitToLeader("cmd-1")
	cluster.SubmitToLeader("cmd-2")

	// Kill the leader
	cluster.StopNode(initialLeader)
	fmt.Printf("Stopped leader node %d\n", initialLeader)

	// New leader should be elected
	newLeader, _ := cluster.WaitForLeader(2 * time.Second)
	fmt.Printf("New leader elected: node %d\n", newLeader)

	// Cluster should still accept commands
	index, _, _ := cluster.SubmitToLeader("after-failure")
	cluster.WaitForCommitIndex(index, time.Second)

	// Restart old leader - it becomes follower
	cluster.RestartNode(initialLeader)
	fmt.Printf("Restarted old leader as follower\n")

	// Old leader catches up
	cluster.WaitForCommitIndex(index, time.Second)
	fmt.Println("All nodes synchronized")
}

// customStateMachine is an example custom state machine
type customStateMachine struct {
	id   int
	data map[string]string
}

func (sm *customStateMachine) Apply(entry raft.LogEntry) interface{} {
	// Custom apply logic
	return fmt.Sprintf("node-%d-applied", sm.id)
}

func (sm *customStateMachine) Snapshot() ([]byte, error) {
	// Custom snapshot logic
	return []byte(fmt.Sprintf("snapshot-%d", sm.id)), nil
}

func (sm *customStateMachine) Restore(snapshot []byte) error {
	// Custom restore logic
	return nil
}
