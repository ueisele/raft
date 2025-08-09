package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gorilla/mux"
	"github.com/ueisele/raft"
	"github.com/ueisele/raft/persistence"
	jsonPersistence "github.com/ueisele/raft/persistence/json"
	"github.com/ueisele/raft/transport"
	httpTransport "github.com/ueisele/raft/transport/http"
)

// Version information
const Version = "1.0.0"

// Command types for the KV store
type CommandType string

const (
	SetCommand    CommandType = "set"
	DeleteCommand CommandType = "delete"
)

// LogLevel represents the logging level
type LogLevel int

const (
	LogLevelDebug LogLevel = iota
	LogLevelInfo
	LogLevelWarn
	LogLevelError
)

// SimpleLogger implements the raft.Logger interface with configurable log level
type SimpleLogger struct {
	level LogLevel
}

// NewSimpleLogger creates a logger with the specified level
func NewSimpleLogger(level LogLevel) *SimpleLogger {
	return &SimpleLogger{level: level}
}

func (l *SimpleLogger) Debug(format string, args ...interface{}) {
	if l.level <= LogLevelDebug {
		log.Printf("[DEBUG] "+format, args...)
	}
}

func (l *SimpleLogger) Info(format string, args ...interface{}) {
	if l.level <= LogLevelInfo {
		log.Printf("[INFO] "+format, args...)
	}
}

func (l *SimpleLogger) Warn(format string, args ...interface{}) {
	if l.level <= LogLevelWarn {
		log.Printf("[WARN] "+format, args...)
	}
}

func (l *SimpleLogger) Error(format string, args ...interface{}) {
	if l.level <= LogLevelError {
		log.Printf("[ERROR] "+format, args...)
	}
}

// Command represents a state machine operation
type Command struct {
	Type      CommandType `json:"type"`
	Key       string      `json:"key"`
	Value     string      `json:"value,omitempty"`
	Timestamp time.Time   `json:"timestamp"`
}

// ApplyResult represents the result of applying a command
type ApplyResult struct {
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
	Version int64  `json:"version,omitempty"`
}

// KVStore implements the StateMachine interface
type KVStore struct {
	mu       sync.RWMutex
	data     map[string]string
	versions map[string]int64
	metadata map[string]time.Time

	// Track applied entries for synchronous operations
	appliedMu      sync.Mutex
	appliedEntries map[int]chan *ApplyResult // index -> result channel
}

// NewKVStore creates a new KV store state machine
func NewKVStore() *KVStore {
	return &KVStore{
		data:           make(map[string]string),
		versions:       make(map[string]int64),
		metadata:       make(map[string]time.Time),
		appliedEntries: make(map[int]chan *ApplyResult),
	}
}

// Apply executes a command from the Raft log
func (kv *KVStore) Apply(entry raft.LogEntry) interface{} {
	var result *ApplyResult

	// Handle command based on its type
	switch cmd := entry.Command.(type) {
	case Command:
		// Direct Command struct
		result = kv.applyCommand(cmd)
	case map[string]interface{}:
		// Parse JSON command (for persistence recovery)
		var command Command

		// Convert map to Command struct
		if cmdType, ok := cmd["type"].(string); ok {
			command.Type = CommandType(cmdType)
		}
		if key, ok := cmd["key"].(string); ok {
			command.Key = key
		}
		if value, ok := cmd["value"].(string); ok {
			command.Value = value
		}
		if timestamp, ok := cmd["timestamp"].(string); ok {
			command.Timestamp, _ = time.Parse(time.RFC3339, timestamp)
		}

		result = kv.applyCommand(command)
	default:
		result = &ApplyResult{
			Success: false,
			Error:   "invalid command format",
		}
	}

	// Notify any waiting goroutine
	kv.appliedMu.Lock()
	if ch, exists := kv.appliedEntries[entry.Index]; exists {
		// Send result and close channel
		// Use non-blocking send in case the waiter timed out
		select {
		case ch <- result:
			// Successfully sent, close the channel
			close(ch)
		default:
			// Waiter timed out, just close without sending
			close(ch)
		}
		delete(kv.appliedEntries, entry.Index)
	}
	kv.appliedMu.Unlock()

	// Return as interface{} for Raft compatibility
	return result
}

