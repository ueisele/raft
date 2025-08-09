# KV Store Implementation Guide

Comprehensive guide for implementing a distributed key/value store using the Raft library.

## Overview

This document provides detailed specifications for building a production-ready distributed key/value store that demonstrates the full capabilities of our Raft consensus library.

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
                    ┌──────────────────────┼──────────────────────┐
                    │                      │                      │
            ┌───────▼──────┐      ┌───────▼──────┐      ┌────────▼─────┐
            │  Transport   │      │ Persistence  │      │   Discovery  │
            │    (HTTP)    │      │    (JSON)    │      │   (Static)   │
            └──────────────┘      └──────────────┘      └──────────────┘
```

## 1. Command Line Interface

### Required Arguments
```bash
--id INT                  # Unique server ID (1, 2, 3, ...)
--raft-listener STRING    # Raft consensus port (e.g., localhost:9090)
--raft-peers STRING       # Raft peer list: "id=host:port,id=host:port"
--api-listener STRING     # REST API endpoint (e.g., localhost:8080)
--api-peers STRING        # API peer list: "id=host:port,id=host:port"
```

### Optional Arguments
```bash
--data-dir STRING     # Storage directory (default: "./data-{id}")
--log-level STRING    # Verbosity: debug|info|warn|error (default: "info")
--snapshot-threshold  # Entries before snapshot (default: 1000)
--help               # Show help and exit
--version            # Show version and exit
```

### Example Usage
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

## 2. Leader Redirection Mechanism

### Strong Consistency Model
All operations (reads and writes) are handled through the leader to ensure strong consistency. This guarantees that:
- All clients see the same data at the same point in time
- No stale reads are possible
- Operations are linearizable

### HTTP 308 Redirect Implementation
When a non-leader node receives any request (GET, PUT, DELETE), it responds with HTTP 308 (Permanent Redirect) pointing to the current leader.

### Client Implementation
Clients should handle 308 redirects automatically.

### Address Discovery Configuration
The system maintains two separate discovery mechanisms:
1. **Raft Discovery**: Maps server IDs to Raft consensus addresses (--raft-peers)
2. **API Discovery**: Maps server IDs to REST API addresses (--api-peers)

This separation allows for:
- Different network interfaces for client vs. cluster traffic
- Security isolation between external API and internal consensus
- Independent scaling of API and consensus layers

## 3. Key-Value Operations

The Key-Value store supports the following operations:
* Set value
* Get value  
* Delete value
* List keys

Requirements:
* All operations go through the leader (strong consistency)
* The set and delete operations are idempotent
* The set and delete operations block until the operation is committed
* Get operations also go through the leader to ensure no stale reads

## 4. REST API Specification

### 4.1 Key-Value Operations

#### PUT /kv/{key} - Set Value
```http
PUT /kv/user:123
Content-Type: application/json

{
  "value": "John Doe"
}

Response: 200 OK
{
  "key": "user:123",
  "value": "John Doe",
  "version": 1,
  "timestamp": "2024-01-15T10:30:45Z",
  "term": 1,
  "commitIndex": 1
}

Errors:
- 308: Not the leader (redirect to leader with Location header)
- 400: Invalid JSON or key format
- 408: Request timeout
- 413: Value too large (>1MB)
- 503: No leader elected (cluster unavailable)
```

**308 Redirect Response:**
```json
{
  "error": "not_leader",
  "leader_id": 2,
  "leader_url": "http://localhost:8081"
}
```
The `Location` header contains the full URL to retry the request on the leader.

#### GET /kv/{key} - Get Value
```http
GET /kv/user:123

Response: 200 OK
{
  "key": "user:123",
  "value": "John Doe",
  "version": 1,
  "timestamp": "2024-01-15T10:30:45Z"
}

Errors:
- 308: Not the leader (redirect to leader for strong consistency)
- 404: Key not found
- 503: No leader elected (cluster unavailable)
```

**Note:** Even read operations redirect to the leader to ensure strong consistency and prevent stale reads.

#### DELETE /kv/{key} - Delete Value
```http
DELETE /kv/user:123

Response: 204 No Content
Response: 200 OK
{
  "term": 1,
  "commitIndex": 2
}

Errors:
- 308: Not the leader (redirect to leader with Location header)
- 404: Key not found  
- 503: No leader elected (cluster unavailable)
```

#### GET /kv - List Keys
```http
GET /kv?prefix=user:&limit=100

Response: 200 OK
{
  "keys": ["user:123", "user:124"],
  "count": 2,
  "has_more": false
}

Errors:
- 308: Not the leader (redirect to leader for consistency)
- 503: No leader elected (cluster unavailable)
```

### 4.2 Cluster Operations

#### GET /status - Cluster Status
```http
GET /status

Response: 200 OK
{
  "node": {
    "id": 1,
    "address": "localhost:9090",
    "state": "leader",
    "term": 5
  },
  "cluster": {
    "leader_id": 1,
    "size": 3,
    "peers": [
      {"id": 2, "address": "localhost:9091", "voting": true, "online": true},
      {"id": 3, "address": "localhost:9092", "voting": true, "online": true}
    ]
  },
  "raft": {
    "commit_index": 42,
    "last_applied": 42,
    "last_log_index": 42,
    "last_log_term": 5
  },
  "stats": {
    "keys_count": 150,
    "data_size_bytes": 15360,
    "snapshot_index": 1000,
    "uptime_seconds": 3600
  }
}
```

#### GET /health - Health Check
```http
GET /health

