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
  Phase 3a (CELT FFT stages and backward-MDCT rotations, plus a
  reassociated SILK synthesis prediction) is on `perf/simd-celt-fft`,
  stacked on phase 2. Phase 3b (the decoder's comb post-filter) is on
  `perf/simd-combfilter`, stacked on 3a. Phase 4 (arm64 NEON versions of
  every kernel) is on `perf/simd-neon`, stacked on 3b. None is merged.
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

## Phase 3: CELT transform and the rest of the decoder

### 3a (implemented)

**FFT stages.** The static-mode KISS FFTs (60/120/240/480 points) factor
as `[5,3,4]`, `[5,3,2,4]`, `[5,3,4,4]` and `[5,3,4,2,4]`; every radix-3,
-4 and -5 stage with m > 1 has m divisible by 4. `FFTRadix3/4/5` run four
consecutive butterflies of a group per pass, real and imaginary parts
deinterleaved into separate registers on load (`SHUFPS $0x88/$0xDD`) and
interleaved on store (`UNPCKLPS/UNPCKHPS`). `C_MUL` is
`ar*br - ai*bi`, `ar*bi + ai*br` with each product rounded; every lane
follows `kf_bfly3/4/5`'s scalar order, negation included (an XOR of the
sign bit is exact). The strided twiddles `tw[j*fstride*q]` are copied once
per plan into a per-stage table laid out in blocks of four butterflies.
The radix-2 stage and the m = 1 radix-4 stage stay scalar (1.9% of decode
together).

**Backward MDCT rotations.** `IMDCTPreRotate` reads `x[2i]` and
`x[N2-1-2i]` four at a time (`CVTPD2PS` is `float32(x)`), rotates, and
scatters each complex result to `bitrev[i]` with `MOVQ`/`MOVHPS`; the
permutation is range-checked once per plan (`kernels.BitRev`).
`IMDCTPostRotate` walks from both ends, reversing the backward stream's
lanes with `SHUFPS $0x1B`, so both float64 output windows are contiguous
(`CVTPS2PD` widening is exact).

**SILK synthesis prediction (pure Go, all architectures).** The decoder's
`silkLPCPredictQ10` summed 16 taps in `j` order, newest sample first, so
each synthesized sample waited for the whole add chain of the previous
one. Its terms are each rounded and the int32 sum wraps, so the older
taps are now summed as a tree and the newest added last; the recursive
dependency shrinks to one multiply and one add.

Kernel speedups: radix-5 stage 5.5x, rotations 2.5x. All FFT, rotation and
NSQ kernel paths were mutation-checked: a changed operand or shuffle in
each makes the kernel tests fail.

End-to-end against phase 2, min of 15 interleaved runs pinned to one CPU:

| Benchmark | phase 2 | 3a | change |
|---|---:|---:|---:|
| 12 RFC 8251 vectors, total | 875.2 ms | 803.0 ms | −8.2% |
| tv01 (CELT) | 135.5 ms | 116.1 ms | −14.3% |
| tv06 (hybrid/CELT) | 83.3 ms | 68.8 ms | −17.4% |
| tv11 (CELT) | 99.7 ms | 85.7 ms | −14.1% |
| encode celt/mono/96k | 89.7 µs | 83.6 µs | −6.8% |
| encode celt/stereo/128k | 157.4 µs | 149.4 µs | −5.1% |

Single SILK-only vectors moved by −8% to +6% between runs; their sum
change is within noise here. An earlier pass that measured only the
prediction change gave −1% to −9% on the SILK vectors.

Verification: official-vector and packet digests, the 600-cell and SILK
matrices (amd64 default and `purego`), and on arm64 under qemu the dsp
tests, the digests and a 300-cell matrix with the same digest as amd64.

### 3b (implemented): the comb post-filter

The decoder's pitch post-filter (`comb_filter` in place) is recursive, but
output `i` reads samples no later than `i - period + 2` with
`period >= 15`, so a block of four consecutive outputs reads only samples
written before the block. `CombConst` and `CombOverlap` run four outputs
per pass with the scalar operation order.

The scalar filter reads through `getHistorySample`, which branches on
every tap. The vector path instead copies the reachable history
(`period + 2` samples, zeros before the start of `pf.buf`) and the frame
into one float32 buffer, filters it, and copies back only the samples the
filter wrote (so samples it skips keep their float64 bits). A first
version did the conversions in Go loops; they then cost four times the
filtering itself, so they now use `CVTPD2PS`/`CVTPS2PD`
(`Float64sToFloat32s`, `Float32sToFloat64s`), which round like Go's
conversions. Without the kernels the conversions would only add work, so
other builds keep the existing scalar filter unchanged.

Effect, from CPU profiles of the CELT-heavy vectors (tv01, tv09, tv10,
tv11): `PostFilter.Apply` fell from 7.0% to 3.8% of decode time, i.e.
about 3% of CELT decoding. Timed runs on this VM could not resolve a
change that small: an unchanged pure Go build measured +2.3% against
itself in the same session.

### Not vectorisable without changing bits

These remain in the profiles and are why the bit-exact plan ends here for
the encoder:

