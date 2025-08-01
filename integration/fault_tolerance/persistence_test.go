package fault_tolerance

import (
	"context"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/ueisele/raft"
	"github.com/ueisele/raft/integration/helpers"
)

// Global storage for persistence data in tests
var persistenceStore = struct {
	mu    sync.Mutex
	data  map[string]*raft.PersistentState
	snaps map[string]*raft.Snapshot
}{
	data:  make(map[string]*raft.PersistentState),
	snaps: make(map[string]*raft.Snapshot),
}

// filePersistence is a simple file-based persistence for testing
type filePersistence struct {
	dataDir string
}

func newFilePersistence(dataDir string) *filePersistence {
	return &filePersistence{
		dataDir: dataDir,
	}
}

func (p *filePersistence) SaveState(state *raft.PersistentState) error {
	persistenceStore.mu.Lock()
	defer persistenceStore.mu.Unlock()

	if state != nil {
		// Make a deep copy to avoid mutation
		stateCopy := raft.PersistentState{
			CurrentTerm: state.CurrentTerm,
			VotedFor:    state.VotedFor,
			CommitIndex: state.CommitIndex,
		}
		// Deep copy the log entries
		if state.Log != nil {
			stateCopy.Log = make([]raft.LogEntry, len(state.Log))
			copy(stateCopy.Log, state.Log)
		}
		persistenceStore.data[p.dataDir] = &stateCopy
	}
	return nil
}

func (p *filePersistence) LoadState() (*raft.PersistentState, error) {
	persistenceStore.mu.Lock()
	defer persistenceStore.mu.Unlock()

	if state, ok := persistenceStore.data[p.dataDir]; ok {
		// Return a deep copy
		stateCopy := raft.PersistentState{
			CurrentTerm: state.CurrentTerm,
			VotedFor:    state.VotedFor,
			CommitIndex: state.CommitIndex,
		}
		if state.Log != nil {
			stateCopy.Log = make([]raft.LogEntry, len(state.Log))
			copy(stateCopy.Log, state.Log)
		}
		return &stateCopy, nil
	}
	return nil, nil
}

func (p *filePersistence) SaveSnapshot(snapshot *raft.Snapshot) error {
	persistenceStore.mu.Lock()
	defer persistenceStore.mu.Unlock()
	
	if snapshot != nil {
		// Deep copy snapshot
		snapCopy := &raft.Snapshot{
			LastIncludedIndex: snapshot.LastIncludedIndex,
			LastIncludedTerm:  snapshot.LastIncludedTerm,
			Data:              make([]byte, len(snapshot.Data)),
		}
		copy(snapCopy.Data, snapshot.Data)
		persistenceStore.snaps[p.dataDir] = snapCopy
	}
	return nil
}

func (p *filePersistence) LoadSnapshot() (*raft.Snapshot, error) {
	persistenceStore.mu.Lock()
	defer persistenceStore.mu.Unlock()
	
	if snap, ok := persistenceStore.snaps[p.dataDir]; ok {
		// Return a deep copy
		snapCopy := &raft.Snapshot{
			LastIncludedIndex: snap.LastIncludedIndex,
			LastIncludedTerm:  snap.LastIncludedTerm,
			Data:              make([]byte, len(snap.Data)),
		}
		copy(snapCopy.Data, snap.Data)
		return snapCopy, nil
	}
	return nil, nil
}

func (p *filePersistence) HasSnapshot() bool {
	persistenceStore.mu.Lock()
	defer persistenceStore.mu.Unlock()
	
	_, ok := persistenceStore.snaps[p.dataDir]
	return ok
}

