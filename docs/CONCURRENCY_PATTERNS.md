# Go Concurrency Patterns for Raft Implementation

## Current Issues Analysis

### 1. Remaining Problems (as of 2025-01-09)
- Multi-node clusters fail to communicate during simultaneous startup
- Python test suite times out even for single-node tests
- Potential goroutine leaks (heartbeat timers not properly cleaned up)
- Complex lock hierarchy risks future deadlocks

### 2. Root Causes
- **Startup Race**: HTTP servers may not be listening when peers try to connect
- **Commit Not Advancing**: Single-node clusters may not auto-advance commit index
- **Lock Complexity**: Multiple nested locks across components

## Recommended Tools

### 1. Deadlock Detection
```bash
# Install go-deadlock for development
go get github.com/sasha-s/go-deadlock
```

Replace `sync.Mutex` with `deadlock.Mutex` in development builds:
```go
// +build debug

package raft

import "github.com/sasha-s/go-deadlock"

type Mutex = deadlock.Mutex
type RWMutex = deadlock.RWMutex
```

### 2. Race Detection (Already in CLAUDE.md)
```bash
go test -race ./...
go build -race ./...
```

### 3. Goroutine Leak Detection
```go
func TestNoGoroutineLeaks(t *testing.T) {
    defer goleak.VerifyNone(t)
    
    // Run test
    node := createNode()
    node.Start()
    node.Stop()
    // goleak will fail if goroutines leak
}
```

## Concurrency Patterns

### 1. Single-Writer Pattern
Instead of complex locking, use one goroutine as the writer:

```go
type RaftNode struct {
    // Commands are serialized through a channel
    submitCh chan submitRequest
    
    // Single goroutine processes all state changes
    eventLoop()
}

func (n *RaftNode) Submit(cmd interface{}) (int, int, bool) {
    req := submitRequest{
        command: cmd,
        respCh:  make(chan submitResponse, 1),
    }
    
    select {
    case n.submitCh <- req:
        resp := <-req.respCh
        return resp.index, resp.term, resp.success
    case <-n.ctx.Done():
        return -1, -1, false
    }
}

func (n *RaftNode) eventLoop() {
    for {
        select {
        case req := <-n.submitCh:
            // Process submit without locks
            resp := n.processSubmit(req.command)
            req.respCh <- resp
            
        case <-n.electionTimer.C:
            n.startElection()
            
        case <-n.heartbeatTicker.C:
            n.sendHeartbeats()
            
        case <-n.ctx.Done():
            return
        }
    }
}
```

### 2. Lock-Free Communication
Use channels instead of shared memory:

```go
// Instead of:
func (rm *ReplicationManager) Replicate() {
    rm.mu.Lock()
    peers := rm.peers
    rm.mu.Unlock()
    // ...
}

// Use:
type ReplicationManager struct {
    peersCh chan []int
}

func (rm *ReplicationManager) Replicate() {
    peers := <-rm.peersCh
    rm.peersCh <- peers // Put it back
    // ...
}
```

### 3. Proper Shutdown Pattern
```go
func (n *raftNode) Stop() error {
    // 1. Stop accepting new work
    close(n.submitCh)
    
    // 2. Stop timers
    n.electionTimer.Stop()
    n.heartbeatTicker.Stop()
    
    // 3. Cancel context for all goroutines
    n.cancel()
    
    // 4. Wait for goroutines with timeout
    done := make(chan struct{})
    go func() {
        n.wg.Wait()
        close(done)
    }()
    
    select {
    case <-done:
        return nil
    case <-time.After(5 * time.Second):
        return errors.New("shutdown timeout")
    }
}
```

### 4. Startup Synchronization
Fix the multi-node startup issue:

```go
type HTTPTransport struct {
    ready chan struct{}
    // ...
}

func (t *HTTPTransport) Start() error {
    t.ready = make(chan struct{})
    
    listener, err := net.Listen("tcp", t.address)
    if err != nil {
        return err
    }
    
    go func() {
        close(t.ready) // Signal ready
        t.server.Serve(listener)
    }()
    
    // Wait for server to be ready
    <-t.ready
    
    // Add small delay for OS to fully register the listener
    time.Sleep(10 * time.Millisecond)
    
    return nil
}

// In peer connections, add retry logic:
func (t *HTTPTransport) connectToPeer(peerID int) error {
    for i := 0; i < 10; i++ {
        err := t.tryConnect(peerID)
        if err == nil {
            return nil
        }
        
        // Exponential backoff
        time.Sleep(time.Duration(1<<uint(i)) * time.Millisecond)
    }
    return fmt.Errorf("failed to connect to peer %d", peerID)
}
```

## Testing Strategies

### 1. Deterministic Testing (Go 1.24+)
```go
import "testing/synctest"

func TestElectionDeterministic(t *testing.T) {
    synctest.Run(func() {
        // Time is controlled
        cluster := createCluster(3)
        
        // Wait for all goroutines to block
        synctest.Wait()
        
        // Trigger specific events
        cluster.nodes[0].triggerElectionTimeout()
        synctest.Wait()
        
        // Assert deterministic outcome
        assert.Equal(t, cluster.nodes[0].state, Leader)
    })
}
```

### 2. Chaos Testing
```go
type ChaosTransport struct {
    Transport
    dropRate    float64
    delayMs     int
    partitioned map[int]bool
}

func (ct *ChaosTransport) SendAppendEntries(peer int, args *AppendEntriesArgs) (*AppendEntriesReply, error) {
    if ct.partitioned[peer] {
        return nil, errors.New("network partition")
    }
    
    if rand.Float64() < ct.dropRate {
        return nil, errors.New("packet dropped")
    }
    
    time.Sleep(time.Duration(rand.Intn(ct.delayMs)) * time.Millisecond)
    
    return ct.Transport.SendAppendEntries(peer, args)
}
```

### 3. Stress Testing
```go
func TestHighConcurrency(t *testing.T) {
    node := createLeaderNode()
    
    const goroutines = 100
    const submitsPerGoroutine = 1000
    
    start := time.Now()
    var wg sync.WaitGroup
    
    for i := 0; i < goroutines; i++ {
        wg.Add(1)
        go func(id int) {
            defer wg.Done()
            for j := 0; j < submitsPerGoroutine; j++ {
                cmd := fmt.Sprintf("cmd-%d-%d", id, j)
                node.Submit(cmd)
            }
        }(i)
    }
    
    wg.Wait()
    duration := time.Since(start)
    
    t.Logf("Processed %d commands in %v", 
        goroutines*submitsPerGoroutine, duration)
    
    // Verify no goroutine leaks
    time.Sleep(100 * time.Millisecond)
    if runtime.NumGoroutine() > 10 {
        t.Errorf("Goroutine leak: %d goroutines remaining", 
            runtime.NumGoroutine())
    }
}
```

## Immediate Action Items

1. **Add go-deadlock to development dependencies**
2. **Fix startup synchronization in HTTPTransport**
3. **Ensure commit index advances in single-node clusters**
4. **Add goroutine leak detection to tests**
5. **Implement retry logic for peer connections**
6. **Add comprehensive stress tests**

## Long-term Improvements

1. **Migrate to single-writer pattern** - Eliminate complex locking
2. **Use channels for cross-component communication**
3. **Implement proper graceful shutdown**
4. **Add chaos testing framework**
5. **Set up continuous profiling for production**