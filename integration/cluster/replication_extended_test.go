package cluster

import (
	"context"
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ueisele/raft"
	"github.com/ueisele/raft/integration/helpers"
)

// TestLogReplicationWithFailures tests log replication with various failure scenarios
func TestLogReplicationWithFailures(t *testing.T) {
	// Create 5-node cluster with failure-injecting transport
	numNodes := 5
	nodes := make([]raft.Node, numNodes)
	transports := make([]*failureTransport, numNodes)
	registry := &failureRegistry{
		nodes: make(map[int]raft.RPCHandler),
		mu:    sync.RWMutex{},
	}

	for i := 0; i < numNodes; i++ {
		config := &raft.Config{
			ID:                 i,
			Peers:              []int{0, 1, 2, 3, 4},
			ElectionTimeoutMin: 150 * time.Millisecond,
			ElectionTimeoutMax: 300 * time.Millisecond,
			HeartbeatInterval:  50 * time.Millisecond,
			Logger:             raft.NewTestLogger(t),
		}

		transport := &failureTransport{
			id:          i,
			registry:    registry,
			failureRate: 0.1, // 10% failure rate
			logger:      raft.NewTestLogger(t),
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
		defer node.Stop()
	}

	// Wait for leader election
	leaderID := helpers.WaitForLeader(t, nodes, 3*time.Second)
	t.Logf("Leader elected: Node %d", leaderID)

	// Track metrics
	var totalSubmitted int32
	var totalCommitted int32
	var totalFailed int32

	// Submit commands despite failures
	numCommands := 100
	for i := 0; i < numCommands; i++ {
		cmd := fmt.Sprintf("cmd-%d", i)
		atomic.AddInt32(&totalSubmitted, 1)

		// Try to submit to leader
		_, _, isLeader := nodes[leaderID].Submit(cmd)
		if !isLeader {
			// Leader might have changed
			atomic.AddInt32(&totalFailed, 1)
			
			// Find new leader
			for j, node := range nodes {
				_, isLeader := node.GetState()
				if isLeader {
					leaderID = j
					break
				}
			}
			continue
		}

		// Small delay to allow replication
		if i%10 == 0 {
			time.Sleep(50 * time.Millisecond)
		}
	}

	// Wait for replication to settle
	time.Sleep(2 * time.Second)

	// Check how many commands were committed
	maxCommitIndex := 0
	for i, node := range nodes {
		commitIndex := node.GetCommitIndex()
		if commitIndex > maxCommitIndex {
			maxCommitIndex = commitIndex
		}
		t.Logf("Node %d commit index: %d", i, commitIndex)
	}

	atomic.StoreInt32(&totalCommitted, int32(maxCommitIndex))

	// Report statistics
	t.Logf("Replication with failures:")
	t.Logf("  Submitted: %d", atomic.LoadInt32(&totalSubmitted))
	t.Logf("  Committed: %d", atomic.LoadInt32(&totalCommitted))
	t.Logf("  Failed: %d", atomic.LoadInt32(&totalFailed))
	
	// Check transport failure stats
	totalAttempts := 0
	totalFailures := 0
	for _, transport := range transports {
		totalAttempts += int(atomic.LoadInt64(&transport.attempts))
		totalFailures += int(atomic.LoadInt64(&transport.failures))
	}
	
	if totalAttempts > 0 {
		failureRate := float64(totalFailures) / float64(totalAttempts)
		t.Logf("  Network failure rate: %.2f%% (%d/%d)", 
			failureRate*100, totalFailures, totalAttempts)
	}

	// Success criteria: at least 50% of commands should be committed despite failures
	successRate := float64(totalCommitted) / float64(totalSubmitted)
	if successRate < 0.5 {
		t.Errorf("Low success rate: %.2f%%", successRate*100)
	} else {
		t.Logf("✓ Good success rate despite failures: %.2f%%", successRate*100)
	}
}

// TestReplicationPerformance tests replication performance under load
func TestReplicationPerformance(t *testing.T) {
	// Create 3-node cluster
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

	// Measure throughput
	numCommands := 1000
	commandSize := 100 // bytes per command
	
	// Generate commands
	commands := make([]string, numCommands)
	for i := 0; i < numCommands; i++ {
		// Create command of specific size
		data := make([]byte, commandSize)
		for j := range data {
			data[j] = byte('a' + (i+j)%26)
		}
		commands[i] = fmt.Sprintf("cmd-%d-%s", i, string(data))
	}

	// Submit commands and measure time
	start := time.Now()
	
	for i, cmd := range commands {
		_, _, isLeader := cluster.Nodes[leaderID].Submit(cmd)
		if !isLeader {
			t.Logf("Failed to submit command %d: not leader", i)
		}
	}
	
	submitDuration := time.Since(start)
	
	// Wait for all to be committed
	lastCommitIndex := cluster.Nodes[leaderID].GetCommitIndex()
	helpers.WaitForConditionWithProgress(t, func() (bool, string) {
		currentCommit := cluster.Nodes[leaderID].GetCommitIndex()
		allCaughtUp := true
		for _, node := range cluster.Nodes {
			if node.GetCommitIndex() < currentCommit {
				allCaughtUp = false
				break
			}
		}
		return allCaughtUp, fmt.Sprintf("commit index: %d", currentCommit)
	}, 10*time.Second, "full replication")
	
	totalDuration := time.Since(start)
	
	// Calculate metrics
	throughputSubmit := float64(numCommands) / submitDuration.Seconds()
	throughputTotal := float64(numCommands) / totalDuration.Seconds()
	dataRate := float64(numCommands*commandSize) / totalDuration.Seconds() / 1024 // KB/s
	
	t.Logf("Replication performance:")
	t.Logf("  Commands: %d × %d bytes", numCommands, commandSize)
	t.Logf("  Submit time: %v (%.0f cmd/s)", submitDuration, throughputSubmit)
	t.Logf("  Total time: %v (%.0f cmd/s)", totalDuration, throughputTotal)
	t.Logf("  Data rate: %.1f KB/s", dataRate)
	t.Logf("  Final commit index: %d", lastCommitIndex)
}

// TestReplicationPatterns tests various replication patterns
func TestReplicationPatterns(t *testing.T) {
	cluster := helpers.NewTestCluster(t, 5)

	// Start cluster
	if err := cluster.Start(); err != nil {
		t.Fatalf("Failed to start cluster: %v", err)
	}

	leaderID, err := cluster.WaitForLeader(2 * time.Second)
	if err != nil {
		t.Fatalf("No leader elected: %v", err)
	}

	t.Run("BurstyTraffic", func(t *testing.T) {
		// Simulate bursty traffic pattern
		for burst := 0; burst < 5; burst++ {
			// Burst of commands
			for i := 0; i < 20; i++ {
				cluster.Nodes[leaderID].Submit(fmt.Sprintf("burst-%d-cmd-%d", burst, i))
			}
			
			// Quiet period
			time.Sleep(200 * time.Millisecond)
		}
		
		// Wait for replication
		time.Sleep(1 * time.Second)
		
		// Check all nodes caught up
		commitIndices := make([]int, len(cluster.Nodes))
		for i, node := range cluster.Nodes {
			commitIndices[i] = node.GetCommitIndex()
		}
		
		minCommit := commitIndices[0]
		maxCommit := commitIndices[0]
		for _, ci := range commitIndices {
			if ci < minCommit {
				minCommit = ci
			}
			if ci > maxCommit {
				maxCommit = ci
			}
		}
		
		t.Logf("Bursty traffic: commit indices range [%d, %d]", minCommit, maxCommit)
		
		if maxCommit-minCommit > 10 {
			t.Errorf("Large divergence in commit indices: %d", maxCommit-minCommit)
		}
	})

	t.Run("SteadyStream", func(t *testing.T) {
		// Simulate steady stream of commands
		ctx, cancel := context.WithCancel(context.Background())
		
		var submitted int32
		go func() {
			ticker := time.NewTicker(10 * time.Millisecond)
			defer ticker.Stop()
			
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					count := atomic.AddInt32(&submitted, 1)
					cluster.Nodes[leaderID].Submit(fmt.Sprintf("steady-%d", count))
				}
			}
		}()
		
		// Run for a period
		time.Sleep(2 * time.Second)
		cancel()
		
		totalSubmitted := atomic.LoadInt32(&submitted)
		
		// Wait for replication to complete
		time.Sleep(1 * time.Second)
		
		// Check commit progress
		avgCommit := 0
		for _, node := range cluster.Nodes {
			avgCommit += node.GetCommitIndex()
		}
		avgCommit /= len(cluster.Nodes)
		
		t.Logf("Steady stream: submitted %d, avg commit index %d", totalSubmitted, avgCommit)
	})

	t.Run("LargeEntries", func(t *testing.T) {
		// Test replication of large entries
		sizes := []int{1024, 10240, 102400} // 1KB, 10KB, 100KB
		
		for _, size := range sizes {
			// Create large command
			data := make([]byte, size)
			for i := range data {
				data[i] = byte(rand.Intn(256))
			}
			
			largeCmd := fmt.Sprintf("large-%d-%s", size, string(data))
			
			start := time.Now()
			idx, _, isLeader := cluster.Nodes[leaderID].Submit(largeCmd)
			if !isLeader {
				t.Logf("Failed to submit %d byte command: not leader", size)
				continue
			}
			
			// Wait for replication
			if err := cluster.WaitForCommitIndex(idx, 5*time.Second); err != nil {
				t.Logf("Large entry (%d bytes) failed to replicate: %v", size, err)
			} else {
				duration := time.Since(start)
				t.Logf("✓ Replicated %d byte entry in %v", size, duration)
			}
		}
	})
}

