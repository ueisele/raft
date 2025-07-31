package raft

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

// TestLeaderCommitIndexPreservation tests that leader preserves commit index across terms
func TestLeaderCommitIndexPreservation(t *testing.T) {
	// Create 5-node cluster
	numNodes := 5
	nodes := make([]Node, numNodes)
	registry := &debugNodeRegistry{
		nodes:  make(map[int]RPCHandler),
		logger: newTestLogger(t),
	}

	for i := 0; i < numNodes; i++ {
		config := &Config{
			ID:                 i,
			Peers:              []int{0, 1, 2, 3, 4},
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
			mu:   sync.Mutex{},
			data: make(map[string]string),
		}

		node, err := NewNode(config, transport, nil, stateMachine)
		if err != nil {
			t.Fatalf("Failed to create node %d: %v", i, err)
		}

		nodes[i] = node
		registry.nodes[i] = node.(RPCHandler)
	}

	// Start all nodes
	ctx := context.Background()
	for i, node := range nodes {
		if err := node.Start(ctx); err != nil {
			t.Fatalf("Failed to start node %d: %v", i, err)
		}
		defer node.Stop()
	}

	// Wait for leader election
	time.Sleep(500 * time.Millisecond)

	// Find initial leader
	var leaderID int
	var leader Node
	for i, node := range nodes {
		if node.IsLeader() {
			leaderID = i
			leader = node
			break
		}
	}

	if leader == nil {
		t.Fatal("No leader elected")
	}

	t.Logf("Initial leader is node %d", leaderID)

	// Submit commands to establish commit index
	for i := 0; i < 10; i++ {
		cmd := fmt.Sprintf("cmd-%d", i)
		_, _, isLeader := leader.Submit(cmd)
		if !isLeader {
			t.Fatal("Lost leadership")
		}
	}

	// Wait for replication and commit
	time.Sleep(1 * time.Second)

	// Record commit index before leader change
	initialCommitIndex := leader.GetCommitIndex()
	t.Logf("Initial commit index: %d", initialCommitIndex)

	// Force leader to step down and simulate complete node failure
	leader.Stop()
	// Remove from registry to simulate node failure
	delete(registry.nodes, leaderID)
	// Also nil out the node reference to ensure it can't be accessed
	nodes[leaderID] = nil
	t.Logf("Stopped leader %d", leaderID)

	// Wait for new leader election with timeout
	var newLeaderID int
	var newLeader Node
	
	timeout := time.After(5 * time.Second)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	
	for {
		select {
		case <-timeout:
			// Debug: Check state of all remaining nodes
			for i, node := range nodes {
				if i != leaderID && node != nil {
					term, isLeader := node.GetState()
					t.Logf("Node %d: term=%d, isLeader=%v", i, term, isLeader)
				}
			}
			t.Fatal("No new leader elected after 5 seconds")
			
		case <-ticker.C:
			// Check if any node became leader
			for i, node := range nodes {
				if i != leaderID && node != nil && node.IsLeader() {
					newLeaderID = i
					newLeader = node
					goto found
				}
			}
		}
	}
	
found:
	t.Logf("New leader elected: node %d", newLeaderID)

	t.Logf("New leader is node %d", newLeaderID)

	// Submit a command to the new leader to force it to commit
	// According to Raft, a new leader cannot commit entries from previous terms
	// until it has committed an entry from its current term
	cmd := "new-leader-cmd"
	_, _, isLeader := newLeader.Submit(cmd)
	if !isLeader {
		t.Fatal("New leader rejected command")
	}
	
	// Wait for the new command to be committed
	time.Sleep(500 * time.Millisecond)
	
	// Check new leader's commit index
	newLeaderCommitIndex := newLeader.GetCommitIndex()
	t.Logf("New leader commit index after new command: %d", newLeaderCommitIndex)

	// New leader should have advanced commit index to at least the initial commit index
	if newLeaderCommitIndex < initialCommitIndex {
		t.Errorf("New leader has lower commit index: %d < %d",
			newLeaderCommitIndex, initialCommitIndex)
	}

	// Submit new commands to verify functionality
	for i := 0; i < 5; i++ {
		cmd := fmt.Sprintf("new-cmd-%d", i)
		_, _, isLeader := newLeader.Submit(cmd)
		if !isLeader {
			t.Fatal("New leader lost leadership")
		}
	}

	// Wait for replication
	time.Sleep(500 * time.Millisecond)

	// Verify commit index advanced beyond initial
	finalCommitIndex := newLeader.GetCommitIndex()
	if finalCommitIndex <= initialCommitIndex {
		t.Error("New leader failed to advance commit index beyond initial")
	}

	t.Logf("Final commit index: %d", finalCommitIndex)
}

