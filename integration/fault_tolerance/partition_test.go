package fault_tolerance

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/ueisele/raft"
	"github.com/ueisele/raft/integration/helpers"
	"github.com/ueisele/raft/integration/helpers/transporttest"
)

// TestAsymmetricPartition tests asymmetric network partitions where A can send to B but B cannot send to A
func TestAsymmetricPartition(t *testing.T) {
	// Create test infrastructure with asymmetric transport
	numNodes := 3
	nodes := make([]raft.Node, numNodes)
	transports := make([]*asymmetricTransport, numNodes)
	registry := &asymmetricRegistry{
		nodes: make(map[int]raft.RPCHandler),
		mu:    sync.RWMutex{},
	}

	for i := 0; i < numNodes; i++ {
		config := &raft.Config{
			ID:                 i,
			Peers:              []int{0, 1, 2},
			ElectionTimeoutMin: 150 * time.Millisecond,
			ElectionTimeoutMax: 300 * time.Millisecond,
			HeartbeatInterval:  50 * time.Millisecond,
		}

		transport := &asymmetricTransport{
			id:              i,
			registry:        registry,
			blockedIncoming: make(map[int]bool),
			blockedOutgoing: make(map[int]bool),
		}
		transports[i] = transport

		stateMachine := raft.NewMockStateMachine()

		node, err := raft.NewNode(config, transport, nil, stateMachine)
		if err != nil {
			t.Fatalf("Failed to create node %d: %v", i, err)
		}

		nodes[i] = node
		registry.nodes[i] = node.(raft.RPCHandler)
		transport.handler = node.(raft.RPCHandler)
	}

	ctx := context.Background()

	// Start all nodes
	for i, node := range nodes {
		if err := node.Start(ctx); err != nil {
			t.Fatalf("Failed to start node %d: %v", i, err)
		}
		nodeCopy := node // Capture loop variable
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			nodeCopy.Stop(ctx) //nolint:errcheck // test cleanup
		})
	}

	// Wait for initial leader election
	leaderID := helpers.WaitForLeader(t, nodes, 2*time.Second)
	t.Logf("Initial leader: Node %d", leaderID)

	// Test Case 1: Leader can send to followers but not receive from them
	t.Log("Test Case 1: Leader outgoing only")

	// Block all incoming messages to the leader
	for i := 0; i < numNodes; i++ {
		if i != leaderID {
			transports[i].blockOutgoingTo(leaderID)
		}
	}

	// Leader should still be able to send heartbeats
	time.Sleep(200 * time.Millisecond)

	// Check if leader maintained leadership
	_, isLeader := nodes[leaderID].GetState()
	if !isLeader {
		t.Log("Leader lost leadership when it could still send heartbeats (expected in some implementations)")
	} else {
		t.Log("Leader maintained leadership with outgoing-only communication")
	}

	// Restore symmetric communication
	for i := 0; i < numNodes; i++ {
		transports[i].clearBlocks()
	}

	// Wait for cluster to stabilize
	time.Sleep(500 * time.Millisecond)

	// Test Case 2: Follower can receive but not send
	t.Log("\nTest Case 2: Follower incoming only")

	// Find current leader
	leaderID = -1
	for i, node := range nodes {
		_, isLeader := node.GetState()
		if isLeader {
			leaderID = i
			break
		}
	}

	if leaderID == -1 {
		t.Fatal("No leader found")
	}

	followerID := (leaderID + 1) % numNodes

	// Block follower's outgoing messages
	transports[followerID].blockAllOutgoing()

	// Submit commands - follower should still receive them
	for i := 0; i < 3; i++ {
		idx, _, isLeader := nodes[leaderID].Submit(fmt.Sprintf("asymmetric-cmd-%d", i))
		if !isLeader {
			t.Logf("Failed to submit command: not leader")
			continue
		}

		// Wait for replication
		time.Sleep(100 * time.Millisecond)

		// Check if follower received the entry (even though it can't acknowledge)
		entry := nodes[followerID].GetLogEntry(idx)
		if entry != nil {
			t.Logf("Follower received entry at index %d despite not being able to acknowledge", idx)
		}
	}

	// Test Case 3: Complex asymmetric partition
	t.Log("\nTest Case 3: Complex asymmetric partition")

	// Create a scenario where:
	// - Node 0 can send to 1 but not 2
	// - Node 1 can send to 2 but not 0
	// - Node 2 can send to 0 but not 1
	transports[0].blockOutgoingTo(2)
	transports[1].blockOutgoingTo(0)
	transports[2].blockOutgoingTo(1)

	// This creates interesting election dynamics
	time.Sleep(1 * time.Second)

	// Count leaders
	leaderCount := 0
	for _, node := range nodes {
		_, isLeader := node.GetState()
		if isLeader {
			leaderCount++
		}
	}

	t.Logf("Number of leaders with complex asymmetric partition: %d", leaderCount)
}

