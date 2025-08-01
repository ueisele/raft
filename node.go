package raft

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// applyConfigDefaults applies sensible defaults to a Config
func applyConfigDefaults(config *Config) *Config {
	// Create a copy to avoid modifying the original
	cfg := *config
	
	// Apply defaults for any zero values
	if cfg.MaxLogSize == 0 {
		// Default to 10,000 entries before snapshot
		// This balances memory usage with snapshot frequency
		cfg.MaxLogSize = 10000
	}
	
	if cfg.ElectionTimeoutMin == 0 {
		// Default to 150ms minimum election timeout
		cfg.ElectionTimeoutMin = 150 * time.Millisecond
	}
	
	if cfg.ElectionTimeoutMax == 0 {
		// Default to 300ms maximum election timeout
		cfg.ElectionTimeoutMax = 300 * time.Millisecond
	}
	
	if cfg.HeartbeatInterval == 0 {
		// Default to 50ms heartbeat interval
		// Should be much smaller than election timeout
		cfg.HeartbeatInterval = 50 * time.Millisecond
	}
	
	// Validate that min <= max for election timeout
	if cfg.ElectionTimeoutMin > cfg.ElectionTimeoutMax {
		cfg.ElectionTimeoutMax = cfg.ElectionTimeoutMin
	}
	
	return &cfg
}

// raftNode implements the Node interface
type raftNode struct {
	mu sync.RWMutex

	config       *Config
	transport    Transport
	persistence  Persistence
	stateMachine StateMachine

	state         *StateManager
	log           *LogManager
	election      *ElectionManager
	replication   *ReplicationManager
	snapshot      *SnapshotManager
	configuration *ConfigurationManager
	safeConfig    *SafeConfigurationManager // Safe configuration manager

	applyNotify chan struct{}
	stopCh      chan struct{}
	stopped     bool

	ctx    context.Context
	cancel context.CancelFunc
}

// NewNode creates a new Raft node
func NewNode(config *Config, transport Transport, persistence Persistence, stateMachine StateMachine) (Node, error) {
	// Apply defaults to config
	cfg := applyConfigDefaults(config)
	
	// Create the node
	node := &raftNode{
		config:       cfg,
		transport:    transport,
		persistence:  persistence,
		stateMachine: stateMachine,
		applyNotify:  make(chan struct{}, 1),
		stopCh:       make(chan struct{}),
	}

	// Initialize components
	node.state = NewStateManager(cfg.ID, cfg)
	node.log = NewLogManager()
	node.snapshot = NewSnapshotManager(node.log, stateMachine, persistence, cfg)
	node.configuration = NewConfigurationManager(cfg.Peers)
	node.election = NewElectionManager(cfg.ID, cfg.Peers, node.state, node.log, transport, cfg)
	node.replication = NewReplicationManager(cfg.ID, cfg.Peers, node.state, node.log, transport, cfg, stateMachine, node.snapshot, node.applyNotify)
	
	// Set the voting members count function
	node.replication.SetVotingMembersCountFunc(func() int {
		return len(node.configuration.GetVotingMembers())
	})
	
	// Initialize safe configuration manager if metrics are available
	var metrics ConfigMetrics
	if cfg.Metrics != nil {
		// Create a simple metrics wrapper if the main metrics interface is available
		metrics = NewSimpleConfigMetrics()
	}
	
	node.safeConfig = NewSafeConfigurationManager(
		node.configuration,
		node.replication,
		node.log,
		cfg.Logger,
		&SafeConfigOptions{
			PromotionThreshold:   0.95,
			PromotionCheckPeriod: 1 * time.Second,
			MinCatchUpEntries:    10,
			Metrics:              metrics,
		},
	)

	// Load persistent state if available
	if persistence != nil {
		if err := node.restoreState(); err != nil {
			return nil, err
		}
	}

	// Set transport RPC handler
	transport.SetRPCHandler(node)

	return node, nil
}

// Start starts the Raft node
func (n *raftNode) Start(ctx context.Context) error {
	n.mu.Lock()
	n.ctx, n.cancel = context.WithCancel(ctx)
	n.mu.Unlock()

	// Start transport
	if err := n.transport.Start(); err != nil {
		return err
	}

	// Start the main loop
	go n.run()

	// Start the apply loop
	go n.applyLoop()

	return nil
}

