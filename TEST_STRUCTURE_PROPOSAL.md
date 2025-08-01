# Raft Test Structure Refactoring Proposal

## Current State Analysis

### Test Files Summary (28 files)
- **Unit Tests**: 6 files (state, log, election, replication, node, basic)
- **Integration Tests**: 2 files (integration, membership)
- **Configuration Tests**: 4 files (configuration, configuration_edge, safe_configuration, leadership_transfer)
- **Fault Tolerance Tests**: 4 files (partition, partition_advanced, healing, persistence/persistence_integration)
- **Safety Tests**: 3 files (safety, safety_extended, voting_safety)
- **Client Tests**: 3 files (client_example, client_extended, client_interaction)
- **Snapshot Tests**: 2 files (snapshot, snapshot_advanced)
- **Leadership Tests**: 2 files (leader_stability, leadership_transfer)
- **Edge Cases**: 1 file (edge_cases)
- **Extended Tests**: 2 files (replication_extended, persistence_integration)

### Test Helpers (3 files)
- `test_helpers.go`: Core mocks and test infrastructure
- `test_wait_helpers.go`: Timing and synchronization utilities
- `test/wait_helpers.go`: Package-level wait helpers (to avoid import cycles)

## Proposed New Structure

### Core Principle
- **Unit tests** stay next to their components in the root `raft/` directory (Go convention)
- **Integration tests** move to `integration/` directory, organized by feature/domain
- Clear separation between fast unit tests and slower integration tests

### 1. Directory Organization

```
raft/
├── # Core implementation files with their unit tests
├── state.go
├── state_test.go                    # Unit tests for StateManager
├── log.go  
├── log_test.go                      # Unit tests for LogManager
├── election.go
├── election_test.go                 # Unit tests for ElectionManager
├── replication.go
├── replication_test.go              # Unit tests for ReplicationManager
├── configuration.go
├── configuration_test.go            # Unit tests for ConfigurationManager
├── safe_configuration.go
├── safe_configuration_test.go       # Unit tests for SafeConfigurationManager
├── node.go
├── node_test.go                     # Unit tests for Node
├── snapshot_manager.go
├── snapshot_manager_test.go         # Unit tests for SnapshotManager
│
├── # Test helpers
├── test_helpers.go                  # Shared test utilities
├── test_wait_helpers.go            # Timing helpers
│
└── integration/                     # All integration tests
    ├── basic/
    │   └── basic_test.go           # Basic node lifecycle, simple scenarios
    │
    ├── cluster/
    │   ├── election_test.go        # Multi-node election scenarios
    │   ├── replication_test.go     # Log replication across cluster
    │   ├── replication_extended_test.go # Extended replication tests with failures
    │   └── membership_test.go      # Adding/removing nodes
    │
    ├── configuration/
    │   ├── changes_test.go         # Configuration change scenarios
    │   ├── edge_cases_test.go      # Config change edge cases
    │   └── safe_addition_test.go   # Safe server addition scenarios
    │
    ├── fault_tolerance/
    │   ├── partition_test.go       # Network partition scenarios
    │   ├── healing_test.go         # Cluster healing after partitions
    │   └── persistence_test.go     # Crash recovery, state persistence
    │
    ├── safety/
    │   ├── core_test.go           # Core Raft safety properties
    │   ├── voting_test.go         # Voting safety scenarios
    │   └── extended_test.go       # Extended safety scenarios
    │
    ├── client/
    │   ├── basic_test.go          # Basic client interactions
    │   ├── advanced_test.go       # Linearizable reads, retries, etc.
    │   └── concurrent_test.go     # Concurrent client scenarios
    │
    ├── snapshot/
    │   ├── basic_test.go          # Snapshot creation/installation
    │   └── advanced_test.go       # Complex snapshot scenarios
    │
    ├── leadership/
    │   ├── stability_test.go      # Leader stability tests
    │   └── transfer_test.go       # Leadership transfer scenarios
    │
    ├── stress/
    │   ├── edge_cases_test.go     # Various edge cases
    │   └── load_test.go           # High load scenarios
    │
    └── helpers/
        ├── cluster.go             # Cluster setup helpers
        ├── transport.go           # Test transport implementations
        ├── timing.go              # Timing utilities
        └── assertions.go          # Test assertions
```

