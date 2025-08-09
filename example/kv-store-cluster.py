#!/usr/bin/env python3
"""
KV Store Cluster Test Suite

Usage:
    kv-store-cluster.py [OPTIONS]

Options:
    --cluster-size N      Number of nodes (default: 3)
    --base-port N        Starting port (default: 8080)
    --run-tests          Execute test suite
    --test-filter REGEX  Run specific tests
    --clients N          Concurrent clients (default: 10)
    --operations N       Operations per client (default: 100)
    --duration SECONDS   Test duration (default: 60)
    --binary PATH        KV store binary (default: ./kv_store)
    --src PATH           KV store source (default: ./kv_store.go)
    --verbose            Enable debug output
    --keep-logs          Don't delete logs after tests
"""

import argparse
import json
import os
import re
import signal
import subprocess
import sys
import time
import threading
import shutil
from concurrent.futures import ThreadPoolExecutor, as_completed
from typing import List, Dict, Optional, Tuple

try:
    import requests
    from requests.adapters import HTTPAdapter
    from urllib3.util.retry import Retry
    REQUESTS_AVAILABLE = True
except ImportError:
    REQUESTS_AVAILABLE = False
    print("Warning: 'requests' module not installed. Limited functionality available.")
    print("To install dependencies:")
    print("  Using uv:     uv pip install -r requirements.txt")
    print("  Using pip:    pip install -r requirements.txt")
    print("  Using poetry: poetry install")
    print()


class Node:
    """Represents a single KV store node"""
    
    def __init__(self, node_id: int, api_port: int, raft_port: int, data_dir: str):
        self.id = node_id
        self.api_port = api_port
        self.raft_port = raft_port
        self.api_address = f"127.0.0.1:{api_port}"
        self.raft_address = f"127.0.0.1:{raft_port}"
        self.data_dir = data_dir
        self.process: Optional[subprocess.Popen] = None
        self.log_file = None
        
    def start(self, binary_path: str, peers: List['Node'], verbose: bool = False):
        """Start the node process"""
        # Prepare peer lists
        raft_peers = []
        api_peers = []
        for peer in peers:
            if peer.id != self.id:
                raft_peers.append(f"{peer.id}={peer.raft_address}")
                api_peers.append(f"{peer.id}={peer.api_address}")
        
        # Build command
        cmd = [
            binary_path,
            "--id", str(self.id),
            "--api-listener", self.api_address,
            "--raft-listener", self.raft_address,
            "--raft-peers", ",".join(raft_peers),
            "--api-peers", ",".join(api_peers),
            "--data-dir", self.data_dir,
            "--log-level", "debug" if verbose else "info"
        ]
        
        # Create log file
        os.makedirs("cluster-logs", exist_ok=True)
        self.log_file = open(f"cluster-logs/node-{self.id}.log", "w")
        
        # Start process
        self.process = subprocess.Popen(
            cmd,
            stdout=self.log_file,
            stderr=subprocess.STDOUT,
            preexec_fn=os.setsid if sys.platform != "win32" else None
        )
        
        print(f"  Starting Node {self.id}: http://{self.api_address} (Raft: {self.raft_address})")
        
    def stop(self):
        """Stop the node process"""
        if self.process:
            try:
                if sys.platform == "win32":
                    self.process.terminate()
                else:
                    os.killpg(os.getpgid(self.process.pid), signal.SIGTERM)
                self.process.wait(timeout=5)
            except:
                self.process.kill()
            finally:
                self.process = None
                
        if self.log_file:
            self.log_file.close()
            
    def is_alive(self) -> bool:
        """Check if the node process is running"""
        return self.process is not None and self.process.poll() is None
    
    def get_status(self) -> Optional[Dict]:
        """Get node status"""
        try:
            resp = requests.get(f"http://{self.api_address}/status", timeout=1)
            if resp.status_code == 200:
                return resp.json()
        except:
            pass
        return None
    
    def is_leader(self) -> bool:
        """Check if node is the leader"""
        status = self.get_status()
        return status and status.get("node", {}).get("state") == "leader"


