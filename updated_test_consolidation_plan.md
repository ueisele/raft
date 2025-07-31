# Updated Test Consolidation Plan After Raft Paper Review

## Key Findings

After reviewing the Raft paper and examining the test files more closely:

### 1. Voting Safety Tests - KEEP SEPARATE
The three voting safety test files appear to test the same critical scenario from different angles:
- **voting_safety_test.go**: Tests the basic safety issue with immediate voting rights
- **voting_safety_demo_test.go**: Demonstrates the concrete danger with partitions
- **voting_member_safety_analysis_test.go**: Provides detailed analysis of the safety implications

**Decision**: These could potentially be consolidated into one comprehensive test file, but they each provide different perspectives on a critical safety issue mentioned in Section 6 of the paper.

### 2. Debug Multi Test - CAN BE REMOVED
After examination, `debug_multi_test.go` is just a basic multi-node test with debug logging enabled. It doesn't test any unique scenarios.

**Decision**: Safe to remove as originally planned.

### 3. Persistence Tests - KEEP BOTH
These likely test different aspects:
- **persistence_test.go**: Basic persistence functionality
- **persistence_integration_test.go**: Multi-node crash recovery scenarios

**Decision**: Keep both as they test different aspects of the Leader Completeness property.

## Revised Consolidation Plan

### Files to Consolidate (Reduced List):

1. **Voting Safety Tests** → Consolidate all three into `voting_safety_test.go`
   - They test the same scenario from different angles
   - Can be organized as subtests in one file

2. **Multi-node Tests** → Remove redundant tests
   - Remove `multi_node_test.go` (merge into integration_test.go)
   - Remove `debug_multi_test.go` (just debug logging, no unique tests)

3. **Healing Tests** → Merge into one file
   - Merge `healing_eventual_test.go` into `healing_test.go`

4. **Snapshot Tests** → Better organization
   - Move `replication_snapshot_test.go` content to `snapshot_advanced_test.go`

### Files to Keep Separate:

1. **Persistence Tests** - Keep both files
2. **Edge Cases Tests** - Tests many unique scenarios
3. **Configuration Tests** - Well separated by complexity
4. **Safety Tests** - Core Raft properties

## Critical Test Coverage to Verify

Before consolidation, ensure these scenarios are covered:

### From Figure 8 (Section 5.4.2):
- Old log entries from previous terms cannot determine commitment
- Must have tests that create the exact scenario in Figure 8

### From Section 6 (Membership Changes):
- Joint consensus (Cold,new) prevents split brain
- New servers must catch up before voting

### From Section 5.2 (Leader Election):
- Vote denial with active leader
- Election timeout randomization effectiveness

### From Section 5.3 (Log Replication):
- Log repair after leader changes
- Consistency check in AppendEntries

## Final Recommendation

**Proceed with consolidation but with these modifications:**

1. **Consolidate voting safety tests** into one comprehensive file with subtests
2. **Keep both persistence test files** - they test different scenarios
3. **Remove only truly redundant tests** like debug_multi_test.go
4. **Before removing any test**, grep for unique test scenarios that might not be obvious from the test name

**Expected outcome:**
- Reduce from 35 to 31 test files (-11%)
- Better organization while preserving all critical test coverage
- Each remaining test file has a clear, distinct purpose

## Verification Steps

1. Run test coverage before consolidation
2. Create a mapping of which scenarios each test covers
3. Consolidate tests
4. Run test coverage after to ensure no reduction
5. Run consolidated tests multiple times to ensure no flakiness introduced