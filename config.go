package raft

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"time"
)

var (
	ErrTimeout = errors.New("operation timed out")
)

// Configuration change type constants
const (
	AddServer         = "add"
	RemoveServer      = "remove"
	AddNonVotingServer = "add_nonvoting"
	PromoteServer     = "promote"
)

// Configuration phase constants
const (
	JointConsensus    = "joint"
	NewConfiguration  = "new"
)

// ServerConfiguration represents a server in the cluster
type ServerConfiguration struct {
	ID        int    `json:"id"`
	Address   string `json:"address"`
	NonVoting bool   `json:"non_voting,omitempty"`
}

// Configuration represents the cluster configuration
type Configuration struct {
	Servers []ServerConfiguration `json:"servers"`
}

// ConfigurationChange represents a configuration change operation
type ConfigurationChange struct {
	Type        string        `json:"type"` // "add" or "remove"
	Server      ServerConfiguration `json:"server"`
	OldConfig   Configuration `json:"oldConfig"`
	NewConfig   Configuration `json:"newConfig"`
	JointConfig *Configuration `json:"jointConfig,omitempty"`
	Phase       string        `json:"phase,omitempty"` // "joint" or "new"
}

// AddServer adds a server to the cluster configuration
func (rf *Raft) AddServer(server ServerConfiguration) error {
	rf.mu.Lock()
	
	if rf.state != Leader {
		rf.mu.Unlock()
		return fmt.Errorf("only leader can add servers")
	}

	// Check if a configuration change is already in progress
	if rf.hasPendingConfigChange() {
		rf.mu.Unlock()
		return fmt.Errorf("configuration change already in progress")
	}

	// Create new configuration
	currentConfig := rf.getCurrentConfiguration()
	
	// Check if server already exists
	for _, s := range currentConfig.Servers {
		if s.ID == server.ID {
			rf.mu.Unlock()
			return fmt.Errorf("server %d already exists in configuration", server.ID)
		}
	}

	var newConfig Configuration
	
	// If the server is not marked as non-voting, we should ensure it has caught up
	// before adding it as a voting member
	if !server.NonVoting {
		// First add as non-voting member to let it catch up
		nonVotingServer := server
		nonVotingServer.NonVoting = true
		
		// Add as non-voting member without joint consensus
		nonVotingConfig := Configuration{
			Servers: append(currentConfig.Servers, nonVotingServer),
		}
		
		change := ConfigurationChange{
			Type:      AddNonVotingServer,
			Server:    nonVotingServer,
			OldConfig: currentConfig,
			NewConfig: nonVotingConfig,
		}
		
		changeData, err := json.Marshal(change)
		if err != nil {
			rf.mu.Unlock()
			return fmt.Errorf("failed to marshal non-voting configuration change: %v", err)
		}
		
		rf.mu.Unlock()
		
		index, term, isLeader := rf.Submit(changeData)
		if !isLeader {
			return fmt.Errorf("lost leadership during configuration change")
		}
		
		if err := rf.waitForCommit(index, term); err != nil {
			return fmt.Errorf("failed to add non-voting server: %v", err)
		}
		
		// Now wait for the new server to catch up
		if err := rf.waitForServerCatchUp(server.ID); err != nil {
			return fmt.Errorf("new server failed to catch up: %v", err)
		}
		
		rf.mu.Lock()
		// Update current config for the voting member promotion
		currentConfig = rf.getCurrentConfiguration()
		
		// Replace the non-voting server with a voting one
		newServers := make([]ServerConfiguration, 0, len(currentConfig.Servers))
		for _, s := range currentConfig.Servers {
			if s.ID == server.ID {
				// Promote to voting member
				votingServer := s
				votingServer.NonVoting = false
				newServers = append(newServers, votingServer)
			} else {
				newServers = append(newServers, s)
			}
		}
		newConfig = Configuration{
			Servers: newServers,
		}
		rf.mu.Unlock()
	} else {
		// Add new server directly
		newConfig = Configuration{
			Servers: append(currentConfig.Servers, server),
		}
		rf.mu.Unlock()
	}
	
	// Use joint consensus for configuration change
	return rf.changeConfiguration(currentConfig, newConfig)
}