// TestReplicationWithSlowFollowers tests replication with slow followers
func TestReplicationWithSlowFollowers(t *testing.T) {
	// Create cluster with asymmetric delays
	numNodes := 5
	nodes := make([]raft.Node, numNodes)
	transports := make([]*delayTransport, numNodes)
	registry := helpers.NewNodeRegistry()

	for i := 0; i < numNodes; i++ {
		config := &raft.Config{
			ID:                 i,
			Peers:              []int{0, 1, 2, 3, 4},
			ElectionTimeoutMin: 150 * time.Millisecond,
			ElectionTimeoutMax: 300 * time.Millisecond,
			HeartbeatInterval:  50 * time.Millisecond,
			Logger:             raft.NewTestLogger(t),
		}

		// Make some followers slow
		var delay time.Duration
		if i == 3 || i == 4 {
			delay = 100 * time.Millisecond // Slow followers
		} else {
			delay = 5 * time.Millisecond // Normal
		}

		transport := &delayTransport{
			baseTransport: helpers.NewMultiNodeTransport(i, registry),
			delay:         delay,
		}
		transports[i] = transport

		node, err := raft.NewNode(config, transport, nil, raft.NewMockStateMachine())
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

	// Wait for leader
	leaderID := helpers.WaitForLeader(t, nodes, 3*time.Second)
	t.Logf("Leader: node %d", leaderID)
	t.Log("Slow followers: nodes 3 and 4")

	// Submit commands
	numCommands := 20
	for i := 0; i < numCommands; i++ {
		cmd := fmt.Sprintf("slow-follower-test-%d", i)
		idx, _, isLeader := nodes[leaderID].Submit(cmd)
		if !isLeader {
			t.Logf("Failed to submit command %d: not leader", i)
			continue
		}

		// Check commit progress periodically
		if i%5 == 4 {
			time.Sleep(200 * time.Millisecond)
			
			for j, node := range nodes {
				commit := node.GetCommitIndex()
				lag := idx - commit
				if j == 3 || j == 4 {
					t.Logf("Slow follower %d: commit=%d, lag=%d", j, commit, lag)
				} else {
					t.Logf("Normal node %d: commit=%d, lag=%d", j, commit, lag)
				}
			}
		}
	}

	// Wait for slow followers to catch up
	time.Sleep(2 * time.Second)

	// Final check
	commitIndices := make([]int, numNodes)
	for i, node := range nodes {
		commitIndices[i] = node.GetCommitIndex()
	}

	minCommit := commitIndices[0]
	maxCommit := commitIndices[0]
	for _, ci := range commitIndices {
		if ci < minCommit {
			minCommit = ci
		}
		if ci > maxCommit {
			maxCommit = ci
		}
	}

	t.Logf("Final commit indices: %v", commitIndices)
	t.Logf("Range: [%d, %d], difference: %d", minCommit, maxCommit, maxCommit-minCommit)

	if maxCommit-minCommit > 5 {
		t.Logf("Warning: Large commit index divergence with slow followers")
	} else {
		t.Log("✓ Slow followers kept up reasonably well")
	}
}

// Helper types for extended replication tests

type failureTransport struct {
	id          int
	registry    *failureRegistry
	handler     raft.RPCHandler
	failureRate float64
	attempts    int64
	failures    int64
	logger      raft.Logger
}

type failureRegistry struct {
	nodes map[int]raft.RPCHandler
	mu    sync.RWMutex
}

func (r *failureRegistry) Register(id int, handler raft.RPCHandler) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.nodes[id] = handler
}

