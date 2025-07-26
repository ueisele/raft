package raft

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

// RPCTransport handles RPC communication between Raft servers
type RPCTransport struct {
	serverAddr string
	client     *http.Client
}

// NewRPCTransport creates a new RPC transport
func NewRPCTransport(serverAddr string) *RPCTransport {
	return &RPCTransport{
		serverAddr: serverAddr,
		client: &http.Client{
			Timeout: 5 * time.Second,
		},
	}
}

// SendRequestVote sends a RequestVote RPC to the specified server
func (t *RPCTransport) SendRequestVote(serverID int, args *RequestVoteArgs, reply *RequestVoteReply) error {
	url := fmt.Sprintf("http://localhost:%d/requestvote", 8000+serverID)
	return t.sendRPC(url, args, reply)
}

// SendAppendEntries sends an AppendEntries RPC to the specified server
func (t *RPCTransport) SendAppendEntries(serverID int, args *AppendEntriesArgs, reply *AppendEntriesReply) error {
	url := fmt.Sprintf("http://localhost:%d/appendentries", 8000+serverID)
	return t.sendRPC(url, args, reply)
}

// SendInstallSnapshot sends an InstallSnapshot RPC to the specified server
func (t *RPCTransport) SendInstallSnapshot(serverID int, args *InstallSnapshotArgs, reply *InstallSnapshotReply) error {
	url := fmt.Sprintf("http://localhost:%d/installsnapshot", 8000+serverID)
	return t.sendRPC(url, args, reply)
}

// sendRPC sends a generic RPC request
func (t *RPCTransport) sendRPC(url string, args interface{}, reply interface{}) error {
	jsonData, err := json.Marshal(args)
	if err != nil {
		return err
	}

	resp, err := t.client.Post(url, "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("RPC failed with status code: %d", resp.StatusCode)
	}

	return json.NewDecoder(resp.Body).Decode(reply)
}

// RPCServer provides HTTP endpoints for Raft RPC calls
type RPCServer struct {
	raft *Raft
	port int
}

// NewRPCServer creates a new RPC server
func NewRPCServer(raft *Raft, port int) *RPCServer {
	return &RPCServer{
		raft: raft,
		port: port,
	}
}

// Start starts the RPC server
func (s *RPCServer) Start() error {
	mux := http.NewServeMux()
	mux.HandleFunc("/requestvote", s.handleRequestVote)
	mux.HandleFunc("/appendentries", s.handleAppendEntries)
	mux.HandleFunc("/installsnapshot", s.handleInstallSnapshot)
	mux.HandleFunc("/submit", s.handleSubmit)
	mux.HandleFunc("/status", s.handleStatus)

	addr := ":" + strconv.Itoa(s.port)
	return http.ListenAndServe(addr, mux)
}

// handleRequestVote handles RequestVote RPC calls
func (s *RPCServer) handleRequestVote(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var args RequestVoteArgs
	if err := json.NewDecoder(r.Body).Decode(&args); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	var reply RequestVoteReply
	if err := s.raft.RequestVote(&args, &reply); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(reply)
}

// handleAppendEntries handles AppendEntries RPC calls
func (s *RPCServer) handleAppendEntries(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var args AppendEntriesArgs
	if err := json.NewDecoder(r.Body).Decode(&args); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	var reply AppendEntriesReply
	if err := s.raft.AppendEntries(&args, &reply); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(reply)
}

// handleInstallSnapshot handles InstallSnapshot RPC calls
func (s *RPCServer) handleInstallSnapshot(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var args InstallSnapshotArgs
	if err := json.NewDecoder(r.Body).Decode(&args); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	var reply InstallSnapshotReply
	if err := s.raft.InstallSnapshot(&args, &reply); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(reply)
}

// handleSubmit handles client command submission
func (s *RPCServer) handleSubmit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var command interface{}
	if err := json.NewDecoder(r.Body).Decode(&command); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	index, term, isLeader := s.raft.Submit(command)

	response := map[string]interface{}{
		"index":    index,
		"term":     term,
		"isLeader": isLeader,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// handleStatus handles status requests
func (s *RPCServer) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	term, isLeader := s.raft.GetState()

	s.raft.mu.RLock()
	status := map[string]interface{}{
		"id":          s.raft.me,
		"term":        term,
		"isLeader":    isLeader,
		"state":       s.raft.state.String(),
		"logLength":   len(s.raft.log),
		"commitIndex": s.raft.commitIndex,
		"lastApplied": s.raft.lastApplied,
	}
	s.raft.mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(status)
}