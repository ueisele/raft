# Development Guidelines for Raft Repository

This document outlines the coding standards, patterns, and conventions used in this Raft implementation to ensure consistency and maintainability.

## Code Style Guidelines

### Naming Conventions

- **Packages**: Lowercase, single words (`raft`, `http`, `json`)
- **Types**: PascalCase (`LogManager`, `StateManager`, `HTTPTransport`)
- **Interfaces**: PascalCase, descriptive nouns (`StateMachine`, `Transport`, `RPCHandler`)
- **Functions/Methods**: 
  - Exported: PascalCase (`NewNode`, `Submit`, `GetState`)
  - Unexported: camelCase (`applyLogEntry`, `sendHeartbeats`)
- **Method receivers**: Single lowercase letter (`n` for node, `t` for transport)
- **Constants**: PascalCase for type constants (`Follower`, `Candidate`, `Leader`)
- **Variables**: camelCase (`currentTerm`, `commitIndex`)

### File Organization

```
raft/
├── core_functionality.go    # One file per major feature
├── core_functionality_test.go
├── transport/               # Sub-packages for abstractions
│   ├── types.go
│   ├── discovery.go
│   └── http/
│       ├── http.go
│       └── http_test.go
├── persistence/
│   └── json/
├── integration/             # Integration tests separate from unit tests
│   ├── feature/
│   └── helpers/
└── example/                 # Runnable examples
```

### Comments and Documentation

- **Type comments**: Start with type name
  ```go
  // LogManager handles all log-related operations for the Raft node
  type LogManager struct {
  ```

- **Method comments**: Start with method name, describe behavior
  ```go
  // AppendEntries processes append entries RPC from leader
  func (n *raftNode) AppendEntries(args *AppendEntriesArgs, reply *AppendEntriesReply) error {
  ```

- **Implementation comments**: Reference Raft paper sections
  ```go
  // Following Figure 2 in the Raft paper
  // If RPC request or response contains term T > currentTerm...
  ```

## Error Handling

### Error Patterns

```go
// Always return error as last value
func (t *HTTPTransport) Start() error {
    if t.handler == nil {
        return fmt.Errorf("RPC handler not set")
    }
    // ...
}

// Use error wrapping with context
if err != nil {
    return fmt.Errorf("failed to get address for server %d: %w", serverID, err)
}

// Custom error types in sub-packages
type TransportError struct {
    ServerID int
    Err      error
}
```

### Error Message Format
- Lowercase, no trailing punctuation
- Include context (IDs, values)
- Be consistent across similar operations

## Testing Conventions

### Test Organization

```go
// Unit tests: Same package, _test.go suffix
func TestLogManager_Append(t *testing.T) {
    // Test single unit of functionality
}

// Table-driven tests for multiple scenarios
func TestNodeState_Transitions(t *testing.T) {
    tests := []struct {
        name     string
        input    State
        expected State
    }{
        // test cases
    }
    
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            // test logic
        })
    }
}

// Integration tests: In integration/ directory
func TestCluster_LeaderElection(t *testing.T) {
    // Test interactions between components
}
```

### Test Helpers

```go
// Use descriptive helper functions
cluster := helpers.CreateCluster(t, 3)
defer cluster.Stop()

helpers.WaitForLeader(t, cluster, 5*time.Second)
helpers.AssertSameTerm(t, cluster)
```

## Concurrency Patterns

### Mutex Usage

```go
type raftNode struct {
    mu sync.RWMutex  // Always use RWMutex for read-heavy workloads
    // fields...
}

// Always defer unlock
func (n *raftNode) getCurrentTerm() int {
    n.mu.RLock()
    defer n.mu.RUnlock()
    return n.currentTerm
}

// Lock ordering: Always acquire in same order to prevent deadlocks
```

### Channel Patterns

```go
// Use buffered channels for async notifications
applyNotify := make(chan struct{}, 1)

// Non-blocking send for notifications
select {
case n.applyNotify <- struct{}{}:
default:
}
```

## Interface Design

### Interface Guidelines

```go
// Interfaces should be small and focused
type Transport interface {
    SendRequestVote(serverID int, args *RequestVoteArgs) (*RequestVoteReply, error)
    SendAppendEntries(serverID int, args *AppendEntriesArgs) (*AppendEntriesReply, error)
    SendInstallSnapshot(serverID int, args *InstallSnapshotArgs) (*InstallSnapshotReply, error)
    SetRPCHandler(handler RPCHandler)
    Start() error
    Stop() error
    GetAddress() string
}

// Return interfaces, accept concrete types
func NewNode(config *Config, transport Transport, persistence Persistence, stateMachine StateMachine) (Node, error) {
    // implementation returns *raftNode which implements Node
}
```

## Raft-Specific Patterns

### Term Management

```go
// Always check term first in RPCs
if args.Term > rf.currentTerm {
    rf.currentTerm = args.Term
    rf.votedFor = nil
    rf.state = Follower
}

// Include term in all responses
reply.Term = rf.currentTerm
```

### State Transitions

```go
// Clear state machine pattern
switch n.state {
case Follower:
    // follower logic
case Candidate:
    // candidate logic
case Leader:
    // leader logic
}
```

### Configuration Changes

```go
// Safe configuration changes
// 1. Add as non-voting member
// 2. Wait for catch-up
// 3. Promote to voting member
```

## Common Tasks

### Running Tests
```bash
# Run all tests with proper flags
go test -v -race -timeout 30s ./...

# Run specific test repeatedly for stability
go test -run TestName -count 10

# Run integration tests only
go test ./integration/... -v
```

### Adding New Features

1. Create feature file (e.g., `feature.go`)
2. Add corresponding test file (`feature_test.go`)
3. Add integration tests in `integration/`
4. Update documentation
5. Reference Raft paper sections in comments

## Important Constraints

1. **No Sleep in Tests**: Use synchronization helpers instead
2. **Paper Compliance**: Implementation follows the Raft paper strictly
3. **Backwards Compatibility**: Maintain interface compatibility
4. **Thread Safety**: All public methods must be thread-safe
5. **Error Context**: Always provide context in error messages

## Commands to Run

When making changes:
```bash
# IMPORTANT: Always ensure code compiles before committing
go build ./...

# Format code
go fmt ./...

# Run tests
go test -race ./...

# Check for common issues
go vet ./...

# Run linter if available
golangci-lint run
```

### Compilation Checks

**CRITICAL**: Always ensure all code compiles before committing changes. This includes:
- Main packages (`go build ./...`)
- Test files (`go test -c ./...`)
- Example code (`go build ./example/...`)

Never leave the codebase in a state where it doesn't compile.

## References

- Raft Paper: "In Search of an Understandable Consensus Algorithm" by Diego Ongaro and John Ousterhout
- Implementation follows Extended Raft paper sections
- Comments reference specific Figure 2 rules and section numbers