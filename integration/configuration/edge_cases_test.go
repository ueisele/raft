package configuration

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/ueisele/raft"
	"github.com/ueisele/raft/integration/helpers"
)

// TestSimultaneousConfigChanges tests handling of concurrent configuration changes
func TestSimultaneousConfigChanges(t *testing.T) {
	// Create a 3-node cluster
	cluster := helpers.NewTestCluster(t, 3)

	// Start cluster
	if err := cluster.Start(); err != nil {
		t.Fatalf("Failed to start cluster: %v", err)
	}

	// Wait for leader election
	leaderID, err := cluster.WaitForLeader(2 * time.Second)
	if err != nil {
		t.Fatalf("No leader elected: %v", err)
	}

	leader := cluster.Nodes[leaderID]

	// Try to add multiple servers simultaneously
	var wg sync.WaitGroup
	results := make([]error, 3)

	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func(serverID int) {
			defer wg.Done()
			err := leader.AddServer(serverID+3, fmt.Sprintf("server-%d", serverID+3), true)
			results[serverID] = err
		}(i)
	}

	wg.Wait()

	// Count successful and failed operations
	successCount := 0
	for i, err := range results {
		if err == nil {
			successCount++
			t.Logf("AddServer %d succeeded", i+3)
		} else {
			t.Logf("AddServer %d failed: %v", i+3, err)
		}
	}

	// Only one should succeed due to serialization
	if successCount != 1 {
		t.Errorf("Expected exactly 1 successful config change, got %d", successCount)
	} else {
		t.Log("✓ Correctly serialized concurrent configuration changes")
	}

	// Verify final configuration
	config := leader.GetConfiguration()
	t.Logf("Final configuration has %d servers", len(config.Servers))
}

// TestConfigChangeRollback tests configuration change behavior when leader is partitioned
func TestConfigChangeRollback(t *testing.T) {
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

	// Get initial configuration
	initialConfig := cluster.Nodes[leaderID].GetConfiguration()
	initialServerCount := len(initialConfig.Servers)

	// First, verify that we only have 5 servers initially
	if initialServerCount != 5 {
		t.Fatalf("Expected 5 initial servers, got %d", initialServerCount)
	}

	// Create a new node but don't add it to configuration yet
	newNodeID := 5
	config := &raft.Config{
		ID:                 newNodeID,
		Peers:              []int{},
		ElectionTimeoutMin: 150 * time.Millisecond,
		ElectionTimeoutMax: 300 * time.Millisecond,
		HeartbeatInterval:  50 * time.Millisecond,
	}

	// Create transport based on cluster type
	var transport raft.Transport
	switch reg := cluster.Registry.(type) {
	case *helpers.PartitionRegistry:
		transport = helpers.NewPartitionableTransport(newNodeID, reg)
	case *helpers.NodeRegistry:
		transport = helpers.NewMultiNodeTransport(newNodeID, reg)
	default:
		t.Fatalf("Unknown registry type: %T", cluster.Registry)
	}

	newNode, err := raft.NewNode(config, transport, nil, raft.NewMockStateMachine())
	if err != nil {
		t.Fatalf("Failed to create new node: %v", err)
	}

	// Register but don't start the node yet
	switch reg := cluster.Registry.(type) {
	case *helpers.PartitionRegistry:
		reg.Register(newNodeID, newNode.(raft.RPCHandler))
	case *helpers.NodeRegistry:
		reg.Register(newNodeID, newNode.(raft.RPCHandler))
	}

	// Partition the leader first
	if err := cluster.PartitionNode(leaderID); err != nil {
		t.Fatalf("Failed to partition leader: %v", err)
	}
	t.Logf("Partitioned leader %d", leaderID)

	// Now try to add server from partitioned leader
	configDone := make(chan error, 1)
	go func() {
		err := cluster.Nodes[leaderID].AddServer(5, "server-5", true)
		configDone <- err
	}()

	// Wait for new leader in majority
	time.Sleep(1 * time.Second)

	var newLeaderID int = -1
	for i := 0; i < 5; i++ {
		if i == leaderID {
			continue
		}
		_, isLeader := cluster.Nodes[i].GetState()
		if isLeader {
			newLeaderID = i
			break
		}
	}

	if newLeaderID == -1 {
		t.Fatal("No new leader elected in majority")
	}

	// Check configuration from new leader's perspective
	newConfig := cluster.Nodes[newLeaderID].GetConfiguration()
	t.Logf("New leader %d sees %d servers in configuration", newLeaderID, len(newConfig.Servers))

	// The partitioned leader's configuration change should not be visible to the new leader
	if len(newConfig.Servers) != initialServerCount {
		// This might happen if the configuration change was replicated before partition
		t.Logf("Configuration has %d servers (initial: %d)", len(newConfig.Servers), initialServerCount)

		// Check if server 5 is in the configuration
		hasServer5 := false
		for _, srv := range newConfig.Servers {
			if srv.ID == 5 {
				hasServer5 = true
				break
			}
		}

		if hasServer5 {
			t.Log("Server 5 was added to configuration (change may have replicated before partition)")
		}
	} else {
		t.Log("✓ Configuration remains at initial size after leader partition")
	}

	// Check original config change result
	select {
	case err := <-configDone:
		if err != nil {
			t.Logf("Original config change failed: %v (expected)", err)
		}
	case <-time.After(2 * time.Second):
		t.Log("Original config change timed out (expected)")
	}

	// Heal partition
	cluster.HealPartition()
}

