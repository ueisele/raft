# Integration Test Helpers Guide

This guide provides a comprehensive overview of the test helpers available in `integration/helpers` for writing Raft integration tests.

## 📁 Directory Structure

```
integration/helpers/
├── cluster.go                 # Main TestCluster implementation
├── assertions.go              # Test assertion utilities
├── timing.go                  # Timing and waiting utilities
├── network.go                 # Network utilities (port allocation)
├── transport_options.go       # Convenient transport decorator options
└── transporttest/             # Transport testing infrastructure
    ├── transport_multinode.go # In-memory transport for tests
    ├── registry.go            # Node registry for transport routing
    ├── capabilities.go        # Capability extraction utilities
    ├── decorator.go           # Base decorator infrastructure
    ├── partition_decorator.go # Network partition simulation
    ├── failure_decorator.go   # Failure injection
    ├── delay_decorator.go     # Network delay simulation
    └── debug_decorator.go     # Debug logging transport
```

## 🎯 Core Components

### 1. TestCluster (`cluster.go`)

The central component for creating and managing Raft clusters in tests.

#### Basic Usage
```go
// Create a 3-node cluster that auto-starts
cluster := helpers.NewTestCluster(t, []int{0, 1, 2}, 
    helpers.WithClusterAutoStart())

// Wait for leader election
leaderID, err := cluster.WaitForLeader(2 * time.Second)

// Submit commands through the leader
index, term, err := cluster.SubmitToLeader("my-command")

// Wait for replication
cluster.WaitForCommitIndex(index, time.Second)
```

#### Key Methods

**Cluster Management:**
- `Start()` - Start all nodes
- `Stop()` - Stop all nodes (automatic via t.Cleanup)
- `AddNode(nodeID, peers)` - Dynamically add a node
- `RemoveNode(nodeID)` - Remove a node from cluster

**Node-Specific Operations:**
- `StartNode(nodeID)` - Start specific node
- `StopNode(nodeID)` - Stop specific node
- `RestartNode(nodeID)` - Restart node with same config
- `SubmitToNode(cmd, nodeID)` - Submit to specific node

**Command Submission:**
- `SubmitToLeader(cmd)` - Submit to current leader
- `SubmitToNode(cmd, nodeID)` - Submit to specific node

**Inspection:**
- `GetNodes()` - Get map of all nodes
- `GetNode(nodeID)` - Get specific node
- `GetTransports()` - Get all transports
- `GetPersistences()` - Get all persistences
- `GetStateMachines()` - Get all state machines
- `GetLeader()` - Get current leader node and ID
- `NodeCount()` - Number of nodes
- `NodeIDs()` - List of node IDs

**Waiting:**
- `WaitForLeader(timeout)` - Wait for leader election
- `WaitForCommitIndex(index, timeout)` - Wait for replication

### 2. Configuration Options

#### Timing Configuration
```go
cluster := helpers.NewTestCluster(t, nodeIDs,
    helpers.WithElectionTimeout(200*time.Millisecond, 400*time.Millisecond),
    helpers.WithHeartbeatInterval(100*time.Millisecond),
    helpers.WithMaxLogSize(1000),
)
```

#### Component Factories
```go
cluster := helpers.NewTestCluster(t, nodeIDs,
    // Custom transport
    helpers.WithTransportFactory(func(nodeID int, registry *transporttest.NodeRegistry) (raft.Transport, error) {
        return customTransport, nil
    }),
    
    // Custom persistence
    helpers.WithPersistenceFactory(func(nodeID int) (raft.Persistence, error) {
        return customPersistence, nil
    }),
    
    // Custom state machine
    helpers.WithStateMachineFactory(func(nodeID int) (raft.StateMachine, error) {
        return customStateMachine, nil
    }),
)
```

#### Pre-configured Options
```go
// Use mock persistence (default)
helpers.WithMockPersistence()

// Use JSON persistence
helpers.WithJSONPersistence("/tmp/raft-test")

// Use mock state machine (default)
helpers.WithMockStateMachine()

// Auto-start all nodes
helpers.WithClusterAutoStart()
```

### 3. Transport Decorators

Transport decorators add network behavior simulation:

#### Network Partitions
```go
cluster := helpers.NewTestCluster(t, nodeIDs,
    helpers.WithPartitionableTransport())

// Create partition: [0] | [1, 2]
transporttest.CreatePartition(cluster, []int{0}, []int{1, 2})

// Heal partition
transporttest.HealPartition(cluster)

// Or use capability interface
if partition, ok := transporttest.GetCapability[transporttest.PartitionCapable](cluster, 0); ok {
    partition.Block(1)      // Block communication to node 1
    partition.Unblock(1)    // Restore communication
}
```

