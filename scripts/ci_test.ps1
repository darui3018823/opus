#!/usr/bin/env pwsh
# Local CI equivalent for the Opus Pure Go library (PowerShell).
#
# Mirrors the GitHub Actions workflows in .github/workflows (test / race / bench).
# This is the recommended runner on Windows (CGO builds need PowerShell + MSYS2).
#
# Usage:
#   pwsh scripts/ci_test.ps1
#   $env:RUN_OPUSREF = "1"; pwsh scripts/ci_test.ps1   # also libopus reference
#
# Exit status is non-zero if any check fails (all checks still run).

$ErrorActionPreference = 'Continue'
$script:Fail = 0

function Invoke-Step {
    param(
        [string]$Label,
        [scriptblock]$Cmd
    )
    Write-Host ""
    Write-Host "=== $Label ==="
    & $Cmd
    if ($LASTEXITCODE -ne 0) {
        Write-Host "FAIL: $Label"
        $script:Fail = 1
    }
    else {
        Write-Host "PASS: $Label"
    }
}

Write-Host "=== Opus Pure Go - local CI ==="
go version

Invoke-Step "build" { go build ./... }
Invoke-Step "vet"   { go vet ./... }
Invoke-Step "test"  { go test -count=1 ./... }

# The race detector requires a C toolchain (cgo).
if ((Get-Command gcc -ErrorAction SilentlyContinue) -or (Get-Command clang -ErrorAction SilentlyContinue)) {
    Invoke-Step "race" { go test -race -count=1 ./... }
}
else {
    Write-Host ""
    Write-Host "=== race ==="
    Write-Host "SKIP: no C compiler (gcc/clang) found; -race requires cgo"
}

Invoke-Step "bench" { go test '-run=^$' -bench=. -benchmem -benchtime=10x ./... }

# Optional libopus reference comparison (needs a C toolchain + libopus).
if ($env:RUN_OPUSREF -eq "1") {
    Invoke-Step "reference (opusref)" { go test -tags opusref -run TestCGORef . }
}
else {
    Write-Host ""
    Write-Host "=== reference (opusref) ==="
    Write-Host 'SKIP: set $env:RUN_OPUSREF="1" to run the libopus comparison (needs libopus)'
}

Write-Host ""
if ($script:Fail -eq 0) {
    Write-Host "=== ALL CHECKS PASSED ==="
}
else {
    Write-Host "=== SOME CHECKS FAILED ==="
}
exit $script:Fail
