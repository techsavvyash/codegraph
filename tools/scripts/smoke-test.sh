#!/usr/bin/env bash
# smoke-test.sh — validates a working codegraph binary
set -euo pipefail

BINARY="${1:-./bin/codegraph}"

echo "=== CodeGraph Smoke Test ==="
echo "Binary: $BINARY"

if [[ ! -f "$BINARY" ]]; then
  echo "ERROR: Binary not found at $BINARY"
  exit 1
fi

echo ""
echo "1. --help exits 0"
"$BINARY" --help > /dev/null 2>&1
echo "   ✓"

echo "2. index --help"
"$BINARY" index --help > /dev/null 2>&1
echo "   ✓"

echo "3. query --help"
"$BINARY" query --help > /dev/null 2>&1
echo "   ✓"

echo ""
echo "=== Smoke tests passed ==="