Response: 200 OK (Healthy)
{
  "status": "healthy",
  "checks": {
    "raft_initialized": true,
    "leader_known": true,
    "can_write": true,
    "peers_reachable": true
  }
}

Response: 503 Service Unavailable (Unhealthy)
{
  "status": "unhealthy",
  "checks": {
    "raft_initialized": true,
    "leader_known": false,
    "can_write": false,
    "peers_reachable": false
  },
  "error": "No leader elected"
}
```

## 5. Python Test Script Specification

### 5.1 Script Interface
```python
#!/usr/bin/env python3
"""
KV Store Cluster Test Suite

Usage:
    kv-store-cluster.py [OPTIONS]

Options:
    --cluster-size N      Number of nodes (default: 3)
    --base-port N        Starting port (default: 8080)
    --run-tests          Execute test suite
    --test-filter REGEX  Run specific tests
    --clients N          Concurrent clients (default: 10)
    --operations N       Operations per client (default: 100)
    --duration SECONDS   Test duration (default: 60)
    --binary PATH        KV store binary (default: ./kv_store)
    --src PATH           KV store source (default: ./kv_store.go)
    --verbose            Enable debug output
    --keep-logs          Don't delete logs after tests
"""
```

Remarks:
* The options --binary and --src are mutually exclusive.

### 5.2 Test Scenarios

* Basic Operations Test
* Consistency Test
* Leader Failure Test
* Network Partition Test

### 5.3 Interactive Mode Output
```
================================================================================
                        KV STORE CLUSTER READY
================================================================================

Cluster Configuration:
  Nodes: 3
  Replication Factor: 2
  
Node Status:
  [LEADER]   Node 1: http://localhost:8080 (Raft: localhost:9090)
  [FOLLOWER] Node 2: http://localhost:8081 (Raft: localhost:9091)
  [FOLLOWER] Node 3: http://localhost:8082 (Raft: localhost:9092)

Quick Test Commands:

  # Write a value (to leader)
  curl -X PUT http://localhost:8080/kv/hello \
       -H "Content-Type: application/json" \
       -d '{"value":"world"}'

  # Read from any node
  curl http://localhost:8081/kv/hello

  # Check cluster status
  curl http://localhost:8080/status | jq

  # Run performance test
  ./kv-store-cluster.py --run-tests --test-filter performance

Logs: ./cluster-logs/
Data: ./cluster-data/

Press Ctrl+C to shutdown cluster...
```

The node status output is updated in real-time as the cluster is being initialized.

## 6. Data Guarantees and Limits

### Consistency Model
- **Strong Consistency**: All operations (reads and writes) go through the leader
- **Linearizability**: All operations appear to execute atomically at some point between their start and completion
- **Sequential Consistency**: Operations from each client maintain order
- **No Stale Reads**: By routing all reads through the leader, clients always see the most recent committed data

### Durability
- **Committed writes**: Persisted to majority before acknowledgment
- **Crash recovery**: All committed data recovered on restart
- **Snapshot frequency**: Every 1000 entries or 5 minutes

### Operational Limits
- **Key size**: Maximum 1KB
- **Value size**: Maximum 1MB
- **Total keys**: Limited by available memory
- **Cluster size**: 3-7 nodes recommended
- **Request timeout**: 5 seconds
- **Election timeout**: 150-300ms
- **Heartbeat interval**: 50ms

## 7. Development

### Build Commands
```bash
# Development build with race detector
go build -race -o kv_store ./example/kv_store.go

# Production build
CGO_ENABLED=0 go build -ldflags="-s -w" -o kv_store ./example/kv_store.go

# Run tests
go test -v -race -timeout 30s ./example/...

# Benchmark
go test -bench=. -benchmem ./example/...

# Coverage report
go test -coverprofile=coverage.out ./example/...
go tool cover -html=coverage.out
```

## Guidelines

* The key-value store implementation should follow clean code principles.
* The key-value store implementation should be tested.

## 8. Monitoring and Observability

### Logging
```json
{
  "timestamp": "2024-01-15T10:30:45.123Z",
  "level": "info",
  "node_id": 1,
  "component": "api",
  "method": "PUT",
  "path": "/kv/user:123",
  "duration_ms": 12,
  "status": 200,
  "client_ip": "127.0.0.1",
  "trace_id": "abc123"
}
```

### Metrics (Prometheus format)
```
# Request metrics
kv_store_requests_total{method="GET",path="/kv",status="200"} 1234
kv_store_request_duration_seconds{method="GET",quantile="0.5"} 0.010

# Raft metrics
raft_term{node_id="1"} 5
raft_commit_index{node_id="1"} 42
raft_leader{node_id="1"} 1

# Storage metrics
kv_store_keys_total{node_id="1"} 150
kv_store_data_bytes{node_id="1"} 15360
```

### Debug Commands
```bash
# Check cluster status
curl -s http://localhost:8080/status | jq

# Monitor real-time logs
tail -f ./data-1/raft.log | jq

# Verify data persistence
ls -la ./data-1/

# Network connectivity test
nc -zv localhost 9090

# Performance profiling
curl http://localhost:8080/debug/pprof/profile?seconds=30 > profile.out
go tool pprof profile.out
```

## References

- [Raft Consensus Algorithm Paper](https://raft.github.io/raft.pdf)
- [Go Raft Library Documentation](../README.md)
- [Example KV Store Source](./kv_store.go)