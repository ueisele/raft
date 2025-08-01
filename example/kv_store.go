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
	"sync"
	"syscall"
	"time"

	"github.com/ueisele/raft"
	"github.com/ueisele/raft/persistence"
	jsonPersistence "github.com/ueisele/raft/persistence/json"
	"github.com/ueisele/raft/transport"
	httpTransport "github.com/ueisele/raft/transport/http"
)

// KVStore is a simple key-value store that implements StateMachine
type KVStore struct {
	mu   sync.RWMutex
	data map[string]string
}

// Command types for the KV store
type SetCommand struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type DeleteCommand struct {
	Key string `json:"key"`
}

// Apply implements StateMachine.Apply
func (kv *KVStore) Apply(entry raft.LogEntry) interface{} {
	kv.mu.Lock()
	defer kv.mu.Unlock()

	// Parse command based on type
	switch cmd := entry.Command.(type) {
	case map[string]interface{}:
		// Handle JSON commands
		if cmdType, ok := cmd["type"].(string); ok {
			switch cmdType {
			case "set":
				key, _ := cmd["key"].(string)
				value, _ := cmd["value"].(string)
				kv.data[key] = value
				log.Printf("Applied SET: %s = %s", key, value)
				return fmt.Sprintf("OK: set %s=%s", key, value)
			case "delete":
				key, _ := cmd["key"].(string)
				delete(kv.data, key)
				log.Printf("Applied DELETE: %s", key)
				return fmt.Sprintf("OK: deleted %s", key)
			}
		}
	}

	return "ERROR: unknown command"
}

// Snapshot implements StateMachine.Snapshot
func (kv *KVStore) Snapshot() ([]byte, error) {
	kv.mu.RLock()
	defer kv.mu.RUnlock()

	return json.Marshal(kv.data)
}

// Restore implements StateMachine.Restore
func (kv *KVStore) Restore(snapshot []byte) error {
	kv.mu.Lock()
	defer kv.mu.Unlock()

	kv.data = make(map[string]string)
	return json.Unmarshal(snapshot, &kv.data)
}

// Get retrieves a value (not part of StateMachine interface)
func (kv *KVStore) Get(key string) (string, bool) {
	kv.mu.RLock()
	defer kv.mu.RUnlock()

	value, exists := kv.data[key]
	return value, exists
}

// GetAll returns all key-value pairs
func (kv *KVStore) GetAll() map[string]string {
	kv.mu.RLock()
	defer kv.mu.RUnlock()

	result := make(map[string]string)
	for k, v := range kv.data {
		result[k] = v
	}
	return result
}

// SimpleLogger implements the Logger interface
type SimpleLogger struct{}

func (l *SimpleLogger) Debug(format string, args ...interface{}) {
	log.Printf("[DEBUG] "+format, args...)
}

func (l *SimpleLogger) Info(format string, args ...interface{}) {
	log.Printf("[INFO] "+format, args...)
}

func (l *SimpleLogger) Warn(format string, args ...interface{}) {
	log.Printf("[WARN] "+format, args...)
}

func (l *SimpleLogger) Error(format string, args ...interface{}) {
	log.Printf("[ERROR] "+format, args...)
}

// CloudDiscovery demonstrates a custom discovery implementation for cloud environments
type CloudDiscovery struct {
	cloudProvider string
	serviceName   string
}

func NewCloudDiscovery(provider, serviceName string) *CloudDiscovery {
	return &CloudDiscovery{
		cloudProvider: provider,
		serviceName:   serviceName,
	}
}

func (c *CloudDiscovery) GetPeerAddress(ctx context.Context, serverID int) (string, error) {
	// In production, this would query cloud provider APIs
	switch c.cloudProvider {
	case "k8s":
		// Use predictable StatefulSet DNS names
		return fmt.Sprintf("raft-%d.%s.default.svc.cluster.local:8000", serverID, c.serviceName), nil
	case "aws":
		// Would use AWS SDK to query EC2 instances by tags
		return "", fmt.Errorf("AWS discovery not implemented in example")
	case "gcp":
		// Would use GCP SDK to query compute instances
		return "", fmt.Errorf("GCP discovery not implemented in example")
	default:
		return "", fmt.Errorf("unsupported cloud provider: %s", c.cloudProvider)
	}
}

func (c *CloudDiscovery) RefreshPeers(ctx context.Context) error {
	// Refresh cached peer information
	return nil
}

