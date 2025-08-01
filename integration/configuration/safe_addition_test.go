package configuration

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ueisele/raft"
	"github.com/ueisele/raft/integration/helpers"
)

// localTransport is a simple in-memory transport for testing
type localTransport struct {
	mu      sync.RWMutex
	id      int
	peers   map[int]raft.RPCHandler
	handler raft.RPCHandler
}

func newLocalTransport(id int) *localTransport {
	return &localTransport{
		id:    id,
		peers: make(map[int]raft.RPCHandler),
	}
}

func (t *localTransport) Connect(peerID int, handler raft.RPCHandler) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.peers[peerID] = handler
}

func (t *localTransport) SendRequestVote(serverID int, args *raft.RequestVoteArgs) (*raft.RequestVoteReply, error) {
	t.mu.RLock()
	handler, ok := t.peers[serverID]
	t.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("peer %d not connected", serverID)
	}

	reply := &raft.RequestVoteReply{}
	err := handler.RequestVote(args, reply)
	return reply, err
}

func (t *localTransport) SendAppendEntries(serverID int, args *raft.AppendEntriesArgs) (*raft.AppendEntriesReply, error) {
	t.mu.RLock()
	handler, ok := t.peers[serverID]
	t.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("peer %d not connected", serverID)
	}

	reply := &raft.AppendEntriesReply{}
	err := handler.AppendEntries(args, reply)
	return reply, err
}

func (t *localTransport) SendInstallSnapshot(serverID int, args *raft.InstallSnapshotArgs) (*raft.InstallSnapshotReply, error) {
	t.mu.RLock()
	handler, ok := t.peers[serverID]
	t.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("peer %d not connected", serverID)
	}

	reply := &raft.InstallSnapshotReply{}
	err := handler.InstallSnapshot(args, reply)
	return reply, err
}

func (t *localTransport) SetRPCHandler(handler raft.RPCHandler) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.handler = handler
}

func (t *localTransport) Start() error {
	return nil
}

func (t *localTransport) Stop() error {
	return nil
}

func (t *localTransport) GetAddress() string {
	return fmt.Sprintf("local://%d", t.id)
}

