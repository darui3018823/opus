# Phase 3 Iteration Log

Integration branch: `codex/phase3-perf-harness`.

## Iteration 1: unified public performance benchmark harness (Qualified)

### Implemented locally

- Added `perf_benchmark_test.go` with `BenchmarkPerf`.
- Covered public encode and decode workloads for CELT-only, SILK-only, and
  hybrid routing across mono and stereo 48 kHz 20 ms frames.
- Setup validates each generated packet's TOC mode and packet duration before
  benchmark timing starts.
- Encode benchmarks pre-generate PCM frames and measure only codec calls.
- Decode benchmarks pre-generate valid packet sequences and reuse the output
  destination buffer.
- Added `docs/PERF_BASELINE.md` to make Phase 3 optimization comparisons
  measurement-driven.

### Qualification observations

Smoke benchmark passed:

```text
go test -run '^$' -bench '^BenchmarkPerf/' -benchtime=1x -benchmem .
```

Baseline benchmark passed:

```text
go test -run '^$' -bench '^BenchmarkPerf/' -benchtime=200ms -count=5 -benchmem .
```

Standard gates passed:

```text
go vet ./...
go test -count=1 ./...
go test -count=1 -tags opusref ./...
```

### Decision

Adopted as the Phase 3-1 baseline harness. Phase 3 optimization iterations may
now use `BenchmarkPerf` and `docs/PERF_BASELINE.md` for before/after
comparisons.
