# Raft Test Categories

## 1. Unit Tests
Tests for individual components in isolation with mocked dependencies.

### Core Component Unit Tests
- **log_test.go** - Tests LogManager operations (append, truncate, get entries)
- **state_test.go** - Tests StateManager transitions between Follower/Candidate/Leader states
- **node_test.go** - Tests node configuration, validation, and basic operations
- **election_test.go** - Tests election logic, vote granting, and term management
- **configuration_test.go** - Tests configuration changes (add/remove servers)
- **safe_configuration_test.go** - Tests safe configuration management and promotion logic

### Basic Functionality Tests
- **basic_test.go** - Tests basic node creation and lifecycle
- **persistence_test.go** - Tests persistence layer operations (save/restore state)

## 2. Integration Tests
Tests that verify interactions between multiple components or nodes.

### Standard Integration Tests
- **integration_test.go** - Core integration tests:
  - TestLeaderElection
  - TestLogReplication
  - TestLeaderFailover
  - TestNetworkPartition
  - TestPersistence

### Multi-Node Behavior Tests
- **multi_node_test.go** - Tests behavior across multiple nodes
- **debug_multi_test.go** - Multi-node tests with debug transport for detailed testing
- **replication_test.go** - Tests log replication across nodes
- **membership_test.go** - Tests membership changes in a running cluster

## 3. Safety and Correctness Tests
Tests that verify Raft safety properties and algorithm correctness.

### Core Safety Properties
- **safety_test.go** - Tests fundamental Raft safety properties:
  - TestElectionSafety (at most one leader per term)
  - TestLogMatchingProperty
  - TestLeaderCompleteness

### Extended Safety Tests
- **safety_extended_test.go** - Additional safety scenarios
- **voting_safety_test.go** - Tests voting-related safety properties
- **voting_member_safety_analysis_test.go** - Analysis of voting member safety
- **voting_safety_demo_test.go** - Demonstrations of voting safety scenarios
- **vote_denial_test.go** - Tests vote denial scenarios

## 4. Fault Tolerance and Edge Case Tests
Tests that verify behavior under failures and edge conditions.

### Network Partition Tests
- **partition_test.go** - Basic network partition scenarios
- **partition_advanced_test.go** - Complex partition scenarios (asymmetric, healing)

### Edge Cases and Corner Scenarios
- **edge_cases_test.go** - Various edge cases:
  - TestPendingConfigChangeBlocking
  - TestTwoNodeElectionDynamics
  - TestLeaderSnapshotDuringConfigChange
  - TestStaleConfigEntriesAfterPartition
  - TestRapidLeadershipChanges

### Leader Stability Tests
- **leader_stability_test.go** - Tests leader stability under various conditions
- **leadership_transfer_test.go** - Tests orderly leadership transfer

### Healing and Recovery Tests
- **healing_test.go** - Tests cluster healing after partitions
- **healing_eventual_test.go** - Tests eventual consistency after healing

## 5. Feature-Specific Tests
Tests for specific Raft features and extensions.

### Snapshot Tests
- **snapshot_test.go** - Basic snapshot functionality:
  - TestSnapshotCreation
  - TestSnapshotInstallation
  - TestSnapshotWithConcurrentWrites
- **snapshot_advanced_test.go** - Advanced snapshot scenarios
- **replication_snapshot_test.go** - Snapshot replication tests

### Configuration Change Tests
- **configuration_edge_test.go** - Edge cases in configuration changes

### Client Interaction Tests
- **client_example_test.go** - Example client usage patterns
- **client_interaction_test.go** - Client request handling
- **client_extended_test.go** - Extended client scenarios

## 6. Extended/Stress Tests
Tests that verify behavior under extended operation or stress.

### Persistence Integration Tests
- **persistence_integration_test.go** - Integration tests for persistence:
  - TestPersistenceWithCrash
  - TestPersistenceWithMultipleNodes
  - TestPersistenceAcrossElections
  - TestPersistenceWithPartialClusterFailure
  - TestPersistenceStressTest

### Extended Replication Tests
- **replication_extended_test.go** - Extended replication scenarios

## Test Organization Summary

| Category | Count | Purpose |
|----------|-------|---------|
| Unit Tests | 8 | Test individual components in isolation |
| Integration Tests | 5 | Test component interactions and multi-node behavior |
| Safety Tests | 6 | Verify Raft safety properties and correctness |
| Fault Tolerance Tests | 7 | Test behavior under failures and edge cases |
| Feature Tests | 6 | Test specific Raft features (snapshots, config changes, client) |
| Extended Tests | 3 | Test under extended operation and stress |

Total: 35 test files (excluding backup directory)