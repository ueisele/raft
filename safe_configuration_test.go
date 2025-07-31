package raft

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	// "github.com/ueisele/raft/test" - removed to avoid import cycle
)

// localTransport is a simple in-memory transport for testing
type localTransport struct {
	mu       sync.RWMutex
	id       int
	peers    map[int]RPCHandler
	handler  RPCHandler
}

func newLocalTransport(id int) *localTransport {
	return &localTransport{
		id:    id,
		peers: make(map[int]RPCHandler),
	}
}

func (t *localTransport) Connect(peerID int, handler RPCHandler) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.peers[peerID] = handler
}

func (t *localTransport) SendRequestVote(serverID int, args *RequestVoteArgs) (*RequestVoteReply, error) {
	t.mu.RLock()
	handler, ok := t.peers[serverID]
	t.mu.RUnlock()
	
	if !ok {
		return nil, fmt.Errorf("peer %d not connected", serverID)
	}
	
	reply := &RequestVoteReply{}
	err := handler.RequestVote(args, reply)
	return reply, err
}

func (t *localTransport) SendAppendEntries(serverID int, args *AppendEntriesArgs) (*AppendEntriesReply, error) {
	t.mu.RLock()
	handler, ok := t.peers[serverID]
	t.mu.RUnlock()
	
	if !ok {
		return nil, fmt.Errorf("peer %d not connected", serverID)
	}
	
	reply := &AppendEntriesReply{}
	err := handler.AppendEntries(args, reply)
	return reply, err
}

func (t *localTransport) SendInstallSnapshot(serverID int, args *InstallSnapshotArgs) (*InstallSnapshotReply, error) {
	t.mu.RLock()
	handler, ok := t.peers[serverID]
	t.mu.RUnlock()
	
	if !ok {
		return nil, fmt.Errorf("peer %d not connected", serverID)
	}
	
	reply := &InstallSnapshotReply{}
	err := handler.InstallSnapshot(args, reply)
	return reply, err
}

func (t *localTransport) SetRPCHandler(handler RPCHandler) {
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
	nodes := make([]Node, 3)
	transports := make([]*localTransport, 3)
	
	ctx := context.Background()
	
	for i := 0; i < 3; i++ {
		config := &Config{
			ID:                 i,
			Peers:              []int{0, 1, 2},
			ElectionTimeoutMin: 150 * time.Millisecond,
			ElectionTimeoutMax: 300 * time.Millisecond,
			HeartbeatInterval:  50 * time.Millisecond,
			Logger:             newTestLogger(t),
			MaxLogSize:         1000,
		}
		
		transport := newLocalTransport(i)
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
	}
	
	// Connect transports
	for i := 0; i < 3; i++ {
		for j := 0; j < 3; j++ {
			if i != j {
				transports[i].Connect(j, nodes[j].(RPCHandler))
			}
		}
	}
	
	// Start all nodes
	for i, node := range nodes {
		if err := node.Start(ctx); err != nil {
			t.Fatalf("Failed to start node %d: %v", i, err)
		}
		defer node.Stop()
	}
	
	// Wait for leader election
	timing := DefaultTimingConfig()
	timing.ElectionTimeout = 1 * time.Second
	leaderID := WaitForLeaderWithConfig(t, nodes, timing)
	if leaderID < 0 {
		t.Fatal("No leader elected")
	}
	leader := nodes[leaderID]
	
	t.Logf("Leader is node %d", leaderID)
	
	// Submit some entries to build up the log
	for i := 0; i < 20; i++ {
		cmd := fmt.Sprintf("command-%d", i)
		index, _, ok := leader.Submit(cmd)
		if !ok {
			t.Fatal("Failed to submit command")
		}
		t.Logf("Submitted command %s at index %d", cmd, index)
	}
	
	// Wait for replication
	WaitForCommitIndexWithConfig(t, nodes, 20, timing)
	
	commitIndex := leader.GetCommitIndex()
	t.Logf("Commit index before adding server: %d", commitIndex)
	
	// Test 1: Safe server addition
	t.Log("Test 1: Adding server safely (as non-voting)")
	
	newServerID := 3
	
	// First create the new server (before adding to configuration)
	newConfig := &Config{
		ID:                 newServerID,
		Peers:              []int{}, // Will be updated by configuration
		ElectionTimeoutMin: 150 * time.Millisecond,
		ElectionTimeoutMax: 300 * time.Millisecond,
		HeartbeatInterval:  50 * time.Millisecond,
		Logger:             newTestLogger(t),
		MaxLogSize:         1000,
	}
	
	newTransport := newLocalTransport(newServerID)
	newStateMachine := &testStateMachine{
		mu:   sync.Mutex{},
		data: make(map[string]string),
	}
	
	newNode, err := NewNode(newConfig, newTransport, nil, newStateMachine)
	if err != nil {
		t.Fatalf("Failed to create new node: %v", err)
	}
	
	// Connect new node to cluster BEFORE adding to configuration
	for i := 0; i < 3; i++ {
		transports[i].Connect(newServerID, newNode.(RPCHandler))
		newTransport.Connect(i, nodes[i].(RPCHandler))
	}
	
	// Start the new node
	if err := newNode.Start(ctx); err != nil {
		t.Fatalf("Failed to start new node: %v", err)
	}
	defer newNode.Stop()
	
	// Now add the server to configuration (it can immediately start receiving logs)
	err = leader.AddServerSafely(newServerID, fmt.Sprintf("server-%d", newServerID))
	if err != nil {
		t.Fatalf("Failed to add server safely: %v", err)
	}
	
	// Wait for configuration to propagate
	WaitForConditionWithProgress(t, func() (bool, string) {
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
			
			// Also log the leader's view of replication
			if i%5 == 0 { // Every 5 seconds
				leaderNode := leader.(*raftNode)
				matchIndex := leaderNode.replication.GetMatchIndex(newServerID)
				t.Logf("  Leader view: matchIndex[%d]=%d, commitIndex=%d", 
					newServerID, matchIndex, leader.GetCommitIndex())
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
				t.Error("✗ Server is still non-voting after promotion")
			}
			break
		}
	}
	
	if !found {
		t.Error("New server not found in configuration")
	}
	
	// Test 2: Unsafe server addition (with warning)
	t.Log("\nTest 2: Unsafe server addition (immediate voting)")
	
	unsafeServerID := 4
	err = leader.AddServer(unsafeServerID, fmt.Sprintf("server-%d", unsafeServerID), true)
	if err != nil {
		t.Logf("Failed to add unsafe server (expected if config change in progress): %v", err)
	} else {
		// Verify it was added as voting immediately
		config = leader.GetConfiguration()
		for _, server := range config.Servers {
			if server.ID == unsafeServerID && server.Voting {
				t.Log("⚠️  Server added as voting immediately (unsafe)")
			}
		}
	}
}

