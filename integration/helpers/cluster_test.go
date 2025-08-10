package helpers_test

import (
	"testing"
	"time"

	"github.com/ueisele/raft"
	"github.com/ueisele/raft/integration/helpers"
)

// TestClusterAutoCleanup verifies that TestCluster automatically cleans up
func TestClusterAutoCleanup(t *testing.T) {
	// Create a test cluster
	cluster := helpers.NewTestCluster(t, 3, helpers.WithClusterAutoStart())
	
	// Wait for leader
	leaderID, err := cluster.WaitForLeader(2 * time.Second)
	if err != nil {
		t.Fatalf("No leader elected: %v", err)
	}
	
	t.Logf("Leader elected: node %d", leaderID)
	
	// Cluster should be automatically stopped when test ends
	// No need to call Stop() explicitly
}

// TestClusterAutoStart verifies that WithClusterAutoStart works correctly
func TestClusterAutoStart(t *testing.T) {
	// Create a cluster with auto-start
	cluster := helpers.NewTestCluster(t, 3, helpers.WithClusterAutoStart())
	
	// Nodes should already be running, so a leader should emerge
	leaderID, err := cluster.WaitForLeader(2 * time.Second)
	if err != nil {
		t.Fatalf("No leader elected in auto-started cluster: %v", err)
	}
	
	t.Logf("Leader elected: node %d", leaderID)
	
	// Verify we can submit commands
	index, term, err := cluster.SubmitCommand("test-command")
	if err != nil {
		t.Fatalf("Failed to submit command: %v", err)
	}
	
	t.Logf("Command submitted at index %d, term %d", index, term)
	
	// Wait for replication
	if err := cluster.WaitForCommitIndex(index, time.Second); err != nil {
		t.Fatalf("Command not replicated: %v", err)
	}
	
	// Cluster will be automatically stopped by t.Cleanup
}

// TestClusterManualStart verifies the default behavior without auto-start
func TestClusterManualStart(t *testing.T) {
	// Create a cluster without auto-start (default behavior)
	cluster := helpers.NewTestCluster(t, 3)
	
	// Nodes should not be running yet
	leader, leaderID := cluster.GetLeader()
	if leader != nil {
		t.Fatalf("Unexpected leader %d before start", leaderID)
	}
	
	// Now start the cluster manually
	if err := cluster.Start(); err != nil {
		t.Fatalf("Failed to start cluster: %v", err)
	}
	
	// Now a leader should emerge
	leaderID, err := cluster.WaitForLeader(2 * time.Second)
	if err != nil {
		t.Fatalf("No leader elected after manual start: %v", err)
	}
	
	t.Logf("Leader elected after manual start: node %d", leaderID)
	
	// Cluster will be automatically stopped by t.Cleanup
}

// TestClusterBasicOperations verifies basic cluster operations
func TestClusterBasicOperations(t *testing.T) {
	// Create and start a 5-node cluster
	cluster := helpers.NewTestCluster(t, 5, helpers.WithClusterAutoStart())
	
	// Wait for leader election
	leaderID, err := cluster.WaitForLeader(2 * time.Second)
	if err != nil {
		t.Fatalf("No leader elected: %v", err)
	}
	
	// Submit multiple commands
	var indices []int
	for i := 0; i < 10; i++ {
		index, _, err := cluster.SubmitCommand(i)
		if err != nil {
			t.Fatalf("Failed to submit command %d: %v", i, err)
		}
		indices = append(indices, index)
	}
	
	// Wait for all commands to be committed
	lastIndex := indices[len(indices)-1]
	if err := cluster.WaitForCommitIndex(lastIndex, 2*time.Second); err != nil {
		t.Fatalf("Commands not committed: %v", err)
	}
	
	t.Logf("All %d commands committed, leader was node %d", len(indices), leaderID)
}

