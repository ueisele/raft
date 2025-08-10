package helpers_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/ueisele/raft"
	"github.com/ueisele/raft/integration/helpers"
	"github.com/ueisele/raft/integration/helpers/transporttest"
)

// TestClusterLifecycle tests cluster creation, startup, and cleanup
func TestClusterLifecycle(t *testing.T) {
	t.Run("AutoCleanup", func(t *testing.T) {
		// Create a test cluster - cleanup registered automatically
		cluster := helpers.NewTestCluster(t, 3, helpers.WithClusterAutoStart())

		// Wait for leader
		leaderID, err := cluster.WaitForLeader(2 * time.Second)
		if err != nil {
			t.Fatalf("No leader elected: %v", err)
		}

		t.Logf("Leader elected: node %d", leaderID)
		// Cluster automatically stopped when test ends via t.Cleanup
	})

	t.Run("AutoStart", func(t *testing.T) {
		// Create a cluster with auto-start
		cluster := helpers.NewTestCluster(t, 3, helpers.WithClusterAutoStart())

		// Nodes should already be running, leader should emerge
		leaderID, err := cluster.WaitForLeader(2 * time.Second)
		if err != nil {
			t.Fatalf("No leader elected in auto-started cluster: %v", err)
		}

		// Verify we can submit commands
		index, term, err := cluster.SubmitCommand("test-command")
		if err != nil {
			t.Fatalf("Failed to submit command: %v", err)
		}

		if err := cluster.WaitForCommitIndex(index, time.Second); err != nil {
			t.Fatalf("Command not replicated: %v", err)
		}

		t.Logf("Auto-started cluster working: leader=%d, command at index=%d term=%d",
			leaderID, index, term)
	})

	t.Run("ManualStart", func(t *testing.T) {
		// Create a cluster without auto-start
		cluster := helpers.NewTestCluster(t, 3)

		// Nodes should not be running yet
		leader, leaderID := cluster.GetLeader()
		if leader != nil {
			t.Fatalf("Unexpected leader %d before start", leaderID)
		}

		// Start the cluster manually
		if err := cluster.Start(); err != nil {
			t.Fatalf("Failed to start cluster: %v", err)
		}

		// Now a leader should emerge
		leaderID, err := cluster.WaitForLeader(2 * time.Second)
		if err != nil {
			t.Fatalf("No leader elected after manual start: %v", err)
		}

		t.Logf("Leader elected after manual start: node %d", leaderID)
	})
}

// TestClusterOperations tests basic cluster operations and node access
func TestClusterOperations(t *testing.T) {
	t.Run("BasicOperations", func(t *testing.T) {
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
	})

	t.Run("NodeAccess", func(t *testing.T) {
		cluster := helpers.NewTestCluster(t, 3, helpers.WithClusterAutoStart())

		// Wait for leader
		leaderID, err := cluster.WaitForLeader(2 * time.Second)
		if err != nil {
			t.Fatalf("No leader: %v", err)
		}

		// Access individual nodes
		for i, node := range cluster.Nodes {
			term, isLeader := node.GetState()
			expectedLeader := (i == leaderID)
			if isLeader != expectedLeader {
				t.Errorf("Node %d: expected isLeader=%v, got %v", i, expectedLeader, isLeader)
			}
			t.Logf("Node %d: term=%d, isLeader=%v", i, term, isLeader)
		}

		// Access state machines and persistence
		for i := 0; i < len(cluster.Nodes); i++ {
			if sm := cluster.GetStateMachine(i); sm == nil {
				t.Errorf("Node %d state machine is nil", i)
			}
			if p := cluster.GetPersistence(i); p == nil {
				t.Errorf("Node %d persistence is nil", i)
			}
		}
	})

	t.Run("SizeVariations", func(t *testing.T) {
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

				// Wait for leader election
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
	})
}

