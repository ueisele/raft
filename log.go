package raft

import (
	"fmt"
	"sync"
)

// LogManager handles log operations
// Implements the log structure described in Section 5.3 of the Raft paper
type LogManager struct {
	mu sync.RWMutex

	// The log entries
	// Index 0 is a dummy entry for easier arithmetic
	entries []LogEntry

	// Snapshot state
	lastSnapshotIndex int
	lastSnapshotTerm  int

	// Commit and apply indices
	commitIndex int
	lastApplied int
}

// NewLogManager creates a new log manager
func NewLogManager() *LogManager {
	return &LogManager{
		// Initialize with dummy entry at index 0
		entries:           []LogEntry{{Term: 0, Index: 0}},
		commitIndex:       0,
		lastApplied:       0,
		lastSnapshotIndex: 0,
		lastSnapshotTerm:  0,
	}
}

// AppendEntry appends a new entry to the log
func (lm *LogManager) AppendEntry(term int, command interface{}) int {
	lm.mu.Lock()
	defer lm.mu.Unlock()

	index := lm.getLastIndexLocked() + 1
	entry := LogEntry{
		Term:    term,
		Index:   index,
		Command: command,
	}

	lm.entries = append(lm.entries, entry)
	return index
}

// AppendEntries appends multiple entries to the log
// Used by followers when receiving AppendEntries RPC
func (lm *LogManager) AppendEntries(prevIndex, prevTerm int, entries []LogEntry) error {
	lm.mu.Lock()
	defer lm.mu.Unlock()

	// Check if we have the previous entry
	if prevIndex > 0 {
		if prevIndex <= lm.lastSnapshotIndex {
			if prevIndex < lm.lastSnapshotIndex || prevTerm != lm.lastSnapshotTerm {
				return fmt.Errorf("log inconsistency: prevIndex in snapshot")
			}
		} else {
			prevEntry := lm.getEntryLocked(prevIndex)
			if prevEntry == nil || prevEntry.Term != prevTerm {
				return fmt.Errorf("log inconsistency at index %d", prevIndex)
			}
		}
	}

	// Find conflict point and truncate if necessary
	for i, entry := range entries {
		index := prevIndex + 1 + i
		existingEntry := lm.getEntryLocked(index)

		if existingEntry != nil {
			if existingEntry.Term != entry.Term {
				// Delete the existing entry and all that follow it
				lm.truncateAfterLocked(index - 1)
				break
			}
			// Entry already exists and matches, skip it
			continue
		}

		// Append remaining entries
		lm.entries = append(lm.entries, entries[i:]...)
		break
	}

	return nil
}

// GetEntry returns the log entry at the given index
func (lm *LogManager) GetEntry(index int) *LogEntry {
	lm.mu.RLock()
	defer lm.mu.RUnlock()
	return lm.getEntryLocked(index)
}

// GetEntries returns log entries in the range [start, end)
func (lm *LogManager) GetEntries(start, end int) []LogEntry {
	lm.mu.RLock()
	defer lm.mu.RUnlock()

	if start <= lm.lastSnapshotIndex {
		start = lm.lastSnapshotIndex + 1
	}

	var entries []LogEntry
	for _, entry := range lm.entries {
		if entry.Index >= start && entry.Index < end {
			entries = append(entries, entry)
		}
	}

	return entries
}

// GetAllEntries returns all log entries
func (lm *LogManager) GetAllEntries() []LogEntry {
	lm.mu.RLock()
	defer lm.mu.RUnlock()

	result := make([]LogEntry, len(lm.entries))
	copy(result, lm.entries)
	return result
}

// GetLastIndex returns the index of the last log entry
func (lm *LogManager) GetLastIndex() int {
	lm.mu.RLock()
	defer lm.mu.RUnlock()
	return lm.getLastIndexLocked()
}

// GetLastLogIndex is an alias for GetLastIndex for compatibility
func (lm *LogManager) GetLastLogIndex() int {
	return lm.GetLastIndex()
}