| Function | Share | Why |
|---|---:|---|
| `opPVQSearch` | 5.7% of encode | Pulse placement keeps the first maximum under `best_den*Rxy > Ryy*best_num`, compared with float32 rounding against the running best. That comparison is not transitive, so per-lane maxima merged afterwards can pick another index in near-ties. (libopus's SSE version is not bit-exact with its C version for this reason.) |
| `innerProd32`, `dualInnerProd32`, energy sums | ~3% of encode | Float32 reductions: lanes would reassociate the sum. |
| `pitchDownsample32` FIR, `celtLPC32` | ~2% of encode | Serial recurrences. |
| `silkWarpedAutocorrelationFLP32`, `silkInnerProductFLP32` | ~6% of encode | A serial all-pass chain; a float64 sum. |
| `applyDeemphasis` | 3.3% of decode | First-order IIR per channel; only the two channels of a stereo stream are independent. |
| `up2HQInternal` | 2–5% of SILK decode | Serial all-pass sections per channel. |

Smaller remaining candidates that are exact: the forward MDCT rotations
(about 1% of CELT encode), the radix-2 and m = 1 radix-4 FFT stages (~2%
of decode), and `interleaveSILKOutInto` (a shuffle).

## Phase 4: arm64 NEON (implemented)

Every kernel has a NEON version, built with `arm64 && go1.27 && !purego`.
Go 1.27's assembler added the vector floating-point and widening integer
instructions (`VFADD`, `VFMUL`, `VFCVTN`, `VSMULL`, `VSHRN`, `VSQADD`,
...); Go 1.24 to 1.26 lack them, so those toolchains build the pure Go
references on arm64 (CI builds both architectures with Go 1.24.11, the
module minimum). NEON is mandatory on arm64, so there is no feature
detection.

Notes per kernel, beyond the SSE versions:

- Every float kernel uses separate `FMUL` and `FADD`/`FSUB`; never a
  fused multiply-add. Advanced SIMD honours FPCR like the scalar unit, so
  subnormals behave the same (unlike 32-bit ARM NEON, which flushes them).
- `VLD2`/`VST2` split interleaved complex values into real and imaginary
  vectors and back, so the FFT stages need no shuffles; `VLD2` on `.D2`
  does the same for the MDCT's float64 even/odd inputs.
- `REV64` then `EXT #8` reverses four lanes.
- `Float32ToInt16`: `FMAX`/`FMIN` propagate NaN, unlike x86 `MAXPS`, so
  the clamp is a `FCMGT` mask and `BSL`, which sends NaN to -32768 as the
  scalar `!(x > -32768)` does. Rounding is `FRINTN` (ties to even) then
  `FCVTZS`, then `XTN`.
- `silk_SMULWB` is `SMULL`/`SMULL2` (int32 x int32 -> int64) then
  `SHRN #16`, which keeps bits 16-47; one register holds four states, and
  the two-state variants use `.S2` and leave lanes 2 and 3 untouched.
- `NSQCand4` uses `SQADD`/`SQSUB` for the saturating add and subtract and
  `SRSHR` for the rounding shift, which the SSE version builds from sign
  tests.
- `FIR8Interp` widens with `SMULL`/`SMULL2` and sums four outputs with two
  `ADDP` levels, then `SQXTN` saturates.

Verification, all under `qemu-aarch64` on the amd64 VM: the kernel tests
on the NEON and fallback paths (including the PCM float32 sweep), the
packages' full test suites (official-vector and packet digests
included), and both random matrices, whose digests equal the amd64 ones.
Ten deliberate asm mutations across the kernels were run; nine failed the
tests and the tenth (`FCMGT` to `FCMGE` in the PCM clamp) is equivalent,
since both send -32768 to -32768; two non-equivalent PCM mutations were
caught. The CI FMA guard also scans comments, so the asm avoids naming
fused instructions.

Not measured: qemu timings say nothing about hardware. The CI `bench
(arm64)` job and the native arm64 test cells run once the PRs target
`main`.

## Phase 5 (implemented): remaining exact candidates

Profiled on the i7-11700 after phases 1-4. The remaining hot spots are mostly
serial (SILK synthesis, entropy and PVQ decoding, the PVQ search, warped
autocorrelation, float inner products). Two exact candidates were taken:

| Change | Method | Profile share (stereo decode benchmarks) |
|---|---|---:|
| `kernels.LPCAnalysisFilter` | FIR with exact int16 products and a wrapping int32 sum: four outputs per pass, PMADDWD (SSE2) / SMULL+SMULL2 (NEON) over 16 zero-padded taps, vector Q12 rounding and int16 saturation | 3.14% → 0.79% |
| Stereo de-emphasis in one loop (pure Go) | the two channels' serial recursions run side by side so the CPU overlaps them; each channel keeps its operation order | 5.16% → 1.98% |

End to end (6 interleaved rounds against phase 1-4): CELT stereo decode
-6.7%, hybrid stereo decode -8.9%; the other rows were within this run's
noise (+/-10-20%).

Considered and not taken: the NLSF delayed-decision quantizer
(`silkNLSFDelDecQuantInto`, about 3% of encode). Its per-state work is
dominated by table lookups (two output tables and the rate table per
state and coefficient), which need scalar gathers on SSE/NEON, so a lane
version would not pay for itself.

Verification added with this phase: `FuzzKernelsFloat`/`FuzzKernelsFixed`
(nightly, amd64 and arm64) and a plain-C oracle job in the opusref
workflow on ubuntu-latest and ubuntu-24.04-arm (encoder/decoder oracles,
libopus audit, parity hunt and chaos CTL cells). The libopus audit's NaN
input cells (`TestAuditEdgeInputs/nan/`) differ from libopus on `main` as
well and are skipped there.

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
