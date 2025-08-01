package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/ueisele/raft"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: go run main.go <server-id> [peer-ids...]")
		fmt.Println("Example: go run main.go 0 1 2")
		os.Exit(1)
	}

	// Parse server ID
	serverID, err := strconv.Atoi(os.Args[1])
	if err != nil {
		log.Fatalf("Invalid server ID: %v", err)
	}

	// Parse peer IDs (including self)
	var peers []int
	for i := 1; i < len(os.Args); i++ {
		peerID, err := strconv.Atoi(os.Args[i])
		if err != nil {
			log.Fatalf("Invalid peer ID: %v", err)
		}
		peers = append(peers, peerID)
	}

	// Create apply channel
	applyCh := make(chan raft.LogEntry, 100)

	// Create persister
	baseDataDir := os.Getenv("RAFT_DATA_DIR")
	if baseDataDir == "" {
		baseDataDir = "./data"
	}
	dataDir := fmt.Sprintf("%s/server-%d", baseDataDir, serverID)
	persister := raft.NewPersister(dataDir, serverID)

	// Create Raft instance
	rf := raft.NewRaft(peers, serverID, applyCh)
	rf.SetPersister(persister)

	// Create and start RPC server
	rpcServer := raft.NewRPCServer(rf, 8000+serverID)
	go func() {
		log.Printf("Starting RPC server for server %d on port %d", serverID, 8000+serverID)
		if err := rpcServer.Start(); err != nil {
			log.Fatalf("Failed to start RPC server: %v", err)
		}
	}()

	// Update RPC methods in Raft instance to use actual transport
	transport := raft.NewRPCTransport(fmt.Sprintf("localhost:%d", 8000+serverID))
	updateRaftTransport(rf, transport)

	// Start Raft
	ctx, cancel := context.WithCancel(context.Background())
	rf.Start(ctx)

	// Start apply loop
	go func() {
		for entry := range applyCh {
			log.Printf("Server %d applied entry %d: %v", serverID, entry.Index, entry.Command)
		}
	}()

	// Start status reporter
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				term, isLeader := rf.GetState()
				log.Printf("Server %d: Term=%d, IsLeader=%v", serverID, term, isLeader)
			}
		}
	}()

	// Handle graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	log.Printf("Server %d started successfully", serverID)
	<-sigCh

	log.Printf("Server %d shutting down...", serverID)
	cancel()
	rf.Stop()
}

// updateRaftTransport updates the Raft instance to use actual RPC transport
func updateRaftTransport(rf *raft.Raft, transport *raft.RPCTransport) {
	// Set up RPC transport functions
	rf.SetSendRequestVote(func(peer int, args *raft.RequestVoteArgs, reply *raft.RequestVoteReply) bool {
		err := transport.SendRequestVote(peer, args, reply)
		return err == nil
	})

	rf.SetSendAppendEntries(func(peer int, args *raft.AppendEntriesArgs, reply *raft.AppendEntriesReply) bool {
		err := transport.SendAppendEntries(peer, args, reply)
		return err == nil
	})

	log.Printf("Using RPC transport for communication")
}
