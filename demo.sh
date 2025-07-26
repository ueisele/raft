#!/bin/bash

# Demo script for running a 3-node Raft cluster

echo "Starting Raft Cluster Demo..."
echo "This will start 3 Raft servers on ports 8000, 8001, and 8002"

# Global variables for server PIDs
SERVER_0_PID=""
SERVER_1_PID=""
SERVER_2_PID=""

# Robust cleanup function
cleanup() {
    echo ""
    echo "Cleaning up servers..."
    
    # Collect all PIDs
    local pids=()
    [ -n "$SERVER_0_PID" ] && pids+=($SERVER_0_PID)
    [ -n "$SERVER_1_PID" ] && pids+=($SERVER_1_PID)
    [ -n "$SERVER_2_PID" ] && pids+=($SERVER_2_PID)
    
    # Also find any processes using our ports (in case PIDs are lost)
    local port_pids=$(lsof -ti :8000-8002 2>/dev/null)
    if [ -n "$port_pids" ]; then
        for pid in $port_pids; do
            # Only add if not already in pids array
            if [[ ! " ${pids[@]} " =~ " $pid " ]]; then
                pids+=($pid)
            fi
        done
    fi
    
    if [ ${#pids[@]} -eq 0 ]; then
        echo "No server processes to clean up"
        return 0
    fi
    
    # First, try graceful termination (SIGTERM)
    echo "Sending SIGTERM to server processes (PIDs: ${pids[*]})..."
    for pid in "${pids[@]}"; do
        if kill -0 "$pid" 2>/dev/null; then
            kill "$pid" 2>/dev/null
        fi
    done
    
    # Wait up to 8 seconds for graceful shutdown (increased from 5)
    local wait_count=0
    local max_wait=8
    while [ $wait_count -lt $max_wait ]; do
        local running_pids=()
        for pid in "${pids[@]}"; do
            if kill -0 "$pid" 2>/dev/null; then
                running_pids+=($pid)
            fi
        done
        
        if [ ${#running_pids[@]} -eq 0 ]; then
            echo "✓ All servers terminated gracefully"
            break
        fi
        
        echo "Waiting for ${#running_pids[@]} processes to terminate... (${running_pids[*]})"
        sleep 1
        wait_count=$((wait_count + 1))
    done
    
    # Check what's still running and force kill if needed
    local still_running=()
    for pid in "${pids[@]}"; do
        if kill -0 "$pid" 2>/dev/null; then
            still_running+=($pid)
        fi
    done
    
    if [ ${#still_running[@]} -gt 0 ]; then
        echo "Force killing remaining processes: ${still_running[*]}"
        for pid in "${still_running[@]}"; do
            kill -9 "$pid" 2>/dev/null
        done
        
        # Wait a moment after force kill
        sleep 2
    fi
    
    # Final verification - check both PIDs and ports
    local remaining=0
    for pid in "${pids[@]}"; do
        if kill -0 "$pid" 2>/dev/null; then
            echo "⚠ Failed to kill PID $pid"
            remaining=$((remaining + 1))
        fi
    done
    
    # Double-check ports are clear
    local remaining_port_pids=$(lsof -ti :8000-8002 2>/dev/null)
    if [ -n "$remaining_port_pids" ]; then
        echo "⚠ Still found processes using ports 8000-8002: $remaining_port_pids"
        echo "Force killing remaining port processes..."
        echo "$remaining_port_pids" | xargs -r kill -9 2>/dev/null
        sleep 1
        
        # Final port check
        remaining_port_pids=$(lsof -ti :8000-8002 2>/dev/null)
        if [ -n "$remaining_port_pids" ]; then
            echo "⚠ Could not kill processes using ports: $remaining_port_pids"
            remaining=$((remaining + 1))
        fi
    fi
    
    if [ $remaining -eq 0 ]; then
        echo "✓ All server processes cleaned up successfully"
    else
        echo "⚠ $remaining processes could not be terminated"
        return 1
    fi
}

# Set up signal handlers for cleanup on interruption only
trap 'echo "Interrupted! Cleaning up..."; cleanup; exit 130' INT TERM

# Create data and log directories in tmp
DATA_DIR="tmp/raft-demo-data"
LOGS_DIR="tmp/raft-demo-logs"
mkdir -p $DATA_DIR/server-0 $DATA_DIR/server-1 $DATA_DIR/server-2 $LOGS_DIR

# Function to wait for leader election
wait_for_leader() {
    local timeout=${1:-30}  # Default 30 seconds timeout
    local start_time=$(date +%s)
    local leader_id=""
    
    echo "Waiting for leader election (timeout: ${timeout}s)..."
    
    while [ $(($(date +%s) - start_time)) -lt $timeout ]; do
        for i in 0 1 2; do
            response=$(curl -s http://localhost:800$i/status 2>/dev/null)
            if [ $? -eq 0 ]; then
                is_leader=$(echo "$response" | jq -r '.isLeader // false' 2>/dev/null)
                if [ "$is_leader" = "true" ]; then
                    leader_id=$i
                    echo "Leader elected: Server $leader_id"
                    echo "$leader_id"
                    return 0
                fi
            fi
        done
        sleep 1
    done
    
    echo "Timeout: No leader elected within ${timeout} seconds"
    return 1
}

# Function to find current leader
find_leader() {
    for i in 0 1 2; do
        response=$(curl -s http://localhost:800$i/status 2>/dev/null)
        if [ $? -eq 0 ]; then
            is_leader=$(echo "$response" | jq -r '.isLeader // false' 2>/dev/null)
            if [ "$is_leader" = "true" ]; then
                echo "$i"
                return 0
            fi
        fi
    done
    return 1
}

# Function to check if a port is available
check_port_available() {
    local port=$1
    if lsof -i :$port >/dev/null 2>&1; then
        return 1  # Port is in use
    fi
    return 0  # Port is available
}

# Function to verify server process started successfully
verify_server_started() {
    local server_id=$1
    local pid=$2
    local port=$((8000 + server_id))
    local max_wait=10
    local count=0
    
    echo "Verifying server $server_id started successfully..."
    
    # First check if the process is still running
    if ! kill -0 $pid 2>/dev/null; then
        echo "✗ Server $server_id process died immediately (PID $pid)"
        return 1
    fi
    
    # Wait for the server to bind to its port
    while [ $count -lt $max_wait ]; do
        if lsof -i :$port >/dev/null 2>&1; then
            echo "✓ Server $server_id is listening on port $port"
            return 0
        fi
        
        # Check if process is still alive
        if ! kill -0 $pid 2>/dev/null; then
            echo "✗ Server $server_id process died during startup (PID $pid)"
            return 1
        fi
        
        sleep 1
        count=$((count + 1))
    done
    
    echo "✗ Server $server_id failed to bind to port $port within ${max_wait}s"
    return 1
}

# Function to submit command to leader and verify acceptance
submit_command_to_leader() {
    local command="$1"
    local leader_id=$(find_leader)
    
    if [ -z "$leader_id" ]; then
        echo "Error: No leader found"
        return 1
    fi
    
    echo "Submitting command '$command' to leader (Server $leader_id)..."
    
    response=$(curl -s -X POST http://localhost:800$leader_id/submit \
        -H "Content-Type: application/json" \
        -d "\"$command\"")
    
    if [ $? -ne 0 ]; then
        echo "Error: Failed to submit command"
        return 1
    fi
    
    is_leader=$(echo "$response" | jq -r '.isLeader // false' 2>/dev/null)
    index=$(echo "$response" | jq -r '.index // null' 2>/dev/null)
    term=$(echo "$response" | jq -r '.term // null' 2>/dev/null)
    
    if [ "$is_leader" = "true" ] && [ "$index" != "null" ] && [ "$term" != "null" ]; then
        echo "✓ Command accepted by leader: Index=$index, Term=$term"
        return 0
    else
        echo "✗ Command rejected or leader changed: $response"
        return 1
    fi
}

# Check if required ports are available
echo "Checking port availability..."
for port in 8000 8001 8002; do
    if ! check_port_available $port; then
        echo "✗ Port $port is already in use. Please free the port and try again."
        echo "You can check what's using the port with: lsof -i :$port"
        exit 1
    fi
done
echo "✓ All required ports (8000-8002) are available"

# Start servers in background and verify they start successfully
echo "Starting server 0..."
RAFT_DATA_DIR=$DATA_DIR go run example/main.go 0 0 1 2 > $LOGS_DIR/server-0.log 2>&1 &
SERVER_0_PID=$!

if ! verify_server_started 0 $SERVER_0_PID; then
    echo "Failed to start server 0, exiting..."
    cleanup
    exit 1
fi

echo "Starting server 1..."
RAFT_DATA_DIR=$DATA_DIR go run example/main.go 1 0 1 2 > $LOGS_DIR/server-1.log 2>&1 &
SERVER_1_PID=$!

if ! verify_server_started 1 $SERVER_1_PID; then
    echo "Failed to start server 1, exiting..."
    cleanup
    exit 1
fi

echo "Starting server 2..."
RAFT_DATA_DIR=$DATA_DIR go run example/main.go 2 0 1 2 > $LOGS_DIR/server-2.log 2>&1 &
SERVER_2_PID=$!

if ! verify_server_started 2 $SERVER_2_PID; then
    echo "Failed to start server 2, exiting..."
    cleanup
    exit 1
fi

echo "✓ All servers started successfully"

# Wait for leader election with timeout
if ! wait_for_leader 30; then
    echo "Failed to elect leader, exiting..."
    cleanup
    exit 1
fi

# Show status of all servers
echo ""
echo "=== Server Status ==="
for i in 0 1 2; do
    echo "Server $i status:"
    curl -s http://localhost:800$i/status | jq '.'
    echo ""
done

# Submit some commands using the new function
echo "=== Submitting Commands ==="
if ! submit_command_to_leader "hello world"; then
    echo "Failed to submit first command, exiting..."
    cleanup
    exit 1
fi

echo ""
if ! submit_command_to_leader "test command"; then
    echo "Failed to submit second command, exiting..."
    cleanup
    exit 1
fi

echo ""
if ! submit_command_to_leader "final test"; then
    echo "Failed to submit third command, exiting..."
    cleanup
    exit 1
fi

# Wait a moment for replication
echo ""
echo "Waiting for command replication..."
sleep 3

# Show final status with command verification
echo ""
echo "=== Final Server Status ==="
for i in 0 1 2; do
    echo "Server $i final status:"
    status=$(curl -s http://localhost:800$i/status)
    echo "$status" | jq '.'
    
    # Check if commands were replicated
    commit_index=$(echo "$status" | jq -r '.commitIndex // 0')
    last_applied=$(echo "$status" | jq -r '.lastApplied // 0')
    log_length=$(echo "$status" | jq -r '.logLength // 0')
    
    if [ "$commit_index" -gt 0 ] && [ "$last_applied" -gt 0 ]; then
        echo "✓ Server $i has processed $last_applied commands (committed: $commit_index, log: $log_length)"
    else
        echo "⚠ Server $i may not have processed all commands yet"
    fi
    echo ""
done

# Verify all servers have the same committed state
echo "=== Replication Verification ==="
leader_id=$(find_leader)
if [ -n "$leader_id" ]; then
    leader_status=$(curl -s "http://localhost:800$leader_id/status")
    leader_commit=$(echo "$leader_status" | jq -r '.commitIndex // 0')
    
    echo "Leader (Server $leader_id) commit index: $leader_commit"
    
    all_synced=true
    for i in 0 1 2; do
        if [ "$i" != "$leader_id" ]; then
            follower_status=$(curl -s http://localhost:800$i/status)
            follower_commit=$(echo "$follower_status" | jq -r '.commitIndex // 0')
            
            if [ "$follower_commit" -eq "$leader_commit" ]; then
                echo "✓ Server $i is in sync (commit index: $follower_commit)"
            else
                echo "✗ Server $i is out of sync (commit index: $follower_commit, expected: $leader_commit)"
                all_synced=false
            fi
        fi
    done
    
    if [ "$all_synced" = true ]; then
        echo "✓ All servers are synchronized!"
    else
        echo "⚠ Some servers are not yet synchronized"
    fi
else
    echo "⚠ No leader found for final verification"
fi

# Verify persistence to disk
echo ""
echo "=== Persistence Verification ==="
echo "Checking if commands were persisted to disk..."

persistence_verified=true
for i in 0 1 2; do
    state_file="$DATA_DIR/server-$i/raft-state-$i.json"
    if [ -f "$state_file" ]; then
        echo "✓ Server $i state file exists: $state_file"
        
        # Check if our test commands are in the persisted log
        commands_found=0
        if grep -q "hello world" "$state_file"; then
            commands_found=$((commands_found + 1))
        fi
        if grep -q "test command" "$state_file"; then
            commands_found=$((commands_found + 1))
        fi
        if grep -q "final test" "$state_file"; then
            commands_found=$((commands_found + 1))
        fi
        
        if [ $commands_found -eq 3 ]; then
            echo "✓ Server $i has all 3 commands persisted to disk"
        else
            echo "✗ Server $i is missing persisted commands (found $commands_found/3)"
            persistence_verified=false
        fi
        
        # Show log length from persisted file
        log_length=$(jq '.log | length' "$state_file" 2>/dev/null)
        current_term=$(jq '.currentTerm' "$state_file" 2>/dev/null)
        if [ -n "$log_length" ] && [ -n "$current_term" ]; then
            echo "  Persisted state: Term=$current_term, Log entries=$log_length"
        fi
    else
        echo "✗ Server $i state file not found: $state_file"
        persistence_verified=false
    fi
    echo ""
done

if [ "$persistence_verified" = true ]; then
    echo "✓ All commands successfully persisted to disk across all servers!"
else
    echo "⚠ Some commands may not have been properly persisted"
fi

echo ""
echo "Demo completed successfully! Now cleaning up servers..."

# Explicitly call cleanup and wait for completion
cleanup

# Wait a bit more to ensure complete termination
echo "Waiting for all processes to fully terminate..."
sleep 2

# Final verification
remaining_processes=$(lsof -ti :8000-8002 2>/dev/null | wc -l)
if [ "$remaining_processes" -gt 0 ]; then
    echo "⚠ Warning: $remaining_processes processes may still be using ports 8000-8002"
    lsof -i :8000-8002 2>/dev/null || true
else
    echo "✓ All server processes have been successfully terminated"
fi

echo ""
echo "Demo finished! Check $LOGS_DIR/server-*.log for detailed output."