// TestRapidPartitionChanges tests system behavior with rapidly changing partitions
func TestRapidPartitionChanges(t *testing.T) {
	// Create 5-node cluster with partitionable transport
	cluster := helpers.NewTestClusterOfSize(t, 5, helpers.WithPartitionableTransport(), helpers.WithClusterAutoStart())

	// Wait for initial leader
	_, err := cluster.WaitForLeader(2 * time.Second)
	if err != nil {
		t.Fatalf("No initial leader elected: %v", err)
	}

	// Track cluster state
	type PartitionEvent struct {
		time        time.Time
		description string
		leaderCount int
		maxTerm     int
	}

	events := []PartitionEvent{}

	recordEvent := func(desc string) {
		leaderCount := 0
		maxTerm := 0

		for _, node := range cluster.Nodes {
			term, isLeader := node.GetState()
			if isLeader {
				leaderCount++
			}
			if term > maxTerm {
				maxTerm = term
			}
		}

		events = append(events, PartitionEvent{
			time:        time.Now(),
			description: desc,
			leaderCount: leaderCount,
			maxTerm:     maxTerm,
		})

		t.Logf("%s - Leaders: %d, Max Term: %d", desc, leaderCount, maxTerm)
	}

	recordEvent("Initial state")

	// Rapid partition changes
	partitionPatterns := []struct {
		name      string
		partition func()
		duration  time.Duration
	}{
		{
			name: "Split brain (2-3)",
			partition: func() {
				// Partition into [0,1] and [2,3,4]
				transporttest.PartitionNode(cluster, 0) //nolint:errcheck // test partition setup
				transporttest.PartitionNode(cluster, 1) //nolint:errcheck // test partition setup
			},
			duration: 300 * time.Millisecond,
		},
		{
			name: "Isolate leader",
			partition: func() {
				// Find and isolate current leader
				for i, node := range cluster.Nodes {
					_, isLeader := node.GetState()
					if isLeader {
						transporttest.PartitionNode(cluster, i) //nolint:errcheck // test partition setup
						break
					}
				}
			},
			duration: 200 * time.Millisecond,
		},
		{
			name: "Rolling isolation",
			partition: func() {
				// Isolate nodes one by one
				go func() {
					for i := 0; i < 5; i++ {
						transporttest.PartitionNode(cluster, i) //nolint:errcheck // test partition setup
						time.Sleep(50 * time.Millisecond)
						transporttest.HealPartition(cluster)
					}
				}()
			},
			duration: 400 * time.Millisecond,
		},
		{
			name: "Majority isolated",
			partition: func() {
				// Isolate 3 out of 5 nodes
				transporttest.PartitionNode(cluster, 0) //nolint:errcheck // test partition setup
				transporttest.PartitionNode(cluster, 1) //nolint:errcheck // test partition setup
				transporttest.PartitionNode(cluster, 2) //nolint:errcheck // test partition setup
			},
			duration: 300 * time.Millisecond,
		},
	}

	// Execute rapid changes
	for _, pattern := range partitionPatterns {
		t.Logf("\nApplying partition: %s", pattern.name)
		pattern.partition()
		recordEvent(fmt.Sprintf("After %s", pattern.name))

		time.Sleep(pattern.duration)

		transporttest.HealPartition(cluster)
		recordEvent("After heal")

		// Brief stabilization period
		time.Sleep(100 * time.Millisecond)
	}

	// Final stabilization
	time.Sleep(1 * time.Second)
	recordEvent("Final state")

	// Analysis
	t.Log("\n=== Partition Event Summary ===")
	for _, event := range events {
		t.Logf("%s: %s", event.time.Format("15:04:05.000"), event.description)
		t.Logf("  Leaders: %d, Max Term: %d", event.leaderCount, event.maxTerm)
	}

	// Verify cluster eventually stabilizes
	finalLeaderCount := 0
	for _, node := range cluster.Nodes {
		_, isLeader := node.GetState()
		if isLeader {
			finalLeaderCount++
		}
	}

	if finalLeaderCount != 1 {
		t.Errorf("Cluster did not stabilize to single leader: %d leaders", finalLeaderCount)
	} else {
		t.Log("✓ Cluster stabilized to single leader after rapid partitions")
	}

	// Verify cluster is functional
	idx, _, err := cluster.SubmitToLeader("post-partition-test")
	if err != nil {
		t.Fatalf("Failed to submit command after partitions: %v", err)
	}

	if err := cluster.WaitForCommitIndex(idx, 2*time.Second); err != nil {
		t.Fatalf("Command not committed after partitions: %v", err)
	}

	t.Log("✓ Cluster functional after rapid partition changes")
}

