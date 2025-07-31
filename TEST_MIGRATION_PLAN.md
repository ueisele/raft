# Test Migration Plan

## Overview
This document provides a step-by-step plan to migrate from the current test structure (28 files in root) to a cleaner structure where unit tests remain in the root directory next to their components, and integration tests are organized by feature/domain in an `integration/` directory.

## Core Migration Principles

1. **Unit tests stay in place** - Follow Go convention of `*_test.go` next to `*.go`
2. **Integration tests move to `integration/`** - Organized by feature/domain
3. **Mixed test files get split** - Separate unit from integration tests
4. **Test helpers get reorganized** - Shared helpers stay in root, integration-specific move

## Current State Analysis

### Pure Unit Test Files (Keep in Root)
- `state_test.go` - Tests StateManager component
- `log_test.go` - Tests LogManager component  
- `election_test.go` - Tests ElectionManager component
- `replication_test.go` - Tests ReplicationManager component
- `node_test.go` - Tests Node configuration/validation

### Pure Integration Test Files (Move to integration/)
- `integration_test.go` - Multi-node scenarios
- `voting_safety_test.go` - Voting safety scenarios
- `leader_stability_test.go` - Leader stability tests
- `membership_test.go` - Membership changes
- `healing_test.go` - Partition healing
- All other multi-node test files

### Mixed Test Files (Need Splitting)
- `configuration_test.go` - Contains both unit and integration tests
- `safe_configuration_test.go` - Contains both unit and integration tests
- `snapshot_test.go` - Contains both unit and integration tests
- `persistence_test.go` - Contains both unit and integration tests

## Migration Phases

### Phase 1: Create Directory Structure

```bash
# Create integration test directories
mkdir -p integration/{basic,cluster,configuration,fault_tolerance,safety,client,snapshot,leadership,stress,helpers}

# Create README files
cat > integration/README.md << EOF
# Integration Tests

This directory contains all integration tests for the Raft implementation.
Integration tests verify the behavior of multiple components working together
in realistic scenarios.

## Directory Structure
- basic/ - Basic multi-node scenarios
- cluster/ - Cluster operations (election, replication, membership)
- configuration/ - Configuration change scenarios
- fault_tolerance/ - Partition, healing, and persistence tests
- safety/ - Raft safety property verification
- client/ - Client interaction patterns
- snapshot/ - Snapshot operations
- leadership/ - Leadership stability and transfer
- stress/ - Edge cases and stress tests
- helpers/ - Shared test utilities

## Running Tests
\`\`\`bash
# Run all integration tests
go test ./integration/...

# Run specific category
go test ./integration/configuration/...
\`\`\`
EOF
```

### Phase 2: Extract Integration-Specific Helpers

#### 2.1 Move Transport Implementations
Create `integration/helpers/transport.go`:
```go
package helpers

// Move from test_helpers.go:
// - multiNodeTransport
// - debugTransport  
// - partitionableTransport
// - Associated registries (nodeRegistry, debugNodeRegistry, partitionRegistry)
```

#### 2.2 Create Cluster Setup Utilities
Create `integration/helpers/cluster.go`:
```go
package helpers

type TestCluster struct {
    Nodes []raft.Node
    // ... cluster management
}

func NewTestCluster(t *testing.T, size int, opts ...ClusterOption) *TestCluster
func (c *TestCluster) WaitForLeader(t *testing.T) int
func (c *TestCluster) PartitionNode(nodeID int)
// ... other cluster operations
```

#### 2.3 Move Timing Utilities
Create `integration/helpers/timing.go`:
```go
package helpers

// Move integration-specific timing helpers
// Keep basic timing config in test_wait_helpers.go for unit tests
```

### Phase 3: Split Mixed Test Files

#### 3.1 Split configuration_test.go

**Keep in `configuration_test.go` (unit tests):**
```go
func TestConfigurationManager(t *testing.T) { ... }
// Other unit tests that test ConfigurationManager directly
```

**Move to `integration/configuration/changes_test.go`:**
```go
func TestBasicConfigurationChange(t *testing.T) { ... }
// Other tests that require multi-node setup
```

#### 3.2 Split safe_configuration_test.go

**Create `safe_configuration_test.go` (unit tests):**
```go
func TestSafeConfigurationManager(t *testing.T) { ... }
func TestCatchUpProgressCalculation(t *testing.T) { ... }
```

