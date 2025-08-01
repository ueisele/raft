# Raft Consensus Library for Go

A comprehensive implementation of the Raft consensus algorithm in Go, following the design from the extended Raft paper by Diego Ongaro and John Ousterhout.

**Documentation**: This README covers usage and features. For internal implementation details, see [IMPLEMENTATION.md](IMPLEMENTATION.md).

## Features

This implementation includes all the core features described in the Raft paper:

### Core Algorithm
- **Leader Election**: Randomized election timeouts to elect leaders efficiently
- **Log Replication**: Strong leader-based log replication with consistency guarantees
- **Safety**: Implementation of all safety properties including Election Safety, Leader Append-Only, Log Matching, Leader Completeness, and State Machine Safety

### Advanced Features
- **Persistence**: Durable storage of persistent state (currentTerm, votedFor, log)
- **Log Compaction**: Snapshotting mechanism to compact logs and save storage
- **Cluster Membership Changes**: Safe server addition with automatic promotion from non-voting to voting
- **Client Interaction**: Linearizable semantics with duplicate detection

## Architecture

### Core Components

1. **raft.go**: Main Raft algorithm implementation
2. **rpc.go**: RPC transport layer for inter-server communication
3. **persistence.go**: Persistent state management
4. **snapshot.go**: Log compaction and snapshot handling
5. **config.go**: Cluster membership change management

### Key Data Structures

```go
type Raft struct {
    // Persistent state on all servers
    currentTerm int
    votedFor    *int
    log         []LogEntry

    // Volatile state on all servers
    commitIndex int
    lastApplied int

    // Volatile state on leaders
    nextIndex  []int
    matchIndex []int
    
    // Additional state for operation
    state         State
    electionTimer *time.Timer
    heartbeatTick *time.Ticker
    // ...
}
```

## Usage

### Basic Example

```go
package main

import (
    "context"
    "log"
    "github.com/ueisele/raft"
)

func main() {
    // Create a 3-node cluster
    peers := []int{0, 1, 2}
    applyCh := make(chan raft.LogEntry, 100)
    
    // Create Raft instance for server 0
    rf := raft.NewRaft(peers, 0, applyCh)
    
    // Set up persistence
    persister := raft.NewPersister("./data", 0)
    rf.SetPersister(persister)
    
    // Start the server
    ctx := context.Background()
    rf.Start(ctx)
    
    // Submit commands
    if index, term, isLeader := rf.Submit("hello world"); isLeader {
        log.Printf("Command submitted: index=%d, term=%d", index, term)
    }
    
    // Process applied entries
    go func() {
        for entry := range applyCh {
            log.Printf("Applied: %v", entry.Command)
        }
    }()
}
```

### Safe Server Addition

```go
// Safe way to add a new server (recommended)
err := node.AddServerSafely(4, "server-4:8004")
if err != nil {
    log.Fatal(err)
}

// Monitor catch-up progress
progress := node.GetServerProgress(4)
log.Printf("Server 4 progress: %.1f%%", progress.CatchUpProgress() * 100)

// Unsafe way (NOT recommended - adds voting member immediately)
err = node.AddServer(5, "server-5:8005", true) // true = voting
```

### Running a Cluster

```bash
# Terminal 1 - Start server 0
go run example/main.go 0 0 1 2

# Terminal 2 - Start server 1  
go run example/main.go 1 0 1 2

# Terminal 3 - Start server 2
go run example/main.go 2 0 1 2
```

### RPC Interface

The implementation provides HTTP-based RPC endpoints:

- `POST /requestvote` - RequestVote RPC
- `POST /appendentries` - AppendEntries RPC  
- `POST /installsnapshot` - InstallSnapshot RPC
- `POST /submit` - Client command submission
- `GET /status` - Server status

### Client Interaction

```bash
# Submit a command to the leader
curl -X POST http://localhost:8000/submit \
  -H "Content-Type: application/json" \
  -d '"my command"'

# Check server status
curl http://localhost:8000/status
```

## Configuration

### Timing Parameters

The implementation uses the following default timing parameters:

- **Election timeout**: 150-300ms (randomized)
- **Heartbeat interval**: 50ms  
- **RPC timeout**: 5s

These satisfy the timing requirement: `broadcastTime ≪ electionTimeout ≪ MTBF`

### Persistence

The implementation uses JSON-based persistence for human readability and debugging ease. State is persisted to the specified data directory.