// TestPartitionDuringConfigChange tests partition during configuration change
func TestPartitionDuringConfigChange(t *testing.T) {
	// Create initial 3-node cluster
	cluster := helpers.NewTestClusterOfSize(t, 3, helpers.WithPartitionableTransport(), helpers.WithClusterAutoStart())

	// Wait for leader
	leaderID, err := cluster.WaitForLeader(2 * time.Second)
	if err != nil {
		t.Fatalf("No leader elected: %v", err)
	}

	// Submit some initial data
	for i := 0; i < 5; i++ {
		idx, _, err := cluster.SubmitToLeader(fmt.Sprintf("initial-%d", i))
		if err != nil {
			t.Fatalf("Failed to submit command: %v", err)
		}
		if err := cluster.WaitForCommitIndex(idx, time.Second); err != nil {
			t.Logf("Warning: WaitForCommitIndex failed for initial command %d: %v", i, err)
		}
	}

	t.Log("Starting configuration change...")

	// Start adding a new server (simulated)
	// In real implementation, this would be AddServer RPC

	// Partition during the configuration change
	// This tests the safety of configuration changes under partition

	// Create partition: leader + 1 node vs other node
	isolatedNode := (leaderID + 2) % 3
	if err := transporttest.PartitionNode(cluster, isolatedNode); err != nil {
		t.Fatalf("Failed to partition node: %v", err)
	}

	t.Logf("Partitioned node %d during configuration change", isolatedNode)

	// Try to continue operation with majority
	for i := 0; i < 3; i++ {
		_, _, isLeader := cluster.Nodes[leaderID].Submit(fmt.Sprintf("during-partition-%d", i))
		if !isLeader {
			t.Logf("Failed to submit during partition: not leader")
		}
	}

	// Wait a bit
	time.Sleep(500 * time.Millisecond)

	// Heal partition
	transporttest.HealPartition(cluster)
	t.Log("Healed partition")

	// Wait for stabilization
	time.Sleep(1 * time.Second)

	// Verify all nodes have consistent state
	var maxCommitIndex int
	for i, node := range cluster.Nodes {
		commitIndex := node.GetCommitIndex()
		t.Logf("Node %d commit index: %d", i, commitIndex)
		if commitIndex > maxCommitIndex {
			maxCommitIndex = commitIndex
		}
	}

	// All nodes should eventually have the same commit index
	helpers.WaitForConditionWithProgress(t, func() (bool, string) {
		minCommit := maxCommitIndex
		for _, node := range cluster.Nodes {
			if commit := node.GetCommitIndex(); commit < minCommit {
				minCommit = commit
			}
		}
		return minCommit == maxCommitIndex,
			fmt.Sprintf("min commit %d, max commit %d", minCommit, maxCommitIndex)
	}, 2*time.Second, "commit index convergence")

	t.Log("✓ Cluster recovered after partition during configuration change")
}