// GetLastTerm returns the term of the last log entry
func (lm *LogManager) GetLastTerm() int {
	lm.mu.RLock()
	defer lm.mu.RUnlock()
	return lm.getLastTermLocked()
}

// GetLastLogTerm is an alias for GetLastTerm for compatibility
func (lm *LogManager) GetLastLogTerm() int {
	return lm.GetLastTerm()
}

// GetCommitIndex returns the current commit index
func (lm *LogManager) GetCommitIndex() int {
	lm.mu.RLock()
	defer lm.mu.RUnlock()
	return lm.commitIndex
}

// SetCommitIndex updates the commit index
func (lm *LogManager) SetCommitIndex(index int) {
	lm.mu.Lock()
	defer lm.mu.Unlock()

	if index > lm.commitIndex {
		// Cap at last log index
		lastIndex := lm.getLastIndexLocked()
		if index > lastIndex {
			index = lastIndex
		}
		lm.commitIndex = index
	}
}

// GetLastApplied returns the last applied index
func (lm *LogManager) GetLastApplied() int {
	lm.mu.RLock()
	defer lm.mu.RUnlock()
	return lm.lastApplied
}

// SetLastApplied updates the last applied index
func (lm *LogManager) SetLastApplied(index int) {
	lm.mu.Lock()
	defer lm.mu.Unlock()
	lm.lastApplied = index
}

// GetUncommittedEntries returns entries that haven't been committed yet
func (lm *LogManager) GetUncommittedEntries() []LogEntry {
	lm.mu.RLock()
	defer lm.mu.RUnlock()

	var entries []LogEntry
	for _, entry := range lm.entries {
		if entry.Index > lm.commitIndex {
			entries = append(entries, entry)
		}
	}

	return entries
}

// GetUnappliedEntries returns committed entries that haven't been applied yet
func (lm *LogManager) GetUnappliedEntries() []LogEntry {
	lm.mu.RLock()
	defer lm.mu.RUnlock()

	var entries []LogEntry
	for _, entry := range lm.entries {
		if entry.Index > lm.lastApplied && entry.Index <= lm.commitIndex {
			entries = append(entries, entry)
		}
	}

	return entries
}

// IsUpToDate checks if the given last log index/term is at least as up-to-date as ours
// Implements the election restriction in Section 5.4.1
func (lm *LogManager) IsUpToDate(lastIndex, lastTerm int) bool {
	lm.mu.RLock()
	defer lm.mu.RUnlock()

	ourLastTerm := lm.getLastTermLocked()
	ourLastIndex := lm.getLastIndexLocked()

	// First check terms
	if lastTerm != ourLastTerm {
		return lastTerm > ourLastTerm
	}

	// Terms are equal, check indices
	return lastIndex >= ourLastIndex
}

// CreateSnapshot creates a snapshot up to the given index
func (lm *LogManager) CreateSnapshot(index, term int) error {
	lm.mu.Lock()
	defer lm.mu.Unlock()

	if index > lm.commitIndex {
		return fmt.Errorf("cannot snapshot beyond commit index")
	}

	if index <= lm.lastSnapshotIndex {
		return fmt.Errorf("cannot snapshot before last snapshot")
	}

	// Update snapshot state
	lm.lastSnapshotIndex = index
	lm.lastSnapshotTerm = term

	// Trim the log
	lm.trimLogLocked(index)

	return nil
}

// MatchEntry checks if the entry at the given index has the given term
func (lm *LogManager) MatchEntry(index, term int) bool {
	lm.mu.RLock()
	defer lm.mu.RUnlock()

	// Index 0 always matches
	if index == 0 {
		return term == 0
	}

	entry := lm.getEntryLocked(index)
	if entry == nil {
		return false
	}

	return entry.Term == term
}