// Stop gracefully shuts down the Raft node
func (n *raftNode) Stop() {
	n.mu.Lock()
	if n.stopped {
		n.mu.Unlock()
		return
	}
	n.stopped = true
	
	// Save state before shutdown
	n.persist()
	
	if n.cancel != nil {
		n.cancel()
	}
	n.mu.Unlock()

	// Stop state manager timers
	n.state.Stop()
	
	// Stop safe configuration manager
	if n.safeConfig != nil {
		n.safeConfig.Stop()
	}
	
	close(n.stopCh)
	n.transport.Stop()
}

// Submit submits a command to the Raft cluster
func (n *raftNode) Submit(command interface{}) (int, int, bool) {
	n.mu.Lock()
	defer n.mu.Unlock()

	state, _ := n.state.GetState()
	if state != Leader {
		return -1, -1, false
	}

	// Append to log
	entry := LogEntry{
		Term:    n.state.GetCurrentTerm(),
		Index:   n.log.GetLastLogIndex() + 1,
		Command: command,
	}

	prevIndex := n.log.GetLastLogIndex()
	prevTerm := n.log.GetLastLogTerm()
	n.log.AppendEntries(prevIndex, prevTerm, []LogEntry{entry})

	// Persist state
	n.persist()

	// Trigger replication
	n.replication.Replicate()

	return entry.Index, entry.Term, true
}

// GetState returns current term and whether this server is the leader
func (n *raftNode) GetState() (int, bool) {
	n.mu.RLock()
	defer n.mu.RUnlock()

	state, _ := n.state.GetState()
	return n.state.GetCurrentTerm(), state == Leader
}

// IsLeader returns true if this node is the current leader
func (n *raftNode) IsLeader() bool {
	n.mu.RLock()
	defer n.mu.RUnlock()

	state, _ := n.state.GetState()
	return state == Leader
}

// GetCurrentTerm returns the current term
func (n *raftNode) GetCurrentTerm() int {
	n.mu.RLock()
	defer n.mu.RUnlock()

	return n.state.GetCurrentTerm()
}

// GetCommitIndex returns the current commit index
func (n *raftNode) GetCommitIndex() int {
	n.mu.RLock()
	defer n.mu.RUnlock()

	return n.log.GetCommitIndex()
}

// GetLogLength returns the current log length
func (n *raftNode) GetLogLength() int {
	n.mu.RLock()
	defer n.mu.RUnlock()

	return n.log.GetLastLogIndex()
}

// GetLogEntry returns the log entry at the specified index
func (n *raftNode) GetLogEntry(index int) *LogEntry {
	n.mu.RLock()
	defer n.mu.RUnlock()

	entry := n.log.GetEntry(index)
	return entry
}

// GetTransportHandler returns the transport handler for this node
func (n *raftNode) GetTransportHandler() Transport {
	return n.transport
}

// AddServer adds a new server to the cluster
// WARNING: Setting voting=true is unsafe and not recommended
func (n *raftNode) AddServer(id int, address string, voting bool) error {
	n.mu.Lock()
	defer n.mu.Unlock()

	// Only leader can add servers
	state, _ := n.state.GetState()
	if state != Leader {
		return fmt.Errorf("only leader can add servers")
	}

	// Log warning if adding voting server immediately
	if voting && n.config.Logger != nil {
		n.config.Logger.Warn("AddServer: Adding server %d as voting member immediately is UNSAFE. Consider using AddServerSafely instead.", id)
	}

	// Start configuration change
	server := ServerConfig{
		ID:      id,
		Address: address,
		Voting:  voting,
	}

	if err := n.configuration.StartAddServer(server); err != nil {
		return err
	}

	// Get the pending change
	change := n.configuration.GetPendingConfigChange()
	if change == nil {
		return fmt.Errorf("failed to create configuration change")
	}

	// Marshal the configuration change
	changeData, err := MarshalConfigChange(change)
	if err != nil {
		n.configuration.CancelPendingChange()
		return err
	}

	// Submit as a special log entry
	entry := LogEntry{
		Term:  n.state.GetCurrentTerm(),
		Index: n.log.GetLastLogIndex() + 1,
		Command: ConfigCommand{
			Type: "configuration_change",
			Data: changeData,
		},
	}

	prevIndex := n.log.GetLastLogIndex()
	prevTerm := n.log.GetLastLogTerm()
	n.log.AppendEntries(prevIndex, prevTerm, []LogEntry{entry})

	if n.config.Logger != nil {
		n.config.Logger.Info("Created configuration change entry at index %d", entry.Index)
	}

	// Persist and replicate
	n.persist()
	n.replication.Replicate()

	// For simplicity, we'll return immediately
	// In a production system, we'd wait for the entry to be committed
	return nil
}

