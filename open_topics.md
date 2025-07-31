# Open Topics and Known Limitations

This document outlines the known limitations, open topics, and potential improvements for this Raft implementation.

## Key Limitations

### 1. Joint Consensus Not Implemented
- **Impact**: Configuration changes are not as safe as they could be
- **Details**: The Raft paper describes a two-phase configuration change process using joint consensus to ensure safety during membership changes. This implementation uses a simpler single-phase approach.
- **Workaround**: Use `AddServerSafely` to add servers as non-voting first, then promote them after they catch up

### 2. Leaders Don't Step Down When Partitioned
- **Impact**: A partitioned leader continues to believe it's the leader even without majority
- **Details**: When a leader is partitioned from the majority of the cluster, it should step down to follower state. Currently, it remains in leader state but cannot commit new entries.
- **Tests Affected**: `TestComplexMultiPartition` had to be adjusted to handle this limitation

### 3. SafeConfigurationManager State Not Replicated
- **Impact**: Automatic promotion of non-voting servers fails after leader changes
- **Details**: The SafeConfigurationManager tracks which non-voting servers should be promoted, but this state is local to each node. When leadership changes, the new leader doesn't know about pending promotions.
- **Workaround**: Manually promote non-voting servers or ensure leadership stability during server additions

### 4. Replication Performance Under Extreme Load
- **Impact**: Under extreme load, some nodes may fall significantly behind
- **Details**: The current implementation blocks new replication RPCs while one is in flight. This can cause large divergences in commit indices under heavy load.
- **Potential Fix**: Implement pipelining or allow multiple in-flight RPCs

## Test Suite Issues

### 1. TestBasicMembershipChange Flakiness
- **Pass Rate**: ~70% (7/10 in last run)
- **Cause**: Cluster instability during rapid configuration changes leads to frequent elections
- **Details**: Multiple configuration changes in quick succession can trigger leader elections, causing some nodes to miss log entries

### 2. Timing-Sensitive Tests
- **Affected Tests**: Many tests rely on sleep statements for synchronization
- **Issue**: Tests may fail on slower systems or under high load
- **Potential Improvement**: Implement proper synchronization primitives or polling with timeouts

## Potential Improvements

### 1. Implement Joint Consensus
- Would make configuration changes safer
- Prevent split-brain scenarios during membership changes
- Allow atomic replacement of multiple servers

### 2. Add Pre-Vote Phase
- Reduce disruption from partitioned nodes
- Prevent unnecessary elections when a node rejoins
- Improve cluster stability

### 3. Leader Lease
- Implement leader lease to prevent two leaders in the same term
- Improve read performance with local reads on leader

### 4. Automatic Stepping Down
- Make leaders step down when they detect they've lost quorum
- Faster recovery from network partitions

### 5. Replicated SafeConfigurationManager State
- Store promotion tracking in the replicated log
- Ensure promotions continue after leader changes
- Make configuration changes more robust

### 6. Improved Replication Pipeline
- Allow multiple in-flight AppendEntries RPCs
- Better performance under load
- Reduce replication lag

## Security Considerations

### 1. No Authentication
- RPCs are not authenticated
- Any node can join the cluster if it knows the node IDs

### 2. No Encryption
- All communication is in plaintext
- Not suitable for untrusted networks

### 3. No Authorization
- No access control for operations
- All nodes have equal privileges

## Documentation Needs

### 1. Configuration Best Practices
- Document safe procedures for adding/removing nodes
- Explain when to use AddServer vs AddServerSafely
- Provide examples of common configuration scenarios

### 2. Operational Guide
- How to handle various failure scenarios
- Monitoring and alerting recommendations
- Capacity planning guidelines

### 3. API Documentation
- Complete documentation of all public interfaces
- Example usage for common operations
- Error handling guidelines