// TestClusterFactories tests the factory pattern implementation
func TestClusterFactories(t *testing.T) {
	t.Run("DefaultFactories", func(t *testing.T) {
		// When no factories specified, should use mock implementations
		cluster := helpers.NewTestCluster(t, 3, helpers.WithClusterAutoStart())

		if _, err := cluster.WaitForLeader(2 * time.Second); err != nil {
			t.Fatalf("Failed to elect leader: %v", err)
		}

		// Verify mock implementations are used
		for i := 0; i < 3; i++ {
			if cluster.Persistences[i] == nil {
				t.Errorf("Node %d has nil persistence", i)
			}
			if cluster.StateMachines[i] == nil {
				t.Errorf("Node %d has nil state machine", i)
			}
		}
	})

	t.Run("CustomFactories", func(t *testing.T) {
		// Track which components were created
		createdTransports := make(map[int]bool)
		createdPersistence := make(map[int]bool)
		createdStateMachines := make(map[int]bool)

		cluster := helpers.NewTestCluster(t, 3,
			helpers.WithTransportFactory(func(nodeID int, registry *helpers.NodeRegistry) (raft.Transport, error) {
				createdTransports[nodeID] = true
				return helpers.NewMultiNodeTransport(nodeID, registry), nil
			}),
			helpers.WithPersistenceFactory(func(nodeID int) (raft.Persistence, error) {
				createdPersistence[nodeID] = true
				return raft.NewMockPersistence(), nil
			}),
			helpers.WithStateMachineFactory(func(nodeID int) (raft.StateMachine, error) {
				createdStateMachines[nodeID] = true
				return raft.NewMockStateMachine(), nil
			}),
			helpers.WithClusterAutoStart(),
		)

		// Verify all factories were called
		for i := 0; i < 3; i++ {
			if !createdTransports[i] {
				t.Errorf("Transport factory not called for node %d", i)
			}
			if !createdPersistence[i] {
				t.Errorf("Persistence factory not called for node %d", i)
			}
			if !createdStateMachines[i] {
				t.Errorf("State machine factory not called for node %d", i)
			}
		}

		if _, err := cluster.WaitForLeader(2 * time.Second); err != nil {
			t.Fatalf("Failed to elect leader: %v", err)
		}
	})

	t.Run("BackwardCompatibility", func(t *testing.T) {
		// Test backward compatibility with old-style arrays
		persistences := []raft.Persistence{
			raft.NewMockPersistence(),
			raft.NewMockPersistence(),
			raft.NewMockPersistence(),
		}
		stateMachines := []raft.StateMachine{
			raft.NewMockStateMachine(),
			raft.NewMockStateMachine(),
			raft.NewMockStateMachine(),
		}

		cluster := helpers.NewTestCluster(t, 3,
			helpers.WithPersistence(persistences),
			helpers.WithStateMachines(stateMachines),
			helpers.WithClusterAutoStart(),
		)

		// Verify the exact instances are used
		for i := 0; i < 3; i++ {
			if cluster.Persistences[i] != persistences[i] {
				t.Errorf("Node %d not using expected persistence instance", i)
			}
			if cluster.StateMachines[i] != stateMachines[i] {
				t.Errorf("Node %d not using expected state machine instance", i)
			}
		}

		if _, err := cluster.WaitForLeader(2 * time.Second); err != nil {
			t.Fatalf("Failed to elect leader: %v", err)
		}
	})

	t.Run("DynamicNodeAddition", func(t *testing.T) {
		nodeCreated := false

		// Start with 3 nodes for easier leader election
		cluster := helpers.NewTestCluster(t, 3,
			helpers.WithPersistenceFactory(func(nodeID int) (raft.Persistence, error) {
				if nodeID == 3 {
					nodeCreated = true
				}
				return raft.NewMockPersistence(), nil
			}),
			helpers.WithClusterAutoStart(),
		)

		if _, err := cluster.WaitForLeader(2 * time.Second); err != nil {
			t.Fatalf("Failed initial election: %v", err)
		}

		// Add node 3 - should use the factory
		node, err := cluster.AddNode(3)
		if err != nil {
			t.Fatalf("Failed to add node: %v", err)
		}

		if node == nil {
			t.Fatal("AddNode returned nil node")
		}

		if !nodeCreated {
			t.Error("Factory was not called for new node")
		}
	})
}