// RemoveServer removes a server from the cluster
func (n *raftNode) RemoveServer(id int) error {
	n.mu.Lock()
	defer n.mu.Unlock()

	// Only leader can remove servers
	state, _ := n.state.GetState()
	if state != Leader {
		return fmt.Errorf("only leader can remove servers")
	}

	// Start configuration change
	if err := n.configuration.StartRemoveServer(id); err != nil {
		return err
	}

	// Get the pending change
	change := n.configuration.GetPendingConfigChange()
	if change == nil {
		return fmt.Errorf("failed to create configuration change")
	}

	// Marshal the configuration change
	changeData, err := MarshalConfigChange(change)
	if err != nil {
		n.configuration.CancelPendingChange()
		return err
	}

	// Submit as a special log entry
	entry := LogEntry{
		Term:  n.state.GetCurrentTerm(),
		Index: n.log.GetLastLogIndex() + 1,
		Command: ConfigCommand{
			Type: "configuration_change",
			Data: changeData,
		},
	}

	prevIndex := n.log.GetLastLogIndex()
	prevTerm := n.log.GetLastLogTerm()
	n.log.AppendEntries(prevIndex, prevTerm, []LogEntry{entry})

	if n.config.Logger != nil {
		n.config.Logger.Info("Created configuration change entry at index %d", entry.Index)
	}

	// Persist and replicate
	n.persist()
	n.replication.Replicate()

	return nil
}

// submitConfigurationChange submits a configuration change as a log entry
func (n *raftNode) submitConfigurationChange(change *PendingConfigChange) error {
	// Marshal the configuration change
	changeData, err := MarshalConfigChange(change)
	if err != nil {
		return err
	}

	// Submit as a special log entry
	entry := LogEntry{
		Term:  n.state.GetCurrentTerm(),
		Index: n.log.GetLastLogIndex() + 1,
		Command: ConfigCommand{
			Type: "configuration_change",
			Data: changeData,
		},
	}

	prevIndex := n.log.GetLastLogIndex()
	prevTerm := n.log.GetLastLogTerm()
	n.log.AppendEntries(prevIndex, prevTerm, []LogEntry{entry})

	if n.config.Logger != nil {
		n.config.Logger.Info("Created configuration change entry at index %d", entry.Index)
	}

	// Persist and replicate
	n.persist()
	n.replication.Replicate()

	return nil
}

// AddServerSafely adds a new server as non-voting and automatically promotes when caught up
func (n *raftNode) AddServerSafely(id int, address string) error {
	// Update safe config to know if we're leader
	n.safeConfig.SetIsLeaderFunc(func() bool {
		state, _ := n.state.GetState()
		return state == Leader
	})
	
	// Set the submit function to actually submit configuration changes
	n.safeConfig.SetSubmitConfigChangeFunc(func() error {
		// Get the pending change from configuration manager
		change := n.configuration.GetPendingConfigChange()
		if change == nil {
			return fmt.Errorf("no pending configuration change")
		}
		
		// Submit it using the node's internal method
		return n.submitConfigurationChange(change)
	})
	
	// Use the safe configuration manager
	return n.safeConfig.AddServerSafely(id, address)
}

// GetServerProgress returns the catch-up progress of a non-voting server
func (n *raftNode) GetServerProgress(id int) *ServerProgress {
	return n.safeConfig.GetServerProgress(id)
}

// GetConfiguration returns the current cluster configuration
func (n *raftNode) GetConfiguration() *ClusterConfiguration {
	return n.configuration.GetConfiguration()
}

