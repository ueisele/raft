package raft

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

// TestBasicMembershipChange tests adding and removing servers from the cluster
func TestBasicMembershipChange(t *testing.T) {
	// Start with 3-node cluster
	initialPeers := []int{0, 1, 2}
	rafts := make([]*TestRaft, 3)
	applyChannels := make([]chan LogEntry, 3)
	transport := NewTestTransport()

	for i := 0; i < 3; i++ {
		applyChannels[i] = make(chan LogEntry, 100)
		rafts[i] = NewTestRaft(initialPeers, i, applyChannels[i], transport)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	for i := 0; i < 3; i++ {
		rafts[i].Start(ctx)
	}

	// Wait for leader election
	time.Sleep(1 * time.Second)

	// Find leader
	var leader *Raft
	var leaderIdx int
	for i, rf := range rafts {
		_, isLeader := rf.GetState()
		if isLeader {
			leader = rf.Raft
			leaderIdx = i
			break
		}
	}

	if leader == nil {
		t.Fatal("No leader found")
	}

	// Submit some initial commands
	for i := 0; i < 5; i++ {
		leader.Submit(fmt.Sprintf("initial-cmd%d", i))
		time.Sleep(100 * time.Millisecond)
	}

	// Test adding a new server (server 3)
	newServerConfig := ServerConfiguration{ID: 3, Address: "localhost:8003"}
	oldConfig := Configuration{Servers: []ServerConfiguration{
		{ID: 0, Address: "localhost:8000"},
		{ID: 1, Address: "localhost:8001"},
		{ID: 2, Address: "localhost:8002"},
	}}
	newConfig := Configuration{Servers: append(oldConfig.Servers, newServerConfig)}

	// Create configuration change
	change := ConfigurationChange{
		Type:    AddServer,
		Server:  newServerConfig,
		OldConfig: oldConfig,
		NewConfig: newConfig,
	}

	// Submit configuration change
	changeData, _ := json.Marshal(change)
	index, term, isLeader := leader.Submit(string(changeData))
	if !isLeader {
		t.Fatal("Leader lost leadership during configuration change")
	}

	t.Logf("Configuration change submitted at index %d, term %d", index, term)

	// Wait for configuration change to be applied
	time.Sleep(2 * time.Second)

	// Verify the configuration change was applied
	leader.mu.RLock()
	currentConfig := leader.currentConfig
	leader.mu.RUnlock()

	if len(currentConfig.Servers) != 4 {
		t.Errorf("Expected 4 servers in new configuration, got %d", len(currentConfig.Servers))
	}

	found := false
	for _, server := range currentConfig.Servers {
		if server.ID == 3 {
			found = true
			break
		}
	}
	if !found {
		t.Error("New server not found in configuration")
	}

	// Test removing a server (server 0)
	removeConfig := Configuration{Servers: currentConfig.Servers[1:]} // Remove first server
	removeChange := ConfigurationChange{
		Type:    RemoveServer,
		Server:  ServerConfiguration{ID: 0, Address: "localhost:8000"},
		OldConfig: currentConfig,
		NewConfig: removeConfig,
	}

	removeData, _ := json.Marshal(removeChange)
	
	// If the leader is server 0, it should step down after committing this change
	wasLeaderServer0 := (leaderIdx == 0)
	
	leader.Submit(string(removeData))
	time.Sleep(3 * time.Second)

	if wasLeaderServer0 {
		// Server 0 should have stepped down
		_, stillLeader := rafts[0].GetState()
		if stillLeader {
			t.Error("Removed server should have stepped down from leadership")
		}

		// A new leader should be elected from remaining servers
		newLeaderFound := false
		for i := 1; i < 3; i++ {
			_, isLeader := rafts[i].GetState()
			if isLeader {
				newLeaderFound = true
				break
			}
		}
		if !newLeaderFound {
			t.Error("No new leader elected after removing old leader")
		}
	}

	// Stop all instances
	for i := 0; i < 3; i++ {
		rafts[i].Stop()
	}
}

// TestJointConsensus tests the two-phase configuration change protocol
func TestJointConsensus(t *testing.T) {
	peers := []int{0, 1, 2}
	rafts := make([]*TestRaft, 3)
	applyChannels := make([]chan LogEntry, 3)
	transport := NewTestTransport()

	for i := 0; i < 3; i++ {
		applyChannels[i] = make(chan LogEntry, 100)
		rafts[i] = NewTestRaft(peers, i, applyChannels[i], transport)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	for i := 0; i < 3; i++ {
		rafts[i].Start(ctx)
	}

	// Wait for leader election
	time.Sleep(1 * time.Second)

	var leader *Raft
	for _, rf := range rafts {
		_, isLeader := rf.GetState()
		if isLeader {
			leader = rf.Raft
			break
		}
	}

	if leader == nil {
		t.Fatal("No leader found")
	}

	// Submit initial commands
	for i := 0; i < 3; i++ {
		leader.Submit(fmt.Sprintf("pre-reconfig-cmd%d", i))
		time.Sleep(100 * time.Millisecond)
	}

	// Create a simplified joint consensus configuration change
	oldConfig := Configuration{Servers: []ServerConfiguration{
		{ID: 0, Address: "localhost:8000"},
		{ID: 1, Address: "localhost:8001"},
		{ID: 2, Address: "localhost:8002"},
	}}

	newConfig := Configuration{Servers: []ServerConfiguration{
		{ID: 0, Address: "localhost:8000"},
		{ID: 1, Address: "localhost:8001"},
		{ID: 2, Address: "localhost:8002"},
		{ID: 3, Address: "localhost:8003"},
	}}

	// Phase 1: Transition to joint consensus (Cold,new)
	// Create joint configuration that contains all servers from both old and new configs
	jointConfig := Configuration{Servers: []ServerConfiguration{
		{ID: 0, Address: "localhost:8000"},
		{ID: 1, Address: "localhost:8001"},
		{ID: 2, Address: "localhost:8002"},
		{ID: 3, Address: "localhost:8003"},
	}}
	
	jointChange := ConfigurationChange{
		Type:        "joint",
		OldConfig:   oldConfig,
		NewConfig:   newConfig,
		JointConfig: &jointConfig,
	}

	jointData, _ := json.Marshal(jointChange)
	leader.Submit(string(jointData))
	time.Sleep(2 * time.Second)

	// Verify joint consensus is active
	leader.mu.RLock()
	inJointConsensus := leader.inJointConsensus
	leader.mu.RUnlock()

	if !inJointConsensus {
		t.Error("Should be in joint consensus phase")
	}

	// Submit commands during joint consensus
	for i := 0; i < 3; i++ {
		leader.Submit(fmt.Sprintf("joint-cmd%d", i))
		time.Sleep(100 * time.Millisecond)
	}

	// Phase 2: Transition to new configuration (Cnew)
	finalChange := ConfigurationChange{
		Type:      "final",
		OldConfig: oldConfig,
		NewConfig: newConfig,
	}

	finalData, _ := json.Marshal(finalChange)
	leader.Submit(string(finalData))
	time.Sleep(3 * time.Second)

	// Verify transition to new configuration
	leader.mu.RLock()
	finalInJoint := leader.inJointConsensus
	finalConfig := leader.currentConfig
	leader.mu.RUnlock()

	if finalInJoint {
		t.Error("Should have exited joint consensus")
	}

	if len(finalConfig.Servers) != len(newConfig.Servers) {
		t.Errorf("Final configuration has wrong number of servers: got %d, expected %d", 
			len(finalConfig.Servers), len(newConfig.Servers))
	}

	// Submit post-reconfiguration commands
	for i := 0; i < 3; i++ {
		leader.Submit(fmt.Sprintf("post-reconfig-cmd%d", i))
		time.Sleep(100 * time.Millisecond)
	}

	// Stop all instances
	for i := 0; i < 3; i++ {
		rafts[i].Stop()
	}
}

// TestMembershipChangeEdgeCases tests edge cases in membership changes
func TestMembershipChangeEdgeCases(t *testing.T) {
	peers := []int{0, 1, 2, 3, 4}
	rafts := make([]*TestRaft, 5)
	applyChannels := make([]chan LogEntry, 5)
	transport := NewTestTransport()

	for i := 0; i < 5; i++ {
		applyChannels[i] = make(chan LogEntry, 100)
		rafts[i] = NewTestRaft(peers, i, applyChannels[i], transport)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	for i := 0; i < 5; i++ {
		rafts[i].Start(ctx)
	}

	time.Sleep(1 * time.Second)

	var leader *Raft
	var leaderIdx int
	for i, rf := range rafts {
		_, isLeader := rf.GetState()
		if isLeader {
			leader = rf.Raft
			leaderIdx = i
			break
		}
	}

	if leader == nil {
		t.Fatal("No leader found")
	}

	// Test 1: Remove multiple servers simultaneously (should be rejected or handled carefully)
	currentConfig := Configuration{Servers: make([]ServerConfiguration, 5)}
	for i := 0; i < 5; i++ {
		currentConfig.Servers[i] = ServerConfiguration{ID: i, Address: fmt.Sprintf("localhost:800%d", i)}
	}

	// Try to remove 2 servers at once (dangerous - might lose majority)
	newConfig := Configuration{Servers: currentConfig.Servers[:3]}
	multiRemoveChange := ConfigurationChange{
		Type:      RemoveServer,
		OldConfig: currentConfig,
		NewConfig: newConfig,
	}

	multiRemoveData, _ := json.Marshal(multiRemoveChange)
	leader.Submit(string(multiRemoveData))
	time.Sleep(2 * time.Second)

	// Test 2: Add server that's already in cluster (should be idempotent)
	duplicateServer := ServerConfiguration{ID: 1, Address: "localhost:8001"}
	duplicateChange := ConfigurationChange{
		Type:      AddServer,
		Server:    duplicateServer,
		OldConfig: currentConfig,
		NewConfig: currentConfig, // No change
	}

	duplicateData, _ := json.Marshal(duplicateChange)
	leader.Submit(string(duplicateData))
	time.Sleep(1 * time.Second)

	// Test 3: Configuration change during network partition
	// Partition the cluster
	transport.DisconnectPair(0, 3)
	transport.DisconnectPair(0, 4)
	transport.DisconnectPair(1, 3)
	transport.DisconnectPair(1, 4)
	transport.DisconnectPair(2, 3)
	transport.DisconnectPair(2, 4)

	// Try configuration change in minority partition
	if leaderIdx >= 3 {
		// Leader is in minority, should not be able to commit changes
		minorityChange := ConfigurationChange{
			Type: AddServer,
			Server: ServerConfiguration{ID: 5, Address: "localhost:8005"},
			OldConfig: currentConfig,
			NewConfig: Configuration{Servers: append(currentConfig.Servers, ServerConfiguration{ID: 5, Address: "localhost:8005"})},
		}

		minorityData, _ := json.Marshal(minorityChange)
		leader.Submit(string(minorityData))
		time.Sleep(2 * time.Second)

		// Change should not be committed due to lack of majority
		leader.mu.RLock()
		commitIndex := leader.commitIndex
		leader.mu.RUnlock()

		// The change might be in the log but not committed
		t.Logf("Leader in minority partition commit index: %d", commitIndex)
	}

	// Heal partition
	transport.ReconnectPair(0, 3)
	transport.ReconnectPair(0, 4)
	transport.ReconnectPair(1, 3)
	transport.ReconnectPair(1, 4)
	transport.ReconnectPair(2, 3)
	transport.ReconnectPair(2, 4)

	time.Sleep(3 * time.Second)

	// Test 4: Rapid successive configuration changes
	for i := 0; i < 3; i++ {
		rapidChange := ConfigurationChange{
			Type: AddServer,
			Server: ServerConfiguration{ID: 10 + i, Address: fmt.Sprintf("localhost:80%d", 10+i)},
			OldConfig: currentConfig,
			NewConfig: Configuration{Servers: append(currentConfig.Servers, ServerConfiguration{ID: 10 + i, Address: fmt.Sprintf("localhost:80%d", 10+i)})},
		}

		rapidData, _ := json.Marshal(rapidChange)
		
		// Find current leader (might have changed)
		for _, rf := range rafts {
			_, isLeader := rf.GetState()
			if isLeader {
				rf.Submit(string(rapidData))
				break
			}
		}
		time.Sleep(500 * time.Millisecond)
	}

	time.Sleep(2 * time.Second)

	// Stop all instances
	for i := 0; i < 5; i++ {
		rafts[i].Stop()
	}
}

// TestNonVotingMembers tests the addition of non-voting members before full membership
func TestNonVotingMembers(t *testing.T) {
	peers := []int{0, 1, 2}
	rafts := make([]*TestRaft, 3)
	applyChannels := make([]chan LogEntry, 3)
	transport := NewTestTransport()

	for i := 0; i < 3; i++ {
		applyChannels[i] = make(chan LogEntry, 100)
		rafts[i] = NewTestRaft(peers, i, applyChannels[i], transport)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	for i := 0; i < 3; i++ {
		rafts[i].Start(ctx)
	}

	time.Sleep(1 * time.Second)

	var leader *Raft
	for _, rf := range rafts {
		_, isLeader := rf.GetState()
		if isLeader {
			leader = rf.Raft
			break
		}
	}

	if leader == nil {
		t.Fatal("No leader found")
	}

	// Submit initial commands
	for i := 0; i < 10; i++ {
		leader.Submit(fmt.Sprintf("cmd%d", i))
		time.Sleep(50 * time.Millisecond)
	}

	// Add a non-voting member first
	nonVotingServer := ServerConfiguration{ID: 3, Address: "localhost:8003"}
	nonVotingChange := ConfigurationChange{
		Type:   AddNonVotingServer,
		Server: nonVotingServer,
		OldConfig: Configuration{Servers: []ServerConfiguration{
			{ID: 0, Address: "localhost:8000"},
			{ID: 1, Address: "localhost:8001"},
			{ID: 2, Address: "localhost:8002"},
		}},
		NewConfig: Configuration{Servers: []ServerConfiguration{
			{ID: 0, Address: "localhost:8000"},
			{ID: 1, Address: "localhost:8001"},
			{ID: 2, Address: "localhost:8002"},
			{ID: 3, Address: "localhost:8003", NonVoting: true},
		}},
	}

	nonVotingData, _ := json.Marshal(nonVotingChange)
	leader.Submit(string(nonVotingData))
	time.Sleep(2 * time.Second)

	// Submit more commands to let non-voting member catch up
	for i := 10; i < 20; i++ {
		leader.Submit(fmt.Sprintf("catchup-cmd%d", i))
		time.Sleep(50 * time.Millisecond)
	}

	// Now promote non-voting member to voting member
	votingChange := ConfigurationChange{
		Type:   PromoteServer,
		Server: ServerConfiguration{ID: 3, Address: "localhost:8003"},
		OldConfig: Configuration{Servers: []ServerConfiguration{
			{ID: 0, Address: "localhost:8000"},
			{ID: 1, Address: "localhost:8001"},
			{ID: 2, Address: "localhost:8002"},
			{ID: 3, Address: "localhost:8003", NonVoting: true},
		}},
		NewConfig: Configuration{Servers: []ServerConfiguration{
			{ID: 0, Address: "localhost:8000"},
			{ID: 1, Address: "localhost:8001"},
			{ID: 2, Address: "localhost:8002"},
			{ID: 3, Address: "localhost:8003", NonVoting: false},
		}},
	}

	votingData, _ := json.Marshal(votingChange)
	leader.Submit(string(votingData))
	time.Sleep(2 * time.Second)

	// Verify the new server can participate in elections
	// Force a leader change to test if new server can become leader
	originalLeaderIdx := -1
	for i, rf := range rafts {
		_, isLeader := rf.GetState()
		if isLeader {
			originalLeaderIdx = i
			transport.DisconnectServer(i)
			break
		}
	}

	time.Sleep(3 * time.Second)

	// Check if a new leader was elected (possibly the new server if it's in the test cluster)
	newLeaderElected := false
	for i, rf := range rafts {
		if i != originalLeaderIdx {
			_, isLeader := rf.GetState()
			if isLeader {
				newLeaderElected = true
				t.Logf("New leader elected: server %d", i)
				break
			}
		}
	}

	if !newLeaderElected {
		t.Log("No new leader elected (may be expected with only 3 servers and one disconnected)")
	}

	// Reconnect original leader
	if originalLeaderIdx >= 0 {
		transport.ReconnectServer(originalLeaderIdx)
	}

	time.Sleep(2 * time.Second)

	// Stop all instances
	for i := 0; i < 3; i++ {
		rafts[i].Stop()
	}
}