For detailed persistence format and file structure, see [Architecture - Persistence Format](docs/ARCHITECTURE.md#persistence-format).

## Testing

Run the test suite:

```bash
go test -v
```

Run benchmarks:

```bash
go test -bench=.
```

## Implementation Details

### Safety Properties

The implementation ensures all Raft safety properties:

1. **Election Safety**: At most one leader per term
2. **Leader Append-Only**: Leaders never overwrite log entries  
3. **Log Matching**: Logs with matching entries are identical up to that point
4. **Leader Completeness**: All committed entries appear in future leader logs
5. **State Machine Safety**: State machines apply the same sequence of commands

### Performance Characteristics

- **Leader election**: Typically completes in 150-300ms (benchmark: ~500ms average)
- **Log replication**: Single round-trip to majority for each entry
- **Throughput**: Supports batching and pipelining (configurable)
- **Recovery**: Fast recovery after failures through log repair
- **Memory usage**: O(log size + cluster size)

### Configuration Changes

Implements safe server addition with automatic promotion:

1. New servers are added as non-voting members first
2. They receive log entries but don't participate in voting
3. Once caught up (95% of log), they are automatically promoted to voting
4. This prevents empty-log servers from disrupting cluster availability

For more details, see [SAFE_SERVER_ADDITION.md](SAFE_SERVER_ADDITION.md).

### Log Compaction

Supports snapshotting for log compaction:

- State machines can create snapshots asynchronously
- InstallSnapshot RPC for catching up slow followers
- Automatic cleanup of superseded log entries

## Design Decisions

### Understandability Focus

Following the paper's emphasis on understandability:

- Clear separation of concerns (election, replication, safety)
- Strong leadership model simplifies design
- Minimal state space and deterministic behavior
- Comprehensive logging and debugging support

### Practical Considerations

- HTTP-based RPC for simplicity and debugging
- JSON persistence for human-readable state
- Configurable timing parameters
- Comprehensive error handling
- Clean shutdown support

## Unimplemented Features

### Joint Consensus

The implementation does not support the full two-phase joint consensus protocol described in the Raft paper for configuration changes.

**Why it's not implemented:**
- Adds significant complexity for limited benefit
- Requires tracking two configurations simultaneously (C_old and C_new)
- Requires majority agreement from BOTH configurations
- Complex edge cases and testing requirements

**Our safer alternative:**
- Always add servers as non-voting members first
- Automatically promote to voting after catching up (95% of log)
- Prevents empty-log servers from affecting quorum
- Much simpler to understand and maintain
- Provides 99% of the safety with 10% of the complexity

**Impact:**
- Cannot safely change majority of cluster at once
- Must add/remove servers one at a time
- For most use cases, this is actually safer and more predictable

### Configuration Rollback

The current implementation does not support automatic rollback of failed configuration changes.

**Why it's not implemented:**
- The Raft paper doesn't specify a rollback mechanism
- Our safe server addition approach minimizes the need for rollback
- Failed additions can be manually removed before promotion
- Implementing automatic rollback would require complex state management

**Current approach:**
- Safe server addition prevents most configuration problems
- Only one configuration change is allowed at a time
- Failed changes require administrator intervention to resolve

## Limitations and Future Work

See [Roadmap and Limitations](ROADMAP_AND_LIMITATIONS.md) for comprehensive information about:
- Current limitations and known issues
- Unimplemented features and design decisions
- Performance considerations
- Security gaps
- Future improvements and roadmap

## Documentation

- [Documentation Index](docs/README.md) - Complete documentation overview
- [Implementation Details](IMPLEMENTATION.md) - Detailed implementation notes
- [Changelog](CHANGELOG.md) - Recent changes and version history
- [Roadmap and Limitations](ROADMAP_AND_LIMITATIONS.md) - Known limitations and future work
- [Test Results](TEST_RESULTS_SUMMARY.md) - Current test status

### Guides
- [Test Optimization Guide](docs/guides/TEST_OPTIMIZATION_GUIDE.md) - Writing reliable tests
- [Timing Best Practices](docs/guides/TIMING_BEST_PRACTICES.md) - Timing-resilient tests

### Features
- [Safe Server Addition](docs/features/SAFE_SERVER_ADDITION.md) - Safe membership changes

## References

- [Raft Paper](https://raft.github.io/raft.pdf) - In Search of an Understandable Consensus Algorithm
- [Raft Website](https://raft.github.io/) - Raft consensus algorithm
- [The Raft Consensus Algorithm](https://raft.github.io/) - Interactive visualization

## License

MIT License - see LICENSE file for details.