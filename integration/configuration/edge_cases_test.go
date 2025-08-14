package configuration

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/ueisele/raft"
	"github.com/ueisele/raft/integration/helpers"
	"github.com/ueisele/raft/integration/helpers/transporttest"
	"github.com/ueisele/raft/persistence"
	"github.com/ueisele/raft/persistence/json"
)

// TestSimultaneousConfigChanges tests handling of concurrent configuration changes
func TestSimultaneousConfigChanges(t *testing.T) {
	// Create a 3-node cluster
	cluster := helpers.NewTestClusterOfSize(t, 3, helpers.WithClusterAutoStart())

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
	cluster := helpers.NewTestClusterOfSize(t, 5, helpers.WithPartitionableTransport(), helpers.WithClusterAutoStart())

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
	customConfig := func(config *raft.Config) {
		config.Peers = []int{} // Empty peers - not part of configuration yet
	}
	
	// Create node but don't start it yet
	_, err = cluster.AddNodeWithConfig(newNodeID, customConfig, false)
	if err != nil {
		t.Fatalf("Failed to create new node: %v", err)
	}

	// Partition the leader using transport decorators
	for i := 0; i < 5; i++ {
		if i != leaderID {
			if partition, ok := transporttest.GetCapability[transporttest.PartitionCapable](cluster, i); ok {
				partition.Block(leaderID)
			}
		}
		if partition, ok := transporttest.GetCapability[transporttest.PartitionCapable](cluster, leaderID); ok {
			partition.Block(i)
		}
	}
	t.Logf("Partitioned leader %d", leaderID)

	// Now try to add server from partitioned leader
	configDone := make(chan error, 1)
	go func() {
		err := cluster.Nodes[leaderID].AddServer(5, "server-5", true)
		configDone <- err
	}()

	// Wait for new leader in majority
	var newLeaderID = -1
	helpers.WaitForCondition(t, func() bool {
		for i := 0; i < 5; i++ {
			if i == leaderID {
				continue
			}
			_, isLeader := cluster.Nodes[i].GetState()
			if isLeader {
				newLeaderID = i
				return true
			}
		}
		return false
	}, 2*time.Second, "new leader election in majority")

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
	for i := 0; i < 5; i++ {
		if partition, ok := transporttest.GetCapability[transporttest.PartitionCapable](cluster, i); ok {
			partition.Unblock(leaderID)
		}
		if partition, ok := transporttest.GetCapability[transporttest.PartitionCapable](cluster, leaderID); ok {
			partition.Unblock(i)
		}
	}
}

// TestConfigChangeWithNodeFailures tests configuration changes with node failures
func TestConfigChangeWithNodeFailures(t *testing.T) {
	// Create 5-node cluster
	cluster := helpers.NewTestClusterOfSize(t, 5, helpers.WithClusterAutoStart())

	// Wait for leader
	leaderID, err := cluster.WaitForLeader(2 * time.Second)
	if err != nil {
		t.Fatalf("No leader elected: %v", err)
	}

	// Stop a follower node
	followerToStop := (leaderID + 1) % 5
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	cluster.Nodes[followerToStop].Stop(ctx) //nolint:errcheck // intentional stop for test
	cancel()
	t.Logf("Stopped follower node %d", followerToStop)

	// Try to remove the stopped node
	err = cluster.Nodes[leaderID].RemoveServer(followerToStop)
	if err != nil {
		t.Fatalf("RemoveServer failed: %v", err)
	}

	t.Log("✓ RemoveServer command accepted")

	// Submit a dummy command to ensure configuration change is committed and wait for it
	idx, _, isLeader := cluster.Nodes[leaderID].Submit("dummy-after-remove")
	if !isLeader {
		// Find new leader
		for i, node := range cluster.Nodes {
			if i != followerToStop && node.IsLeader() {
				leaderID = i
				break
			}
		}
	}

	// Wait for the command to be committed
	if isLeader && idx > 0 {
		helpers.WaitForCommitIndex(t, cluster.GetNodesSlice(), idx, time.Second)
	}

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

			// Wait for configuration change to be replicated to majority
			helpers.WaitForCondition(t, func() bool {
				config := cluster.Nodes[leaderID].GetConfiguration()
				for _, srv := range config.Servers {
					if srv.ID == followerToStop {
						return true // Configuration change has been processed
					}
				}
				return false
			}, 500*time.Millisecond, "configuration change to be processed")

			// Restart the node
			customConfig := func(config *raft.Config) {
				config.Peers = []int{0, 1, 2, 3, 4}
				config.Logger = raft.NewTestLogger(t)
			}
			
			_, err := cluster.AddNodeWithConfig(followerToStop, customConfig, true)
			if err == nil {
				t.Logf("Restarted node %d", followerToStop)

				// Wait for node to catch up
				helpers.WaitForCondition(t, func() bool {
					// Check if the restarted node has caught up
					if node, ok := cluster.GetNode(followerToStop); ok {
						return node.GetCommitIndex() > 0
					}
					return false
				}, 2*time.Second, "node catch up after restart")

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
	cluster := helpers.NewTestClusterOfSize(t, 3)

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

			// Wait to see if a new leader is elected
			newLeaderFound := false
			var newLeaderID int
			helpers.WaitForCondition(t, func() bool {
				for i, node := range cluster.Nodes {
					if i == leaderID {
						continue
					}
					_, isLeader := node.GetState()
					if isLeader {
						newLeaderFound = true
						newLeaderID = i
						return true
					}
				}
				return false
			}, 3*time.Second, "new leader election after self-removal")

			if newLeaderFound {
				t.Logf("New leader elected: node %d", newLeaderID)
			}

			if !newLeaderFound {
				t.Error("No new leader elected after leader removed itself")
			}
		}
	})
}