func (kv *KVStore) applyCommand(cmd Command) *ApplyResult {
	kv.mu.Lock()
	defer kv.mu.Unlock()

	switch cmd.Type {
	case SetCommand:
		kv.data[cmd.Key] = cmd.Value
		kv.versions[cmd.Key]++
		kv.metadata[cmd.Key] = cmd.Timestamp

		return &ApplyResult{
			Success: true,
			Version: kv.versions[cmd.Key],
		}

	case DeleteCommand:
		if _, exists := kv.data[cmd.Key]; !exists {
			return &ApplyResult{
				Success: false,
				Error:   "key not found",
			}
		}

		delete(kv.data, cmd.Key)
		delete(kv.versions, cmd.Key)
		delete(kv.metadata, cmd.Key)

		return &ApplyResult{
			Success: true,
		}

	default:
		return &ApplyResult{
			Success: false,
			Error:   "unknown command type",
		}
	}
}

// Snapshot creates a point-in-time backup
func (kv *KVStore) Snapshot() ([]byte, error) {
	kv.mu.RLock()
	defer kv.mu.RUnlock()

	snapshot := struct {
		Data     map[string]string    `json:"data"`
		Versions map[string]int64     `json:"versions"`
		Metadata map[string]time.Time `json:"metadata"`
	}{
		Data:     make(map[string]string),
		Versions: make(map[string]int64),
		Metadata: make(map[string]time.Time),
	}

	// Deep copy the data
	for k, v := range kv.data {
		snapshot.Data[k] = v
	}
	for k, v := range kv.versions {
		snapshot.Versions[k] = v
	}
	for k, v := range kv.metadata {
		snapshot.Metadata[k] = v
	}

	return json.Marshal(snapshot)
}

// Restore loads from snapshot
func (kv *KVStore) Restore(data []byte) error {
	kv.mu.Lock()
	defer kv.mu.Unlock()

	var snapshot struct {
		Data     map[string]string    `json:"data"`
		Versions map[string]int64     `json:"versions"`
		Metadata map[string]time.Time `json:"metadata"`
	}

	if err := json.Unmarshal(data, &snapshot); err != nil {
		return fmt.Errorf("unmarshal snapshot: %w", err)
	}

	kv.data = snapshot.Data
	kv.versions = snapshot.Versions
	kv.metadata = snapshot.Metadata

	if kv.data == nil {
		kv.data = make(map[string]string)
	}
	if kv.versions == nil {
		kv.versions = make(map[string]int64)
	}
	if kv.metadata == nil {
		kv.metadata = make(map[string]time.Time)
	}

	return nil
}

// Get retrieves a value (not part of StateMachine interface, for direct reads)
func (kv *KVStore) Get(key string) (string, int64, time.Time, bool) {
	kv.mu.RLock()
	defer kv.mu.RUnlock()

	value, exists := kv.data[key]
	if !exists {
		return "", 0, time.Time{}, false
	}

	return value, kv.versions[key], kv.metadata[key], true
}

// List returns all keys with optional prefix filter
func (kv *KVStore) List(prefix string, limit int) []string {
	kv.mu.RLock()
	defer kv.mu.RUnlock()

	keys := make([]string, 0)
	for key := range kv.data {
		if prefix == "" || strings.HasPrefix(key, prefix) {
			keys = append(keys, key)
			if limit > 0 && len(keys) >= limit {
				break
			}
		}
	}

	return keys
}

// Stats returns store statistics
func (kv *KVStore) Stats() (int, int64) {
	kv.mu.RLock()
	defer kv.mu.RUnlock()

	count := len(kv.data)
	var size int64
	for k, v := range kv.data {
		size += int64(len(k) + len(v))
	}

	return count, size
}

// WaitForApplied waits for a specific log index to be applied
func (kv *KVStore) WaitForApplied(index int, timeout time.Duration) (*ApplyResult, error) {
	// Create a channel to wait on
	ch := make(chan *ApplyResult, 1)

	kv.appliedMu.Lock()
	kv.appliedEntries[index] = ch
	kv.appliedMu.Unlock()

	// Wait for result or timeout
	select {
	case result := <-ch:
		return result, nil
	case <-time.After(timeout):
		// Clean up on timeout
		kv.appliedMu.Lock()
		delete(kv.appliedEntries, index)
		kv.appliedMu.Unlock()
		return nil, fmt.Errorf("timeout waiting for index %d to be applied", index)
	}
}

// Server represents the KV store server
type Server struct {
	nodeID      int
	raftNode    raft.Node
	kvStore     *KVStore
	apiListener string
	apiPeers    map[int]string
	raftPeers   map[int]string
	startTime   time.Time
}

