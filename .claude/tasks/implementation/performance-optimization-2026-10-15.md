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

- 2026-10-15 06:30 (scheduled run) review items 1-5:
  - `089d843` static-size MDCTs rotate straight through the float32 FFT
    buffer with a cached twiddle slice (`imdctRawStatic`,
    `cltMDCTForward(static)`); `TestIMDCTRawStaticMatchesGeneric` and
    `TestMDCTForwardStaticMatchesGeneric` compare against the generic path
    bit for bit. CELT decode -6% to -11%, CELT encode -3% to -6%.
  - `be670d8` output loop hoists `frameSize/d.downsample`. -1% to -3%.
  - `8c82d9e` shorter `celtPVQU` table path (still not inlinable: cost 88
    of 80) and the pulse vector lives in `quantAllBandsScratch.iy`.
    -1% to -5% on CELT vectors.
  - `5bbd5ba` `Decode` soft-clips into float32 and converts float32 to int16
    directly (`decodeSoftClipped32`, `float32ToInt16`). -1% to -4%.
  - `c7a3afb` `ApplyFineEnergy`/`ApplyFinalFineEnergy` removed: their
    `band.Energy` writes were always overwritten in `decodeCELTRange`
    (`decodeBandCoeffs` has no error return in between). -1% to -3%.

  - `db0dde4` (item 6, part) `opPVQSearch`/`algQuant` vectors on the stack:
    CELT encode -5% to -7%, CELT encode bytes -45% to -62%.
  - `b114619` (item 6, part) hybrid HB-gain/stereo-width fades copy into
    `Encoder.hybridFadeBuf`: hybrid encode -2%, bytes -10% to -15%.
  - Full `go test ./...`, the `-a` FMA check and `go test -tags opusref ./...`
    (7m19s) pass on `b114619`.

| Vector | v1.6.0 | after pass 1 | now |
|---|---:|---:|---:|
| tv01 | 328 ms | 175 ms | 150 ms |
| tv02 | 54 ms | 42 ms | 41 ms |
| tv05 | 181 ms | 123 ms | 108 ms |
| tv09 | 343 ms | 144 ms | 123 ms |
| tv10 | 384 ms | 185 ms | 160 ms |
| tv11 | 272 ms | 127 ms | 108 ms |

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

- 2026-10-15 afternoon (user said to continue): items 6 and 7.
  - `db3cbd8` SILK pitch analysis (`silkFindPitchLagsFLP32`,
    `silkPitchAnalysisCoreFLP32`) works in `Encoder.pitchScratch`; every
    buffer is cleared when handed out, like the old `make`. SILK/hybrid
    encode -2% to -4%, bytes -12% to -20%.
  - `fa8ca5d` frame state snapshots (`snapshotFrameStateInto`,
    `silkNSQState.cloneInto`) reuse three per-encoder buffers (initial,
    rate-loop "lower", LBRR); each snapshot lives within one frame. Bytes
    -8% to -21%, time within noise.
  - `2042bc4` noise-shape `x`/`pitchRes`/`pitchL`, `lpcInPreInputInto`,
    `buildLPCInPreInto` and the process_gains `scaled`/LPC arrays use
    `Encoder.frameScratch`. Interleaved run: hybrid/mono -3.6%, bytes -27% to
    -33%.
  - `a0fb31a` CELT `pitchSearch32`, `removeDoubling32` and `tfAnalysis` use
    stack arrays with a `make` fallback for larger sizes. CELT encode bytes
    -79% to -85%, time within noise.
  - `3b43b5e` (item 7) FrameTrace recording runs only while
    `silk.EnableFrameTraces()` is held; the seven opusref test functions that
    read `LastFrameTrace`/`PacketFrameTraces` call it. SILK encode -3% to -5%,
    allocations -19% to -41%. The trace-reading opusref oracle tests pass
    (PowerShell, 4m54s).
  - Full `go test -tags opusref ./...` passes on `0057598` (root 453s).
  - After rebasing onto the cloud commits (`1f6e3ba`..`67340e0`): `go test ./...`,
    the `-a` FMA check and Windows `go test -tags opusref ./...` (7m31s) pass.
  - Timing on this machine drifted during the session (celt/mono read 118 to
    132 µs on unchanged code), so the per-commit numbers above come from
    interleaved old/new binaries.

