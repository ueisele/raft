# Migration Plan: Making PeerDiscovery Mandatory for HTTPTransport

## Overview

This document outlines a phased migration plan to make `PeerDiscovery` a required constructor parameter for `HTTPTransport`, ensuring all transports have proper peer discovery configured from creation.

## Current State

```go
// Current: Discovery is optional
transport := http.NewHTTPTransport(config)
transport.SetDiscovery(discovery) // Can be forgotten
```

## Target State

```go
// Target: Discovery is required
transport := http.NewHTTPTransport(config, discovery)
// No SetDiscovery method needed
```

## Migration Phases

### Phase 1: Add New Constructor and Validation (v0.9.0)
**Timeline: Current Release**
**Breaking Changes: None**

1. Add new required-discovery constructor:
```go
// New constructor with required discovery
func NewHTTPTransportWithDiscovery(config *transport.Config, discovery transport.PeerDiscovery) (*HTTPTransport, error) {
    if discovery == nil {
        return nil, fmt.Errorf("discovery cannot be nil")
    }
    return &HTTPTransport{
        serverID:   config.ServerID,
        address:    config.Address,
        httpClient: &http.Client{
            Timeout: time.Duration(config.RPCTimeout) * time.Millisecond,
        },
        discovery: discovery,
    }, nil
}
```

2. Mark existing constructor as deprecated:
```go
// Deprecated: Use NewHTTPTransportWithDiscovery instead.
// This constructor will require discovery parameter in v1.0.0
func NewHTTPTransport(config *transport.Config) *HTTPTransport {
    // ... existing implementation
}
```

3. Add runtime validation in Start():
```go
func (t *HTTPTransport) Start() error {
    if t.handler == nil {
        return fmt.Errorf("RPC handler not set")
    }
    if t.discovery == nil {
        return fmt.Errorf("peer discovery not set - use SetDiscovery() or NewHTTPTransportWithDiscovery()")
    }
    // ... rest of implementation
}
```

### Phase 2: Make Discovery Required (v1.0.0)
**Timeline: Major Version Release**
**Breaking Changes: Yes**

1. Update the main constructor signature:
```go
// NewHTTPTransport creates a new HTTP transport with required discovery
func NewHTTPTransport(config *transport.Config, discovery transport.PeerDiscovery) (*HTTPTransport, error) {
    if discovery == nil {
        return nil, fmt.Errorf("discovery cannot be nil")
    }
    
    return &HTTPTransport{
        serverID:   config.ServerID,
        address:    config.Address,
        httpClient: &http.Client{
            Timeout: time.Duration(config.RPCTimeout) * time.Millisecond,
        },
        discovery: discovery,
    }, nil
}
```

2. Remove SetDiscovery method:
```go
// Remove this method - discovery is now immutable
// func (t *HTTPTransport) SetDiscovery(discovery transport.PeerDiscovery)
```

3. Remove the duplicate constructor:
```go
// Remove NewHTTPTransportWithDiscovery as it's now redundant
```

## Migration Guide for Users

### Step 1: Identify Usage (During Phase 1)

Search for current usage:
```bash
# Find all NewHTTPTransport calls
grep -r "NewHTTPTransport(" . --include="*.go"

# Find all SetDiscovery calls
grep -r "SetDiscovery(" . --include="*.go"
```

### Step 2: Update Code Patterns

#### Pattern 1: Basic Usage
```go
// Old code
transport := http.NewHTTPTransport(config)
transport.SetDiscovery(discovery)

// New code (Phase 1+)
transport, err := http.NewHTTPTransportWithDiscovery(config, discovery)
if err != nil {
    return err
}
```

#### Pattern 2: With Static Peers
```go
// Old code
transport := http.NewHTTPTransport(config)
peers := map[int]string{...}
transport.SetDiscovery(transport.NewStaticPeerDiscovery(peers))

// New code (Phase 1+)
peers := map[int]string{...}
transport, err := http.NewHTTPTransportWithStaticPeers(config, peers)
if err != nil {
    return err
}
```

