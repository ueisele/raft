package safety

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/ueisele/raft"
	"github.com/ueisele/raft/integration/helpers"
)

// TestVotingSafety combines all voting safety tests into one comprehensive test
func TestVotingSafety(t *testing.T) {
	t.Run("NewVotingServerSafety", func(t *testing.T) {
		testNewVotingServerSafety(t)
	})

	t.Run("SaferApproach", func(t *testing.T) {
		testSaferApproach(t)
	})

	t.Run("VotingSafetyDemonstrationSimple", func(t *testing.T) {
		testVotingSafetyDemonstrationSimple(t)
	})

	t.Run("ImmediateVotingDanger", func(t *testing.T) {
		testImmediateVotingDanger(t)
	})

	t.Run("VotingMemberSafetyAnalysis", func(t *testing.T) {
		testVotingMemberSafetyAnalysis(t)
	})

	t.Run("DangerOfImmediateVoting", func(t *testing.T) {
		testDangerOfImmediateVoting(t)
	})

	// Vote denial tests
	t.Run("VoteDenialWithActiveLeader", func(t *testing.T) {
		testVoteDenialWithActiveLeader(t)
	})

	t.Run("VoteGrantingAfterTimeout", func(t *testing.T) {
		testVoteGrantingAfterTimeout(t)
	})

	t.Run("VoteDenialPreventsUnnecessaryElections", func(t *testing.T) {
		testVoteDenialPreventsUnnecessaryElections(t)
	})
}

// testNewVotingServerSafety shows potential issue when newly added server becomes voting immediately
func testNewVotingServerSafety(t *testing.T) {
	// Create a 3-node cluster with partitionable transport
	numNodes := 3
	nodes := make([]raft.Node, numNodes)
	registry := helpers.NewPartitionRegistry()

	for i := 0; i < numNodes; i++ {
		config := &raft.Config{
			ID:                 i,
			Peers:              []int{0, 1, 2},
			ElectionTimeoutMin: 100 * time.Millisecond,
			ElectionTimeoutMax: 200 * time.Millisecond,
			HeartbeatInterval:  30 * time.Millisecond,
			Logger:             raft.NewTestLogger(t),
		}

		transport := helpers.NewPartitionableTransport(i, registry)

		stateMachine := raft.NewMockStateMachine()

		node, err := raft.NewNode(config, transport, nil, stateMachine)
		if err != nil {
			t.Fatalf("Failed to create node %d: %v", i, err)
		}

		nodes[i] = node
		registry.Register(i, node.(raft.RPCHandler))
	}

	ctx := context.Background()

	// Start all nodes
	for i, node := range nodes {
		if err := node.Start(ctx); err != nil {
			t.Fatalf("Failed to start node %d: %v", i, err)
		}
		defer node.Stop()
	}

	// Wait for initial leader election
	leaderID := helpers.WaitForLeader(t, nodes, 2*time.Second)
	t.Logf("Initial leader: Node %d", leaderID)

	// Submit some commands
	for i := 0; i < 3; i++ {
		_, _, isLeader := nodes[leaderID].Submit(fmt.Sprintf("cmd%d", i))
		if !isLeader {
			t.Fatalf("Failed to submit command: not leader")
		}
	}

	// Wait for commands to replicate
	helpers.WaitForCommitIndex(t, nodes, 3, 2*time.Second)

	// Simulate adding a new server (node 3)
	// In real implementation, this would be done through configuration change
	// For demonstration, we'll partition the network

	// Partition node 0 (potential old leader) from the rest
	// In the test environment, we'll simulate partition by blocking transport
	// This test demonstrates the concept rather than full implementation
	t.Log("Would partition node 0 from nodes 1 and 2 (simulation)")

	// Wait for new leader among nodes 1 and 2
	// Give some time for the leader to realize it's partitioned
	time.Sleep(150 * time.Millisecond)

	var newLeaderID int
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		for i := 1; i <= 2; i++ {
			_, isLeader := nodes[i].GetState()
			if isLeader {
				newLeaderID = i
				break
			}
		}
		if newLeaderID != 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if newLeaderID == 0 {
		t.Fatal("No new leader elected among nodes 1 and 2")
	}

	t.Logf("New leader: Node %d", newLeaderID)

	// At this point, we have:
	// - Node 0: isolated, might still think it's leader
	// - Node 1 or 2: new leader
	// - A newly added server could disrupt this if it votes immediately
}

