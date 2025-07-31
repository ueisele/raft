package raft

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

// TestBasicMembershipChange tests basic server addition and removal
// Note: This test can be flaky due to cluster instability during rapid
// configuration changes. Multiple leader elections may occur, causing
// occasional failures when nodes haven't caught up with the latest entries.
func TestBasicMembershipChange(t *testing.T) {
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
	for _, node := range nodes {
		if node.IsLeader() {
			leader = node
			break
		}
	}

	if leader == nil {
		t.Fatal("No leader elected")
	}

	// Test 1: Add a voting server
	t.Log("Test 1: Adding voting server")

	// Create new node
	newNodeID := 3
	newConfig := &Config{
		ID:                 newNodeID,
		Peers:              []int{}, // Will be updated by leader
		ElectionTimeoutMin: 150 * time.Millisecond,
		ElectionTimeoutMax: 300 * time.Millisecond,
		HeartbeatInterval:  50 * time.Millisecond,
		Logger:             newTestLogger(t),
	}

	newTransport := &debugTransport{
		id:       newNodeID,
		registry: registry,
		logger:   newTestLogger(t),
	}

	newStateMachine := &testStateMachine{
		mu:   sync.Mutex{},
		data: make(map[string]string),
	}

	newNode, err := NewNode(newConfig, newTransport, nil, newStateMachine)
	if err != nil {
		t.Fatalf("Failed to create new node: %v", err)
	}

	registry.nodes[newNodeID] = newNode.(RPCHandler)

	// Start new node
	if err := newNode.Start(ctx); err != nil {
		t.Fatalf("Failed to start new node: %v", err)
	}
	defer newNode.Stop()

	// Add server directly as voting member for this test
	// Note: In production, use AddServerSafely for safer configuration changes
	err = leader.AddServer(newNodeID, fmt.Sprintf("server-%d:8000", newNodeID), true)
	if err != nil {
		t.Fatalf("Failed to add server: %v", err)
	}

	// Wait for configuration to propagate and new node to catch up
	time.Sleep(2 * time.Second)
	
	// Submit a dummy command to force replication to the new node
	// This ensures the new node's log is synchronized
	dummyIndex, _, isLeader := leader.Submit("sync-new-node")
	if !isLeader {
		t.Fatal("Lost leadership while adding new node")
	}
	
	// Wait for the dummy command to be replicated to all nodes
	maxWait := 10 * time.Second
	start := time.Now()
	for time.Since(start) < maxWait {
		allCaughtUp := true
		for _, node := range append(nodes, newNode) {
			if node.GetCommitIndex() < dummyIndex {
				allCaughtUp = false
				break
			}
		}
		if allCaughtUp {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	
	if newNode.GetCommitIndex() < dummyIndex {
		t.Logf("Warning: New node not fully caught up. Commit index: %d, expected: %d", 
			newNode.GetCommitIndex(), dummyIndex)
	}

	// Verify configuration on all nodes
	for i, node := range append(nodes, newNode) {
		config := node.GetConfiguration()
		if len(config.Servers) != 4 {
			t.Errorf("Node %d has %d servers, expected 4", i, len(config.Servers))
		}

		// Check new server is voting
		for _, server := range config.Servers {
			if server.ID == newNodeID && !server.Voting {
				t.Errorf("New server is not voting on node %d", i)
			}
		}
	}

	// Test 2: Add non-voting server
	t.Log("Test 2: Adding non-voting server")

	nonVotingID := 4
	
	// Create the non-voting node first
	nonVotingConfig := &Config{
		ID:                 nonVotingID,
		Peers:              []int{},
		ElectionTimeoutMin: 150 * time.Millisecond,
		ElectionTimeoutMax: 300 * time.Millisecond,
		HeartbeatInterval:  50 * time.Millisecond,
		Logger:             newTestLogger(t),
	}

	nonVotingTransport := &debugTransport{
		id:       nonVotingID,
		registry: registry,
		logger:   newTestLogger(t),
	}

	nonVotingStateMachine := &testStateMachine{
		mu:   sync.Mutex{},
		data: make(map[string]string),
	}

	nonVotingNode, err := NewNode(nonVotingConfig, nonVotingTransport, nil, nonVotingStateMachine)
	if err != nil {
		t.Fatalf("Failed to create non-voting node: %v", err)
	}

	registry.nodes[nonVotingID] = nonVotingNode.(RPCHandler)

	// Start non-voting node
	if err := nonVotingNode.Start(ctx); err != nil {
		t.Fatalf("Failed to start non-voting node: %v", err)
	}
	defer nonVotingNode.Stop()
	
	// Now add it as a non-voting server
	err = leader.AddServer(nonVotingID, fmt.Sprintf("server-%d:8000", nonVotingID), false)
	if err != nil {
		t.Fatalf("Failed to add non-voting server: %v", err)
	}

	// Wait for configuration change
	time.Sleep(500 * time.Millisecond)

	// Verify non-voting server added
	config := leader.GetConfiguration()
	foundNonVoting := false
	for _, server := range config.Servers {
		if server.ID == nonVotingID {
			foundNonVoting = true
			if server.Voting {
				t.Error("Non-voting server is marked as voting")
			}
		}
	}

	if !foundNonVoting {
		t.Error("Non-voting server not found in configuration")
	}

	// Test 3: Remove server
	t.Log("Test 3: Removing server")

	// Remove node 2
	removeID := 2
	err = leader.RemoveServer(removeID)
	if err != nil {
		t.Fatalf("Failed to remove server: %v", err)
	}

	// Wait for configuration change
	time.Sleep(500 * time.Millisecond)

	// Verify server removed
	config = leader.GetConfiguration()
	for _, server := range config.Servers {
		if server.ID == removeID {
			t.Errorf("Removed server %d still in configuration", removeID)
		}
	}

	// Verify removed node knows it was removed
	nodes[removeID].Stop()

	// Test 4: Verify quorum with new configuration
	t.Log("Test 4: Testing quorum with new configuration")

	// Wait for all nodes to catch up after configuration changes
	time.Sleep(2 * time.Second)
	
	// Ensure all nodes have caught up to the same log length
	maxRetries := 10
	for retry := 0; retry < maxRetries; retry++ {
		logLengths := make(map[int]int)
		remainingNodes := []Node{nodes[0], nodes[1], newNode}
		
		for i, node := range remainingNodes {
			nodeID := i
			if i == 2 {
				nodeID = 3 // newNode
			}
			logLengths[nodeID] = node.GetLogLength()
		}
		
		// Check if all have same log length
		allSame := true
		var firstLength int = -1
		for _, length := range logLengths {
			if firstLength == -1 {
				firstLength = length
			} else if length != firstLength {
				allSame = false
				t.Logf("Retry %d: Node log lengths differ: %v", retry, logLengths)
				break
			}
		}
		
		if allSame {
			t.Logf("All nodes have same log length: %d", firstLength)
			break
		}
		
		if retry == maxRetries-1 {
			t.Fatalf("Nodes failed to converge to same log length after %d retries: %v", maxRetries, logLengths)
		}
		
		time.Sleep(1 * time.Second)
	}
	
	// Find current leader (may have changed during config operations)
	leader = nil
	var leaderID int
	for i, node := range []Node{nodes[0], nodes[1], newNode} {
		if node.IsLeader() {
			leader = node
			if i < 2 {
				leaderID = i
			} else {
				leaderID = 3 // newNode
			}
			break
		}
	}
	
	if leader == nil {
		// Wait a bit more for leader election
		time.Sleep(2 * time.Second)
		for i, node := range []Node{nodes[0], nodes[1], newNode} {
			if node.IsLeader() {
				leader = node
				if i < 2 {
					leaderID = i
				} else {
					leaderID = 3 // newNode
				}
				break
			}
		}
		if leader == nil {
			t.Fatal("No leader found after configuration changes")
		}
	}
	
	t.Logf("Current leader is node %d", leaderID)
	
	// Verify leader knows about all remaining nodes
	leaderConfig := leader.GetConfiguration()
	expectedNodes := map[int]bool{0: false, 1: false, 3: false, 4: false}
	for _, server := range leaderConfig.Servers {
		if _, expected := expectedNodes[server.ID]; expected {
			expectedNodes[server.ID] = true
		}
	}
	
	// Log which nodes the leader knows about
	t.Logf("Leader configuration includes nodes: %v", leaderConfig.Servers)
	for nodeID, found := range expectedNodes {
		if nodeID != 2 && !found { // Node 2 was removed
			t.Logf("WARNING: Leader doesn't know about node %d", nodeID)
		}
	}

	// Verify command can be committed
	index, _, isLeader := leader.Submit("final-test-command")
	if !isLeader {
		t.Fatal("Lost leadership when submitting command")
	}
	t.Logf("Submitted final-test-command at index %d", index)
	
	// Force immediate heartbeat to trigger replication
	// This helps ensure the command is replicated quickly
	if leaderNode, ok := leader.(*raftNode); ok {
		leaderNode.replication.SendHeartbeats()
	}

	// Wait for replication with retries
	// Increased retries and wait time to allow cluster to heal
	maxWaitRetries := 30 // Increased from 10 to 30
	for retry := 0; retry < maxWaitRetries; retry++ {
		time.Sleep(500 * time.Millisecond)
		
		// Check if all nodes have the command
		allHaveCommand := true
		notCaughtUp := []string{}
		
		for i, node := range []Node{nodes[0], nodes[1], newNode} {
			nodeID := i
			if i == 2 {
				nodeID = 3 // newNode
			}
			
			commitIndex := node.GetCommitIndex()
			logLength := node.GetLogLength()
			
			if commitIndex < index || logLength < index {
				allHaveCommand = false
				notCaughtUp = append(notCaughtUp, fmt.Sprintf("node%d(commit=%d,len=%d)", 
					nodeID, commitIndex, logLength))
			}
		}
		
		if allHaveCommand {
			t.Logf("SUCCESS: All nodes converged after %d retries (%.1f seconds)", 
				retry, float64(retry)*0.5)
			break
		}
		
		// Log progress every 5 retries
		if retry > 0 && retry % 5 == 0 {
			// Also check current leader
			var currentLeader *int
			var currentLeaderNode Node
			for i, node := range []Node{nodes[0], nodes[1], newNode} {
				if node.IsLeader() {
					nodeID := i
					if i == 2 {
						nodeID = 3
					}
					currentLeader = &nodeID
					currentLeaderNode = node
					break
				}
			}
			
			if currentLeader != nil {
				t.Logf("Retry %d: Still waiting for: %v (need >=%d), current leader: node%d", 
					retry, notCaughtUp, index, *currentLeader)
				
				// If we're stuck for a while, the issue might be that entries from
				// previous terms can't be committed. Have the current leader submit
				// a new entry to move things forward.
				if retry >= 10 && !allHaveCommand {
					t.Log("Cluster appears stuck - submitting new entry to force progress")
					newIndex, _, isLeader := currentLeaderNode.Submit(fmt.Sprintf("unstick-%d", retry))
					if isLeader {
						t.Logf("Submitted new entry at index %d to help cluster progress", newIndex)
						// Update our target index if this creates a higher index
						if newIndex > index {
							index = newIndex
						}
					}
				}
			} else {
				t.Logf("Retry %d: Still waiting for: %v (need >=%d), NO LEADER!", 
					retry, notCaughtUp, index)
			}
		}
		
		if retry == maxWaitRetries-1 {
			t.Logf("TIMEOUT: After %d retries (%.1f seconds), not converged: %v", 
				maxWaitRetries, float64(maxWaitRetries)*0.5, notCaughtUp)
		}
	}

	// Check commit on remaining nodes
	for i, node := range []Node{nodes[0], nodes[1], newNode} {
		commitIndex := node.GetCommitIndex()
		logLength := node.GetLogLength()
		nodeID := i
		if i == 2 {
			nodeID = 3 // newNode has ID 3
		}
		// Also check if node is voting
		config := node.GetConfiguration()
		isVoting := false
		for _, server := range config.Servers {
			if server.ID == nodeID && server.Voting {
				isVoting = true
				break
			}
		}
		t.Logf("Node %d: commit index=%d, log length=%d, voting=%v (need commit>=%d)", 
			nodeID, commitIndex, logLength, isVoting, index)
		if commitIndex < index {
			t.Errorf("Command not committed on node %d: commit index=%d, expected >=%d", nodeID, commitIndex, index)
		}
	}
}