class Cluster:
    """Manages a cluster of KV store nodes"""
    
    def __init__(self, size: int, base_port: int, binary_path: str, verbose: bool = False):
        self.size = size
        self.base_port = base_port
        self.binary_path = binary_path
        self.verbose = verbose
        self.nodes: List[Node] = []
        
        # Create nodes
        for i in range(1, size + 1):
            api_port = base_port + (i - 1)
            raft_port = base_port + 1000 + (i - 1)
            data_dir = f"cluster-data/node-{i}"
            self.nodes.append(Node(i, api_port, raft_port, data_dir))
            
    def start(self):
        """Start all nodes in the cluster"""
        print("\nStarting cluster...")
        
        # Clean up old data
        if os.path.exists("cluster-data"):
            shutil.rmtree("cluster-data")
        if os.path.exists("cluster-logs"):
            shutil.rmtree("cluster-logs")
            
        # Create directories
        for node in self.nodes:
            os.makedirs(node.data_dir, exist_ok=True)
            
        # Start nodes
        for node in self.nodes:
            node.start(self.binary_path, self.nodes, self.verbose)
            time.sleep(0.5)  # Small delay between starts
            
        print("\nWaiting for cluster to initialize...")
        self.wait_for_leader(timeout=10)
        
    def stop(self):
        """Stop all nodes in the cluster"""
        print("\nStopping cluster...")
        for node in self.nodes:
            node.stop()
            
    def wait_for_leader(self, timeout: int = 10) -> Optional[Node]:
        """Wait for a leader to be elected"""
        start_time = time.time()
        while time.time() - start_time < timeout:
            for node in self.nodes:
                if node.is_leader():
                    print(f"  Leader elected: Node {node.id}")
                    self.print_status()
                    return node
            time.sleep(0.5)
        
        print("  WARNING: No leader elected within timeout")
        return None
    
    def get_leader(self) -> Optional[Node]:
        """Get the current leader node"""
        for node in self.nodes:
            if node.is_leader():
                return node
        return None
    
    def print_status(self):
        """Print cluster status"""
        print("\nNode Status:")
        for node in self.nodes:
            status = node.get_status()
            if status:
                state = status.get("node", {}).get("state", "unknown").upper()
                tag = f"[{state:^8}]"
                print(f"  {tag} Node {node.id}: http://{node.api_address} (Raft: {node.raft_address})")
            else:
                print(f"  [OFFLINE ] Node {node.id}: http://{node.api_address} (Raft: {node.raft_address})")


class KVClient:
    """Client for interacting with the KV store"""
    
    def __init__(self, base_url: str):
        self.base_url = base_url
        self.session = requests.Session()
        
        # Configure retry strategy for automatic redirect following
        retry_strategy = Retry(
            total=5,
            status_forcelist=[308],
            allowed_methods=["GET", "PUT", "DELETE"],
            backoff_factor=0.1
        )
        adapter = HTTPAdapter(max_retries=retry_strategy)
        self.session.mount("http://", adapter)
        
    def put(self, key: str, value: str) -> Tuple[bool, Optional[Dict]]:
        """Set a key-value pair"""
        try:
            resp = self.session.put(
                f"{self.base_url}/kv/{key}",
                json={"value": value},
                timeout=5,
                allow_redirects=True
            )
            if resp.status_code == 200:
                return True, resp.json()
            return False, {"error": resp.text}
        except Exception as e:
            return False, {"error": str(e)}
    
    def get(self, key: str) -> Tuple[bool, Optional[str]]:
        """Get a value by key"""
        try:
            resp = self.session.get(
                f"{self.base_url}/kv/{key}",
                timeout=5,
                allow_redirects=True
            )
            if resp.status_code == 200:
                return True, resp.json().get("value")
            elif resp.status_code == 404:
                return True, None
            return False, None
        except Exception as e:
            return False, None
    
    def delete(self, key: str) -> bool:
        """Delete a key"""
        try:
            resp = self.session.delete(
                f"{self.base_url}/kv/{key}",
                timeout=5,
                allow_redirects=True
            )
            return resp.status_code in [200, 204, 404]
        except:
            return False
    
    def list_keys(self, prefix: str = "", limit: int = 0) -> Tuple[bool, List[str]]:
        """List all keys"""
        try:
            params = {}
            if prefix:
                params["prefix"] = prefix
            if limit:
                params["limit"] = limit
                
            resp = self.session.get(
                f"{self.base_url}/kv",
                params=params,
                timeout=5,
                allow_redirects=True
            )
            if resp.status_code == 200:
                return True, resp.json().get("keys", [])
            return False, []
        except:
            return False, []


