# Integration Tests Refactoring Plan

## Overview
This plan outlines the refactoring needed to update all integration tests to use the new helpers API properly.

## Current Issues Identified

### 1. **Direct Field Access** (4 occurrences)
- `cluster.nodes[]` - Should use `cluster.GetNode()` or `cluster.GetNodes()`
- Files affected:
  - `fault_tolerance/persistence_test.go`

### 2. **Direct Transport/Component Access** (19 occurrences)
- Direct access to `transports[]` arrays
- Should use `cluster.GetTransport()` or transport decorators
- Files affected:
  - `fault_tolerance/partition_test.go`
  - `fault_tolerance/persistence_test.go`

### 3. **Fixed time.Sleep Usage** (151 occurrences!)
- Heavy use of `time.Sleep()` instead of condition-based waiting
- Major files affected:
  - `transport/http_transport_test.go` (17 occurrences)
  - `configuration/edge_cases_test.go` (8 occurrences)
  - `snapshot/advanced_test.go` (5 occurrences)
  - Many others with 1-3 occurrences

### 4. **Manual Node Creation** (28 occurrences)
- Direct calls to `raft.NewNode()` instead of using TestCluster
- Should use TestCluster for all node management
- Files affected:
  - `transport/` tests
  - `fault_tolerance/` tests
  - `configuration/` tests

### 5. **Old API Usage**
- `WaitForStableCluster` (5 occurrences) - deprecated
- `SubmitCommand` already replaced with `SubmitToLeader`

## Refactoring Categories

### Category A: Critical - Compilation/Runtime Issues
1. **Direct field access** (`cluster.nodes[]`)
2. **Missing helper methods**

### Category B: High Priority - Test Reliability
1. **Replace time.Sleep with condition-based waiting**
2. **Use TestCluster for all node management**

### Category C: Medium Priority - Best Practices
1. **Use transport decorators instead of custom transports**
2. **Leverage assertion helpers**
3. **Use timing configurations**

### Category D: Low Priority - Code Quality
1. **Consistent error handling patterns**
2. **Better test documentation**

## File-by-File Refactoring Plan

### 1. `fault_tolerance/persistence_test.go`
**Issues:**
- Direct access to `cluster.nodes[]` (lines 338, 340, 462, 597)
- Manual node creation
- Custom persistence implementation

**Fixes:**
- Replace `cluster.nodes[leaderID].Submit()` with `cluster.SubmitToNode(cmd, leaderID)`
- Replace `cluster.nodes[leaderID].Stop()` with `cluster.StopNode(leaderID)`
- Use TestCluster throughout instead of manual node creation
- Use `WithPersistenceFactory` for custom persistence

### 2. `fault_tolerance/partition_test.go`
**Issues:**
- Custom transport arrays and manual blocking
- Direct transport manipulation

**Fixes:**
- Use `WithPartitionableTransport()` option
- Use `transporttest.CreatePartition()` and `transporttest.HealPartition()`
- Replace custom asymmetric transport with decorator pattern

### 3. `transport/http_transport_test.go`
**Issues:**
- 17 `time.Sleep()` calls
- Manual node creation
- Not using TestCluster

**Fixes:**
- Replace all `time.Sleep()` with `helpers.WaitForCondition()`
- Migrate to TestCluster with custom transport factory
- Use condition-based waiting for connection establishment

### 4. `configuration/edge_cases_test.go`
**Issues:**
- 8 `time.Sleep()` calls
- Complex timing scenarios

**Fixes:**
- Replace sleeps with `helpers.WaitForCondition()`
- Use `helpers.Eventually()` and `helpers.Consistently()`
- Add progress reporting for long operations

### 5. `snapshot/` tests
**Issues:**
- Sleep usage in snapshot timing
- Manual snapshot triggering

**Fixes:**
- Use `helpers.WaitForCondition()` for snapshot completion
- Add progress reporting for snapshot operations

### 6. `configuration/safe_addition_test.go`
**Issues:**
- Polling with sleep (line 275)

**Fixes:**
- Replace with `helpers.WaitForServers()`

### 7. All other test files
**Common fixes:**
- Replace `time.Sleep()` with appropriate wait helpers
- Use TestCluster API consistently
- Leverage assertion helpers

## Implementation Strategy

### Phase 1: Critical Fixes (Compilation/Runtime)
1. Fix direct field access in `persistence_test.go`
2. Ensure all tests compile and run

### Phase 2: Transport Refactoring
1. Migrate `partition_test.go` to use transport decorators
2. Update asymmetric partition tests

### Phase 3: Sleep Elimination
1. Start with files with most sleeps
2. Replace with condition-based waiting
3. Add progress reporting where needed

### Phase 4: Test Cluster Migration
1. Migrate tests still using manual node creation
2. Ensure consistent use of TestCluster API

### Phase 5: Assertion and Helper Adoption
1. Use assertion helpers throughout
2. Add better error messages
3. Improve test documentation

## Specific Refactoring Patterns

### Pattern 1: Replace Direct Node Access
```go
// OLD
cluster.nodes[leaderID].Submit("cmd")
cluster.nodes[nodeID].Stop(ctx)

// NEW
cluster.SubmitToNode("cmd", leaderID)
cluster.StopNode(nodeID)
```

### Pattern 2: Replace time.Sleep
```go
// OLD
time.Sleep(500 * time.Millisecond)

// NEW
helpers.WaitForCondition(t, func() bool {
    return cluster.IsStable()
}, time.Second, "cluster stabilization")
```

### Pattern 3: Use Transport Decorators
```go
// OLD
transports[i].blockOutgoingTo(j)

// NEW
cluster := NewTestCluster(t, nodes, WithPartitionableTransport())
transporttest.CreatePartition(cluster, []int{i}, []int{j})
```

### Pattern 4: Manual Node Creation to TestCluster
```go
// OLD
for i := 0; i < 3; i++ {
    node, _ := raft.NewNode(config, transport, persistence, sm)
    nodes[i] = node
}

// NEW
cluster := NewTestCluster(t, []int{0, 1, 2},
    WithCustomTransport(...),
    WithCustomPersistence(...),
    WithClusterAutoStart())
```

### Pattern 5: Use Assertion Helpers
```go
// OLD
leaderCount := 0
for _, node := range nodes {
    if node.IsLeader() {
        leaderCount++
    }
}
if leaderCount != 1 {
    t.Errorf("Expected 1 leader, got %d", leaderCount)
}

// NEW
helpers.AssertLeaderCount(t, cluster.GetNodes())
```

## Success Criteria

1. **No compilation errors** after refactoring
2. **All tests pass** reliably
3. **No time.Sleep usage** except where absolutely necessary
4. **Consistent use of TestCluster API**
5. **Better test reliability** (less flakiness)
6. **Improved test readability** and maintainability

## Estimated Effort

- Phase 1: 1 hour (critical fixes)
- Phase 2: 2 hours (transport refactoring)
- Phase 3: 4 hours (sleep elimination)
- Phase 4: 3 hours (TestCluster migration)
- Phase 5: 2 hours (assertions and polish)

**Total: ~12 hours of refactoring**

## Priority Order

1. **fault_tolerance/persistence_test.go** - Has compilation issues
2. **fault_tolerance/partition_test.go** - Complex transport usage
3. **transport/http_transport_test.go** - Most time.Sleep usage
4. **configuration/edge_cases_test.go** - Complex timing
5. **snapshot/advanced_test.go** - Snapshot timing
6. All remaining files with time.Sleep
7. General cleanup and assertion adoption