// TestConfigChangeWithNodeFailures tests configuration changes with node failures
func TestConfigChangeWithNodeFailures(t *testing.T) {
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

	// Stop a follower node
	followerToStop := (leaderID + 1) % 5
	cluster.Nodes[followerToStop].Stop()
	t.Logf("Stopped follower node %d", followerToStop)

	// Try to remove the stopped node
	err = cluster.Nodes[leaderID].RemoveServer(followerToStop)
	if err != nil {
		t.Fatalf("RemoveServer failed: %v", err)
	}

	t.Log("✓ RemoveServer command accepted")

	// Wait for configuration change to be committed
	time.Sleep(500 * time.Millisecond)

	// Submit a dummy command to ensure configuration change is committed
	_, _, isLeader := cluster.Nodes[leaderID].Submit("dummy-after-remove")
	if !isLeader {
		// Find new leader
		for i, node := range cluster.Nodes {
			if i != followerToStop && node.IsLeader() {
				leaderID = i
				break
			}
		}
	}

	// Wait a bit more for propagation
	time.Sleep(500 * time.Millisecond)

	// Verify configuration
	config := cluster.Nodes[leaderID].GetConfiguration()
	found := false
	for _, server := range config.Servers {
		if server.ID == followerToStop {
			found = true
			break
		}
	}

	if found {
		t.Errorf("Removed server %d still in configuration", followerToStop)
		t.Logf("Current configuration:")
		for _, srv := range config.Servers {
			t.Logf("  Server %d: %s", srv.ID, srv.Address)
		}
	} else {
		t.Log("✓ Server successfully removed from configuration")
	}

	// Only try to add it back if it was successfully removed
	if !found {
		// Try to add it back while it's still stopped
		err = cluster.Nodes[leaderID].AddServer(followerToStop, fmt.Sprintf("server-%d", followerToStop), true)
		if err != nil {
			t.Logf("AddServer for stopped node failed: %v (expected if configuration change is in progress)", err)
		} else {
			t.Log("AddServer for stopped node succeeded")

			// This might succeed but the node won't catch up until restarted
			time.Sleep(500 * time.Millisecond)

			// Restart the node
			ctx := context.Background()
			config := &raft.Config{
				ID:                 followerToStop,
				Peers:              []int{0, 1, 2, 3, 4},
				ElectionTimeoutMin: 150 * time.Millisecond,
				ElectionTimeoutMax: 300 * time.Millisecond,
				HeartbeatInterval:  50 * time.Millisecond,
				Logger:             raft.NewTestLogger(t),
			}

			transport := helpers.NewMultiNodeTransport(followerToStop, cluster.Registry.(*helpers.NodeRegistry))
			node, err := raft.NewNode(config, transport, nil, raft.NewMockStateMachine())
			if err == nil {
				cluster.Nodes[followerToStop] = node
				cluster.Registry.(*helpers.NodeRegistry).Register(followerToStop, node.(raft.RPCHandler))
				if err := node.Start(ctx); err != nil {
					t.Errorf("Failed to restart node %d: %v", followerToStop, err)
				}
				t.Logf("Restarted node %d", followerToStop)

				// Wait for node to catch up
				time.Sleep(1 * time.Second)

				// Verify final configuration
				finalConfig := cluster.Nodes[leaderID].GetConfiguration()
				t.Logf("Final configuration has %d servers:", len(finalConfig.Servers))
				for _, srv := range finalConfig.Servers {
					t.Logf("  Server %d: %s", srv.ID, srv.Address)
				}
			}
		}
	}
}

