# Raft Key-Value Store Example

This example demonstrates how to build a distributed key-value store using the Raft consensus library. It showcases the core features of the Raft implementation including leader election, log replication, and state machine integration.

## Architecture Overview

The example implements a simple key-value store that uses Raft for consensus across multiple nodes. Each node in the cluster maintains:

- **Raft Node**: Handles consensus, leader election, and log replication
- **KV Store**: The state machine that applies committed commands
- **HTTP Transport**: Network communication between Raft nodes
- **JSON Persistence**: Durable storage for Raft state and snapshots
- **Client API**: HTTP endpoints for interacting with the KV store

## How the Raft Library is Used

### 1. State Machine Implementation

The `KVStore` struct implements the `raft.StateMachine` interface:

```go
type StateMachine interface {
    Apply(entry LogEntry) interface{}
    Snapshot() ([]byte, error)
    Restore(snapshot []byte) error
}
```

- `Apply()`: Executes committed commands (SET/DELETE operations)
- `Snapshot()`: Serializes the current state for log compaction
- `Restore()`: Rebuilds state from a snapshot

### 2. Transport Layer

The example uses HTTP transport with static peer discovery:

```go
transport, err := httpTransport.NewHTTPTransport(transportConfig, discovery)
```

The transport handles:
- RequestVote RPCs for leader election
- AppendEntries RPCs for log replication
- InstallSnapshot RPCs for catching up lagging nodes

### 3. Persistence Layer

JSON-based persistence stores:
- Current term and voted-for information
- Log entries
- Snapshots for log compaction

### 4. Node Creation and Configuration

```go
config := &raft.Config{
    ID:                 nodeID,
    Peers:              peerIDs,
    ElectionTimeoutMin: 150 * time.Millisecond,
    ElectionTimeoutMax: 300 * time.Millisecond,
    HeartbeatInterval:  50 * time.Millisecond,
    MaxLogSize:         10000,
}

node, err := raft.NewNode(config, transport, persistence, kvStore)
```

## Running the Example

### Starting a 3-Node Cluster

Open three terminals and run:

**Terminal 1 - Node 0:**
```bash
go run kv_store.go -id 0 -listener localhost:8000 -peers 0:localhost:8000,1:localhost:8001,2:localhost:8002
```

**Terminal 2 - Node 1:**
```bash
go run kv_store.go -id 1 -listener localhost:8001 -peers 0:localhost:8000,1:localhost:8001,2:localhost:8002
```

**Terminal 3 - Node 2:**
```bash
go run kv_store.go -id 2 -listener localhost:8002 -peers 0:localhost:8000,1:localhost:8001,2:localhost:8002
```

### Command-Line Flags

- `-id`: Node ID (must be unique in the cluster)
- `-listener`: Bind address and port for Raft RPC (e.g., `0.0.0.0:8000`)
- `-peers`: Comma-separated list of all peers in format `id:host:port`
- `-data`: Data directory for persistence (default: `./data/nodeN`)

### Client API Endpoints

The client API runs on port = raft_port + 1000 (e.g., 9000 for node 0):

#### Set a Key-Value Pair
```bash
curl -X POST http://localhost:9000/kv \
  -H "Content-Type: application/json" \
  -d '{"key":"foo","value":"bar"}'
```

Response:
```json
{"index":1,"term":1}
```

#### Get a Value
```bash
curl http://localhost:9000/kv/foo
```

Response:
```
bar
```

#### Delete a Key
```bash
curl -X DELETE http://localhost:9000/kv/foo
```

Response:
```json
{"index":2,"term":1}
```

#### Check Node Status
```bash
curl http://localhost:9000/status
```

Response:
```json
{
  "nodeId": 0,
  "term": 1,
  "isLeader": true,
  "leaderID": 0,
  "commitIndex": 1,
  "lastLogIndex": 1,
  "servers": [
    {"id": 0, "voting": true},
    {"id": 1, "voting": true},
    {"id": 2, "voting": true}
  ]
}
```

#### View All Data (Debug)
```bash
curl http://localhost:9000/debug/kv
```

Response:
```json
{"foo":"bar","key2":"value2"}
```

## How It Works

### Leader Election

1. Nodes start as followers
2. If no heartbeat is received within the election timeout, a follower becomes a candidate
3. The candidate requests votes from other nodes
4. If a majority votes for the candidate, it becomes the leader
5. The leader sends periodic heartbeats to maintain authority

### Log Replication

1. Client sends a command to any node
2. If the node is not the leader, it returns an error with the leader ID
3. The leader appends the command to its log
4. The leader replicates the log entry to all followers
5. Once a majority acknowledges, the entry is committed
6. Committed entries are applied to the state machine

### Fault Tolerance

- The cluster can tolerate failures of up to (N-1)/2 nodes
- For a 3-node cluster, 1 node can fail without losing availability
- Failed nodes can rejoin and catch up via log replication or snapshots

### Snapshots and Log Compaction

- When the log grows beyond `MaxLogSize`, a snapshot is triggered
- The snapshot captures the current state machine state
- Old log entries covered by the snapshot are discarded
- Lagging nodes can catch up quickly using snapshots

## Advanced Usage

### Binding to All Interfaces

For Docker or cloud deployments:

```bash
go run kv_store.go -id 0 -listener 0.0.0.0:8000 -peers 0:server1:8000,1:server2:8000,2:server3:8000
```

### Custom Data Directory

```bash
go run kv_store.go -id 0 -listener localhost:8000 -peers 0:localhost:8000,1:localhost:8001,2:localhost:8002 -data /var/lib/raft/node0
```

## Key Concepts Demonstrated

1. **Consensus**: All nodes agree on the same sequence of commands
2. **Fault Tolerance**: System continues operating despite node failures
3. **Strong Consistency**: All reads reflect previously committed writes
4. **Leader Election**: Automatic failover when the leader fails
5. **Log Replication**: Reliable command replication across nodes
6. **Snapshots**: Efficient state transfer and log compaction

## Monitoring and Debugging

- Check `/status` endpoint to see current leader and term
- Monitor logs for election and replication events
- Use `/debug/kv` to verify state consistency across nodes
- Data persistence in `./data/nodeN/` directories

## Limitations of This Example

- Uses HTTP transport (production systems might prefer gRPC or TCP)
- Simple key-value operations only (no transactions or complex queries)
- No authentication or encryption
- Static peer discovery (no dynamic membership changes)

This example provides a foundation for building more sophisticated distributed systems using the Raft consensus algorithm.