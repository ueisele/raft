package stress

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ueisele/raft"
	"github.com/ueisele/raft/integration/helpers"
)

// TestHighLoadSingleClient tests system behavior under high load from a single client
func TestHighLoadSingleClient(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping load test in short mode")
	}

	// Create 5-node cluster
	cluster := helpers.NewTestCluster(t, 5)

	// Start cluster
	if err := cluster.Start(); err != nil {
		t.Fatalf("Failed to start cluster: %v", err)
	}
	defer cluster.Stop() //nolint:errcheck // test cleanup

	// Wait for leader
	_, err := cluster.WaitForLeader(2 * time.Second)
	if err != nil {
		t.Fatalf("No leader elected: %v", err)
	}

	// Submit many commands rapidly
	commandCount := 1000
	successCount := 0
	startTime := time.Now()

	for i := 0; i < commandCount; i++ {
		cmd := fmt.Sprintf("load-cmd-%d", i)
		_, _, err := cluster.SubmitCommand(cmd)
		if err == nil {
			successCount++
		}

		// No delay - maximum throughput
	}

	duration := time.Since(startTime)
	throughput := float64(successCount) / duration.Seconds()

	t.Logf("Submitted %d/%d commands in %v", successCount, commandCount, duration)
	t.Logf("Throughput: %.2f commands/second", throughput)

	// Verify cluster is still healthy
	cluster.WaitForStableCluster(2 * time.Second)

	// Wait for all commands to be committed
	if successCount > 0 {
		lastIndex := successCount
		helpers.WaitForCommitIndex(t, cluster.Nodes, lastIndex, 5*time.Second)
	}
}

// TestHighLoadMultipleClients tests system behavior under high load from multiple clients
func TestHighLoadMultipleClients(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping load test in short mode")
	}

	// Create 5-node cluster
	cluster := helpers.NewTestCluster(t, 5)

	// Start cluster
	if err := cluster.Start(); err != nil {
		t.Fatalf("Failed to start cluster: %v", err)
	}
	defer cluster.Stop() //nolint:errcheck // test cleanup

	// Wait for leader
	_, err := cluster.WaitForLeader(2 * time.Second)
	if err != nil {
		t.Fatalf("No leader elected: %v", err)
	}

	// Launch multiple concurrent clients
	clientCount := 10
	commandsPerClient := 100
	var wg sync.WaitGroup
	successCount := int64(0)
	errorCount := int64(0)

	startTime := time.Now()

	for clientID := 0; clientID < clientCount; clientID++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			for i := 0; i < commandsPerClient; i++ {
				cmd := fmt.Sprintf("client-%d-cmd-%d", id, i)
				_, _, err := cluster.SubmitCommand(cmd)
				if err == nil {
					atomic.AddInt64(&successCount, 1)
				} else {
					atomic.AddInt64(&errorCount, 1)
				}
			}
		}(clientID)
	}

	wg.Wait()
	duration := time.Since(startTime)

	totalCommands := int64(clientCount * commandsPerClient)
	finalSuccess := atomic.LoadInt64(&successCount)
	finalErrors := atomic.LoadInt64(&errorCount)
	throughput := float64(finalSuccess) / duration.Seconds()

	t.Logf("Multiple clients: %d clients × %d commands = %d total",
		clientCount, commandsPerClient, totalCommands)
	t.Logf("Results: %d successful, %d errors in %v",
		finalSuccess, finalErrors, duration)
	t.Logf("Throughput: %.2f commands/second", throughput)

	// Verify cluster health
	cluster.WaitForStableCluster(2 * time.Second)
}

