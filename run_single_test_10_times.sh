#!/bin/bash

# Script to test a single file 10 times
# Usage: ./run_single_test_10_times.sh <test_file>

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

TEST_FILE="${1:-./basic_test.go}"

echo -e "${YELLOW}Testing: $TEST_FILE${NC}"
echo "Running test 10 times..."

# Track success
FILE_SUCCESS=true

# Run the test 10 times
for i in {1..10}; do
    echo -n "  Run $i/10: "
    
    # Run the test with 60s timeout
    if timeout 60 go test -v "$TEST_FILE" > /tmp/test_output_$$.log 2>&1; then
        echo -e "${GREEN}PASS${NC}"
    else
        echo -e "${RED}FAIL${NC}"
        echo "    Error output:"
        tail -n 20 /tmp/test_output_$$.log | sed 's/^/    /'
        FILE_SUCCESS=false
        break
    fi
done

# Clean up
rm -f /tmp/test_output_$$.log

if [ "$FILE_SUCCESS" = true ]; then
    echo -e "\n${GREEN}✓ All 10 runs passed${NC}"
    exit 0
else
    echo -e "\n${RED}✗ Test failed${NC}"
    exit 1
fi