// RemoveServer removes a server from the cluster configuration
func (rf *Raft) RemoveServer(serverID int) error {
	rf.mu.Lock()

	if rf.state != Leader {
		rf.mu.Unlock()
		return fmt.Errorf("not the leader")
	}

	// Check if a configuration change is already in progress
	if rf.hasPendingConfigChange() {
		rf.mu.Unlock()
		return fmt.Errorf("configuration change already in progress")
	}

	currentConfig := rf.getCurrentConfiguration()
	
	// Find and remove the server
	newServers := make([]ServerConfiguration, 0, len(currentConfig.Servers)-1)
	found := false
	for _, s := range currentConfig.Servers {
		if s.ID != serverID {
			newServers = append(newServers, s)
		} else {
			found = true
		}
	}

	if !found {
		rf.mu.Unlock()
		return fmt.Errorf("server %d not found in configuration", serverID)
	}

	newConfig := Configuration{
		Servers: newServers,
	}

	// Special handling if we're removing ourselves (the leader)
	if serverID == rf.me {
		// We need to submit the configuration change first, then transfer leadership
		// This ensures the configuration change is in the log and will be applied
		
		// Find a suitable target for leadership transfer
		var targetServer int
		found := false
		for _, s := range newServers {
			if rf.isVotingMember(s.ID) && s.ID != rf.me {
				// Check if this server is reasonably caught up
				for i, peerID := range rf.peers {
					if peerID == s.ID && i < len(rf.matchIndex) {
						// If the server is caught up (within 10 entries), use it
						if rf.matchIndex[i] >= rf.getLastLogIndex()-10 {
							targetServer = s.ID
							found = true
							break
						}
					}
				}
				if found {
					break
				}
			}
		}
		
		if !found {
			// If no suitable target found, still proceed with the configuration change
			log.Printf("Server %d: No suitable target for leadership transfer, proceeding with self-removal", rf.me)
		}
		
		rf.mu.Unlock()
		
		// Submit the configuration change (this will add it to the log)
		err := rf.changeConfiguration(currentConfig, newConfig)
		if err != nil {
			return fmt.Errorf("failed to submit configuration change: %v", err)
		}
		
		// Now transfer leadership if we found a suitable target
		if found {
			log.Printf("Server %d transferring leadership to %d after submitting configuration change", rf.me, targetServer)
			
			// Transfer leadership
			if err := rf.TransferLeadership(targetServer); err != nil {
				// Log but don't fail - configuration change is already in progress
				log.Printf("Server %d: Failed to transfer leadership: %v", rf.me, err)
			}
			
			return fmt.Errorf("leadership transferred")
		}
		
		return nil
	}

	rf.mu.Unlock()
	
	return rf.changeConfiguration(currentConfig, newConfig)
}

// changeConfiguration implements the joint consensus algorithm for configuration changes
func (rf *Raft) changeConfiguration(oldConfig, newConfig Configuration) error {
	// Create joint consensus configuration
	jointConfig := Configuration{
		Servers: make([]ServerConfiguration, 0, len(oldConfig.Servers)+len(newConfig.Servers)),
	}
	
	// Add all servers from both configurations (avoiding duplicates)
	serverMap := make(map[int]ServerConfiguration)
	for _, s := range oldConfig.Servers {
		serverMap[s.ID] = s
	}
	for _, s := range newConfig.Servers {
		serverMap[s.ID] = s
	}
	
	for _, s := range serverMap {
		jointConfig.Servers = append(jointConfig.Servers, s)
	}

	// Step 1: Commit the joint configuration (Cold,new)
	change := ConfigurationChange{
		Type:        "joint",
		OldConfig:   oldConfig,
		NewConfig:   newConfig,
		JointConfig: &jointConfig,
	}

	changeData, err := json.Marshal(change)
	if err != nil {
		return fmt.Errorf("failed to marshal configuration change: %v", err)
	}

	// Submit the joint configuration as a log entry
	index, term, isLeader := rf.Submit(changeData)
	if !isLeader {
		return fmt.Errorf("lost leadership during configuration change")
	}

	// Wait for the joint configuration to be committed
	if err := rf.waitForCommit(index, term); err != nil {
		return fmt.Errorf("failed to commit joint configuration: %v", err)
	}

	log.Printf("Server %d committed joint configuration", rf.me)

	// Step 2: Commit the new configuration (Cnew)
	finalChange := ConfigurationChange{
		Type:      "final",
		OldConfig: oldConfig,
		NewConfig: newConfig,
	}
	
	log.Printf("Server %d submitting final configuration", rf.me)

	finalData, err := json.Marshal(finalChange)
	if err != nil {
		return fmt.Errorf("failed to marshal final configuration change: %v", err)
	}

	finalIndex, finalTerm, isLeader := rf.Submit(finalData)
	if !isLeader {
		return fmt.Errorf("lost leadership during final configuration change")
	}

	// Wait for the final configuration to be committed
	if err := rf.waitForCommit(finalIndex, finalTerm); err != nil {
		return fmt.Errorf("failed to commit final configuration: %v", err)
	}

	log.Printf("Server %d completed configuration change", rf.me)
	return nil
}

