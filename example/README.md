# Distributed Key-Value Store Example

A production-ready distributed key-value store built on top of the Raft consensus library, demonstrating how to build reliable distributed systems with strong consistency guarantees.

## Features

- **Strong Consistency**: All operations go through the Raft leader ensuring linearizable reads and writes
- **Fault Tolerance**: Continues operating with a majority of nodes available
- **Automatic Leader Election**: Seamlessly elects a new leader if the current one fails
- **HTTP REST API**: Simple API for key-value operations
- **Leader Redirection**: Automatic HTTP 308 redirects from followers to leader
- **Cluster Management**: Python script for easy cluster orchestration and testing

## Quick Start

### 1. Build the KV Store

```bash
# From the example directory
go build -o kv_store kv_store.go
```

### 2. Start a 3-Node Cluster (Interactive Mode)

```bash
uv sync
uv run python kv-store-cluster.py
```

This starts a 3-node cluster and displays:
- Node status (leader/follower)
- Example curl commands
- API endpoints

### 3. Interact with the Cluster

```bash
# Set a value (will redirect to leader automatically)
curl -X PUT http://localhost:8080/kv/mykey \
     -H "Content-Type: application/json" \
     -d '{"value":"hello world"}'

# Get a value (from any node)
curl http://localhost:8081/kv/mykey

# Delete a value
curl -X DELETE http://localhost:8082/kv/mykey

# List all keys
curl http://localhost:8080/kv

# Check cluster status
curl http://localhost:8080/status | jq

# Health check
curl http://localhost:8080/health
```

## Manual Cluster Setup

Start individual nodes manually:

```bash
# Node 1
./kv_store --id 1 \
  --raft-listener localhost:9090 --raft-peers "2=localhost:9091,3=localhost:9092" \
  --api-listener localhost:8080 --api-peers "2=localhost:8081,3=localhost:8082"

# Node 2
./kv_store --id 2 \
  --raft-listener localhost:9091 --raft-peers "1=localhost:9090,3=localhost:9092" \
  --api-listener localhost:8081 --api-peers "1=localhost:8080,3=localhost:8082"

# Node 3
./kv_store --id 3 \
  --raft-listener localhost:9092 --raft-peers "1=localhost:9090,2=localhost:9091" \
  --api-listener localhost:8082 --api-peers "1=localhost:8080,2=localhost:8081"
```

## Running Tests

### Automated Test Suite

```bash
# Run all tests
./kv-store-cluster.py --run-tests

# Run specific tests
./kv-store-cluster.py --run-tests --test-filter consistency

# Run with more clients and operations
./kv-store-cluster.py --run-tests --clients 20 --operations 500
```

### Test Scenarios

1. **Basic Operations**: Tests SET, GET, DELETE operations
2. **Consistency**: Verifies concurrent writes maintain consistency
3. **Leader Failure**: Tests automatic failover when leader crashes

## REST API

### Key-Value Operations

| Method | Path | Description |
|--------|------|-------------|
| PUT | `/kv/{key}` | Set a value |
| GET | `/kv/{key}` | Get a value |
| DELETE | `/kv/{key}` | Delete a value |
| GET | `/kv` | List all keys |

### Cluster Operations

| Method | Path | Description |
|--------|------|-------------|
| GET | `/status` | Get cluster status |
| GET | `/health` | Health check |

### Response Codes

- `200 OK`: Success
- `308 Permanent Redirect`: Not the leader (includes redirect URL)
- `404 Not Found`: Key doesn't exist
- `408 Request Timeout`: Operation timed out
- `413 Payload Too Large`: Value exceeds 1MB limit
- `503 Service Unavailable`: No leader elected

## Command Line Options

### KV Store Binary

```
Required:
  --id INT                  Unique server ID (1, 2, 3, ...)
  --raft-listener STRING    Raft consensus address
  --raft-peers STRING       Raft peer list: "id=host:port,..."
  --api-listener STRING     REST API address
  --api-peers STRING        API peer list: "id=host:port,..."

Optional:
  --data-dir STRING         Storage directory (default: "./data-{id}")
  --log-level STRING        Log verbosity: debug|info|warn|error
  --snapshot-threshold INT  Entries before snapshot (default: 1000)
  --help                    Show help
  --version                 Show version
```

### Python Test Script

```
Options:
  --cluster-size N      Number of nodes (default: 3)
  --base-port N        Starting port (default: 8080)
  --run-tests          Execute test suite
  --test-filter REGEX  Run specific tests
  --clients N          Concurrent clients (default: 10)
  --operations N       Operations per client (default: 100)
  --binary PATH        KV store binary (default: ./kv_store)
  --src PATH           KV store source (default: ./kv_store.go)
  --verbose            Enable debug output
  --keep-logs          Don't delete logs after tests
```

## Architecture

```
┌─────────────┐     HTTP/REST      ┌──────────────┐
│   Clients   │ ◄─────────────────► │   KV Store   │
└─────────────┘                     │   REST API   │
                                    └──────┬───────┘
                                           │
                                    ┌──────▼───────┐
                                    │ StateMachine │
                                    │   (KVStore)  │
                                    └──────┬───────┘
                                           │
                                    ┌──────▼───────┐
                                    │  Raft Node   │
                                    └──────┬───────┘
                                           │
            ┌──────────────────────────────┼──────────────────────────┐
            │                              │                          │
    ┌───────▼──────┐              ┌───────▼──────┐          ┌────────▼─────┐
    │  Transport   │              │ Persistence  │          │   Discovery  │
    │    (HTTP)    │              │    (JSON)    │          │   (Static)   │
    └──────────────┘              └──────────────┘          └──────────────┘
```

## Data Guarantees

- **Strong Consistency**: All operations go through the leader
- **Linearizability**: Operations appear atomic
- **Durability**: Committed writes are persisted to disk
- **Fault Tolerance**: Survives minority node failures

## Limitations

- Key size: Maximum 1KB
- Value size: Maximum 1MB
- Request timeout: 5 seconds
- No authentication/authorization (for demonstration only)

## Development

### Running Tests
```bash
# Unit tests
go test -v ./example/...

# Race detection
go test -race ./example/...

# Coverage
go test -cover ./example/...
```

### Building for Production
```bash
CGO_ENABLED=0 go build -ldflags="-s -w" -o kv_store kv_store.go
```

## Troubleshooting

### No Leader Elected
- Ensure all nodes can reach each other on both API and Raft ports
- Check that node IDs are unique
- Verify majority of nodes are running

### High Latency
- Check network connectivity between nodes
- Monitor CPU and memory usage
- Consider adjusting snapshot threshold

### Data Not Persisting
- Check data directory permissions
- Ensure sufficient disk space
- Review logs in `cluster-logs/` directory

## License

See the main repository LICENSE file.