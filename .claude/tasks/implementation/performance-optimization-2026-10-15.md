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
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -a -gcflags='all=-S' ./... 2>&1 | grep -E '\b(FMADD[SD]|FMSUB[SD]|FNMADD[SD]|FNMSUB[SD])\b' | grep -F Programs/Go/opus
```

## Progress log

- 2026-10-15 `27e9938`, `327ee87`: guards and benchmarks added
  (`perf_vectors_test.go`, `perf_libopus_policy_test.go`, CELT digests in
  `perf_benchmark_test.go`). Note: `BenchmarkPerf` pins `ModePolicyLegacy`;
  `BenchmarkPerfDefault` measures the default libopus policy.
- 2026-10-15 `a501b5f` D1 done: dense `pvqUTable` (64x320 uint32) replaces
  the `sync.Map` lookup; `TestPVQUTableMatchesRecurrence` checks every entry.
  Decode -16% to -43% on CELT/hybrid vectors, CELT encode -8%.
- 2026-10-15 `357f955` D2 part 1: `CELTMode` keeps backward-MDCT scratch,
  `CLTMDCTBackwardInto` writes into a caller buffer (`Decoder.synthBuf` on the
  non-transient path), FFT plans are a static array, `transform` uses a stack
  `fstride`. Decode another -5% to -9% on CELT vectors, allocations -35%.

- 2026-10-15 `5fdf739` D2 part 2: `decodeCELTRange` reuses decoder-owned
  synthesis buffers; `celt.Decoder.Decode`/`DecodeHybrid` now return a slice
  valid until the next decode call (every root-package caller copies it
  immediately). CELT vectors -2% to -5%, allocations -13% to -33%.
- 2026-10-15 `1fc230d` D2 part 3: `AssembleMDCTInto`, `quantAllBandsScratch`
  (decoder and CELT encoder), decoder `bandX`/`collapse`, stack arrays
  (`maxPVQN` = 176) for the cwrsi pulse vector and the Hadamard reorders.
  `CopyAllStateFrom` clears the scratch after its struct copy so a clone never
  shares buffers. CELT vectors -6% to -12%, allocations -30% to -57%, CELT
  encode -5%.

- 2026-10-15 `cebd0e6` D3 part 1: the packet decoders build their PCM in
  `Decoder.pcmScratch`; the public `DecodeFloat` copies it out, while
  `decodeFloatShared` (used by `Decode24`, `DecodeFloat32` and multistream)
  does not; the int16 soft clip reuses `softclipScratch`. CELT vectors now
  allocate 3-7 MiB instead of 283 MiB.
- 2026-10-15 `2a2eb49`, `758afa0` D3 part 2: 8 kHz and 12 kHz digests added
  (taken from v1.6.0 `2edb1c2`, identical to the optimised code). The SILK
  resampler gained `ProcessAppend` and reusable work buffers; `resampleSILK`
  returns `Decoder.silkResampled`, valid until its next call. SILK vectors
  -12% to -16%.
- 2026-10-15 `283509e` D3 part 3: SILK `synthesize` work buffers are decoder
  scratch (cleared on use, dropped by `CopyAllStateFrom`). SILK -4%.

| Vector | baseline | now | pion |
|---|---:|---:|---:|
| tv01 | 328 ms | 179 ms | 110 ms |
| tv05 | 181 ms | 127 ms | 95 ms |
| tv09 | 343 ms | 144 ms | 98 ms |
| tv10 | 384 ms | 187 ms | 127 ms |
| tv11 | 272 ms | 128 ms | 86 ms |
| tv02 (SILK) | 54 ms | 44 ms | 39 ms |
| tv12 (SILK) | 70 ms | 54 ms | 43 ms |
| all 12 | 2.54 s | 1.30 s | 0.90 s |

GC is no longer visible in the decode profile. The remaining CELT gap is
compute: every CELT buffer is `[]float64` holding float32-rounded values, so
each operation converts float32<->float64 (`imdctRawInto`, `quantPartition`,
`decodeCELTRange`). Moving CELT to `[]float32` storage is the next large
lever but touches the whole CELT package; do it only with the digests and
the opusref suite green at every step.

- 2026-10-15 `bfed07e` E2: `silkTraceSNREnabled` read once; call sites
  test it before boxing the trace arguments. SILK encode allocs -10%.
- 2026-10-15 `1a63ff7` E1: with `haveShape32` (set on every frame, never
  cleared) `applyShape32` provides every shape field the NSQ reads and the
  LPC_in_pre gains, so the float64 `analyzeNoiseShapeFLP` is skipped in
  `closedLoopNSQWithRateScale` and `shapeGainAnalysis` is no longer called
  from `encodeRangeFrame`. SILK/hybrid encode -21% to -26%. Full
  `go test -tags opusref ./...` passed on this commit (7m24s).
- 2026-10-15 `e252745` E6 (part): `pitchXcorr32` computes four lags per pass
  (each lag still sums in j order) without bounds checks. CELT encode -14%
  to -21%.

- 2026-10-15 `679e3cc`, `1555b07`, `b6daa13` E3 (part): reuse the
  conditioned-input and CELT delay buffers (`conditionBuf`, `delayJoined`;
  `lastConditionedInput` is now valid until the next call), forward MDCT
  outputs (`CLTMDCTForwardInto`, encoder `mdctOut`/`mdctCoeffs`/`mdctLong`),
  SILK `pulseBlocks`, `nsqScratch` and `pitchBuf`; `shellSumsFit` works in a
  stack array and `pitchResidual` takes `r.res` without a copy.

Note on the FMA check: run it with `-a`
(`GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -a -gcflags='all=-S' ./...`);
without `-a`, cached packages print no assembly and the check passes
vacuously.

| Encoder workload | baseline | now | alloc baseline | alloc now |
|---|---:|---:|---:|---:|
| celt/mono/96k | 202 µs | 128 µs | 63 KiB | 23 KiB |
| celt/stereo/128k | 312 µs | 206 µs | 109 KiB | 35 KiB |
| silk/mono/12k | 738 µs | 496 µs | 245 KiB | 118 KiB |
| silk/stereo/12k | 793 µs | 532 µs | 274 KiB | 129 KiB |
| hybrid/mono/24k | 426 µs | 300 µs | 179 KiB | 89 KiB |
| hybrid/stereo/36k | 1151 µs | 801 µs | 424 KiB | 212 KiB |

## Next steps (in order)

From an Opus read-only review on 2026-10-15 (commit `52969ac`; estimates
unless marked measured). Every item below keeps output bit-identical by
construction (lossless float32<->float64 round trips, integer work or buffer
reuse); guard each with the digest tests.

1. Decoder MDCT (`internal/dsp/mdct_celt.go` `imdctRawInto`,
   `fft_opus.go` `opusFFTInto`): write the pre-rotation straight into
   `opusComplex` at `fout[bitrev[i]]`, post-rotate from `fout`, use a
   precomputed per-mode twiddle slice instead of `libopusMDCTTwiddle`
   (not inlined, called 4x per element). Measured in an experiment: 960-pt
   8.7 -> 4.7 µs. Same for `CLTMDCTForwardInto` (encoder).
2. `internal/celt/decoder.go` output loop
   (`for i := 0; i < frameSize/d.downsample; i++ { output[i*cc+c] = samplesOut[i*d.downsample] ... }`):
   hoist `frameSize/d.downsample` and `d.downsample` into locals (the loop
   divides and reloads per sample; verified by reading the code).
3. `celtPVQU` fast path: make the table lookup inlinable (keep the slow
   path in a separate function); avoid clearing the whole `[176]int` in
   `algUnquant` (only `n` entries are used).
4. Output conversion (`finishOutput`, `floatToInt16`, `SoftClipFloat32`):
   soft clip in float32 and convert to int16 in one pass.
5. `ApplyFineEnergy`/`ApplyFinalFineEnergy` (`internal/celt/bands.go`) write
   `band.Energy` that `decodeCELTRange` overwrites before any read
   (verified); drop the `math.Exp2` work if PLC and the digests agree.
6. Encoder allocations (GC is ~10-12% of encode CPU): `opPVQSearch`/
   `algQuant` (16% of encode bytes), `snapshotFrameState` and
   `silkNSQState.clone` (double-buffer), hybrid `celtChunk` copies
   (`opus.go` `encodeHybridPacket`), pitch-analysis scratch, `tfAnalysis`,
   `buildLPCInPre`/`lpcInPreInput`/`exactProcessGains`.
7. Gate the SILK encoder traces (`recordNSQTrace`, `loopTrace`,
   `traceLPCInPre`, `pendingTrace` copies) behind a flag; the opusref
   oracle tests read `LastFrameTrace`/`PacketFrameTraces` and must enable
   it.
8. SILK decode: keep int16 through `decodeFrame`, stereo and resampling
   and convert to float once (today int16 -> float64 -> int16 -> ...);
   remove the remaining per-frame allocations in `decodeMultiStereoEC`,
   `decodeFrame`, `stereoMSToLR`, `decodePulses`, `decodeNLSF`.
9. NSQ (`internal/silk/nsq_del_dec.go`): add an assertion that the
   short-prediction index never goes out of range, then reslice windows to
   drop bounds checks and preallocate `sampleState`.
10. Smaller: postfilter history branch per sample, SILK LPC synthesis and
    `iirFIRInterpol` bounds checks; remove the unreachable
    `selectRateControlPlan` branch.
11. Only after re-profiling: CELT `[]float32` storage. pion's CELT speed
    comes from float32 buffers, precomputed trig slices and bit reversal
    applied in the pre-rotation, all compatible with bit-exactness, but the
    migration needs an audit of every float64 expression. Medium risk.

Do not: reorder float sums, unroll float32 reductions into partial sums,
replace `math.Exp2`/`Floor`/`RoundToEven` with approximations, or write
`x*y+z` without explicit `float32(...)` rounding.

Before opening the PR: `go test ./...`, `go test -race ./...`, PowerShell
`go test -tags opusref ./...`, the arm64 FMA check with `-a`, update
`docs/PERF_BASELINE.md`, draft PR.

Re-run the pion comparison with the harness in the session scratchpad
(`vcmp/`); if it is gone, rebuild it from the description in this brief:
decode each `testdata/opus_newvectors/testvectorNN.bit` with both libraries
at 48 kHz stereo int16 and time it with `testing.B`.