class TestRunner:
    """Runs test scenarios against the cluster"""
    
    def __init__(self, cluster: Cluster, clients: int = 10, operations: int = 100):
        self.cluster = cluster
        self.num_clients = clients
        self.operations_per_client = operations
        self.passed = 0
        self.failed = 0
        
    def run_all(self, test_filter: str = ""):
        """Run all test scenarios"""
        print("\n" + "="*80)
        print("RUNNING TEST SUITE")
        print("="*80)
        
        tests = [
            ("basic_operations", self.test_basic_operations),
            ("consistency", self.test_consistency),
            ("leader_failure", self.test_leader_failure),
        ]
        
        for name, test_func in tests:
            if test_filter and not re.search(test_filter, name):
                continue
                
            print(f"\n[TEST] {name}")
            try:
                test_func()
                print(f"  ✓ PASSED")
                self.passed += 1
            except AssertionError as e:
                print(f"  ✗ FAILED: {e}")
                self.failed += 1
            except Exception as e:
                print(f"  ✗ ERROR: {e}")
                self.failed += 1
        
        print("\n" + "="*80)
        print(f"TEST RESULTS: {self.passed} passed, {self.failed} failed")
        print("="*80)
        
        return self.failed == 0
    
    def test_basic_operations(self):
        """Test basic set, get, delete operations"""
        leader = self.cluster.get_leader()
        assert leader, "No leader available"
        
        client = KVClient(f"http://{leader.api_address}")
        
        # Test SET
        success, result = client.put("test_key", "test_value")
        assert success, f"Failed to set key: {result}"
        
        # Wait briefly for entry to be committed and applied
        time.sleep(0.5)
        
        # Test GET
        success, value = client.get("test_key")
        assert success and value == "test_value", f"Get returned wrong value: {value}"
        
        # Test DELETE
        success = client.delete("test_key")
        assert success, "Failed to delete key"
        
        # Wait briefly for deletion to be committed and applied
        time.sleep(0.5)
        
        # Verify deletion
        success, value = client.get("test_key")
        assert success and value is None, "Key still exists after deletion"
        
        print("  - Basic operations: SET, GET, DELETE ✓")
        
    def test_consistency(self):
        """Test concurrent writes maintain consistency"""
        leader = self.cluster.get_leader()
        assert leader, "No leader available"
        
        key = "counter"
        
        def increment_counter(client_id: int):
            client = KVClient(f"http://{leader.api_address}")
            successes = 0
            for i in range(self.operations_per_client):
                # Simple increment simulation
                success, _ = client.put(f"{key}_{client_id}_{i}", str(i))
                if success:
                    successes += 1
            return successes
        
        # Run concurrent increments
        with ThreadPoolExecutor(max_workers=self.num_clients) as executor:
            futures = [executor.submit(increment_counter, i) for i in range(self.num_clients)]
            total_successes = sum(f.result() for f in as_completed(futures))
        
        expected = self.num_clients * self.operations_per_client
        assert total_successes == expected, f"Expected {expected} operations, got {total_successes}"
        
        # Verify all keys exist
        client = KVClient(f"http://{leader.api_address}")
        success, keys = client.list_keys(prefix=f"{key}_")
        assert success and len(keys) == expected, f"Expected {expected} keys, found {len(keys)}"
        
        print(f"  - Consistency test: {total_successes}/{expected} operations succeeded ✓")
        
    def test_leader_failure(self):
        """Test cluster recovers from leader failure"""
        # Get initial leader
        old_leader = self.cluster.get_leader()
        assert old_leader, "No initial leader"
        
        # Write data before failure
        client = KVClient(f"http://{old_leader.api_address}")
        success, _ = client.put("before_failure", "important_data")
        assert success, "Failed to write before failure"
        
        # Wait for data to be replicated and committed
        time.sleep(1)
        
        print(f"  - Stopping leader (Node {old_leader.id})...")
        
        # Stop the leader
        old_leader.stop()
        time.sleep(2)
        
        # Wait for new leader
        new_leader = self.cluster.wait_for_leader(timeout=10)
        assert new_leader, "No new leader elected"
        assert new_leader.id != old_leader.id, "Same leader after failure"
        
        print(f"  - New leader elected (Node {new_leader.id})")
        
        # Verify data persisted
        client = KVClient(f"http://{new_leader.api_address}")
        success, value = client.get("before_failure")
        assert success and value == "important_data", "Data lost after leader failure"
        
        # Write new data
        success, _ = client.put("after_failure", "new_data")
        assert success, "Failed to write after leader change"
        
        print("  - Data persistence and new writes working ✓")