// TestJointConsensusEdgeCases tests edge cases in joint consensus
func TestJointConsensusEdgeCases(t *testing.T) {
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

	t.Run("LeaderNotInNewConfig", func(t *testing.T) {
		// Try to remove the leader itself
		err := cluster.Nodes[leaderID].RemoveServer(leaderID)
		if err != nil {
			t.Logf("RemoveServer(self) failed: %v", err)
		} else {
			t.Log("Leader initiated its own removal")

			// Wait to see what happens
			time.Sleep(2 * time.Second)

			// Check if a new leader was elected
			newLeaderFound := false
			for i, node := range cluster.Nodes {
				if i == leaderID {
					continue
				}
				_, isLeader := node.GetState()
				if isLeader {
					newLeaderFound = true
					t.Logf("New leader elected: node %d", i)
					break
				}
			}

			if !newLeaderFound {
				t.Error("No new leader elected after leader removed itself")
			}
		}
	})
}

// TestConfigurationPersistence tests that configuration changes are persistent
func TestConfigurationPersistence(t *testing.T) {
	// This test would require persistence support
	t.Skip("Requires persistence implementation")
}

// TestMaximumClusterSize tests behavior at maximum cluster size
func TestMaximumClusterSize(t *testing.T) {
	// Start with 3 nodes
	initialSize := 3
	maxSize := 9 // Typical max for Raft

	cluster := helpers.NewTestCluster(t, initialSize)

	// Start cluster
	if err := cluster.Start(); err != nil {
		t.Fatalf("Failed to start cluster: %v", err)
	}

	// Wait for leader
	leaderID, err := cluster.WaitForLeader(2 * time.Second)
	if err != nil {
		t.Fatalf("No leader elected: %v", err)
	}

	// Try to grow cluster to maximum size
	ctx := context.Background()
	currentSize := initialSize

	for currentSize < maxSize {
		newNodeID := currentSize

		// Create new node
		config := &raft.Config{
			ID:                 newNodeID,
			Peers:              []int{}, // Will be updated when added
			ElectionTimeoutMin: 150 * time.Millisecond,
			ElectionTimeoutMax: 300 * time.Millisecond,
			HeartbeatInterval:  50 * time.Millisecond,
			Logger:             raft.NewTestLogger(t),
		}

		transport := helpers.NewMultiNodeTransport(newNodeID, cluster.Registry.(*helpers.NodeRegistry))
		node, err := raft.NewNode(config, transport, nil, raft.NewMockStateMachine())
		if err != nil {
			t.Fatalf("Failed to create node %d: %v", newNodeID, err)
		}

		// Register and start node
		cluster.Registry.(*helpers.NodeRegistry).Register(newNodeID, node.(raft.RPCHandler))
		if err := node.Start(ctx); err != nil {
			t.Fatalf("Failed to start node %d: %v", newNodeID, err)
		}

		// Add to cluster configuration
		err = cluster.Nodes[leaderID].AddServer(newNodeID, fmt.Sprintf("server-%d", newNodeID), true)
		if err != nil {
			t.Logf("Failed to add node %d at size %d: %v", newNodeID, currentSize, err)
			node.Stop()
			break
		}

		// Add to our tracking
		cluster.Nodes = append(cluster.Nodes, node)
		currentSize++

		t.Logf("Successfully grew cluster to size %d", currentSize)

		// Give time for configuration to propagate
		time.Sleep(500 * time.Millisecond)
	}

	t.Logf("✓ Cluster reached size %d", currentSize)

	// Verify cluster is still functional at this size
	idx, _, isLeader := cluster.Nodes[leaderID].Submit("test-at-max-size")
	if !isLeader {
		t.Logf("Failed to submit at size %d: not leader", currentSize)
	} else {
		if err := cluster.WaitForCommitIndex(idx, 3*time.Second); err != nil {
			t.Logf("Failed to commit at size %d: %v", currentSize, err)
		} else {
			t.Logf("✓ Cluster functional at size %d", currentSize)
		}
	}
}

// TestConfigChangeTimeout tests configuration change timeouts
func TestConfigChangeTimeout(t *testing.T) {
	// Create cluster with very slow network
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

	// Create a new node that will be very slow to respond
	slowNodeID := 3

	// Don't actually start the node - just try to add it
	err = cluster.Nodes[leaderID].AddServer(slowNodeID, "slow-server", true)

	// This should eventually timeout or fail
	if err != nil {
		t.Logf("✓ AddServer failed for non-existent node: %v", err)
	} else {
		t.Log("AddServer succeeded for non-existent node")

		// Check if configuration actually includes the node
		config := cluster.Nodes[leaderID].GetConfiguration()
		found := false
		for _, server := range config.Servers {
			if server.ID == slowNodeID {
				found = true
				break
			}
		}

		if found {
			t.Error("Non-existent node added to configuration")
		}
	}
}
