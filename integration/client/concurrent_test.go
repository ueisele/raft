package client

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ueisele/raft/integration/helpers"
)

// TestConcurrentClients tests multiple concurrent client operations
func TestConcurrentClients(t *testing.T) {
	// Create 5-node cluster
	cluster := helpers.NewTestCluster(t, 5)

	// Start cluster
	if err := cluster.Start(); err != nil {
		t.Fatalf("Failed to start cluster: %v", err)
	}
	defer cluster.Stop()

	// Wait for leader
	_, err := cluster.WaitForLeader(2 * time.Second)
	if err != nil {
		t.Fatalf("No leader elected: %v", err)
	}

	// Launch multiple concurrent clients
	clientCount := 20
	operationsPerClient := 50
	var wg sync.WaitGroup

	successCount := int64(0)
	errorCount := int64(0)

	startTime := time.Now()

	for clientID := 0; clientID < clientCount; clientID++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			for op := 0; op < operationsPerClient; op++ {
				// Mix of read and write operations
				var cmd string
				if op%3 == 0 {
					cmd = fmt.Sprintf("GET client-%d-key-%d", id, op)
				} else {
					cmd = fmt.Sprintf("SET client-%d-key-%d value-%d", id, op, op)
				}

				_, _, err := cluster.SubmitCommand(cmd)
				if err != nil {
					atomic.AddInt64(&errorCount, 1)
				} else {
					atomic.AddInt64(&successCount, 1)
				}
			}
		}(clientID)
	}

	wg.Wait()
	duration := time.Since(startTime)

	totalOps := int64(clientCount * operationsPerClient)
	finalSuccess := atomic.LoadInt64(&successCount)
	finalErrors := atomic.LoadInt64(&errorCount)

	t.Logf("Concurrent clients test:")
	t.Logf("  %d clients × %d ops = %d total operations",
		clientCount, operationsPerClient, totalOps)
	t.Logf("  Results: %d successful, %d errors", finalSuccess, finalErrors)
	t.Logf("  Duration: %v", duration)
	t.Logf("  Throughput: %.2f ops/second", float64(finalSuccess)/duration.Seconds())

	// Verify high success rate
	successRate := float64(finalSuccess) / float64(totalOps)
	if successRate < 0.95 {
		t.Errorf("Low success rate: %.2f%% (expected > 95%%)", successRate*100)
	}
}

// TestConcurrentReadsAndWrites tests concurrent read and write operations
func TestConcurrentReadsAndWrites(t *testing.T) {
	// Create 3-node cluster
	cluster := helpers.NewTestCluster(t, 3)

	// Start cluster
	if err := cluster.Start(); err != nil {
		t.Fatalf("Failed to start cluster: %v", err)
	}
	defer cluster.Stop()

	// Wait for leader
	_, err := cluster.WaitForLeader(2 * time.Second)
	if err != nil {
		t.Fatalf("No leader elected: %v", err)
	}

	// Set up shared state
	sharedKey := "shared-counter"
	_, _, err = cluster.SubmitCommand(fmt.Sprintf("SET %s 0", sharedKey))
	if err != nil {
		t.Fatalf("Failed to initialize shared state: %v", err)
	}

	// Launch concurrent readers and writers
	writerCount := 5
	readerCount := 10
	opsPerWorker := 20

	var wg sync.WaitGroup
	writeSuccess := int64(0)
	readSuccess := int64(0)

	// Writers increment the counter
	for i := 0; i < writerCount; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			for j := 0; j < opsPerWorker; j++ {
				cmd := fmt.Sprintf("INCREMENT %s", sharedKey)
				_, _, err := cluster.SubmitCommand(cmd)
				if err == nil {
					atomic.AddInt64(&writeSuccess, 1)
				}
				time.Sleep(10 * time.Millisecond)
			}
		}(i)
	}

	// Readers check the counter
	for i := 0; i < readerCount; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			for j := 0; j < opsPerWorker; j++ {
				cmd := fmt.Sprintf("GET %s", sharedKey)
				_, _, err := cluster.SubmitCommand(cmd)
				if err == nil {
					atomic.AddInt64(&readSuccess, 1)
				}
				time.Sleep(5 * time.Millisecond)
			}
		}(i)
	}

	wg.Wait()

	finalWrites := atomic.LoadInt64(&writeSuccess)
	finalReads := atomic.LoadInt64(&readSuccess)

	t.Logf("Concurrent reads and writes:")
	t.Logf("  Writers: %d × %d = %d writes (%d successful)",
		writerCount, opsPerWorker, writerCount*opsPerWorker, finalWrites)
	t.Logf("  Readers: %d × %d = %d reads (%d successful)",
		readerCount, opsPerWorker, readerCount*opsPerWorker, finalReads)
}