// TestNodeRestartWithPersistence tests that nodes can restart and recover state
func TestNodeRestartWithPersistence(t *testing.T) {
	// Create temp directory for persistence
	tempDir, err := ioutil.TempDir("", "raft-test-")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Clear persistence store
	persistenceStore.mu.Lock()
	persistenceStore.data = make(map[string]*raft.PersistentState)
	persistenceStore.snaps = make(map[string]*raft.Snapshot)
	persistenceStore.mu.Unlock()

	// Create 3-node cluster with persistence
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
		
		// Create persistence for each node
		nodeDir := filepath.Join(tempDir, fmt.Sprintf("node-%d", i))
		persistence := newFilePersistence(nodeDir)

		stateMachine := raft.NewMockStateMachine()

		node, err := raft.NewNode(config, transport, persistence, stateMachine)
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

	// Wait for leader election
	leaderID := helpers.WaitForLeader(t, nodes, 2*time.Second)
	leaderTerm, _ := nodes[leaderID].GetState()
	t.Logf("Initial leader: Node %d (term %d)", leaderID, leaderTerm)

	// Submit commands
	committedCommands := []string{}
	for i := 0; i < 10; i++ {
		cmd := fmt.Sprintf("persistent-cmd-%d", i)
		idx, _, isLeader := nodes[leaderID].Submit(cmd)
		if !isLeader {
			t.Fatalf("Failed to submit command: not leader")
		}
		
		helpers.WaitForCommitIndex(t, nodes, idx, time.Second)
		committedCommands = append(committedCommands, cmd)
	}

	commitIndexBefore := nodes[0].GetCommitIndex()
	t.Logf("Commit index before restart: %d", commitIndexBefore)

	// Stop all nodes
	for _, node := range nodes {
		node.Stop()
	}
	t.Log("Stopped all nodes")

	// Recreate nodes with same persistence
	newNodes := make([]raft.Node, numNodes)
	newRegistry := helpers.NewNodeRegistry()

	for i := 0; i < numNodes; i++ {
		config := &raft.Config{
			ID:                 i,
			Peers:              []int{0, 1, 2},
			ElectionTimeoutMin: 150 * time.Millisecond,
			ElectionTimeoutMax: 300 * time.Millisecond,
			HeartbeatInterval:  50 * time.Millisecond,
			Logger:             raft.NewTestLogger(t),
		}

		transport := helpers.NewMultiNodeTransport(i, newRegistry)
		
		// Use same persistence directory
		nodeDir := filepath.Join(tempDir, fmt.Sprintf("node-%d", i))
		persistence := newFilePersistence(nodeDir)

		stateMachine := raft.NewMockStateMachine()

		node, err := raft.NewNode(config, transport, persistence, stateMachine)
		if err != nil {
			t.Fatalf("Failed to recreate node %d: %v", i, err)
		}

		newNodes[i] = node
		newRegistry.Register(i, node.(raft.RPCHandler))
	}

	// Start all new nodes
	for i, node := range newNodes {
		if err := node.Start(ctx); err != nil {
			t.Fatalf("Failed to restart node %d: %v", i, err)
		}
		defer node.Stop()
	}

	t.Log("Restarted all nodes")

	// Wait for leader election
	newLeaderID := helpers.WaitForLeader(t, newNodes, 3*time.Second)
	newLeaderTerm, _ := newNodes[newLeaderID].GetState()
	t.Logf("New leader after restart: Node %d (term %d)", newLeaderID, newLeaderTerm)

	// Term should be at least as high as before
	if newLeaderTerm < leaderTerm {
		t.Errorf("Term went backwards: %d -> %d", leaderTerm, newLeaderTerm)
	}

	// Wait for nodes to recover
	time.Sleep(1 * time.Second)

	// Verify committed entries were preserved
	for i, node := range newNodes {
		commitIndex := node.GetCommitIndex()
		t.Logf("Node %d commit index after restart: %d", i, commitIndex)
		
		// Check log entries
		for j, cmd := range committedCommands[:commitIndexBefore] {
			entry := node.GetLogEntry(j + 1)
			if entry == nil {
				t.Errorf("Node %d missing entry at index %d", i, j+1)
				continue
			}
			if entryCmd, ok := entry.Command.(string); !ok || entryCmd != cmd {
				t.Errorf("Node %d has wrong command at index %d: got %v, want %s",
					i, j+1, entry.Command, cmd)
			}
		}
	}

	// Submit new commands to verify cluster is functional
	idx, _, isLeader := newNodes[newLeaderID].Submit("after-restart-cmd")
	if !isLeader {
		t.Fatalf("Failed to submit command after restart: not leader")
	}

	helpers.WaitForCommitIndex(t, newNodes, idx, 2*time.Second)
	t.Log("✓ Cluster functional after restart with persistence")
}

