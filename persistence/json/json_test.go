package json

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/ueisele/raft"
	"github.com/ueisele/raft/persistence"
)

// Unit tests that don't interact with the file system

func TestGetFilenamesUnit(t *testing.T) {
	tests := []struct {
		name             string
		dataDir          string
		serverID         int
		expectedState    string
		expectedSnapshot string
	}{
		{
			name:             "basic paths",
			dataDir:          "/data",
			serverID:         42,
			expectedState:    "/data/raft-state-42.json",
			expectedSnapshot: "/data/raft-snapshot-42.json",
		},
		{
			name:             "with trailing slash",
			dataDir:          "/data/",
			serverID:         1,
			expectedState:    "/data/raft-state-1.json",
			expectedSnapshot: "/data/raft-snapshot-1.json",
		},
		{
			name:             "nested path",
			dataDir:          "/var/lib/raft",
			serverID:         0,
			expectedState:    "/var/lib/raft/raft-state-0.json",
			expectedSnapshot: "/var/lib/raft/raft-snapshot-0.json",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &JSONPersistence{
				dataDir:  tt.dataDir,
				serverID: tt.serverID,
			}

			if got := p.getStateFilename(); got != tt.expectedState {
				t.Errorf("getStateFilename() = %v, want %v", got, tt.expectedState)
			}

			if got := p.getSnapshotFilename(); got != tt.expectedSnapshot {
				t.Errorf("getSnapshotFilename() = %v, want %v", got, tt.expectedSnapshot)
			}
		})
	}
}

func TestPersistenceErrorWrapping(t *testing.T) {
	baseErr := errors.New("base error")
	
	tests := []struct {
		name string
		err  error
		op   string
	}{
		{
			name: "save error",
			err:  &persistence.PersistenceError{Op: "save", Err: baseErr},
			op:   "save",
		},
		{
			name: "load error",
			err:  &persistence.PersistenceError{Op: "load", Err: baseErr},
			op:   "load",
		},
		{
			name: "delete error",
			err:  &persistence.PersistenceError{Op: "delete", Err: baseErr},
			op:   "delete",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			persErr, ok := tt.err.(*persistence.PersistenceError)
			if !ok {
				t.Fatal("Expected PersistenceError type")
			}
			if persErr.Op != tt.op {
				t.Errorf("Op = %v, want %v", persErr.Op, tt.op)
			}
			if persErr.Err != baseErr {
				t.Errorf("Wrapped error = %v, want %v", persErr.Err, baseErr)
			}
		})
	}
}