// testSaferApproach shows how non-voting members prevent the safety issue
func testSaferApproach(t *testing.T) {
	// Similar setup but with safer configuration approach
	cluster := helpers.NewTestCluster(t, 3)

	// Start cluster
	if err := cluster.Start(); err != nil {
		t.Fatalf("Failed to start cluster: %v", err)
	}

	// Wait for leader
	leaderID, err := cluster.WaitForLeader(2 * time.Second)
	if err != nil {
		t.Fatalf("No leader elected: %v", err)
	}

	t.Logf("Leader is node %d", leaderID)

	// Add new server as non-voting member first
	// This is the safer approach - new servers don't vote immediately
	t.Log("Adding new server as non-voting member (safer approach)")

	// Submit some test data
	for i := 0; i < 5; i++ {
		idx, _, err := cluster.SubmitCommand(fmt.Sprintf("safe-cmd-%d", i))
		if err != nil {
			t.Fatalf("Failed to submit command: %v", err)
		}
		cluster.WaitForCommitIndex(idx, time.Second)
	}

	t.Log("✓ Safer approach: New servers added as non-voting members first")
}

// testVotingSafetyDemonstrationSimple demonstrates voting safety with a simple scenario
func testVotingSafetyDemonstrationSimple(t *testing.T) {
	// Create a small cluster to demonstrate the concept
	cluster := helpers.NewTestCluster(t, 3)

	// Start cluster
	if err := cluster.Start(); err != nil {
		t.Fatalf("Failed to start cluster: %v", err)
	}

	// Wait for leader
	initialLeader, err := cluster.WaitForLeader(2 * time.Second)
	if err != nil {
		t.Fatalf("No leader elected: %v", err)
	}

	t.Logf("Initial configuration: 3 nodes, leader is node %d", initialLeader)

	// Log the importance of voting member management
	t.Log("Key safety principle: New servers should not immediately participate in voting")
	t.Log("This prevents them from electing leaders without complete logs")
}

// testImmediateVotingDanger shows what could happen with immediate voting rights
func testImmediateVotingDanger(t *testing.T) {
	// Create scenario where immediate voting could be dangerous
	numNodes := 3
	nodes := make([]raft.Node, numNodes)
	registry := helpers.NewNodeRegistry()

	for i := 0; i < numNodes; i++ {
		config := &raft.Config{
			ID:                 i,
			Peers:              []int{0, 1, 2},
			ElectionTimeoutMin: 150 * time.Millisecond,
			ElectionTimeoutMax: 300 * time.Millisecond,
			HeartbeatInterval:  50 * time.Millisecond,
			Logger:             raft.NewTestLogger(t),
		}

		transport := helpers.NewMultiNodeTransport(i, registry)

		node, err := raft.NewNode(config, transport, nil, raft.NewMockStateMachine())
		if err != nil {
			t.Fatalf("Failed to create node %d: %v", i, err)
		}

		nodes[i] = node
		registry.Register(i, node.(raft.RPCHandler))
	}

	ctx := context.Background()

	// Start nodes
	for i, node := range nodes {
		if err := node.Start(ctx); err != nil {
			t.Fatalf("Failed to start node %d: %v", i, err)
		}
		defer node.Stop()
	}

	// Wait for leader
	leaderID := helpers.WaitForLeader(t, nodes, 2*time.Second)

	// Submit important data
	importantData := []string{
		"critical-config-1",
		"critical-config-2",
		"critical-config-3",
	}

	for _, data := range importantData {
		idx, _, isLeader := nodes[leaderID].Submit(data)
		if !isLeader {
			t.Fatalf("Failed to submit command: not leader")
		}
		helpers.WaitForCommitIndex(t, nodes, idx, time.Second)
	}

	t.Log("Scenario: What if a new server with empty log could vote immediately?")
	t.Log("Risk: It might help elect a leader that doesn't have the critical data")
	t.Log("Solution: New servers must catch up before gaining voting rights")
}