// TransferLeadership attempts to transfer leadership to the specified server
func (n *raftNode) TransferLeadership(targetID int) error {
	n.mu.Lock()
	defer n.mu.Unlock()

	state, _ := n.state.GetState()
	if state != Leader {
		return fmt.Errorf("not the leader")
	}

	// Check if target is in the configuration and is a voting member
	config := n.configuration.GetConfiguration()
	found := false
	isVoting := false
	for _, server := range config.Servers {
		if server.ID == targetID {
			found = true
			isVoting = server.Voting
			break
		}
	}

	if !found {
		return fmt.Errorf("target server %d not in configuration", targetID)
	}
	
	if !isVoting {
		return fmt.Errorf("target server %d is not a voting member", targetID)
	}

	// Leadership transfer process:
	// 1. Stop accepting new client requests
	// 2. Bring target's log up to date
	// 3. Send TimeoutNow RPC to target to trigger immediate election
	
	// For a basic implementation:
	// We'll ensure the target is up-to-date and then step down
	
	// First, trigger replication to ensure target has latest entries
	n.replication.Replicate()
	
	// Give some time for replication
	go func() {
		time.Sleep(50 * time.Millisecond)
		
		n.mu.Lock()
		defer n.mu.Unlock()
		
		// Check if we're still leader
		state, _ := n.state.GetState()
		if state == Leader {
			// Step down to trigger new election
			// The target server should have the most up-to-date log
			// and win the election
			n.state.BecomeFollower(n.state.GetCurrentTerm())
			
			if n.config.Logger != nil {
				n.config.Logger.Info("Stepped down to transfer leadership to %d", targetID)
			}
		}
	}()

	return nil
}

// GetLeader returns the ID of the current leader (-1 if unknown)
func (n *raftNode) GetLeader() int {
	leaderID := n.state.GetLeaderID()
	if leaderID == nil {
		return -1
	}
	return *leaderID
}

// handleConfigurationChange applies a committed configuration change
func (n *raftNode) handleConfigurationChange(data []byte, index int) {
	// Unmarshal the configuration change
	change, err := UnmarshalConfigChange(data)
	if err != nil {
		if n.config.Logger != nil {
			n.config.Logger.Error("Failed to unmarshal configuration change: %v", err)
		}
		return
	}

	if n.config.Logger != nil {
		n.config.Logger.Info("Applying configuration change type=%s at index %d", change.Type, index)
	}

	// Apply the configuration change
	n.mu.Lock()
	defer n.mu.Unlock()

	// Apply the configuration directly from the change data
	n.configuration.ApplyCommittedConfiguration(change.NewConfig, index)

	// Update election and replication managers with new configuration
	// Get all member IDs (both voting and non-voting)
	allMembers := []int{}
	currentConfig := n.configuration.GetConfiguration()
	for _, server := range currentConfig.Servers {
		allMembers = append(allMembers, server.ID)
	}

	// Check if this node has been removed from the configuration
	nodeInConfig := false
	for _, server := range currentConfig.Servers {
		if server.ID == n.config.ID {
			nodeInConfig = true
			break
		}
	}
	
	// If this node was removed and is currently the leader, schedule step down
	if !nodeInConfig {
		state, _ := n.state.GetState()
		if state == Leader {
			if n.config.Logger != nil {
				n.config.Logger.Info("Scheduling step down after being removed from configuration")
			}
			// Schedule step down after a delay to allow configuration propagation
			// Use a timer to ensure cleanup
			time.AfterFunc(500*time.Millisecond, func() {
				n.mu.Lock()
				defer n.mu.Unlock()
				
				// Check again if we're still leader
				state, _ := n.state.GetState()
				if state == Leader {
					if n.config.Logger != nil {
						n.config.Logger.Info("Stepping down as leader after being removed from configuration")
					}
					n.state.BecomeFollower(n.state.GetCurrentTerm())
					// Stop replication to prevent further heartbeats
					n.replication.StopReplication()
				}
			})
		}
	} else {
		// Check if the current leader was removed
		state, _ := n.state.GetState()
		if state == Follower {
			leaderID := n.state.GetLeaderID()
			if leaderID != nil {
				// Check if the leader is still in the configuration
				leaderInConfig := false
				for _, server := range currentConfig.Servers {
					if server.ID == *leaderID {
						leaderInConfig = true
						break
					}
				}
				
				if !leaderInConfig {
					// Leader was removed, clear leader ID to trigger new election
					if n.config.Logger != nil {
						n.config.Logger.Info("Leader %d was removed from configuration, clearing leader ID", *leaderID)
					}
					n.state.SetLeaderID(nil)
					// Force election timer to expire soon
					n.state.ForceElectionTimeout()
				}
			}
		}
	}

	// Update the election and replication managers with the new configuration
	n.election.UpdatePeers(allMembers)
	// Update replication manager's peer list if we're leader
	state, _ := n.state.GetState()
	if state == Leader {
		if n.config.Logger != nil {
			n.config.Logger.Info("Leader updating replication peers after config change: %v", allMembers)
		}
		n.replication.UpdatePeers(allMembers)
		// Force immediate heartbeat to new configuration
		n.replication.SendHeartbeats()
	}
	
	if n.config.Logger != nil {
		n.config.Logger.Info("Configuration changed at index %d: %v", index, allMembers)
	}
}