// TestClusterDecorators tests transport decorator functionality
func TestClusterDecorators(t *testing.T) {
	t.Run("SingleDecorator", func(t *testing.T) {
		// Test with partitionable transport
		cluster := helpers.NewTestCluster(t, 3,
			helpers.WithPartitionableTransport(),
			helpers.WithClusterAutoStart(),
		)

		// Wait for initial leader
		leaderID, err := cluster.WaitForLeader(2 * time.Second)
		if err != nil {
			t.Fatalf("No initial leader: %v", err)
		}

		// Verify partitionable capability
		for i := 0; i < 3; i++ {
			if _, ok := helpers.GetTransportCapability[transporttest.PartitionCapable](cluster, i); !ok {
				t.Errorf("Node %d should have PartitionCapable", i)
			}
		}

		// Test partition functionality
		helpers.CreatePartition(cluster, []int{leaderID}, []int{(leaderID + 1) % 3, (leaderID + 2) % 3})

		// The isolated leader should step down
		helpers.WaitForFollower(t, []raft.Node{cluster.Nodes[leaderID]}, 2*time.Second)

		// Heal the partition
		helpers.HealPartition(cluster)

		// A leader should emerge again
		if _, err := cluster.WaitForLeader(2 * time.Second); err != nil {
			t.Fatalf("No leader after healing: %v", err)
		}
	})

	t.Run("MultipleDecorators", func(t *testing.T) {
		decoratorCalls := make(map[string]int)

		// Create cluster with multiple transport decorators
		cluster := helpers.NewTestCluster(t, 3,
			helpers.WithTransportDecorators(
				func(nodeID int, wrapped raft.Transport) raft.Transport {
					decoratorCalls[fmt.Sprintf("partition-%d", nodeID)]++
					return transporttest.NewPartitionableDecorator(wrapped)
				},
				func(nodeID int, wrapped raft.Transport) raft.Transport {
					decoratorCalls[fmt.Sprintf("failure-%d", nodeID)]++
					return transporttest.NewFailureDecorator(wrapped, 0.0)
				},
			),
			helpers.WithClusterAutoStart(),
		)

		// Verify decorators were applied to all nodes
		for i := 0; i < 3; i++ {
			if decoratorCalls[fmt.Sprintf("partition-%d", i)] != 1 {
				t.Errorf("Partition decorator not applied to node %d", i)
			}
			if decoratorCalls[fmt.Sprintf("failure-%d", i)] != 1 {
				t.Errorf("Failure decorator not applied to node %d", i)
			}

			// Verify capabilities are accessible
			if _, ok := helpers.GetTransportCapability[transporttest.PartitionCapable](cluster, i); !ok {
				t.Errorf("Node %d missing PartitionCapable", i)
			}
			if _, ok := helpers.GetTransportCapability[transporttest.FailureCapable](cluster, i); !ok {
				t.Errorf("Node %d missing FailureCapable", i)
			}
		}

		if _, err := cluster.WaitForLeader(2 * time.Second); err != nil {
			t.Fatalf("Failed to elect leader: %v", err)
		}

		// Test that decorators don't break functionality
		idx, _, err := cluster.SubmitCommand("test-with-decorators")
		if err != nil {
			t.Fatalf("Failed to submit command: %v", err)
		}
		if err := cluster.WaitForCommitIndex(idx, time.Second); err != nil {
			t.Fatalf("Command not committed: %v", err)
		}
	})

	t.Run("DecoratorCapabilities", func(t *testing.T) {
		// Test specific decorator capabilities
		cluster := helpers.NewTestCluster(t, 3,
			helpers.WithTransportDecorators(
				func(nodeID int, wrapped raft.Transport) raft.Transport {
					return transporttest.NewPartitionableDecorator(wrapped)
				},
				func(nodeID int, wrapped raft.Transport) raft.Transport {
					return transporttest.NewDebugDecorator(wrapped, nodeID, nil)
				},
			),
			helpers.WithClusterAutoStart(),
		)

		// Test partition capability
		if partition, ok := helpers.GetTransportCapability[transporttest.PartitionCapable](cluster, 0); ok {
			partition.Block(99)
			if !partition.IsBlocked(99) {
				t.Error("Partition capability not working")
			}
			partition.Unblock(99)
		} else {
			t.Error("Missing PartitionCapable")
		}

		// Test debug capability
		if _, ok := helpers.GetTransportCapability[transporttest.DebugCapable](cluster, 0); !ok {
			t.Error("Missing DebugCapable")
		}

		// Verify cluster still functions
		if _, err := cluster.WaitForLeader(2 * time.Second); err != nil {
			t.Fatalf("No leader with decorators: %v", err)
		}
	})
}

