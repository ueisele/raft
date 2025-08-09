# Raft Implementation Debug Status

## Date: 2025-01-08
## Updated: 2025-01-09 (continued session - 3rd update)

## Current Status

### What's Working
- Single-node clusters successfully elect leaders
- Python test suite runs with `uv` (after converting pyproject.toml to PEP 621 format)
- RPC timeout is fixed (set to 1000ms in kv_store.go)
- Safe configuration manager properly detects leadership (IsLeaderFunc callback set in node.go)
- Race conditions in ReplicationManager fixed (converted Mutex to RWMutex)
- SIGTERM handling now works properly (fixed timeout-based lock acquisition in Stop())
- Leader election completes successfully in single-node clusters
- Log entries are successfully persisted to disk (verified in raft-state-1.json)
- persist() now completes successfully after releasing n.mu lock

### Issues Fixed in This Session
1. **HTTP transport startup race condition**: Fixed by using `net.Listen()` first, then `server.Serve()` to ensure the server is listening before Start() returns
2. **Deadlock in heartbeat ticker**: Fixed by releasing the read lock before calling `SendHeartbeats()` 
3. **SIGTERM not working**: Fixed by adding timeout to Stop() method's lock acquisition (2 second timeout)
4. **Election deadlock**: Fixed by releasing n.mu lock before starting election goroutine (line 768 in node.go)
5. **persist() blocking**: Fixed by releasing n.mu lock before calling persist() to avoid deadlock with file I/O operations
6. **Submit() manual lock management**: Changed from defer to manual unlock to allow releasing lock before persist()

### Current Issue - FIXED: Multiple Deadlocks in ReplicationManager

**Root Causes Identified and Fixed**:
1. **GetMatchIndex deadlock**: Was using `Lock()` instead of `RLock()` - FIXED
2. **sendHeartbeats deadlock**: Was trying to acquire RLock while SendHeartbeats held Lock - FIXED by creating two versions
3. **persist() deadlock**: Was holding node lock while doing file I/O - FIXED by releasing lock before persist
4. **election deadlock**: Was holding lock while starting election goroutine - FIXED by releasing lock first

**Debug Log Sequence**:
```
[DEBUG] Submit: persisting state...
[DEBUG] Submit: persist completed, triggering replication...
[DEBUG] Replicate: starting
<hangs here - never reaches "Replicate: have X peers">
```

**Investigation Findings**:
- The issue is NOT in persist() - it completes successfully
- Log entries ARE being written to disk (verified in raft-state-1.json)
- The deadlock is specifically in the ReplicationManager's Replicate() function
- Even after removing state checks and lock acquisitions, something still blocks

### Key Observations
1. When starting a 3-node cluster, nodes can't reach each other despite having correct addresses
2. We fixed the IPv6/IPv4 issue by using explicit `127.0.0.1` addresses instead of "localhost"
3. The HTTP transport is properly configured with connection pooling
4. Each node starts its own HTTP server for Raft RPCs on ports 9080-9082
5. Single node test shows the system works when there's no inter-node communication needed

### Error Patterns in Logs
```
[DEBUG] Failed to send RequestVote to 2: transport error to server 2: failed to send request: Post "http://127.0.0.1:9081/raft/requestvote": dial tcp 127.0.0.1:9081: connect: connection refused

[DEBUG] Failed to send RequestVote to 1: transport error to server 1: failed to send request: Post "http://127.0.0.1:9080/raft/requestvote": context deadline exceeded
```

### Files Modified During Debugging

1. **`/var/home/eiseleu/Repositories/Personal/GitHub/ueisele/raft/node.go`**
   - Fixed safe configuration manager's leader detection (line 127-130)
   - Fixed SIGTERM handling with timeout-based lock acquisition in Stop() (lines 160-202)
   - **Fixed election deadlock by releasing lock before starting goroutine (line 768)**
   - **Changed Submit() to manually manage locks instead of defer (lines 225-300)**
   - **Released n.mu lock before calling persist() to avoid deadlock (line 275)**
   - Added extensive debug logging for lock acquisition/release tracking

