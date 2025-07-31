package raft

import (
	"context"
	"fmt"
	"testing"
	"time"

	// "github.com/ueisele/raft/test" - removed to avoid import cycle
)

// TestBasicConfigurationChange tests adding and removing servers
func TestBasicConfigurationChange(t *testing.T) {
	// Create 4 nodes but only include 3 in initial configuration
	nodes := make([]Node, 4)
	registry := &debugNodeRegistry{
		nodes:  make(map[int]RPCHandler),
		logger: newTestLogger(t),
	}

	for i := 0; i < 4; i++ {
		// Initial configuration only includes nodes 0, 1, 2
		initialPeers := []int{0, 1, 2}
		
		config := &Config{
			ID:                 i,
			Peers:              initialPeers,
			ElectionTimeoutMin: 150 * time.Millisecond,
			ElectionTimeoutMax: 300 * time.Millisecond,
			HeartbeatInterval:  50 * time.Millisecond,
			Logger:             newTestLogger(t),
		}

		transport := &debugTransport{
			id:       i,
			registry: registry,
			logger:   newTestLogger(t),
		}

		stateMachine := &testStateMachine{
			data: make(map[string]string),
		}

		node, err := NewNode(config, transport, nil, stateMachine)
		if err != nil {
			t.Fatalf("Failed to create node %d: %v", i, err)
		}

		nodes[i] = node
		registry.nodes[i] = node.(RPCHandler)
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
	timing := DefaultTimingConfig()
	timing.ElectionTimeout = 2 * time.Second
	var leader Node
	leaderID := WaitForLeaderWithConfig(t, nodes, timing)
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
	WaitForConditionWithProgress(t, func() (bool, string) {
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

	// Wait for the new server to catch up by checking its log length
	WaitForConditionWithProgress(t, func() (bool, string) {
		node3LogLength := nodes[3].GetLogLength()
		leaderLogLength := leader.GetLogLength()
		
		if node3LogLength < leaderLogLength {
			// Submit a dummy command to trigger replication
			leader.Submit(fmt.Sprintf("catch-up"))
		}
		
		return node3LogLength >= leaderLogLength, fmt.Sprintf("node 3 log length: %d, leader log length: %d", node3LogLength, leaderLogLength)
	}, 10*time.Second, "node 3 catch up")
	t.Log("Node 3 has caught up with the leader")

	// Verify server 3 was added
	found := false
	for _, server := range configAfterAdd.Servers {
		if server.ID == 3 {
			found = true
			if !server.Voting {
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
	WaitForConditionWithProgress(t, func() (bool, string) {
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
	WaitForConditionWithProgress(t, func() (bool, string) {
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
	t.Logf("Final configuration after remove: %d servers", len(finalConfig.Servers))
	for _, srv := range finalConfig.Servers {
		t.Logf("  Server %d: %s (voting=%v)", srv.ID, srv.Address, srv.Voting)
	}
	if len(finalConfig.Servers) != 3 {
		t.Errorf("Expected 3 servers after remove, got %d", len(finalConfig.Servers))
	}

	// Verify server 2 was removed
	found = false
	for _, server := range finalConfig.Servers {
		if server.ID == 2 {
			found = true
			break
		}
	}
	if found {
		t.Error("Server 2 should have been removed from configuration")
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

// TestConfigurationManager tests the configuration manager directly
func TestConfigurationManager(t *testing.T) {
	cm := NewConfigurationManager([]int{1, 2, 3})

	// Test initial configuration
	config := cm.GetConfiguration()
	if len(config.Servers) != 3 {
		t.Errorf("Expected 3 servers, got %d", len(config.Servers))
	}

	// Test getting voting members
	members := cm.GetVotingMembers()
	if len(members) != 3 {
		t.Errorf("Expected 3 voting members, got %d", len(members))
	}

	// Test IsMember
	if !cm.IsMember(1) {
		t.Error("Server 1 should be a member")
	}
	if cm.IsMember(4) {
		t.Error("Server 4 should not be a member")
	}

	// Test adding a server
	newServer := ServerConfig{
		ID:      4,
		Address: "node-4",
		Voting:  false,
	}

	err := cm.StartAddServer(newServer)
	if err != nil {
		t.Errorf("Failed to start add server: %v", err)
	}

	// Check pending change
	pending := cm.GetPendingConfigChange()
	if pending == nil {
		t.Fatal("Expected pending configuration change")
	}

	if pending.Type != ConfigChangeAddServer {
		t.Errorf("Expected add server change, got %v", pending.Type)
	}

	// Try to start another change (should fail)
	err = cm.StartRemoveServer(1)
	if err == nil {
		t.Error("Should not allow concurrent configuration changes")
	}

	// Commit the change
	err = cm.CommitConfiguration(100)
	if err != nil {
		t.Errorf("Failed to commit configuration: %v", err)
	}

	// Verify new configuration
	config = cm.GetConfiguration()
	if len(config.Servers) != 4 {
		t.Errorf("Expected 4 servers after add, got %d", len(config.Servers))
	}

	if config.Index != 100 {
		t.Errorf("Expected configuration index 100, got %d", config.Index)
	}

	// Test removing a server
	err = cm.StartRemoveServer(4)
	if err != nil {
		t.Errorf("Failed to start remove server: %v", err)
	}

	// Cancel the change
	cm.CancelPendingChange()

	// Verify configuration unchanged
	config = cm.GetConfiguration()
	if len(config.Servers) != 4 {
		t.Errorf("Configuration should be unchanged after cancel")
	}
}
