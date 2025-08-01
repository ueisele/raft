# Raft Architecture

This document describes the internal architecture, file structure, and component relationships of the Raft implementation.

## File Structure

```
raft/
├── node.go              # Main Raft node implementation
├── state.go             # State machine and state management
├── election.go          # Leader election logic
├── replication.go       # Log replication manager
├── log.go               # Log structure and operations
├── configuration.go     # Cluster membership management
├── safe_configuration.go # Safe configuration changes
├── snapshot_manager.go  # Snapshot creation and installation
├── types.go             # Core data structures and types
├── interfaces.go        # Public interfaces (Node, Transport, etc.)
│
├── transport/
│   ├── types.go         # Transport interface and types
│   └── http/
│       └── http.go      # HTTP-based RPC transport
│
├── persistence/
│   ├── types.go         # Persistence interface
│   └── json/
│       └── json.go      # JSON file-based persistence
│
├── example/
│   └── kv_store.go      # Example key-value store
│
├── integration/         # Integration tests organized by feature
│   ├── basic/
│   ├── client/
│   ├── cluster/
│   ├── configuration/
│   ├── fault_tolerance/
│   ├── leadership/
│   ├── safety/
│   ├── snapshot/
│   ├── stress/
│   ├── transport/
│   └── helpers/         # Shared test utilities
│
└── docs/                # Documentation
    ├── guides/
    └── features/
```

## Core Components

### 1. Node (node.go)
The main entry point and coordinator for all Raft operations:
- Manages the main event loop
- Coordinates between different managers
- Handles RPC requests and responses
- Provides the public API

### 2. State Manager (state.go)
Manages node state transitions:
- Tracks current state (Follower, Candidate, Leader)
- Manages persistent state (currentTerm, votedFor)
- Handles state-specific behavior

### 3. Election Manager (election.go)
Handles leader election process:
- Manages election timeouts
- Conducts elections when timeouts occur
- Implements vote denial for configuration changes
- Tracks recent heartbeats

### 4. Replication Manager (replication.go)
Manages log replication to followers:
- Sends AppendEntries RPCs
- Tracks nextIndex and matchIndex for each peer
- Handles log consistency checks
- Manages commit index advancement

### 5. Log Manager (log.go)
Manages the replicated log:
- Stores log entries
- Provides log querying operations
- Handles log truncation for snapshots
- Maintains log invariants

### 6. Configuration Manager (configuration.go)
Manages cluster membership:
- Tracks current cluster configuration
- Handles configuration change entries
- Validates configuration changes
- Manages voting vs non-voting members

### 7. Safe Configuration Manager (safe_configuration.go)
Implements safe server addition:
- Tracks non-voting members
- Monitors catch-up progress
- Automatically promotes to voting when ready
- Prevents disruption during additions

### 8. Snapshot Manager (snapshot_manager.go)
Handles log compaction:
- Creates snapshots when log grows too large
- Installs snapshots from leaders
- Manages snapshot persistence
- Coordinates with state machine

## Data Flow

### Leader Election Flow
```
ElectionTimeout → State.BecomeCandidate() → Election.StartElection()
    ↓
Send RequestVote RPCs → Collect responses → Check majority
    ↓
If won: State.BecomeLeader() → Start heartbeats
If lost: State.BecomeFollower()
```

### Log Replication Flow
```
Client.Submit() → Leader.AppendToLog() → Replication.SendAppendEntries()
    ↓
Followers receive → Check consistency → Append to log → Reply
    ↓
Leader receives replies → Update matchIndex → Advance commitIndex
    ↓
Apply committed entries to state machine
```

### Configuration Change Flow
```
AddServer() → Create ConfigEntry → Append to log
    ↓
Replicate to majority → Apply configuration
    ↓
If safe addition: Track as non-voting → Monitor progress
    ↓
When caught up (95%): Promote to voting → Create new ConfigEntry
```

## Persistence Format

### State Files
Located in the data directory, one file per type:

1. **current_term.json**: Stores current term
   ```json
   {"term": 5}
   ```

2. **voted_for.json**: Stores vote in current term
   ```json
   {"votedFor": 2}
   ```

3. **log.json**: Stores the replicated log
   ```json
   {
     "entries": [
       {"index": 1, "term": 1, "command": "set x=1", "type": "command"},
       {"index": 2, "term": 1, "command": {...}, "type": "configuration"}
     ]
   }
   ```

4. **snapshot.json**: Stores snapshot metadata
   ```json
   {
     "lastIncludedIndex": 100,
     "lastIncludedTerm": 3,
     "configuration": {...},
     "data": "base64-encoded-state-machine-data"
   }
   ```

## Transport Protocol

### HTTP RPC Endpoints
All RPCs use POST with JSON payloads:

- `/raft/requestvote` - RequestVote RPC
- `/raft/appendentries` - AppendEntries RPC  
- `/raft/installsnapshot` - InstallSnapshot RPC

### Message Formats

**RequestVote**:
```json
{
  "term": 5,
  "candidateId": 2,
  "lastLogIndex": 10,
  "lastLogTerm": 4
}
```

**AppendEntries**:
```json
{
  "term": 5,
  "leaderId": 1,
  "prevLogIndex": 9,
  "prevLogTerm": 4,
  "entries": [...],
  "leaderCommit": 8
}
```

## Concurrency Model

### Locking Strategy
- Single mutex (`mu`) protects all Raft state
- No fine-grained locking to avoid deadlocks
- Lock released during blocking I/O operations

### Goroutines
1. **Main event loop**: Processes timeouts and client requests
2. **RPC handlers**: One per incoming RPC
3. **Apply loop**: Applies committed entries to state machine
4. **Replication workers**: One per follower (when leader)

### Channels
- `applyCh`: Delivers committed entries to state machine
- `applyNotify`: Signals when new entries are ready to apply
- `stopCh`: Coordinates graceful shutdown

## Extension Points

### Custom Transports
Implement the `Transport` interface:
```go
type Transport interface {
    SendRequestVote(serverID int, args *RequestVoteArgs) (*RequestVoteReply, error)
    SendAppendEntries(serverID int, args *AppendEntriesArgs) (*AppendEntriesReply, error)
    SendInstallSnapshot(serverID int, args *InstallSnapshotArgs) (*InstallSnapshotReply, error)
    SetRPCHandler(handler RPCHandler)
    Start() error
    Stop() error
}
```

### Custom Persistence
Implement the `Persistence` interface:
```go
type Persistence interface {
    SaveState(state *PersistentState) error
    LoadState() (*PersistentState, error)
    SaveSnapshot(snapshot *Snapshot) error
    LoadSnapshot() (*Snapshot, error)
}
```

### Custom State Machines
Implement the `StateMachine` interface:
```go
type StateMachine interface {
    Apply(entry LogEntry) interface{}
    Snapshot() ([]byte, error)
    Restore(snapshot []byte) error
}
```