// TestSafeServerAddition tests the safe server addition functionality
func TestSafeServerAddition(t *testing.T) {
	// Create a 3-node cluster
	nodes := make([]raft.Node, 3)
	transports := make([]*localTransport, 3)

	ctx := context.Background()

	// Create initial cluster
	for i := 0; i < 3; i++ {
		config := &raft.Config{
			ID:                 i,
			Peers:              []int{0, 1, 2},
			ElectionTimeoutMin: 150 * time.Millisecond,
			ElectionTimeoutMax: 300 * time.Millisecond,
			HeartbeatInterval:  50 * time.Millisecond,
		}

		transports[i] = newLocalTransport(i)
		stateMachine := raft.NewMockStateMachine()

		node, err := raft.NewNode(config, transports[i], nil, stateMachine)
		if err != nil {
			t.Fatalf("Failed to create node %d: %v", i, err)
		}

		nodes[i] = node
	}

	// Connect all nodes
	for i := 0; i < 3; i++ {
		for j := 0; j < 3; j++ {
			if i != j {
				transports[i].Connect(j, nodes[j].(raft.RPCHandler))
			}
		}
	}

	// Start all nodes
	for i, node := range nodes {
		if err := node.Start(ctx); err != nil {
			t.Fatalf("Failed to start node %d: %v", i, err)
		}
		defer node.Stop() //nolint:errcheck // test cleanup
	}

	// Wait for leader election
	leaderID := helpers.WaitForLeader(t, nodes, 2*time.Second)
	leader := nodes[leaderID]

	// Submit some commands to build up the log
	t.Log("Building log with initial commands...")
	for i := 0; i < 50; i++ {
		cmd := fmt.Sprintf("command-%d", i)
		idx, _, isLeader := leader.Submit(cmd)
		if !isLeader {
			t.Fatal("Lost leadership during setup")
		}
		if i%10 == 0 {
			t.Logf("Submitted %d commands, latest index: %d", i+1, idx)
		}
	}

	// Wait for replication
	leaderCommitIndex := leader.GetCommitIndex()
	helpers.WaitForCommitIndex(t, nodes, leaderCommitIndex, 5*time.Second)

	t.Logf("Initial cluster ready with commit index: %d", leaderCommitIndex)

	// Create a new server
	newServerID := 3
	newConfig := &raft.Config{
		ID:                 newServerID,
		Peers:              []int{}, // Will be updated when added
		ElectionTimeoutMin: 150 * time.Millisecond,
		ElectionTimeoutMax: 300 * time.Millisecond,
		HeartbeatInterval:  50 * time.Millisecond,
	}

	newTransport := newLocalTransport(newServerID)
	newStateMachine := raft.NewMockStateMachine()

	newNode, err := raft.NewNode(newConfig, newTransport, nil, newStateMachine)
	if err != nil {
		t.Fatalf("Failed to create new node: %v", err)
	}

	// Connect new node to cluster BEFORE adding to configuration
	for i := 0; i < 3; i++ {
		transports[i].Connect(newServerID, newNode.(raft.RPCHandler))
		newTransport.Connect(i, nodes[i].(raft.RPCHandler))
	}

	// Start the new node
	if err := newNode.Start(ctx); err != nil {
		t.Fatalf("Failed to start new node: %v", err)
	}
	defer newNode.Stop() //nolint:errcheck // test cleanup

	// Now add the server to configuration (it can immediately start receiving logs)
	err = leader.AddServerSafely(newServerID, fmt.Sprintf("server-%d", newServerID))
	if err != nil {
		t.Fatalf("Failed to add server safely: %v", err)
	}

	// Wait for configuration to propagate
	helpers.WaitForConditionWithProgress(t, func() (bool, string) {
		config := leader.GetConfiguration()
		for _, server := range config.Servers {
			if server.ID == newServerID {
				return true, fmt.Sprintf("server %d added to configuration", newServerID)
			}
		}
		return false, "waiting for configuration to include new server"
	}, 2*time.Second, "configuration propagation")

	// Monitor catch-up progress
	t.Log("Monitoring catch-up progress...")

	// Check initial configuration
	config := leader.GetConfiguration()
	for _, server := range config.Servers {
		if server.ID == newServerID {
			t.Logf("Server %d initially added as voting=%v", server.ID, server.Voting)
			break
		}
	}

	startTime := time.Now()
	lastProgress := -1
	promotionCheckCount := 0

	for i := 0; i < 60; i++ { // Max 60 seconds
		progress := leader.GetServerProgress(newServerID)
		if progress != nil {
			catchUpRatio := progress.CatchUpProgress()

			// Only log if progress changed
			if progress.CurrentIndex != lastProgress {
				lastProgress = progress.CurrentIndex
				t.Logf("Progress check %d: Server %d at index %d/%d (%.1f%%) - need 95%% to promote",
					i+1, newServerID, progress.CurrentIndex, progress.TargetLogIndex, catchUpRatio*100)
			}

			// Also log the leader's view
			if i%5 == 0 { // Every 5 seconds
				t.Logf("  Leader view: commitIndex=%d", leader.GetCommitIndex())
			}
		} else {
			// Progress was nil - check if promoted
			config := leader.GetConfiguration()
			for _, server := range config.Servers {
				if server.ID == newServerID && server.Voting {
					t.Logf("Server %d promoted to voting after %s (progress tracking stopped)",
						newServerID, time.Since(startTime))
					goto promoted
				}
			}
		}

		// Check if promoted
		config = leader.GetConfiguration()
		for _, server := range config.Servers {
			if server.ID == newServerID && server.Voting {
				t.Logf("Server %d promoted to voting after %s", newServerID, time.Since(startTime))
				goto promoted
			}
		}

		time.Sleep(50 * time.Millisecond) // Small polling interval
		promotionCheckCount++
	}

	t.Error("Server was not promoted within timeout")

promoted:
	// Verify the new server was promoted to voting
	config = leader.GetConfiguration()
	found := false
	for _, server := range config.Servers {
		if server.ID == newServerID {
			found = true
			if server.Voting {
				t.Log("✓ Server was successfully promoted to voting member")
			} else {
				t.Error("Server was added but not promoted to voting")
			}
			break
		}
	}

	if !found {
		t.Error("New server not found in configuration")
	}

	// Verify the cluster still works
	finalCmd := "final-command"
	finalIdx, _, isLeader := leader.Submit(finalCmd)
	if !isLeader {
		t.Fatal("Lost leadership after adding server")
	}

	t.Logf("Submitted final command at index %d", finalIdx)

	// Wait for all nodes including the new one to commit
	allNodes := append(nodes, newNode)
	helpers.WaitForCommitIndex(t, allNodes, finalIdx, 5*time.Second)

	t.Log("✓ All nodes including new server have committed final command")
}

