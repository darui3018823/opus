# Performance Phase 3-1 Benchmark Harness

Last updated: 2026-07-17

## Objective

Create a unified public performance benchmark harness for representative Opus
workloads before starting optimization work.

The harness must cover encode and decode for:

- mono and stereo
- 20 ms frames
- 48 kHz public API paths
- CELT-only, SILK-only, and hybrid routing
- `-benchmem` allocation reporting

## Scope

- Add root-package benchmarks that exercise the public `Encoder` and `Decoder`
  APIs.
- Validate each benchmark packet's intended Opus mode and duration before
  timing starts.
- Record the local baseline in `docs/PERF_BASELINE.md`.
- Do not change codec behavior, packet generation logic, or benchmarked public
  API semantics.

Out of scope:

- libopus comparative benchmarks.
- Micro-optimizations. Phase 3 optimization iterations start only after this
  harness is adopted.
- Any behavior-changing quality or rate-control work.

## Acceptance Criteria

- `go test -run '^$' -bench '^BenchmarkPerf/' -benchmem .` runs all 12
  encode/decode workload cases.
- Each benchmark setup checks TOC mode and packet duration before
  `b.ResetTimer`.
- `docs/PERF_BASELINE.md` records the local command, environment, benchmark
  workload definitions, and baseline results.
- Standard gates pass:

```text
go vet ./...
go test -count=1 ./...
go test -count=1 -tags opusref ./...
```

## Verification Commands

```text
go test -run '^$' -bench '^BenchmarkPerf/' -benchtime=1x -benchmem .
go test -run '^$' -bench '^BenchmarkPerf/' -benchtime=200ms -count=5 -benchmem .
go vet ./...
go test -count=1 ./...
go test -count=1 -tags opusref ./...
```