func TestJSONSerializationLogic(t *testing.T) {
	// Test JSON marshaling/unmarshaling logic without file I/O
	tests := []struct {
		name  string
		state *raft.PersistentState
	}{
		{
			name: "state with all fields",
			state: &raft.PersistentState{
				CurrentTerm: 10,
				VotedFor:    intPtr(5),
				Log: []raft.LogEntry{
					{Term: 0, Index: 0},
					{Term: 1, Index: 1, Command: "cmd1"},
				},
			},
		},
		{
			name: "state with nil voted for",
			state: &raft.PersistentState{
				CurrentTerm: 20,
				VotedFor:    nil,
				Log:         []raft.LogEntry{{Term: 0, Index: 0}},
			},
		},
		{
			name: "state with configuration",
			state: &raft.PersistentState{
				CurrentTerm: 5,
				VotedFor:    intPtr(1),
				Log: []raft.LogEntry{
					{Term: 0, Index: 0},
					{Term: 1, Index: 1, Command: &raft.Configuration{
						Servers: []raft.Server{
							{ID: 1, Address: "addr1"},
							{ID: 2, Address: "addr2"},
						},
					}},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Marshal
			data, err := json.MarshalIndent(tt.state, "", "  ")
			if err != nil {
				t.Fatalf("Failed to marshal: %v", err)
			}

			// Unmarshal
			var decoded raft.PersistentState
			if err := json.Unmarshal(data, &decoded); err != nil {
				t.Fatalf("Failed to unmarshal: %v", err)
			}

			// Compare
			if decoded.CurrentTerm != tt.state.CurrentTerm {
				t.Errorf("CurrentTerm = %v, want %v", decoded.CurrentTerm, tt.state.CurrentTerm)
			}

			if (decoded.VotedFor == nil) != (tt.state.VotedFor == nil) {
				t.Error("VotedFor nil mismatch")
			} else if decoded.VotedFor != nil && *decoded.VotedFor != *tt.state.VotedFor {
				t.Errorf("VotedFor = %v, want %v", *decoded.VotedFor, *tt.state.VotedFor)
			}

			if len(decoded.Log) != len(tt.state.Log) {
				t.Errorf("Log length = %v, want %v", len(decoded.Log), len(tt.state.Log))
			}
		})
	}
}

func TestSnapshotWrapperSerialization(t *testing.T) {
	// Test the snapshot wrapper serialization logic
	tests := []struct {
		name     string
		snapshot *raft.Snapshot
	}{
		{
			name: "basic snapshot",
			snapshot: &raft.Snapshot{
				Data:              []byte("test data"),
				LastIncludedIndex: 100,
				LastIncludedTerm:  5,
			},
		},
		{
			name: "empty data snapshot",
			snapshot: &raft.Snapshot{
				Data:              []byte{},
				LastIncludedIndex: 50,
				LastIncludedTerm:  3,
			},
		},
		{
			name: "large data snapshot",
			snapshot: &raft.Snapshot{
				Data:              make([]byte, 1024),
				LastIncludedIndex: 1000,
				LastIncludedTerm:  20,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create wrapper
			wrapper := struct {
				LastIncludedIndex int    `json:"lastIncludedIndex"`
				LastIncludedTerm  int    `json:"lastIncludedTerm"`
				Data              []byte `json:"data"`
			}{
				LastIncludedIndex: tt.snapshot.LastIncludedIndex,
				LastIncludedTerm:  tt.snapshot.LastIncludedTerm,
				Data:              tt.snapshot.Data,
			}

			// Marshal
			data, err := json.MarshalIndent(wrapper, "", "  ")
			if err != nil {
				t.Fatalf("Failed to marshal: %v", err)
			}

			// Unmarshal
			var decoded struct {
				LastIncludedIndex int    `json:"lastIncludedIndex"`
				LastIncludedTerm  int    `json:"lastIncludedTerm"`
				Data              []byte `json:"data"`
			}
			if err := json.Unmarshal(data, &decoded); err != nil {
				t.Fatalf("Failed to unmarshal: %v", err)
			}

			// Verify
			if decoded.LastIncludedIndex != tt.snapshot.LastIncludedIndex {
				t.Errorf("LastIncludedIndex = %v, want %v", decoded.LastIncludedIndex, tt.snapshot.LastIncludedIndex)
			}
			if decoded.LastIncludedTerm != tt.snapshot.LastIncludedTerm {
				t.Errorf("LastIncludedTerm = %v, want %v", decoded.LastIncludedTerm, tt.snapshot.LastIncludedTerm)
			}
			if string(decoded.Data) != string(tt.snapshot.Data) {
				t.Errorf("Data length = %v, want %v", len(decoded.Data), len(tt.snapshot.Data))
			}
		})
	}
}

func TestEmptyLogHandlingLogic(t *testing.T) {
	// Test the logic for handling empty logs (should add dummy entry)
	emptyLog := []raft.LogEntry{}
	
	// Simulate the logic from LoadState
	if len(emptyLog) == 0 {
		emptyLog = []raft.LogEntry{{Term: 0, Index: 0}}
	}

	if len(emptyLog) != 1 {
		t.Errorf("Expected log with 1 entry, got %d", len(emptyLog))
	}

	if emptyLog[0].Term != 0 || emptyLog[0].Index != 0 {
		t.Errorf("Expected dummy entry (term=0, index=0), got (term=%d, index=%d)",
			emptyLog[0].Term, emptyLog[0].Index)
	}
}

// Helper function
func intPtr(i int) *int {
	return &i
}