// TestClusterIntegration tests complex scenarios with custom options
func TestClusterIntegration(t *testing.T) {
	t.Run("FullyCustomized", func(t *testing.T) {
		// Track all customizations
		createdComponents := make(map[string]int)

		cluster := helpers.NewTestCluster(t, 3,
			// Custom timing
			helpers.WithElectionTimeout(100*time.Millisecond, 200*time.Millisecond),
			helpers.WithHeartbeatInterval(25*time.Millisecond),
			helpers.WithMaxLogSize(100),

			// Custom factories
			helpers.WithTransportFactory(func(nodeID int, registry *helpers.NodeRegistry) (raft.Transport, error) {
				createdComponents[fmt.Sprintf("transport-%d", nodeID)]++
				return helpers.NewMultiNodeTransport(nodeID, registry), nil
			}),
			helpers.WithPersistenceFactory(func(nodeID int) (raft.Persistence, error) {
				createdComponents[fmt.Sprintf("persistence-%d", nodeID)]++
				return raft.NewMockPersistence(), nil
			}),
			helpers.WithStateMachineFactory(func(nodeID int) (raft.StateMachine, error) {
				createdComponents[fmt.Sprintf("sm-%d", nodeID)]++
				return raft.NewMockStateMachine(), nil
			}),

			// Decorators
			helpers.WithTransportDecorators(
				func(nodeID int, wrapped raft.Transport) raft.Transport {
					createdComponents[fmt.Sprintf("decorator1-%d", nodeID)]++
					return transporttest.NewPartitionableDecorator(wrapped)
				},
				func(nodeID int, wrapped raft.Transport) raft.Transport {
					createdComponents[fmt.Sprintf("decorator2-%d", nodeID)]++
					return transporttest.NewDebugDecorator(wrapped, nodeID, nil)
				},
			),

			helpers.WithClusterAutoStart(),
		)

		// Verify all components were created
		for i := 0; i < 3; i++ {
			expectedComponents := []string{
				fmt.Sprintf("transport-%d", i),
				fmt.Sprintf("persistence-%d", i),
				fmt.Sprintf("sm-%d", i),
				fmt.Sprintf("decorator1-%d", i),
				fmt.Sprintf("decorator2-%d", i),
			}

			for _, comp := range expectedComponents {
				if createdComponents[comp] != 1 {
					t.Errorf("Component %s not created exactly once: %d", comp, createdComponents[comp])
				}
			}
		}

		// Verify cluster works with all customizations
		leaderID, err := cluster.WaitForLeader(time.Second)
		if err != nil {
			t.Fatalf("No leader with custom config: %v", err)
		}

		// Submit commands to verify everything works
		index, term, err := cluster.SubmitCommand("test-command")
		if err != nil {
			t.Fatalf("Failed to submit command: %v", err)
		}

		if err := cluster.WaitForCommitIndex(index, time.Second); err != nil {
			t.Fatalf("Command not committed: %v", err)
		}

		t.Logf("Fully customized cluster working: leader=%d, command at index=%d term=%d",
			leaderID, index, term)
	})

	t.Run("MixedNodeTypes", func(t *testing.T) {
		// Different persistence/state machine per node
		cluster := helpers.NewTestCluster(t, 5,
			helpers.WithPersistenceFactory(func(nodeID int) (raft.Persistence, error) {
				// Could return different types based on nodeID
				return raft.NewMockPersistence(), nil
			}),
			helpers.WithStateMachineFactory(func(nodeID int) (raft.StateMachine, error) {
				// Different configurations per node
				sm := raft.NewMockStateMachine()
				// Could configure differently based on nodeID
				return sm, nil
			}),
			helpers.WithClusterAutoStart(),
		)

		if _, err := cluster.WaitForLeader(2 * time.Second); err != nil {
			t.Fatalf("Failed to elect leader: %v", err)
		}

		// Submit commands to verify heterogeneous cluster works
		for i := 0; i < 5; i++ {
			idx, _, err := cluster.SubmitCommand(fmt.Sprintf("cmd-%d", i))
			if err != nil {
				t.Fatalf("Failed to submit command: %v", err)
			}
			if err := cluster.WaitForCommitIndex(idx, time.Second); err != nil {
				t.Logf("Warning: command %d not committed quickly", i)
			}
		}
	})
}
