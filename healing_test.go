package raft

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	// "github.com/ueisele/raft/test" - removed to avoid import cycle
)

// TestClusterHealing verifies that a cluster eventually converges after disruptions
func TestClusterHealing(t *testing.T) {
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

	t.Logf("Initial leader is node %d", leaderID)

	// Submit a command
	index1, term1, isLeader := leader.Submit("command-1")
	if !isLeader {
		t.Fatal("Not leader when submitting")
	}
	t.Logf("Submitted command-1 at index %d, term %d", index1, term1)

	// Wait for replication
	WaitForCommitIndexWithConfig(t, nodes, index1, timing)

	// Force leader to step down by stopping it
	t.Logf("Stopping leader node %d", leaderID)
	nodes[leaderID].Stop()

	// Wait for new leader election
	remainingNodes := []Node{}
	for i, node := range nodes {
		if i != leaderID {
			remainingNodes = append(remainingNodes, node)
		}
	}
	newLeaderID := WaitForLeaderWithConfig(t, remainingNodes, timing)

	// Find new leader by ID
	var newLeader Node
	if newLeaderID >= 0 && newLeaderID < len(nodes) {
		newLeader = nodes[newLeaderID]
	}

	if newLeader == nil {
		t.Fatal("No new leader elected")
	}

	t.Logf("New leader is node %d", newLeaderID)

	// Submit another command with new leader
	index2, term2, isLeader := newLeader.Submit("command-2")
	if !isLeader {
		t.Fatal("Not leader when submitting")
	}
	t.Logf("Submitted command-2 at index %d, term %d", index2, term2)

	// Wait for cluster to stabilize
	t.Log("Waiting for cluster to heal...")
	
	// Check convergence
	maxRetries := 20
	for retry := 0; retry < maxRetries; retry++ {
		time.Sleep(100 * time.Millisecond) // Small delay between checks
		
		// Get state of remaining nodes
		states := make(map[int]string)
		for i, node := range nodes {
			if i == leaderID {
				continue // Skip stopped node
			}
			commitIndex := node.GetCommitIndex()
			logLength := node.GetLogLength()
			states[i] = fmt.Sprintf("commit=%d,len=%d", commitIndex, logLength)
		}
		
		// Check if both remaining nodes have converged
		converged := true
		var firstState string
		for _, state := range states {
			if firstState == "" {
				firstState = state
			} else if state != firstState {
				converged = false
				break
			}
		}
		
		if converged {
			t.Logf("SUCCESS: Cluster converged after %d retries (%.1f seconds): %v", 
				retry, float64(retry)*0.5, states)
			
			// Verify they have both commands
			for i, node := range nodes {
				if i == leaderID {
					continue
				}
				if node.GetCommitIndex() < index2 {
					t.Errorf("Node %d hasn't committed all entries: commit=%d, expected>=%d",
						i, node.GetCommitIndex(), index2)
				}
			}
			return
		}
		
		if retry > 0 && retry % 5 == 0 {
			t.Logf("Retry %d: States: %v", retry, states)
		}
	}
	
	t.Fatal("Cluster failed to converge after timeout")
}

