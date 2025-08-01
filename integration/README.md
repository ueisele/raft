# Integration Tests

This directory contains all integration tests for the Raft implementation.
Integration tests verify the behavior of multiple components working together
in realistic scenarios.

## Directory Structure
- `basic/` - Basic multi-node scenarios
- `cluster/` - Cluster operations (election, replication, membership)
- `configuration/` - Configuration change scenarios
- `fault_tolerance/` - Partition, healing, and persistence tests
- `safety/` - Raft safety property verification
- `client/` - Client interaction patterns
- `snapshot/` - Snapshot operations
- `leadership/` - Leadership stability and transfer
- `stress/` - Edge cases and stress tests
- `helpers/` - Shared test utilities

## Running Tests
```bash
# Run all integration tests
go test ./integration/...

# Run specific category
go test ./integration/configuration/...

# Run with verbose output
go test -v ./integration/...

# Run with race detection
go test -race ./integration/...
```

## Test Guidelines

Integration tests should:
- Test interactions between multiple components
- Use realistic multi-node clusters
- Verify end-to-end behavior
- Test fault tolerance scenarios
- Not mock core Raft components

For unit tests of individual components, see the `*_test.go` files
in the root directory next to their implementations.