#### Pattern 3: Test Code
```go
// Old test code
transport := http.NewHTTPTransport(config)
transport.SetDiscovery(&mockDiscovery{...})

// New test code (Phase 1+)
discovery := &mockDiscovery{...}
transport, err := http.NewHTTPTransportWithDiscovery(config, discovery)
require.NoError(t, err)
```

### Step 3: Update Integration Tests

For integration tests that create multiple transports:
```go
// Old code
transports := make([]*http.HTTPTransport, 3)
for i := 0; i < 3; i++ {
    transports[i] = http.NewHTTPTransport(configs[i])
}
// Later...
discovery := transport.NewStaticPeerDiscovery(peers)
for _, trans := range transports {
    trans.SetDiscovery(discovery)
}

// New code
discovery := transport.NewStaticPeerDiscovery(peers)
transports := make([]*http.HTTPTransport, 3)
for i := 0; i < 3; i++ {
    trans, err := http.NewHTTPTransportWithDiscovery(configs[i], discovery)
    if err != nil {
        t.Fatalf("Failed to create transport: %v", err)
    }
    transports[i] = trans
}
```

## Implementation Checklist

### Phase 1 Implementation
- [ ] Add `NewHTTPTransportWithDiscovery` constructor
- [ ] Add `NewHTTPTransportWithStaticPeers` helper
- [ ] Update builder to use new constructor
- [ ] Add validation in `Start()` method
- [ ] Update documentation with deprecation notices
- [ ] Add examples using new constructors
- [ ] Update DISCOVERY.md with migration notes

### Phase 2 Implementation
- [ ] Add runtime deprecation warnings
- [ ] Add compile-time deprecation annotations
- [ ] Create migration script/tool (optional)
- [ ] Update all examples to use new pattern
- [ ] Blog post/announcement about upcoming change

### Phase 3 Implementation
- [ ] Update `NewHTTPTransport` signature
- [ ] Remove `SetDiscovery` method
- [ ] Remove `NewHTTPTransportWithDiscovery` (now redundant)
- [ ] Update all tests
- [ ] Update all documentation
- [ ] Update semantic version to 1.0.0

## Benefits After Migration

1. **Fail-Fast**: Invalid configurations caught at compile time
2. **Immutability**: Discovery cannot be changed after creation
3. **Thread Safety**: No race conditions during initialization
4. **Clearer API**: Required dependencies are explicit
5. **Better Testing**: Forces proper setup in tests

## Rollback Plan

If issues arise during migration:

1. **Phase 1**: No rollback needed (backward compatible)
2. **Phase 2**: Remove warnings in patch release
3. **Phase 3**: Provide compatibility layer:
```go
// Temporary compatibility wrapper
func NewHTTPTransportCompat(config *transport.Config) *HTTPTransportBuilder {
    return &HTTPTransportBuilder{config: config}
}

type HTTPTransportBuilder struct {
    config *transport.Config
    discovery transport.PeerDiscovery
}

func (b *HTTPTransportBuilder) SetDiscovery(d transport.PeerDiscovery) *HTTPTransportBuilder {
    b.discovery = d
    return b
}

func (b *HTTPTransportBuilder) Build() (*HTTPTransport, error) {
    return NewHTTPTransport(b.config, b.discovery)
}
```

## Implementation Status

### ✅ Phase 1 (Completed)
- Added validation in `Start()` requiring discovery
- Added deprecation documentation
- Created migration tools and examples
- Maintained backward compatibility

### ✅ Phase 2 (Completed - Combined with v1.0.0)
- Made discovery a required constructor parameter
- Removed `SetDiscovery()` method
- Removed the old constructor without discovery
- All tests and examples updated

## Breaking Changes in v1.0.0

The following changes have been implemented:

1. **Constructor now requires discovery**:
   ```go
   // Old (no longer supported)
   transport := http.NewHTTPTransport(config)
   transport.SetDiscovery(discovery)
   
   // New (required)
   transport, err := http.NewHTTPTransport(config, discovery)
   ```

2. **SetDiscovery method removed**: Discovery is now immutable after construction

3. **NewHTTPTransportWithDiscovery deprecated**: Use `NewHTTPTransport` directly

4. **Error handling required**: Constructor now returns an error

This ensures all transports have proper peer discovery configured from creation, improving reliability and preventing runtime failures.