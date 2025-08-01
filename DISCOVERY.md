# Peer Discovery in Raft

This document describes the peer discovery mechanism implemented in this Raft library, which is essential for production deployments in distributed systems.

## Overview

The library provides a pluggable `PeerDiscovery` interface that allows different mechanisms for discovering peer addresses in a Raft cluster. This design enables the library to work in various deployment environments without hardcoding network configurations.

## The PeerDiscovery Interface

```go
type PeerDiscovery interface {
    GetPeerAddress(ctx context.Context, serverID int) (string, error)
    RefreshPeers(ctx context.Context) error
    Close() error
}
```

## Built-in Implementations

### StaticPeerDiscovery

The library includes `StaticPeerDiscovery` for configuration-based peer discovery:

```go
peers := map[int]string{
    0: "node0.example.com:8000",
    1: "node1.example.com:8001",
    2: "node2.example.com:8002",
}
discovery := transport.NewStaticPeerDiscovery(peers)
```

Features:
- Thread-safe operations
- Dynamic updates via `UpdatePeers()`
- Suitable for most production deployments
- No external dependencies

## Using Discovery with HTTP Transport

### Method 1: Constructor with Discovery

```go
httpTransport, err := http.NewHTTPTransportWithDiscovery(
    transportConfig,
    discovery,
)
```

### Method 2: Static Peers Helper

```go
httpTransport, err := http.NewHTTPTransportWithStaticPeers(
    transportConfig,
    peers,
)
```

### Method 3: Builder Pattern

```go
httpTransport, err := http.NewBuilder(nodeID, address).
    WithDiscovery(discovery).
    WithTimeout(5 * time.Second).
    Build()
```

## Production Deployment Examples

### 1. Static Configuration (Recommended for Starting)

```go
// Define your cluster topology
clusterPeers := map[int]string{
    0: "raft-0.internal:8000",
    1: "raft-1.internal:8001",
    2: "raft-2.internal:8002",
}

// Create discovery
discovery := transport.NewStaticPeerDiscovery(clusterPeers)

// Use with transport
transport, err := http.NewHTTPTransportWithDiscovery(config, discovery)
```

### 2. Kubernetes StatefulSet

For Kubernetes deployments using StatefulSets:

```go
type K8sDiscovery struct {
    namespace   string
    serviceName string
    port        int
}

func (k *K8sDiscovery) GetPeerAddress(ctx context.Context, serverID int) (string, error) {
    // StatefulSet pods have predictable DNS names
    return fmt.Sprintf("raft-%d.%s.%s.svc.cluster.local:%d", 
        serverID, k.serviceName, k.namespace, k.port), nil
}
```

### 3. Consul Integration

```go
type ConsulDiscovery struct {
    client *consul.Client
    prefix string
}

func (c *ConsulDiscovery) GetPeerAddress(ctx context.Context, serverID int) (string, error) {
    key := fmt.Sprintf("%s/servers/%d", c.prefix, serverID)
    pair, _, err := c.client.KV().Get(key, nil)
    if err != nil {
        return "", err
    }
    return string(pair.Value), nil
}
```

### 4. Cloud Provider Integration

```go
type AWSDiscovery struct {
    ec2Client *ec2.Client
    tagKey    string
    tagValue  string
}

func (a *AWSDiscovery) GetPeerAddress(ctx context.Context, serverID int) (string, error) {
    // Query EC2 instances by tags
    // Return private IP of matching instance
}
```

## Dynamic Peer Updates

The discovery mechanism supports dynamic updates for cluster membership changes:

```go
// Initial cluster
discovery := transport.NewStaticPeerDiscovery(initialPeers)

// Later, update the peer list
discovery.UpdatePeers(newPeers)
```

## Best Practices

1. **Start Simple**: Begin with `StaticPeerDiscovery` and static configuration
2. **Use Service Discovery**: In dynamic environments, integrate with service discovery systems
3. **Handle Failures**: Implement retries and circuit breakers in custom discovery implementations
4. **Monitor Discovery**: Log discovery failures and latencies
5. **Cache Results**: Cache discovery results to reduce load on external systems

## Migration from Legacy Code

If you have existing code that was using hardcoded address resolution:

```go
// Old way (before discovery mechanism)
func getServerAddress(serverID int) string {
    return fmt.Sprintf("localhost:%d", 8000+serverID)
}

// New way with discovery
peers := map[int]string{
    0: "localhost:8000",
    1: "localhost:8001",
    2: "localhost:8002",
}
transport.SetDiscovery(transport.NewStaticPeerDiscovery(peers))
```

## Testing

For unit tests, you can create a simple mock discovery:

```go
type mockDiscovery struct {
    addresses map[int]string
}

func (m *mockDiscovery) GetPeerAddress(ctx context.Context, serverID int) (string, error) {
    addr, ok := m.addresses[serverID]
    if !ok {
        return "", fmt.Errorf("server %d not found", serverID)
    }
    return addr, nil
}

// Use in tests
transport.SetDiscovery(&mockDiscovery{
    addresses: map[int]string{
        0: testServer0.URL,
        1: testServer1.URL,
    },
})
```

## Security Considerations

- Use TLS for transport encryption (not yet implemented)
- Validate peer identities
- Use private networks or VPNs for cluster communication
- Implement authentication in custom discovery mechanisms

## Future Enhancements

- Built-in DNS SRV record discovery
- Automatic TLS certificate management
- Health checking integration
- Circuit breaker for discovery failures
- Metrics and observability hooks