@echo off
REM Local CI equivalent for the Opus Pure Go library (Windows cmd).
REM
REM Mirrors the GitHub Actions workflows in .github/workflows (test / race / bench).
REM Equivalent ci_test.sh (POSIX) and ci_test.ps1 (PowerShell) are also provided.
REM
REM Usage:
REM   scripts\ci_test.bat
REM   set RUN_OPUSREF=1 && scripts\ci_test.bat    REM also libopus reference
REM
REM Exit status is non-zero if any check fails (all checks still run).

setlocal enabledelayedexpansion
set FAIL=0

echo === Opus Pure Go - local CI ===
go version

echo.
echo === build ===
go build ./...
if errorlevel 1 (echo FAIL: build & set FAIL=1) else (echo PASS: build)

echo.
echo === vet ===
go vet ./...
if errorlevel 1 (echo FAIL: vet & set FAIL=1) else (echo PASS: vet)

echo.
echo === test ===
go test -count=1 ./...
if errorlevel 1 (echo FAIL: test & set FAIL=1) else (echo PASS: test)

echo.
echo === race ===
set HAVE_CC=0
where gcc >nul 2>&1 && set HAVE_CC=1
where clang >nul 2>&1 && set HAVE_CC=1
if "!HAVE_CC!"=="1" (
    go test -race -count=1 ./...
    if errorlevel 1 (echo FAIL: race & set FAIL=1) else (echo PASS: race)
) else (
    echo SKIP: no C compiler ^(gcc/clang^) found; -race requires cgo
)

echo.
echo === bench ===
go test -run="^$" -bench=. -benchmem -benchtime=10x ./...
if errorlevel 1 (echo FAIL: bench & set FAIL=1) else (echo PASS: bench)

echo.
echo === reference (opusref) ===
if "%RUN_OPUSREF%"=="1" (
    go test -tags opusref -run TestCGORef .
    if errorlevel 1 (echo FAIL: reference & set FAIL=1) else (echo PASS: reference)
) else (
    echo SKIP: set RUN_OPUSREF=1 to run the libopus comparison ^(needs libopus^)
)

echo.
if !FAIL! EQU 0 (echo === ALL CHECKS PASSED ===) else (echo === SOME CHECKS FAILED ===)
endlocal & exit /b %FAIL%
