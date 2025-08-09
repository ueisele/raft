package raft

import (
	"testing"
)

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
		return // Make linter understand this is unreachable
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