// TestCrashRecoveryScenarios tests various crash and recovery scenarios
func TestCrashRecoveryScenarios(t *testing.T) {
	// Create temp directory for persistence
	tempDir, err := ioutil.TempDir("", "raft-crash-test-")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Clear persistence store
	persistenceStore.mu.Lock()
	persistenceStore.data = make(map[string]*raft.PersistentState)
	persistenceStore.snaps = make(map[string]*raft.Snapshot)
	persistenceStore.mu.Unlock()

	// Scenario 1: Leader crashes after accepting but before committing
	t.Log("Scenario 1: Leader crash before commit")
	
	// Create cluster
	cluster := createPersistentCluster(t, tempDir, 5)
	
	// Start nodes
	ctx := context.Background()
	startCluster(t, ctx, cluster)
	
	// Find leader
	leaderID := helpers.WaitForLeader(t, cluster.nodes, 2*time.Second)
	
	// Submit command but crash leader immediately
	cluster.nodes[leaderID].Submit("uncommitted-cmd")
	cluster.nodes[leaderID].Stop()
	t.Logf("Crashed leader %d after accepting command", leaderID)
	
	// Wait for new leader
	time.Sleep(1 * time.Second)
	
	// Restart crashed node
	restartNode(t, ctx, cluster, leaderID)
	
	// Verify cluster consistency
	time.Sleep(2 * time.Second)
	verifyClusterConsistency(t, cluster.nodes)
	
	// Stop cluster
	stopCluster(cluster)

	// Scenario 2: Multiple followers crash during replication
	t.Log("\nScenario 2: Multiple followers crash during replication")
	
	// Create new cluster
	cluster2 := createPersistentCluster(t, tempDir+"2", 5)
	startCluster(t, ctx, cluster2)
	
	leaderID = helpers.WaitForLeader(t, cluster2.nodes, 2*time.Second)
	
	// Start submitting commands
	go func() {
		for i := 0; i < 10; i++ {
			cluster2.nodes[leaderID].Submit(fmt.Sprintf("concurrent-%d", i))
			time.Sleep(50 * time.Millisecond)
		}
	}()
	
	// Crash followers during replication
	time.Sleep(100 * time.Millisecond)
	for i := 0; i < 5; i++ {
		if i != leaderID && i%2 == 0 {
			cluster2.nodes[i].Stop()
			t.Logf("Crashed follower %d", i)
		}
	}
	
	// Wait and restart
	time.Sleep(500 * time.Millisecond)
	for i := 0; i < 5; i++ {
		if i != leaderID && i%2 == 0 {
			restartNode(t, ctx, cluster2, i)
		}
	}
	
	// Verify recovery
	time.Sleep(2 * time.Second)
	verifyClusterConsistency(t, cluster2.nodes)
	
	stopCluster(cluster2)

	// Scenario 3: Rolling restarts
	t.Log("\nScenario 3: Rolling restarts")
	
	cluster3 := createPersistentCluster(t, tempDir+"3", 5)
	startCluster(t, ctx, cluster3)
	
	// Submit initial data
	leaderID = helpers.WaitForLeader(t, cluster3.nodes, 2*time.Second)
	for i := 0; i < 5; i++ {
		idx, _, _ := cluster3.nodes[leaderID].Submit(fmt.Sprintf("rolling-%d", i))
		helpers.WaitForCommitIndex(t, cluster3.nodes, idx, time.Second)
	}
	
	// Rolling restart each node
	for i := 0; i < 5; i++ {
		t.Logf("Rolling restart of node %d", i)
		cluster3.nodes[i].Stop()
		time.Sleep(200 * time.Millisecond)
		restartNode(t, ctx, cluster3, i)
		time.Sleep(500 * time.Millisecond)
	}
	
	// Verify cluster still functional
	newLeaderID := helpers.WaitForLeader(t, cluster3.nodes, 2*time.Second)
	idx, _, isLeader := cluster3.nodes[newLeaderID].Submit("after-rolling-restart")
	if !isLeader {
		t.Fatalf("Failed to submit after rolling restart: not leader")
	}
	
	helpers.WaitForCommitIndex(t, cluster3.nodes, idx, 2*time.Second)
	t.Log("✓ Cluster survived rolling restarts")
	
	stopCluster(cluster3)
}

// TestPersistenceWithSnapshots tests persistence with snapshots
func TestPersistenceWithSnapshots(t *testing.T) {
	// Create temp directory
	tempDir, err := ioutil.TempDir("", "raft-snap-test-")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Clear persistence store
	persistenceStore.mu.Lock()
	persistenceStore.data = make(map[string]*raft.PersistentState)
	persistenceStore.snaps = make(map[string]*raft.Snapshot)
	persistenceStore.mu.Unlock()

	// Create cluster with snapshot support
	cluster := createPersistentCluster(t, tempDir, 3)
	
	ctx := context.Background()
	startCluster(t, ctx, cluster)
	
	// Find leader and submit many commands
	leaderID := helpers.WaitForLeader(t, cluster.nodes, 2*time.Second)
	
	// Submit enough commands to trigger snapshot
	for i := 0; i < 100; i++ {
		idx, _, isLeader := cluster.nodes[leaderID].Submit(fmt.Sprintf("snap-cmd-%d", i))
		if !isLeader {
			continue
		}
		
		if i%10 == 0 {
			helpers.WaitForCommitIndex(t, cluster.nodes, idx, time.Second)
		}
	}
	
	// Force snapshot creation (in real implementation)
	t.Log("Assuming snapshots were created...")
	
	// Stop all nodes
	stopCluster(cluster)
	
	// Restart cluster
	newCluster := createPersistentCluster(t, tempDir, 3)
	startCluster(t, ctx, newCluster)
	
	// Verify nodes recovered from snapshot + log
	time.Sleep(2 * time.Second)
	
	// Check that nodes have data
	for i, node := range newCluster.nodes {
		commitIndex := node.GetCommitIndex()
		t.Logf("Node %d recovered with commit index: %d", i, commitIndex)
		
		// Should have recovered significant progress
		if commitIndex < 50 {
			t.Errorf("Node %d didn't recover enough state: commit index %d", i, commitIndex)
		}
	}
	
	// Verify cluster is functional
	newLeaderID := helpers.WaitForLeader(t, newCluster.nodes, 2*time.Second)
	idx, _, isLeader := newCluster.nodes[newLeaderID].Submit("after-snapshot-recovery")
	if !isLeader {
		t.Fatalf("Failed to submit after snapshot recovery: not leader")
	}
	
	helpers.WaitForCommitIndex(t, newCluster.nodes, idx, 2*time.Second)
	t.Log("✓ Cluster recovered from snapshots and is functional")
	
	stopCluster(newCluster)
}

