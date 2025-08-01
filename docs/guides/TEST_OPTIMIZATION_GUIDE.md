# Test Optimization Guide

This guide explains how to optimize Raft tests by replacing fixed `time.Sleep()` delays with event-based synchronization.

## Why Optimize?

Fixed delays in tests:
- Make tests slow and unreliable
- Can cause flaky tests if delays are too short
- Waste time if delays are too long
- Total test suite time was over 100 seconds due to cumulative sleeps

## Optimization Patterns

### 1. Waiting for Leader Election

**Before:**
```go
time.Sleep(1 * time.Second)
var leader *Raft
for i, rf := range rafts {
    _, isLeader := rf.GetState()
    if isLeader {
        leader = rf.Raft
        break
    }
}
if leader == nil {
    t.Fatal("No leader elected")
}
```

**After:**
```go
leaderIndex := WaitForLeader(t, rafts, 2*time.Second)
leader := rafts[leaderIndex].Raft
```

### 2. Waiting for Log Replication

**Before:**
```go
// Submit commands
for _, cmd := range commands {
    leader.Submit(cmd)
}
time.Sleep(2 * time.Second)
// Check logs...
```

**After:**
```go
// Submit commands
for _, cmd := range commands {
    leader.Submit(cmd)
}
WaitForCommitIndex(t, rafts, len(commands), 2*time.Second)
```

### 3. Waiting for Applied Entries

**Before:**
```go
leader.Submit(command)
time.Sleep(500 * time.Millisecond)
// Manually check apply channel
```

**After:**
```go
leader.Submit(command)
entries := WaitForAppliedEntries(t, applyCh, 1, 1*time.Second)
if entries[0].Command != command {
    t.Fatalf("Wrong command applied")
}
```

### 4. Conditional Waiting

**Before:**
```go
// Wait for some condition
time.Sleep(100 * time.Millisecond)
if !someCondition() {
    t.Fatal("Condition not met")
}
```

**After:**
```go
WaitForCondition(t, func() bool {
    return someCondition()
}, 500*time.Millisecond, "condition description")
```

### 5. Waiting for Heartbeats

**Before:**
```go
// Wait for heartbeats
time.Sleep(100 * time.Millisecond)
```

**After:**
```go
WaitForCondition(t, func() bool {
    rf.mu.Lock()
    defer rf.mu.Unlock()
    return time.Since(rf.lastHeartbeat) < rf.electionTimeoutMin
}, 500*time.Millisecond, "should receive heartbeat")
```

## Available Helper Functions

```go
// Wait for a leader to be elected
WaitForLeader(t testing.TB, rafts []*TestRaft, timeout time.Duration) int

// Wait for a condition to become true
WaitForCondition(t *testing.T, condition func() bool, timeout time.Duration, msg string)

// Wait until there is no leader
WaitForNoLeader(t *testing.T, rafts []*TestRaft, timeout time.Duration)

// Wait for all servers to reach a commit index
WaitForCommitIndex(t *testing.T, rafts []*TestRaft, minIndex int, timeout time.Duration)

// Wait for entries to be applied
WaitForAppliedEntries(t *testing.T, applyCh chan LogEntry, expected int, timeout time.Duration) []LogEntry

// Wait for a server to reach a term
WaitForTerm(t *testing.T, rf *Raft, minTerm int, timeout time.Duration)

// Wait for servers to agree on a log entry
WaitForServersToAgree(t *testing.T, rafts []*TestRaft, index int, timeout time.Duration)

// Wait for an election to complete
WaitForElection(t *testing.T, rafts []*TestRaft, oldTerm int, timeout time.Duration) (int, int)
```

## Best Practices

1. **Use appropriate timeouts**: Generally 1-2 seconds is sufficient for most operations
2. **Check frequently**: Helper functions check every 10ms for quick response
3. **Descriptive messages**: Provide clear failure messages for debugging
4. **Avoid fixed delays**: Only use `time.Sleep()` when testing timing-specific behavior
5. **Batch operations**: When possible, wait for the final state rather than intermediate steps

## Example Conversion

Here's a complete example of converting a test:

**Original Test:**
```go
func TestExample(t *testing.T) {
    // ... setup ...
    
    // Wait for leader
    time.Sleep(1 * time.Second)
    
    // Submit command
    leader.Submit("test")
    time.Sleep(500 * time.Millisecond)
    
    // Check applied
    select {
    case entry := <-applyCh:
        // check entry
    case <-time.After(1 * time.Second):
        t.Fatal("Timeout")
    }
}
```

**Optimized Test:**
```go
func TestExample(t *testing.T) {
    // ... setup ...
    
    // Wait for leader
    leaderIndex := WaitForLeader(t, rafts, 2*time.Second)
    leader := rafts[leaderIndex].Raft
    
    // Submit command
    leader.Submit("test")
    
    // Wait for applied entry
    entries := WaitForAppliedEntries(t, applyCh, 1, 1*time.Second)
    // check entries[0]
}
```

## Results

After optimization:
- Individual test times reduced by 50-90%
- Total test suite time reduced from >100s to <30s
- Tests are more reliable and less flaky
- Easier to debug failures with descriptive messages