def build_binary(src_path: str, binary_path: str) -> bool:
    """Build the Go binary from source"""
    print(f"Building binary from {src_path}...")
    try:
        subprocess.run(
            ["go", "build", "-o", binary_path, src_path],
            check=True,
            capture_output=True
        )
        print(f"  Binary built: {binary_path}")
        return True
    except subprocess.CalledProcessError as e:
        print(f"  Build failed: {e.stderr.decode()}")
        return False


def print_interactive_info(cluster: Cluster):
    """Print information for interactive mode"""
    print("\n" + "="*80)
    print(" " * 20 + "KV STORE CLUSTER READY")
    print("="*80)
    
    print("\nCluster Configuration:")
    print(f"  Nodes: {cluster.size}")
    print(f"  Replication Factor: {(cluster.size + 1) // 2}")
    
    cluster.print_status()
    
    leader = cluster.get_leader()
    if leader:
        print("\nQuick Test Commands:")
        print("\n  # Write a value (to leader)")
        print(f'  curl -X PUT http://{leader.api_address}/kv/hello \\')
        print('       -H "Content-Type: application/json" \\')
        print('       -d \'{"value":"world"}\'')
        
        print("\n  # Read from any node")
        print(f"  curl http://{cluster.nodes[0].api_address}/kv/hello")
        
        print("\n  # Check cluster status")
        print(f"  curl http://{leader.api_address}/status | jq")
        
        print("\n  # Run performance test")
        print("  ./kv-store-cluster.py --run-tests --test-filter performance")
    
    print("\nLogs: ./cluster-logs/")
    print("Data: ./cluster-data/")
    print("\nPress Ctrl+C to shutdown cluster...")


def main():
    parser = argparse.ArgumentParser(
        description="KV Store Cluster Test Suite",
        formatter_class=argparse.RawDescriptionHelpFormatter
    )
    
    parser.add_argument("--cluster-size", type=int, default=3,
                      help="Number of nodes (default: 3)")
    parser.add_argument("--base-port", type=int, default=8080,
                      help="Starting port (default: 8080)")
    parser.add_argument("--run-tests", action="store_true",
                      help="Execute test suite")
    parser.add_argument("--test-filter", type=str, default="",
                      help="Run specific tests matching regex")
    parser.add_argument("--clients", type=int, default=10,
                      help="Concurrent clients (default: 10)")
    parser.add_argument("--operations", type=int, default=100,
                      help="Operations per client (default: 100)")
    parser.add_argument("--duration", type=int, default=60,
                      help="Test duration in seconds (default: 60)")
    parser.add_argument("--binary", type=str, default="./kv_store",
                      help="KV store binary path (default: ./kv_store)")
    parser.add_argument("--src", type=str, default="./kv_store.go",
                      help="KV store source path (default: ./kv_store.go)")
    parser.add_argument("--verbose", action="store_true",
                      help="Enable debug output")
    parser.add_argument("--keep-logs", action="store_true",
                      help="Don't delete logs after tests")
    
    args = parser.parse_args()
    
    # Check if binary exists, build if needed
    binary_path = args.binary
    if not os.path.exists(binary_path):
        if os.path.exists(args.src):
            if not build_binary(args.src, binary_path):
                sys.exit(1)
        else:
            print(f"Error: Binary {binary_path} not found and source {args.src} not available")
            sys.exit(1)
    
    # Create cluster
    cluster = Cluster(args.cluster_size, args.base_port, binary_path, args.verbose)
    
    # Handle Ctrl+C gracefully
    def signal_handler(sig, frame):
        print("\n\nInterrupted by user")
        cluster.stop()
        if not args.keep_logs:
            if os.path.exists("cluster-logs"):
                shutil.rmtree("cluster-logs")
            if os.path.exists("cluster-data"):
                shutil.rmtree("cluster-data")
        sys.exit(0)
    
    signal.signal(signal.SIGINT, signal_handler)
    
    try:
        # Start cluster
        cluster.start()
        
        if args.run_tests:
            # Run tests
            runner = TestRunner(cluster, args.clients, args.operations)
            success = runner.run_all(args.test_filter)
            
            # Stop cluster
            cluster.stop()
            
            # Clean up unless keeping logs
            if not args.keep_logs:
                if os.path.exists("cluster-logs"):
                    shutil.rmtree("cluster-logs")
                if os.path.exists("cluster-data"):
                    shutil.rmtree("cluster-data")
            
            sys.exit(0 if success else 1)
        else:
            # Interactive mode
            print_interactive_info(cluster)
            
            # Wait forever
            while True:
                time.sleep(1)
                
    except KeyboardInterrupt:
        pass
    finally:
        cluster.stop()


if __name__ == "__main__":
    main()