func (c *CloudDiscovery) Close() error {
	// Clean up any resources
	return nil
}

func main() {
	var (
		nodeID      = flag.Int("id", 0, "Node ID (0-2 for 3-node cluster)")
		httpPort    = flag.Int("port", 0, "HTTP port (default: 8000+nodeID)")
		dataDir     = flag.String("data", "", "Data directory (default: ./data/nodeN)")
		cloudMode   = flag.String("cloud", "", "Cloud provider mode: k8s, aws, gcp (default: static)")
		serviceName = flag.String("service", "raft-service", "Service name for cloud discovery")
	)
	flag.Parse()

	// Validate node ID
	if *nodeID < 0 || *nodeID > 2 {
		log.Fatal("Node ID must be between 0 and 2")
	}

	// Set defaults
	if *httpPort == 0 {
		*httpPort = 8000 + *nodeID
	}
	if *dataDir == "" {
		*dataDir = fmt.Sprintf("./data/node%d", *nodeID)
	}

	// Create data directory
	if err := os.MkdirAll(*dataDir, 0755); err != nil {
		log.Fatalf("Failed to create data directory: %v", err)
	}

	// Create KV store
	kvStore := &KVStore{
		data: make(map[string]string),
	}

	// Create peer discovery based on mode
	var discovery transport.PeerDiscovery

	if *cloudMode != "" {
		// Cloud-based discovery
		log.Printf("Using cloud discovery: provider=%s, service=%s", *cloudMode, *serviceName)
		discovery = NewCloudDiscovery(*cloudMode, *serviceName)
	} else {
		// Static peer discovery for local development
		peers := map[int]string{
			0: "localhost:8000",
			1: "localhost:8001",
			2: "localhost:8002",
		}
		log.Printf("Using static peer discovery: %v", peers)
		discovery = transport.NewStaticPeerDiscovery(peers)
	}

	// Create HTTP transport with discovery
	transportConfig := &transport.Config{
		ServerID:   *nodeID,
		Address:    fmt.Sprintf("localhost:%d", *httpPort),
		RPCTimeout: 5000, // 5 seconds
	}

	// You can also use the builder pattern:
	// httpTransport, err := httpTransport.NewBuilder(*nodeID, transportConfig.Address).
	//     WithDiscovery(discovery).
	//     WithTimeout(5 * time.Second).
	//     Build()

	httpTrans, err := httpTransport.NewHTTPTransport(transportConfig, discovery)
	if err != nil {
		log.Fatalf("Failed to create transport: %v", err)
	}

	// Create persistence
	persistenceConfig := &persistence.Config{
		DataDir:  *dataDir,
		ServerID: *nodeID,
	}
	jsonPersist, err := jsonPersistence.NewJSONPersistence(persistenceConfig)
	if err != nil {
		log.Fatalf("Failed to create persistence: %v", err)
	}

	// Create Raft configuration
	config := &raft.Config{
		ID:                 *nodeID,
		Peers:              []int{0, 1, 2},
		ElectionTimeoutMin: 150 * time.Millisecond,
		ElectionTimeoutMax: 300 * time.Millisecond,
		HeartbeatInterval:  50 * time.Millisecond,
		MaxLogSize:         10000,
		Logger:             &SimpleLogger{},
	}

	// Create Raft node
	node, err := raft.NewNode(config, httpTrans, jsonPersist, kvStore)
	if err != nil {
		log.Fatalf("Failed to create Raft node: %v", err)
	}

	// Transport RPC handler is already set in raft.NewNode

	// Start transport
	if err := httpTrans.Start(); err != nil {
		log.Fatalf("Failed to start transport: %v", err)
	}

	// Start the node
	ctx := context.Background()
	if err := node.Start(ctx); err != nil {
		log.Fatalf("Failed to start Raft node: %v", err)
	}

	log.Printf("Raft node %d started on %s", *nodeID, httpTrans.GetAddress())

	// Setup HTTP API for client interaction
	setupHTTPAPI(node, kvStore, *httpPort+1000, *nodeID)

	// Example: Dynamic peer updates (useful for cloud environments)
	if *cloudMode != "" {
		go func() {
			ticker := time.NewTicker(30 * time.Second)
			defer ticker.Stop()

			for range ticker.C {
				// In production, this would fetch from service discovery
				log.Println("Would refresh peers from cloud provider here")
			}
		}()
	}

	// Example usage after cluster is ready
	go func() {
		time.Sleep(5 * time.Second)

		// Only submit commands if we're the leader
		if node.IsLeader() {
			// Set a value
			setCmd := map[string]interface{}{
				"type":  "set",
				"key":   "example",
				"value": "Hello from Raft!",
			}

			index, term, isLeader := node.Submit(setCmd)
			if isLeader {
				log.Printf("Example command submitted: index=%d, term=%d", index, term)
			}
		}
	}()

	// Handle graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	<-sigChan

	log.Println("Shutting down...")
	node.Stop()
	if err := httpTrans.Stop(); err != nil {
		log.Printf("Error stopping transport: %v", err)
	}
}

