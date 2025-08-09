package http

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
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
	discovery  transport.PeerDiscovery
	rpcTimeout time.Duration
}

// NewHTTPTransport creates a new HTTP transport with required peer discovery
func NewHTTPTransport(config *transport.Config, discovery transport.PeerDiscovery) (*HTTPTransport, error) {
	if discovery == nil {
		return nil, fmt.Errorf("discovery cannot be nil")
	}

	// Create HTTP client with custom transport to handle timeouts properly
	httpClient := &http.Client{
		Transport: &http.Transport{
			// Use default transport settings but with better connection pooling
			MaxIdleConns:        100,
			MaxIdleConnsPerHost: 10,
			IdleConnTimeout:     90 * time.Second,
		},
	}

	// Use default timeout if not specified
	rpcTimeout := time.Duration(config.RPCTimeout) * time.Millisecond
	if rpcTimeout == 0 {
		rpcTimeout = 5 * time.Second // Default to 5 seconds
	}

	return &HTTPTransport{
		serverID:   config.ServerID,
		address:    config.Address,
		httpClient: httpClient,
		discovery:  discovery,
		rpcTimeout: rpcTimeout,
	}, nil
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

	// Create listener first to ensure we can bind to the address
	listener, err := net.Listen("tcp", t.address)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", t.address, err)
	}

	// Copy server reference for goroutine to avoid race
	server := t.httpServer

	// Channel to signal server is ready
	ready := make(chan struct{})

	// Start server in background
	go func() {
		// Signal that we're starting to serve
		close(ready)
		
		// Start serving (this blocks until server stops)
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			// Log error but don't crash
			fmt.Printf("HTTP server error: %v\n", err)
		}
	}()

	// Wait for server to start accepting connections
	<-ready
	
	// Add a small delay to ensure the OS has fully registered the listener
	// This helps with "connection refused" errors during simultaneous startup
	time.Sleep(10 * time.Millisecond)

	return nil
}

// Stop stops the HTTP server
func (t *HTTPTransport) Stop() error {
	var errs []error

	// Stop accepting new connections
	if t.httpServer != nil {
		if err := t.httpServer.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close http server: %w", err))
		}
	}

	// Close idle connections on the client
	if t.httpClient != nil {
		t.httpClient.CloseIdleConnections()
	}

	return errors.Join(errs...)
}

// GetAddress returns the address this transport is listening on
func (t *HTTPTransport) GetAddress() string {
	return t.address
}

// SendRequestVote sends a RequestVote RPC
func (t *HTTPTransport) SendRequestVote(serverID int, args *raft.RequestVoteArgs) (*raft.RequestVoteReply, error) {
	addr, err := t.getServerAddress(serverID)
	if err != nil {
		return nil, &transport.TransportError{ServerID: serverID, Err: err}
	}

	url := fmt.Sprintf("http://%s/raft/requestvote", addr)

	// Create context with timeout based on configured RPC timeout
	ctx, cancel := context.WithTimeout(context.Background(), t.rpcTimeout)
	defer cancel()

	var reply raft.RequestVoteReply
	if err := t.sendRPC(ctx, url, args, &reply); err != nil {
		return nil, &transport.TransportError{ServerID: serverID, Err: err}
	}

	return &reply, nil
}

// SendAppendEntries sends an AppendEntries RPC
func (t *HTTPTransport) SendAppendEntries(serverID int, args *raft.AppendEntriesArgs) (*raft.AppendEntriesReply, error) {
	addr, err := t.getServerAddress(serverID)
	if err != nil {
		return nil, &transport.TransportError{ServerID: serverID, Err: err}
	}

	url := fmt.Sprintf("http://%s/raft/appendentries", addr)

	// Create context with timeout based on configured RPC timeout
	ctx, cancel := context.WithTimeout(context.Background(), t.rpcTimeout)
	defer cancel()

	var reply raft.AppendEntriesReply
	if err := t.sendRPC(ctx, url, args, &reply); err != nil {
		return nil, &transport.TransportError{ServerID: serverID, Err: err}
	}

	return &reply, nil
}

// SendInstallSnapshot sends an InstallSnapshot RPC
func (t *HTTPTransport) SendInstallSnapshot(serverID int, args *raft.InstallSnapshotArgs) (*raft.InstallSnapshotReply, error) {
	addr, err := t.getServerAddress(serverID)
	if err != nil {
		return nil, &transport.TransportError{ServerID: serverID, Err: err}
	}

	url := fmt.Sprintf("http://%s/raft/installsnapshot", addr)

	// Create context with timeout based on configured RPC timeout
	ctx, cancel := context.WithTimeout(context.Background(), t.rpcTimeout)
	defer cancel()

	var reply raft.InstallSnapshotReply
	if err := t.sendRPC(ctx, url, args, &reply); err != nil {
		return nil, &transport.TransportError{ServerID: serverID, Err: err}
	}

	return &reply, nil
}

// sendRPC sends a generic RPC request with context support and retry logic
func (t *HTTPTransport) sendRPC(ctx context.Context, url string, args interface{}, reply interface{}) error {
	jsonData, err := json.Marshal(args)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	// Retry logic for connection failures during startup
	var resp *http.Response
	maxRetries := 3
	for attempt := 0; attempt < maxRetries; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(jsonData))
		if err != nil {
			return fmt.Errorf("failed to create request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err = t.httpClient.Do(req)
		if err == nil {
			break // Success
		}
		
		// Check if it's a connection error and we should retry
		if attempt < maxRetries-1 && isRetriableError(err) {
			// Exponential backoff: 10ms, 20ms, 40ms
			backoff := time.Duration(10<<uint(attempt)) * time.Millisecond
			select {
			case <-time.After(backoff):
				continue // Retry
			case <-ctx.Done():
				return fmt.Errorf("context cancelled during retry: %w", ctx.Err())
			}
		}
		
		return fmt.Errorf("failed to send request: %w", err)
	}
	
	if resp == nil {
		return fmt.Errorf("no response after %d retries", maxRetries)
	}
	defer resp.Body.Close() //nolint:errcheck // read-only operation

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("RPC failed with status %d", resp.StatusCode)
	}

	if err := json.NewDecoder(resp.Body).Decode(reply); err != nil {
		return fmt.Errorf("failed to decode response: %w", err)
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
	json.NewEncoder(w).Encode(reply) //nolint:errcheck // best effort response encoding
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
	json.NewEncoder(w).Encode(reply) //nolint:errcheck // best effort response encoding
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
	json.NewEncoder(w).Encode(reply) //nolint:errcheck // best effort response encoding
}

// getServerAddress returns the address for a given server ID
func (t *HTTPTransport) getServerAddress(serverID int) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	addr, err := t.discovery.GetPeerAddress(ctx, serverID)
	if err != nil {
		return "", fmt.Errorf("failed to get address for server %d: %w", serverID, err)
	}
	return addr, nil
}

// isRetriableError checks if an error is retriable (e.g., connection refused during startup)
func isRetriableError(err error) bool {
	if err == nil {
		return false
	}
	
	errStr := err.Error()
	
	// Connection errors that might happen during startup
	retriableErrors := []string{
		"connection refused",
		"no such host",
		"connection reset",
		"broken pipe",
		"network is unreachable",
	}
	
	for _, retriable := range retriableErrors {
		if strings.Contains(errStr, retriable) {
			return true
		}
	}
	
	return false
}