// getCurrentConfiguration returns the current cluster configuration
func (rf *Raft) getCurrentConfiguration() Configuration {
	// Return the current configuration from the Raft state
	return rf.currentConfig
}

// hasPendingConfigChange checks if there's a configuration change in the log that hasn't been applied yet
func (rf *Raft) hasPendingConfigChange() bool {
	// Check if we're in joint consensus
	if rf.inJointConsensus {
		return true
	}
	
	// Check if there are any configuration entries after lastApplied
	for i := rf.lastApplied + 1; i <= rf.getLastLogIndex(); i++ {
		entry := rf.getLogEntry(i)
		if entry != nil && rf.isConfigurationEntry(*entry) {
			return true
		}
	}
	return false
}

// waitForCommit waits for a log entry to be committed
func (rf *Raft) waitForCommit(index, term int) error {
	timeout := time.After(5 * time.Second)
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-timeout:
			return ErrTimeout
		case <-ticker.C:
			rf.mu.RLock()
			if rf.commitIndex >= index {
				entry := rf.getLogEntry(index)
				if entry != nil && entry.Term == term {
					rf.mu.RUnlock()
					return nil
				}
			}
			rf.mu.RUnlock()
		}
	}
}

// applyConfigurationChange applies a configuration change to the Raft state
// This function assumes the caller already holds the mutex
func (rf *Raft) applyConfigurationChange(change ConfigurationChange) {
	switch change.Type {
	case "joint":
		// Apply joint configuration
		if change.JointConfig != nil {
			rf.applyConfigurationLocked(*change.JointConfig)
			rf.inJointConsensus = true
		}
		log.Printf("Server %d applied joint configuration", rf.me)
		
	case "final":
		// Apply final configuration
		rf.applyConfigurationLocked(change.NewConfig)
		rf.inJointConsensus = false
		log.Printf("Server %d applied final configuration", rf.me)
		
		// If this server is not in the new configuration and is the leader,
		// delay stepping down to ensure configuration propagates
		if rf.state == Leader && !rf.isInConfiguration(change.NewConfig, rf.me) {
			// Schedule step down after a delay to allow configuration to propagate
			go func() {
				// Wait for configuration to propagate
				time.Sleep(500 * time.Millisecond)
				
				rf.mu.Lock()
				defer rf.mu.Unlock()
				
				// Check if we're still the leader before stepping down
				if rf.state == Leader {
					rf.state = Follower
					rf.stopElectionTimer()
					if rf.heartbeatTick != nil {
						rf.heartbeatTick.Stop()
					}
					rf.resetElectionTimer()
					log.Printf("Server %d stepped down as it's not in new configuration", rf.me)
				}
			}()
		}
		
	case AddServer, RemoveServer, AddNonVotingServer, PromoteServer:
		// Direct configuration changes (without joint consensus)
		rf.applyConfigurationLocked(change.NewConfig)
		log.Printf("Server %d applied %s configuration change", rf.me, change.Type)
		
		// If this server was removed and is the leader, step down
		if change.Type == RemoveServer && rf.state == Leader && !rf.isInConfiguration(change.NewConfig, rf.me) {
			rf.state = Follower
			rf.resetElectionTimer()
			if rf.heartbeatTick != nil {
				rf.heartbeatTick.Stop()
			}
			log.Printf("Server %d stepped down as it was removed from configuration", rf.me)
		}
	}
}