| Encoder workload | baseline | after pass 1 | now | alloc baseline | alloc now | allocs/op now |
|---|---:|---:|---:|---:|---:|---:|
| celt/mono/96k | 202 µs | 128 µs | 121-132 µs | 63 KiB | 1.8 KiB | 16 |
| celt/stereo/128k | 312 µs | 206 µs | 187-209 µs | 109 KiB | 2.8 KiB | 17 |
| silk/mono/12k | 738 µs | 496 µs | 474-506 µs | 245 KiB | 54 KiB | 219 |
| silk/stereo/12k | 793 µs | 532 µs | 508-522 µs | 274 KiB | 73 KiB | 208 |
| hybrid/mono/24k | 426 µs | 300 µs | 277-286 µs | 179 KiB | 34 KiB | 136 |
| hybrid/stereo/36k | 1151 µs | 801 µs | 741-761 µs | 424 KiB | 88 KiB | 322 |

- 2026-10-15 evening (Linux cloud run, Xeon @ 2.8 GHz, 4 vCPU, Go 1.24.11,
  libopus 1.6.1 built static from the release tarball): item 8 (part) and
  Linux verification.
  - `1f6e3ba` SILK decode: `silkLPCPredictQ10` unrolls orders 10/16, the
    LTP filter reads one 5-tap window when every tap is in range,
    `iirFIRInterpol` sizes its output once; per-frame parameter slices are
    stack arrays or decoder scratch (`synthOut`, `pulseBuf`, `nlsfQ15Buf`,
    `lpcSetBuf`). `decode_kernels_test.go` checks the kernels against the
    old loops.
  - `c9b8211` `splitOpusFramesInto` + `[48][]byte` stack buffers in
    `inspectPacket` and the decoder packet paths.
  - Interleaved old/new binaries, 6 runs each: tv02 -16%, tv04 -21%,
    tv06 -15%, tv12 -15%, tv03/05/07/10 -4% to -6%, CELT-only vectors
    within noise; allocations -24% to -83% (SILK vectors -75% to -83%).
  - Tried and dropped: splitting `combFilterConst` into a history part and
    a direct-index part showed no difference on CELT vectors.
  - `a5e94a4` `scripts/oracle/build_celt_coeff.ps1` did not run on Linux
    (`$env:TEMP`, backslash paths, helper inserted after `static`; glibc's
    `stdio.h` then fails to compile). `TestCELTScalarSynthesisStageScan`
    passes on Linux for tv01/tv07/tv11. tv09 exits 2 because the oracle
    accepts CELT-only packets of at least 2 bytes (not platform-specific).
  - Linux results on the final commit: `go test ./...`, `go test -race
    ./...`, `go test -tags opusref ./...` (all 12 vectors, encoder oracles
    via `build_encoder.ps1` under pwsh 7.4), the `opus16` and `opusextsrc`
    steps of `opusref.yml`, and the arm64 `-a` FMA check (no site in this
    module) pass. No Linux/Windows behaviour difference was found in the
    codec. Opt-in audits (`OPUS_LIBOPUS_AUDIT=1`) were not run.
  - pwsh on a host without ICU needs `DOTNET_SYSTEM_GLOBALIZATION_INVARIANT=1`.
  - `scripts/oracle/build.ps1` (diag-only IMDCT oracle) is still
    Windows-only and pins libopus 1.5.2; port it to 1.6.1 before relying on
    `OPUS_ORACLE_IMDCT_COMPARE`.

- 2026-10-15 evening: item 8.
  - `bed2914` `decodeFrameI16`; stereo mid/side go to MS-to-LR as int16
    (the float64 detour was an exact round trip); decodeFrame/MS-to-LR
    outputs in decoder scratch. Interleaved: tv02-tv06 bytes -54% to -63%,
    allocations -24% to -41%, time -4% (not significant);
    `BenchmarkPerfDefaultDecodeFEC` -4% to -13%.
  - `6a9cb8c` `DecodeMultiWithDecoderReuse` for the Opus SILK-only and
    hybrid paths (they consume the PCM within the frame). tv03/tv04 -3%,
    bytes another -59% to -75%.
  - CPU profile now (tv02-tv04): `iirFIRInterpol` 19%, `synthesize` 18%
    cumulative, `SoftClipFloat32` 7%, int16 output conversion 7%.

- 2026-10-15 evening: item 9. `14aaea8` NSQ short prediction fast path and
  stack sample state: interleaved SILK/hybrid encode -5% geomean (noisy),
  allocations -3% to -10%. LTP/shaping-lag windowing: no change, reverted.
  `go test ./...`, FMA `-a` and Windows opusref (7m42s, on `04ab664`) pass.

- 2026-10-15 evening: item 10. `6efb65c` ILog via `bits.Len32`
  (interleaved: tv01 -8%, tv07 -11%, tv09 -12%); `ddedb9b` silkCLZ32 via
  `bits.LeadingZeros32` (hybrid/stereo -1.6%). Both have equality tests
  against the old loops. CELT decode profile after this: `cwrsiLibopusInto`
  10%, `imdctRawStatic` 11% cumulative, `applyDeemphasis` 4.5%,
  `expRotation1` 4%, `celtPVQU` 4%, `SoftClipFloat32` 3%; no single float64
  hot spot stands out for item 11.

