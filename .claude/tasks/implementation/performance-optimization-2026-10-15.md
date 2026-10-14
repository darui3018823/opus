# Performance Optimisation (v1.7 cycle)

- Status: in progress
- Last updated: 2026-10-15
- Branch: `perf/decoder-encoder-optimization`

## Objective

Close the decode-speed gap to pion/opus (2.8x slower on the RFC 8251 vectors)
and cut encoder CPU and allocation cost, without changing a single decoded
sample or encoded byte.

## Scope

- Decoder and encoder hot paths in the root package, `internal/celt`,
  `internal/silk`, `internal/dsp` and `internal/resampler`.
- No public API removal. New allocation-free API variants are allowed when the
  existing API forces an allocation.
- Out of scope: algorithmic changes that alter output, SIMD assembly, and
  changes to float evaluation order (byte identity with libopus depends on it).

## Constraints

- Output must stay bit-identical: decoded PCM, final range and encoded
  packets.
- No fused multiply-add on arm64 (CI step "No fused multiply-add on FMA
  architectures"). Keep explicit `float32(...)` rounding where it exists.
- Keep float32 and float64 summation order unchanged when rewriting loops.

## Baseline (2026-10-15, i7-11700, Go 1.26.5, windows/amd64, commit 2edb1c2)

Official vectors, 48 kHz stereo int16, fresh decoder per vector:

| Vector | time | alloc | allocs | pion/opus time |
|---|---:|---:|---:|---:|
| tv01 (CELT) | 328 ms | 283 MiB | 341k | 109 ms |
| tv02 (SILK) | 54 ms | 85 MiB | 78k | 39 ms |
| tv05 (hybrid) | 181 ms | 297 MiB | 186k | 95 ms |
| tv10 (CELT/mixed) | 384 ms | 348 MiB | 427k | 127 ms |
| tv12 (SILK) | 70 ms | 123 MiB | — | 43 ms |

Default-policy encoder (`BenchmarkPerfDefault`, 20 ms frames):

| Workload | time/frame |
|---|---:|
| celt/mono/96k | 202 µs |
| celt/stereo/128k | 312 µs |
| silk/mono/12k | 738 µs |
| silk/stereo/12k | 793 µs |
| hybrid/mono/24k | 426 µs |
| hybrid/stereo/36k | 1151 µs |

## Issues found by profiling

Decoder (CPU profile over all 12 vectors):

| ID | Issue | Cost | Location |
|---|---|---|---|
| D1 | `celtPVQU` looks up U(n,k) in a `sync.Map` with an interface key on every call | 17% CPU (`HashTrieMap.Load`) | `internal/celt/pvq.go:353` |
| D2 | CELT synthesis allocates per frame: `IMDCTRaw`, `CLTMDCTBackward`, `opusFFT`, `decodeCELTRange`, `AssembleMDCT`, `quantAllBandsImpl`, `decodeBandCoeffs`, `cwrsiLibopus` | ~65% of 2.6 GB allocated; GC ~12% CPU | `internal/dsp/mdct*.go`, `internal/celt/` |
| D3 | Top-level decode allocates per packet: `decodePacketFloat`, `finishOutput`, `interleaveSILKOut`, `resampleSILK`, SILK `synthesize` and resampler `iirFIR` | ~30% of allocations | `opus.go`, `internal/silk/` |
| D4 | `DecodeFloat` / `DecodeFloat32` return a new slice per call | API forces allocation | `opus.go` |

Encoder (default libopus policy):

| ID | Issue | Cost | Location |
|---|---|---|---|
| E1 | SILK runs both the float64 `analyzeNoiseShapeFLP` and the float32 libopus `noiseShapeFLP32Trace`; check whether the float64 result is still used on the libopus path | `silkWarpedAutocorrelationFLP` 6.4% | `internal/silk/encoder.go:888`, `noise_shape.go:295` |
| E2 | `silkTraceSNR` calls `os.Getenv` on every call | small, per frame | `internal/silk/debug_trace.go:9` |
| E3 | Per-frame allocations: SILK ~1.1k allocs/frame (`ltpResidualEnergyPerSubframe`, `lpcInPreInput`, `shellSumsFit`, `makePulseBlocks`, `silkNSQState.clone`), CELT ~200 | GC ~6% CPU | `internal/silk/`, `internal/celt/` |
| E4 | CELT encoder `celtPVQU` uses the same `sync.Map` as D1 | 3% | `internal/celt/pvq.go` |
| E5 | Legacy policy only: `detectSignalBandwidthAndSparsity` runs a generic FFT computing `cos`/`sin` per call | 13% of legacy encode | `bandwidth_detect.go`, `internal/dsp/fft.go` |
| E6 | Compute-bound kernels: `silkNoiseShapeQuantizerDelDec`, `pitchXcorr32`, `runPrefilter` | 6–14% each | float order fixed; only bounds-check and loop overhead work |

## Acceptance criteria

- `TestOfficialVectorDecodeDigests`, `TestPerfPredictivePacketRegression` and
  `TestPerfDefaultPolicyPacketRegression` pass unchanged.
- `go test ./...`, `go test -race ./...` and `go test -tags opusref ./...`
  pass (opusref via PowerShell).
- The arm64 FMA check finds no site.
- Each optimisation commit shows a benchstat improvement on the benchmark it
  targets; `docs/PERF_BASELINE.md` records the final numbers.

## Verification commands

```text
go test -count=1 -run 'TestOfficialVectorDecodeDigests|TestPerfPredictivePacketRegression|TestPerfDefaultPolicyPacketRegression' .
go test -run '^$' -bench 'BenchmarkDecodeOfficialVectors|BenchmarkPerfDefault' -benchmem -benchtime 2s -count 6 . > new.txt
benchstat base.txt new.txt
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -gcflags='all=-S' ./... 2>&1 | grep -E '\b(FMADD[SD]|FMSUB[SD]|FNMADD[SD]|FNMSUB[SD])\b'
```

## Progress log

- 2026-10-15: guards and benchmarks added (`perf_vectors_test.go`,
  `perf_libopus_policy_test.go`, CELT digests in `perf_benchmark_test.go`).
