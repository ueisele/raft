package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
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
				return fmt.Sprintf("OK: set %s=%s", key, value)
			case "delete":
				key, _ := cmd["key"].(string)
				delete(kv.data, key)
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


func main() {
	// Example configuration for a 3-node cluster
	serverID := 1 // Change this for each server
	peers := []int{1, 2, 3}

	// Create KV store
	kvStore := &KVStore{
		data: make(map[string]string),
	}

	// Create peer discovery
	peers := map[int]string{
		0: "localhost:8000",
		1: "localhost:8001",
		2: "localhost:8002",
	}
	discovery := transport.NewStaticPeerDiscovery(peers)

	// Create HTTP transport with discovery
	transportConfig := &transport.Config{
		ServerID:   serverID,
		Address:    fmt.Sprintf("localhost:%d", 8000+serverID),
		RPCTimeout: 1000, // 1 second
	}
	httpTransport, err := httpTransport.NewHTTPTransport(transportConfig, discovery)
	if err != nil {
		log.Fatalf("Failed to create transport: %v", err)
	}

	// Create persistence
	persistenceConfig := &persistence.Config{
		DataDir:  fmt.Sprintf("./data/node%d", serverID),
		ServerID: serverID,
	}
	persistence, err := jsonPersistence.NewJSONPersistence(persistenceConfig)
	if err != nil {
		log.Fatalf("Failed to create persistence: %v", err)
	}

	// Create Raft configuration
	config := &raft.Config{
		ID:                 serverID,
		Peers:              peers,
		ElectionTimeoutMin: 150 * time.Millisecond,
		ElectionTimeoutMax: 300 * time.Millisecond,
		HeartbeatInterval:  50 * time.Millisecond,
		MaxLogSize:         1000,
		Logger:             &SimpleLogger{},
	}

	// Create Raft node
	node, err := raft.NewNode(config, httpTransport, persistence, kvStore)
	if err != nil {
		log.Fatalf("Failed to create Raft node: %v", err)
	}

	// Start the node
	ctx := context.Background()
	if err := node.Start(ctx); err != nil {
		log.Fatalf("Failed to start Raft node: %v", err)
	}

	log.Printf("Raft node %d started on %s", serverID, httpTransport.GetAddress())

	// Example: Submit commands after a delay to allow leader election
	time.Sleep(5 * time.Second)

	// Try to set a value
	setCmd := map[string]interface{}{
		"type":  "set",
		"key":   "foo",
		"value": "bar",
	}

	index, term, isLeader := node.Submit(setCmd)
	if isLeader {
		log.Printf("Command submitted: index=%d, term=%d", index, term)
	} else {
		log.Printf("Not the leader, cannot submit command")
	}

	// Keep the server running
	select {}
}
