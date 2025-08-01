package raft

import (
	"fmt"
	"sync"
	"time"
)

// SimpleConfigMetrics provides a simple in-memory implementation of ConfigMetrics
type SimpleConfigMetrics struct {
	mu sync.RWMutex

	// Counters
	serversAdded    int
	serversPromoted int
	configErrors    int

	// Server tracking
	serverMetrics map[int]*ServerMetric

	// Error tracking
	lastErrors []error
	maxErrors  int
}

// ServerMetric tracks metrics for a single server
type ServerMetric struct {
	ServerID          int
	AddedAt           time.Time
	PromotedAt        *time.Time
	PromotionDuration *time.Duration
	LastProgress      float64
	ProgressHistory   []ProgressPoint
}

// ProgressPoint represents a progress measurement at a point in time
type ProgressPoint struct {
	Time     time.Time
	Progress float64
}

// NewSimpleConfigMetrics creates a new simple metrics collector
func NewSimpleConfigMetrics() *SimpleConfigMetrics {
	return &SimpleConfigMetrics{
		serverMetrics: make(map[int]*ServerMetric),
		lastErrors:    make([]error, 0, 10),
		maxErrors:     10,
	}
}

// RecordServerAdded records a server being added
func (m *SimpleConfigMetrics) RecordServerAdded(serverID int, voting bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.serversAdded++

	m.serverMetrics[serverID] = &ServerMetric{
		ServerID:        serverID,
		AddedAt:         time.Now(),
		ProgressHistory: make([]ProgressPoint, 0),
	}
}

// RecordServerPromoted records a server being promoted to voting
func (m *SimpleConfigMetrics) RecordServerPromoted(serverID int, duration time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.serversPromoted++

	if metric, exists := m.serverMetrics[serverID]; exists {
		now := time.Now()
		metric.PromotedAt = &now
		metric.PromotionDuration = &duration
	}
}

// RecordCatchUpProgress records the catch-up progress of a server
func (m *SimpleConfigMetrics) RecordCatchUpProgress(serverID int, progress float64) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if metric, exists := m.serverMetrics[serverID]; exists {
		metric.LastProgress = progress

		// Add to history (keep last 100 points)
		metric.ProgressHistory = append(metric.ProgressHistory, ProgressPoint{
			Time:     time.Now(),
			Progress: progress,
		})

		if len(metric.ProgressHistory) > 100 {
			metric.ProgressHistory = metric.ProgressHistory[len(metric.ProgressHistory)-100:]
		}
	}
}

// RecordConfigurationError records a configuration error
func (m *SimpleConfigMetrics) RecordConfigurationError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.configErrors++

	m.lastErrors = append(m.lastErrors, err)
	if len(m.lastErrors) > m.maxErrors {
		m.lastErrors = m.lastErrors[1:]
	}
}

// GetSummary returns a summary of configuration metrics
func (m *SimpleConfigMetrics) GetSummary() ConfigMetricsSummary {
	m.mu.RLock()
	defer m.mu.RUnlock()

	summary := ConfigMetricsSummary{
		ServersAdded:    m.serversAdded,
		ServersPromoted: m.serversPromoted,
		ConfigErrors:    m.configErrors,
		ServerDetails:   make([]ServerMetricSummary, 0),
	}

	for _, metric := range m.serverMetrics {
		serverSummary := ServerMetricSummary{
			ServerID:     metric.ServerID,
			AddedAt:      metric.AddedAt,
			LastProgress: metric.LastProgress,
		}

		if metric.PromotedAt != nil {
			serverSummary.PromotedAt = metric.PromotedAt
			serverSummary.PromotionDuration = metric.PromotionDuration
		}

		summary.ServerDetails = append(summary.ServerDetails, serverSummary)
	}

	// Copy last errors
	summary.RecentErrors = make([]string, len(m.lastErrors))
	for i, err := range m.lastErrors {
		summary.RecentErrors[i] = err.Error()
	}

	return summary
}

// ConfigMetricsSummary provides a summary of configuration metrics
type ConfigMetricsSummary struct {
	ServersAdded    int
	ServersPromoted int
	ConfigErrors    int
	ServerDetails   []ServerMetricSummary
	RecentErrors    []string
}

// ServerMetricSummary provides a summary for a single server
type ServerMetricSummary struct {
	ServerID          int
	AddedAt           time.Time
	PromotedAt        *time.Time
	PromotionDuration *time.Duration
	LastProgress      float64
}

// String returns a string representation of the metrics summary
func (s ConfigMetricsSummary) String() string {
	str := "Configuration Metrics:\n"
	str += fmt.Sprintf("  Servers Added: %d\n", s.ServersAdded)
	str += fmt.Sprintf("  Servers Promoted: %d\n", s.ServersPromoted)
	str += fmt.Sprintf("  Configuration Errors: %d\n", s.ConfigErrors)

	if len(s.ServerDetails) > 0 {
		str += "\nServer Details:\n"
		for _, server := range s.ServerDetails {
			str += fmt.Sprintf("  Server %d:\n", server.ServerID)
			str += fmt.Sprintf("    Added: %s\n", server.AddedAt.Format(time.RFC3339))
			if server.PromotedAt != nil {
				str += fmt.Sprintf("    Promoted: %s (after %s)\n",
					server.PromotedAt.Format(time.RFC3339),
					server.PromotionDuration)
			} else {
				str += fmt.Sprintf("    Progress: %.1f%%\n", server.LastProgress*100)
			}
		}
	}

	if len(s.RecentErrors) > 0 {
		str += "\nRecent Errors:\n"
		for _, err := range s.RecentErrors {
			str += fmt.Sprintf("  - %s\n", err)
		}
	}

	return str
}