#### Failure Injection
```go
cluster := helpers.NewTestCluster(t, nodeIDs,
    helpers.WithFailureTransport(0.1)) // 10% failure rate

// Adjust failure rate dynamically
if failure, ok := transporttest.GetCapability[transporttest.FailureCapable](cluster, 0); ok {
    failure.SetFailureRate(0.5) // 50% failure
    stats := failure.GetStats()
    t.Logf("Failures: %d/%d", stats.Failures, stats.Attempts)
}
```

#### Network Delays
```go
cluster := helpers.NewTestCluster(t, nodeIDs,
    helpers.WithDelayTransport())

// Set delays between specific nodes
transporttest.SetDelayBetween(cluster, 0, 1, 100*time.Millisecond)

// Or use capability interface
if delay, ok := transporttest.GetCapability[transporttest.DelayCapable](cluster, 0); ok {
    delay.SetDelay(1, 50*time.Millisecond) // 50ms delay to node 1
}
```

#### Debug Logging
```go
cluster := helpers.NewTestCluster(t, nodeIDs,
    helpers.WithDebugTransport(logger))
```

#### Multiple Decorators
```go
cluster := helpers.NewTestCluster(t, nodeIDs,
    helpers.WithTransportDecorators(
        func(nodeID int, wrapped raft.Transport) raft.Transport {
            return transporttest.NewPartitionableDecorator(wrapped)
        },
        func(nodeID int, wrapped raft.Transport) raft.Transport {
            return transporttest.NewFailureDecorator(wrapped, 0.05)
        },
    ),
)
```

### 4. Assertions (`assertions.go`)

Utilities for verifying cluster state:

```go
// Verify leader count
leaderID := helpers.AssertLeaderCount(t, nodes) // Ensures exactly 1 leader

// Verify no leader exists
helpers.AssertNoLeader(t, nodes)

// Verify all nodes have same term
term := helpers.AssertSameTerm(t, nodes)

// Verify commit index
helpers.AssertCommitIndex(t, node, expectedIndex)
helpers.AssertMinCommitIndex(t, nodes, minIndex)

// Verify configuration
helpers.AssertConfiguration(t, nodes, []int{0, 1, 2})

// Verify state machine content (for MockStateMachine)
helpers.AssertStateMachineContent(t, sm, "key", "expected-value")

// Safety properties
helpers.AssertElectionSafety(t, nodes)  // At most 1 leader per term
helpers.AssertLogConsistency(t, nodes, upToIndex)  // Logs match
```

### 5. Timing Utilities (`timing.go`)

Utilities for waiting and timing in tests:

#### Wait Functions
```go
// Wait for condition with timeout
helpers.WaitForCondition(t, func() bool {
    return cluster.IsStable()
}, 5*time.Second, "cluster stabilization")

// Wait with progress updates
helpers.WaitForConditionWithProgress(t, func() (bool, string) {
    count := getNodeCount()
    done := count >= 5
    progress := fmt.Sprintf("nodes: %d/5", count)
    return done, progress
}, 10*time.Second, "scale to 5 nodes")

// Specific wait helpers
helpers.WaitForLeader(t, nodes, timeout)
helpers.WaitForFollower(t, nodes, timeout)  // All become followers
helpers.WaitForCommitIndex(t, nodes, targetIndex, timeout)
helpers.WaitForTerm(t, nodes, targetTerm, timeout)
helpers.WaitForServers(t, nodes, []int{0,1,2}, timeout)
```

#### Assertions Over Time
```go
// Eventually becomes true
helpers.Eventually(t, func() bool {
    return node.IsLeader()
}, 2*time.Second, "node becomes leader")

// Consistently remains true
helpers.Consistently(t, func() bool {
    return node.IsLeader()
}, 1*time.Second, "node stays leader")
```

#### Timing Configurations
```go
// Default timing (balanced)
timing := helpers.DefaultTimingConfig()
// ElectionTimeout: 500ms, HeartbeatInterval: 50ms

// Fast timing (for quick tests)
timing := helpers.FastTimingConfig()
// ElectionTimeout: 100ms, HeartbeatInterval: 10ms
```

### 6. Network Utilities (`network.go`)

```go
// Allocate free ports for real network transports
ports, err := helpers.GetFreePorts(3)
// Returns 3 available TCP ports
```

