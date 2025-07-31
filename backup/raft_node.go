package raft

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// Node represents a Raft node that coordinates all components
// This is the main entry point for users of the library
type Node struct {
	mu sync.RWMutex

	// Configuration
	id     int
	peers  []int
	config *Config

	// Core components
	state       *StateManager
	log         *LogManager
	election    *ElectionManager
	replication *ReplicationManager

	// External interfaces
	transport    Transport
	persistence  Persistence
	stateMachine StateMachine

	// Channels
	stopCh    chan struct{}
	commandCh chan commandRequest

	// RPC handler registration
	rpcHandler RPCHandler
}

// commandRequest represents a client command submission
type commandRequest struct {
	command  interface{}
	resultCh chan commandResult
}

// commandResult represents the result of a command submission
type commandResult struct {
	index    int
	term     int
	isLeader bool
}

// NewNode creates a new Raft node
func NewNode(config *Config, transport Transport, persistence Persistence, stateMachine StateMachine) (*Node, error) {
	if config == nil {
		return nil, fmt.Errorf("config cannot be nil")
	}
	if transport == nil {
		return nil, fmt.Errorf("transport cannot be nil")
	}
	if persistence == nil {
		return nil, fmt.Errorf("persistence cannot be nil")
	}
	if stateMachine == nil {
		return nil, fmt.Errorf("stateMachine cannot be nil")
	}

	// Initialize components
	stateManager := NewStateManager(config.ID, config)
	logManager := NewLogManager()

	// Create the node
	node := &Node{
		id:           config.ID,
		peers:        config.Peers,
		config:       config,
		state:        stateManager,
		log:          logManager,
		transport:    transport,
		persistence:  persistence,
		stateMachine: stateMachine,
		stopCh:       make(chan struct{}),
		commandCh:    make(chan commandRequest, 100),
	}

	// Create election and replication managers with node as transport
	node.election = NewElectionManager(config.ID, config.Peers, stateManager, logManager, node, config)
	node.replication = NewReplicationManager(config.ID, config.Peers, stateManager, logManager, node, config, stateMachine)

	// Set up state transition callbacks
	stateManager.SetOnBecomeLeader(func() {
		node.replication.BecomeLeader()
	})

	// Set RPC handler
	transport.SetRPCHandler(node)

	// Restore from persistence
	if err := node.restore(); err != nil {
		return nil, fmt.Errorf("failed to restore from persistence: %v", err)
	}

	return node, nil
}

// Start starts the Raft node
func (n *Node) Start(ctx context.Context) error {
	// Start transport
	if err := n.transport.Start(); err != nil {
		return fmt.Errorf("failed to start transport: %v", err)
	}

	// Start main event loop
	go n.run(ctx)

	if n.config.Logger != nil {
		n.config.Logger.Info("Raft node %d started", n.id)
	}

	return nil
}

// Stop gracefully shuts down the Raft node
func (n *Node) Stop() error {
	close(n.stopCh)
	n.state.Stop()

	if err := n.transport.Stop(); err != nil {
		return fmt.Errorf("failed to stop transport: %v", err)
	}

	if n.config.Logger != nil {
		n.config.Logger.Info("Raft node %d stopped", n.id)
	}

	return nil
}

// Submit submits a command to the Raft cluster
func (n *Node) Submit(command interface{}) (int, int, bool) {
	req := commandRequest{
		command:  command,
		resultCh: make(chan commandResult, 1),
	}

	select {
	case n.commandCh <- req:
		result := <-req.resultCh
		return result.index, result.term, result.isLeader
	case <-time.After(time.Second):
		return -1, -1, false
	}
}

// GetState returns current term and whether this server is the leader
func (n *Node) GetState() (int, bool) {
	state, term := n.state.GetState()
	return term, state == Leader
}

// AddServer adds a new server to the cluster (leader only)
func (n *Node) AddServer(server Server) error {
	state, _ := n.state.GetState()
	if state != Leader {
		return fmt.Errorf("not the leader")
	}

	// TODO: Implement configuration changes
	return fmt.Errorf("configuration changes not implemented yet")
}

// RemoveServer removes a server from the cluster (leader only)
func (n *Node) RemoveServer(serverID int) error {
	state, _ := n.state.GetState()
	if state != Leader {
		return fmt.Errorf("not the leader")
	}

	// TODO: Implement configuration changes
	return fmt.Errorf("configuration changes not implemented yet")
}

// TransferLeadership attempts to transfer leadership to another server
func (n *Node) TransferLeadership(targetServer int) error {
	state, _ := n.state.GetState()
	if state != Leader {
		return fmt.Errorf("not the leader")
	}

	// TODO: Implement leadership transfer
	return fmt.Errorf("leadership transfer not implemented yet")
}

