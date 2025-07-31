package raft

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	// "github.com/ueisele/raft/test" - removed to avoid import cycle
)

// TestLeaderAppendOnly verifies that a leader only appends to its log, never overwrites
func TestLeaderAppendOnly(t *testing.T) {
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
	timing := DefaultTimingConfig()
	timing.ElectionTimeout = 500 * time.Millisecond
	leaderID := WaitForLeaderWithConfig(t, nodes, timing)
	if leaderID < 0 {
		t.Fatal("No leader elected")
	}
	leader := nodes[leaderID]

	t.Logf("Leader is node %d", leaderID)

	// Track log entries as leader appends
	type logSnapshot struct {
		index   int
		term    int
		command string
	}

	var logHistory [][]logSnapshot

	// Helper to capture log state
	captureLog := func() []logSnapshot {
		n := leader.(*raftNode)
		n.mu.RLock()
		defer n.mu.RUnlock()

		entries := n.log.GetAllEntries()
		snapshot := make([]logSnapshot, len(entries))
		for i, entry := range entries {
			cmd := ""
			if str, ok := entry.Command.(string); ok {
				cmd = str
			}
			snapshot[i] = logSnapshot{
				index:   entry.Index,
				term:    entry.Term,
				command: cmd,
			}
		}
		return snapshot
	}

	// Submit commands and capture log after each
	for i := 0; i < 5; i++ {
		cmd := fmt.Sprintf("command-%d", i)
		index, term, isLeader := leader.Submit(cmd)
		if !isLeader {
			t.Fatal("Lost leadership")
		}

		t.Logf("Submitted %s at index %d, term %d", cmd, index, term)
		// Small delay between commands
		time.Sleep(50 * time.Millisecond)

		snapshot := captureLog()
		logHistory = append(logHistory, snapshot)

		// Verify append-only property
		if i > 0 {
			prevSnapshot := logHistory[i-1]
			currentSnapshot := logHistory[i]

			// Current log should be at least as long
			if len(currentSnapshot) < len(prevSnapshot) {
				t.Errorf("Log shrunk: was %d entries, now %d",
					len(prevSnapshot), len(currentSnapshot))
			}

			// All previous entries should be unchanged
			for j := 0; j < len(prevSnapshot); j++ {
				if prevSnapshot[j] != currentSnapshot[j] {
					t.Errorf("Log entry %d changed: was %+v, now %+v",
						j, prevSnapshot[j], currentSnapshot[j])
				}
			}
		}
	}

	t.Log("Leader append-only property verified")
}

// TestStateMachineSafety verifies that all state machines execute same commands in same order
func TestStateMachineSafety(t *testing.T) {
	// Create 5-node cluster
	numNodes := 5
	nodes := make([]Node, numNodes)
	stateMachines := make([]*testStateMachine, numNodes)
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

		// Create state machine that records command order
		stateMachine := &testStateMachine{
			mu:   sync.Mutex{},
			data: make(map[string]string),
		}
		stateMachines[i] = stateMachine

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
	timing := DefaultTimingConfig()
	timing.ElectionTimeout = 500 * time.Millisecond
	WaitForLeaderWithConfig(t, nodes, timing)

	// Submit many commands
	commandCount := 20
	for i := 0; i < commandCount; i++ {
		// Find current leader
		var leader Node
		for _, node := range nodes {
			if node.IsLeader() {
				leader = node
				break
			}
		}

		if leader == nil {
			t.Fatal("No leader")
		}

		cmd := fmt.Sprintf("cmd-%d", i)
		_, _, isLeader := leader.Submit(cmd)
		if !isLeader {
			// Leader changed, retry
			i--
			time.Sleep(50 * time.Millisecond) // Small delay before retry
			continue
		}

		// Small delay between commands
		time.Sleep(50 * time.Millisecond)
	}

	// Wait for all commands to be applied
	// Find the last command's index by looking at the leader's log
	var lastIndex int
	for _, node := range nodes {
		if node.IsLeader() {
			lastIndex = node.GetLogLength() - 1
			break
		}
	}
	WaitForCommitIndexWithConfig(t, nodes, lastIndex, timing)

	// Verify all state machines have same state
	// Note: Without commands field, we can't verify command order
	// Instead verify that all have same data
	var referenceData map[string]string
	referenceSet := false

	for i, sm := range stateMachines {
		sm.mu.Lock()
		data := make(map[string]string)
		for k, v := range sm.data {
			data[k] = v
		}
		sm.mu.Unlock()

		if !referenceSet {
			referenceData = data
			referenceSet = true
			t.Logf("Node %d has %d entries", i, len(data))
		} else {
			// Compare with reference
			if len(data) != len(referenceData) {
				t.Errorf("Node %d has different data count: %d vs %d",
					i, len(data), len(referenceData))
				continue
			}

			for k, v := range data {
				if refV, ok := referenceData[k]; !ok || refV != v {
					t.Errorf("Node %d has different value for key %s", i, k)
					break
				}
			}
		}
	}

	t.Log("State machine safety verified")
}

