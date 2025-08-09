# Wait for Applied Pattern: Design Proposals

## Problem Statement

In Raft, there's a critical distinction between when an entry is **committed** (replicated to a majority) and when it's **applied** (executed by the state machine). Currently, the `Submit()` method returns immediately after an entry is committed, but applications often need to wait until the entry is actually applied to ensure linearizability.

### Current Situation

```
Timeline: Submit → Replicate → Commit → [Client Returns] → Apply → [State Changed]
                                         ↑ Current API      ↑ What apps need
```

Without waiting for application:
- Client submits a PUT operation
- Gets success response when entry is committed
- Immediately issues a GET
- GET might not see the value yet (race condition)

### Why This Matters

This pattern is needed by virtually every production Raft implementation:
- **Key-Value Stores**: Need to ensure writes are visible before acknowledging
- **Databases**: Must guarantee transaction completion
- **Configuration Systems**: Need to confirm changes are applied
- **Service Registries**: Must ensure service registration is complete

## Design Options

### Option 1: Built-in WaitForApplied Method ✅

Add methods to the Node interface for synchronous operations:

```go
type Node interface {
    // Existing method
    Submit(command interface{}) (int, int, bool)
    
    // New methods
    WaitForApplied(index int, timeout time.Duration) (interface{}, error)
    SubmitAndWait(command interface{}, timeout time.Duration) (interface{}, error)
}
```

**Pros:**
- Simple, intuitive API
- Correct by default (encourages linearizable operations)
- Minimal overhead (only tracks actively waited entries)
- Backward compatible
- Single implementation in core library

**Cons:**
- Increases core API surface
- Couples consensus with application semantics
- Memory overhead for tracking results
- **Race condition complexity**: Separate Submit() and WaitForApplied() calls can miss already-applied entries (requires result caching)

**Implementation Complexity:** Medium - Need to handle race between Submit and WaitForApplied

#### Critical Implementation Consideration: Race Condition

When implementing separate `Submit()` and `WaitForApplied()` methods, there's a race condition:

```
Thread 1: Submit(cmd) -> index 5
Thread 2: applyLoop applies index 5
Thread 1: WaitForApplied(5) -> Too late! Already applied
```

**Solution**: Implement a result cache that stores recent apply results (e.g., last 1000 entries) to handle the case where `WaitForApplied` is called after the entry was already applied. This adds memory overhead but ensures correctness.

### Option 2: Future/Promise-Based API 🔄

Return a future object that can be awaited:

```go
type ApplyFuture interface {
    Index() int
    Term() int
    Result() (interface{}, error)
    ResultWithTimeout(timeout time.Duration) (interface{}, error)
    ResultChan() <-chan interface{}
    ErrorChan() <-chan error
}

type Node interface {
    Submit(command interface{}) (int, int, bool)
    SubmitAsync(command interface{}) (ApplyFuture, error)
}
```

**Pros:**
- Modern async/await pattern
- **No race conditions** - future created atomically with submission
- Composable (can pass futures around)
- Clear ownership of result
- Natural timeout handling
- **No result cache needed** - futures hold their own results
- Perfect integration with single-writer pattern

**Cons:**
- Two different submit methods may confuse users
- Future objects need lifecycle management
- More complex API surface

**Implementation Complexity:** Medium - Need to design future lifecycle carefully

#### How It Solves the Race Condition

```go
// Option 1 (has race):
index, _, _ := node.Submit(cmd)      // Submit happens
// Race window here! Entry might be applied
result, err := node.WaitForApplied(index, timeout)  // Might miss it!

// Option 2 (no race):
future := node.SubmitAsync(cmd)      // Future created atomically
result, err := future.Result()       // Can never miss - future exists from start
```

### Option 3: Callback Registration 📞

Allow registering callbacks for applied entries:

```go
type ApplyCallback func(index int, result interface{}, err error)

type Node interface {
    Submit(command interface{}) (int, int, bool)
    OnApplied(index int, callback ApplyCallback)
    SubmitWithCallback(command interface{}, callback ApplyCallback) (int, int, bool)
}
```

