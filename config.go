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
	defer rf.mu.Unlock()

	if rf.state != Leader {
		return fmt.Errorf("only leader can add servers")
	}

	// Create new configuration
	currentConfig := rf.getCurrentConfiguration()
	
	// Check if server already exists
	for _, s := range currentConfig.Servers {
		if s.ID == server.ID {
			return fmt.Errorf("server %d already exists in configuration", server.ID)
		}
	}

	newConfig := Configuration{
		Servers: append(currentConfig.Servers, server),
	}

	// Use joint consensus for configuration change
	return rf.changeConfiguration(currentConfig, newConfig)
}

// RemoveServer removes a server from the cluster configuration
func (rf *Raft) RemoveServer(serverID int) error {
	rf.mu.Lock()
	defer rf.mu.Unlock()

	if rf.state != Leader {
		return fmt.Errorf("only leader can remove servers")
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
		return fmt.Errorf("server %d not found in configuration", serverID)
	}

	newConfig := Configuration{
		Servers: newServers,
	}

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
				if index > 0 && index <= len(rf.log) && rf.log[index-1].Term == term {
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
		// step down after committing the new configuration
		if rf.state == Leader && !rf.isInConfiguration(change.NewConfig, rf.me) {
			rf.state = Follower
			rf.stopElectionTimer()
			if rf.heartbeatTick != nil {
				rf.heartbeatTick.Stop()
			}
			log.Printf("Server %d stepped down as it's not in new configuration", rf.me)
		}
		
	case AddServer, RemoveServer, AddNonVotingServer, PromoteServer:
		// Direct configuration changes (without joint consensus)
		rf.applyConfigurationLocked(change.NewConfig)
		log.Printf("Server %d applied %s configuration change", rf.me, change.Type)
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
				newNextIndex[i] = len(rf.log)
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

// getMajoritySize returns the majority size for the current configuration
func (rf *Raft) getMajoritySize() int {
	return len(rf.peers)/2 + 1
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