package persistence

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/ueisele/raft"
	"github.com/ueisele/raft/persistence"
	jsonPersistence "github.com/ueisele/raft/persistence/json"
)

func TestNewJSONPersistence(t *testing.T) {
	tests := []struct {
		name    string
		config  *persistence.Config
		setup   func(string) error
		wantErr bool
	}{
		{
			name: "successful creation",
			config: &persistence.Config{
				DataDir:  filepath.Join(t.TempDir(), "data"),
				ServerID: 1,
			},
			wantErr: false,
		},
		{
			name: "directory already exists",
			config: &persistence.Config{
				DataDir:  t.TempDir(),
				ServerID: 2,
			},
			wantErr: false,
		},
		{
			name: "invalid directory path",
			config: &persistence.Config{
				DataDir:  "/root/cannot-create-here",
				ServerID: 3,
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.setup != nil {
				if err := tt.setup(tt.config.DataDir); err != nil {
					t.Fatal(err)
				}
			}

			p, err := jsonPersistence.NewJSONPersistence(tt.config)
			if (err != nil) != tt.wantErr {
				t.Errorf("NewJSONPersistence() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr {
				if p == nil {
					t.Error("Expected non-nil persistence")
				}
				// Can't access private fields from outside package
				// The fact that NewJSONPersistence succeeded is enough
			}
		})
	}
}

func TestSaveAndLoadState(t *testing.T) {
	tempDir := t.TempDir()
	p, err := jsonPersistence.NewJSONPersistence(&persistence.Config{
		DataDir:  tempDir,
		ServerID: 1,
	})
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name  string
		state *raft.PersistentState
	}{
		{
			name: "basic state",
			state: &raft.PersistentState{
				CurrentTerm: 5,
				VotedFor:    intPtr(2),
				Log: []raft.LogEntry{
					{Term: 0, Index: 0},
					{Term: 1, Index: 1, Command: "cmd1"},
					{Term: 2, Index: 2, Command: "cmd2"},
				},
			},
		},
		{
			name: "state with nil voted for",
			state: &raft.PersistentState{
				CurrentTerm: 10,
				VotedFor:    nil,
				Log: []raft.LogEntry{
					{Term: 0, Index: 0},
				},
			},
		},
		{
			name: "state with configuration entry",
			state: &raft.PersistentState{
				CurrentTerm: 3,
				VotedFor:    intPtr(1),
				Log: []raft.LogEntry{
					{Term: 0, Index: 0},
					{Term: 1, Index: 1, Command: &raft.Configuration{
						Servers: []raft.Server{
							{ID: 1, Address: "server1", NonVoting: false},
							{ID: 2, Address: "server2", NonVoting: false},
						},
					}},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Save state
			if err := p.SaveState(tt.state); err != nil {
				t.Fatalf("SaveState() error = %v", err)
			}

			// Verify file was created
			stateFile := filepath.Join(tempDir, "raft-state-1.json")
			if _, err := os.Stat(stateFile); os.IsNotExist(err) {
				t.Error("State file was not created")
			}

			// Load state
			loaded, err := p.LoadState()
			if err != nil {
				t.Fatalf("LoadState() error = %v", err)
			}

			// Compare states
			if loaded.CurrentTerm != tt.state.CurrentTerm {
				t.Errorf("CurrentTerm = %v, want %v", loaded.CurrentTerm, tt.state.CurrentTerm)
			}

			if (loaded.VotedFor == nil) != (tt.state.VotedFor == nil) {
				t.Errorf("VotedFor nil mismatch")
			} else if loaded.VotedFor != nil && *loaded.VotedFor != *tt.state.VotedFor {
				t.Errorf("VotedFor = %v, want %v", *loaded.VotedFor, *tt.state.VotedFor)
			}

			if len(loaded.Log) != len(tt.state.Log) {
				t.Errorf("Log length = %v, want %v", len(loaded.Log), len(tt.state.Log))
			}
		})
	}
}

func TestLoadStateNonExistent(t *testing.T) {
	tempDir := t.TempDir()
	p, err := jsonPersistence.NewJSONPersistence(&persistence.Config{
		DataDir:  tempDir,
		ServerID: 1,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Load non-existent state
	state, err := p.LoadState()
	if err != nil {
		t.Errorf("LoadState() should not error for non-existent file, got %v", err)
	}
	if state != nil {
		t.Error("LoadState() should return nil for non-existent file")
	}
}

func TestLoadStateCorruptedFile(t *testing.T) {
	tempDir := t.TempDir()
	p, err := jsonPersistence.NewJSONPersistence(&persistence.Config{
		DataDir:  tempDir,
		ServerID: 1,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Write corrupted JSON
	stateFile := filepath.Join(tempDir, "raft-state-1.json")
	if err := os.WriteFile(stateFile, []byte("invalid json"), 0644); err != nil {
		t.Fatal(err)
	}

	// Try to load
	_, err = p.LoadState()
	if err == nil {
		t.Error("LoadState() should error on corrupted file")
	}

	persErr, ok := err.(*persistence.PersistenceError)
	if !ok {
		t.Error("Expected PersistenceError type")
	} else if persErr.Op != "load" {
		t.Errorf("PersistenceError.Op = %v, want 'load'", persErr.Op)
	}
}

func TestSaveStateWriteError(t *testing.T) {
	// Create a directory where we can't write
	tempDir := t.TempDir()
	p, err := jsonPersistence.NewJSONPersistence(&persistence.Config{
		DataDir:  tempDir,
		ServerID: 1,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Make directory read-only
	if err := os.Chmod(tempDir, 0555); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(tempDir, 0755) //nolint:errcheck // restore permissions for cleanup

	state := &raft.PersistentState{
		CurrentTerm: 1,
		Log:         []raft.LogEntry{{Term: 0, Index: 0}},
	}

	err = p.SaveState(state)
	if err == nil {
		t.Error("SaveState() should error when directory is read-only")
	}

	persErr, ok := err.(*persistence.PersistenceError)
	if !ok {
		t.Error("Expected PersistenceError type")
	} else if persErr.Op != "save" {
		t.Errorf("PersistenceError.Op = %v, want 'save'", persErr.Op)
	}
}

func TestSaveAndLoadSnapshot(t *testing.T) {
	tempDir := t.TempDir()
	p, err := jsonPersistence.NewJSONPersistence(&persistence.Config{
		DataDir:  tempDir,
		ServerID: 1,
	})
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name     string
		snapshot *raft.Snapshot
	}{
		{
			name: "basic snapshot",
			snapshot: &raft.Snapshot{
				Data:              []byte("snapshot data"),
				LastIncludedIndex: 100,
				LastIncludedTerm:  5,
			},
		},
		{
			name: "empty snapshot data",
			snapshot: &raft.Snapshot{
				Data:              []byte{},
				LastIncludedIndex: 50,
				LastIncludedTerm:  3,
			},
		},
		{
			name: "large snapshot",
			snapshot: &raft.Snapshot{
				Data:              make([]byte, 1024*1024), // 1MB
				LastIncludedIndex: 1000,
				LastIncludedTerm:  20,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Save snapshot
			if err := p.SaveSnapshot(tt.snapshot); err != nil {
				t.Fatalf("SaveSnapshot() error = %v", err)
			}

			// Verify file was created
			snapshotFile := filepath.Join(tempDir, "raft-snapshot-1.json")
			if _, err := os.Stat(snapshotFile); os.IsNotExist(err) {
				t.Error("Snapshot file was not created")
			}

			// Load snapshot
			loaded, err := p.LoadSnapshot()
			if err != nil {
				t.Fatalf("LoadSnapshot() error = %v", err)
			}

			// Compare snapshots
			if loaded.LastIncludedIndex != tt.snapshot.LastIncludedIndex {
				t.Errorf("LastIncludedIndex = %v, want %v", loaded.LastIncludedIndex, tt.snapshot.LastIncludedIndex)
			}
			if loaded.LastIncludedTerm != tt.snapshot.LastIncludedTerm {
				t.Errorf("LastIncludedTerm = %v, want %v", loaded.LastIncludedTerm, tt.snapshot.LastIncludedTerm)
			}
			if string(loaded.Data) != string(tt.snapshot.Data) {
				t.Errorf("Data length = %v, want %v", len(loaded.Data), len(tt.snapshot.Data))
			}
		})
	}
}

func TestLoadSnapshotNonExistent(t *testing.T) {
	tempDir := t.TempDir()
	p, err := jsonPersistence.NewJSONPersistence(&persistence.Config{
		DataDir:  tempDir,
		ServerID: 1,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Load non-existent snapshot
	snapshot, err := p.LoadSnapshot()
	if err != nil {
		t.Errorf("LoadSnapshot() should not error for non-existent file, got %v", err)
	}
	if snapshot != nil {
		t.Error("LoadSnapshot() should return nil for non-existent file")
	}
}

func TestHasSnapshot(t *testing.T) {
	tempDir := t.TempDir()
	p, err := jsonPersistence.NewJSONPersistence(&persistence.Config{
		DataDir:  tempDir,
		ServerID: 1,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Check before creating snapshot
	if p.HasSnapshot() {
		t.Error("HasSnapshot() should return false when no snapshot exists")
	}

	// Create snapshot
	snapshot := &raft.Snapshot{
		Data:              []byte("test"),
		LastIncludedIndex: 10,
		LastIncludedTerm:  2,
	}
	if err := p.SaveSnapshot(snapshot); err != nil {
		t.Fatal(err)
	}

	// Check after creating snapshot
	if !p.HasSnapshot() {
		t.Error("HasSnapshot() should return true after saving snapshot")
	}
}

func TestDeleteSnapshot(t *testing.T) {
	tempDir := t.TempDir()
	p, err := jsonPersistence.NewJSONPersistence(&persistence.Config{
		DataDir:  tempDir,
		ServerID: 1,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Create snapshot
	snapshot := &raft.Snapshot{
		Data:              []byte("test"),
		LastIncludedIndex: 10,
		LastIncludedTerm:  2,
	}
	if err := p.SaveSnapshot(snapshot); err != nil {
		t.Fatal(err)
	}

	// Verify it exists
	if !p.HasSnapshot() {
		t.Error("Snapshot should exist after saving")
	}

	// Delete snapshot
	if err := p.DeleteSnapshot(); err != nil {
		t.Fatalf("DeleteSnapshot() error = %v", err)
	}

	// Verify it's gone
	if p.HasSnapshot() {
		t.Error("Snapshot should not exist after deletion")
	}

	// Delete again (should not error)
	if err := p.DeleteSnapshot(); err != nil {
		t.Errorf("DeleteSnapshot() on non-existent file should not error, got %v", err)
	}
}

func TestAtomicWrites(t *testing.T) {
	tempDir := t.TempDir()
	p, err := jsonPersistence.NewJSONPersistence(&persistence.Config{
		DataDir:  tempDir,
		ServerID: 1,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Save initial state
	state1 := &raft.PersistentState{
		CurrentTerm: 1,
		VotedFor:    intPtr(1),
		Log:         []raft.LogEntry{{Term: 0, Index: 0}},
	}
	if err := p.SaveState(state1); err != nil {
		t.Fatal(err)
	}

	// Verify no temp files remain
	files, err := os.ReadDir(tempDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if filepath.Ext(file.Name()) == ".tmp" {
			t.Errorf("Temporary file %s should not exist after save", file.Name())
		}
	}
}

func TestConcurrentAccess(t *testing.T) {
	tempDir := t.TempDir()
	p, err := jsonPersistence.NewJSONPersistence(&persistence.Config{
		DataDir:  tempDir,
		ServerID: 1,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Run concurrent saves and loads
	done := make(chan bool)
	errors := make(chan error, 10)

	// Writers
	for i := 0; i < 5; i++ {
		go func(term int) {
			state := &raft.PersistentState{
				CurrentTerm: term,
				VotedFor:    intPtr(term),
				Log:         []raft.LogEntry{{Term: 0, Index: 0}},
			}
			if err := p.SaveState(state); err != nil {
				errors <- err
			}
			done <- true
		}(i)
	}

	// Readers
	for i := 0; i < 5; i++ {
		go func() {
			if _, err := p.LoadState(); err != nil {
				errors <- err
			}
			done <- true
		}()
	}

	// Wait for all goroutines
	for i := 0; i < 10; i++ {
		<-done
	}

	// Check for errors
	select {
	case err := <-errors:
		t.Errorf("Concurrent access error: %v", err)
	default:
		// No errors
	}
}

func TestEmptyLogHandling(t *testing.T) {
	tempDir := t.TempDir()
	p, err := jsonPersistence.NewJSONPersistence(&persistence.Config{
		DataDir:  tempDir,
		ServerID: 1,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Save state with empty log
	state := &raft.PersistentState{
		CurrentTerm: 1,
		VotedFor:    nil,
		Log:         []raft.LogEntry{},
	}

	if err := p.SaveState(state); err != nil {
		t.Fatal(err)
	}

	// Load state - should add dummy entry
	loaded, err := p.LoadState()
	if err != nil {
		t.Fatal(err)
	}

	if len(loaded.Log) != 1 {
		t.Errorf("Expected log with dummy entry, got length %d", len(loaded.Log))
	}

	if loaded.Log[0].Term != 0 || loaded.Log[0].Index != 0 {
		t.Errorf("Expected dummy entry at index 0 term 0, got index %d term %d",
			loaded.Log[0].Index, loaded.Log[0].Term)
	}
}

func TestJSONFormatVerification(t *testing.T) {
	tempDir := t.TempDir()
	p, err := jsonPersistence.NewJSONPersistence(&persistence.Config{
		DataDir:  tempDir,
		ServerID: 1,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Save state
	state := &raft.PersistentState{
		CurrentTerm: 5,
		VotedFor:    intPtr(2),
		Log: []raft.LogEntry{
			{Term: 0, Index: 0},
			{Term: 1, Index: 1, Command: "test"},
		},
	}
	if err := p.SaveState(state); err != nil {
		t.Fatal(err)
	}

	// Read and verify JSON format
	stateFile := filepath.Join(tempDir, "raft-state-1.json")
	data, err := os.ReadFile(stateFile)
	if err != nil {
		t.Fatal(err)
	}

	// Verify it's valid JSON
	var parsed map[string]interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Errorf("State file is not valid JSON: %v", err)
	}

	// Verify it's indented (pretty-printed)
	if !contains(string(data), "\n  ") {
		t.Error("JSON should be pretty-printed with indentation")
	}
}

// Helper functions
func intPtr(i int) *int {
	return &i
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && containsAt(s, substr, 0)
}

func containsAt(s, substr string, start int) bool {
	for i := start; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// Benchmarks
func BenchmarkSaveState(b *testing.B) {
	tempDir := b.TempDir()
	p, err := jsonPersistence.NewJSONPersistence(&persistence.Config{
		DataDir:  tempDir,
		ServerID: 1,
	})
	if err != nil {
		b.Fatal(err)
	}

	state := &raft.PersistentState{
		CurrentTerm: 100,
		VotedFor:    intPtr(5),
		Log:         make([]raft.LogEntry, 100),
	}
	for i := range state.Log {
		state.Log[i] = raft.LogEntry{
			Term:    i / 10,
			Index:   i,
			Command: fmt.Sprintf("command-%d", i),
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := p.SaveState(state); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkLoadState(b *testing.B) {
	tempDir := b.TempDir()
	p, err := jsonPersistence.NewJSONPersistence(&persistence.Config{
		DataDir:  tempDir,
		ServerID: 1,
	})
	if err != nil {
		b.Fatal(err)
	}

	// Pre-save a state
	state := &raft.PersistentState{
		CurrentTerm: 100,
		VotedFor:    intPtr(5),
		Log:         make([]raft.LogEntry, 100),
	}
	for i := range state.Log {
		state.Log[i] = raft.LogEntry{
			Term:    i / 10,
			Index:   i,
			Command: fmt.Sprintf("command-%d", i),
		}
	}
	if err := p.SaveState(state); err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := p.LoadState(); err != nil {
			b.Fatal(err)
		}
	}
}
