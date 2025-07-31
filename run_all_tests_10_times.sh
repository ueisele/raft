#!/bin/bash

# Script to run all tests 10 times in a row
# Overall timeout: 20 minutes (1200 seconds)

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Start time for overall timeout
START_TIME=$(date +%s)
OVERALL_TIMEOUT=1200 # 20 minutes in seconds

echo "Running all tests in the package 10 times"
echo "Overall timeout: 20 minutes"
echo "================================================"

# Function to check if we've exceeded the overall timeout
check_timeout() {
    CURRENT_TIME=$(date +%s)
    ELAPSED=$((CURRENT_TIME - START_TIME))
    if [ $ELAPSED -ge $OVERALL_TIMEOUT ]; then
        echo -e "\n${RED}ERROR: Overall timeout of 20 minutes exceeded${NC}"
        echo "Elapsed time: $((ELAPSED / 60)) minutes $((ELAPSED % 60)) seconds"
        exit 1
    fi
}

# Track success
ALL_SUCCESS=true
FAILED_RUNS=""

# Run all tests 10 times
for i in {1..10}; do
    check_timeout
    
    echo -e "\n${YELLOW}Run $i/10${NC}"
    echo -n "  Status: "
    
    # Calculate remaining time for this test run
    CURRENT_TIME=$(date +%s)
    ELAPSED=$((CURRENT_TIME - START_TIME))
    REMAINING=$((OVERALL_TIMEOUT - ELAPSED))
    
    # Use a reasonable timeout for each test run (min of 180s or remaining time)
    TEST_TIMEOUT=$((REMAINING < 180 ? REMAINING : 180))
    
    # Run all tests with timeout
    if timeout $TEST_TIMEOUT go test -v ./... > /tmp/test_output_$$.log 2>&1; then
        echo -e "${GREEN}PASS${NC}"
        # Show summary of passed tests
        PASSED_COUNT=$(grep -c "^--- PASS:" /tmp/test_output_$$.log || true)
        echo "    Passed $PASSED_COUNT tests"
    else
        echo -e "${RED}FAIL${NC}"
        echo "    Error output:"
        # Show failed tests
        grep "^--- FAIL:" /tmp/test_output_$$.log | head -10 | sed 's/^/    /' || true
        # Show any panic or error messages
        grep -A5 -i -E "(panic:|error:|FAIL)" /tmp/test_output_$$.log | tail -20 | sed 's/^/    /' || true
        ALL_SUCCESS=false
        FAILED_RUNS="$FAILED_RUNS $i"
    fi
done

# Clean up
rm -f /tmp/test_output_$$.log

# Final summary
echo -e "\n================================================"
echo "TEST SUMMARY"
echo "================================================"

if [ "$ALL_SUCCESS" = true ]; then
    echo -e "${GREEN}✓ All 10 test runs passed successfully!${NC}"
    exit 0
else
    echo -e "${RED}✗ Some test runs failed${NC}"
    echo "Failed runs:$FAILED_RUNS"
    exit 1
fi

# Show total time
END_TIME=$(date +%s)
TOTAL_TIME=$((END_TIME - START_TIME))
echo -e "\nTotal execution time: $((TOTAL_TIME / 60)) minutes $((TOTAL_TIME % 60)) seconds"