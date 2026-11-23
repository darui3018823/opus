# SIMD Kernels for v2

- **Objective:** speed up the hottest inner loops with vector instructions
  while every packet, final range and decoded sample stays bit-identical to
  the scalar build on every architecture.
- **Status (last updated 2026-11-24, 2b):** phase 1 is implemented on
  `perf/simd-pitch-xcorr` (PR #42): the `internal/kernels` package with
  three amd64 kernels, its tests, and the CI guards. The first part of
  phase 2 (warped AR noise shaping and short-term prediction across
  delayed-decision states) is on `perf/simd-nsq-deldec`, stacked on it.
  Phase 2b (the rest of each state's sample) is on the same branch.
  Neither is merged. Phases 3 and 4 are proposals.
- **Inputs:** the four 2026-11-22 performance audits (ARCH-03 in
  [the Gemini high audit](../memory/audits/2026-11-22-perf-memory-optimization-audit.md)),
  CPU profiles of `BenchmarkPerfDefault` and `BenchmarkDecodeOfficialVectors`
  taken on 2026-11-23, and the measurements below.

## The bit-exactness contract

The encoder is byte-identical to plain-C libopus 1.6.1 and the decoder's
digests are pinned, so a kernel may not change a single bit. That rules out
the way libopus's own SSE/AVX/NEON kernels work (they reassociate float sums,
which is why the SIMD libopus build is not byte-identical to the plain C one;
see `docs/GAP_ANALYSIS.md`). The rules, in `internal/kernels/doc.go`:

1. **Vectorise across independent outputs, never along a float sum.** Lags of
   a cross-correlation, output samples of a FIR, channels, PCM samples. Each
   lane then performs exactly the scalar operation sequence of its output.
2. **No FMA.** Only separate multiply and add (MULPS/ADDPS, FMUL/FADD). The
   scalar Go code already avoids fusion with explicit `float32(...)`
   conversions, and CI checks the compiled Go for FMA instructions; the new
   static step also rejects FMA mnemonics in `.s` files.
3. **Integer sums may be reassociated.** int32 addition wraps modulo 2^32, so
   PMADDWD-style pair sums give the scalar result whatever the order.
4. **Rounding and clamping must use instructions with the same IEEE
   semantics:** MULPS/ADDPS round like MULSS/ADDSS; CVTPS2DQ rounds half to
   even under the default MXCSR (Go never changes it); MAXPS/MINPS return
   their source operand on NaN, which matches `!(x > lo)` style clamps when
   the bound is the source.
5. **NaN payloads are out of scope.** A NaN result must stay NaN. Its sign
   and payload are not pinned even by the scalar code: with two NaN inputs
   x86 returns the first operand, and the Go compiler may order a
   commutative add either way. A `-race` build of the scalar `pitchXcorr32`
   already returns `0x7fc00000` where the normal build returns
   `0xffc00000`.
6. Subnormals are kept (Go does not set FTZ/DAZ), so vector and scalar
   handle them the same way; the tests feed them explicitly.

## Mechanism

| Option | Pros | Cons | Verdict |
|---|---|---|---|
| Go assembly (`.s`) + own CPUID | Works on the module's Go version, no dependency, full control, no bounds checks | Plan 9 syntax, per-arch code, no inlining across the call | **Phase 1–3** |
| `simd/archsimd` (Go 1.26+, `GOEXPERIMENT=simd`) | Pure Go source, compiler intrinsics, arm64 NEON in Go 1.27 | Experimental, needs the experiment set at build time by the *user*, so a library cannot make it the default path; codegen still has bounds checks and spills | Watch; revisit when it leaves the experiment |
| portable `simd` (`Float32s`, Go 1.27, same experiment) | Vector-length agnostic | Same experiment gate | Watch |
| `golang.org/x/sys/cpu` for detection | Maintained | First dependency of a zero-dependency module | Not needed: CPUID/XGETBV are 20 lines |

Measured `pitchXcorr32` at the encoder's coarse search size (240 × 244 lags),
Go 1.27.4, Xeon 2.3 GHz (AVX-512 capable, kernels use AVX):

| Implementation | ns/op | vs scalar |
|---|---:|---:|
| scalar Go (4 lags per pass) | 11,100 | 1.0x |
| `archsimd.Float32x8`, naive slicing | 5,300 | 2.1x |
| `archsimd.Float32x8`, tightened slicing | 4,100 | 2.7x |
| SSE assembly, 16 lags per pass | 3,400 | 3.3x |
| AVX assembly, 32 lags per pass | 2,400 | 4.6x |

The archsimd version is bit-identical too, so moving the kernels to it later
is a mechanical change once the experiment is gone.

### Layout

```text
internal/kernels/
  doc.go              contract (above)
  xcorr.go            PitchXcorr32 + PitchXcorr32Generic (reference)
  fir8.go             FIR8Interp + FIR8Table
  pcm.go              Float32ToInt16 + Float32ToInt16Generic
  *_amd64.go / .s     amd64 && !purego: kernels, dispatch, CPUID/XGETBV
  arch_generic.go     !amd64 || purego: stubs that compute nothing
```

Each public entry point checks bounds once in Go, lets the architecture
function compute as much as fits the vector width and returns how much it
did, and finishes the remainder with the reference loop. Callers in `celt`,
`silk` and the root package call the entry point; they keep no per-arch
code. `-tags purego` compiles only the references.

Dispatch is a package variable set at init (`useAVX` from CPUID.1:ECX
AVX+OSXSAVE and XCR0 bits 1–2; SSE/SSE2 are the amd64 baseline). Tests flip
the variables to run every path.

## Phase 1 (implemented)

| Kernel | Callers | Method | Kernel speedup |
|---|---|---|---:|
| `PitchXcorr32` | CELT `pitchSearch32` (encoder prefilter, PLC), `celtAutocorr32` | One lag per lane; AVX 32 lags/pass (4 × YMM), SSE 16; the 1–3 leftover lags are recomputed by an overlapping 4-lag block (same bits rewritten) | 4.6x |
| `FIR8Interp` | SILK `iirFIRInterpol` (IIR/FIR up-sampler: 8/12/16 kHz → 24/48 kHz) | PMADDWD per output, 4×4 transpose-add, `((v>>14)+1)>>1` rounding (no int32 overflow, equal to the int64 form), PACKSSDW saturation | ~9x |
| `Float32ToInt16` | int16 `Decode` output | MULPS, MAXPS/MINPS clamp, CVTPS2DQ, PACKSSDW, 8 samples/pass | ~9x |

End-to-end, interleaved A/B against `main` (`8f3b430`), 8 rounds, `-cpu=1`,
2-vCPU cloud VM (noisy; only p < 0.05 rows shown):

| Benchmark | main | branch | change |
|---|---:|---:|---:|
| encode celt/mono/96k | 90.8 µs | 83.7 µs | −7.7% |
| encode celt/stereo/128k | 143.2 µs | 137.4 µs | −4.1% |
| decode FEC voip/mono/20k | 23.0 µs | 18.6 µs | −19.1% |
| decode FEC voip/stereo/24k | 43.3 µs | 33.9 µs | −21.7% |
| decode FEC voip/mono/32k | 43.1 µs | 39.3 µs | −8.8% |
| tv02 (SILK NB) | 31.8 ms | 21.6 ms | −32.2% |
| tv03 (SILK MB) | 30.0 ms | 22.1 ms | −26.3% |
| tv04 (SILK WB) | 42.3 ms | 31.2 ms | −26.1% |
| tv05 (hybrid) | 81.5 ms | 74.5 ms | −8.5% |
| tv07 | 65.7 ms | 62.7 ms | −4.5% |
| tv08 | 64.9 ms | 62.0 ms | −4.5% |
| tv12 (SILK) | 34.4 ms | 27.6 ms | −19.9% |

Geometric mean over all 21 benchmarks in the run: −10.4%. SILK and hybrid
encode workloads did not move (their hot spot is the delayed-decision NSQ,
phase 2). Allocations are unchanged.

### Verification done

- Kernel differential tests on every path (AVX, SSE, pure Go fallback):
  all shapes `length ≤ 40 × maxPitch ≤ 75`, codec shapes, subnormals,
  signed zeros, ±1e30 overflow, inf and NaN, exact-length slices; FIR8 with
  random and extreme tables (including the int32 wrap of
  eight `-32768 × -32768` products); PCM conversion over **all 2^32 float32
  bit patterns** (`OPUS_KERNELS_EXHAUSTIVE=1`, 35 s).
- `TestOfficialVectorDecodeDigests` (12 RFC 8251 vectors × 6 output
  configurations), `TestPerfPredictivePacketRegression`,
  `TestPerfDefaultPolicyPacketRegression`: unchanged.
- A 600-cell random codec matrix (8–48 kHz, mono/stereo, VoIP/audio/RLD,
  2.5–60 ms frames, complexity 0–10, CBR/CVBR/VBR, DTX, in-band FEC with
  FEC decoding, PLC; 20,565 packets) hashed over packets, final ranges and
  decoded PCM gives the same digest for this branch, this branch with
  `-tags purego`, and `main`.
- `go test ./...`, `go test -tags purego ./...`, `go test -race` on the
  touched packages, `go vet` for amd64, arm64, 386, js/wasm, and windows.

## Phase 2: SILK delayed-decision quantizer

`silkNoiseShapeQuantizerDelDec` was 44% of the SILK and hybrid default
encode profile (2026-11-23). Inside it, the warped AR noise-shaping filter
took about 37%, the short-term LPC prediction 12% and the quantizer
candidates 9%. All three are fixed point, and the delayed-decision states
(2 at complexity 2–5, 3 at 6–7, 4 at 8–10) are independent until the
per-sample survivor selection, so one state per lane is exact. This is the
structure of libopus's `NSQ_del_dec_avx2.c`.

### 2a (implemented)

| Kernel | Method |
|---|---|
| `WarpedAR4.Step` | The whole warped all-pass chain and the `n_AR_Q14` sum for 2 or 4 states. Two states per XMM, one per 64-bit half: `PMULDQ` then `PSRLQ $16` leaves bits 16–47 of the 64-bit product in the low dword, which is exactly `silk_SMULWB`'s int32 result (logical and arithmetic shifts agree on those bits); `PADDL`/`PSUBL`/`PSLLL` wrap like the scalar code. A 2-lane variant serves complexity 2–5. |
| `LPC4.Predict` | `LPC_pred_Q14` for 2 or 4 states over a per-lane mirror of `sLPC_Q14`. The int32 sum wraps, so tap order is free. |

Both need SSE4.1 (`PMULDQ`), detected from CPUID.1:ECX bit 19. The
quantizer keeps its per-state arrays (AoS) as the source of truth: the
filter state moves to `WarpedAR4.S` for one subframe and back, and
`LPC4.H` receives the same per-sample writes and survivor copies as each
state's `sLPC_Q14`. Without SSE4.1, on other architectures and with
`purego`, the existing per-state loop runs unchanged; the `purego` CI job
keeps it covered.

Min of 25 interleaved runs, pinned to one CPU (`taskset -c 1`), against
phase 1. Medians on this VM spread by ±10–20% and could not resolve the
smaller changes; interference only adds time, so the minimum is the
cleaner estimate:

| Workload (default policy) | phase 1 | 2a | change |
|---|---:|---:|---:|
| silk/mono/12k, complexity 5 | 470.5 µs | 445.5 µs | −5.3% |
| silk/stereo/12k, complexity 5 | 506.8 µs | 484.5 µs | −4.4% |
| hybrid/mono/24k, complexity 5 | 276.0 µs | 267.7 µs | −3.0% |
| hybrid/stereo/36k, complexity 5 | 777.4 µs | 745.6 µs | −4.1% |
| silk/mono/12k, complexity 10 | 919.6 µs | 746.3 µs | −18.8% |
| silk/stereo/12k, complexity 10 | 1127.1 µs | 886.5 µs | −21.3% |
| hybrid/mono/24k, complexity 10 | 472.3 µs | 405.2 µs | −14.2% |
| hybrid/stereo/36k, complexity 10 | 1406.9 µs | 1177.6 µs | −16.3% |

`BenchmarkPerfDefaultComplexity10` is new; complexity 5 is the
`NewEncoder` default and complexity 9 the `EncoderProfileLibopus` one.

The warped AR kernel now sits at its latency floor: each sample is a
24-step serial chain (`PSUBL` → `PMULDQ` → `PSRLQ` → `PADDL`, about 8
cycles a step), about 83 ns at 2.3 GHz against 88 ns measured. A wider
vector does not shorten the chain. Note that the kernel executes *more*
instructions than the scalar loop (callgrind: +2% for the whole encode);
the gain comes from running the states' chains side by side.

Verification: kernel differential tests on every path (random and
extreme int32/int16 values, all even orders 2–24, 1–4 states, 8
consecutive samples so state updates are compared), the packet-regression
digests, and a SILK-focused random matrix (800 cells, VoIP, 6–40 kb/s,
complexity 2–10, DTX/FEC/PLC; 27,327 packets) with the same digest for this
branch, `-tags purego` and `main`.

### 2b (implemented)

`NSQCand4.Step` runs the rest of each state's sample: `silk_RAND`, `n_LF`,
the saturating residual, the sign flip and limiter, the two quantizer
candidates and their rate-distortion costs, the ranking, and both
candidates' `xq`, `Diff`, `LF_AR`, `sLTP_shp` and `LPC_exc`. Four states
fit one XMM as int32 lanes (SSE4.1 `PMULLD`, `PMAXSD`/`PMINSD`,
`PBLENDW`); the two `SMULW*` products use `PMULDQ` on even and odd lanes.
Every branch is a lane mask:

- `silk_ADD_SAT32`/`silk_SUB_SAT32` detect overflow from the sign bits
  (`(a^s) & (b^s)`, `(a^b) & (a^s)`) and select `a>>31 ^ INT32_MAX`.
- The four `q1_Q0` cases collapse to
  `q1 = (q<<10) + offset + (q>0 ? -80 : q<0 ? 80 : 0)` and
  `q2 = q1 + 1024 - (q ∈ {0, -1} ? 80 : 0)`, with the rate terms negated
  where `q < 0` (for `q1`) and `q < -1` (for `q2`). The `Lambda_Q10 > 2048`
  rate-distortion offset is a branch on a per-subframe scalar.
- Ranking selects rank 0 with the `rd1 < rd2` mask and gets rank 1 as
  `q1 ^ q2 ^ rank0`.

The quantizer's per-sample candidate storage (`sampleState`) is now this
struct of arrays in both paths, so the survivor selection reads the kernel
output in place; copying it into the per-state structs cancelled the
kernel's gain in a first attempt. The scalar path writes the same arrays.

Kernel tests: 200,000 random and extreme cases per path, plus every
residual in [-33·1024, 32·1024] for lambda on both sides of 2048 and four
offsets. Two deliberate mutations (a shift and the 2048 boundary) were
checked to fail them. The SILK matrix digest is unchanged on amd64 (SIMD
and `purego`) and on arm64 (`qemu-aarch64`), and the arm64 test suite,
including the official-vector digests, passes under qemu.

Cumulative against phase 1, min of 25 interleaved runs pinned to one CPU:

| Workload (default policy) | phase 1 | 2a + 2b | change |
|---|---:|---:|---:|
| silk/mono/12k, complexity 10 | 847.9 µs | 527.9 µs | −37.7% |
| silk/stereo/12k, complexity 10 | 878.0 µs | 574.3 µs | −34.6% |
| hybrid/mono/24k, complexity 10 | 379.5 µs | 319.9 µs | −15.7% |
| hybrid/stereo/36k, complexity 10 | 1149.7 µs | 953.7 µs | −17.0% |
| silk/mono/12k, complexity 5 | 402.3 µs | 368.2 µs | −8.5% |
| silk/stereo/12k, complexity 5 | 417.4 µs | 427.1 µs | +2.3% |
| hybrid/mono/24k, complexity 5 | 227.2 µs | 227.9 µs | +0.3% |
| hybrid/stereo/36k, complexity 5 | 634.6 µs | 607.2 µs | −4.3% |

At complexity 5 (two states) the changes are within this VM's noise; the
order of the runs alone moved the minimum by up to 8%. The pure Go path
(`purego`) showed no consistent change.

### Left in the SILK encoder

`silkWarpedAutocorrelationFLP32` (serial all-pass chain, not vectorisable
exactly), `silkInnerProductFLP32` (float64 accumulation order),
`silkNLSFDelDecQuantInto` (vectorisable across survivors, medium effort),
and the scalar survivor selection and per-state bookkeeping.

## Phase 3: CELT transform and remaining CELT encoder loops

| Function | Share | Plan |
|---|---:|---|
| `imdctRawStatic` + `bfly2/3/4/5` (decode) | ~16% of decode | The KISS FFT data is already `float32` (`opusComplex`). Within one stage the `m` butterflies are independent, so vectorise across them with the scalar per-element operation order; pre/post-rotation loops are element-wise. Needs a split real/imag (SoA) scratch or shuffles; benchmark both. |
| `cltMDCTForward` (encode) | 3.6% | Same transform |
| `combFilter32`, `PostFilter.combFilter` | 1–4% | 5-tap FIR per output sample: one output per lane. |
| `opPVQSearch` | 5% of encode | The pulse-placement argmax compares `Rxy²·best_den > best_num·Ryy` per position with first-index tie-breaking: compute the candidates in lanes, reduce with an index-aware max that keeps the lowest index. Medium risk; needs the PVQ oracle. |
| `transientAnalysis32`, `spreadingDecision32`, `toneLPC` | ~1% each | Element-wise parts only. Low priority. |
| `applyDeemphasis` (decode) | 4% | Recursive; only across channels (2 lanes). Low value. |

## Phase 4: arm64 NEON

- Same kernels with FMUL/FADD (never FMLA) and SMULL/ADDP for the integer
  ones. The Go arm64 assembler lacks some NEON mnemonics; use `WORD`
  encodings with a comment, as the standard library does.
- No feature detection is needed (NEON/ASIMD is mandatory on arm64).
- CI already runs native linux/macos/windows arm64 tests and the official
  vector digests on arm64, which is exactly the coverage the kernels need.
- Note: the Go compiler fuses `x*y + z` on arm64 unless an explicit
  conversion intervenes, so kernel *references* must keep their
  `float32(...)` wrappers; the existing `-S` FMA guard covers them.

## Not planned

- Reassociating float reductions (`innerProd32`, `dualInnerProd32`,
  `silkInnerProductFLP32`, energy sums): faster, but changes bits and breaks
  libopus parity.
- AVX-512: the 8-wide kernels are already memory/latency bound at these
  sizes, and frequency throttling on some parts makes wins uncertain.
- Runtime switching that users can control: the `purego` tag is enough.

## Acceptance criteria (each phase)

- Kernel tests pass on every path and under `-race`; the reference is the
  previous scalar code moved verbatim.
- `TestOfficialVectorDecodeDigests` and both packet-regression digests are
  unchanged; `go test -tags opusref ./...` is unchanged against the plain-C
  libopus 1.6.1 oracle.
- The random codec matrix digest is equal between the default build,
  `-tags purego` and the base commit.
- No benchmark slower beyond noise in an interleaved A/B; the targeted
  workloads improve by at least 5% (the repository's existing threshold).
- `go vet` (default and `purego`) clean on amd64 and arm64; no FMA in `.s`.

## Verification commands

```text
go test -count=1 ./internal/kernels/
go test -count=1 -race ./internal/kernels/
OPUS_KERNELS_EXHAUSTIVE=1 go test -count=1 -run AllFloat32 ./internal/kernels/
go test -count=1 -tags purego ./...
go test -count=1 -run 'TestOfficialVectorDecodeDigests|TestPerf(DefaultPolicy|Predictive)PacketRegression' .
go test -count=1 ./...
go test -run '^$' -bench . ./internal/kernels/
# A/B: build base and new test binaries, run them alternately, compare with benchstat
go test -c -o new.test . && <checkout base> && go test -c -o base.test .
./base.test -test.run '^$' -test.bench 'BenchmarkDecodeOfficialVectors|BenchmarkPerfDefault$' -test.benchtime 300ms -test.cpu 1
```
