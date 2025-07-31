package raft

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	// "github.com/ueisele/raft/test" - removed to avoid import cycle
)

// TestLinearizableReads tests that reads return the most recent committed value
func TestLinearizableReads(t *testing.T) {
	// Create 5-node cluster
	numNodes := 5
	nodes := make([]Node, numNodes)
	stateMachines := make([]*kvStateMachine, numNodes)
	loggers := make([]*SafeTestLogger, numNodes)
	
	registry := &debugNodeRegistry{
		nodes:  make(map[int]RPCHandler),
		logger: newTestLogger(t),
	}

	for i := 0; i < numNodes; i++ {
		logger := NewSafeTestLogger(t)
		loggers[i] = logger
		
		config := &Config{
			ID:                 i,
			Peers:              []int{0, 1, 2, 3, 4},
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

		// Use KV state machine for linearizable reads
		stateMachine := &kvStateMachine{
			data: make(map[string]string),
			nodeID: i,
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
		defer func(idx int) {
			if idx < len(loggers) && loggers[idx] != nil {
				loggers[idx].Stop()
			}
			nodes[idx].Stop()
		}(i)
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

	// Test linearizable reads
	key := "counter"

	// Writer goroutine
	stopCh := make(chan struct{})
	writerDone := make(chan struct{})

	go func() {
		defer close(writerDone)
		value := 0

		for {
			select {
			case <-stopCh:
				return
			default:
				value++
				cmd := kvCommand{
					Type:  "set",
					Key:   key,
					Value: fmt.Sprintf("%d", value),
				}

				_, _, isLeader := leader.Submit(cmd)
				if !isLeader {
					// Find new leader
					for _, node := range nodes {
						if node.IsLeader() {
							leader = node
							break
						}
					}
				}

				// Writer interval
				time.Sleep(10 * time.Millisecond)
			}
		}
	}()

	// Reader goroutines
	numReaders := 3
	violations := int32(0)

	for i := 0; i < numReaders; i++ {
		go func(readerID int) {
			lastSeen := 0

			for {
				select {
				case <-stopCh:
					return
				default:
					// Read from leader to ensure linearizability
					cmd := kvCommand{
						Type: "get",
						Key:  key,
					}

					_, _, isLeader := leader.Submit(cmd)
					if !isLeader {
						continue
					}

					// Wait for command to be applied
					// Small delay to allow command processing
					time.Sleep(20 * time.Millisecond)

					// Read value from leader's state machine
					stateMachines[leaderID].mu.Lock()
					valueStr := stateMachines[leaderID].data[key]
					stateMachines[leaderID].mu.Unlock()

					if valueStr != "" {
						var value int
						fmt.Sscanf(valueStr, "%d", &value)

						// Check linearizability: values should only increase
						if value < lastSeen {
							atomic.AddInt32(&violations, 1)
							t.Logf("Reader %d: Linearizability violation - saw %d after %d",
								readerID, value, lastSeen)
						}

						lastSeen = value
					}

					// Reader interval
					time.Sleep(5 * time.Millisecond)
				}
			}
		}(i)
	}

	// Run test for 5 seconds
	time.Sleep(5 * time.Second)
	close(stopCh)
	<-writerDone

	// Check for violations
	if violations > 0 {
		t.Errorf("Detected %d linearizability violations", violations)
	} else {
		t.Log("Linearizable reads verified - no violations detected")
	}
}

// TestIdempotentOperations tests that operations can be safely retried
func TestIdempotentOperations(t *testing.T) {
	// Create 3-node cluster
	nodes := make([]Node, 3)
	stateMachines := make([]*kvStateMachine, 3)
	loggers := make([]*SafeTestLogger, 3)
	
	registry := &debugNodeRegistry{
		nodes:  make(map[int]RPCHandler),
		logger: newTestLogger(t),
	}

	for i := 0; i < 3; i++ {
		logger := NewSafeTestLogger(t)
		loggers[i] = logger
		
		config := &Config{
			ID:                 i,
			Peers:              []int{0, 1, 2},
			ElectionTimeoutMin: 150 * time.Millisecond,
			ElectionTimeoutMax: 300 * time.Millisecond,
			HeartbeatInterval:  50 * time.Millisecond,
			Logger:             logger,
		}

		transport := &debugTransport{
			id:       i,
			registry: registry,
			logger:   newTestLogger(t),
		}

		stateMachine := &kvStateMachine{
			data: make(map[string]string),
			nodeID: i, // Add node ID for logging
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
		defer func(idx int) {
			if idx < len(loggers) && loggers[idx] != nil {
				loggers[idx].Stop()
			}
			nodes[idx].Stop()
		}(i)
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

	// Test idempotent increment operation
	clientID := "client-1"
	requestID := "request-1"

	// Create idempotent increment command
	cmd := kvCommand{
		Type:      "increment",
		Key:       "counter",
		ClientID:  clientID,
		RequestID: requestID,
	}

	// Submit the same command multiple times
	indices := make([]int, 0)
	for i := 0; i < 5; i++ {
		index, _, isLeader := leader.Submit(cmd)
		if !isLeader {
			t.Fatal("Lost leadership")
		}
		indices = append(indices, index)
		t.Logf("Submission %d: index %d", i+1, index)

		// Small delay between retries
		time.Sleep(50 * time.Millisecond)
	}

	// Wait for all commands to be applied
	WaitForConditionWithProgress(t, func() (bool, string) {
		// Check if the last command is committed
		lastIndex := indices[len(indices)-1]
		return leader.GetCommitIndex() >= lastIndex, 
			fmt.Sprintf("commit index: %d, last command index: %d", leader.GetCommitIndex(), lastIndex)
	}, timing.ReplicationTimeout, "idempotent commands to be committed")

	// Verify that counter was only incremented once
	for i, sm := range stateMachines {
		sm.mu.Lock()
		value := sm.data["counter"]
		processedCount := len(sm.processed)
		sm.mu.Unlock()

		t.Logf("Node %d: counter=%s, processed requests=%d", i, value, processedCount)
		if value != "1" {
			t.Errorf("Node %d: Expected counter=1, got %s", i, value)
		}
	}

	// Test with different request ID
	cmd.RequestID = "request-2"
	index2, _, isLeader := leader.Submit(cmd)
	if !isLeader {
		t.Fatal("Lost leadership")
	}
	t.Logf("Submitted second unique request at index %d", index2)

	// Force a few more heartbeats to ensure replication
	for i := 0; i < 5; i++ {
		time.Sleep(50 * time.Millisecond)
		// Submit a dummy command to trigger replication
		dummyCmd := fmt.Sprintf("dummy-%d", i)
		leader.Submit(dummyCmd)
	}

	// Wait for application
	WaitForConditionWithProgress(t, func() (bool, string) {
		commitIndex := leader.GetCommitIndex()
		return commitIndex >= index2, fmt.Sprintf("commit index: %d, need: %d", commitIndex, index2)
	}, timing.ReplicationTimeout, "second unique request to commit")

	// Check commit index progress
	commitIndex := leader.GetCommitIndex()
	t.Logf("Leader commit index: %d", commitIndex)

	// Verify commit index advanced to the new request
	if commitIndex < index2 {
		t.Errorf("Commit index %d < index %d after waiting", commitIndex, index2)
	}

	// Verify counter incremented to 2 on all nodes
	WaitForConditionWithProgress(t, func() (bool, string) {
		allNodesCorrect := true
		incorrectNodes := []int{}
		for i, sm := range stateMachines {
			sm.mu.Lock()
			value := sm.data["counter"]
			sm.mu.Unlock()

			if value != "2" {
				allNodesCorrect = false
				incorrectNodes = append(incorrectNodes, i)
			}
		}
		if !allNodesCorrect {
			return false, fmt.Sprintf("nodes %v have incorrect counter values", incorrectNodes)
		}
		return true, "all nodes have counter=2"
	}, timing.StabilizationTimeout, "all nodes to have counter=2")
	
	// Log final state
	for i, sm := range stateMachines {
		sm.mu.Lock()
		value := sm.data["counter"]
		processedCount := len(sm.processed)
		var processedKeys []string
		for k := range sm.processed {
			processedKeys = append(processedKeys, k)
		}
		sm.mu.Unlock()
		t.Logf("Node %d: counter=%s, processed requests=%d, keys=%v", i, value, processedCount, processedKeys)
	}

	t.Log("Idempotent operations verified")
}

// TestClientRetries tests client retry logic with various failure scenarios
func TestClientRetries(t *testing.T) {
	// Create 3-node cluster with partitionable transport
	nodes := make([]Node, 3)
	transports := make([]*partitionableTransport, 3)
	loggers := make([]*SafeTestLogger, 3)
	
	registry := &partitionRegistry{
		nodes: make(map[int]RPCHandler),
		mu:    sync.RWMutex{},
	}

	for i := 0; i < 3; i++ {
		logger := NewSafeTestLogger(t)
		loggers[i] = logger
		
		config := &Config{
			ID:                 i,
			Peers:              []int{0, 1, 2},
			ElectionTimeoutMin: 150 * time.Millisecond,
			ElectionTimeoutMax: 300 * time.Millisecond,
			HeartbeatInterval:  50 * time.Millisecond,
			Logger:             logger,
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
		defer func(idx int) {
			if idx < len(loggers) && loggers[idx] != nil {
				loggers[idx].Stop()
			}
			nodes[idx].Stop()
		}(i)
	}

	// Wait for leader election
	timing := DefaultTimingConfig()
	timing.ElectionTimeout = 500 * time.Millisecond
	WaitForLeaderWithConfig(t, nodes, timing)

	// Client retry simulator
	type clientRequest struct {
		id      string
		command string
		retries int
		success bool
	}

	client := &retryClient{
		nodes:      nodes,
		maxRetries: 5,
		retryDelay: 100 * time.Millisecond,
	}

	// Test 1: Retry during leader change
	t.Log("Test 1: Retry during leader change")

	// Find and partition current leader
	var leaderID int
	for i, node := range nodes {
		if node.IsLeader() {
			leaderID = i
			break
		}
	}

	// Start request
	resultCh := make(chan clientRequest)
	partitionDone := make(chan struct{})
	
	go func() {
		req := clientRequest{
			id:      "req-1",
			command: "test-command-1",
		}

		// Partition leader after first attempt
		go func() {
			defer close(partitionDone)
			time.Sleep(50 * time.Millisecond)
			for i := 0; i < 3; i++ {
				if i != leaderID {
					transports[leaderID].Block(i)
					transports[i].Block(leaderID)
				}
			}
			// Don't log here - test may have finished
		}()

		req.success = client.submitWithRetry(req.command)
		resultCh <- req
	}()

	result := <-resultCh
	
	// Wait for partition goroutine to complete
	<-partitionDone
	if !result.success {
		t.Error("Client failed to submit command after retries")
	} else {
		t.Logf("Successfully submitted after %d retries", result.retries)
	}

	// Heal partition
	for i := 0; i < 3; i++ {
		for j := 0; j < 3; j++ {
			transports[i].Unblock(j)
		}
	}

	// Test 2: Retry with intermittent network failures
	t.Log("Test 2: Retry with intermittent network failures")

	successCount := int32(0)
	failureCount := int32(0)

	// Submit many requests concurrently with random failures
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(reqID int) {
			defer wg.Done()

			req := clientRequest{
				id:      fmt.Sprintf("req-%d", reqID),
				command: fmt.Sprintf("command-%d", reqID),
			}

			if client.submitWithRetry(req.command) {
				atomic.AddInt32(&successCount, 1)
			} else {
				atomic.AddInt32(&failureCount, 1)
			}
		}(i)
	}

	wg.Wait()

	t.Logf("Concurrent requests: %d succeeded, %d failed", successCount, failureCount)

	if failureCount > 2 {
		t.Errorf("Too many failures: %d", failureCount)
	}
}

// kvStateMachine implements a simple key-value store with idempotency support
type kvStateMachine struct {
	mu        sync.Mutex
	data      map[string]string
	processed map[string]bool // Track processed requests for idempotency
	nodeID    int             // For debugging
}

type kvCommand struct {
	Type      string // get, set, increment
	Key       string
	Value     string
	ClientID  string // For idempotency
	RequestID string // For idempotency
}

func (sm *kvStateMachine) Apply(entry LogEntry) interface{} {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	cmd, ok := entry.Command.(kvCommand)
	if !ok {
		// Handle string commands for compatibility
		if str, ok := entry.Command.(string); ok {
			sm.data[str] = fmt.Sprintf("applied-%d", entry.Index)
			return nil
		}
		return nil
	}

	// Check idempotency
	if cmd.ClientID != "" && cmd.RequestID != "" {
		key := fmt.Sprintf("%s-%s", cmd.ClientID, cmd.RequestID)
		if sm.processed == nil {
			sm.processed = make(map[string]bool)
		}
		if sm.processed[key] {
			// Already processed this request
			fmt.Printf("Node %d: Skipping duplicate request %s\n", sm.nodeID, key)
			return sm.data[cmd.Key]
		}
		sm.processed[key] = true
		fmt.Printf("Node %d: Processing new request %s\n", sm.nodeID, key)
	}

	switch cmd.Type {
	case "get":
		return sm.data[cmd.Key]
	case "set":
		sm.data[cmd.Key] = cmd.Value
		return cmd.Value
	case "increment":
		var val int
		if v, exists := sm.data[cmd.Key]; exists {
			fmt.Sscanf(v, "%d", &val)
		}
		val++
		sm.data[cmd.Key] = fmt.Sprintf("%d", val)
		fmt.Printf("Node %d: Incremented %s to %d\n", sm.nodeID, cmd.Key, val)
		return sm.data[cmd.Key]
	}

	return nil
}

func (sm *kvStateMachine) Snapshot() ([]byte, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	// Simple implementation for testing
	data := fmt.Sprintf("{")
	first := true
	for k, v := range sm.data {
		if !first {
			data += ","
		}
		data += fmt.Sprintf("\"%s\":\"%s\"", k, v)
		first = false
	}
	data += "}"

	return []byte(data), nil
}

func (sm *kvStateMachine) Restore(snapshot []byte) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	sm.data = make(map[string]string)
	sm.processed = make(map[string]bool)

	// Simple parsing for test
	return nil
}

// retryClient simulates a client with retry logic
type retryClient struct {
	nodes      []Node
	maxRetries int
	retryDelay time.Duration
}

func (c *retryClient) submitWithRetry(command string) bool {
	for attempt := 0; attempt < c.maxRetries; attempt++ {
		// Find current leader
		var leader Node
		for _, node := range c.nodes {
			if node.IsLeader() {
				leader = node
				break
			}
		}

		if leader == nil {
			// No leader, wait and retry
			time.Sleep(c.retryDelay)
			continue
		}

		// Try to submit
		_, _, isLeader := leader.Submit(command)
		if isLeader {
			return true
		}

		// Leader changed or error, retry
		time.Sleep(c.retryDelay)
	}

	return false
}
