#!/bin/bash

# Test core functionality multiple times
# Focus on the most important tests

echo "Testing core Raft functionality for stability..."

# Core tests that should always pass
CORE_TESTS="TestBasicNodeCreation|TestLogManager|TestStateManager|TestReplication|TestElectionManagerHandleRequestVote"

# Run core tests 10 times
for i in {1..10}; do
    echo ""
    echo "=== Run $i/10 ==="
    
    if ! go test -v -run "$CORE_TESTS" -timeout 30s > test_run_$i.log 2>&1; then
        echo "❌ Run $i failed!"
        echo "Failed tests:"
        grep "FAIL:" test_run_$i.log
        exit 1
    else
        echo "✅ Run $i passed"
        # Show summary
        grep -E "PASS:|FAIL:" test_run_$i.log | wc -l | xargs echo "  Tests run:"
    fi
done

echo ""
echo "✅ All 10 runs completed successfully!"

# Clean up log files
rm -f test_run_*.log