// TruncateAfter removes all entries after the given index
func (lm *LogManager) TruncateAfter(index int) {
	lm.mu.Lock()
	defer lm.mu.Unlock()

	// Find the position in our entries array
	pos := -1
	for i, entry := range lm.entries {
		if entry.Index == index {
			pos = i
			break
		}
	}

	if pos >= 0 && pos < len(lm.entries)-1 {
		lm.entries = lm.entries[:pos+1]
	}
}

// InstallSnapshot installs a snapshot, discarding appropriate log entries
func (lm *LogManager) InstallSnapshot(lastIndex, lastTerm int) {
	lm.mu.Lock()
	defer lm.mu.Unlock()

	lm.lastSnapshotIndex = lastIndex
	lm.lastSnapshotTerm = lastTerm

	// Find if we have any entries after the snapshot
	var newEntries []LogEntry
	newEntries = append(newEntries, LogEntry{Term: 0, Index: 0}) // Dummy entry

	for _, entry := range lm.entries {
		if entry.Index > lastIndex {
			newEntries = append(newEntries, entry)
		}
	}

	lm.entries = newEntries

	// Update indices
	if lm.commitIndex < lastIndex {
		lm.commitIndex = lastIndex
	}
	if lm.lastApplied < lastIndex {
		lm.lastApplied = lastIndex
	}
}

// GetSnapshot returns the current snapshot state
func (lm *LogManager) GetSnapshot() (int, int) {
	lm.mu.RLock()
	defer lm.mu.RUnlock()
	return lm.lastSnapshotIndex, lm.lastSnapshotTerm
}

// RestoreFromPersistence restores the log from persistent state
func (lm *LogManager) RestoreFromPersistence(entries []LogEntry, lastSnapshotIndex, lastSnapshotTerm int) {
	lm.mu.Lock()
	defer lm.mu.Unlock()

	if len(entries) == 0 {
		lm.entries = []LogEntry{{Term: 0, Index: 0}}
	} else {
		lm.entries = entries
	}

	lm.lastSnapshotIndex = lastSnapshotIndex
	lm.lastSnapshotTerm = lastSnapshotTerm
}

// GetPersistentState returns the log entries for persistence
func (lm *LogManager) GetPersistentState() []LogEntry {
	lm.mu.RLock()
	defer lm.mu.RUnlock()

	// Return a copy to prevent external modification
	entries := make([]LogEntry, len(lm.entries))
	copy(entries, lm.entries)
	return entries
}

// Internal methods (assume lock is held)

func (lm *LogManager) getEntryLocked(index int) *LogEntry {
	if index <= lm.lastSnapshotIndex {
		return nil // Entry is in snapshot
	}

	for i := range lm.entries {
		if lm.entries[i].Index == index {
			return &lm.entries[i]
		}
	}

	return nil
}

func (lm *LogManager) getLastIndexLocked() int {
	if len(lm.entries) > 0 {
		lastEntry := lm.entries[len(lm.entries)-1]
		if lastEntry.Index > lm.lastSnapshotIndex {
			return lastEntry.Index
		}
	}
	return lm.lastSnapshotIndex
}

func (lm *LogManager) getLastTermLocked() int {
	if len(lm.entries) > 0 {
		lastEntry := lm.entries[len(lm.entries)-1]
		if lastEntry.Index > lm.lastSnapshotIndex {
			return lastEntry.Term
		}
	}
	return lm.lastSnapshotTerm
}

func (lm *LogManager) truncateAfterLocked(index int) {
	newEntries := []LogEntry{}
	for _, entry := range lm.entries {
		if entry.Index <= index {
			newEntries = append(newEntries, entry)
		}
	}
	lm.entries = newEntries
}

func (lm *LogManager) trimLogLocked(index int) {
	// Keep dummy entry and entries after the snapshot
	newEntries := []LogEntry{{Term: 0, Index: 0}}

	for _, entry := range lm.entries {
		if entry.Index > index {
			newEntries = append(newEntries, entry)
		}
	}

	lm.entries = newEntries
}