// TestSustainedLoad tests system behavior under sustained load over time
func TestSustainedLoad(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping sustained load test in short mode")
	}

	// Create 3-node cluster
	cluster := helpers.NewTestCluster(t, 3)

	// Start cluster
	if err := cluster.Start(); err != nil {
		t.Fatalf("Failed to start cluster: %v", err)
	}
	defer cluster.Stop() //nolint:errcheck // test cleanup

	// Wait for leader
	_, err := cluster.WaitForLeader(2 * time.Second)
	if err != nil {
		t.Fatalf("No leader elected: %v", err)
	}

	// Run sustained load for a period
	duration := 10 * time.Second
	done := make(chan struct{})
	successCount := int64(0)
	errorCount := int64(0)

	// Track throughput over time
	throughputSamples := make([]float64, 0)
	sampleMutex := sync.Mutex{}

	// Start load generator
	go func() {
		commandNum := 0
		lastSampleTime := time.Now()
		lastSampleCount := int64(0)

		for {
			select {
			case <-done:
				return
			default:
				cmd := fmt.Sprintf("sustained-cmd-%d", commandNum)
				_, _, err := cluster.SubmitCommand(cmd)
				if err == nil {
					atomic.AddInt64(&successCount, 1)
				} else {
					atomic.AddInt64(&errorCount, 1)
				}
				commandNum++

				// Sample throughput every second
				if time.Since(lastSampleTime) >= time.Second {
					currentCount := atomic.LoadInt64(&successCount)
					throughput := float64(currentCount-lastSampleCount) / time.Since(lastSampleTime).Seconds()

					sampleMutex.Lock()
					throughputSamples = append(throughputSamples, throughput)
					sampleMutex.Unlock()

					lastSampleTime = time.Now()
					lastSampleCount = currentCount
				}

				// Small delay to avoid overwhelming the system
				time.Sleep(time.Millisecond)
			}
		}
	}()

	// Run for specified duration
	time.Sleep(duration)
	close(done)

	// Wait a bit for in-flight requests
	time.Sleep(time.Second)

	// Calculate statistics
	finalSuccess := atomic.LoadInt64(&successCount)
	finalErrors := atomic.LoadInt64(&errorCount)
	avgThroughput := float64(finalSuccess) / duration.Seconds()

	sampleMutex.Lock()
	samples := throughputSamples
	sampleMutex.Unlock()

	// Calculate throughput variance
	var minThroughput, maxThroughput float64
	if len(samples) > 0 {
		minThroughput = samples[0]
		maxThroughput = samples[0]
		for _, sample := range samples {
			if sample < minThroughput {
				minThroughput = sample
			}
			if sample > maxThroughput {
				maxThroughput = sample
			}
		}
	}

	t.Logf("Sustained load test ran for %v", duration)
	t.Logf("Total: %d successful, %d errors", finalSuccess, finalErrors)
	t.Logf("Average throughput: %.2f commands/second", avgThroughput)
	t.Logf("Throughput range: %.2f - %.2f commands/second", minThroughput, maxThroughput)

	// Verify cluster is still healthy
	cluster.WaitForStableCluster(2 * time.Second)
}

// TestLoadWithNodeFailures tests system behavior under load with node failures
func TestLoadWithNodeFailures(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping load test with failures in short mode")
	}

	// Create 5-node cluster
	cluster := helpers.NewTestCluster(t, 5)

	// Start cluster
	if err := cluster.Start(); err != nil {
		t.Fatalf("Failed to start cluster: %v", err)
	}
	defer cluster.Stop() //nolint:errcheck // test cleanup

	// Wait for leader
	initialLeaderID, err := cluster.WaitForLeader(2 * time.Second)
	if err != nil {
		t.Fatalf("No leader elected: %v", err)
	}

	// Start load generator
	done := make(chan struct{})
	successCount := int64(0)
	errorCount := int64(0)

	go func() {
		commandNum := 0
		for {
			select {
			case <-done:
				return
			default:
				cmd := fmt.Sprintf("failure-test-cmd-%d", commandNum)
				_, _, err := cluster.SubmitCommand(cmd)
				if err == nil {
					atomic.AddInt64(&successCount, 1)
				} else {
					atomic.AddInt64(&errorCount, 1)
				}
				commandNum++
				time.Sleep(5 * time.Millisecond)
			}
		}
	}()

	// Let it run for a bit
	time.Sleep(2 * time.Second)

	// Stop a follower
	followerID := (initialLeaderID + 1) % 5
	t.Logf("Stopping follower node %d", followerID)
	cluster.Nodes[followerID].Stop()

	// Continue load for a bit
	time.Sleep(2 * time.Second)

	// Stop another follower
	followerID2 := (initialLeaderID + 2) % 5
	t.Logf("Stopping follower node %d", followerID2)
	cluster.Nodes[followerID2].Stop()

	// Continue load - should still work with 3 nodes
	time.Sleep(2 * time.Second)

	// Stop the load
	close(done)
	time.Sleep(500 * time.Millisecond)

	finalSuccess := atomic.LoadInt64(&successCount)
	finalErrors := atomic.LoadInt64(&errorCount)

	t.Logf("Load test with failures: %d successful, %d errors", finalSuccess, finalErrors)

	// Verify remaining nodes still have a leader
	activeNodes := make([]raft.Node, 0)
	for i, node := range cluster.Nodes {
		if i != followerID && i != followerID2 {
			activeNodes = append(activeNodes, node)
		}
	}

	helpers.WaitForLeader(t, activeNodes, 2*time.Second)
	t.Log("Cluster maintained availability despite failures")
}