// TestSafeConfigurationMetrics tests metric collection during safe configuration changes
func TestSafeConfigurationMetrics(t *testing.T) {
	// Create a small cluster for testing metrics
	cluster := helpers.NewTestCluster(t, 3)

	// Start cluster
	if err := cluster.Start(); err != nil {
		t.Fatalf("Failed to start cluster: %v", err)
	}

	// Wait for leader
	leaderID, err := cluster.WaitForLeader(2 * time.Second)
	if err != nil {
		t.Fatalf("Failed to elect leader: %v", err)
	}

	leader := cluster.Nodes[leaderID]

	// Create a metrics collector
	metrics := &configMetricsCollector{}

	// Submit commands and track metrics
	for i := 0; i < 10; i++ {
		start := time.Now()
		idx, _, isLeader := leader.Submit(fmt.Sprintf("cmd-%d", i))
		if !isLeader {
			t.Fatal("Lost leadership")
		}

		// Wait for commit
		if err := cluster.WaitForCommitIndex(idx, time.Second); err != nil {
			t.Fatalf("Command %d failed to commit: %v", i, err)
		}

		duration := time.Since(start)
		metrics.recordReplication(duration)

		t.Logf("Command %d replicated in %v", i, duration)
	}

	// Create a new node
	newConfig := &raft.Config{
		ID:                 3,
		Peers:              []int{},
		ElectionTimeoutMin: 150 * time.Millisecond,
		ElectionTimeoutMax: 300 * time.Millisecond,
		HeartbeatInterval:  50 * time.Millisecond,
	}

	transport := helpers.NewMultiNodeTransport(3, cluster.Registry.(*helpers.NodeRegistry))
	newNode, err := raft.NewNode(newConfig, transport, nil, raft.NewMockStateMachine())
	if err != nil {
		t.Fatalf("Failed to create new node: %v", err)
	}

	// Register new node
	cluster.Registry.(*helpers.NodeRegistry).Register(3, newNode.(raft.RPCHandler))

	// Start new node
	ctx := context.Background()
	if err := newNode.Start(ctx); err != nil {
		t.Fatalf("Failed to start new node: %v", err)
	}
	defer newNode.Stop() //nolint:errcheck // test cleanup

	// Measure configuration change time
	configStart := time.Now()

	// Add server safely (if available, otherwise use regular add)
	if err := leader.AddServer(3, "server-3", false); err != nil {
		t.Fatalf("Failed to add server: %v", err)
	}

	// Wait for configuration to include new server
	helpers.WaitForServers(t, cluster.Nodes, []int{0, 1, 2, 3}, 5*time.Second)

	configDuration := time.Since(configStart)
	metrics.recordConfigChange(configDuration)

	t.Logf("Configuration change completed in %v", configDuration)

	// Generate summary
	summary := metrics.generateSummary()

	// Verify metrics were collected
	if metrics.replicationCount == 0 {
		t.Error("No replication metrics collected")
	}

	if metrics.configChangeCount == 0 {
		t.Error("No configuration change metrics collected")
	}

	// Print summary
	t.Log("Metrics Summary:")
	t.Log(summary)
}

// configMetricsCollector collects metrics during configuration changes
type configMetricsCollector struct {
	mu                sync.Mutex
	replicationTimes  []time.Duration
	configChangeTimes []time.Duration
	replicationCount  int
	configChangeCount int
}

func (m *configMetricsCollector) recordReplication(duration time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.replicationTimes = append(m.replicationTimes, duration)
	m.replicationCount++
}

func (m *configMetricsCollector) recordConfigChange(duration time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.configChangeTimes = append(m.configChangeTimes, duration)
	m.configChangeCount++
}

func (m *configMetricsCollector) generateSummary() string {
	m.mu.Lock()
	defer m.mu.Unlock()

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Replication Operations: %d\n", m.replicationCount))

	if m.replicationCount > 0 {
		var total time.Duration
		for _, d := range m.replicationTimes {
			total += d
		}
		avg := total / time.Duration(m.replicationCount)
		sb.WriteString(fmt.Sprintf("  Average Replication Time: %v\n", avg))
	}

	sb.WriteString(fmt.Sprintf("Configuration Changes: %d\n", m.configChangeCount))

	if m.configChangeCount > 0 {
		var total time.Duration
		for _, d := range m.configChangeTimes {
			total += d
		}
		avg := total / time.Duration(m.configChangeCount)
		sb.WriteString(fmt.Sprintf("  Average Config Change Time: %v\n", avg))
	}

	return sb.String()
}