2. **`/var/home/eiseleu/Repositories/Personal/GitHub/ueisele/raft/replication.go`**
   - Changed Mutex to RWMutex in ReplicationManager
   - Fixed race conditions in Replicate() and sendHeartbeats()
   - **Added RLock to advanceCommitIndex() for thread safety (line 307)**
   - **Commented out state check in Replicate() to avoid deadlock (lines 89-94)**
   - **Removed lock acquisition in Replicate() that was causing deadlock (lines 96-99)**
   - Added extensive debug logging to trace execution flow

3. **`/var/home/eiseleu/Repositories/Personal/GitHub/ueisele/raft/persistence/json/json.go`**
   - Added debug markers to trace SaveState() execution (removed after debugging)
   - Confirmed file I/O operations complete successfully

4. **`/var/home/eiseleu/Repositories/Personal/GitHub/ueisele/raft/example/kv_store.go`**
   - Added SimpleLogger implementation
   - Set RPCTimeout to 1000ms (line 657)
   - Added extensive debug logging to trace API request flow

5. **`/var/home/eiseleu/Repositories/Personal/GitHub/ueisele/raft/transport/http/http.go`**
   - Fixed HTTP server startup race condition using net.Listen() before Serve()
   - Added custom HTTP client with connection pooling

6. **`/var/home/eiseleu/Repositories/Personal/GitHub/ueisele/raft/example/pyproject.toml`**
   - Converted from Poetry to PEP 621 format for uv compatibility

7. **`/var/home/eiseleu/Repositories/Personal/GitHub/ueisele/raft/example/kv-store-cluster.py`**
   - Changed from "localhost" to "127.0.0.1" for IPv4 addresses

### Next Steps to Investigate
1. **Replicate() Still Blocking**: Despite removing state checks and locks, something still blocks
   - The issue appears to be after the "Replicate: starting" log
   - Even with simplified code (peers = rm.peers), execution doesn't continue
   - Possible circular dependency or hidden deadlock in called functions
   - May need to trace all mutex acquisitions in the call stack

2. **Multi-node Cluster Issues**: After fixing the Replicate() issue, test multi-node clusters
   - Nodes still can't reach each other when started simultaneously
   - May need startup synchronization or retry logic

### Summary of Progress
- **Major Achievement**: Successfully identified and fixed multiple deadlocks, allowing persist() to complete
- **Current Blocker**: Replicate() function in ReplicationManager still hangs, preventing API responses
- **Impact**: KV store can persist data but cannot return HTTP responses to clients

### Test Commands

```bash
# Run full test suite
uv run python kv-store-cluster.py --run-tests --verbose

# Test with single node (this works!)
uv run python kv-store-cluster.py --cluster-size 1 --run-tests

# Build the binary
go build -o kv_store kv_store.go

# Manual single node test
./kv_store --id 1 --api-listener 127.0.0.1:8080 --raft-listener 127.0.0.1:9080 --data-dir ./test-data

# Check node status
curl -s http://127.0.0.1:8080/status | python3 -m json.tool
```

### Fixed Issues - Root Causes

1. **HTTP Transport Race Condition**: The `Start()` method was returning before the server was actually listening. Fixed by creating the listener first with `net.Listen()`, then using `server.Serve(listener)` instead of `server.ListenAndServe()`.

2. **RequestVote RPC Deadlock**: The heartbeat ticker was holding a read lock while calling `SendHeartbeats()`, which could block RequestVote RPCs that need a write lock. Fixed by releasing the lock before calling `SendHeartbeats()`.

3. **Signal Handling Issue**: The kv_store doesn't respond to SIGTERM because `Stop()` tries to acquire a lock that might be held indefinitely. This is why only `pkill -9` works.

The error pattern supports this:
- Early in startup: "connection refused" (server not listening yet)
- Later: "context deadline exceeded" (server is up but overwhelmed or in a bad state)

### Potential Fix
Add a mechanism to ensure the HTTP server is actually listening before `Start()` returns, possibly by:
1. Using a channel to signal when `ListenAndServe` is ready
2. Adding a small retry mechanism in the HTTP client
3. Implementing a "ready" check before starting elections