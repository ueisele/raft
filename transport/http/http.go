package http

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	raft "github.com/ueisele/raft"
	"github.com/ueisele/raft/transport"
)

// HTTPTransport implements raft.Transport interface using HTTP
type HTTPTransport struct {
	serverID   int
	address    string
	httpClient *http.Client
	httpServer *http.Server
	handler    raft.RPCHandler
	mux        *http.ServeMux
}

// NewHTTPTransport creates a new HTTP transport
func NewHTTPTransport(config *transport.Config) *HTTPTransport {
	return &HTTPTransport{
		serverID: config.ServerID,
		address:  config.Address,
		httpClient: &http.Client{
			Timeout: time.Duration(config.RPCTimeout) * time.Millisecond,
		},
	}
}

// SetRPCHandler sets the RPC handler for incoming requests
func (t *HTTPTransport) SetRPCHandler(handler raft.RPCHandler) {
	t.handler = handler
}

// Start starts the HTTP server
func (t *HTTPTransport) Start() error {
	if t.handler == nil {
		return fmt.Errorf("RPC handler not set")
	}

	// Set up HTTP routes
	t.mux = http.NewServeMux()
	t.mux.HandleFunc("/raft/requestvote", t.handleRequestVote)
	t.mux.HandleFunc("/raft/appendentries", t.handleAppendEntries)
	t.mux.HandleFunc("/raft/installsnapshot", t.handleInstallSnapshot)

	t.httpServer = &http.Server{
		Addr:    t.address,
		Handler: t.mux,
	}

	// Start server in background
	go func() {
		if err := t.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			// Log error but don't crash
			fmt.Printf("HTTP server error: %v\n", err)
		}
	}()

	return nil
}

// Stop stops the HTTP server
func (t *HTTPTransport) Stop() error {
	if t.httpServer != nil {
		return t.httpServer.Close()
	}
	return nil
}

// GetAddress returns the address this transport is listening on
func (t *HTTPTransport) GetAddress() string {
	return t.address
}

// SendRequestVote sends a RequestVote RPC
func (t *HTTPTransport) SendRequestVote(serverID int, args *raft.RequestVoteArgs) (*raft.RequestVoteReply, error) {
	url := fmt.Sprintf("http://%s/raft/requestvote", t.getServerAddress(serverID))

	var reply raft.RequestVoteReply
	if err := t.sendRPC(url, args, &reply); err != nil {
		return nil, &transport.TransportError{ServerID: serverID, Err: err}
	}

	return &reply, nil
}

// SendAppendEntries sends an AppendEntries RPC
func (t *HTTPTransport) SendAppendEntries(serverID int, args *raft.AppendEntriesArgs) (*raft.AppendEntriesReply, error) {
	url := fmt.Sprintf("http://%s/raft/appendentries", t.getServerAddress(serverID))

	var reply raft.AppendEntriesReply
	if err := t.sendRPC(url, args, &reply); err != nil {
		return nil, &transport.TransportError{ServerID: serverID, Err: err}
	}

	return &reply, nil
}

// SendInstallSnapshot sends an InstallSnapshot RPC
func (t *HTTPTransport) SendInstallSnapshot(serverID int, args *raft.InstallSnapshotArgs) (*raft.InstallSnapshotReply, error) {
	url := fmt.Sprintf("http://%s/raft/installsnapshot", t.getServerAddress(serverID))

	var reply raft.InstallSnapshotReply
	if err := t.sendRPC(url, args, &reply); err != nil {
		return nil, &transport.TransportError{ServerID: serverID, Err: err}
	}

	return &reply, nil
}

// sendRPC sends a generic RPC request
func (t *HTTPTransport) sendRPC(url string, args interface{}, reply interface{}) error {
	jsonData, err := json.Marshal(args)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %v", err)
	}

	resp, err := t.httpClient.Post(url, "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("failed to send request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("RPC failed with status %d", resp.StatusCode)
	}

	if err := json.NewDecoder(resp.Body).Decode(reply); err != nil {
		return fmt.Errorf("failed to decode response: %v", err)
	}

	return nil
}

// HTTP handlers for incoming RPCs

func (t *HTTPTransport) handleRequestVote(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var args raft.RequestVoteArgs
	if err := json.NewDecoder(r.Body).Decode(&args); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	var reply raft.RequestVoteReply
	if err := t.handler.RequestVote(&args, &reply); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(reply)
}

func (t *HTTPTransport) handleAppendEntries(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var args raft.AppendEntriesArgs
	if err := json.NewDecoder(r.Body).Decode(&args); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	var reply raft.AppendEntriesReply
	if err := t.handler.AppendEntries(&args, &reply); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(reply)
}

func (t *HTTPTransport) handleInstallSnapshot(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var args raft.InstallSnapshotArgs
	if err := json.NewDecoder(r.Body).Decode(&args); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	var reply raft.InstallSnapshotReply
	if err := t.handler.InstallSnapshot(&args, &reply); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(reply)
}

// getServerAddress returns the address for a given server ID
// In a real implementation, this would use a configuration or discovery service
func (t *HTTPTransport) getServerAddress(serverID int) string {
	// For now, use a simple port mapping
	port := 8000 + serverID
	return "localhost:" + strconv.Itoa(port)
}
