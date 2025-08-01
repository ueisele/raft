package raft

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// ConfigurationManager handles cluster membership changes
// Implements Section 6 of the Raft paper
type ConfigurationManager struct {
	mu sync.RWMutex

	// Current configuration
	currentConfig *ClusterConfiguration

	// Pending configuration change (if any)
	pendingConfig *PendingConfigChange

	// Dependencies
	node Node
}

// ClusterConfiguration represents the current cluster membership
type ClusterConfiguration struct {
	Servers []ServerConfig `json:"servers"`
	Index   int            `json:"index"` // Log index where this config was committed
}

// ServerConfig represents a server in the cluster
type ServerConfig struct {
	ID      int    `json:"id"`
	Address string `json:"address"`
	Voting  bool   `json:"voting"`
}

// PendingConfigChange represents a pending configuration change
type PendingConfigChange struct {
	Type      ConfigChangeType      `json:"type"`
	Server    ServerConfig          `json:"server"`
	OldConfig *ClusterConfiguration `json:"old_config"`
	NewConfig *ClusterConfiguration `json:"new_config"`
	StartTime int64                 `json:"start_time"`
}

// ConfigChangeType represents the type of configuration change
type ConfigChangeType string

const (
	ConfigChangeAddServer    ConfigChangeType = "add_server"
	ConfigChangeRemoveServer ConfigChangeType = "remove_server"
)

// NewConfigurationManager creates a new configuration manager
func NewConfigurationManager(initialPeers []int) *ConfigurationManager {
	servers := make([]ServerConfig, len(initialPeers))
	for i, id := range initialPeers {
		servers[i] = ServerConfig{
			ID:      id,
			Address: fmt.Sprintf("node-%d", id),
			Voting:  true,
		}
	}

	return &ConfigurationManager{
		currentConfig: &ClusterConfiguration{
			Servers: servers,
			Index:   0,
		},
	}
}

// GetConfiguration returns the current cluster configuration
func (cm *ConfigurationManager) GetConfiguration() *ClusterConfiguration {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	// Return a copy
	config := &ClusterConfiguration{
		Servers: make([]ServerConfig, len(cm.currentConfig.Servers)),
		Index:   cm.currentConfig.Index,
	}
	copy(config.Servers, cm.currentConfig.Servers)
	return config
}

// GetVotingMembers returns the list of voting member IDs
func (cm *ConfigurationManager) GetVotingMembers() []int {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	var members []int
	for _, server := range cm.currentConfig.Servers {
		if server.Voting {
			members = append(members, server.ID)
		}
	}
	return members
}

// IsMember checks if a server is a member of the current configuration
func (cm *ConfigurationManager) IsMember(serverID int) bool {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	for _, server := range cm.currentConfig.Servers {
		if server.ID == serverID {
			return true
		}
	}
	return false
}

// IsVotingMember checks if a server is a voting member
func (cm *ConfigurationManager) IsVotingMember(serverID int) bool {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	for _, server := range cm.currentConfig.Servers {
		if server.ID == serverID && server.Voting {
			return true
		}
	}
	return false
}

// StartAddServer initiates adding a new server to the cluster
// This is a simplified version without joint consensus
func (cm *ConfigurationManager) StartAddServer(server ServerConfig) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	// Check if server already exists in current configuration
	for _, s := range cm.currentConfig.Servers {
		if s.ID == server.ID {
			return fmt.Errorf("server %d already exists", server.ID)
		}
	}

	// Check if server exists in pending configuration
	if cm.pendingConfig != nil {
		// Check if we're trying to add the same server that's already pending
		if cm.pendingConfig.Type == ConfigChangeAddServer && cm.pendingConfig.Server.ID == server.ID {
			return fmt.Errorf("server %d already being added", server.ID)
		}

		// Check if server exists in the pending new configuration
		for _, s := range cm.pendingConfig.NewConfig.Servers {
			if s.ID == server.ID {
				return fmt.Errorf("server %d already exists in pending configuration", server.ID)
			}
		}

		return fmt.Errorf("configuration change already in progress")
	}

	// Create new configuration
	newServers := make([]ServerConfig, len(cm.currentConfig.Servers)+1)
	copy(newServers, cm.currentConfig.Servers)
	newServers[len(cm.currentConfig.Servers)] = server

	newConfig := &ClusterConfiguration{
		Servers: newServers,
		Index:   0, // Will be set when committed
	}

	// Record pending change
	cm.pendingConfig = &PendingConfigChange{
		Type:      ConfigChangeAddServer,
		Server:    server,
		OldConfig: cm.currentConfig,
		NewConfig: newConfig,
		StartTime: time.Now().UnixNano(),
	}

	return nil
}