**Pros:**
- Event-driven architecture
- No blocking/waiting
- Flexible for different patterns

**Cons:**
- Callback hell potential
- Harder to reason about
- Error handling is complex
- Not idiomatic Go (Go prefers channels/blocking)

**Implementation Complexity:** Medium - Need careful callback management

### Option 4: Optional Wrapper/Middleware 🎁

Keep core simple, provide optional synchronous wrapper:

```go
// In package github.com/ueisele/raft/sync
type SyncNode struct {
    raft.Node
    // internal tracking
}

func NewSyncNode(node raft.Node) *SyncNode
func (s *SyncNode) SubmitAndWait(cmd interface{}, timeout time.Duration) (interface{}, error)
```

**Pros:**
- Core library stays minimal
- Opt-in complexity
- Can evolve independently
- Multiple wrapper implementations possible

**Cons:**
- Users might not discover it
- Requires wrapping state machine
- Fragmentation (each app might implement differently)
- Extra abstraction layer

**Implementation Complexity:** Medium - Need to intercept Apply calls

### Option 5: State Machine Interface Extension 🔌

Extend StateMachine to support notifications:

```go
type NotifyingStateMachine interface {
    StateMachine
    RegisterApplyListener(index int, ch chan<- interface{})
}

// Raft node checks if StateMachine implements NotifyingStateMachine
```

**Pros:**
- State machine controls notification
- No changes to Node interface
- Flexible implementation

**Cons:**
- Complex for state machine implementers
- Easy to get wrong
- Splits responsibility
- Type assertions needed

**Implementation Complexity:** High - Complex interaction between components

### Option 6: Keep As-Is (Do Nothing) 🚫

Leave it to applications to implement their own tracking.

**Pros:**
- Library stays minimal and focused
- No additional complexity
- Maximum flexibility for applications
- No memory overhead in core

**Cons:**
- Every application reimplements the same pattern
- Easy to get wrong (race conditions)
- Poor developer experience
- Not "batteries included"

## Comparison Matrix

| Criteria | Option 1 | Option 2 | Option 3 | Option 4 | Option 5 | Option 6 |
|----------|----------|----------|----------|----------|----------|----------|
| **Ease of Use** | ⭐⭐⭐⭐⭐ | ⭐⭐⭐⭐ | ⭐⭐⭐ | ⭐⭐⭐⭐ | ⭐⭐ | ⭐ |
| **Correctness by Default** | ⭐⭐⭐⭐⭐ | ⭐⭐⭐⭐⭐ | ⭐⭐⭐ | ⭐⭐⭐⭐ | ⭐⭐⭐ | ⭐ |
| **Backward Compatibility** | ⭐⭐⭐⭐⭐ | ⭐⭐⭐⭐⭐ | ⭐⭐⭐⭐⭐ | ⭐⭐⭐⭐⭐ | ⭐⭐⭐⭐ | ⭐⭐⭐⭐⭐ |
| **Implementation Simplicity** | ⭐⭐⭐⭐ | ⭐⭐⭐ | ⭐⭐⭐ | ⭐⭐⭐ | ⭐⭐ | ⭐⭐⭐⭐⭐ |
| **Performance** | ⭐⭐⭐⭐ | ⭐⭐⭐⭐ | ⭐⭐⭐⭐⭐ | ⭐⭐⭐ | ⭐⭐⭐ | ⭐⭐⭐⭐⭐ |
| **Go Idiomatic** | ⭐⭐⭐⭐⭐ | ⭐⭐⭐ | ⭐⭐ | ⭐⭐⭐⭐ | ⭐⭐⭐ | ⭐⭐⭐⭐ |

## Recommendation: Option 2 - Future/Promise-Based API ✅

After careful analysis and considering the planned single-writer pattern implementation, I strongly recommend **Option 2: Future/Promise-Based API** for the following reasons:

### Why Option 2 is Best