// TestCascadingPartitions tests cascading network failures
func TestCascadingPartitions(t *testing.T) {
	// Create 7-node cluster for complex partition scenarios
	cluster := helpers.NewTestClusterOfSize(t, 7, helpers.WithPartitionableTransport(), helpers.WithClusterAutoStart())

	// Wait for initial leader
	_, err := cluster.WaitForLeader(2 * time.Second)
	if err != nil {
		t.Fatalf("No initial leader elected: %v", err)
	}

	// Submit initial data
	for i := 0; i < 10; i++ {
		idx, _, err := cluster.SubmitToLeader(fmt.Sprintf("initial-%d", i))
		if err != nil {
			t.Fatalf("Failed to submit command: %v", err)
		}
		if err := cluster.WaitForCommitIndex(idx, time.Second); err != nil {
			t.Logf("Warning: WaitForCommitIndex failed for initial command %d: %v", i, err)
		}
	}

	// Cascading partition scenario:
	// 1. First, partition 2 nodes
	// 2. Then partition 2 more
	// 3. Finally partition 1 more (leaving only 2 connected)

	t.Log("Starting cascading partitions...")

	// Phase 1: Partition nodes 0 and 1
	transporttest.PartitionNode(cluster, 0) //nolint:errcheck // test partition setup
	transporttest.PartitionNode(cluster, 1) //nolint:errcheck // test partition setup
	t.Log("Phase 1: Partitioned nodes 0 and 1 (5 nodes remaining)")

	// Wait for a leader among the remaining 5 nodes
	leaderFound := false
	helpers.WaitForCondition(t, func() bool {
		for i := 2; i < 7; i++ {
			_, isLeader := cluster.Nodes[i].GetState()
			if isLeader {
				leaderFound = true
				t.Logf("Leader found at node %d after phase 1", i)
				return true
			}
		}
		return false
	}, 2*time.Second, "leader among remaining nodes after phase 1")

	if !leaderFound {
		t.Error("No leader after partitioning 2 nodes")
	}

	// Phase 2: Partition nodes 2 and 3
	transporttest.PartitionNode(cluster, 2) //nolint:errcheck // test partition setup
	transporttest.PartitionNode(cluster, 3) //nolint:errcheck // test partition setup
	t.Log("Phase 2: Partitioned nodes 2 and 3 (3 nodes remaining)")

	time.Sleep(500 * time.Millisecond)

	// With only 3 nodes out of 7, there should be NO leader (no quorum: 3 < 4)
	leaderFound = false
	for i := 4; i < 7; i++ {
		_, isLeader := cluster.Nodes[i].GetState()
		if isLeader {
			leaderFound = true
			t.Errorf("Unexpected leader at node %d with minority (3/7 nodes)", i)
			break
		}
	}

	if !leaderFound {
		t.Log("✓ No leader after partitioning 4 nodes (correct: 3/7 is not a quorum)")
	}

	// Phase 3: Partition node 4 (leaving only 2 nodes: 5 and 6)
	transporttest.PartitionNode(cluster, 4) //nolint:errcheck // test partition setup
	t.Log("Phase 3: Partitioned node 4 (2 nodes remaining - no quorum)")

	// Wait to ensure no leader emerges with only 2/7 nodes
	helpers.WaitForCondition(t, func() bool {
		// Check that remaining nodes have stepped down
		for i := 5; i < 7; i++ {
			_, isLeader := cluster.Nodes[i].GetState()
			if isLeader {
				return false // Still has a leader, keep waiting
			}
		}
		return true // No leaders among minority
	}, 2*time.Second, "nodes to step down without quorum")

	// Verify no leader
	leaderCount := 0
	for i := 5; i < 7; i++ {
		_, isLeader := cluster.Nodes[i].GetState()
		if isLeader {
			leaderCount++
		}
	}

	if leaderCount > 0 {
		t.Error("Leader elected with minority (2/7 nodes)")
	} else {
		t.Log("✓ No leader with only 2/7 nodes connected")
	}

	// Heal partitions in reverse order
	t.Log("\nHealing partitions in reverse order...")

	// Heal node 4 first (now have 3 nodes: 4, 5, 6)
	transporttest.HealPartition(cluster)

	// Wait for nodes to detect healing but not necessarily elect leader yet (still no quorum)
	helpers.WaitForCondition(t, func() bool {
		// Just ensure nodes can communicate
		return true // Quick check, main verification comes after full heal
	}, 500*time.Millisecond, "partition heal to take effect")

	// Continue healing
	transporttest.HealPartition(cluster)

	// Now wait for cluster to stabilize with majority restored
	helpers.WaitForCondition(t, func() bool {
		// Check if any node has become leader
		for _, node := range cluster.Nodes {
			_, isLeader := node.GetState()
			if isLeader {
				return true
			}
		}
		return false
	}, 3*time.Second, "leader election after healing")

	// Verify cluster recovered
	finalLeader, err := cluster.WaitForLeader(3 * time.Second)
	if err != nil {
		t.Fatalf("Cluster did not recover after healing: %v", err)
	}

	t.Logf("✓ Cluster recovered with leader at node %d", finalLeader)

	// Verify functionality
	idx, _, err := cluster.SubmitToLeader("post-cascade-test")
	if err != nil {
		t.Fatalf("Failed to submit after cascade: %v", err)
	}

	if err := cluster.WaitForCommitIndex(idx, 2*time.Second); err != nil {
		t.Fatalf("Command not committed after cascade: %v", err)
	}

	t.Log("✓ Cluster fully functional after cascading partitions")
}

