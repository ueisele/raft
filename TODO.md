# TODO List - Raft Implementation Refactoring

## Completed Tasks ✅

1. **Fix direct cluster.nodes[] access in fault_tolerance/persistence_test.go**
   - Replaced array access with map-based API
   
2. **Refactor partition_test.go to use transport decorators**
   - Replaced custom asymmetric transport with decorator pattern
   
3. **Fix compilation errors in healing_test.go and partition_test.go**
   - Updated to use new transport test helpers
   
4. **Fix assertion helpers to not use misleading slice indices as node IDs**
   - Assertion helpers now use actual node IDs, not array indices
   
5. **Add GetID() method to Node interface**
   - Breaking change accepted for better node identification
   
6. **Ensure all fault_tolerance tests pass**
   - Fixed flaky tests and timing issues
   
7. **Fix failing persistence tests**
   - TestCrashRecoveryScenarios - Now properly verifies data preservation
   - TestPersistenceWithSnapshots - Actually verifies snapshots are created/loaded
   
8. **Replace time.Sleep in transport/http_transport_test.go**
   - Replaced 17 occurrences with condition-based waiting
   
9. **Replace time.Sleep in configuration/edge_cases_test.go**
   - Replaced 8 occurrences with proper synchronization
   
10. **Fix encapsulation issue with direct Registry access**
    - Added AddNodeWithConfig method to TestCluster
    
11. **Fix tests that make assumptions without verification**
    - Fixed TestPersistenceWithSnapshots (was assuming snapshots)
    - Fixed TestConfigurationPersistence (was completely skipped)
    - Fixed TestNewVotingServerSafety (was just "demonstrating concept")
    - Fixed TestLeadershipTransferToSpecificNode (false success)
    - Fixed TestLeadershipTransferConstraints (assumptions about "real implementation")

## Discovered Bugs 🐛

1. **BUG: Configuration changes not being persisted**
   - Found when we unskipped TestConfigurationPersistence
   - Configuration changes are lost after cluster restart
   - Critical for production use
   
2. **BUG: Leadership transfer doesn't go to specified target**
   - Found when we fixed TestLeadershipTransferToSpecificNode
   - Without TimeoutNow RPC, transfers go to random nodes
   - Test was falsely claiming success

## Pending Tasks 📝

### High Priority - Bugs to Fix
- [ ] Fix configuration persistence bug
- [ ] Fix leadership transfer targeting bug

### Test Improvements
- [ ] Replace time.Sleep in snapshot/advanced_test.go (12 occurrences)
- [ ] Replace time.Sleep in snapshot/basic_test.go
- [ ] Replace time.Sleep in remaining test files
- [ ] Migrate manual node creation to TestCluster in transport tests
- [ ] Migrate manual node creation in remaining tests  
- [ ] Replace WaitForStableCluster with new helper methods
- [ ] Add assertion helpers throughout all tests

### Code Cleanup
- [ ] Remove backward compatibility Nodes and Transports array fields from TestCluster struct
- [ ] Verify all tests compile and pass

## Key Lessons Learned 📚

1. **Tests that skip verification hide real bugs**
   - We found 2 critical bugs by fixing tests that were skipped or making assumptions
   
2. **Always verify actual behavior, never assume**
   - Tests must check what actually happened, not what should happen
   
3. **Tests that falsely claim success are dangerous**
   - They provide false confidence and hide real issues
   
4. **"Would work in real implementation" is not a test**
   - Test the current code, not hypothetical implementations
   
5. **Crash recovery tests must verify data preservation**
   - Not just cluster stability, but actual data integrity

## Notes

- All tests should use condition-based waiting instead of time.Sleep for synchronization
- Use subshells `(cd dir && command)` for directory changes to preserve working directory
- Tests must fail properly when expectations aren't met
- Document any new bugs found when fixing tests