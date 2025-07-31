#!/bin/bash

# Script to run each test 10 times in a row
# Overall timeout: 20 minutes (1200 seconds)

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Start time for overall timeout
START_TIME=$(date +%s)
OVERALL_TIMEOUT=7200 # 2 hours in seconds

# Get list of all tests
echo "Discovering tests..."
ALL_TESTS=$(go test -list . 2>/dev/null | grep -E "^Test" | grep -v "^TestMain$" || true)
TOTAL_TESTS=$(echo "$ALL_TESTS" | wc -l)

echo "Found $TOTAL_TESTS tests to run"
echo "Each test will be run 10 times"
echo "Overall timeout: 2 hours"
echo "================================================"

# Track results
PASSED_TESTS=0
FAILED_TESTS=0
FAILED_LIST=""

# Function to check if we've exceeded the overall timeout
check_timeout() {
    CURRENT_TIME=$(date +%s)
    ELAPSED=$((CURRENT_TIME - START_TIME))
    if [ $ELAPSED -ge $OVERALL_TIMEOUT ]; then
        echo -e "\n${RED}ERROR: Overall timeout of 2 hours exceeded${NC}"
        echo "Elapsed time: $((ELAPSED / 60)) minutes $((ELAPSED % 60)) seconds"
        echo -e "\nCompleted: $((PASSED_TESTS + FAILED_TESTS))/$TOTAL_TESTS tests"
        exit 1
    fi
}

# Process each test
TEST_NUM=0
for TEST_NAME in $ALL_TESTS; do
    TEST_NUM=$((TEST_NUM + 1))
    echo -e "\n${BLUE}[$TEST_NUM/$TOTAL_TESTS] Testing: $TEST_NAME${NC}"
    
    # Track success for this test
    TEST_SUCCESS=true
    FAILED_RUN=0
    
    # Run the test 10 times
    for i in {1..10}; do
        check_timeout
        
        # Calculate remaining time
        CURRENT_TIME=$(date +%s)
        ELAPSED=$((CURRENT_TIME - START_TIME))
        REMAINING=$((OVERALL_TIMEOUT - ELAPSED))
        
        # Skip if less than 5 seconds remaining
        if [ $REMAINING -lt 5 ]; then
            echo -e "  ${YELLOW}Skipping remaining runs due to time limit${NC}"
            TEST_SUCCESS=false
            break
        fi
        
        echo -n "  Run $i/10: "
        
        # Use a reasonable timeout for each test (30s or remaining time)
        TEST_TIMEOUT=$((REMAINING < 30 ? REMAINING : 30))
        
        # Run the specific test with timeout
        if timeout $TEST_TIMEOUT go test -run "^${TEST_NAME}$" -timeout 25s > /tmp/test_output_$$.log 2>&1; then
            echo -e "${GREEN}PASS${NC}"
        else
            echo -e "${RED}FAIL${NC}"
            FAILED_RUN=$i
            TEST_SUCCESS=false
            # Show brief error info
            if grep -q "panic:" /tmp/test_output_$$.log; then
                echo "    Panic detected!"
            elif grep -q "timeout" /tmp/test_output_$$.log; then
                echo "    Test timeout!"
            else
                # Show the actual failure line
                grep -E "^\s+.*_test\.go:[0-9]+:" /tmp/test_output_$$.log | head -1 | sed 's/^/    /'
            fi
            break
        fi
    done
    
    # Update counters
    if [ "$TEST_SUCCESS" = true ]; then
        echo -e "  ${GREEN}✓ All 10 runs passed${NC}"
        PASSED_TESTS=$((PASSED_TESTS + 1))
    else
        echo -e "  ${RED}✗ Failed at run $FAILED_RUN${NC}"
        FAILED_TESTS=$((FAILED_TESTS + 1))
        FAILED_LIST="$FAILED_LIST\n  - $TEST_NAME (failed at run $FAILED_RUN)"
    fi
    
    # Clean up
    rm -f /tmp/test_output_$$.log
done

# Final summary
echo -e "\n================================================"
echo -e "${YELLOW}TEST SUMMARY${NC}"
echo "================================================"
echo -e "Total tests: $TOTAL_TESTS"
echo -e "${GREEN}Passed: $PASSED_TESTS${NC}"
echo -e "${RED}Failed: $FAILED_TESTS${NC}"

if [ $FAILED_TESTS -gt 0 ]; then
    echo -e "\nFailed tests:$FAILED_LIST"
fi

# Show total time
END_TIME=$(date +%s)
TOTAL_TIME=$((END_TIME - START_TIME))
echo -e "\nTotal execution time: $((TOTAL_TIME / 60)) minutes $((TOTAL_TIME % 60)) seconds"

# Exit with appropriate code
if [ $FAILED_TESTS -eq 0 ]; then
    echo -e "\n${GREEN}All tests passed 10 times successfully!${NC}"
    exit 0
else
    echo -e "\n${RED}Some tests failed${NC}"
    exit 1
fi