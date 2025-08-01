# Best Practices for Writing Timing-Resilient Raft Tests

## Overview

Distributed system tests are inherently timing-sensitive. This guide provides best practices for writing tests that are reliable across different environments and load conditions.

## Key Principles

### 1. Never Use Fixed Sleep Durations

❌ **Bad:**
```go
// Submit command
leader.Submit("cmd")
time.Sleep(500 * time.Millisecond)  // Hope it's replicated by now
// Check result
```

✅ **Good:**
```go
// Submit command
index, _, _ := leader.Submit("cmd")
// Wait for specific condition
test.WaitForCommitIndex(t, nodes, index, 5*time.Second)
```

### 2. Use Condition-Based Waiting

Always wait for specific conditions rather than arbitrary time periods.

```go
// Wait for leader election
leaderID := test.WaitForLeader(t, nodes, 2*time.Second)

// Wait for replication
test.WaitForCommitIndex(t, nodes, targetIndex, 3*time.Second)

// Wait for custom condition
test.WaitForCondition(t, func() bool {
    return node.GetState() == Follower
}, 1*time.Second, "node should become follower")
```

### 3. Use Progress Tracking

For long-running operations, track progress to detect stuck states:

```go
test.WaitForConditionWithProgress(t, func() (bool, string) {
    count := getReplicatedCount()
    return count >= expected, fmt.Sprintf("replicated to %d/%d nodes", count, expected)
}, 5*time.Second, "replication should complete")
```

### 4. Use Appropriate Timing Configurations

Different test scenarios need different timing:

```go
// Fast tests (unit tests, simple scenarios)
timing := test.FastTimingConfig()

// Normal tests (integration tests)
timing := test.DefaultTimingConfig()

// Slow tests (stress tests, partition scenarios)
timing := test.SlowTimingConfig()

// Custom timing
timing := &test.TimingConfig{
    PollInterval:       10 * time.Millisecond,
    ElectionTimeout:    2 * time.Second,
    ReplicationTimeout: 5 * time.Second,
}
```

### 5. Test Stability with Consistently

Verify that conditions remain stable:

```go
// Ensure single leader remains stable
test.Consistently(t, func() bool {
    return countLeaders(nodes) == 1
}, 2*time.Second, "should maintain single leader")
```

### 6. Use Eventually for Async Operations

For operations that should complete eventually:

```go
test.Eventually(t, func() bool {
    return allNodesConverged(nodes)
}, 10*time.Second, "all nodes should converge")
```

### 7. Implement Retries for Flaky Operations

Some operations might fail due to timing:

```go
err := test.RetryWithBackoff(t, func() error {
    return performFlakyOperation()
}, 3, 100*time.Millisecond)
```

## Common Patterns

### Waiting for Leader Election

```go
// Simple wait
leaderID := test.WaitForLeader(t, nodes, 2*time.Second)

// Wait for stable leader (no elections for a period)
leaderID := test.WaitForStableLeader(t, nodes, timing)

// Wait for new election after term
newLeader, newTerm := test.WaitForElection(t, nodes, oldTerm, 5*time.Second)
```

### Waiting for Replication

```go
// Wait for all nodes to reach commit index
test.WaitForCommitIndex(t, nodes, targetIndex, 3*time.Second)

// Wait for majority replication
test.WaitForReplication(t, nodes, index, timing)

// Wait for specific node
test.WaitForLogLength(t, node, expectedLength, 2*time.Second)
```

### Testing Partitions

```go
// Create partition
createPartition(transports, group1, group2)

// Wait for each partition to stabilize
test.Eventually(t, func() bool {
    return partitionHasLeader(group1) && !partitionHasLeader(group2)
}, timing.ElectionTimeout, "partitions should stabilize correctly")

// Heal partition
healPartition(transports)

// Wait for reunified cluster
test.WaitForStableCluster(t, nodes, timing.StabilizationTimeout)
```

### Testing Configuration Changes

```go
// Add server
err := leader.AddServer(newID, addr, false)

// Wait for configuration to propagate
test.Eventually(t, func() bool {
    for _, node := range nodes {
        if !nodeHasServer(node, newID) {
            return false
        }
    }
    return true
}, timing.ReplicationTimeout, "configuration should propagate")
```

## Anti-Patterns to Avoid

### 1. Racing Against Time
```go
// ❌ Bad: Assumes operation completes in fixed time
go func() {
    time.Sleep(100 * time.Millisecond)
    node.Stop()
}()
time.Sleep(200 * time.Millisecond)
// Check something that depends on stop
```

### 2. Cascading Sleeps
```go
// ❌ Bad: Multiple fixed sleeps
startCluster()
time.Sleep(500 * time.Millisecond)  // Wait for start
submitCommand()
time.Sleep(500 * time.Millisecond)  // Wait for replication
checkResult()
time.Sleep(500 * time.Millisecond)  // Wait for apply
```

### 3. Ignoring Progress
```go
// ❌ Bad: No visibility into what's happening
time.Sleep(10 * time.Second)  // Just wait a long time
if !condition() {
    t.Fatal("Condition not met")  // No idea why it failed
}
```

## Environment Considerations

### CI/CD Systems
- Use longer timeouts in CI (2-3x normal)
- Enable debug logging for failures
- Consider retry at test level for known flaky tests

### Local Development
- Use fast timing configuration
- Provide environment variable overrides
- Enable verbose progress tracking

### Load Testing
- Use slow timing configuration
- Monitor for timing-related failures
- Collect timing metrics

## Example: Refactoring a Timing-Sensitive Test

### Before:
```go
func TestLeaderElection(t *testing.T) {
    nodes := createCluster(3)
    startNodes(nodes)
    
    time.Sleep(500 * time.Millisecond)  // Wait for election
    
    leaderCount := 0
    for _, node := range nodes {
        if node.IsLeader() {
            leaderCount++
        }
    }
    
    if leaderCount != 1 {
        t.Fatal("Expected exactly one leader")
    }
}
```

### After:
```go
func TestLeaderElection(t *testing.T) {
    timing := test.DefaultTimingConfig()
    nodes := createCluster(3)
    startNodes(nodes)
    
    // Wait for election with progress tracking
    leaderID := test.WaitForStableLeader(t, nodes, timing)
    t.Logf("Leader elected: node %d", leaderID)
    
    // Ensure leadership remains stable
    test.Consistently(t, func() bool {
        return countLeaders(nodes) == 1
    }, timing.ElectionTimeout, "should maintain single leader")
}
```

## Summary

Writing timing-resilient tests requires:
1. Explicit waiting for conditions
2. Progress tracking and visibility
3. Appropriate timeouts for different scenarios
4. Retry mechanisms for inherently flaky operations
5. Consistent patterns across the test suite

By following these practices, tests become more reliable, easier to debug, and portable across different environments.