## 📝 Common Test Patterns

### Pattern 1: Basic Leader Election Test
```go
func TestLeaderElection(t *testing.T) {
    cluster := helpers.NewTestCluster(t, []int{0, 1, 2}, 
        helpers.WithClusterAutoStart())
    
    leaderID, err := cluster.WaitForLeader(2 * time.Second)
    if err != nil {
        t.Fatalf("No leader elected: %v", err)
    }
    
    // Verify only one leader
    helpers.AssertLeaderCount(t, cluster.GetNodes())
    
    // Verify all nodes have same term
    helpers.AssertSameTerm(t, cluster.GetNodes())
}
```

### Pattern 2: Fault Tolerance Test
```go
func TestNodeFailure(t *testing.T) {
    cluster := helpers.NewTestCluster(t, []int{0, 1, 2, 3, 4}, 
        helpers.WithClusterAutoStart())
    
    // Get initial leader
    initialLeader, _ := cluster.WaitForLeader(2 * time.Second)
    
    // Submit commands
    idx, _, _ := cluster.SubmitToLeader("cmd-1")
    cluster.WaitForCommitIndex(idx, time.Second)
    
    // Kill the leader
    cluster.StopNode(initialLeader)
    
    // New leader should be elected
    newLeader, _ := cluster.WaitForLeader(2 * time.Second)
    if newLeader == initialLeader {
        t.Fatal("Same leader after failure")
    }
    
    // Cluster should still work
    idx, _, _ = cluster.SubmitToLeader("cmd-2")
    
    // Restart old leader
    cluster.RestartNode(initialLeader)
    
    // Wait for synchronization
    cluster.WaitForCommitIndex(idx, 2 * time.Second)
}
```

### Pattern 3: Network Partition Test
```go
func TestNetworkPartition(t *testing.T) {
    cluster := helpers.NewTestCluster(t, []int{0, 1, 2, 3, 4},
        helpers.WithPartitionableTransport(),
        helpers.WithClusterAutoStart())
    
    leaderID, _ := cluster.WaitForLeader(2 * time.Second)
    
    // Create partition: minority (leader) | majority
    minority := []int{leaderID}
    majority := []int{}
    for i := 0; i < 5; i++ {
        if i != leaderID {
            majority = append(majority, i)
            if len(majority) >= 3 {
                break
            }
        }
    }
    
    transporttest.CreatePartition(cluster, minority, majority)
    
    // Old leader should step down
    helpers.WaitForFollower(t, []raft.Node{
        cluster.GetNode(leaderID)}, 2*time.Second)
    
    // Majority should elect new leader
    helpers.WaitForCondition(t, func() bool {
        for _, id := range majority {
            if node, _ := cluster.GetNode(id); node.IsLeader() {
                return true
            }
        }
        return false
    }, 2*time.Second, "majority elects leader")
    
    // Heal partition
    transporttest.HealPartition(cluster)
    
    // Cluster should converge
    helpers.WaitForLeader(t, cluster.GetNodes(), 2*time.Second)
    helpers.AssertSameTerm(t, cluster.GetNodes())
}
```

### Pattern 4: Configuration Change Test
```go
func TestAddNode(t *testing.T) {
    // Start with 3 nodes
    cluster := helpers.NewTestCluster(t, []int{0, 1, 2},
        helpers.WithClusterAutoStart())
    
    cluster.WaitForLeader(2 * time.Second)
    
    // Add node 3
    newNode, err := cluster.AddNode(3, []int{0, 1, 2, 3})
    if err != nil {
        t.Fatalf("Failed to add node: %v", err)
    }
    
    // Start the new node
    newNode.Start(context.Background())
    
    // Wait for configuration to propagate
    helpers.WaitForServers(t, cluster.GetNodes(), 
        []int{0, 1, 2, 3}, 2*time.Second)
    
    // Verify new node participates
    idx, _, _ := cluster.SubmitToLeader("after-add")
    cluster.WaitForCommitIndex(idx, 2*time.Second)
    
    // Verify configuration
    helpers.AssertConfiguration(t, cluster.GetNodes(), 
        []int{0, 1, 2, 3})
}
```