// waitForServerCatchUp waits for a server to catch up with the leader's log
func (rf *Raft) waitForServerCatchUp(serverID int) error {
	timeout := time.After(30 * time.Second) // Give server 30 seconds to catch up
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	
	rf.mu.RLock()
	targetIndex := rf.getLastLogIndex()
	rf.mu.RUnlock()
	
	for {
		select {
		case <-timeout:
			return fmt.Errorf("timeout waiting for server %d to catch up", serverID)
		case <-ticker.C:
			rf.mu.RLock()
			
			// Find the server's index in the peers array
			serverIndex := -1
			for i, peerID := range rf.peers {
				if peerID == serverID {
					serverIndex = i
					break
				}
			}
			
			if serverIndex >= 0 && serverIndex < len(rf.matchIndex) {
				// Check if server has caught up to within 10 entries of our target
				if rf.matchIndex[serverIndex] >= targetIndex-10 {
					rf.mu.RUnlock()
					return nil
				}
			}
			
			// Check if we're still the leader
			if rf.state != Leader {
				rf.mu.RUnlock()
				return fmt.Errorf("no longer the leader")
			}
			
			rf.mu.RUnlock()
		}
	}
}

// applyConfiguration applies a configuration to the Raft state
func (rf *Raft) applyConfiguration(config Configuration) {
	rf.mu.Lock()
	defer rf.mu.Unlock()
	rf.applyConfigurationLocked(config)
}

// applyConfigurationLocked applies a configuration to the Raft state
// This function assumes the caller already holds the mutex
func (rf *Raft) applyConfigurationLocked(config Configuration) {
	// Update the current configuration
	rf.currentConfig = config
	
	// Update peers list
	newPeers := make([]int, len(config.Servers))
	for i, server := range config.Servers {
		newPeers[i] = server.ID
	}
	rf.peers = newPeers

	// Resize leader state arrays if necessary
	if rf.state == Leader {
		oldLen := len(rf.nextIndex)
		newLen := len(newPeers)
		
		if newLen > oldLen {
			// Expand arrays
			newNextIndex := make([]int, newLen)
			newMatchIndex := make([]int, newLen)
			
			copy(newNextIndex, rf.nextIndex)
			copy(newMatchIndex, rf.matchIndex)
			
			// Initialize new entries
			for i := oldLen; i < newLen; i++ {
				newNextIndex[i] = rf.getLastLogIndex() + 1
				newMatchIndex[i] = 0
			}
			
			rf.nextIndex = newNextIndex
			rf.matchIndex = newMatchIndex
		} else if newLen < oldLen {
			// Shrink arrays
			rf.nextIndex = rf.nextIndex[:newLen]
			rf.matchIndex = rf.matchIndex[:newLen]
		}
	}
}

// isInConfiguration checks if a server is in the given configuration
func (rf *Raft) isInConfiguration(config Configuration, serverID int) bool {
	for _, server := range config.Servers {
		if server.ID == serverID {
			return true
		}
	}
	return false
}

// isVotingMember checks if a server is a voting member in the current configuration
func (rf *Raft) isVotingMember(serverID int) bool {
	for _, server := range rf.currentConfig.Servers {
		if server.ID == serverID {
			return !server.NonVoting
		}
	}
	return false
}

// getMajoritySize returns the majority size for the current configuration
func (rf *Raft) getMajoritySize() int {
	// Count only voting members
	votingCount := 0
	for _, server := range rf.currentConfig.Servers {
		if !server.NonVoting {
			votingCount++
		}
	}
	return votingCount/2 + 1
}

// isConfigurationEntry checks if a log entry is a configuration change
func (rf *Raft) isConfigurationEntry(entry LogEntry) bool {
	// Try to unmarshal as configuration change
	var change ConfigurationChange
	if data, ok := entry.Command.([]byte); ok {
		return json.Unmarshal(data, &change) == nil
	}
	if data, ok := entry.Command.(string); ok {
		return json.Unmarshal([]byte(data), &change) == nil
	}
	return false
}

// handleConfigurationEntry handles a configuration change entry
// This function assumes the caller already holds the mutex
func (rf *Raft) handleConfigurationEntry(entry LogEntry) {
	var change ConfigurationChange
	var err error
	
	if data, ok := entry.Command.([]byte); ok {
		err = json.Unmarshal(data, &change)
	} else if data, ok := entry.Command.(string); ok {
		err = json.Unmarshal([]byte(data), &change)
	} else {
		log.Printf("Server %d: invalid configuration entry format", rf.me)
		return
	}
	
	if err != nil {
		log.Printf("Server %d: failed to unmarshal configuration change: %v", rf.me, err)
		return
	}
	
	rf.applyConfigurationChange(change)
}