- 2026-10-15 night (Linux cloud run): encoder pass, item 11 deferred.
  Cross-implementation baseline first (scratch harness, 48 kHz, complexity
  10, constrained VBR, libopus 1.6.1 Release = 1x): decode all 12 vectors
  libopus 837 ms, pion `44637de` 1.91x, v1.0.0 9.25x, v1.5.0 5.00x, v1.6.0
  5.54x, HEAD 2.07x; encode HEAD 2.2-2.6x (v1.5+ packets match libopus
  mode mix and bitrate; v1.0.0 codes everything as CELT so is not
  comparable). `21b779c`: NSQ del-dec fixed-window taps, BCE in the warped
  AR loop, live-window-only `copyDelayedDecisionSuffix`; PVQ search BCE;
  LPC analysis filter converts coefficients once; inner product / warped
  autocorrelation subslices; NLSF tables on the stack; scratch reuse in
  `analyzeNLSF`, `frontEndFrame`, `silkInput`; `isDigitalSilence` early
  exit. Interleaved `BenchmarkPerfDefault` (n=10): SILK -11%, hybrid -7% to
  -13%, CELT not significant; B/op -18% to -36% on SILK/hybrid. Harness
  after: encode 2.2-2.5x libopus. `go test ./...`, packet digests and the
  encoder oracle suite pass on `21b779c`. Remaining encode cost: NSQ
  arithmetic itself, CELT prefilter/pre-emphasis float64<->float32
  conversion (item 11 territory), PVQ search.

- 2026-10-16 06:30 scheduled run (clock checked 06:30:07): item 11 needs
  the user's decision (medium risk), so this run took item 6's remaining
  encoder allocations, which the real-audio comparison flagged (115 to 214
  GCs per track). `e79a02d`: stack arrays / encoder scratch for local
  analysis temporaries. Interleaved, 2 s x 6: SILK/hybrid encode bytes -18%
  to -37%, allocations -23% to -31%, hybrid/mono -1.3%, hybrid/stereo
  -2.2%, others within noise; decode unchanged.

- 2026-10-16 afternoon: follow-ups after the real-audio report.
  - `9dc3e79` `Encode`/`Encode24` reuse their float64 conversion buffer
    (15 KB per 20 ms stereo frame; the benchmarks use `EncodeFloat` and
    missed it). Re-encoding 36 real-audio cells is byte-identical;
    `TestEncodeInt16AllocsMatchFloat` fails before the fix and passes after.
    Per-frame (QueryPerformanceCounter) on a 280 s track: allocations
    23-45 KB -> 7-28 KB, GCs 6-12 -> 1-7; p99 unchanged (390-470 µs), max
    about 2.4 ms at 64k and above, not GC-bound.
  - Concurrency (64k, int16 Encode, N goroutines): 80% efficiency up to the
    8 physical cores, 719x real time at N=16; libopus via N ffmpeg
    processes scales the same way at about 1.7x the throughput.
  - Lossless sources (five FLAC tracks, soxr to 48 kHz), matched VBR:
    dSNR -0.00..+0.03 dB, rate 0..0.3%, identical packets median 88%,
    modes 100%, decoder max 1 LSB.
  - `4b4acce` adds arm64 Linux/macOS cells to the official-vector CI job so
    the decode digests run on an FMA architecture; run 38858997687 on
    `fc0c4f9` passed all 11 jobs, including both arm64 vector cells.

- 2026-10-16 evening: C/Go profile follow-up (see
  `.claude/tasks/investigation/plainc-parity-and-profile-2026-10-16.md` on
  `fix/encoder-plainc-parity`). `13e243f`: the encoder pre-emphasis runs in
  float32 locals with the loop split by channel count (it converted
  float64 -> float32 -> float64 per sample; gcc also auto-vectorises the C
  loop at -O2). 1680 s encode at 64k: 20.28 s -> 19.62 s (-3.2%, three
  interleaved runs), packets byte-identical, no FMA. Tried and reverted
  (no measurable change on real audio): accumulating `runPrefilter`'s sums
  in locals, and removing the bounds checks in `expRotation1` with
  equal-length views. In those kernels the cost is the float64 storage
  (load/convert/store per element), which points back to item 11.

- 2026-10-17 06:30 scheduled run (clock checked 06:30:09): item 11 still
  waits for the user, so the run took more of item 6. The stereo SILK-only
  path copied its CELT input every frame for the HB-gain / stereo-width
  fades; the copy now reuses `Encoder.celtFadeBuf` (also used by the rare
  CELT-only HB fade). Interleaved 2 s x 6: silk/stereo/12k encode -1.63%
  time, -33.8% bytes; everything else unchanged. Local commit only (PR #32
  is open; not pushed).