### Pattern 5: Performance Under Stress
```go
func TestHighLoad(t *testing.T) {
    cluster := helpers.NewTestCluster(t, []int{0, 1, 2},
        helpers.WithFailureTransport(0.05), // 5% failure rate
        helpers.WithClusterAutoStart())
    
    cluster.WaitForLeader(2 * time.Second)
    
    // Submit many commands concurrently
    var wg sync.WaitGroup
    errors := make(chan error, 100)
    
    for i := 0; i < 10; i++ {
        wg.Add(1)
        go func(id int) {
            defer wg.Done()
            for j := 0; j < 10; j++ {
                cmd := fmt.Sprintf("cmd-%d-%d", id, j)
                _, _, err := cluster.SubmitToLeader(cmd)
                if err != nil {
                    errors <- err
                }
            }
        }(i)
    }
    
    wg.Wait()
    close(errors)
    
    // Check error rate
    errorCount := len(errors)
    if errorCount > 10 {
        t.Errorf("Too many errors: %d/100", errorCount)
    }
    
    // Verify cluster still functional
    helpers.AssertLeaderCount(t, cluster.GetNodes())
    helpers.AssertElectionSafety(t, cluster.GetNodes())
}
```

## 🎨 Best Practices

1. **Always use ClusterAutoStart** for simpler tests:
   ```go
   cluster := helpers.NewTestCluster(t, nodeIDs, 
       helpers.WithClusterAutoStart())
   ```

2. **Use condition-based waiting** instead of fixed sleeps:
   ```go
   // Good ✅
   helpers.WaitForCondition(t, func() bool {
       return node.IsReady()
   }, 2*time.Second, "node ready")
   
   // Bad ❌
   time.Sleep(500 * time.Millisecond)
   ```

3. **Leverage automatic cleanup** via t.Cleanup:
   ```go
   // Cluster automatically stops when test ends
   cluster := helpers.NewTestCluster(t, nodeIDs)
   // No manual cleanup needed!
   ```

4. **Use descriptive test names** and log progress:
   ```go
   t.Run("LeaderFailureWithPendingCommands", func(t *testing.T) {
       t.Log("Setting up 5-node cluster...")
       // test code
       t.Log("Leader failed, waiting for new election...")
       // more test code
   })
   ```

5. **Test both positive and negative cases**:
   ```go
   // Test successful case
   _, _, err := cluster.SubmitToLeader("cmd")
   if err != nil {
       t.Fatalf("Should succeed: %v", err)
   }
   
   // Test failure case
   _, _, err = cluster.SubmitToNode("cmd", followerID)
   if err == nil {
       t.Fatal("Should fail when submitting to follower")
   }
   ```

6. **Use appropriate timing for test type**:
   ```go
   // Fast tests
   cluster := helpers.NewTestCluster(t, nodeIDs,
       helpers.WithElectionTimeout(50*time.Millisecond, 100*time.Millisecond))
   
   // Stability tests
   cluster := helpers.NewTestCluster(t, nodeIDs,
       helpers.WithElectionTimeout(500*time.Millisecond, 1000*time.Millisecond))
   ```

## 🔍 Debugging Tips

1. **Enable debug logging**:
   ```go
   cluster := helpers.NewTestCluster(t, nodeIDs,
       helpers.WithLogger(raft.NewTestLogger(t)),
       helpers.WithDebugTransport(raft.NewTestLogger(t)))
   ```

2. **Use progress reporting** for long operations:
   ```go
   helpers.WaitForConditionWithProgress(t, func() (bool, string) {
       status := getDetailedStatus()
       return status.Ready, status.String()
   }, 30*time.Second, "complex operation")
   ```

3. **Inspect cluster state** when tests fail:
   ```go
   t.Cleanup(func() {
       if t.Failed() {
           nodes := cluster.GetNodes()
           for id, node := range nodes {
               term, isLeader := node.GetState()
               commit := node.GetCommitIndex()
               t.Logf("Node %d: term=%d, leader=%v, commit=%d",
                   id, term, isLeader, commit)
           }
       }
   })
   ```

4. **Use capability inspection** to verify decorators:
   ```go
   for _, id := range cluster.NodeIDs() {
       caps := []string{}
       if _, ok := transporttest.GetCapability[transporttest.PartitionCapable](cluster, id); ok {
           caps = append(caps, "Partition")
       }
       if _, ok := transporttest.GetCapability[transporttest.FailureCapable](cluster, id); ok {
           caps = append(caps, "Failure")
       }
       t.Logf("Node %d capabilities: %v", id, caps)
   }
   ```

This guide covers the essential test helpers for writing comprehensive Raft integration tests. The helpers provide a robust foundation for testing various scenarios including normal operations, failures, network issues, and configuration changes.