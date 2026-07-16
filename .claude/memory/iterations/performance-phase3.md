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

## Iteration 2: reuse SILK NSQ state restore buffers (Qualified)

### Implemented locally

- Added `silkNSQState.copyFrom` and changed `restoreFrameState` to copy saved
  NSQ history into the encoder's existing buffers when capacity permits.
- Kept `snapshotFrameState` as a deep copy so speculative rate-control trials
  cannot mutate the saved initial state.
- Added `TestNSQStateCopyFromDoesNotAliasHistory` to guard that restored NSQ
  history does not alias the saved source.

### Measurement

Command:

```text
go test -run '^$' -bench '^BenchmarkPerf/' -benchtime=200ms -count=5 -benchmem .
```

Median comparison against `docs/PERF_BASELINE.md`:

| Benchmark | Baseline ns/op | New ns/op | Time | Baseline B/op | New B/op | Allocation |
|---|---:|---:|---:|---:|---:|---:|
| `encode/silk/mono/48k/20ms` | 5594485 | 5118559 | -8.5% | 683858 | 686536 | +0.4% |
| `encode/silk/stereo/48k/20ms` | 20143800 | 18465131 | -8.3% | 5095172 | 4663431 | -8.4% |
| `encode/hybrid/mono/48k/20ms` | 3544840 | 3413251 | -3.7% | 670307 | 657077 | -2.0% |
| `encode/hybrid/stereo/48k/20ms` | 12391829 | 12004582 | -3.1% | 3226950 | 2962475 | -8.2% |

CELT-only and decode workloads are not expected to move because this change is
confined to SILK encoder speculative state rollback.

### Qualification observations

Targeted tests passed:

```text
go test -count=1 ./internal/silk -run "TestNSQStateCopyFromDoesNotAliasHistory|TestTrellisNSQVoicedRoundTrip|TestHomebrewToTrellisNSQStateHandoff|TestLTPSumLogGainStateRollback|TestNSQScaleBoundaryXQUsesFullGainPrecision" -v
go test -count=1 . -run "TestEncoderSILKOnlyStereoQualityBaseline|TestEncoderHybridVoiceRoundTrip|TestEncoderHybridSelectionBoundariesStrict" -v
go test -count=1 -tags opusref -run "TestCGOStereoTrellisFinalRange|TestCGOHybridTrellisFinalRange|TestOpusSILKStereoABAgainstLibopusEncoder|TestOpusSILKHybridABAgainstLibopusEncoder|TestCGOEncodeRefSILKOnly|TestCGOEncodeRefHybrid" -v .
```

Standard gates passed:

```text
go vet ./...
go test -count=1 ./...
go test -count=1 -tags opusref ./...
```

### Decision

Adopted. The iteration meets the Phase 3 threshold through at least 5%
improvement in SILK encode time and hybrid stereo allocation, while preserving
the targeted final-range, quality, and opusref interoperability guards.

Follow-up candidate: a separate iteration can hoist `analyzeNoiseShapeFLP`
per-subframe scratch buffers. Profiles still show that function as a major
allocation hotspot after this change.