1. **No Race Conditions**: The future is created atomically with the submit operation, completely eliminating the race condition between Submit() and WaitForApplied() that plagues Option 1.

2. **Perfect Fit with Single-Writer Pattern**: Integrates seamlessly with the channel-based eventLoop pattern, where futures can be created and tracked naturally without additional locking.

3. **Proven Pattern**: Industry-standard Raft implementations use futures:
   - HashiCorp Raft: `ApplyLog()` returns a `Future`
   - This is the most battle-tested approach in production

4. **Clean API Design**: 
   - One method for async operations: `SubmitAsync() → Future`
   - Future encapsulates all waiting logic
   - No need for separate WaitForApplied method
   - No result cache needed (unlike Option 1)

5. **Maximum Flexibility**: Clients can:
   - Block immediately: `future.Result()`
   - Set timeout: `future.ResultWithTimeout(5*time.Second)`
   - Combine with other operations: `select` on multiple futures
   - Pass futures between goroutines

6. **Lower Memory Overhead**: No need for a result cache since futures are created upfront and hold their own result channels.

### Implementation Approach

```go
// Future represents an async operation that will complete when applied
type ApplyFuture interface {
    // Index returns the log index of this operation
    Index() int
    
    // Term returns the term when this operation was submitted
    Term() int
    
    // Result blocks until the operation is applied and returns the result
    Result() (interface{}, error)
    
    // ResultWithTimeout blocks with a timeout
    ResultWithTimeout(timeout time.Duration) (interface{}, error)
    
    // ResultChan returns the channel for advanced use cases (select statements)
    ResultChan() <-chan interface{}
    ErrorChan() <-chan error
}

// Concrete implementation
type applyFuture struct {
    index    int
    term     int
    resultCh chan interface{}
    errorCh  chan error
}

func (f *applyFuture) Result() (interface{}, error) {
    select {
    case result := <-f.resultCh:
        return result, nil
    case err := <-f.errorCh:
        return nil, err
    }
}

func (f *applyFuture) ResultWithTimeout(timeout time.Duration) (interface{}, error) {
    select {
    case result := <-f.resultCh:
        return result, nil
    case err := <-f.errorCh:
        return nil, err
    case <-time.After(timeout):
        return nil, ErrTimeout
    }
}

// Addition to Node interface
type Node interface {
    // ... existing methods ...
    
    // Async submit that returns a future
    SubmitAsync(command interface{}) (ApplyFuture, error)
    
    // Keep Submit for backward compatibility
    Submit(command interface{}) (int, int, bool)
}
```

The implementation with single-writer pattern:
- **eventLoop** creates futures when processing submit requests
- Futures are tracked in a `map[int]*ApplyFuture`
- **applyLoop** sends results through the future's channel
- No result cache needed - futures ARE the result holders
- Clean separation: consensus logic | future tracking | client waiting

#### Client Usage Patterns

```go
// Pattern A: Simple blocking wait
future := node.SubmitAsync(cmd)
result, err := future.Result()
if err != nil {
    return fmt.Errorf("command failed: %w", err)
}

// Pattern B: Timeout handling
future := node.SubmitAsync(cmd)
result, err := future.ResultWithTimeout(5 * time.Second)
if err == ErrTimeout {
    return fmt.Errorf("operation timed out")
}

// Pattern C: Batch operations
futures := make([]ApplyFuture, len(commands))
for i, cmd := range commands {
    futures[i], _ = node.SubmitAsync(cmd)
}
// Do other work...
for i, future := range futures {
    results[i], _ = future.Result()
}

// Pattern D: Select with cancellation
future := node.SubmitAsync(cmd)
select {
case result := <-future.ResultChan():
    // Process result
case err := <-future.ErrorChan():
    // Handle error
case <-ctx.Done():
    // Cancelled by context
    return ctx.Err()
}
```

### Migration Path

1. **Phase 1**: Add new methods, mark as experimental
2. **Phase 2**: Update examples to use `SubmitAndWait` where appropriate
3. **Phase 3**: After stabilization, recommend as best practice in docs

