# Raft Consensus Library for Go

A comprehensive implementation of the Raft consensus algorithm in Go, following the design from the extended Raft paper by Diego Ongaro and John Ousterhout.

## Features

This implementation includes all the core features described in the Raft paper:

### Core Algorithm
- **Leader Election**: Randomized election timeouts to elect leaders efficiently
- **Log Replication**: Strong leader-based log replication with consistency guarantees
- **Safety**: Implementation of all safety properties including Election Safety, Leader Append-Only, Log Matching, Leader Completeness, and State Machine Safety

### Advanced Features
- **Persistence**: Durable storage of persistent state (currentTerm, votedFor, log)
- **Log Compaction**: Snapshotting mechanism to compact logs and save storage
- **Cluster Membership Changes**: Joint consensus approach for safe configuration changes
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

Data is persisted to JSON files in the specified data directory:
- `raft-state-{id}.json` - Persistent state
- `raft-snapshot-{id}.json` - Snapshots

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

- **Leader election**: Typically completes in 150-300ms
- **Log replication**: Single round-trip to majority for each entry
- **Throughput**: Supports batching and pipelining (configurable)
- **Recovery**: Fast recovery after failures through log repair

### Configuration Changes

Implements the joint consensus approach:

1. Leader proposes joint configuration (Cold,new)
2. Once committed, leader proposes final configuration (Cnew)
3. Ensures safety during transitions

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

## Limitations and Future Work

### Current Limitations

- No Byzantine fault tolerance (assumes fail-stop)
- HTTP RPC may not be optimal for high-performance scenarios
- Basic persistence implementation (not optimized for large logs)

### Potential Improvements

- gRPC transport for better performance
- More efficient binary persistence format
- Additional optimizations (pipelining, batching)
- Monitoring and metrics integration
- Pre-vote optimization for reducing disruptions

## References

- [Raft Paper](https://raft.github.io/raft.pdf) - In Search of an Understandable Consensus Algorithm
- [Raft Website](https://raft.github.io/) - Raft consensus algorithm
- [The Raft Consensus Algorithm](https://raft.github.io/) - Interactive visualization

## License

MIT License - see LICENSE file for details.