// run is the main event loop
func (n *raftNode) run() {
	defer func() {
		if n.config.Logger != nil {
			n.config.Logger.Info("Node %d: run loop exiting", n.config.ID)
		}
	}()
	
	// Use a ticker to periodically check timers
	// This ensures we don't miss timer resets
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	
	for {
		select {
		case <-n.ctx.Done():
			if n.config.Logger != nil {
				n.config.Logger.Info("Node %d: run loop exiting due to context done", n.config.ID)
			}
			return
		case <-n.stopCh:
			if n.config.Logger != nil {
				n.config.Logger.Info("Node %d: run loop exiting due to stop channel", n.config.ID)
			}
			return

		case <-ticker.C:
			// Check election timer
			select {
			case <-n.state.GetElectionTimer():
				n.mu.Lock()
				state, _ := n.state.GetState()
				if n.config.Logger != nil {
					n.config.Logger.Info("Node %d: Election timer expired, state=%v", n.config.ID, state)
				}
				if state != Leader {
					// Check if we're in the current configuration as a voting member
					config := n.configuration.GetConfiguration()
					inConfig := false
					isVoting := false
					for _, server := range config.Servers {
						if server.ID == n.config.ID {
							inConfig = true
							isVoting = server.Voting
							break
						}
					}
					
					if inConfig && isVoting {
						// Start election only if we're a voting member
						n.state.BecomeCandidate()
						n.persist()

						// Run election in background
						go func() {
							if n.election.StartElection() {
								n.mu.Lock()
								n.state.BecomeLeader()
								// Set self as leader
								leaderID := n.config.ID
								n.state.SetLeaderID(&leaderID)
								n.replication.BecomeLeader()
								n.mu.Unlock()
							}
						}()
					} else {
						// Either not in configuration or non-voting member
						if n.config.Logger != nil {
							if !inConfig {
								n.config.Logger.Debug("Node %d not starting election - not in configuration", n.config.ID)
							} else {
								n.config.Logger.Debug("Node %d not starting election - non-voting member", n.config.ID)
							}
						}
						// Reset timer anyway to avoid busy loop
						n.state.ResetElectionTimer()
					}
				}
				n.mu.Unlock()
			default:
				// Timer hasn't expired yet
			}
			
			// Check heartbeat timer for leaders
			select {
			case <-n.state.GetHeartbeatTicker():
				n.mu.RLock()
				state, _ := n.state.GetState()
				if state == Leader {
					n.replication.SendHeartbeats()
				}
				n.mu.RUnlock()
			default:
				// Heartbeat ticker hasn't fired yet
			}
		}
	}
}