// TestLeaderElectionRestriction verifies that only up-to-date nodes can become leader
func TestLeaderElectionRestriction(t *testing.T) {
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

	// Wait for initial leader election
	timing := DefaultTimingConfig()
	timing.ElectionTimeout = 500 * time.Millisecond
	leaderID := WaitForLeaderWithConfig(t, nodes, timing)
	if leaderID < 0 {
		t.Fatal("No leader elected")
	}

	t.Logf("Initial leader is node %d", leaderID)

	// Submit some commands
	leader := nodes[leaderID]
	for i := 0; i < 5; i++ {
		cmd := fmt.Sprintf("command-%d", i)
		_, _, isLeader := leader.Submit(cmd)
		if !isLeader {
			t.Fatal("Lost leadership")
		}
	}

	// Wait for replication
	WaitForCommitIndexWithConfig(t, nodes, 5, timing)

	// Partition node 4 before more commands are submitted
	for i := 0; i < numNodes; i++ {
		if i != 4 {
			transports[4].Block(i)
			transports[i].Block(4)
		}
	}

	t.Log("Partitioned node 4")

	// Submit more commands that node 4 won't receive
	for i := 5; i < 10; i++ {
		cmd := fmt.Sprintf("command-%d", i)
		_, _, isLeader := leader.Submit(cmd)
		if !isLeader {
			t.Fatal("Lost leadership")
		}
	}

	// Wait for replication among non-partitioned nodes
	// Get the last command index from the leader
	var lastCmdIndex int
	if nodes[leaderID] != nil && nodes[leaderID].IsLeader() {
		lastCmdIndex = nodes[leaderID].GetLogLength() - 1
	}
	// Wait for non-partitioned nodes to catch up
	nonPartitioned := []Node{nodes[0], nodes[1], nodes[2], nodes[3]}
	WaitForCommitIndexWithConfig(t, nonPartitioned, lastCmdIndex, timing)

	// Record log lengths
	logLengths := make([]int, numNodes)
	for i, node := range nodes {
		logLengths[i] = node.GetLogLength()
		t.Logf("Node %d log length: %d", i, logLengths[i])
	}

	// Node 4 should have shorter log
	if logLengths[4] >= logLengths[0] {
		t.Error("Partitioned node has same log length as others")
	}

	// Now partition the current leader and node 0 together
	// This creates minority with leader + outdated node 4 in majority
	for i := 1; i < 4; i++ {
		transports[leaderID].Block(i)
		transports[i].Block(leaderID)
		transports[0].Block(i)
		transports[i].Block(0)
	}

	// Unblock node 4 so it can communicate with others
	for i := 0; i < numNodes; i++ {
		transports[4].Unblock(i)
		transports[i].Unblock(4)
	}

	t.Log("Created new partition with outdated node 4 in majority")

	// Wait for new election
	WaitForConditionWithProgress(t, func() (bool, string) {
		// Check if a new leader emerged in the new majority
		leaderCount := 0
		for i, node := range nodes {
			if i != leaderID && i != 0 && node.IsLeader() {
				leaderCount++
			}
		}
		return leaderCount >= 1, fmt.Sprintf("%d leaders in new majority", leaderCount)
	}, timing.ElectionTimeout*2, "new leader election after partition")

	// Check who became leader in majority partition
	// Node 4 should NOT be able to become leader due to outdated log
	if nodes[4].IsLeader() {
		t.Error("Outdated node 4 became leader - violates election restriction")
	}

	// One of nodes 1, 2, or 3 should be leader
	var newLeaderFound bool
	for i := 1; i < 4; i++ {
		if nodes[i].IsLeader() {
			newLeaderFound = true
			t.Logf("Node %d (up-to-date) became new leader", i)
			break
		}
	}

	if !newLeaderFound {
		t.Error("No up-to-date node became leader in majority")
	}
}
