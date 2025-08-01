package configuration

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/ueisele/raft"
	"github.com/ueisele/raft/integration/helpers"
)

// TestBasicConfigurationChange tests adding and removing servers
func TestBasicConfigurationChange(t *testing.T) {
	// Create 4 nodes but only include 3 in initial configuration
	nodes := make([]raft.Node, 4)
	registry := helpers.NewDebugNodeRegistry(raft.NewTestLogger(t))

	for i := 0; i < 4; i++ {
		// Initial configuration only includes nodes 0, 1, 2
		initialPeers := []int{0, 1, 2}
		
		config := &raft.Config{
			ID:                 i,
			Peers:              initialPeers,
			ElectionTimeoutMin: 150 * time.Millisecond,
			ElectionTimeoutMax: 300 * time.Millisecond,
			HeartbeatInterval:  50 * time.Millisecond,
			Logger:             raft.NewTestLogger(t),
		}

		transport := helpers.NewDebugTransport(i, registry, raft.NewTestLogger(t))

		stateMachine := raft.NewMockStateMachine()

		node, err := raft.NewNode(config, transport, nil, stateMachine)
		if err != nil {
			t.Fatalf("Failed to create node %d: %v", i, err)
		}

		nodes[i] = node
		registry.Register(i, node.(raft.RPCHandler))
	}

	// Start all nodes (including node 3 which is not in configuration)
	ctx := context.Background()
	for i, node := range nodes {
		if err := node.Start(ctx); err != nil {
			t.Fatalf("Failed to start node %d: %v", i, err)
		}
		defer node.Stop()
	}

	// Wait for leader election
	timing := helpers.DefaultTimingConfig()
	timing.ElectionTimeout = 2 * time.Second
	var leader raft.Node
	leaderID := helpers.WaitForLeader(t, nodes, timing.ElectionTimeout)
	if leaderID >= 0 {
		leader = nodes[leaderID]
	} else {
		t.Fatal("No leader elected")
	}

	// Get initial configuration
	initialConfig := leader.GetConfiguration()
	t.Logf("Initial configuration: %d servers", len(initialConfig.Servers))
	for _, srv := range initialConfig.Servers {
		t.Logf("  Server %d: %s (voting=%v)", srv.ID, srv.Address, srv.Voting)
	}
	if len(initialConfig.Servers) != 3 {
		t.Errorf("Expected 3 servers, got %d", len(initialConfig.Servers))
	}

	// Test 1: Add server 3 (which exists but is not in configuration)
	t.Log("Test 1: Adding server 3 to configuration")
	err := leader.AddServer(3, "node-3", true)
	if err != nil {
		t.Errorf("Failed to add server: %v", err)
	}

	// Wait for configuration change to propagate
	helpers.WaitForConditionWithProgress(t, func() (bool, string) {
		config := leader.GetConfiguration()
		return len(config.Servers) == 4, fmt.Sprintf("waiting for config to have 4 servers, currently has %d", len(config.Servers))
	}, 2*time.Second, "configuration propagation after add")

	// Check configuration after add
	configAfterAdd := leader.GetConfiguration()
	t.Logf("Configuration after add: %d servers", len(configAfterAdd.Servers))
	for _, srv := range configAfterAdd.Servers {
		t.Logf("  Server %d: %s (voting=%v)", srv.ID, srv.Address, srv.Voting)
	}
	if len(configAfterAdd.Servers) != 4 {
		t.Errorf("Expected 4 servers after add, got %d", len(configAfterAdd.Servers))
	}

	// Verify server 3 was added
	found := false
	for _, srv := range configAfterAdd.Servers {
		if srv.ID == 3 {
			found = true
			if srv.Address != "node-3" {
				t.Errorf("Server 3 has wrong address: %s", srv.Address)
			}
			if !srv.Voting {
				t.Error("Server 3 should be a voting member")
			}
			break
		}
	}
	if !found {
		t.Error("Server 3 should have been added to configuration")
	}

	// Ensure all nodes are at the same commit index before removing
	t.Log("Ensuring all nodes are synchronized before removal")
	leaderCommitIndex := leader.GetCommitIndex()
	helpers.WaitForConditionWithProgress(t, func() (bool, string) {
		allSynced := true
		statuses := []string{}
		for i, node := range nodes[:4] { // Check all 4 nodes
			nodeCommitIndex := node.GetCommitIndex()
			statuses = append(statuses, fmt.Sprintf("node %d: %d", i, nodeCommitIndex))
			if nodeCommitIndex < leaderCommitIndex {
				allSynced = false
			}
		}
		
		if !allSynced {
			// Submit a command to trigger replication
			leader.Submit(fmt.Sprintf("sync"))
			leaderCommitIndex = leader.GetCommitIndex()
		}
		
		return allSynced, fmt.Sprintf("commit indices: %v (leader: %d)", statuses, leaderCommitIndex)
	}, 5*time.Second, "all nodes synchronization")
	t.Log("All nodes synchronized")

	// Test 2: Remove server 2
	t.Log("Test 2: Removing server 2 from configuration")
	err = leader.RemoveServer(2)
	if err != nil {
		t.Errorf("Failed to remove server: %v", err)
	}

	// Wait for configuration change to be applied
	helpers.WaitForConditionWithProgress(t, func() (bool, string) {
		config := leader.GetConfiguration()
		
		if len(config.Servers) != 3 {
			// Submit a dummy command to trigger replication
			leader.Submit(fmt.Sprintf("remove-helper"))
		}
		
		return len(config.Servers) == 3, fmt.Sprintf("waiting for config to have 3 servers after removal, currently has %d", len(config.Servers))
	}, 5*time.Second, "configuration propagation after remove")
	t.Log("Configuration change applied successfully")

	// Check final configuration
	finalConfig := leader.GetConfiguration()
	t.Logf("Final configuration: %d servers", len(finalConfig.Servers))
	for _, srv := range finalConfig.Servers {
		t.Logf("  Server %d: %s (voting=%v)", srv.ID, srv.Address, srv.Voting)
	}
	if len(finalConfig.Servers) != 3 {
		t.Errorf("Expected 3 servers after remove, got %d", len(finalConfig.Servers))
	}

	// Verify server 2 was removed
	for _, srv := range finalConfig.Servers {
		if srv.ID == 2 {
			t.Error("Server 2 should have been removed from configuration")
		}
	}

	// Verify remaining servers are 0, 1, 3
	expectedServers := map[int]bool{0: false, 1: false, 3: false}
	for _, server := range finalConfig.Servers {
		if _, ok := expectedServers[server.ID]; ok {
			expectedServers[server.ID] = true
		}
	}
	for id, found := range expectedServers {
		if !found {
			t.Errorf("Expected server %d in final configuration", id)
		}
	}
}