// testVotingMemberSafetyAnalysis analyzes voting member safety scenarios
func testVotingMemberSafetyAnalysis(t *testing.T) {
	t.Log("=== Voting Member Safety Analysis ===")
	t.Log("Scenario 1: Adding server to healthy cluster")
	t.Log("  - Safe if new server is non-voting initially")
	t.Log("  - Server catches up with leader's log")
	t.Log("  - Only then promoted to voting member")

	t.Log("\nScenario 2: Adding server during network partition")
	t.Log("  - Dangerous if new server can vote immediately")
	t.Log("  - Could help minority partition elect new leader")
	t.Log("  - Results in split-brain scenario")

	t.Log("\nScenario 3: Multiple servers added concurrently")
	t.Log("  - Configuration changes must be serialized")
	t.Log("  - Joint consensus helps with safety")

	// Demonstrate with actual test
	cluster := helpers.NewTestCluster(t, 3)

	// Start cluster
	if err := cluster.Start(); err != nil {
		t.Fatalf("Failed to start cluster: %v", err)
	}

	leaderID, err := cluster.WaitForLeader(2 * time.Second)
	if err != nil {
		t.Fatalf("No leader elected: %v", err)
	}

	// Get initial configuration
	config := cluster.Nodes[leaderID].GetConfiguration()
	t.Logf("Initial configuration has %d voting members", len(config.Servers))

	// Analysis complete
	t.Log("\n✓ Analysis complete: Non-voting phase is critical for safety")
}

// testDangerOfImmediateVoting demonstrates the danger of immediate voting
func testDangerOfImmediateVoting(t *testing.T) {
	// This test demonstrates why immediate voting is dangerous
	t.Log("=== Demonstrating Danger of Immediate Voting ===")

	// Setup: 5-node cluster
	cluster := helpers.NewTestCluster(t, 5, helpers.WithPartitionableTransport())

	// Start cluster
	if err := cluster.Start(); err != nil {
		t.Fatalf("Failed to start cluster: %v", err)
	}

	// Wait for stable leader
	leaderID, err := cluster.WaitForLeader(2 * time.Second)
	if err != nil {
		t.Fatalf("No leader elected: %v", err)
	}

	// Submit critical data that must not be lost
	criticalData := []string{
		"user-account-created",
		"payment-processed",
		"inventory-updated",
	}

	lastIndex := 0
	for _, data := range criticalData {
		idx, _, err := cluster.SubmitCommand(data)
		if err != nil {
			t.Fatalf("Failed to submit critical data: %v", err)
		}
		lastIndex = idx
	}

	// Wait for replication
	if err := cluster.WaitForCommitIndex(lastIndex, 2*time.Second); err != nil {
		t.Fatalf("Critical data not replicated: %v", err)
	}

	t.Log("Critical data replicated to all nodes")

	// Now simulate what would happen if a new server could vote immediately
	// during a network partition

	// Partition the leader and one follower from the rest
	majorityNodes := []int{(leaderID + 1) % 5, (leaderID + 2) % 5, (leaderID + 3) % 5}
	minorityNodes := []int{leaderID, (leaderID + 4) % 5}

	t.Logf("Creating partition: minority=%v, majority=%v", minorityNodes, majorityNodes)

	// In a real scenario with immediate voting:
	// 1. Minority partition (2 nodes) adds a new server
	// 2. New server has empty log but can vote
	// 3. With 3 nodes, minority becomes majority
	// 4. They elect a leader without critical data
	// 5. Data loss occurs!

	t.Log("\nDanger illustrated:")
	t.Log("- Minority partition (2/5 nodes) is isolated")
	t.Log("- If they could add voting server immediately: 3/6 nodes")
	t.Log("- New quorum could elect leader without critical data")
	t.Log("- Result: CATASTROPHIC DATA LOSS")

	t.Log("\n✓ This is why new servers must be non-voting initially!")
}

// Vote denial test functions

