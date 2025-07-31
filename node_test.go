package raft

import (
	"testing"
	"time"
)

// TestConfigDefaults tests that default values are applied correctly
func TestConfigDefaults(t *testing.T) {
	// Test with empty config
	config := &Config{
		ID:    0,
		Peers: []int{0},
	}
	
	// Create a node with minimal config
	transport := &testTransport{responses: make(map[int]*RequestVoteReply)}
	stateMachine := &testStateMachine{data: make(map[string]string)}
	
	node, err := NewNode(config, transport, nil, stateMachine)
	if err != nil {
		t.Fatalf("Failed to create node: %v", err)
	}
	
	// Get the internal config
	n := node.(*raftNode)
	cfg := n.config
	
	// Verify defaults were applied
	if cfg.MaxLogSize != 10000 {
		t.Errorf("MaxLogSize default not applied: got %d, want 10000", cfg.MaxLogSize)
	}
	
	if cfg.ElectionTimeoutMin != 150*time.Millisecond {
		t.Errorf("ElectionTimeoutMin default not applied: got %v, want %v", cfg.ElectionTimeoutMin, 150*time.Millisecond)
	}
	
	if cfg.ElectionTimeoutMax != 300*time.Millisecond {
		t.Errorf("ElectionTimeoutMax default not applied: got %v, want %v", cfg.ElectionTimeoutMax, 300*time.Millisecond)
	}
	
	if cfg.HeartbeatInterval != 50*time.Millisecond {
		t.Errorf("HeartbeatInterval default not applied: got %v, want %v", cfg.HeartbeatInterval, 50*time.Millisecond)
	}
}

// TestConfigDefaultsPreserveExisting tests that existing values are not overwritten
func TestConfigDefaultsPreserveExisting(t *testing.T) {
	// Test with custom config values
	config := &Config{
		ID:                 0,
		Peers:              []int{0},
		MaxLogSize:         5000,
		ElectionTimeoutMin: 200 * time.Millisecond,
		ElectionTimeoutMax: 400 * time.Millisecond,
		HeartbeatInterval:  100 * time.Millisecond,
	}
	
	// Create a node with custom config
	transport := &testTransport{responses: make(map[int]*RequestVoteReply)}
	stateMachine := &testStateMachine{data: make(map[string]string)}
	
	node, err := NewNode(config, transport, nil, stateMachine)
	if err != nil {
		t.Fatalf("Failed to create node: %v", err)
	}
	
	// Get the internal config
	n := node.(*raftNode)
	cfg := n.config
	
	// Verify custom values were preserved
	if cfg.MaxLogSize != 5000 {
		t.Errorf("MaxLogSize was overwritten: got %d, want 5000", cfg.MaxLogSize)
	}
	
	if cfg.ElectionTimeoutMin != 200*time.Millisecond {
		t.Errorf("ElectionTimeoutMin was overwritten: got %v, want %v", cfg.ElectionTimeoutMin, 200*time.Millisecond)
	}
	
	if cfg.ElectionTimeoutMax != 400*time.Millisecond {
		t.Errorf("ElectionTimeoutMax was overwritten: got %v, want %v", cfg.ElectionTimeoutMax, 400*time.Millisecond)
	}
	
	if cfg.HeartbeatInterval != 100*time.Millisecond {
		t.Errorf("HeartbeatInterval was overwritten: got %v, want %v", cfg.HeartbeatInterval, 100*time.Millisecond)
	}
}

// TestConfigDefaultsValidation tests that invalid configurations are fixed
func TestConfigDefaultsValidation(t *testing.T) {
	// Test with invalid config (min > max)
	config := &Config{
		ID:                 0,
		Peers:              []int{0},
		ElectionTimeoutMin: 400 * time.Millisecond,
		ElectionTimeoutMax: 200 * time.Millisecond, // Invalid: max < min
	}
	
	// Create a node with invalid config
	transport := &testTransport{responses: make(map[int]*RequestVoteReply)}
	stateMachine := &testStateMachine{data: make(map[string]string)}
	
	node, err := NewNode(config, transport, nil, stateMachine)
	if err != nil {
		t.Fatalf("Failed to create node: %v", err)
	}
	
	// Get the internal config
	n := node.(*raftNode)
	cfg := n.config
	
	// Verify that max was adjusted to be >= min
	if cfg.ElectionTimeoutMax < cfg.ElectionTimeoutMin {
		t.Errorf("ElectionTimeoutMax not fixed: min=%v, max=%v", cfg.ElectionTimeoutMin, cfg.ElectionTimeoutMax)
	}
	
	// Should be equal in this case
	if cfg.ElectionTimeoutMax != cfg.ElectionTimeoutMin {
		t.Errorf("ElectionTimeoutMax should equal ElectionTimeoutMin when max < min: got max=%v, min=%v", 
			cfg.ElectionTimeoutMax, cfg.ElectionTimeoutMin)
	}
}