### Integration with Single-Writer Pattern

The Future API works beautifully with the single-writer pattern from `CONCURRENCY_PATTERNS.md`:

```go
type submitRequest struct {
    command interface{}
    future  *applyFuture  // Return the future, not a response channel
}

type appliedEntry struct {
    index  int
    result interface{}
}

func (n *raftNode) SubmitAsync(cmd interface{}) (ApplyFuture, error) {
    future := &applyFuture{
        resultCh: make(chan interface{}, 1),
        errorCh:  make(chan error, 1),
    }
    
    req := submitRequest{
        command: cmd,
        future:  future,
    }
    
    select {
    case n.submitCh <- req:
        return future, nil
    case <-n.ctx.Done():
        return nil, ErrNodeStopped
    }
}

func (n *raftNode) eventLoop() {
    pendingFutures := make(map[int]*applyFuture)
    
    for {
        select {
        case req := <-n.submitCh:
            // Process submit without locks
            if n.state != Leader {
                req.future.errorCh <- ErrNotLeader
                continue
            }
            
            // Create log entry
            index := n.log.AppendEntry(req.command)
            req.future.index = index
            req.future.term = n.currentTerm
            
            // Track future for when entry is applied
            pendingFutures[index] = req.future
            
        case applied := <-n.appliedCh:
            // Entry was applied by applyLoop
            if future, ok := pendingFutures[applied.index]; ok {
                future.resultCh <- applied.result
                delete(pendingFutures, applied.index)
            }
            
        case <-n.ctx.Done():
            // Clean shutdown - notify all pending futures
            for _, future := range pendingFutures {
                future.errorCh <- ErrNodeStopped
            }
            return
        }
    }
}

// applyLoop sends applied entries to the eventLoop
func (n *raftNode) applyLoop() {
    for {
        select {
        case <-n.applyNotify:
            // Apply committed entries
            for _, entry := range entriesToApply {
                result := n.stateMachine.Apply(entry)
                
                // Notify eventLoop about applied entry
                select {
                case n.appliedCh <- appliedEntry{
                    index:  entry.Index,
                    result: result,
                }:
                case <-n.ctx.Done():
                    return
                }
            }
        case <-n.ctx.Done():
            return
        }
    }
}
```

#### Key Benefits of This Design

1. **Clean Separation of Concerns**:
   - `eventLoop`: Handles submit, creates futures, tracks pending operations
   - `applyLoop`: Applies entries, sends results back to eventLoop
   - `Future`: Encapsulates the async operation and waiting logic
   - Client code remains simple and intuitive

2. **No Shared Memory**:
   - All communication via channels
   - No mutex contention
   - Follows Go's "Don't communicate by sharing memory; share memory by communicating"

3. **Graceful Shutdown**:
   - All pending futures are notified on shutdown
   - No goroutine leaks
   - Clean error propagation

### Alternative Consideration

If there's strong preference for the simplicity of Option 1's API surface, it could be implemented with a result cache to handle the race condition. However, this would:
- Require additional memory for the cache
- Add complexity to determine cache size and eviction policy
- Still not integrate as cleanly with the single-writer pattern

## Conclusion

The "Wait for Applied" pattern is fundamental enough to warrant first-class support in the Raft library. **Option 2 (Future/Promise API)** provides the best solution because it:

1. **Eliminates race conditions** that would require complex workarounds in Option 1
2. **Integrates perfectly** with the planned single-writer concurrency pattern
3. **Follows proven patterns** from production Raft implementations (HashiCorp Raft)
4. **Provides maximum flexibility** for different client use cases
5. **Requires no result caching**, keeping memory overhead minimal

By implementing futures at the library level, we:
- Eliminate an entire class of race condition bugs
- Provide a clean, composable API
- Enable both synchronous and asynchronous usage patterns
- Align with Go's concurrency best practices (channels over shared memory)

The implementation complexity is comparable to Option 1, but the resulting API is more robust and flexible.