# Test Helper Migration Guide

## Overview

We're consolidating test helpers from `helpers.go` and `timing_helpers.go` into a single, well-organized `wait_helpers.go` file.

## Migration Steps

### 1. Update Imports

Replace:
```go
import "github.com/ueisele/raft/test"
```

With (if you need timing configs):
```go
import "github.com/ueisele/raft/test"
```
No change needed in imports, but you may want to use the enhanced versions.

### 2. Function Replacements

| Old Function | New Function | Notes |
|-------------|--------------|-------|
| `test.WaitForLeader()` | `test.WaitForLeaderWithConfig()` | Use with `DefaultTimingConfig()` |
| `test.WaitForCommitIndex()` | `test.WaitForCommitIndexWithConfig()` | Adds progress tracking |
| `test.WaitForCondition()` | `test.WaitForConditionWithProgress()` | Better diagnostics |

### 3. Using Timing Configurations

Instead of hardcoded timeouts:
```go
// Old
test.WaitForLeader(t, nodes, 2*time.Second)

// New - more flexible
timing := test.DefaultTimingConfig()
test.WaitForLeaderWithConfig(t, nodes, timing)
```

### 4. Deprecated Functions

The following functions are deprecated but still available for backward compatibility:
- `WaitForLeader` - Use `WaitForLeaderWithConfig`
- `WaitForCommitIndex` - Use `WaitForCommitIndexWithConfig`
- `WaitForCondition` - Use `WaitForConditionWithProgress`

## Benefits of Migration

1. **Configurable Timing**: Adapt to different environments (CI, local, slow systems)
2. **Progress Tracking**: Better visibility into what's happening during waits
3. **Consistent Polling**: All functions use the same configurable poll interval
4. **Better Diagnostics**: Enhanced error messages with current state

## Example Migration

### Before:
```go
func TestSomething(t *testing.T) {
    // ... setup ...
    
    // Wait for leader
    time.Sleep(500 * time.Millisecond)
    
    // Submit command
    index, _, _ := leader.Submit("cmd")
    
    // Wait for replication
    time.Sleep(1 * time.Second)
    
    // Check result
    if nodes[0].GetCommitIndex() < index {
        t.Error("Not replicated")
    }
}
```

### After:
```go
func TestSomething(t *testing.T) {
    timing := test.DefaultTimingConfig()
    // ... setup ...
    
    // Wait for leader with progress tracking
    leaderID := test.WaitForStableLeader(t, nodes, timing)
    leader := nodes[leaderID]
    
    // Submit command
    index, _, _ := leader.Submit("cmd")
    
    // Wait for replication with diagnostics
    test.WaitForCommitIndexWithConfig(t, nodes, index, timing)
    
    // Result is guaranteed by wait function
}
```

## Cleanup Plan

1. **Phase 1**: Create `wait_helpers.go` with all functionality ✓
2. **Phase 2**: Update tests to use new functions (in progress)
3. **Phase 3**: Mark old files as deprecated
4. **Phase 4**: Remove `helpers.go` and `timing_helpers.go` after migration