// LeaderRedirect response structure
type LeaderRedirect struct {
	Error     string `json:"error"`
	LeaderID  int    `json:"leader_id"`
	LeaderURL string `json:"leader_url"`
}

// enforceLeaderOnly checks if current node is leader and redirects if not
func (s *Server) enforceLeaderOnly(w http.ResponseWriter, r *http.Request) bool {
	isLeader := s.raftNode.IsLeader()
	if !isLeader {
		leaderID := s.raftNode.GetLeader()

		if leaderID == -1 {
			http.Error(w, `{"error":"no leader elected"}`, http.StatusServiceUnavailable)
			return false
		}

		// Get leader's API address
		leaderAPIAddr := s.apiPeers[leaderID]
		leaderURL := fmt.Sprintf("http://%s%s", leaderAPIAddr, r.URL.Path)
		if r.URL.RawQuery != "" {
			leaderURL += "?" + r.URL.RawQuery
		}

		// Set redirect headers
		w.Header().Set("Location", leaderURL)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusPermanentRedirect) // 308

		// Include JSON body with redirect information
		_ = json.NewEncoder(w).Encode(LeaderRedirect{
			Error:     "not_leader",
			LeaderID:  leaderID,
			LeaderURL: fmt.Sprintf("http://%s", leaderAPIAddr),
		})

		return false
	}
	return true
}

// handlePut handles PUT /kv/{key}
func (s *Server) handlePut(w http.ResponseWriter, r *http.Request) {
	if !s.enforceLeaderOnly(w, r) {
		return
	}

	vars := mux.Vars(r)
	key := vars["key"]

	// Validate key size
	if len(key) > 1024 {
		http.Error(w, `{"error":"key too large (max 1KB)"}`, http.StatusBadRequest)
		return
	}

	// Parse request body
	var req struct {
		Value string `json:"value"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}

	// Validate value size
	if len(req.Value) > 1024*1024 {
		http.Error(w, `{"error":"value too large (max 1MB)"}`, http.StatusRequestEntityTooLarge)
		return
	}

	// Create command
	cmd := Command{
		Type:      SetCommand,
		Key:       key,
		Value:     req.Value,
		Timestamp: time.Now(),
	}

	// Submit to Raft
	index, term, isLeader := s.raftNode.Submit(cmd)

	if !isLeader {
		// This should not happen since we already checked, but be safe
		http.Error(w, `{"error":"not leader"}`, http.StatusServiceUnavailable)
		return
	}

	// Wait for the command to be applied (with 10 second timeout)
	result, err := s.kvStore.WaitForApplied(index, 10*time.Second)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"operation timeout: %v"}`, err), http.StatusRequestTimeout)
		return
	}

	// Check if the operation was successful
	if !result.Success {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, result.Error), http.StatusInternalServerError)
		return
	}

	// Return success response
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"key":       key,
		"value":     req.Value,
		"timestamp": cmd.Timestamp.Format(time.RFC3339),
		"term":      term,
		"index":     index,
		"version":   result.Version,
	})
}

// handleGet handles GET /kv/{key}
func (s *Server) handleGet(w http.ResponseWriter, r *http.Request) {
	if !s.enforceLeaderOnly(w, r) {
		return
	}

	vars := mux.Vars(r)
	key := vars["key"]

	value, version, timestamp, exists := s.kvStore.Get(key)
	if !exists {
		http.Error(w, `{"error":"key not found"}`, http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"key":       key,
		"value":     value,
		"version":   version,
		"timestamp": timestamp.Format(time.RFC3339),
	})
}