// TestClusterWithPartitioning verifies partitionable transport
func TestClusterWithPartitioning(t *testing.T) {
	// Create a cluster with partitionable transport
	cluster := helpers.NewTestCluster(t, 3, 
		helpers.WithPartitionableTransport(),
		helpers.WithClusterAutoStart(),
	)
	
	// Wait for initial leader
	leaderID, err := cluster.WaitForLeader(2 * time.Second)
	if err != nil {
		t.Fatalf("No initial leader: %v", err)
	}
	t.Logf("Initial leader: node %d", leaderID)
	
	// Create a partition (isolate the leader)
	cluster.CreatePartition([]int{leaderID}, []int{(leaderID+1)%3, (leaderID+2)%3})
	
	// The isolated leader should step down
	helpers.WaitForFollower(t, []raft.Node{cluster.Nodes[leaderID]}, 2*time.Second)
	
	// Heal the partition
	cluster.HealPartition()
	
	// A leader should emerge again
	newLeaderID, err := cluster.WaitForLeader(2 * time.Second)
	if err != nil {
		t.Fatalf("No leader after healing: %v", err)
	}
	t.Logf("New leader after healing: node %d", newLeaderID)
}

// TestClusterCustomOptions verifies custom configuration options
func TestClusterCustomOptions(t *testing.T) {
	// Create custom state machines and persistence
	var stateMachines []raft.StateMachine
	var persistences []raft.Persistence
	for i := 0; i < 3; i++ {
		stateMachines = append(stateMachines, raft.NewMockStateMachine())
		persistences = append(persistences, raft.NewMockPersistence())
	}
	
	// Create cluster with custom options
	cluster := helpers.NewTestCluster(t, 3,
		helpers.WithElectionTimeout(100*time.Millisecond, 200*time.Millisecond),
		helpers.WithHeartbeatInterval(25*time.Millisecond),
		helpers.WithStateMachines(stateMachines),
		helpers.WithPersistence(persistences),
		helpers.WithMaxLogSize(100),
		helpers.WithClusterAutoStart(),
	)
	
	// Verify cluster works with custom configuration
	leaderID, err := cluster.WaitForLeader(time.Second)
	if err != nil {
		t.Fatalf("No leader with custom config: %v", err)
	}
	
	// Verify custom components were used
	for i := 0; i < 3; i++ {
		if cluster.StateMachines[i] != stateMachines[i] {
			t.Errorf("Node %d not using custom state machine", i)
		}
		if cluster.Persistences[i] != persistences[i] {
			t.Errorf("Node %d not using custom persistence", i)
		}
	}
	
	t.Logf("Cluster working with custom configuration, leader: node %d", leaderID)
}

// TestClusterNodeAccess verifies accessing individual nodes
func TestClusterNodeAccess(t *testing.T) {
	cluster := helpers.NewTestCluster(t, 3, helpers.WithClusterAutoStart())
	
	// Wait for leader
	leaderID, err := cluster.WaitForLeader(2 * time.Second)
	if err != nil {
		t.Fatalf("No leader: %v", err)
	}
	
	// Access individual nodes
	for i, node := range cluster.Nodes {
		term, isLeader := node.GetState()
		if i == leaderID {
			if !isLeader {
				t.Errorf("Node %d should be leader but isn't", i)
			}
		} else {
			if isLeader {
				t.Errorf("Node %d shouldn't be leader but is", i)
			}
		}
		t.Logf("Node %d: term=%d, isLeader=%v", i, term, isLeader)
	}
	
	// Access state machines
	for i := 0; i < len(cluster.Nodes); i++ {
		sm := cluster.GetStateMachine(i)
		if sm == nil {
			t.Errorf("Node %d state machine is nil", i)
		}
	}
	
	// Access persistence
	for i := 0; i < len(cluster.Nodes); i++ {
		p := cluster.GetPersistence(i)
		if p == nil {
			t.Errorf("Node %d persistence is nil", i)
		}
	}
}

// TestClusterSizeVariations tests clusters of different sizes
func TestClusterSizeVariations(t *testing.T) {
	testCases := []struct {
		name string
		size int
	}{
		{"single-node", 1},
		{"three-node", 3},
		{"five-node", 5},
		{"seven-node", 7},
	}
	
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cluster := helpers.NewTestCluster(t, tc.size, helpers.WithClusterAutoStart())
			
			// Wait for leader election (single node should become leader immediately)
			leaderID, err := cluster.WaitForLeader(2 * time.Second)
			if err != nil {
				t.Fatalf("No leader in %s cluster: %v", tc.name, err)
			}
			
			// Submit a command
			index, _, err := cluster.SubmitCommand("test-command")
			if err != nil {
				t.Fatalf("Failed to submit in %s: %v", tc.name, err)
			}
			
			// Wait for replication
			cluster.WaitForCommitIndex(index, time.Second)
			
			t.Logf("%s cluster working, leader: node %d", tc.name, leaderID)
		})
	}
}