// TestLoadWithNetworkPartitions tests system behavior under load with network partitions
func TestLoadWithNetworkPartitions(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping load test with partitions in short mode")
	}

	// Create 5-node cluster with partitionable transport
	cluster := helpers.NewTestCluster(t, 5, helpers.WithPartitionableTransport())

	// Start cluster
	if err := cluster.Start(); err != nil {
		t.Fatalf("Failed to start cluster: %v", err)
	}
	defer cluster.Stop() //nolint:errcheck // test cleanup

	// Wait for leader
	_, err := cluster.WaitForLeader(2 * time.Second)
	if err != nil {
		t.Fatalf("No leader elected: %v", err)
	}

	// Start load generator
	done := make(chan struct{})
	successCount := int64(0)
	errorCount := int64(0)
	partitionErrors := int64(0)

	go func() {
		commandNum := 0
		for {
			select {
			case <-done:
				return
			default:
				cmd := fmt.Sprintf("partition-load-cmd-%d", commandNum)
				_, _, err := cluster.SubmitCommand(cmd)
				if err == nil {
					atomic.AddInt64(&successCount, 1)
				} else {
					atomic.AddInt64(&errorCount, 1)
					if err.Error() == "no leader available" {
						atomic.AddInt64(&partitionErrors, 1)
					}
				}
				commandNum++
				time.Sleep(5 * time.Millisecond)
			}
		}
	}()

	// Let it run normally for a bit
	time.Sleep(2 * time.Second)
	baselineSuccess := atomic.LoadInt64(&successCount)

	// Create a partition (3 nodes vs 2 nodes)
	t.Log("Creating network partition")
	cluster.CreatePartition([]int{0, 1, 2}, []int{3, 4})

	// Run under partition
	time.Sleep(3 * time.Second)
	partitionSuccess := atomic.LoadInt64(&successCount) - baselineSuccess

	// Heal partition
	t.Log("Healing network partition")
	cluster.HealPartition()

	// Run after healing
	beforeHeal := atomic.LoadInt64(&successCount)
	time.Sleep(2 * time.Second)
	healedSuccess := atomic.LoadInt64(&successCount) - beforeHeal

	// Stop load
	close(done)
	time.Sleep(500 * time.Millisecond)

	finalSuccess := atomic.LoadInt64(&successCount)
	finalErrors := atomic.LoadInt64(&errorCount)
	finalPartitionErrors := atomic.LoadInt64(&partitionErrors)

	t.Logf("Load test with network partitions:")
	t.Logf("  Total: %d successful, %d errors (%d partition-related)",
		finalSuccess, finalErrors, finalPartitionErrors)
	t.Logf("  Before partition: %d commands", baselineSuccess)
	t.Logf("  During partition: %d commands", partitionSuccess)
	t.Logf("  After healing: %d commands", healedSuccess)

	// Verify cluster recovered
	cluster.WaitForStableCluster(2 * time.Second)
}
