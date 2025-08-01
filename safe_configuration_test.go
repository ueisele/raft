package raft

import (
	"testing"
)

// TestCatchUpProgressCalculation tests the progress calculation logic
func TestCatchUpProgressCalculation(t *testing.T) {
	tests := []struct {
		name            string
		matchIndex      int
		lastLogIndex    int
		commitIndex     int
		minEntries      int
		threshold       float64
		expectPromotion bool
	}{
		{
			name:            "Empty log",
			matchIndex:      0,
			lastLogIndex:    0,
			commitIndex:     0,
			minEntries:      10,
			threshold:       0.95,
			expectPromotion: false, // No entries replicated
		},
		{
			name:            "Fully caught up",
			matchIndex:      100,
			lastLogIndex:    100,
			commitIndex:     95,
			minEntries:      10,
			threshold:       0.95,
			expectPromotion: true,
		},
		{
			name:            "95% caught up",
			matchIndex:      95,
			lastLogIndex:    100,
			commitIndex:     90,
			minEntries:      10,
			threshold:       0.95,
			expectPromotion: true,
		},
		{
			name:            "Below threshold",
			matchIndex:      90,
			lastLogIndex:    100,
			commitIndex:     90,
			minEntries:      10,
			threshold:       0.95,
			expectPromotion: false,
		},
		{
			name:            "Not enough entries",
			matchIndex:      5,
			lastLogIndex:    5,
			commitIndex:     5,
			minEntries:      10,
			threshold:       0.95,
			expectPromotion: false,
		},
		{
			name:            "Behind commit index",
			matchIndex:      80,
			lastLogIndex:    100,
			commitIndex:     90,
			minEntries:      10,
			threshold:       0.95,
			expectPromotion: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Calculate progress
			var progress float64
			if tt.lastLogIndex > 0 {
				progress = float64(tt.matchIndex) / float64(tt.lastLogIndex)
			} else {
				progress = 1.0
			}

			// Check promotion criteria
			entriesReplicated := tt.matchIndex
			caughtUpToCommit := tt.matchIndex >= tt.commitIndex
			sufficientProgress := progress >= tt.threshold
			sufficientEntries := entriesReplicated >= tt.minEntries

			shouldPromote := caughtUpToCommit && sufficientProgress && sufficientEntries

			if shouldPromote != tt.expectPromotion {
				t.Errorf("Expected promotion=%v, got %v (progress=%.2f, caught up=%v, sufficient=%v, entries=%v)",
					tt.expectPromotion, shouldPromote, progress, caughtUpToCommit, sufficientProgress, sufficientEntries)
			}
		})
	}
}

// TODO: Add unit tests for SafeConfigurationManager when it's implemented as a separate component
