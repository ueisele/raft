package raft

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// SafeConfigurationManager extends ConfigurationManager with safety features
// for adding new servers. It implements automatic promotion from non-voting
// to voting members after they have caught up with the log.
//
// Limitation: The promotion tracking state is not replicated across nodes. 
// If leadership changes after adding a non-voting server, the new leader's 
// SafeConfigurationManager won't know about pending promotions. In such cases,
// the server will remain non-voting until manually promoted.
type SafeConfigurationManager struct {
	*ConfigurationManager
	mu sync.RWMutex
	
	// Dependencies
	replicationManager *ReplicationManager
	logManager         *LogManager
	logger             Logger
	isLeader           func() bool // Function to check if node is leader
	submitConfigChange func() error // Function to submit configuration changes
	
	// Tracking server catch-up progress
	serverProgress map[int]*ServerProgress
	
	// Configuration for automatic promotion
	promotionThreshold   float64       // Percentage of log caught up (e.g., 0.95 for 95%)
	promotionCheckPeriod time.Duration // How often to check for promotion
	minCatchUpEntries    int           // Minimum entries to replicate before promotion
	
	// Metrics collection
	metrics ConfigMetrics
	
	// Context for cancellation
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// ServerProgress tracks the catch-up progress of a non-voting server
type ServerProgress struct {
	ServerID         int
	StartTime        time.Time
	LastChecked      time.Time
	InitialLogIndex  int
	TargetLogIndex   int
	CurrentIndex     int
	PromotionPending bool
	Error            error
}

// CatchUpProgress returns the catch-up progress as a ratio (0.0 to 1.0)
func (sp *ServerProgress) CatchUpProgress() float64 {
	if sp.TargetLogIndex == 0 {
		return 1.0 // Empty log means fully caught up
	}
	return float64(sp.CurrentIndex) / float64(sp.TargetLogIndex)
}

// ConfigMetrics provides metrics about configuration changes
type ConfigMetrics interface {
	RecordServerAdded(serverID int, voting bool)
	RecordServerPromoted(serverID int, duration time.Duration)
	RecordCatchUpProgress(serverID int, progress float64)
	RecordConfigurationError(err error)
}

// SafeConfigOptions contains options for SafeConfigurationManager
type SafeConfigOptions struct {
	PromotionThreshold   float64       // Default: 0.95 (95%)
	PromotionCheckPeriod time.Duration // Default: 1 second
	MinCatchUpEntries    int           // Default: 10
	Metrics              ConfigMetrics // Optional metrics collector
}

// DefaultSafeConfigOptions returns default options
func DefaultSafeConfigOptions() *SafeConfigOptions {
	return &SafeConfigOptions{
		PromotionThreshold:   0.95,
		PromotionCheckPeriod: 1 * time.Second,
		MinCatchUpEntries:    10,
	}
}

// NewSafeConfigurationManager creates a new safe configuration manager
func NewSafeConfigurationManager(
	base *ConfigurationManager,
	replicationManager *ReplicationManager,
	logManager *LogManager,
	logger Logger,
	options *SafeConfigOptions,
) *SafeConfigurationManager {
	if options == nil {
		options = DefaultSafeConfigOptions()
	}
	
	ctx, cancel := context.WithCancel(context.Background())
	
	scm := &SafeConfigurationManager{
		ConfigurationManager: base,
		replicationManager:   replicationManager,
		logManager:          logManager,
		logger:              logger,
		serverProgress:      make(map[int]*ServerProgress),
		promotionThreshold:  options.PromotionThreshold,
		promotionCheckPeriod: options.PromotionCheckPeriod,
		minCatchUpEntries:   options.MinCatchUpEntries,
		metrics:             options.Metrics,
		ctx:                 ctx,
		cancel:              cancel,
	}
	
	// Start the promotion monitor
	scm.wg.Add(1)
	go scm.promotionMonitor()
	
	return scm
}

// Stop stops the safe configuration manager
func (scm *SafeConfigurationManager) Stop() {
	scm.cancel()
	scm.wg.Wait()
}

// AddServerSafely adds a new server with safety checks and automatic promotion
func (scm *SafeConfigurationManager) AddServerSafely(serverID int, address string) error {
	scm.mu.Lock()
	defer scm.mu.Unlock()
	
	// Safety check 1: Verify we're the leader
	if !scm.checkIsLeader() {
		return fmt.Errorf("only leader can add servers")
	}
	
	// Safety check 2: Check if server already exists
	config := scm.GetConfiguration()
	for _, server := range config.Servers {
		if server.ID == serverID {
			return fmt.Errorf("server %d already exists in configuration", serverID)
		}
	}
	
	// Safety check 3: Get current log state for tracking
	currentLogIndex := scm.logManager.GetLastLogIndex()
	commitIndex := scm.logManager.GetCommitIndex()
	
	// Log the addition
	if scm.logger != nil {
		scm.logger.Info("Adding server %d as non-voting member (log index: %d, commit: %d)", 
			serverID, currentLogIndex, commitIndex)
	}
	
	// Always add as non-voting first
	err := scm.ConfigurationManager.StartAddServer(ServerConfig{
		ID:      serverID,
		Address: address,
		Voting:  false, // ALWAYS start as non-voting
	})
	
	if err != nil {
		if scm.metrics != nil {
			scm.metrics.RecordConfigurationError(err)
		}
		return fmt.Errorf("failed to add non-voting server: %w", err)
	}
	
	// Submit the configuration change
	if scm.submitConfigChange != nil {
		if err := scm.submitConfigChange(); err != nil {
			scm.ConfigurationManager.CancelPendingChange()
			if scm.metrics != nil {
				scm.metrics.RecordConfigurationError(err)
			}
			return fmt.Errorf("failed to submit configuration change: %w", err)
		}
	}
	
	// Track the server's catch-up progress
	scm.serverProgress[serverID] = &ServerProgress{
		ServerID:        serverID,
		StartTime:       time.Now(),
		LastChecked:     time.Now(),
		InitialLogIndex: 0, // New server starts with empty log
		TargetLogIndex:  currentLogIndex,
		CurrentIndex:    0,
	}
	
	// Record metrics
	if scm.metrics != nil {
		scm.metrics.RecordServerAdded(serverID, false)
	}
	
	return nil
}

// AddVotingServerUnsafe adds a voting server immediately (unsafe, not recommended)
func (scm *SafeConfigurationManager) AddVotingServerUnsafe(serverID int, address string) error {
	// Log a warning
	if scm.logger != nil {
		scm.logger.Warn("Adding server %d as VOTING member immediately - this is UNSAFE and not recommended!", serverID)
	}
	
	// Safety check: Warn if the new server would affect quorum significantly
	config := scm.GetConfiguration()
	votingCount := 0
	for _, server := range config.Servers {
		if server.Voting {
			votingCount++
		}
	}
	
	newMajority := (votingCount + 2) / 2 + 1 // After adding new voting server
	if newMajority > votingCount {
		if scm.logger != nil {
			scm.logger.Warn("WARNING: Adding voting server increases majority from %d to %d - cluster may lose availability!", 
				(votingCount/2)+1, newMajority)
		}
	}
	
	return scm.ConfigurationManager.StartAddServer(ServerConfig{
		ID:      serverID,
		Address: address,
		Voting:  true,
	})
}

// promotionMonitor runs in the background and promotes servers when ready
func (scm *SafeConfigurationManager) promotionMonitor() {
	defer scm.wg.Done()
	
	ticker := time.NewTicker(scm.promotionCheckPeriod)
	defer ticker.Stop()
	
	for {
		select {
		case <-scm.ctx.Done():
			return
		case <-ticker.C:
			scm.checkAndPromoteServers()
		}
	}
}

// checkAndPromoteServers checks all non-voting servers for promotion readiness
func (scm *SafeConfigurationManager) checkAndPromoteServers() {
	scm.mu.Lock()
	defer scm.mu.Unlock()
	
	// Only leader can promote servers
	if !scm.checkIsLeader() {
		if scm.logger != nil {
			scm.logger.Debug("Not checking for promotions - not leader")
		}
		return
	}
	
	// Get current configuration
	config := scm.GetConfiguration()
	
	if scm.logger != nil {
		scm.logger.Debug("Checking %d servers for promotion", len(config.Servers))
	}
	
	// Check each non-voting server
	for _, server := range config.Servers {
		if !server.Voting {
			if scm.logger != nil {
				scm.logger.Debug("Checking non-voting server %d for promotion", server.ID)
			}
			scm.checkServerForPromotion(server.ID)
		}
	}
}

// checkServerForPromotion checks if a specific server is ready for promotion
func (scm *SafeConfigurationManager) checkServerForPromotion(serverID int) {
	progress, exists := scm.serverProgress[serverID]
	if !exists {
		// Not tracking this server
		return
	}
	
	// Skip if promotion already pending
	if progress.PromotionPending {
		return
	}
	
	// Get current replication state
	matchIndex := scm.replicationManager.GetMatchIndex(serverID)
	lastLogIndex := scm.logManager.GetLastLogIndex()
	commitIndex := scm.logManager.GetCommitIndex()
	
	// Update progress
	progress.CurrentIndex = matchIndex
	progress.LastChecked = time.Now()
	
	// Calculate catch-up progress
	var catchUpProgress float64
	if lastLogIndex > 0 {
		catchUpProgress = float64(matchIndex) / float64(lastLogIndex)
	} else {
		catchUpProgress = 1.0 // Empty log means fully caught up
	}
	
	// Record metrics
	if scm.metrics != nil {
		scm.metrics.RecordCatchUpProgress(serverID, catchUpProgress)
	}
	
	// Check promotion criteria
	entriesReplicated := matchIndex - progress.InitialLogIndex
	caughtUpToCommit := matchIndex >= commitIndex
	sufficientProgress := catchUpProgress >= scm.promotionThreshold
	sufficientEntries := entriesReplicated >= scm.minCatchUpEntries
	
	if scm.logger != nil {
		scm.logger.Debug("Server %d catch-up: progress=%.2f%%, match=%d, commit=%d, entries=%d",
			serverID, catchUpProgress*100, matchIndex, commitIndex, entriesReplicated)
	}
	
	// Promote if all criteria are met
	if caughtUpToCommit && sufficientProgress && sufficientEntries {
		if scm.logger != nil {
			scm.logger.Info("Server %d ready for promotion: caught up to index %d (%.2f%% of log)",
				serverID, matchIndex, catchUpProgress*100)
		}
		
		progress.PromotionPending = true
		
		// Schedule promotion (do it asynchronously to avoid holding lock)
		go scm.promoteServer(serverID)
	}
}

// promoteServer promotes a non-voting server to voting
func (scm *SafeConfigurationManager) promoteServer(serverID int) {
	// Start a configuration change to promote the server
	scm.mu.Lock()
	
	// Find the server in current config
	config := scm.GetConfiguration()
	var serverToPromote *ServerConfig
	for _, server := range config.Servers {
		if server.ID == serverID && !server.Voting {
			s := server // Copy
			serverToPromote = &s
			break
		}
	}
	
	if serverToPromote == nil {
		if scm.logger != nil {
			scm.logger.Warn("Server %d not found or already voting", serverID)
		}
		scm.mu.Unlock()
		return
	}
	
	// Create a configuration change to promote to voting
	if scm.logger != nil {
		scm.logger.Info("Server %d is ready for promotion to voting member", serverID)
	}
	
	// Get the server's address from current configuration
	serverAddress := serverToPromote.Address
	
	// Record metrics before starting configuration change
	var startTime time.Time
	if progress, exists := scm.serverProgress[serverID]; exists {
		startTime = progress.StartTime
	}
	
	scm.mu.Unlock()
	
	// To promote a non-voting server to voting, we need to:
	// 1. Remove the non-voting server
	// 2. Add it back as a voting server
	// This ensures proper configuration change tracking
	
	// First, remove the non-voting server
	err := scm.ConfigurationManager.StartRemoveServer(serverID)
	if err != nil {
		if scm.logger != nil {
			scm.logger.Error("Failed to remove non-voting server %d for promotion: %v", serverID, err)
		}
		if scm.metrics != nil {
			scm.metrics.RecordConfigurationError(err)
		}
		return
	}
	
	// Submit the removal
	if scm.submitConfigChange != nil {
		if err := scm.submitConfigChange(); err != nil {
			scm.ConfigurationManager.CancelPendingChange()
			if scm.logger != nil {
				scm.logger.Error("Failed to submit removal for promotion: %v", err)
			}
			return
		}
	}
	
	// Wait a bit for the removal to be processed
	time.Sleep(100 * time.Millisecond)
	
	// Now add it back as a voting server
	err = scm.ConfigurationManager.StartAddServer(ServerConfig{
		ID:      serverID,
		Address: serverAddress,
		Voting:  true,
	})
	if err != nil {
		if scm.logger != nil {
			scm.logger.Error("Failed to add server %d as voting member: %v", serverID, err)
		}
		if scm.metrics != nil {
			scm.metrics.RecordConfigurationError(err)
		}
		return
	}
	
	// Submit the addition
	if scm.submitConfigChange != nil {
		if err := scm.submitConfigChange(); err != nil {
			scm.ConfigurationManager.CancelPendingChange()
			if scm.logger != nil {
				scm.logger.Error("Failed to submit addition for promotion: %v", err)
			}
			return
		}
	}
	
	// Success - update metrics and clean up tracking
	scm.mu.Lock()
	defer scm.mu.Unlock()
	
	if scm.metrics != nil && !startTime.IsZero() {
		duration := time.Since(startTime)
		scm.metrics.RecordServerPromoted(serverID, duration)
	}
	
	// Clean up tracking
	delete(scm.serverProgress, serverID)
	
	if scm.logger != nil {
		scm.logger.Info("Server %d successfully promoted to voting member", serverID)
	}
}

// GetServerProgress returns the catch-up progress for a server
func (scm *SafeConfigurationManager) GetServerProgress(serverID int) *ServerProgress {
	scm.mu.RLock()
	defer scm.mu.RUnlock()
	
	if progress, exists := scm.serverProgress[serverID]; exists {
		// Return a copy
		p := *progress
		return &p
	}
	return nil
}

// GetAllServerProgress returns progress for all tracked servers
func (scm *SafeConfigurationManager) GetAllServerProgress() map[int]*ServerProgress {
	scm.mu.RLock()
	defer scm.mu.RUnlock()
	
	result := make(map[int]*ServerProgress)
	for id, progress := range scm.serverProgress {
		p := *progress
		result[id] = &p
	}
	return result
}

// SetIsLeaderFunc sets the function to check if node is leader
func (scm *SafeConfigurationManager) SetIsLeaderFunc(f func() bool) {
	scm.mu.Lock()
	defer scm.mu.Unlock()
	scm.isLeader = f
}

// SetSubmitConfigChangeFunc sets the function to submit configuration changes
func (scm *SafeConfigurationManager) SetSubmitConfigChangeFunc(f func() error) {
	scm.mu.Lock()
	defer scm.mu.Unlock()
	scm.submitConfigChange = f
}

// checkIsLeader checks if the current node is the leader
func (scm *SafeConfigurationManager) checkIsLeader() bool {
	if scm.isLeader != nil {
		return scm.isLeader()
	}
	return false
}