// TestClientConnectionStress tests client behavior under connection stress
func TestClientConnectionStress(t *testing.T) {
	// Create 5-node cluster
	cluster := helpers.NewTestCluster(t, 5)

	// Start cluster
	if err := cluster.Start(); err != nil {
		t.Fatalf("Failed to start cluster: %v", err)
	}
	defer cluster.Stop()

	// Wait for leader
	_, err := cluster.WaitForLeader(2 * time.Second)
	if err != nil {
		t.Fatalf("No leader elected: %v", err)
	}

	// Simulate many short-lived client connections
	connectionCount := 50
	var wg sync.WaitGroup
	successCount := int64(0)

	for i := 0; i < connectionCount; i++ {
		wg.Add(1)
		go func(connID int) {
			defer wg.Done()

			// Each "connection" submits a few commands then disconnects
			for j := 0; j < 3; j++ {
				cmd := fmt.Sprintf("conn-%d-cmd-%d", connID, j)
				_, _, err := cluster.SubmitCommand(cmd)
				if err == nil {
					atomic.AddInt64(&successCount, 1)
				}
			}

			// Simulate connection close/reconnect delay
			time.Sleep(50 * time.Millisecond)
		}(i)

		// Stagger connection starts
		time.Sleep(10 * time.Millisecond)
	}

	wg.Wait()

	finalSuccess := atomic.LoadInt64(&successCount)
	expectedOps := int64(connectionCount * 3)

	t.Logf("Client connection stress test:")
	t.Logf("  Connections: %d", connectionCount)
	t.Logf("  Successful operations: %d/%d", finalSuccess, expectedOps)

	if finalSuccess < expectedOps*8/10 {
		t.Errorf("Too many failed operations: only %d/%d succeeded",
			finalSuccess, expectedOps)
	}
}

// TestConcurrentConfigurationChanges tests client operations during configuration changes
func TestConcurrentConfigurationChanges(t *testing.T) {
	// Create initial 3-node cluster
	cluster := helpers.NewTestCluster(t, 3)

	// Start cluster
	if err := cluster.Start(); err != nil {
		t.Fatalf("Failed to start cluster: %v", err)
	}
	defer cluster.Stop()

	// Wait for leader
	leaderID, err := cluster.WaitForLeader(2 * time.Second)
	if err != nil {
		t.Fatalf("No leader elected: %v", err)
	}

	leader := cluster.Nodes[leaderID]

	// Start client operations
	done := make(chan struct{})
	clientErrors := int64(0)
	clientSuccess := int64(0)

	go func() {
		opNum := 0
		for {
			select {
			case <-done:
				return
			default:
				cmd := fmt.Sprintf("config-change-op-%d", opNum)
				_, _, err := cluster.SubmitCommand(cmd)
				if err != nil {
					atomic.AddInt64(&clientErrors, 1)
				} else {
					atomic.AddInt64(&clientSuccess, 1)
				}
				opNum++
				time.Sleep(20 * time.Millisecond)
			}
		}
	}()

	// Let client operations run
	time.Sleep(500 * time.Millisecond)

	// Add a new server (would be node 3 in a real scenario)
	// Note: This is simulated as actual server addition requires more setup
	t.Log("Simulating configuration change")

	// Submit configuration command
	_, _, isLeader := leader.Submit("ADD_SERVER 3")
	if !isLeader {
		t.Log("Node is no longer leader during configuration change")
	}

	// Continue client operations during config change
	time.Sleep(time.Second)

	// Stop client operations
	close(done)
	time.Sleep(100 * time.Millisecond)

	finalSuccess := atomic.LoadInt64(&clientSuccess)
	finalErrors := atomic.LoadInt64(&clientErrors)

	t.Logf("Client operations during configuration changes:")
	t.Logf("  Successful: %d", finalSuccess)
	t.Logf("  Errors: %d", finalErrors)
	t.Logf("  Error rate: %.2f%%", float64(finalErrors)/float64(finalSuccess+finalErrors)*100)

	// Verify cluster is still functional
	_, _, err = cluster.SubmitCommand("post-config-test")
	if err != nil {
		t.Errorf("Failed to submit command after config changes: %v", err)
	}
}