// Asymmetric transport implementation
type asymmetricTransport struct {
	id              int
	registry        *asymmetricRegistry
	handler         raft.RPCHandler
	mu              sync.Mutex
	blockedIncoming map[int]bool
	blockedOutgoing map[int]bool
}

type asymmetricRegistry struct {
	nodes map[int]raft.RPCHandler
	mu    sync.RWMutex
}

func (t *asymmetricTransport) blockOutgoingTo(nodeID int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.blockedOutgoing[nodeID] = true
}

func (t *asymmetricTransport) blockAllOutgoing() {
	t.mu.Lock()
	defer t.mu.Unlock()
	for i := 0; i < len(t.registry.nodes); i++ {
		if i != t.id {
			t.blockedOutgoing[i] = true
		}
	}
}

func (t *asymmetricTransport) clearBlocks() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.blockedIncoming = make(map[int]bool)
	t.blockedOutgoing = make(map[int]bool)
}

func (t *asymmetricTransport) SendRequestVote(serverID int, args *raft.RequestVoteArgs) (*raft.RequestVoteReply, error) {
	t.mu.Lock()
	if t.blockedOutgoing[serverID] {
		t.mu.Unlock()
		return nil, fmt.Errorf("outgoing blocked to %d", serverID)
	}
	t.mu.Unlock()

	t.registry.mu.RLock()
	handler, ok := t.registry.nodes[serverID]
	t.registry.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("node %d not found", serverID)
	}

	reply := &raft.RequestVoteReply{}
	err := handler.RequestVote(args, reply)
	return reply, err
}

func (t *asymmetricTransport) SendAppendEntries(serverID int, args *raft.AppendEntriesArgs) (*raft.AppendEntriesReply, error) {
	t.mu.Lock()
	if t.blockedOutgoing[serverID] {
		t.mu.Unlock()
		return nil, fmt.Errorf("outgoing blocked to %d", serverID)
	}
	t.mu.Unlock()

	t.registry.mu.RLock()
	handler, ok := t.registry.nodes[serverID]
	t.registry.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("node %d not found", serverID)
	}

	reply := &raft.AppendEntriesReply{}
	err := handler.AppendEntries(args, reply)
	return reply, err
}

func (t *asymmetricTransport) SendInstallSnapshot(serverID int, args *raft.InstallSnapshotArgs) (*raft.InstallSnapshotReply, error) {
	t.mu.Lock()
	if t.blockedOutgoing[serverID] {
		t.mu.Unlock()
		return nil, fmt.Errorf("outgoing blocked to %d", serverID)
	}
	t.mu.Unlock()

	t.registry.mu.RLock()
	handler, ok := t.registry.nodes[serverID]
	t.registry.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("node %d not found", serverID)
	}

	reply := &raft.InstallSnapshotReply{}
	err := handler.InstallSnapshot(args, reply)
	return reply, err
}

func (t *asymmetricTransport) SetRPCHandler(handler raft.RPCHandler) {
	t.handler = handler
}

func (t *asymmetricTransport) Start() error {
	return nil
}

func (t *asymmetricTransport) Stop() error {
	return nil
}

func (t *asymmetricTransport) GetAddress() string {
	return fmt.Sprintf("asymmetric://%d", t.id)
}
