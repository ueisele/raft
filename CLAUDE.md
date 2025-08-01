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

### Core Principles for Library Code

As a Raft library, we must never silently ignore errors that could affect correctness or data integrity. Users of this library depend on accurate error reporting to build reliable distributed systems.

### Error Categories

1. **Critical Errors** (MUST handle):
   - Persistence/state saving failures
   - Network transport errors during consensus operations
   - Snapshot creation/restoration failures
   - Log compaction errors
   - Any Close() errors on write operations

2. **Non-Critical Errors** (MAY log):
   - Read-only resource cleanup
   - Metrics collection failures
   - Debug/monitoring operations

### Error Patterns

#### Named Return Values for Defer Error Handling

```go
// For operations that modify state or persist data
func (p *Persistence) SaveState(state *PersistentState) (err error) {
    file, err := os.Create(p.statePath)
    if err != nil {
        return fmt.Errorf("create state file: %w", err)
    }
    defer func() {
        if cerr := file.Close(); cerr != nil && err == nil {
            err = fmt.Errorf("close state file: %w", cerr)
        }
    }()
    
    // Write operations...
    
    // Ensure durability before returning success
    return file.Sync()
}
```

#### Multi-Error Handling (Go 1.20+)

```go
func (t *Transport) Stop() error {
    var errs []error
    
    // Critical cleanup
    if t.listener != nil {
        if err := t.listener.Close(); err != nil {
            errs = append(errs, fmt.Errorf("close listener: %w", err))
        }
    }
    
    // Wait for pending operations
    t.wg.Wait()
    
    return errors.Join(errs...)
}
```

#### Read-Only Operations

```go
// Acceptable for read-only operations
func (n *Node) LoadDebugInfo() (*DebugInfo, error) {
    file, err := os.Open(n.debugPath)
    if err != nil {
        return nil, fmt.Errorf("open debug file: %w", err)
    }
    defer file.Close() // Read-only, Close() error not critical
    
    var info DebugInfo
    if err := json.NewDecoder(file).Decode(&info); err != nil {
        return nil, fmt.Errorf("decode debug info: %w", err)
    }
    return &info, nil
}
```

### Migration Plan for Defer Error Handling

#### Phase 1: Critical Path (Immediate)
Fix all defer statements in:
- `persistence/` - All write operations must handle Close() errors
- `transport/` - Network resources must be properly closed
- `snapshot_manager.go` - Snapshot operations affect correctness
- `node.go` - Core state management

#### Phase 2: Production Code (Short-term)
Update remaining production code:
- Log management functions
- Configuration persistence
- State machine interfaces

#### Phase 3: Test Code (Long-term)
For test files, use linter directives:
```go
defer os.RemoveAll(tempDir) //nolint:errcheck // test cleanup
defer transport.Stop() //nolint:errcheck // test cleanup
```

### Error Message Format
- Lowercase, no trailing punctuation
- Include operation context: `fmt.Errorf("save state: %w", err)`
- Be specific about what failed: `fmt.Errorf("close state file: %w", err)`
- Chain errors for context: `fmt.Errorf("persist term %d: %w", term, err)`

### Interface Documentation

```go
// Persistence defines the interface for persisting Raft state.
// 
// Implementations MUST ensure durability of all write operations.
// Any error returned from SaveState or SaveSnapshot indicates that
// the operation failed and the state was NOT persisted.
//
// Close() errors on write operations MUST be handled and returned.
type Persistence interface {
    SaveState(state *PersistentState) error
    LoadState() (*PersistentState, error)
    SaveSnapshot(snapshot *Snapshot) error
    LoadSnapshot() (*Snapshot, error)
}
```

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
go build -o /dev/null ./...

# Format code
go fmt ./...

# Run tests
go test -race ./...

# Check for common issues
go vet ./...

# Run linter if available
golangci-lint run ./...
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