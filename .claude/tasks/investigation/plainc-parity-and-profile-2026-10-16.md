# Plain-C Parity Fixes and C/Go Profile Comparison (2026-10-16)

- Status: parity part complete; profile comparison complete; optimisation
  follow-ups open
- Branch: `fix/encoder-plainc-parity` (from `perf/decoder-encoder-optimization`
  at `f110793`)
- Related: [real-audio comparison](real-audio-libopus-comparison-2026-10-16.md),
  [performance brief](../implementation/performance-optimization-2026-10-15.md)

## Objective

1. Find why the Go encoder differed from plain-C libopus 1.6.1 on 7 of 52
   real-audio cells, and fix it.
2. Find where the remaining ~1.6x time ratio against plain C comes from:
   the same computation running slower, or more computation.

## Scope and method

- Reference: libopus 1.6.1 built with gcc 16.1 `-O2`, no intrinsics, no RTCD
  (`opus_demo`, a static library with `-g`, and the instrumented
  `enc_oracle --script-enc`).
- Inputs: 13 real tracks (8 from YouTube, 5 lossless FLAC) plus a 1684 s
  ASMR recording, 16/32/64/128 kb/s, unconstrained VBR, complexity 10,
  application audio, 20 ms, int16 input.
- Divergences were narrowed with `TestRealAudioCELTParity` (opusref): it
  feeds an external s16le file (`OPUS_PARITY_PCM`) to both encoders and
  prints the first differing frame's CELT decisions, SILK decisions and raw
  oracle lines (`OPUS_PARITY_FROM` limits tracing to the frames of interest).

## Parity fixes

| Commit | Cause | Cells |
|---|---|---|
| `f651287` fix(celt) | `clt_compute_allocation` writes `min(intensity, codedBands)` (or 0) back through `&st->intensity`; the encoder kept the pre-allocation value, so the next frame's hysteresis started from a different band. Triggered by an FB -> SWB switch frame that coded 14 bands with intensity 15. | Endorfin 64k (from packet 10), jazz 64k (from 145) |
| `3c62ff6` fix(celt) | `stereo_analysis` accumulated in float64; libopus's `opus_val32` is float. A close dual-stereo decision flipped. | jazz 32/64/128k (frame 7259) |
| `25c6870` fix(silk) | On CELT-only -> SILK/hybrid the SILK encoder is rebuilt for the prefill after `SetOpusActivity`/`SetDTX` ran; libopus passes both as `silk_Encode` arguments, so `VAD_NO_ACTIVITY` still lowers `speech_activity_Q8` below the DTX threshold. The first hybrid frame after a quiet CELT stretch was coded unvoiced instead of inactive. | ASMR short 16k/32k |

Result: 56 of 56 cells (1,006,840 packets) are byte-identical to plain-C
libopus. `go test ./...`, the arm64 FMA check and `go test -tags opusref
./...` pass; the existing digest tests did not change (no synthetic fixture
reached these paths).

Regression coverage: `TestCarryPacketStateKeepsCallArguments` fails without
the SILK fix. The two CELT fixes are covered by the real-audio probe only:
synthetic stereo music and noise, with and without bandwidth switches
(`TestCELTBandwidthSwitchVBROracle`), never produced a frame whose coded band
count fell below the hysteresis intensity, so a synthetic test passed with
the fix removed and was not kept.

## Profile comparison (J-POP x6, 1680 s, 64 kb/s, same machine)

C time comes from a sampling profiler built for this (`csample.c`: a thread
suspends the main thread every ~1 ms and records RIP; addresses are
symbolised with addr2line). Go time comes from pprof. Go's own profiler
could not see into cgo frames on Windows, and gprof's time sampling does not
work with MinGW here.

Totals (codec only): decode C 2.92 s vs Go 4.90 s (1.68x); encode C 12.48 s
vs Go 19.43 s (1.56x).

Decode, grouped by stage (leaf time):

