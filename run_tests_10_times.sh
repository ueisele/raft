#!/bin/bash

# Script to run each test file 10 times in a row
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

# Find all test files excluding backup directory
TEST_FILES=$(find . -name "*_test.go" -not -path "./backup/*" -not -path "./vendor/*" | sort)

# Count total test files
TOTAL_FILES=$(echo "$TEST_FILES" | wc -l)
echo "Found $TOTAL_FILES test files to run (excluding backup directory)"
echo "Each test will be run 10 times"
echo "Overall timeout: 20 minutes"
echo "================================================"

# Track results
PASSED_FILES=0
FAILED_FILES=0
FAILED_LIST=""

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

# Process each test file
FILE_NUM=0
for TEST_FILE in $TEST_FILES; do
    FILE_NUM=$((FILE_NUM + 1))
    echo -e "\n${YELLOW}[$FILE_NUM/$TOTAL_FILES] Testing: $TEST_FILE${NC}"
    
    # Track success for this file
    FILE_SUCCESS=true
    
    # Run the test 10 times
    for i in {1..10}; do
        check_timeout
        
        echo -n "  Run $i/10: "
        
        # Calculate remaining time for this test run
        CURRENT_TIME=$(date +%s)
        ELAPSED=$((CURRENT_TIME - START_TIME))
        REMAINING=$((OVERALL_TIMEOUT - ELAPSED))
        
        # Use a reasonable timeout for each test run (min of 60s or remaining time)
        TEST_TIMEOUT=$((REMAINING < 60 ? REMAINING : 60))
        
        # Run the test with timeout
        if timeout $TEST_TIMEOUT go test -v "$TEST_FILE" > /tmp/test_output_$$.log 2>&1; then
            echo -e "${GREEN}PASS${NC}"
        else
            echo -e "${RED}FAIL${NC}"
            echo "    Error output:"
            tail -n 20 /tmp/test_output_$$.log | sed 's/^/    /'
            FILE_SUCCESS=false
            break
        fi
    done
    
    # Update counters
    if [ "$FILE_SUCCESS" = true ]; then
        echo -e "  ${GREEN}✓ All 10 runs passed${NC}"
        PASSED_FILES=$((PASSED_FILES + 1))
    else
        echo -e "  ${RED}✗ Failed${NC}"
        FAILED_FILES=$((FAILED_FILES + 1))
        FAILED_LIST="$FAILED_LIST\n  - $TEST_FILE"
    fi
    
    # Clean up
    rm -f /tmp/test_output_$$.log
done

# Final summary
echo -e "\n================================================"
echo "TEST SUMMARY"
echo "================================================"
echo -e "Total files tested: $TOTAL_FILES"
echo -e "${GREEN}Passed: $PASSED_FILES${NC}"
echo -e "${RED}Failed: $FAILED_FILES${NC}"

if [ $FAILED_FILES -gt 0 ]; then
    echo -e "\nFailed test files:$FAILED_LIST"
    exit 1
else
    echo -e "\n${GREEN}All tests passed 10 times successfully!${NC}"
fi

# Show total time
END_TIME=$(date +%s)
TOTAL_TIME=$((END_TIME - START_TIME))
echo -e "\nTotal execution time: $((TOTAL_TIME / 60)) minutes $((TOTAL_TIME % 60)) seconds"