// TestClusterHealingWithUncommittedEntry tests healing when there's an uncommitted entry
func TestClusterHealingWithUncommittedEntry(t *testing.T) {
	// Create 3-node cluster
	nodes := make([]Node, 3)
	transports := make([]*partitionableTransport, 3)
	registry := &partitionRegistry{
		nodes: make(map[int]RPCHandler),
		mu:    sync.RWMutex{},
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
	timing := DefaultTimingConfig()
	timing.ElectionTimeout = 500 * time.Millisecond
	leaderID := WaitForLeaderWithConfig(t, nodes, timing)
	if leaderID < 0 {
		t.Fatal("No leader elected")
	}
	leader := nodes[leaderID]

	t.Logf("Initial leader is node %d", leaderID)

	// Partition the leader from one follower before submitting
	followerToPartition := (leaderID + 1) % 3
	t.Logf("Partitioning leader %d from follower %d", leaderID, followerToPartition)
	transports[leaderID].Block(followerToPartition)
	transports[followerToPartition].Block(leaderID)

	// Submit a command - it will replicate to only one follower
	index, term, isLeader := leader.Submit("partially-replicated-command")
	if !isLeader {
		t.Fatal("Not leader when submitting")
	}
	t.Logf("Submitted command at index %d, term %d (only replicated to one follower)", index, term)

	// Wait a bit for partial replication
	time.Sleep(50 * time.Millisecond) // Short delay to allow partial replication

	// Now partition the leader from everyone
	for i := 0; i < 3; i++ {
		if i != leaderID {
			transports[leaderID].Block(i)
			transports[i].Block(leaderID)
		}
	}
	t.Logf("Fully isolated leader %d", leaderID)

	// Wait for new leader election among remaining nodes
	Eventually(t, func() bool {
		for i, node := range nodes {
			if i != leaderID && node.IsLeader() {
				return true
			}
		}
		return false
	}, 1*time.Second, "new leader election among remaining nodes")

	// Heal all partitions
	t.Log("Healing all partitions...")
	for i := 0; i < 3; i++ {
		for j := 0; j < 3; j++ {
			transports[i].Unblock(j)
		}
	}

	// The cluster should eventually converge
	// The uncommitted entry from the old leader should either be:
	// 1. Committed if the new leader has it
	// 2. Overwritten if the new leader doesn't have it

	t.Log("Waiting for cluster to heal after partition...")
	
	maxRetries := 30
	for retry := 0; retry < maxRetries; retry++ {
		time.Sleep(100 * time.Millisecond) // Small delay between checks
		
		// Get state of all nodes
		states := make(map[int]string)
		commitIndices := make([]int, 3)
		for i, node := range nodes {
			commitIndex := node.GetCommitIndex()
			logLength := node.GetLogLength()
			states[i] = fmt.Sprintf("commit=%d,len=%d", commitIndex, logLength)
			commitIndices[i] = commitIndex
		}
		
		// Check if all nodes have converged
		converged := true
		firstState := states[0]
		for i := 1; i < 3; i++ {
			if states[i] != firstState {
				converged = false
				break
			}
		}
		
		if converged {
			t.Logf("SUCCESS: Cluster converged after %d retries (%.1f seconds): %v", 
				retry, float64(retry)*0.5, states)
			t.Log("The cluster successfully healed after the partition!")
			return
		}
		
		if retry > 0 && retry % 5 == 0 {
			// Find current leader
			var currentLeader *int
			for i, node := range nodes {
				if node.IsLeader() {
					currentLeader = &i
					break
				}
			}
			
			if currentLeader != nil {
				t.Logf("Retry %d: States: %v, leader: node%d", retry, states, *currentLeader)
			} else {
				t.Logf("Retry %d: States: %v, NO LEADER", retry, states)
			}
		}
	}
	
	t.Fatal("Cluster failed to converge after timeout")
}

// TestClusterEventualHealing tests if a cluster heals without intervention
func TestClusterEventualHealing(t *testing.T) {
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

	t.Logf("Initial leader is node %d", leaderID)

	// Submit 3 commands
	var indices []int
	for i := 0; i < 3; i++ {
		index, _, isLeader := leader.Submit(fmt.Sprintf("command-%d", i+1))
		if !isLeader {
			t.Fatal("Not leader when submitting")
		}
		indices = append(indices, index)
		t.Logf("Submitted command-%d at index %d", i+1, index)
		time.Sleep(10 * time.Millisecond) // Small delay between commands for ordering
	}

	// Wait for replication
	if len(indices) > 0 {
		WaitForCommitIndexWithConfig(t, nodes, indices[len(indices)-1], timing)
	}

	// Now cause leadership changes by stopping the leader
	t.Logf("Stopping leader node %d", leaderID)
	nodes[leaderID].Stop()

	// Wait for new leader
	var newLeader Node
	var newLeaderID int
	WaitForConditionWithProgress(t, func() (bool, string) {
		for i, node := range nodes {
			if i != leaderID && node.IsLeader() {
				newLeader = node
				newLeaderID = i
				return true, fmt.Sprintf("new leader is node %d", i)
			}
		}
		return false, "waiting for new leader"
	}, timing.ElectionTimeout*2, "new leader election")

	if newLeader == nil {
		t.Fatal("No new leader elected")
	}

	t.Logf("New leader is node %d", newLeaderID)

	// Submit a command with new leader to try to force progress
	finalIndex, _, isLeader := newLeader.Submit("final-command")
	if !isLeader {
		t.Fatal("Not leader when submitting")
	}
	t.Logf("Submitted final-command at index %d", finalIndex)

	// Now wait and see if all nodes converge WITHOUT any intervention
	t.Log("Waiting for cluster to heal naturally...")

	var lastLogState string
	stuckCount := 0
	lastProgressTime := time.Now()

	WaitForConditionWithProgress(t, func() (bool, string) {

		// Get state of remaining nodes
		states := make(map[int]string)
		commitIndices := make(map[int]int)
		logLengths := make(map[int]int)

		for i, node := range nodes {
			if i == leaderID {
				continue // Skip stopped node
			}
			commitIndex := node.GetCommitIndex()
			logLength := node.GetLogLength()
			states[i] = fmt.Sprintf("commit=%d,len=%d", commitIndex, logLength)
			commitIndices[i] = commitIndex
			logLengths[i] = logLength
		}

		// Check if converged
		converged := true
		var firstState string
		for _, state := range states {
			if firstState == "" {
				firstState = state
			} else if state != firstState {
				converged = false
				break
			}
		}

		// Check if all have the final command
		allHaveFinal := true
		for _, commitIdx := range commitIndices {
			if commitIdx < finalIndex {
				allHaveFinal = false
				break
			}
		}

		currentLogState := fmt.Sprintf("%v", states)
		if currentLogState == lastLogState {
			stuckCount++
		} else {
			stuckCount = 0
			lastProgressTime = time.Now()
		}
		lastLogState = currentLogState

		// Log progress periodically
		if time.Since(lastProgressTime) > 5*time.Second {
			// Check who is leader
			var currentLeaderID *int
			for i, node := range nodes {
				if i != leaderID && node.IsLeader() {
					id := i
					currentLeaderID = &id
					break
				}
			}
			if currentLeaderID != nil {
				// Leader info already logged in status
			}
			lastProgressTime = time.Now()
		}

		if converged && allHaveFinal {
			return true, fmt.Sprintf("cluster converged with all nodes having final command")
		}

		return false, fmt.Sprintf("states: %v, converged: %v, allHaveFinal: %v", states, converged, allHaveFinal)
	}, 30*time.Second, "cluster natural healing")

	t.Log("SUCCESS: Cluster converged naturally")
}
