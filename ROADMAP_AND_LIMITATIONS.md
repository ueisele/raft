# Roadmap and Limitations

This document consolidates all known limitations, unimplemented features, and future work items for the Raft implementation.

## Major Unimplemented Features

### 1. Joint Consensus
**Status**: Not implemented by design  
**Impact**: High for safety during configuration changes  
**Details**: 
- Full two-phase joint consensus protocol not implemented
- Cannot safely change majority of cluster at once
- Must add/remove servers one at a time
- Alternative approach: Safe server addition with automatic promotion
- Reasoning: Adds significant complexity for limited benefit in practice

### 2. Configuration Rollback
**Status**: Not implemented  
**Impact**: Medium - requires manual intervention  
**Details**:
- No automatic rollback of failed configuration changes
- Failed changes require administrator intervention
- No rollback mechanism specified in Raft paper

### 3. Leader Step-Down When Partitioned
**Status**: Not implemented  
**Impact**: Medium - affects liveness  
**Details**:
- Partitioned leader continues believing it's leader without majority
- Cannot commit new entries but remains in leader state
- Should step down to follower when it can't reach majority

### 4. Replicated SafeConfigurationManager State
**Status**: Not implemented  
**Impact**: Medium - affects automatic promotions  
**Details**:
- Automatic promotion state is local to each node
- New leader doesn't know about pending promotions
- Promotion decisions are lost during leader changes

### 5. Automatic Promotion to Voting
**Status**: Partially implemented  
**Impact**: Low - logs intent but doesn't execute  
**Details**:
- Currently only logs when a server should be promoted
- Doesn't actually execute the promotion
- Requires manual intervention to complete

## Performance Limitations

### Current Limitations
1. **HTTP RPC Overhead**: May not be optimal for high-performance scenarios
2. **Basic Persistence**: Not optimized for large logs
3. **Replication Under Load**: 
   - Nodes may fall significantly behind under extreme load
   - Blocks new replication RPCs while one is in flight
   - Can cause large divergences in commit indices
4. **No Batching**: Commands processed individually
5. **No Pipelining**: Sequential processing of log entries

### Planned Optimizations
1. **gRPC Transport**: Better performance than HTTP
2. **Binary Persistence Format**: More efficient than JSON
3. **Batching**: Group multiple commands for efficiency
4. **Pipelining**: Reduce latency by overlapping operations
5. **Pre-vote Optimization**: Reduce disruptions from isolated nodes
6. **Leader Lease**: Enable local reads without consensus

## Security Gaps

### Current State
- **No Authentication**: Any node can join the cluster
- **No Encryption**: All communication is plaintext
- **No Authorization**: No access control for operations
- **No Byzantine Fault Tolerance**: Assumes fail-stop model

### Required for Production
1. TLS encryption for all communication
2. Mutual authentication between nodes
3. Access control for client operations
4. Audit logging for security events

## Test Suite Issues

### Known Flaky Tests
1. **TestBasicMembershipChange**: ~70% pass rate due to cluster instability
2. **TestSnapshotInstallationRaceConditions**: Race condition in snapshot installation
3. **Timing-Sensitive Tests**: Many tests rely on sleep statements

### Test Improvements Needed
1. Replace remaining sleep statements with proper synchronization
2. Add stress tests for configuration changes
3. Add chaos engineering tests
4. Improve test isolation

## Documentation Gaps

### Missing Documentation
1. **Configuration Best Practices**: How to safely manage cluster membership
2. **Operational Guide**: Day-to-day cluster management
3. **Complete API Documentation**: All public interfaces
4. **Troubleshooting Guide**: Common issues and solutions
5. **Performance Tuning Guide**: Optimization strategies

## Production Readiness Checklist

### Essential Items
- [ ] TLS encryption support
- [ ] Authentication mechanism
- [ ] Monitoring and metrics endpoints
- [ ] Health check endpoints
- [ ] Graceful shutdown handling
- [ ] Backup and recovery procedures
- [ ] Log rotation support

### Nice to Have
- [ ] Pre-vote optimization
- [ ] Read-only optimizations
- [ ] Dynamic configuration updates
- [ ] Tracing support
- [ ] Rate limiting

## Priority Roadmap

### High Priority
1. Fix remaining flaky test (TestSnapshotInstallationRaceConditions)
2. Add TLS support for secure communication
3. Implement proper monitoring/metrics
4. Complete automatic promotion to voting

### Medium Priority
1. Implement leader step-down when partitioned
2. Add gRPC transport option
3. Improve replication performance under load
4. Add authentication mechanism

### Low Priority
1. Joint consensus (complexity vs benefit trade-off)
2. Configuration rollback mechanism
3. Binary persistence format
4. Pre-vote optimization

## Contributing

If you're interested in working on any of these items, please:
1. Check if there's an existing issue
2. Discuss the approach before implementation
3. Follow the existing code style and patterns
4. Add comprehensive tests for new features