**Move to `integration/configuration/safe_addition_test.go`:**
```go
func TestSafeServerAddition(t *testing.T) { ... }
```

#### 3.3 Split snapshot_test.go

**Keep unit tests in `snapshot_manager_test.go`:**
```go
func TestSnapshotCreation(t *testing.T) { ... }
// Tests that don't require multi-node setup
```

**Move to `integration/snapshot/basic_test.go`:**
```go
func TestSnapshotInstallation(t *testing.T) { ... }
func TestSnapshotWithConcurrentWrites(t *testing.T) { ... }
```

### Phase 4: Move Pure Integration Tests

| Current File | Target Location | New Name |
|-------------|-----------------|----------|
| integration_test.go | integration/cluster/ | Split into election_test.go, replication_test.go |
| voting_safety_test.go | integration/safety/voting_test.go | Keep same |
| safety_test.go | integration/safety/core_test.go | Keep same |
| safety_extended_test.go | integration/safety/extended_test.go | Keep same |
| leader_stability_test.go | integration/leadership/stability_test.go | Keep same |
| leadership_transfer_test.go | integration/leadership/transfer_test.go | Keep same |
| partition_test.go + partition_advanced_test.go | integration/fault_tolerance/partition_test.go | Merge files |
| healing_test.go | integration/fault_tolerance/healing_test.go | Keep same |
| persistence_integration_test.go | integration/fault_tolerance/persistence_test.go | Rename |
| client_*.go (3 files) | integration/client/ | Consolidate to basic_test.go, advanced_test.go |
| edge_cases_test.go | integration/stress/edge_cases_test.go | Keep same |

### Phase 5: Update Imports

#### 5.1 Update Integration Test Imports
```go
// Before
import (
    "testing"
    "github.com/ueisele/raft"
)

// After  
import (
    "testing"
    "github.com/ueisele/raft"
    "github.com/ueisele/raft/integration/helpers"
)
```

#### 5.2 Update Helper References
- Change transport references to use `helpers.NewMultiNodeTransport`
- Change cluster setup to use `helpers.NewTestCluster`

### Phase 6: Verify and Clean Up

#### 6.1 Verification Checklist
- [ ] All unit tests still pass: `go test .`
- [ ] All integration tests pass: `go test ./integration/...`
- [ ] No test files left in root that should be in integration/
- [ ] No duplicate test functions
- [ ] Test coverage maintained or improved

#### 6.2 Clean Up
- Remove old test files that have been moved
- Update CI/CD configuration
- Update documentation

### Phase 7: CI/CD Updates

#### 7.1 Update GitHub Actions
```yaml
name: Tests
on: [push, pull_request]

jobs:
  unit-tests:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v3
      - uses: actions/setup-go@v4
      - name: Run Unit Tests
        run: go test -v -race -cover .

  integration-tests:
    runs-on: ubuntu-latest
    strategy:
      matrix:
        category: [cluster, configuration, fault_tolerance, safety, client, snapshot, leadership, stress]
    steps:
      - uses: actions/checkout@v3
      - uses: actions/setup-go@v4
      - name: Run Integration Tests - ${{ matrix.category }}
        run: go test -v -race ./integration/${{ matrix.category }}/...
```

## Migration Timeline

### Week 1: Preparation
- Day 1-2: Create directory structure and documentation
- Day 3-4: Extract and reorganize helpers
- Day 5: Review and adjust plan based on findings

### Week 2: Test Migration
- Day 1-2: Split mixed test files
- Day 3-4: Move pure integration tests
- Day 5: Update imports and verify

### Week 3: Finalization
- Day 1-2: Update CI/CD configuration
- Day 3-4: Final verification and cleanup
- Day 5: Documentation and team communication

## Rollback Plan

1. All changes in feature branch
2. Original files preserved until verification complete
3. Can revert to main branch if issues arise
4. Gradual migration allows partial rollback

## Success Criteria

1. **No Test Loss**: Same number of test functions before/after
2. **Coverage Maintained**: Test coverage ≥ current level
3. **Faster Development**: Unit tests run in < 1 second
4. **Clear Organization**: Developers can easily find/add tests
5. **CI Performance**: Parallel execution reduces total time

## Post-Migration Benefits

1. **Development Speed**: Run `go test .` for instant feedback
2. **Clear Structure**: Obvious where each test belongs
3. **Better CI/CD**: Parallel execution of test categories
4. **Easier Onboarding**: New developers understand test organization
5. **Maintainability**: Related tests grouped together