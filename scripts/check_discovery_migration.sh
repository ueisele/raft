#!/bin/bash

# Script to check if your codebase needs migration for the PeerDiscovery changes
# Usage: ./scripts/check_discovery_migration.sh [path_to_check]

set -e

# Color codes for output
RED='\033[0;31m'
YELLOW='\033[1;33m'
GREEN='\033[0;32m'
NC='\033[0m' # No Color

# Default to current directory if no path specified
SEARCH_PATH="${1:-.}"

echo "Checking for HTTPTransport usage that needs migration..."
echo "Searching in: $SEARCH_PATH"
echo ""

# Track if any issues found
ISSUES_FOUND=0

# Check for NewHTTPTransport usage
echo "1. Checking for NewHTTPTransport usage..."
if grep -r "NewHTTPTransport(" "$SEARCH_PATH" --include="*.go" --exclude-dir=vendor 2>/dev/null | grep -v "NewHTTPTransportWithDiscovery\|NewHTTPTransportWithStaticPeers"; then
    echo -e "${YELLOW}⚠️  Found usage of NewHTTPTransport that may need migration${NC}"
    ISSUES_FOUND=1
else
    echo -e "${GREEN}✓ No problematic NewHTTPTransport usage found${NC}"
fi
echo ""

# Check for SetDiscovery usage (indicates old pattern)
echo "2. Checking for SetDiscovery usage..."
if grep -r "\.SetDiscovery(" "$SEARCH_PATH" --include="*.go" --exclude-dir=vendor 2>/dev/null; then
    echo -e "${YELLOW}⚠️  Found SetDiscovery usage - consider migrating to constructor-based approach${NC}"
    ISSUES_FOUND=1
else
    echo -e "${GREEN}✓ No SetDiscovery usage found${NC}"
fi
echo ""

# Check for transport creation without immediate discovery setup
echo "3. Checking for potential missing discovery setup..."
# This is a heuristic - looks for NewHTTPTransport not followed by SetDiscovery within 10 lines
TEMP_FILE=$(mktemp)
find "$SEARCH_PATH" -name "*.go" -not -path "*/vendor/*" -exec grep -n "NewHTTPTransport(" {} + 2>/dev/null | while read -r line; do
    FILE=$(echo "$line" | cut -d: -f1)
    LINE_NUM=$(echo "$line" | cut -d: -f2)
    
    # Check if SetDiscovery appears within next 10 lines
    if ! sed -n "${LINE_NUM},$((LINE_NUM + 10))p" "$FILE" | grep -q "SetDiscovery"; then
        echo "$FILE:$LINE_NUM" >> "$TEMP_FILE"
    fi
done

if [ -s "$TEMP_FILE" ]; then
    echo -e "${RED}❌ Found NewHTTPTransport calls without nearby SetDiscovery:${NC}"
    cat "$TEMP_FILE"
    ISSUES_FOUND=1
else
    echo -e "${GREEN}✓ All NewHTTPTransport calls appear to have SetDiscovery${NC}"
fi
rm -f "$TEMP_FILE"
echo ""

# Summary and recommendations
echo "========================================="
if [ $ISSUES_FOUND -eq 0 ]; then
    echo -e "${GREEN}✅ No migration issues found!${NC}"
    echo "Your code appears to be ready for the PeerDiscovery changes."
else
    echo -e "${YELLOW}⚠️  Migration recommended${NC}"
    echo ""
    echo "Recommended actions:"
    echo "1. Replace NewHTTPTransport + SetDiscovery with NewHTTPTransportWithDiscovery"
    echo "2. Use NewHTTPTransportWithStaticPeers for simple static configurations"
    echo "3. Ensure all transports have discovery configured before Start()"
    echo ""
    echo "Example migration:"
    echo "  Old:"
    echo "    transport := http.NewHTTPTransport(config)"
    echo "    transport.SetDiscovery(discovery)"
    echo ""
    echo "  New:"
    echo "    transport, err := http.NewHTTPTransportWithDiscovery(config, discovery)"
    echo ""
    echo "See MIGRATION_PEERDISCOVERY.md for detailed migration guide."
fi
echo "========================================="

exit $ISSUES_FOUND