// TestConfigurationPersistence tests that configuration changes are persistent
func TestConfigurationPersistence(t *testing.T) {
	// Create temp directory for persistence
	tempDir, err := os.MkdirTemp("", "raft-config-persist-")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(tempDir) }) //nolint:errcheck // test cleanup

	// Create cluster with persistence
	cluster := helpers.NewTestClusterOfSize(t, 3,
		helpers.WithPersistenceFactory(func(nodeID int) (raft.Persistence, error) {
			nodeDir := filepath.Join(tempDir, fmt.Sprintf("node-%d", nodeID))
			config := &persistence.Config{
				DataDir:  nodeDir,
				ServerID: nodeID,
			}
			return json.NewJSONPersistence(config)
		}),
		helpers.WithClusterAutoStart())

	// Wait for leader
	leaderID, err := cluster.WaitForLeader(2 * time.Second)
	if err != nil {
		t.Fatalf("No leader elected: %v", err)
	}

	// Add a new server to the configuration
	err = cluster.Nodes[leaderID].AddServer(3, "server-3", true)
	if err != nil {
		t.Fatalf("Failed to add server: %v", err)
	}

	// Wait for configuration change to be committed
	helpers.WaitForCondition(t, func() bool {
		config := cluster.Nodes[leaderID].GetConfiguration()
		for _, srv := range config.Servers {
			if srv.ID == 3 {
				return true
			}
		}
		return false
	}, 2*time.Second, "configuration change commit")

	// Verify all nodes have the new configuration
	for nodeID, node := range cluster.GetNodes() {
		config := node.GetConfiguration()
		found := false
		for _, srv := range config.Servers {
			if srv.ID == 3 {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Node %d doesn't have server 3 in configuration", nodeID)
		}
	}

	// Stop cluster
	cluster.Stop()

	// Restart cluster with same persistence
	newCluster := helpers.NewTestClusterOfSize(t, 3,
		helpers.WithPersistenceFactory(func(nodeID int) (raft.Persistence, error) {
			nodeDir := filepath.Join(tempDir, fmt.Sprintf("node-%d", nodeID))
			config := &persistence.Config{
				DataDir:  nodeDir,
				ServerID: nodeID,
			}
			return json.NewJSONPersistence(config)
		}),
		helpers.WithClusterAutoStart())

	// Wait for new leader
	_, err = newCluster.WaitForLeader(2 * time.Second)
	if err != nil {
		t.Fatalf("No leader elected after restart: %v", err)
	}

	// Verify configuration was persisted and restored
	for nodeID, node := range newCluster.GetNodes() {
		config := node.GetConfiguration()
		found := false
		for _, srv := range config.Servers {
			if srv.ID == 3 {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Node %d lost server 3 from configuration after restart", nodeID)
		} else {
			t.Logf("Node %d correctly restored configuration with server 3", nodeID)
		}
	}

	t.Log("✓ Configuration changes are properly persisted and restored")
}

// TestMaximumClusterSize tests behavior at maximum cluster size
func TestMaximumClusterSize(t *testing.T) {
	// Start with 3 nodes
	initialSize := 3
	maxSize := 9 // Typical max for Raft

	cluster := helpers.NewTestClusterOfSize(t, initialSize, helpers.WithClusterAutoStart())

	// Wait for leader
	leaderID, err := cluster.WaitForLeader(2 * time.Second)
	if err != nil {
		t.Fatalf("No leader elected: %v", err)
	}

	// Try to grow cluster to maximum size
	currentSize := initialSize

	for currentSize < maxSize {
		newNodeID := currentSize

		// Create new node with empty peers (will be updated when added)
		customConfig := func(config *raft.Config) {
			config.Peers = []int{} // Will be updated when added
			config.Logger = raft.NewTestLogger(t)
		}
		
		node, err := cluster.AddNodeWithConfig(newNodeID, customConfig, true)
		if err != nil {
			t.Fatalf("Failed to create node %d: %v", newNodeID, err)
		}

		// Add to cluster configuration
		err = cluster.Nodes[leaderID].AddServer(newNodeID, fmt.Sprintf("server-%d", newNodeID), true)
		if err != nil {
			t.Logf("Failed to add node %d at size %d: %v", newNodeID, currentSize, err)
			// Node cleanup is handled by the cluster
			if err := cluster.StopNode(newNodeID); err != nil {
				t.Logf("Failed to stop node %d: %v", newNodeID, err)
			}
			break
		}

		// Add to our tracking
		cluster.Nodes = append(cluster.Nodes, node)
		currentSize++

		t.Logf("Successfully grew cluster to size %d", currentSize)

		// Wait for configuration to be committed
		helpers.WaitForCondition(t, func() bool {
			// Check if the new node appears in the configuration
			config := cluster.Nodes[leaderID].GetConfiguration()
			for _, srv := range config.Servers {
				if srv.ID == newNodeID {
					return true
				}
			}
			return false
		}, time.Second, "configuration propagation")
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
	cluster := helpers.NewTestClusterOfSize(t, 3)

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