// TestLeaderStabilityDuringConfigChange tests leader remains stable during configuration changes
func TestLeaderStabilityDuringConfigChange(t *testing.T) {
	// Create 3-node cluster
	nodes := make([]Node, 3)
	registry := &debugNodeRegistry{
		nodes:  make(map[int]RPCHandler),
		logger: newTestLogger(t),
	}

	for i := 0; i < 3; i++ {
		config := &Config{
			ID:                 i,
			Peers:              []int{0, 1, 2},
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
			mu:   sync.Mutex{},
			data: make(map[string]string),
		}

		node, err := NewNode(config, transport, nil, stateMachine)
		if err != nil {
			t.Fatalf("Failed to create node %d: %v", i, err)
		}

		nodes[i] = node
		registry.nodes[i] = node.(RPCHandler)
	}

	// Start all nodes
	ctx := context.Background()
	for i, node := range nodes {
		if err := node.Start(ctx); err != nil {
			t.Fatalf("Failed to start node %d: %v", i, err)
		}
		defer node.Stop()
	}

	// Wait for leader election
	time.Sleep(500 * time.Millisecond)

	// Find leader
	var leader Node
	var leaderID int
	for i, node := range nodes {
		if node.IsLeader() {
			leader = node
			leaderID = i
			break
		}
	}

	if leader == nil {
		t.Fatal("No leader elected")
	}

	t.Logf("Leader is node %d", leaderID)

	// Track leader changes
	leaderChanges := 0
	stopMonitor := make(chan struct{})
	var monitorWg sync.WaitGroup

	monitorWg.Add(1)
	go func() {
		defer monitorWg.Done()
		currentLeader := leaderID

		for {
			select {
			case <-stopMonitor:
				return
			default:
				for i, node := range nodes {
					if node.IsLeader() && i != currentLeader {
						leaderChanges++
						currentLeader = i
						t.Logf("Leader changed to node %d", i)
					}
				}
				time.Sleep(100 * time.Millisecond)
			}
		}
	}()

	// Start configuration change
	t.Log("Starting configuration change")
	err := leader.AddServer(3, "server-3:8003", true)
	if err != nil {
		t.Logf("Configuration change result: %v", err)
	}

	// Submit commands during config change
	for i := 0; i < 10; i++ {
		cmd := fmt.Sprintf("during-config-%d", i)
		_, _, isLeader := leader.Submit(cmd)
		if !isLeader {
			t.Log("Leader lost leadership during config change")
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Stop monitoring
	close(stopMonitor)
	monitorWg.Wait()

	// Check leader stability
	if leaderChanges > 1 {
		t.Errorf("Too many leader changes during config change: %d", leaderChanges)
	} else if leaderChanges == 1 {
		t.Log("One leader change occurred (acceptable during config change)")
	} else {
		t.Log("Leader remained stable throughout config change")
	}
}

// TestLeaderElectionWithVaryingClusterSizes tests leader election with different cluster sizes
func TestLeaderElectionWithVaryingClusterSizes(t *testing.T) {
	testCases := []struct {
		name        string
		clusterSize int
		expectation string
	}{
		{"Single node", 1, "Should elect self as leader"},
		{"Two nodes", 2, "Should elect leader with both nodes"},
		{"Three nodes", 3, "Standard case"},
		{"Five nodes", 5, "Larger cluster"},
		{"Seven nodes", 7, "Large cluster"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create cluster
			nodes := make([]Node, tc.clusterSize)
			loggers := make([]*SafeTestLogger, tc.clusterSize)
			stoppedNodes := make(map[int]bool)
			
			registry := &debugNodeRegistry{
				nodes:  make(map[int]RPCHandler),
				logger: newTestLogger(t),
			}

			peers := make([]int, tc.clusterSize)
			for i := 0; i < tc.clusterSize; i++ {
				peers[i] = i
			}

			for i := 0; i < tc.clusterSize; i++ {
				logger := NewSafeTestLogger(t)
				loggers[i] = logger
				
				config := &Config{
					ID:                 i,
					Peers:              peers,
					ElectionTimeoutMin: 150 * time.Millisecond,
					ElectionTimeoutMax: 300 * time.Millisecond,
					HeartbeatInterval:  50 * time.Millisecond,
					Logger:             logger,
					MaxLogSize:         1000, // Prevent premature snapshots
				}

				transport := &debugTransport{
					id:       i,
					registry: registry,
					logger:   newTestLogger(t),
				}

				stateMachine := &testStateMachine{
					mu:   sync.Mutex{},
					data: make(map[string]string),
				}

				node, err := NewNode(config, transport, nil, stateMachine)
				if err != nil {
					t.Fatalf("Failed to create node %d: %v", i, err)
				}

				nodes[i] = node
				registry.nodes[i] = node.(RPCHandler)
			}

			// Start all nodes
			ctx := context.Background()
			for i, node := range nodes {
				if err := node.Start(ctx); err != nil {
					t.Fatalf("Failed to start node %d: %v", i, err)
				}
				defer func(idx int) {
					if !stoppedNodes[idx] {
						loggers[idx].Stop()
						nodes[idx].Stop()
					}
				}(i)
			}

			// Wait for leader election
			time.Sleep(1 * time.Second)

			// Count leaders
			leaderCount := 0
			var leaderID int
			for i, node := range nodes {
				if node.IsLeader() {
					leaderCount++
					leaderID = i
				}
			}

			// Verify exactly one leader
			if leaderCount != 1 {
				t.Errorf("Expected exactly 1 leader, found %d", leaderCount)
			} else {
				t.Logf("Leader elected: node %d", leaderID)
			}

			// Test command submission
			if leaderCount == 1 {
				leader := nodes[leaderID]
				_, _, isLeader := leader.Submit("test-command")
				if !isLeader {
					t.Error("Leader rejected command")
				}
			}

			// Check quorum requirements
			if tc.clusterSize > 1 {
				// Stop minority of nodes
				stopCount := (tc.clusterSize - 1) / 2
				for i := 0; i < stopCount; i++ {
					if i != leaderID {
						loggers[i].Stop()
						nodes[i].Stop()
						stoppedNodes[i] = true
					}
				}

				// Leader should still function with majority
				time.Sleep(500 * time.Millisecond)

				if nodes[leaderID].IsLeader() {
					_, _, isLeader := nodes[leaderID].Submit("majority-test")
					if !isLeader {
						t.Error("Leader lost leadership with majority available")
					}
				}
			}
		})
	}
}

// TestLeaderHandlingDuringPartitionRecovery tests leader behavior during partition recovery
func TestLeaderHandlingDuringPartitionRecovery(t *testing.T) {
	// Create 5-node cluster with partitionable transport
	numNodes := 5
	nodes := make([]Node, numNodes)
	transports := make([]*partitionableTransport, numNodes)
	registry := &partitionRegistry{
		nodes: make(map[int]RPCHandler),
		mu:    sync.RWMutex{},
	}

	for i := 0; i < numNodes; i++ {
		config := &Config{
			ID:                 i,
			Peers:              []int{0, 1, 2, 3, 4},
			ElectionTimeoutMin: 150 * time.Millisecond,
			ElectionTimeoutMax: 300 * time.Millisecond,
			HeartbeatInterval:  50 * time.Millisecond,
		}

		transport := &partitionableTransport{
			id:       i,
			registry: registry,
			blocked:  make(map[int]bool),
		}
		transports[i] = transport

		stateMachine := &testStateMachine{
			mu:   sync.Mutex{},
			data: make(map[string]string),
		}

		node, err := NewNode(config, transport, nil, stateMachine)
		if err != nil {
			t.Fatalf("Failed to create node %d: %v", i, err)
		}

		nodes[i] = node
		registry.nodes[i] = node.(RPCHandler)
	}

	// Start all nodes
	ctx := context.Background()
	for i, node := range nodes {
		if err := node.Start(ctx); err != nil {
			t.Fatalf("Failed to start node %d: %v", i, err)
		}
		defer node.Stop()
	}

	// Wait for leader election
	time.Sleep(500 * time.Millisecond)

	// Find initial leader
	var leaderID int
	for i, node := range nodes {
		if node.IsLeader() {
			leaderID = i
			break
		}
	}

	t.Logf("Initial leader is node %d", leaderID)

	// Create partition: [0,1] | [2,3,4]
	group1 := []int{0, 1}
	group2 := []int{2, 3, 4}

	for _, n1 := range group1 {
		for _, n2 := range group2 {
			transports[n1].Block(n2)
			transports[n2].Block(n1)
		}
	}

	t.Log("Created partition: [0,1] | [2,3,4]")

	// Wait for new leaders in each partition
	time.Sleep(1 * time.Second)

	// Find leaders in each partition
	var leader1, leader2 int
	leader1 = -1
	leader2 = -1

	for _, i := range group1 {
		if nodes[i].IsLeader() {
			leader1 = i
			break
		}
	}

	for _, i := range group2 {
		if nodes[i].IsLeader() {
			leader2 = i
			break
		}
	}

	t.Logf("Partition leaders: group1=%d, group2=%d", leader1, leader2)

	// Submit different commands to each partition
	if leader1 >= 0 {
		for i := 0; i < 3; i++ {
			cmd := fmt.Sprintf("group1-cmd-%d", i)
			nodes[leader1].Submit(cmd)
		}
	}

	if leader2 >= 0 {
		for i := 0; i < 5; i++ {
			cmd := fmt.Sprintf("group2-cmd-%d", i)
			nodes[leader2].Submit(cmd)
		}
	}

	// Record terms before healing
	termsBeforeHeal := make([]int, numNodes)
	for i, node := range nodes {
		termsBeforeHeal[i] = node.GetCurrentTerm()
		t.Logf("Node %d term before heal: %d", i, termsBeforeHeal[i])
	}

	// Heal partition
	for i := 0; i < numNodes; i++ {
		for j := 0; j < numNodes; j++ {
			transports[i].Unblock(j)
		}
	}

	t.Log("Partition healed")

	// Wait for convergence
	time.Sleep(2 * time.Second)

	// Verify single leader emerges
	leaderCount := 0
	var finalLeaderID int
	for i, node := range nodes {
		if node.IsLeader() {
			leaderCount++
			finalLeaderID = i
		}
	}

	if leaderCount != 1 {
		t.Errorf("Expected exactly 1 leader after healing, found %d", leaderCount)
	} else {
		t.Logf("Final leader after healing: node %d", finalLeaderID)
	}

	// Check that final leader has highest term
	finalLeaderTerm := nodes[finalLeaderID].GetCurrentTerm()
	for i, node := range nodes {
		if node.GetCurrentTerm() > finalLeaderTerm {
			t.Errorf("Node %d has higher term than leader: %d > %d",
				i, node.GetCurrentTerm(), finalLeaderTerm)
		}
	}

	// Verify all nodes converged to same commit index
	commitIndices := make([]int, numNodes)
	for i, node := range nodes {
		commitIndices[i] = node.GetCommitIndex()
	}

	for i := 1; i < numNodes; i++ {
		if commitIndices[i] != commitIndices[0] {
			t.Errorf("Nodes have different commit indices: %v", commitIndices)
			break
		}
	}
}

// TestLeadershipTransferToNonVotingMember tests transfer to non-voting member fails
func TestLeadershipTransferToNonVotingMember(t *testing.T) {
	// Create 3-node cluster
	nodes := make([]Node, 3)
	registry := &debugNodeRegistry{
		nodes:  make(map[int]RPCHandler),
		logger: newTestLogger(t),
	}

	for i := 0; i < 3; i++ {
		config := &Config{
			ID:                 i,
			Peers:              []int{0, 1, 2},
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
			mu:   sync.Mutex{},
			data: make(map[string]string),
		}

		node, err := NewNode(config, transport, nil, stateMachine)
		if err != nil {
			t.Fatalf("Failed to create node %d: %v", i, err)
		}

		nodes[i] = node
		registry.nodes[i] = node.(RPCHandler)
	}

	// Start all nodes
	ctx := context.Background()
	for i, node := range nodes {
		if err := node.Start(ctx); err != nil {
			t.Fatalf("Failed to start node %d: %v", i, err)
		}
		defer node.Stop()
	}

	// Wait for leader election
	time.Sleep(500 * time.Millisecond)

	// Find leader
	var leader Node
	var leaderID int
	for i, node := range nodes {
		if node.IsLeader() {
			leader = node
			leaderID = i
			break
		}
	}

	if leader == nil {
		t.Fatal("No leader elected")
	}

	t.Logf("Leader is node %d", leaderID)

	// Add non-voting member
	err := leader.AddServer(3, "server-3:8003", false)
	if err != nil {
		t.Logf("Add non-voting server result: %v", err)
	}

	// Wait for configuration to propagate
	time.Sleep(500 * time.Millisecond)

	// Try to transfer leadership to non-voting member
	err = leader.TransferLeadership(3)
	if err == nil {
		t.Error("Transfer to non-voting member should fail")
	} else {
		t.Logf("Transfer correctly failed: %v", err)
	}

	// Verify leader hasn't changed
	if !leader.IsLeader() {
		t.Error("Leader stepped down after failed transfer")
	}

	// Try valid transfer to voting member
	targetID := (leaderID + 1) % 3
	err = leader.TransferLeadership(targetID)
	if err != nil {
		t.Logf("Valid transfer attempt: %v", err)
	}

	// Wait for transfer
	time.Sleep(1 * time.Second)

	// Check if transfer succeeded
	if nodes[targetID].IsLeader() {
		t.Logf("Leadership successfully transferred to node %d", targetID)
	} else if leader.IsLeader() {
		t.Log("Original leader retained leadership")
	} else {
		// Find new leader
		for i, node := range nodes {
			if node.IsLeader() {
				t.Logf("New leader is node %d", i)
				break
			}
		}
	}
}
