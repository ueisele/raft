# Safe Server Addition Guide

This guide explains how to safely add new servers to a Raft cluster without compromising availability or consistency.

## The Problem: Immediate Voting Rights

When a new server with an empty log is added as a voting member immediately, it can cause serious issues:

1. **Availability Loss**: The new server cannot acknowledge log entries it doesn't have, potentially preventing the cluster from committing new entries.

2. **Election Disruption**: An empty-log server can vote in elections, potentially electing a leader with an incomplete log.

3. **Quorum Calculation**: Adding multiple voting servers at once can make it impossible to achieve majority.

## The Solution: Safe Server Addition

This implementation provides `AddServerSafely()` which:
1. Always adds new servers as **non-voting** members first
2. Monitors their catch-up progress automatically
3. Promotes to voting member only after catching up

## Usage

### Safe Method (Recommended)

```go
// Add a new server safely - it will be non-voting initially
err := node.AddServerSafely(newServerID, "server-4:8004")
if err != nil {
    log.Fatal(err)
}

// Monitor progress
for {
    progress := node.GetServerProgress(newServerID)
    if progress != nil {
        fmt.Printf("Server %d progress: %.1f%% (index %d/%d)\n",
            newServerID,
            progress.CatchUpProgress() * 100,
            progress.CurrentIndex,
            progress.TargetLogIndex)
    }
    time.Sleep(1 * time.Second)
}
```

### Unsafe Method (Not Recommended)

```go
// WARNING: This adds voting member immediately - dangerous!
err := node.AddServer(newServerID, "server-4:8004", true)
```

## Configuration Options

When creating a node, you can configure the safe addition behavior:

```go
config := &raft.Config{
    // ... other config ...
    
    // Safe configuration options
    SafeConfigOptions: &raft.SafeConfigOptions{
        PromotionThreshold:   0.95,        // Promote when 95% caught up
        PromotionCheckPeriod: time.Second, // Check every second
        MinCatchUpEntries:    10,          // Minimum 10 entries before promotion
    },
}
```

## Metrics and Monitoring

The safe configuration manager provides metrics:

```go
// Get metrics summary
metrics := node.GetConfigMetrics()
fmt.Println(metrics.String())

// Output:
// Configuration Metrics:
//   Servers Added: 2
//   Servers Promoted: 1
//   Configuration Errors: 0
// Server Details:
//   Server 3:
//     Added: 2024-01-20T10:00:00Z
//     Promoted: 2024-01-20T10:00:45Z (after 45s)
//   Server 4:
//     Added: 2024-01-20T10:01:00Z
//     Progress: 78.5%
```

## Best Practices

### DO:
- Always use `AddServerSafely()` for production clusters
- Monitor server progress before manual operations
- Add servers one at a time when possible
- Ensure new servers have good network connectivity
- Wait for previous additions to complete before adding more

### DON'T:
- Never use `AddServer(..., true)` unless you understand the risks
- Don't add multiple servers simultaneously
- Don't add servers during network partitions
- Don't remove servers that haven't finished catching up

## Examples

### Adding Multiple Servers Safely

```go
servers := []struct{
    id   int
    addr string
}{
    {4, "server-4:8004"},
    {5, "server-5:8005"},
    {6, "server-6:8006"},
}

for _, server := range servers {
    // Add server
    if err := node.AddServerSafely(server.id, server.addr); err != nil {
        log.Printf("Failed to add server %d: %v", server.id, err)
        continue
    }
    
    // Wait for catch-up
    for {
        progress := node.GetServerProgress(server.id)
        if progress == nil || progress.PromotionPending {
            break // Promoted or error
        }
        time.Sleep(5 * time.Second)
    }
    
    log.Printf("Server %d successfully added and promoted", server.id)
}
```

### Handling Failures

```go
err := node.AddServerSafely(newServerID, address)
if err != nil {
    switch {
    case strings.Contains(err.Error(), "already exists"):
        // Server already in configuration
        log.Printf("Server %d already exists", newServerID)
    case strings.Contains(err.Error(), "only leader"):
        // Not the leader, find leader and retry
        leaderID := node.GetLeader()
        log.Printf("Not leader, current leader is %d", leaderID)
    default:
        // Other error
        log.Printf("Failed to add server: %v", err)
    }
}
```

## Technical Details

### Catch-Up Progress Calculation

Progress is calculated as:
```
progress = matchIndex / lastLogIndex
```

A server is considered caught up when:
1. Progress >= PromotionThreshold (default 95%)
2. matchIndex >= commitIndex (caught up to committed entries)
3. At least MinCatchUpEntries have been replicated

### Automatic Promotion

The promotion process:
1. Non-voting server receives log entries via AppendEntries
2. Progress monitor checks every PromotionCheckPeriod
3. When criteria are met, server is promoted to voting
4. Metrics are updated with promotion time

### Safety Guarantees

This approach ensures:
- Cluster never loses availability due to empty-log servers
- Quorum calculations remain valid during addition
- No disruption to ongoing operations
- Gradual, monitored transition to full membership

## Troubleshooting

### Server Not Catching Up

Check:
1. Network connectivity between leader and new server
2. New server's resources (CPU, disk I/O)
3. Rate of new entries being added to log
4. Error logs on both leader and new server

### Promotion Not Happening

Verify:
1. Server has caught up to commit index
2. Progress is above threshold (95%)
3. Minimum entries requirement is met
4. No configuration changes in progress

### Multiple Servers Stuck

If adding multiple servers:
1. Check if you're hitting quorum issues
2. Ensure previous additions completed
3. Consider removing unsuccessful additions
4. Add servers one at a time

## Summary

Safe server addition is critical for maintaining Raft cluster availability. Always use `AddServerSafely()` and monitor progress before assuming a server is fully integrated into the cluster.