// TakeSnapshot tells Raft to create a snapshot at the given index
func (n *Node) TakeSnapshot(index int) error {
	// Get snapshot from state machine
	data, err := n.stateMachine.Snapshot()
	if err != nil {
		return fmt.Errorf("failed to create snapshot: %v", err)
	}

	// Get term at index
	entry := n.log.GetEntry(index)
	if entry == nil {
		return fmt.Errorf("no entry at index %d", index)
	}

	// Create snapshot
	snapshot := &Snapshot{
		Data:              data,
		LastIncludedIndex: index,
		LastIncludedTerm:  entry.Term,
	}

	// Save to persistence
	if err := n.persistence.SaveSnapshot(snapshot); err != nil {
		return fmt.Errorf("failed to save snapshot: %v", err)
	}

	// Update log
	if err := n.log.CreateSnapshot(index, entry.Term); err != nil {
		return fmt.Errorf("failed to update log: %v", err)
	}

	return nil
}

// run is the main event loop
func (n *Node) run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-n.stopCh:
			return
		case <-n.state.GetElectionTimer():
			// Election timeout - start election
			state, _ := n.state.GetState()
			if state != Leader {
				n.election.StartElection()
			}
		case <-n.state.GetHeartbeatTicker():
			// Heartbeat interval - send heartbeats
			state, _ := n.state.GetState()
			if state == Leader {
				n.replication.SendHeartbeats()
			}
		case req := <-n.commandCh:
			// Handle command submission
			n.handleCommand(req)
		}
	}
}

// handleCommand processes a command submission
func (n *Node) handleCommand(req commandRequest) {
	state, term := n.state.GetState()

	if state != Leader {
		req.resultCh <- commandResult{
			index:    -1,
			term:     term,
			isLeader: false,
		}
		return
	}

	// Append to log
	index := n.log.AppendEntry(term, req.command)

	// Persist
	n.persist()

	// Start replication
	n.replication.Replicate()

	req.resultCh <- commandResult{
		index:    index,
		term:     term,
		isLeader: true,
	}
}

// persist saves state to persistence
func (n *Node) persist() {
	state := &PersistentState{
		CurrentTerm: n.state.GetTerm(),
		VotedFor:    n.state.GetVotedFor(),
		Log:         n.log.GetPersistentState(),
	}

	if err := n.persistence.SaveState(state); err != nil && n.config.Logger != nil {
		n.config.Logger.Error("Failed to persist state: %v", err)
	}
}

// restore loads state from persistence
func (n *Node) restore() error {
	// Restore state
	state, err := n.persistence.LoadState()
	if err != nil {
		return fmt.Errorf("failed to load state: %v", err)
	}

	if state != nil {
		n.state.SetTerm(state.CurrentTerm)
		if state.VotedFor != nil {
			n.state.Vote(*state.VotedFor)
		}

		// Restore log
		snapshotIndex, snapshotTerm := 0, 0
		if n.persistence.HasSnapshot() {
			snapshot, err := n.persistence.LoadSnapshot()
			if err != nil {
				return fmt.Errorf("failed to load snapshot: %v", err)
			}
			if snapshot != nil {
				snapshotIndex = snapshot.LastIncludedIndex
				snapshotTerm = snapshot.LastIncludedTerm

				// Restore state machine from snapshot
				if err := n.stateMachine.Restore(snapshot.Data); err != nil {
					return fmt.Errorf("failed to restore state machine: %v", err)
				}
			}
		}

		n.log.RestoreFromPersistence(state.Log, snapshotIndex, snapshotTerm)
	}

	return nil
}

// RPC Handlers (implement RPCHandler interface)

// RequestVote handles RequestVote RPC
func (n *Node) RequestVote(args *RequestVoteArgs, reply *RequestVoteReply) error {
	n.election.HandleRequestVote(args, reply)
	n.persist()
	return nil
}

// AppendEntries handles AppendEntries RPC
func (n *Node) AppendEntries(args *AppendEntriesArgs, reply *AppendEntriesReply) error {
	n.replication.HandleAppendEntries(args, reply)
	n.persist()
	return nil
}

// InstallSnapshot handles InstallSnapshot RPC
func (n *Node) InstallSnapshot(args *InstallSnapshotArgs, reply *InstallSnapshotReply) error {
	// TODO: Implement snapshot installation
	reply.Term = n.state.GetTerm()
	return fmt.Errorf("InstallSnapshot not implemented yet")
}

// Transport interface implementation (for internal use)

// SendRequestVote sends RequestVote RPC
func (n *Node) SendRequestVote(serverID int, args *RequestVoteArgs) (*RequestVoteReply, error) {
	return n.transport.SendRequestVote(serverID, args)
}

// SendAppendEntries sends AppendEntries RPC
func (n *Node) SendAppendEntries(serverID int, args *AppendEntriesArgs) (*AppendEntriesReply, error) {
	return n.transport.SendAppendEntries(serverID, args)
}

// SendInstallSnapshot sends InstallSnapshot RPC
func (n *Node) SendInstallSnapshot(serverID int, args *InstallSnapshotArgs) (*InstallSnapshotReply, error) {
	return n.transport.SendInstallSnapshot(serverID, args)
}
