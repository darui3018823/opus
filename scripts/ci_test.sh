#!/bin/bash
# CI Test Script for Opus Pure Go Library
# This script runs all tests and verification tools.

set -e  # Exit on error

echo "=== Opus Pure Go Library CI Test ==="
echo ""

# Check for libopus tools (optional, for bitstream comparison)
if command -v opusenc &> /dev/null && command -v opusdec &> /dev/null; then
    echo "✓ libopus tools found (opusenc, opusdec)"
    LIBOPUS_AVAILABLE=1
else
    echo "⚠ libopus tools not found - bitstream comparison tests will be skipped"
    LIBOPUS_AVAILABLE=0
fi
echo ""

# Run Go tests
echo "=== Running Go Tests ==="
go test -v ./...

# Run benchmarks (short)
echo ""
echo "=== Running Benchmarks ==="
go test -bench=. -benchtime=100ms ./...

# Build verification
echo ""
echo "=== Build Verification ==="
go build ./...

echo ""
echo "=== CI Test Complete ==="