| Stage | C (s) | Go (s) | Go / C | Share of the gap |
|---|---:|---:|---:|---:|
| MDCT / FFT | 0.636 | 0.620 | 0.98 | -1% |
| Entropy coder | 0.175 | 0.160 | 0.92 | -1% |
| Spreading rotation | 0.338 | 0.470 | 1.39 | +7% |
| PVQ decode (cwrsi, pvq_u) | 0.257 | 0.430 | 1.68 | +9% |
| Output (deemphasis, soft clip, int16) | 0.474 | 0.750 | 1.58 | +14% |
| Inner products / norms | 0.182 | 0.370 | 2.03 | +9% |
| Band logic / allocation | 0.433 | 1.010 | 2.33 | +29% |
| Postfilter | 0.048 | 0.170 | 3.52 | +6% |
| Go runtime / GC / memmove | 0 | 0.360 | - | +18% |

Encode, matching functions (self time):

| C -> Go | C (s) | Go (s) | Go / C |
|---|---:|---:|---:|
| `l1_metric` -> `l1Metric` | 0.29 | 0.30 | 1.0 |
| `transient_analysis` -> `transientAnalysis32` | 0.68 | 0.77 | 1.1 |
| `xcorr_kernel_c` -> `pitchXcorr32` | 0.97 | 1.13 | 1.2 |
| `tonality_analysis` (self) | 0.45 | 0.66 | 1.5 |
| `gemm_accum` -> `gemmAccum` | 0.42 | 0.63 | 1.5 |
| `spreading_decision` | 0.58 | 0.90 | 1.6 |
| `op_pvq_search_c` -> `opPVQSearch` | 1.14 | 2.15 | 1.9 |
| `exp_rotation1` -> `expRotation1` | 0.67 | 1.29 | 1.9 |
| `icwrs` -> `icwrsLibopus` | 0.23 | 0.44 | 1.9 |
| `run_prefilter` -> `runPrefilter` (self) | 0.31 | 1.32 | 4.3 |
| `celt_encode_with_ec` -> `encodeRange` (self) | 0.04 | 1.08 | large |
| Go runtime / GC / memmove | 0 | 1.02 | - |

Reading: the arithmetic-heavy kernels (FFT/MDCT, the range coder, cross
correlation, transient analysis, L1 metrics) already run at C speed. The gap
sits in (a) Go runtime and GC (about 18% of the decode gap, 15% of the
encode gap), (b) orchestration code doing extra per-sample work that C does
not (`encodeRange`, `decodeCELTRange`, `runPrefilter`, mostly float64 <->
float32 conversion of the float64 buffers), and (c) a handful of integer or
short-loop kernels at 1.5x to 2x (PVQ search and enumeration, spreading
rotation, inner products).

A first attempt at (b), accumulating `runPrefilter`'s before/after sums in a
local, measured no change on real audio (21.20 s vs 21.35 s median of three)
and was reverted; per-line attribution inside that function looks skewed.

## Follow-ups

1. Decide on item 11 of the performance brief (float32 storage): the
   profile now points at float64 buffers as a source of the orchestration
   overhead, but it has to stay bit-exact.
2. Look at `opPVQSearch`, `expRotation1` and `icwrsLibopus` in assembly for
   bounds checks and conversions (1.9x each).
3. Reduce GC work further (encoder still allocates about 14 KB per frame at
   64k; the conversion buffer fix removed 15 KB).

## Verification commands

```sh
# parity probe (needs an external 48 kHz stereo s16le file)
OPUS_PARITY_PCM=track.raw OPUS_PARITY_FRAMES=7262 OPUS_PARITY_KBPS=32 \
  OPUS_PARITY_FROM=7255 go test -tags opusref -run TestRealAudioCELTParity -v .
go test -tags opusref -run TestCELTBandwidthSwitchVBROracle .
go test ./internal/silk -run TestCarryPacketStateKeepsCallArguments
```