## Next steps (in order)

From an Opus read-only review on 2026-10-15 (commit `52969ac`; estimates
unless marked measured). Every item below keeps output bit-identical by
construction (lossless float32<->float64 round trips, integer work or buffer
reuse); guard each with the digest tests.

1. (done) Decoder MDCT (`internal/dsp/mdct_celt.go` `imdctRawInto`,
   `fft_opus.go` `opusFFTInto`): write the pre-rotation straight into
   `opusComplex` at `fout[bitrev[i]]`, post-rotate from `fout`, use a
   precomputed per-mode twiddle slice instead of `libopusMDCTTwiddle`
   (not inlined, called 4x per element). Measured in an experiment: 960-pt
   8.7 -> 4.7 µs. Same for `CLTMDCTForwardInto` (encoder).
2. (done) `internal/celt/decoder.go` output loop
   (`for i := 0; i < frameSize/d.downsample; i++ { output[i*cc+c] = samplesOut[i*d.downsample] ... }`):
   hoist `frameSize/d.downsample` and `d.downsample` into locals (the loop
   divides and reloads per sample; verified by reading the code).
3. (done) `celtPVQU` fast path: make the table lookup inlinable (keep the slow
   path in a separate function); avoid clearing the whole `[176]int` in
   `algUnquant` (only `n` entries are used).
4. (done) Output conversion (`finishOutput`, `floatToInt16`, `SoftClipFloat32`):
   soft clip in float32 and convert to int16 in one pass.
5. (done) `ApplyFineEnergy`/`ApplyFinalFineEnergy` (`internal/celt/bands.go`) write
   `band.Energy` that `decodeCELTRange` overwrites before any read
   (verified); drop the `math.Exp2` work if PLC and the digests agree.
6. (done) Encoder allocations: `opPVQSearch`/`algQuant`, hybrid `celtChunk`,
   pitch analysis, frame state snapshots, noise-shape/LPC_in_pre/
   process_gains buffers, CELT pitch search/remove_doubling/tfAnalysis.
   `21b779c` (cloud) moved `analyzeNLSF`/`frontEndFrame`/`silkInput`
   temporaries to scratch; `e79a02d` moved Burg's work rows, find_LPC's
   interpolation buffers, the residual-energy output, NLSF unpack tables,
   the VAD filter-bank buffer and the NSQ input frame off the heap.
   Remaining per-frame allocations: the rate loop's `frameLoopSnapshot`
   (`encode_frame_loop.go`), NSQ `pulses` (returned and kept as the
   "lower" result), `silkNLSFDelDecQuant`'s `raw` (kept as `bestRaw`),
   `DownmixToMono`, `int16SliceToInt32`, `entcode.NewEncoder`; each needs a
   lifetime check because the result outlives the call.
7. (done) FrameTrace recording is gated by `silk.EnableFrameTraces()`.
8. (done) SILK decode: kernels and parameter buffers (`1f6e3ba`), LBRR
   decoders (`b04dd8d`), stereo mid/side stay int16 into MS-to-LR and the
   frame/packet outputs live in decoder scratch (`bed2914`, `6a9cb8c`).
   Not done: skipping the float round trip after the resampler. A CPU
   profile of tv02-tv04 puts `SoftClipFloat32` plus the int16 conversion at
   about 13%, but bypassing the soft clip needs proof that its per-channel
   memory stays unchanged for SILK-only output; left for later.
9. (done, partly) NSQ: `14aaea8` short-term prediction takes a fast path
   when its whole window is in range (no tap can be skipped, so no
   assertion is needed) with orders 16 and 10 unrolled; `sampleState` is on
   the stack. SILK encode about -5%. Windowing the LTP and harmonic-shaping
   lag taps the same way measured no change and was dropped. The warped AR
   loop (`sAR2Q14`) is the remaining NSQ hot spot; its bounds checks depend
   on the runtime `shapingLPCOrder`.
10. (done, partly) Smaller: `6efb65c` `entcode.ILog`/`Log2Ceiling` use
    `math/bits` (CELT decode -8% to -12%); `ddedb9b` `silkCLZ32` uses
    `bits.LeadingZeros32` (-1.4%). Not worth it: `iirFIRInterpol` already
    does libopus's per-sample work with sliced rows (19% of SILK decode
    is the filter itself); the postfilter's `getHistorySample` is inlined
    and its branch predictable (`combFilterConst` is 2.6% of CELT decode).
    Not done: the unreachable `selectRateControlPlan` branch (cleanup, no
    speed effect).
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