// setupHTTPAPI creates a simple HTTP API for client interaction
func setupHTTPAPI(node raft.Node, kvStore *KVStore, port int, nodeID int) {
	mux := http.NewServeMux()

	// GET /kv/{key} - Get a value
	mux.HandleFunc("/kv/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		key := r.URL.Path[4:] // Remove "/kv/" prefix
		value, exists := kvStore.Get(key)
		if !exists {
			http.Error(w, "Key not found", http.StatusNotFound)
			return
		}

		if _, err := w.Write([]byte(value)); err != nil {
			log.Printf("Error writing response: %v", err)
		}
	})

	// POST /kv - Set a key-value pair
	mux.HandleFunc("/kv", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var cmd SetCommand
		if err := json.NewDecoder(r.Body).Decode(&cmd); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		setCmd := map[string]interface{}{
			"type":  "set",
			"key":   cmd.Key,
			"value": cmd.Value,
		}

		index, term, isLeader := node.Submit(setCmd)
		if !isLeader {
			// Return leader info if available
			leaderID := node.GetLeader()
			w.Header().Set("X-Raft-Leader", fmt.Sprintf("%d", leaderID))
			http.Error(w, "Not the leader", http.StatusServiceUnavailable)
			return
		}

		response := map[string]interface{}{
			"index": index,
			"term":  term,
		}
		if err := json.NewEncoder(w).Encode(response); err != nil {
			log.Printf("Error encoding response: %v", err)
		}
	})

	// GET /status - Get node status
	mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		term, isLeader := node.GetState()

		// Get configuration details
		config := node.GetConfiguration()
		var servers []map[string]interface{}
		for _, server := range config.Servers {
			servers = append(servers, map[string]interface{}{
				"id":     server.ID,
				"voting": server.Voting,
			})
		}

		status := map[string]interface{}{
			"nodeId":       nodeID,
			"term":         term,
			"isLeader":     isLeader,
			"leaderID":     node.GetLeader(),
			"commitIndex":  node.GetCommitIndex(),
			"lastLogIndex": node.GetLogLength() - 1,
			"servers":      servers,
		}
		if err := json.NewEncoder(w).Encode(status); err != nil {
			log.Printf("Error encoding response: %v", err)
		}
	})

	// GET /kv - Get all key-value pairs (for debugging)
	mux.HandleFunc("/debug/kv", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		data := kvStore.GetAll()
		if err := json.NewEncoder(w).Encode(data); err != nil {
			log.Printf("Error encoding response: %v", err)
		}
	})

	// Start HTTP server
	addr := fmt.Sprintf(":%d", port)
	go func() {
		log.Printf("Client API listening on %s", addr)
		if err := http.ListenAndServe(addr, mux); err != nil {
			log.Printf("Client API server error: %v", err)
		}
	}()
}

// Example commands to interact with the cluster:
//
// 1. Start a 3-node cluster:
//    Terminal 1: go run kv_store.go -id 0
//    Terminal 2: go run kv_store.go -id 1
//    Terminal 3: go run kv_store.go -id 2
//
// 2. Set a value (to leader on port 9000, 9001, or 9002):
//    curl -X POST http://localhost:9000/kv -d '{"key":"foo","value":"bar"}'
//
// 3. Get a value:
//    curl http://localhost:9000/kv/foo
//
// 4. Check node status:
//    curl http://localhost:9000/status
//
// 5. View all data (debug):
//    curl http://localhost:9000/debug/kv
//
// 6. For Kubernetes deployment:
//    go run kv_store.go -id 0 -cloud k8s -service raft-service