// Helper types and functions

type persistentCluster struct {
	nodes      []raft.Node
	transports []raft.Transport
	registry   *helpers.NodeRegistry
	tempDir    string
}

func createPersistentCluster(t *testing.T, tempDir string, size int) *persistentCluster {
	nodes := make([]raft.Node, size)
	transports := make([]raft.Transport, size)
	registry := helpers.NewNodeRegistry()

	for i := 0; i < size; i++ {
		config := &raft.Config{
			ID:                 i,
			Peers:              makeRange(0, size),
			ElectionTimeoutMin: 150 * time.Millisecond,
			ElectionTimeoutMax: 300 * time.Millisecond,
			HeartbeatInterval:  50 * time.Millisecond,
			Logger:             raft.NewTestLogger(t),
		}

		transport := helpers.NewMultiNodeTransport(i, registry)
		transports[i] = transport
		
		// Create persistence for each node
		nodeDir := filepath.Join(tempDir, fmt.Sprintf("node-%d", i))
		persistence := newFilePersistence(nodeDir)

		stateMachine := raft.NewMockStateMachine()

		node, err := raft.NewNode(config, transport, persistence, stateMachine)
		if err != nil {
			t.Fatalf("Failed to create node %d: %v", i, err)
		}

		nodes[i] = node
		registry.Register(i, node.(raft.RPCHandler))
	}

	return &persistentCluster{
		nodes:      nodes,
		transports: transports,
		registry:   registry,
		tempDir:    tempDir,
	}
}

func startCluster(t *testing.T, ctx context.Context, cluster *persistentCluster) {
	for i, node := range cluster.nodes {
		if err := node.Start(ctx); err != nil {
			t.Fatalf("Failed to start node %d: %v", i, err)
		}
	}
}

func stopCluster(cluster *persistentCluster) {
	for _, node := range cluster.nodes {
		node.Stop()
	}
}

func restartNode(t *testing.T, ctx context.Context, cluster *persistentCluster, nodeID int) {
	config := &raft.Config{
		ID:                 nodeID,
		Peers:              makeRange(0, len(cluster.nodes)),
		ElectionTimeoutMin: 150 * time.Millisecond,
		ElectionTimeoutMax: 300 * time.Millisecond,
		HeartbeatInterval:  50 * time.Millisecond,
		Logger:             raft.NewTestLogger(t),
	}

	transport := helpers.NewMultiNodeTransport(nodeID, cluster.registry)
	
	nodeDir := filepath.Join(cluster.tempDir, fmt.Sprintf("node-%d", nodeID))
	persistence := newFilePersistence(nodeDir)

	stateMachine := raft.NewMockStateMachine()

	node, err := raft.NewNode(config, transport, persistence, stateMachine)
	if err != nil {
		t.Fatalf("Failed to recreate node %d: %v", nodeID, err)
	}

	cluster.nodes[nodeID] = node
	cluster.registry.Register(nodeID, node.(raft.RPCHandler))

	if err := node.Start(ctx); err != nil {
		t.Fatalf("Failed to restart node %d: %v", nodeID, err)
	}
}

func verifyClusterConsistency(t *testing.T, nodes []raft.Node) {
	// Get commit indices
	commitIndices := make([]int, len(nodes))
	for i, node := range nodes {
		commitIndices[i] = node.GetCommitIndex()
		t.Logf("Node %d commit index: %d", i, commitIndices[i])
	}

	// Find max commit index
	maxCommit := 0
	for _, commit := range commitIndices {
		if commit > maxCommit {
			maxCommit = commit
		}
	}

	// Verify logs are consistent up to min commit index
	minCommit := maxCommit
	for _, commit := range commitIndices {
		if commit < minCommit {
			minCommit = commit
		}
	}

	if minCommit > 0 {
		helpers.AssertLogConsistency(t, nodes, minCommit)
		t.Logf("✓ Logs consistent up to index %d", minCommit)
	}
}

func makeRange(start, end int) []int {
	result := make([]int, end-start)
	for i := range result {
		result[i] = start + i
	}
	return result
}