// TestConcurrentLeaderFailure tests concurrent client behavior during leader failure
func TestConcurrentLeaderFailure(t *testing.T) {
	// Create 5-node cluster
	cluster := helpers.NewTestCluster(t, 5)

	// Start cluster
	if err := cluster.Start(); err != nil {
		t.Fatalf("Failed to start cluster: %v", err)
	}
	defer cluster.Stop()

	// Wait for leader
	initialLeaderID, err := cluster.WaitForLeader(2 * time.Second)
	if err != nil {
		t.Fatalf("No leader elected: %v", err)
	}

	// Launch concurrent clients
	clientCount := 10
	done := make(chan struct{})
	var wg sync.WaitGroup

	successBeforeFailure := int64(0)
	successAfterFailure := int64(0)
	errorsDuringFailure := int64(0)
	leaderFailed := int64(0)

	for i := 0; i < clientCount; i++ {
		wg.Add(1)
		go func(clientID int) {
			defer wg.Done()

			opNum := 0
			for {
				select {
				case <-done:
					return
				default:
					cmd := fmt.Sprintf("client-%d-op-%d", clientID, opNum)
					_, _, err := cluster.SubmitCommand(cmd)

					if atomic.LoadInt64(&leaderFailed) == 0 {
						if err == nil {
							atomic.AddInt64(&successBeforeFailure, 1)
						}
					} else {
						if err == nil {
							atomic.AddInt64(&successAfterFailure, 1)
						} else {
							atomic.AddInt64(&errorsDuringFailure, 1)
						}
					}

					opNum++
					time.Sleep(10 * time.Millisecond)
				}
			}
		}(i)
	}

	// Let clients run for a bit
	time.Sleep(time.Second)

	// Kill the leader
	atomic.StoreInt64(&leaderFailed, 1)
	cluster.Nodes[initialLeaderID].Stop()
	t.Logf("Stopped leader %d", initialLeaderID)

	// Continue for a while to test recovery
	time.Sleep(3 * time.Second)

	// Stop clients
	close(done)
	wg.Wait()

	t.Logf("Concurrent operations during leader failure:")
	t.Logf("  Before failure: %d successful", atomic.LoadInt64(&successBeforeFailure))
	t.Logf("  During/after failure: %d successful, %d errors",
		atomic.LoadInt64(&successAfterFailure), atomic.LoadInt64(&errorsDuringFailure))

	// Verify new leader was elected
	newLeader := -1
	for i, node := range cluster.Nodes {
		if i != initialLeaderID && node.IsLeader() {
			newLeader = i
			break
		}
	}

	if newLeader == -1 {
		t.Error("No new leader elected after failure")
	} else {
		t.Logf("New leader elected: node %d", newLeader)
	}
}