func (t *failureTransport) shouldFail() bool {
	return rand.Float64() < t.failureRate
}

func (t *failureTransport) SendRequestVote(serverID int, args *raft.RequestVoteArgs) (*raft.RequestVoteReply, error) {
	atomic.AddInt64(&t.attempts, 1)
	
	if t.shouldFail() {
		atomic.AddInt64(&t.failures, 1)
		return nil, fmt.Errorf("simulated network failure")
	}

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

func (t *failureTransport) SendAppendEntries(serverID int, args *raft.AppendEntriesArgs) (*raft.AppendEntriesReply, error) {
	atomic.AddInt64(&t.attempts, 1)
	
	if t.shouldFail() {
		atomic.AddInt64(&t.failures, 1)
		return nil, fmt.Errorf("simulated network failure")
	}

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

func (t *failureTransport) SendInstallSnapshot(serverID int, args *raft.InstallSnapshotArgs) (*raft.InstallSnapshotReply, error) {
	atomic.AddInt64(&t.attempts, 1)
	
	if t.shouldFail() {
		atomic.AddInt64(&t.failures, 1)
		return nil, fmt.Errorf("simulated network failure")
	}

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

func (t *failureTransport) SetRPCHandler(handler raft.RPCHandler) {
	t.handler = handler
}

func (t *failureTransport) Start() error {
	return nil
}

func (t *failureTransport) Stop() error {
	return nil
}

func (t *failureTransport) GetAddress() string {
	return fmt.Sprintf("failure://%d", t.id)
}

// delayTransport adds artificial delays to simulate slow networks
type delayTransport struct {
	baseTransport raft.Transport
	delay         time.Duration
}

func (t *delayTransport) SendRequestVote(serverID int, args *raft.RequestVoteArgs) (*raft.RequestVoteReply, error) {
	time.Sleep(t.delay)
	return t.baseTransport.SendRequestVote(serverID, args)
}

func (t *delayTransport) SendAppendEntries(serverID int, args *raft.AppendEntriesArgs) (*raft.AppendEntriesReply, error) {
	time.Sleep(t.delay)
	return t.baseTransport.SendAppendEntries(serverID, args)
}

func (t *delayTransport) SendInstallSnapshot(serverID int, args *raft.InstallSnapshotArgs) (*raft.InstallSnapshotReply, error) {
	time.Sleep(t.delay)
	return t.baseTransport.SendInstallSnapshot(serverID, args)
}

func (t *delayTransport) SetRPCHandler(handler raft.RPCHandler) {
	t.baseTransport.SetRPCHandler(handler)
}

func (t *delayTransport) Start() error {
	return t.baseTransport.Start()
}

func (t *delayTransport) Stop() error {
	return t.baseTransport.Stop()
}

func (t *delayTransport) GetAddress() string {
	return t.baseTransport.GetAddress()
}