### 2. Test Separation Strategy

#### Unit Tests (in root directory)
- Test single components in isolation
- Use mocks for dependencies
- Fast execution (milliseconds)
- No network communication
- No multi-node setup

Examples:
- `state_test.go`: Tests StateManager transitions, term updates, voting
- `log_test.go`: Tests LogManager append, truncate, matching
- `configuration_test.go`: Tests ConfigurationManager operations

#### Integration Tests (in integration/ directory)
- Test multiple components working together
- Real multi-node clusters
- Network communication (even if in-memory)
- Slower execution (seconds)
- Test end-to-end scenarios

Examples:
- `integration/cluster/election_test.go`: Multi-node leader election
- `integration/configuration/changes_test.go`: Configuration changes in running cluster
- `integration/fault_tolerance/partition_test.go`: Network partition handling

### 3. Test Execution

```bash
# Run unit tests only (fast, for development)
go test .

# Run unit tests with coverage
go test -cover .

# Run all integration tests
go test ./integration/...

# Run specific integration category
go test ./integration/configuration/...
go test ./integration/fault_tolerance/...

# Run everything (unit + integration)
go test ./...

# Run with race detection
go test -race ./...

# Run short tests only (skip slow tests)
go test -short ./...
```

### 4. Benefits of This Structure

1. **Go Convention Compliance**: Unit tests next to code they test
2. **Clear Separation**: Obvious distinction between unit and integration tests
3. **Fast Development Cycle**: Run unit tests quickly during development
4. **Feature Organization**: Integration tests grouped by feature/domain
5. **Selective Testing**: Easy to run specific test categories
6. **CI/CD Optimization**: Can run unit and integration tests in parallel
7. **No Ambiguity**: `integration/` name clearly indicates test type

### 5. Migration Benefits

**From Current State (28 files in root)**:
- Reduced clutter in root directory
- Better organization and discoverability
- Clear test categories
- Easier to identify where to add new tests

**File Count Reduction**:
- Current: 28 test files in root
- After: ~10 unit test files in root + ~18 integration test files organized by feature
- Better: Clear separation and organization even with same file count

### 6. Test Helper Organization

**Shared Helpers (root directory)**:
- `test_helpers.go`: Mocks used by both unit and integration tests
- `test_wait_helpers.go`: Timing utilities used across tests

**Integration-Specific Helpers**:
- `integration/helpers/cluster.go`: Cluster setup for integration tests
- `integration/helpers/transport.go`: Network transports for testing
- `integration/helpers/assertions.go`: Integration test assertions

### 7. Example Test Classifications

| Current File | Unit Tests | Integration Tests |
|-------------|------------|-------------------|
| state_test.go | All tests (keep in root) | None |
| configuration_test.go | TestConfigurationManager | TestBasicConfigurationChange |
| snapshot_test.go | TestSnapshotCreation | TestSnapshotInstallation |
| persistence_test.go | TestBasicPersistence | TestPersistenceWithCrash |
| voting_safety_test.go | None | All tests (voting scenarios) |

### 8. Guidelines for New Tests

**Add Unit Test When**:
- Testing a single component/type
- Testing pure logic (no I/O, no concurrency)
- Test can use mocks for dependencies
- Test runs in milliseconds

**Add Integration Test When**:
- Testing interaction between components
- Testing distributed scenarios
- Requiring multiple nodes
- Testing fault tolerance
- Testing end-to-end workflows

### 9. Future Enhancements

1. **Build Tags**: Add build tags for slow tests
2. **Benchmarks**: Add benchmark tests for performance
3. **Fuzzing**: Add fuzz tests for robustness
4. **Property Tests**: Add property-based tests
5. **Load Tests**: Add dedicated load testing suite

### 10. Success Metrics

1. **Development Speed**: Faster feedback from unit tests
2. **Test Clarity**: Clear where each test belongs
3. **CI Performance**: Parallel execution reduces time
4. **Maintainability**: Easier to find and update tests
5. **Coverage**: No reduction in test coverage