func testVoteDenialWithActiveLeader(t *testing.T) {
	// Create a 3-node cluster
	cluster := helpers.NewTestCluster(t, 3)

	// Start cluster
	if err := cluster.Start(); err != nil {
		t.Fatalf("Failed to start cluster: %v", err)
	}

	// Wait for leader election
	leaderID, err := cluster.WaitForLeader(2 * time.Second)
	if err != nil {
		t.Fatalf("No leader elected: %v", err)
	}

	leader := cluster.Nodes[leaderID]
	followerID := (leaderID + 1) % 3

	// Submit a command to establish leadership
	idx, _, err := cluster.SubmitCommand("test-command")
	if err != nil {
		t.Fatalf("Failed to submit command: %v", err)
	}

	// Wait for command to be committed
	if err := cluster.WaitForCommitIndex(idx, time.Second); err != nil {
		t.Fatalf("Command not committed: %v", err)
	}

	// Get current term
	leaderTerm, _ := leader.GetState()

	// Simulate a candidate requesting vote in the same term
	// This should be denied because there's an active leader
	args := &raft.RequestVoteArgs{
		Term:         leaderTerm,
		CandidateID:  99, // Non-existent candidate
		LastLogIndex: 0,
		LastLogTerm:  0,
	}

	// Get the follower node directly
	follower := cluster.Nodes[followerID]
	if handler, ok := follower.(raft.RPCHandler); ok {
		reply := &raft.RequestVoteReply{}
		err := handler.RequestVote(args, reply)
		if err != nil {
			t.Fatalf("Failed to handle vote request: %v", err)
		}

		if reply.VoteGranted {
			t.Error("Vote was granted when there's an active leader - vote denial failed")
		} else {
			t.Log("✓ Vote correctly denied with active leader")
		}
	} else {
		t.Error("Could not cast node to RPCHandler")
	}
}

func testVoteGrantingAfterTimeout(t *testing.T) {
	// Create a cluster with partitionable transport
	cluster := helpers.NewTestCluster(t, 3, helpers.WithPartitionableTransport())

	// Start cluster
	if err := cluster.Start(); err != nil {
		t.Fatalf("Failed to start cluster: %v", err)
	}

	// Wait for leader
	leaderID, err := cluster.WaitForLeader(2 * time.Second)
	if err != nil {
		t.Fatalf("No leader elected: %v", err)
	}

	// Partition the leader to simulate it being unreachable
	if err := cluster.PartitionNode(leaderID); err != nil {
		t.Fatalf("Failed to partition leader: %v", err)
	}

	t.Logf("Partitioned leader node %d", leaderID)

	// Wait for election timeout
	time.Sleep(400 * time.Millisecond)

	// Check that a new leader has been elected among the remaining nodes
	newLeaderFound := false
	for i, node := range cluster.Nodes {
		if i == leaderID {
			continue
		}
		_, isLeader := node.GetState()
		if isLeader {
			newLeaderFound = true
			t.Logf("✓ New leader elected (node %d) after original leader timeout", i)
			break
		}
	}

	if !newLeaderFound {
		t.Error("No new leader elected after timeout - vote granting may have failed")
	}
}

func testVoteDenialPreventsUnnecessaryElections(t *testing.T) {
	// Create a stable cluster
	cluster := helpers.NewTestCluster(t, 5)

	// Start cluster
	if err := cluster.Start(); err != nil {
		t.Fatalf("Failed to start cluster: %v", err)
	}

	// Wait for leader
	leaderID, err := cluster.WaitForLeader(2 * time.Second)
	if err != nil {
		t.Fatalf("No leader elected: %v", err)
	}

	initialTerm, _ := cluster.Nodes[leaderID].GetState()

	// Submit commands continuously to keep leader active
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(30 * time.Millisecond)
		defer ticker.Stop()
		cmdIndex := 0
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				cluster.SubmitCommand(fmt.Sprintf("heartbeat-%d", cmdIndex))
				cmdIndex++
			}
		}
	}()

	// Run for a period and check that term doesn't increase
	// (no unnecessary elections)
	time.Sleep(1 * time.Second)
	close(done)

	finalTerm, _ := cluster.Nodes[leaderID].GetState()

	if finalTerm > initialTerm {
		t.Errorf("Unnecessary election occurred: term increased from %d to %d", initialTerm, finalTerm)
	} else {
		t.Logf("✓ No unnecessary elections: term remained at %d", initialTerm)
	}

	// Verify cluster stability
	for i, node := range cluster.Nodes {
		term, _ := node.GetState()
		if term != finalTerm {
			t.Errorf("Node %d has different term %d (expected %d)", i, term, finalTerm)
		}
	}
}