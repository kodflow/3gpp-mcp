#!/bin/bash
# ============================================================================
# run-all.sh - Run all hook tests
# ============================================================================

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TOTAL_FAILURES=0

echo "═══════════════════════════════════════════════"
echo "  Hook Test Suite"
echo "═══════════════════════════════════════════════"
echo ""

for test_file in "$SCRIPT_DIR"/*.test.sh; do
    [ -f "$test_file" ] || continue
    echo ""
    bash "$test_file"
    TOTAL_FAILURES=$((TOTAL_FAILURES + $?))
    echo ""
done

echo ""
echo "═══════════════════════════════════════════════"
if [ "$TOTAL_FAILURES" -eq 0 ]; then
    echo "  ALL SUITES PASSED"
else
    echo "  TOTAL FAILURES: $TOTAL_FAILURES"
fi
echo "═══════════════════════════════════════════════"

exit $TOTAL_FAILURES
