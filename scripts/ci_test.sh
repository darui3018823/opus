#!/usr/bin/env bash
# Local CI equivalent for the Opus Pure Go library.
#
# Mirrors the GitHub Actions workflows in .github/workflows (test / race / bench)
# so you can run the same checks before pushing. Equivalent ci_test.ps1 and
# ci_test.bat are provided for Windows.
#
# Usage:
#   scripts/ci_test.sh                 # build, vet, test, bench (+ race if cgo)
#   RUN_OPUSREF=1 scripts/ci_test.sh   # also run the libopus reference compare
#
# Exit status is non-zero if any check fails (all checks still run).

set -uo pipefail

fail=0

run() { # run "<label>" <cmd...>
  local label="$1"; shift
  echo ""
  echo "=== ${label} ==="
  echo "+ $*"
  if "$@"; then
    echo "PASS: ${label}"
  else
    echo "FAIL: ${label}"
    fail=1
  fi
}

echo "=== Opus Pure Go - local CI ==="
go version

run "build" go build ./...
run "vet"   go vet ./...
run "test"  go test -count=1 ./...

# The race detector requires a C toolchain (cgo).
if command -v gcc >/dev/null 2>&1 || command -v clang >/dev/null 2>&1; then
  run "race" go test -race -count=1 ./...
else
  echo ""
  echo "=== race ==="
  echo "SKIP: no C compiler (gcc/clang) found; -race requires cgo"
fi

run "bench" go test -run='^$' -bench=. -benchmem -benchtime=10x ./...

# Optional libopus reference comparison (needs a C toolchain + libopus).
if [ "${RUN_OPUSREF:-0}" = "1" ]; then
  run "reference (opusref)" go test -tags opusref -run TestCGORef .
else
  echo ""
  echo "=== reference (opusref) ==="
  echo "SKIP: set RUN_OPUSREF=1 to run the libopus comparison (needs libopus)"
fi

echo ""
if [ "${fail}" -eq 0 ]; then
  echo "=== ALL CHECKS PASSED ==="
else
  echo "=== SOME CHECKS FAILED ==="
fi
exit "${fail}"