// StartRemoveServer initiates removing a server from the cluster
func (cm *ConfigurationManager) StartRemoveServer(serverID int) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	// Check if server exists
	found := false
	var serverToRemove ServerConfig
	for _, s := range cm.currentConfig.Servers {
		if s.ID == serverID {
			found = true
			serverToRemove = s
			break
		}
	}

	if !found {
		return fmt.Errorf("server %d not found in configuration", serverID)
	}

	// Check if a configuration change is already in progress
	if cm.pendingConfig != nil {
		return fmt.Errorf("configuration change already in progress")
	}

	// Check if removing this server would leave no voting members
	votingCount := 0
	for _, s := range cm.currentConfig.Servers {
		if s.Voting && s.ID != serverID {
			votingCount++
		}
	}

	if votingCount == 0 {
		return fmt.Errorf("cannot remove last voting member")
	}

	// Create new configuration without the server
	newServers := make([]ServerConfig, 0, len(cm.currentConfig.Servers)-1)
	for _, s := range cm.currentConfig.Servers {
		if s.ID != serverID {
			newServers = append(newServers, s)
		}
	}

	newConfig := &ClusterConfiguration{
		Servers: newServers,
		Index:   0, // Will be set when committed
	}

	// Record pending change
	cm.pendingConfig = &PendingConfigChange{
		Type:      ConfigChangeRemoveServer,
		Server:    serverToRemove,
		OldConfig: cm.currentConfig,
		NewConfig: newConfig,
		StartTime: time.Now().UnixNano(),
	}

	return nil
}

// GetPendingConfigChange returns the pending configuration change (if any)
func (cm *ConfigurationManager) GetPendingConfigChange() *PendingConfigChange {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	return cm.pendingConfig
}

// CommitConfiguration commits a configuration change
func (cm *ConfigurationManager) CommitConfiguration(index int) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if cm.pendingConfig == nil {
		return fmt.Errorf("no pending configuration change")
	}

	// Update the configuration
	cm.currentConfig = cm.pendingConfig.NewConfig
	cm.currentConfig.Index = index
	cm.pendingConfig = nil

	return nil
}

// ApplyCommittedConfiguration applies a configuration that has been committed
// This is used by followers who receive the configuration through replication
func (cm *ConfigurationManager) ApplyCommittedConfiguration(config *ClusterConfiguration, index int) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	// Update the configuration
	cm.currentConfig = config
	cm.currentConfig.Index = index

	// Clear any pending configuration since we're applying a committed one
	cm.pendingConfig = nil
}

// CancelPendingChange cancels any pending configuration change
func (cm *ConfigurationManager) CancelPendingChange() {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	cm.pendingConfig = nil
}

// MarshalConfigChange marshals a configuration change to JSON
func MarshalConfigChange(change *PendingConfigChange) ([]byte, error) {
	return json.Marshal(change)
}

// UnmarshalConfigChange unmarshals a configuration change from JSON
func UnmarshalConfigChange(data []byte) (*PendingConfigChange, error) {
	var change PendingConfigChange
	err := json.Unmarshal(data, &change)
	return &change, err
}