// handleDelete handles DELETE /kv/{key}
func (s *Server) handleDelete(w http.ResponseWriter, r *http.Request) {
	if !s.enforceLeaderOnly(w, r) {
		return
	}

	vars := mux.Vars(r)
	key := vars["key"]

	// Check if key exists
	_, _, _, exists := s.kvStore.Get(key)
	if !exists {
		http.Error(w, `{"error":"key not found"}`, http.StatusNotFound)
		return
	}

	// Create command
	cmd := Command{
		Type:      DeleteCommand,
		Key:       key,
		Timestamp: time.Now(),
	}

	// Submit to Raft
	index, term, isLeader := s.raftNode.Submit(cmd)

	if !isLeader {
		// This should not happen since we already checked, but be safe
		http.Error(w, `{"error":"not leader"}`, http.StatusServiceUnavailable)
		return
	}

	// Wait for the command to be applied (with 10 second timeout)
	result, err := s.kvStore.WaitForApplied(index, 10*time.Second)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"operation timeout: %v"}`, err), http.StatusRequestTimeout)
		return
	}

	// Check if the operation was successful
	if !result.Success {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, result.Error), http.StatusInternalServerError)
		return
	}

	// Return success response
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"term":  term,
		"index": index,
	})
}

// handleList handles GET /kv
func (s *Server) handleList(w http.ResponseWriter, r *http.Request) {
	if !s.enforceLeaderOnly(w, r) {
		return
	}

	// Parse query parameters
	prefix := r.URL.Query().Get("prefix")
	limitStr := r.URL.Query().Get("limit")
	limit := 0
	if limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil && l > 0 {
			limit = l
		}
	}

	keys := s.kvStore.List(prefix, limit)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"keys":     keys,
		"count":    len(keys),
		"has_more": false, // Could implement pagination later
	})
}

// handleStatus handles GET /status
func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	state := "follower"
	if s.raftNode.IsLeader() {
		state = "leader"
	}

	leaderID := s.raftNode.GetLeader()

	// Get stats
	keysCount, dataSize := s.kvStore.Stats()

	// Build peers list
	peers := make([]map[string]interface{}, 0)
	for id, addr := range s.raftPeers {
		if id != s.nodeID {
			peers = append(peers, map[string]interface{}{
				"id":      id,
				"address": addr,
				"voting":  true,
				"online":  true, // Could implement health checks
			})
		}
	}

	response := map[string]interface{}{
		"node": map[string]interface{}{
			"id":      s.nodeID,
			"address": s.raftPeers[s.nodeID],
			"state":   state,
			"term":    0, // Would need to expose from raft node
		},
		"cluster": map[string]interface{}{
			"leader_id": leaderID,
			"size":      len(s.raftPeers),
			"peers":     peers,
		},
		"raft": map[string]interface{}{
			"commit_index":   0, // Would need to expose from raft node
			"last_applied":   0,
			"last_log_index": 0,
			"last_log_term":  0,
		},
		"stats": map[string]interface{}{
			"keys_count":      keysCount,
			"data_size_bytes": dataSize,
			"snapshot_index":  0,
			"uptime_seconds":  int64(time.Since(s.startTime).Seconds()),
		},
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(response)
}

// handleHealth handles GET /health
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	leaderID := s.raftNode.GetLeader()
	isLeader := s.raftNode.IsLeader()
	hasLeader := leaderID != -1

	healthy := hasLeader

	response := map[string]interface{}{
		"status": "healthy",
		"checks": map[string]interface{}{
			"raft_initialized": true,
			"leader_known":     hasLeader,
			"can_write":        isLeader || hasLeader,
			"peers_reachable":  true, // Could implement actual checks
		},
	}

	if !healthy {
		response["status"] = "unhealthy"
		response["error"] = "No leader elected"
		w.WriteHeader(http.StatusServiceUnavailable)
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(response)
}

// parsePeers parses peer configuration string
func parsePeers(peersStr string) (map[int]string, error) {
	peers := make(map[int]string)
	if peersStr == "" {
		return peers, nil
	}

	for _, peer := range strings.Split(peersStr, ",") {
		parts := strings.Split(peer, "=")
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid peer format: %s", peer)
		}

		id, err := strconv.Atoi(parts[0])
		if err != nil {
			return nil, fmt.Errorf("invalid peer ID: %s", parts[0])
		}

		peers[id] = parts[1]
	}

	return peers, nil
}

func main() {
	// Command-line flags
	var (
		nodeID       = flag.Int("id", 0, "Unique server ID")
		raftListener = flag.String("raft-listener", "", "Raft consensus address")
		raftPeersStr = flag.String("raft-peers", "", "Raft peer list")
		apiListener  = flag.String("api-listener", "", "REST API address")
		apiPeersStr  = flag.String("api-peers", "", "API peer list")
		dataDir      = flag.String("data-dir", "", "Storage directory")
		logLevel     = flag.String("log-level", "info", "Log level (debug, info, warn, error)")
		_            = flag.Int("snapshot-threshold", 1000, "Entries before snapshot")
		showHelp     = flag.Bool("help", false, "Show help")
		showVersion  = flag.Bool("version", false, "Show version")
	)

	flag.Parse()

	if *showHelp {
		flag.Usage()
		os.Exit(0)
	}

	if *showVersion {
		fmt.Printf("kv_store version %s\n", Version)
		os.Exit(0)
	}

	// Validate required arguments
	if *nodeID == 0 {
		log.Fatal("--id is required")
	}
	if *raftListener == "" {
		log.Fatal("--raft-listener is required")
	}
	if *apiListener == "" {
		log.Fatal("--api-listener is required")
	}

	// Parse peers
	raftPeers, err := parsePeers(*raftPeersStr)
	if err != nil {
		log.Fatalf("Failed to parse raft-peers: %v", err)
	}

	apiPeers, err := parsePeers(*apiPeersStr)
	if err != nil {
		log.Fatalf("Failed to parse api-peers: %v", err)
	}

	// Add self to peers maps
	raftPeers[*nodeID] = *raftListener
	apiPeers[*nodeID] = *apiListener

	// Set data directory
	if *dataDir == "" {
		*dataDir = fmt.Sprintf("./data-%d", *nodeID)
	}

	// Create data directory
	if err := os.MkdirAll(*dataDir, 0755); err != nil {
		log.Fatalf("Failed to create data directory: %v", err)
	}

	// Create state machine
	kvStore := NewKVStore()

	// Create persistence
	persistenceConfig := &persistence.Config{
		DataDir:  *dataDir,
		ServerID: *nodeID,
	}
	pers, err := jsonPersistence.NewJSONPersistence(persistenceConfig)
	if err != nil {
		log.Fatalf("Failed to create persistence: %v", err)
	}

	// Create discovery
	discovery := transport.NewStaticPeerDiscovery(raftPeers)

	// Log the peer addresses for debugging
	log.Printf("Configured peers:")
	for id, addr := range raftPeers {
		log.Printf("  Node %d: %s", id, addr)
	}

	// Create transport
	transportConfig := &transport.Config{
		ServerID:   *nodeID,
		Address:    *raftListener,
		RPCTimeout: 1000, // 1 second timeout for RPCs
	}
	httpTrans, err := httpTransport.NewHTTPTransport(transportConfig, discovery)
	if err != nil {
		log.Fatalf("Failed to create transport: %v", err)
	}

	// Create Raft configuration
	// Parse log level from command line
	var level LogLevel
	switch strings.ToLower(*logLevel) {
	case "debug":
		level = LogLevelDebug
	case "info":
		level = LogLevelInfo
	case "warn":
		level = LogLevelWarn
	case "error":
		level = LogLevelError
	default:
		level = LogLevelInfo
	}
	logger := NewSimpleLogger(level)
	config := &raft.Config{
		ID:     *nodeID,
		Peers:  make([]int, 0, len(raftPeers)),
		Logger: logger,
	}
	// Add all peer IDs to config
	for peerID := range raftPeers {
		config.Peers = append(config.Peers, peerID)
	}

	// Create Raft node
	raftNode, err := raft.NewNode(config, httpTrans, pers, kvStore)
	if err != nil {
		log.Fatalf("Failed to create Raft node: %v", err)
	}

	// Start Raft node
	ctx := context.Background()
	if err := raftNode.Start(ctx); err != nil {
		log.Fatalf("Failed to start Raft node: %v", err)
	}

	// Create server
	server := &Server{
		nodeID:      *nodeID,
		raftNode:    raftNode,
		kvStore:     kvStore,
		apiListener: *apiListener,
		apiPeers:    apiPeers,
		raftPeers:   raftPeers,
		startTime:   time.Now(),
	}

	// Setup HTTP routes
	router := mux.NewRouter()
	router.HandleFunc("/kv/{key}", server.handlePut).Methods("PUT")
	router.HandleFunc("/kv/{key}", server.handleGet).Methods("GET")
	router.HandleFunc("/kv/{key}", server.handleDelete).Methods("DELETE")
	router.HandleFunc("/kv", server.handleList).Methods("GET")
	router.HandleFunc("/status", server.handleStatus).Methods("GET")
	router.HandleFunc("/health", server.handleHealth).Methods("GET")

	// Start HTTP server
	httpServer := &http.Server{
		Addr:    *apiListener,
		Handler: router,
	}

	go func() {
		log.Printf("Node %d: REST API listening on %s", *nodeID, *apiListener)
		log.Printf("Node %d: Raft listening on %s", *nodeID, *raftListener)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("HTTP server error: %v", err)
		}
	}()

	// Wait for interrupt
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	<-sigCh

	log.Println("Shutting down...")

	// Shutdown HTTP server
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(ctx); err != nil {
		log.Printf("HTTP server shutdown error: %v", err)
	}

	// Stop Raft node
	raftNode.Stop()

	log.Println("Shutdown complete")
}
