# Performance and memory fixes from the 2026-11-22 audits

- **Objective:** apply the findings of the four 2026-11-22 performance and memory audits that are worth taking, without changing any packet, final range, decoded PCM, or the parity with plain-C libopus.
- **Status (last updated 2026-11-22):**
  - Phases 0 to 5 are in place on `perf/audit-2026-11-22-fixes`.
  - Phase 6 (CPU hot loops) is in progress.
  - This change is not merged.
- **Sources:**
  - [ChatGPT-6.2-Sol audit](../../memory/audits/performance-audit-2026-11-22.md)
  - [Claude Opus 5.5 audit](../../memory/audits/perf-memory-optimization-audit-2026-11-22.md)
  - [Gemini 4 Neon (medium) audit](../../memory/audits/memory-and-performance-optimization-audit-2026-11-22.md)
  - [Gemini 4 Neon (high) audit](../../memory/audits/2026-11-22-perf-memory-optimization-audit.md)

## Scope

**In scope:**
- Allocation and copy removal in:
  - the public API
  - the decoder construction, FEC and PLC paths
  - CELT, SILK, entcode and dsp
  - multistream, surround and projection
  - the repacketizer and extensions
  - Ogg
- CPU work in hot loops, kept only when it measures faster.

**Out of scope:** the changes below are larger or API-level and are left for later.
- SIMD or assembly kernels.
- A float32 signal pipeline.
- A single libopus-style CELT/SILK decoder.
- New public `*Into` or `EncodeTo` APIs.
- Parallel multistream coding.
- Replacing `math/big` in `exp2.go`.
- A compressed `pvqUTable`.

## Acceptance criteria

- `go fmt ./...` and `go vet ./...` (default and `opusref` tags) are clean.
- `go test ./...` passes.
- `go test -tags opusref ./...` passes in PowerShell.
- `TestOfficialVectorDecodeDigests`, `TestPerfPredictivePacketRegression` and `TestPerfDefaultPolicyPacketRegression` are unchanged.
- No benchmark gets slower beyond noise in an interleaved A/B against the base build.

## Verification commands

```text
go test -count=1 ./...
go test -count=1 -tags opusref ./...    # PowerShell
go test -count=1 -run 'TestOfficialVectorDecodeDigests|TestPerf(DefaultPolicy|Predictive)PacketRegression' .
go test -c -o base.exe . (at 7cc4669) and new.exe (at the tip), then run them alternately:
  -test.run='^$' -test.bench=<set> -test.benchtime=300ms -test.benchmem -test.cpu=1
```

## Benchmarks

All benchmarks below were run on an Intel Core i7-11700 with Go 1.27.4 on Windows/amd64, with `-cpu=1`. Each value is the median of 5 interleaved rounds against the base, which is commit `7cc4669` (the benchmarks commit). Re-running the default-policy encode with 11 rounds showed every workload within -3.3% to +0.5%.

| Benchmark | Base | Now |
|---|---:|---:|
| `NewDecoder(48000, 2)` | 628 µs, 1,439,393 B, 3230 allocs | 7.1 µs, 47,328 B, 43 allocs |
| `NewSurroundDecoder` 5.1 | 2437 µs, 5.76 MB, 12925 allocs | 35 µs, 189 KB, 177 allocs |
| `DecodeFEC` API (Decode + DecodeFEC) | 1111 µs, 2.77 MB, 7273 allocs | 83 µs, 119 KB, 100 allocs |
| `DecodePLC` after CELT | 223 µs, 97 KB, 24 allocs | 209 µs, 16.5 KB, 1 alloc |
| `EncodeFloat32` stereo CELT | 182 µs, 19.4 KB, 18 allocs | 172 µs, 1.9 KB, 13 allocs |
| Decode CELT, SILK or hybrid (6 cells) | 1 to 4 allocs | 0 allocs |
| Legacy CELT mono encode | 221 µs, 55.9 KB | 124 µs, 1.3 KB |
| Legacy hybrid mono encode | 360 µs, 60.7 KB | 242 µs, 3.9 KB |
| Default SILK mono encode | 105 allocs, 6.9 KB | 88 allocs, 4.7 KB |
| Surround 5.1 encode / decode | 183 KB / 53 KB | 6.0 KB / 2.0 KB |
| FOA projection encode / decode | 139 KB / 36 KB | 5.5 KB / 0.9 KB |
| Repacketizer Cat x2 + Out | 8.6 µs, 66 KB | 0.5 µs, 0.9 KB |
| Ogg `ReadPage`, 51 KB page | 1511 µs, 173 KB | 124 µs, 58 KB |
| Ogg `PacketWriter`, 4 pages | 6192 µs | 525 µs |
| Ogg `SeekPCM` | 25.3 ms, 4.8 MB | 0.74 ms, 0.31 MB |

## Decisions

**Bug fix: `SeekPCM` bisection.** A probe whose first page lay at or past the upper bound set `high = offset`, which left the interval unchanged or even widened it. On a 1.65 MB stream, every seek re-read 1.8 times the stream. The bound is now the midpoint, and a regression test counts the I/O.

**Dead state removed:**
- The per-rate generic and bit-exact SILK resamplers.
- The per-rate `sMid` copy in `silkRateInfo`.
- The never-assigned `celtResampler`.
- The never-set `entcode.Decoder.err`.

**Lazy CELT decoders.** A freshly created decoder has the same state the eager, never-used one had. Every configuration switch still transfers state from `lastCeltDec`, so decoding is unchanged. Tests that hook the CELT stages use `forEachCELTDecoder`, which also hooks decoders created later.

**Multistream FEC.** The stream decoders were copied twice. A copy of a `cloneState` copy holds the same state as the first copy, so the staged copy now decodes in place.

**CELT encoder trace.** The per-frame trace rows are recorded only under `celt.EnableFrameTraces`. The oracle tests turn it on.

**Legacy bandwidth detector.** The FFT twiddles, Hann window and bit-reversal order are cached, using the same expressions as before. `TestRealFFTIntoMatchesRealFFT` checks that the result is bit-identical.

**Skipped on purpose (semantic risk outweighs the gain):**
- Reusing the redundancy CELT decoder. It becomes `lastCeltDec`, and a reset would have to match a fresh decoder exactly. It is about 40 KB per mode transition.
- Lazy SILK decoders. They interact through the mono/stereo peers and the rate switch, so a lazily created decoder could differ from the eager one. It is about 7 KB per decoder.
- The remaining SILK encoder allocations whose slices outlive the frame, such as `lastGainSymbols` reused by LBRR.