// applyLoop applies committed entries to the state machine
func (n *raftNode) applyLoop() {
	lastApplied := 0
	
	// Create a ticker for periodic checks
	checkTicker := time.NewTicker(100 * time.Millisecond)
	defer checkTicker.Stop()

	for {
		select {
		case <-n.ctx.Done():
			return
		case <-n.stopCh:
			return
		case <-n.applyNotify:
			// Apply committed entries
			n.mu.RLock()
			commitIndex := n.log.GetCommitIndex()
			entries := []LogEntry{}

			if commitIndex > lastApplied {
				entries = n.log.GetEntries(lastApplied+1, commitIndex+1)
			}
			n.mu.RUnlock()

			for _, entry := range entries {
				// Check if this is a configuration change
				if configCmd, ok := entry.Command.(ConfigCommand); ok {
					if configCmd.Type == "configuration_change" {
						if n.config.Logger != nil {
							n.config.Logger.Info("Applying configuration change at index %d", entry.Index)
						}
						// Handle configuration change
						n.handleConfigurationChange(configCmd.Data, entry.Index)
					}
				} else {
					// Normal command - apply to state machine
					n.stateMachine.Apply(entry)
				}
				lastApplied = entry.Index
				
				// Update lastApplied in log manager
				n.mu.Lock()
				n.log.SetLastApplied(entry.Index)
				n.mu.Unlock()
			}
			
			// Check if we need to take a snapshot
			if n.snapshot != nil && n.snapshot.NeedsSnapshot() {
				n.mu.Lock()
				if err := n.snapshot.TakeSnapshot(lastApplied); err != nil {
					if n.config.Logger != nil {
						n.config.Logger.Error("Failed to take snapshot: %v", err)
					}
				}
				n.mu.Unlock()
			}
		case <-checkTicker.C:
			// Periodic check for missed notifications
			n.mu.RLock()
			commitIndex := n.log.GetCommitIndex()
			n.mu.RUnlock()

			if commitIndex > lastApplied {
				select {
				case n.applyNotify <- struct{}{}:
				default:
				}
			}
		}
	}
}

// persist saves state to persistence
func (n *raftNode) persist() {
	if n.persistence == nil {
		return
	}

	state := &PersistentState{
		CurrentTerm: n.state.GetCurrentTerm(),
		VotedFor:    n.state.GetVotedFor(),
		Log:         n.log.GetAllEntries(),
		CommitIndex: n.log.GetCommitIndex(),
	}

	n.persistence.SaveState(state)
}

// restoreState loads state from persistence
func (n *raftNode) restoreState() error {
	state, err := n.persistence.LoadState()
	if err != nil {
		return err
	}

	if state != nil {
		n.state.SetCurrentTerm(state.CurrentTerm)
		if state.VotedFor != nil {
			n.state.SetVotedFor(*state.VotedFor)
		}

		if len(state.Log) > 0 {
			// Restore log entries - append to empty log
			n.log.AppendEntries(0, 0, state.Log)
		}

		// Restore commit index
		n.log.SetCommitIndex(state.CommitIndex)
	}

	// Load snapshot if exists
	snapshot, err := n.persistence.LoadSnapshot()
	if err != nil {
		return err
	}

	if snapshot != nil {
		// Restore state machine
		if err := n.stateMachine.Restore(snapshot.Data); err != nil {
			return err
		}

		// Update log with snapshot info
		n.log.InstallSnapshot(snapshot.LastIncludedIndex, snapshot.LastIncludedTerm)
	}

	return nil
}

// Transport interface implementation (RPC handlers)

// RequestVote handles RequestVote RPC
func (n *raftNode) RequestVote(args *RequestVoteArgs, reply *RequestVoteReply) error {
	n.mu.Lock()
	defer n.mu.Unlock()

	n.election.HandleRequestVote(args, reply)
	n.persist()

	// Notify apply loop in case there are new entries to apply
	select {
	case n.applyNotify <- struct{}{}:
	default:
	}

	return nil
}

// AppendEntries handles AppendEntries RPC
func (n *raftNode) AppendEntries(args *AppendEntriesArgs, reply *AppendEntriesReply) error {
	n.mu.Lock()
	defer n.mu.Unlock()

	n.replication.HandleAppendEntries(args, reply)
	n.persist()
	
	// Record heartbeat for vote denial optimization
	if reply.Success {
		n.election.RecordHeartbeat()
	}

	// Notify apply loop in case there are new entries to apply
	select {
	case n.applyNotify <- struct{}{}:
	default:
	}

	return nil
}

// InstallSnapshot handles InstallSnapshot RPC
func (n *raftNode) InstallSnapshot(args *InstallSnapshotArgs, reply *InstallSnapshotReply) error {
	n.mu.Lock()
	defer n.mu.Unlock()

	currentTerm := n.state.GetCurrentTerm()
	if err := n.snapshot.HandleInstallSnapshot(args, reply, currentTerm); err != nil {
		return err
	}
	n.persist()

	// Notify apply loop
	select {
	case n.applyNotify <- struct{}{}:
	default:
	}

	return nil
}
