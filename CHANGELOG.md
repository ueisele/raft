# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- Comprehensive test synchronization helpers to replace timing-sensitive sleep calls
- Unit tests for HTTP transport implementation
- Integration tests for HTTP transport covering cluster communication
- Architecture documentation describing internal components and data flow
- Consolidated roadmap and limitations documentation

### Changed
- Reorganized test structure with unit tests in root and integration tests in integration/ directory
- Unified Transport interfaces to use single interface from main package
- Consolidated documentation to eliminate redundancy between README and IMPLEMENTATION
- Improved test reliability by replacing ~350 time.Sleep calls with proper synchronization

### Fixed
- **Vote Denial**: Added check to deny votes if heard from leader within election timeout
- **Non-Voting Members**: New servers added as non-voting first, then promoted after catch-up
- **Bounds Checking**: Fixed index out of bounds during configuration changes
- **Leader Step-Down**: Leaders step down when removed from configuration
- **Test Reliability**: Fixed 12 failing tests across different test suites
- **Import Cycles**: Resolved import cycle issues in test helpers

### Removed
- Obsolete backup directories containing ~15,000 lines of outdated code
- Redundant markdown documentation files (reduced from 28 to 9)
- Duplicate Transport interface definitions
- Redundant sections between README.md and IMPLEMENTATION.md

## [1.0.0] - Initial Release

### Core Features
- **Leader Election**: Randomized timeouts with majority voting
- **Log Replication**: Strong leader-based replication with consistency guarantees
- **Safety Properties**: Implementation of all five Raft safety properties
- **Persistence**: Durable JSON-based storage of state
- **Log Compaction**: Snapshotting mechanism for log size management
- **Safe Server Addition**: Non-voting member support with automatic promotion
- **Client Interaction**: HTTP-based RPC interface with linearizable semantics

### Known Limitations
- Joint consensus not implemented (safe server addition used instead)
- No automatic configuration rollback
- Single-node configuration changes only
- See [ROADMAP_AND_LIMITATIONS.md](ROADMAP_AND_LIMITATIONS.md) for complete list