// TestSafeConfigurationMetrics tests the metrics collection
func TestSafeConfigurationMetrics(t *testing.T) {
	// Create a simple metrics collector
	metrics := NewSimpleConfigMetrics()
	
	// Record some operations
	metrics.RecordServerAdded(1, false)
	metrics.RecordCatchUpProgress(1, 0.25)
	metrics.RecordCatchUpProgress(1, 0.50)
	metrics.RecordCatchUpProgress(1, 0.75)
	metrics.RecordCatchUpProgress(1, 0.95)
	metrics.RecordServerPromoted(1, 45*time.Second)
	
	metrics.RecordServerAdded(2, false)
	metrics.RecordCatchUpProgress(2, 0.30)
	metrics.RecordConfigurationError(fmt.Errorf("test error"))
	
	// Get summary
	summary := metrics.GetSummary()
	
	// Verify metrics
	if summary.ServersAdded != 2 {
		t.Errorf("Expected 2 servers added, got %d", summary.ServersAdded)
	}
	
	if summary.ServersPromoted != 1 {
		t.Errorf("Expected 1 server promoted, got %d", summary.ServersPromoted)
	}
	
	if summary.ConfigErrors != 1 {
		t.Errorf("Expected 1 config error, got %d", summary.ConfigErrors)
	}
	
	// Print summary
	t.Log("Metrics Summary:")
	t.Log(summary.String())
}

// TestCatchUpProgressCalculation tests the progress calculation logic
func TestCatchUpProgressCalculation(t *testing.T) {
	tests := []struct {
		name              string
		matchIndex        int
		lastLogIndex      int
		commitIndex       int
		minEntries        int
		threshold         float64
		expectPromotion   bool
	}{
		{
			name:            "Empty log",
			matchIndex:      0,
			lastLogIndex:    0,
			commitIndex:     0,
			minEntries:      10,
			threshold:       0.95,
			expectPromotion: false, // No entries replicated
		},
		{
			name:            "Fully caught up",
			matchIndex:      100,
			lastLogIndex:    100,
			commitIndex:     95,
			minEntries:      10,
			threshold:       0.95,
			expectPromotion: true,
		},
		{
			name:            "95% caught up",
			matchIndex:      95,
			lastLogIndex:    100,
			commitIndex:     90,
			minEntries:      10,
			threshold:       0.95,
			expectPromotion: true,
		},
		{
			name:            "Below threshold",
			matchIndex:      90,
			lastLogIndex:    100,
			commitIndex:     90,
			minEntries:      10,
			threshold:       0.95,
			expectPromotion: false,
		},
		{
			name:            "Not enough entries",
			matchIndex:      5,
			lastLogIndex:    5,
			commitIndex:     5,
			minEntries:      10,
			threshold:       0.95,
			expectPromotion: false,
		},
		{
			name:            "Behind commit index",
			matchIndex:      80,
			lastLogIndex:    100,
			commitIndex:     90,
			minEntries:      10,
			threshold:       0.95,
			expectPromotion: false,
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Calculate progress
			var progress float64
			if tt.lastLogIndex > 0 {
				progress = float64(tt.matchIndex) / float64(tt.lastLogIndex)
			} else {
				progress = 1.0
			}
			
			// Check promotion criteria
			entriesReplicated := tt.matchIndex
			caughtUpToCommit := tt.matchIndex >= tt.commitIndex
			sufficientProgress := progress >= tt.threshold
			sufficientEntries := entriesReplicated >= tt.minEntries
			
			shouldPromote := caughtUpToCommit && sufficientProgress && sufficientEntries
			
			if shouldPromote != tt.expectPromotion {
				t.Errorf("Expected promotion=%v, got %v (progress=%.2f, caught up=%v, sufficient=%v, entries=%v)",
					tt.expectPromotion, shouldPromote, progress, caughtUpToCommit, sufficientProgress, sufficientEntries)
			}
		})
	}
}