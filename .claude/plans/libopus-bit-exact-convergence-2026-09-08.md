# libopus Bit-Exact Convergence Plan

Last updated: 2026-09-13
Status: Active

## Objective

Continuously move the Pure Go codec toward the checked-in libopus reference,
with bit-exact behavior as the long-term target. Work in small, reviewable
slices: read the corresponding C implementation, port its arithmetic and state
transitions faithfully, run reference and regression tests, record the
remaining mismatch, and repeat.

## Scope

- Use the local `libopus/` source tree as the implementation reference for each
  slice. Name the C files and functions in tests or commit messages when useful.
- Prefer exact integer, rounding, saturation, entropy, and state-transition
  semantics over approximations that merely produce similar audio.
- Build hard oracles for deterministic behavior: accepted packet structure,
  decoded duration, integer PCM, final range, encoder packet bytes, and state
  after reset or mode transitions.
- Keep normal builds Pure Go. The `opusref` build tag may use CGO only for test
  oracles and diagnostics.
- Preserve RFC 6716 interoperability and all currently passing public API,
  official-vector, fuzz, race, and quality gates while convergence proceeds.
- Defer libopus 1.6.1 neural/AI features (including DRED, OSCE, and DNN-backed
  processing) until core codec convergence is exhausted. At that final stage,
  implement only features whose models, weights, licenses, runtime cost, and
  Pure Go integration are practical and independently testable.

The objective is intentionally broader than one release-sized phase. A slice
is complete only when its evidence is committed; the plan remains active while
any libopus/Go mismatch remains.

## Permanent Work Loop

1. Select the smallest observable mismatch and identify the exact libopus C
   call path that defines it.
2. Create a child experiment branch from this convergence branch. Add or
   tighten an oracle that fails on the mismatch and reports its first
   divergent packet, sample, symbol, range, or state value.
3. Port the relevant C arithmetic and state transitions to Go without unrelated
   policy changes.
4. Run the narrow oracle, package tests, the normal suite, and the applicable
   `opusref` comparison.
5. Commit the verified slice using Conventional Commits, update the mismatch
   inventory, merge the accepted commits back into the convergence branch, and
   return to step 1. Delete or retain rejected experiment branches as useful
   evidence, but do not mix their production changes into accepted slices.

If a bit-exact slice requires a large fixed-point rewrite, first commit the
stage-level trace/oracle that localizes the mismatch. This keeps later ports
measurable and prevents a superficially close waveform from hiding an earlier
state or entropy divergence.

## Initial Mismatch Inventory

| Area | Current evidence | Next exactness gate |
|---|---|---|
| Entropy coder | Documented and tested as matching libopus range coding | Retain final-range and symbol traces |
| Decoder framing/range | All 12 official vectors now have zero `FinalRange` mismatches against libopus 1.6.1 and their `.bit` records | Retain the all-vector zero-mismatch gate while localizing PCM divergence |
| int16 decode conversion | Conversion semantics match `FLOAT2INT16`; direct `opus_decode` comparison now reports exact-sample percentage, first/max LSB delta, and range mismatch count | Raise mode-specific exact-sample coverage after range alignment |
| CELT synthesis | Every normalized-energy, denormalized-spectrum, synthesis, post-filter, and PCM stage hash emitted for the three pure-CELT official vectors (01, 07, and 11) matches the checked-in scalar C source | Extend source-level localization to CELT portions of mixed-mode vectors without conflating SILK/CELT transition state or installed-libopus SIMD drift |
| SILK synthesis/PLC | Parameter/range coverage exists; PLC is explicitly non-bit-exact | Compare fixed-point state and PCM on deterministic packet sequences |
| Encoder | Packets interoperate but are not byte-identical | Add mode-specific byte and first-symbol divergence traces |
| Mode/rate policy | Known partial parity in `docs/MODE_RATE_POLICY_DIFF.md` | Compare decisions and state over identical PCM/control sequences |

## Acceptance Criteria Per Slice

- The relevant libopus C source and semantics are identified.
- A deterministic test demonstrates the old mismatch or guards the newly exact
  behavior.
- The implementation matches the selected C behavior, including rounding,
  saturation, overflow width, and state update order.
- Narrow tests, `go test -count=1 ./...`, and applicable `opusref` tests pass.
- The change is committed independently with a Conventional Commit message.

## Verification

```text
go test -count=1 ./...
go vet ./...
go test -count=1 -tags opusref ./...
go test -race -count=1 ./...
go test -count=1 -run '^TestOfficialVectors$' -v .
```

Each slice should also name and run a narrower command that proves its specific
oracle. Expensive full gates may be checkpointed after a sequence of small
commits, but no mismatch is considered removed until the applicable reference
test passes.

## Completed Slices

### 2026-09-08: int16 output conversion

- Reference: `libopus/celt/float_cast.h::FLOAT2INT16`,
  `libopus/celt/mathops.c::celt_float2int16_c`, and
  `libopus/src/opus_decoder.c::opus_decode`.
- Replaced zero-direction truncation with float32-domain scaling, saturation,
  and nearest-even rounding.
- Unified single-stream and multistream normal decode, PLC, and FEC int16
  conversions on the same helper.
- Verified the focused rounding oracle, `go vet ./...`, normal and `opusref`
  suites, the full race suite, and all 12 official vectors.

### 2026-09-08: libopus int16 convergence oracle

- Added a CGO wrapper for libopus `opus_decode`, retaining `opus_decode_float`
  for quality comparisons but no longer using mixed output APIs for the
  bit-exact scoreboard.
- `TestCGORef` now checks libopus `OPUS_GET_FINAL_RANGE` against every official
  `.bit` record, compares int16 samples directly, and reports exact-sample
  coverage, first/max LSB delta, and Go final-range mismatch count.
- The zero-mismatch range baselines for vectors 02-07 are hard regression
  gates; nonzero vector baselines may decrease but may not increase.
- The first remaining entropy mismatch is vector 01 packet 0: Go `0aa13300`,
  libopus/bitstream `16230400`. This is the next convergence slice.
- Verified with `go test -count=1 -tags opusref -run '^TestCGORef$' -v .`,
  `go vet ./...`, and the complete normal and `opusref` suites.

### 2026-09-08: single-stream multi-frame final range

- Reference: `libopus/src/opus_decoder.c::opus_decode_native` and
  `opus_decode_frame`. The outer packet loop overwrites `rangeFinal` after each
  constituent frame; it does not XOR frame ranges from one stream.
- Added a focused oracle for vector 01 packet 0. All three CELT frame ranges
  already matched libopus independently; only the Go packet aggregation was
  wrong (`0aa13300` instead of the last frame's `16230400`).
- Applied last-frame replacement to CELT-only, SILK-only, and hybrid decode.
  Multistream continues to XOR the final ranges of its elementary streams.
- Official-vector range mismatches fell from 3707 total to 27: vector 08 has
  1, vector 09 has 1, vector 10 has 13, vector 12 has 12, and all other vectors
  have zero.
- Verified the focused oracle, complete normal and `opusref` suites,
  `go vet ./...`, and the complete race suite.

### 2026-09-09: SILK and hybrid transition redundancy

- Reference: `libopus/src/opus_decoder.c::opus_decode_frame`, especially the
  SILK-only implicit redundancy rule, direction bit, CELT end-band selection,
  redundant range XOR, and `prev_redundancy` update.
- Localized vector 08 packet 4 through the C SILK symbol boundaries. Its SILK
  body range already matched; Go treated the following direction bit as a
  redundancy-presence bit and skipped a trailing SILK-to-CELT frame.
- Unified leading/trailing SILK and hybrid redundancy decoding, CELT state
  carry/reset, crossfade, and final-range XOR. Official-vector `FinalRange`
  mismatches fell from 27 to zero across all 12 vectors.
- Moved Go encoder CBR fill from the SILK frame body to RFC packet padding so
  libopus cannot mistake zero fill for transition redundancy. Added normal and
  `opusref` regression coverage, including per-packet FEC-stream range checks.
- Verified the focused vector oracle, complete normal and `opusref` suites,
  `go vet ./...`, and the complete race suite.

### 2026-09-09: CELT float decoder state and tv01 PCM oracle

- Reference: `libopus/celt/arch.h`, `quant_bands.c`, and
  `celt_decoder.c::deemphasis_stereo_simple`. The floating build uses
  `float` for `celt_glog`, every `opus_val` width, `celt_sig`, and the
  de-emphasis memory; the fixed-point-only `-28` coarse-energy clamp is absent.
- Kept fine-corrected `oldBandE` directly in the log domain instead of
  reconstructing it through linear energy and `log2`, and matched float32
  predictor, fine-energy, and de-emphasis state transitions.
- Added a constituent-frame tv01 oracle that compares both int16 and float32
  libopus output, sequential and reset-state decoding, maximum error location,
  and a fitted-scale residual. All three frame ranges remain exact. The first
  frame is int16 sample-exact; the next two differ at 17 and 1 samples,
  respectively, always by 1 LSB.
- The fitted-scale residual rules out a uniform gain error. The largest second
  frame float error is near its end, while the third frame's maximum is at its
  beginning, consistent with a long-block transform error followed by carried
  de-emphasis state. Go currently uses a Bluestein FFT while libopus uses its
  mixed-radix KISS FFT, making that transform boundary the next focused gate.
- Verified the focused oracle, complete normal and `opusref` suites,
  `go vet ./...`, and the complete race suite.

### 2026-09-10: bit-exact CELT float KISS FFT and inverse MDCT

- Reference: checked-in libopus 1.6.1 `celt/kiss_fft.c`, `_kiss_fft_guts.h`,
  `mdct.c`, and `static_modes_float.h`.
- Replaced the decoder IMDCT's Bluestein DFT with libopus's radix 2/3/4/5
  factor order, bit reversal, shared 480-point twiddle table semantics, and
  float32 butterfly ordering. FFT plans are immutable and cached without
  increasing the decoder allocation regression baseline.
- Added a C oracle built directly from the checked-in source. Deterministic
  all-output float32 hashes match for every CELT FFT size (60, 120, 240, 480)
  and every inverse-MDCT size (120, 240, 480, 960), including windowed overlap
  and carry state.
- tv01's remaining 17- and 1-sample int16 differences did not change. Because
  the transform boundary is now independently bit-exact, the next divergence
  is before IMDCT in PVQ normalization/spreading or band denormalization.
- Verified the C hash oracle, allocation gate, focused tv01 oracle, complete
  normal and `opusref` suites, `go vet ./...`, and the complete race suite.

### 2026-09-12: bit-exact tv01 PVQ and band denormalization

- Reference: checked-in libopus 1.6.1 `celt/cwrs.c::cwrsi`,
  `vq.c::alg_unquant`/`exp_rotation`/`renormalise_vector`,
  `bands.c::quant_partition`/`stereo_merge`/`denormalise_bands`, and the
  standard 48 kHz pre-emphasis value in `static_modes_float.h`.
- Added a constituent-frame C oracle that traces normalized coefficients,
  fine-corrected `oldBandE`, denormalized spectra, CWRS indices and pulse
  vectors, PVQ normalization/rotation, and stereo-merge state directly from
  the checked-in C source.
- Replaced the saturated-V reconstruction of CELT's symmetric `U(N,K)` table
  with its direct recurrence. This fixes the near-`uint32` index
  `V(24,9)=4003707568`, where Go and C consumed index `2779010792` but formerly
  produced different integer pulse vectors.
- Matched float32 assignment order in pulse normalization, spreading,
  recursive split gains, Haar transforms, spectral folding, folding
  renormalization, and stereo merge. tv01 frame 1 now matches libopus in every
  normalized band and energy word.
- Removed a second, non-libopus normalization from band synthesis. Applying
  the decoded amplitude directly and retaining the uncoded zero tail makes
  both complete 960-value denormalized spectra bit-exact.
- Updated the standard 48 kHz de-emphasis coefficient from the rounded `0.85`
  to libopus's `27853/32768`. tv01 packet 0 now has exact int16 output for all
  three constituent frames in both sequential and reset-state decoding. Its
  float32 output still has sub-LSB drift (maximum below 0.000005 int16 LSB in
  the focused run), so convergence remains active.
- Shared encoder PVQ arithmetic changed one deterministic hybrid-stereo packet
  digest; the new digest was stable across repeated runs and the complete
  libopus interoperability suite remained green.
- Verified the focused coefficient and tv01 oracles, `go test -count=1 ./...`,
  `go test -count=1 -tags opusref ./...`, `go vet ./...`, and
  `go test -race -count=1 ./...`.

### 2026-09-13: transient anti-collapse and static transform tables

- Reference: checked-in libopus 1.6.1 `celt_decoder.c::anti_collapse` and
  `celt_synthesis`, `mdct.c::clt_mdct_backward_c`, `kiss_fft.c`, and the
  standard tables in `static_modes_float.h`.
- Changed transient anti-collapse to consume fine-corrected `oldBandE`
  directly and mirrored libopus float32 threshold, history, exponential,
  noise, and renormalization order. Both tv01 frame-0 channels now match all
  21 normalized bands, all 21 energy words, and their complete denormalized
  coefficient hashes.
- Localized the next drift to MDCT pre-rotation: the coefficients entering the
  transform were exact, but Go regenerated twiddles with `math.Cos` while the
  standard libopus mode uses rounded static constants. A checked-in generator
  now converts the libopus 1.6.1 window, FFT-twiddle, and MDCT-twiddle literals
  to exact Go float32 bit patterns.
- Corrected the standalone FFT/MDCT oracle to use the standard static mode
  rather than Custom Mode's runtime-generated tables. Exact hashes pass for all
  four FFT and inverse-MDCT sizes.
- Added strict tv01 packet-0 stage hashes. All three constituent frames now
  match the scalar C source after synthesis, after the inactive comb filter,
  and after de-emphasis/PCM scaling. Sequential and isolated int16 output
  remains exact. The installed libopus build retains sub-LSB float differences,
  consistent with build-specific compiler/SIMD arithmetic rather than the
  checked-in scalar call path.
- Tightened every official-vector `FinalRange` allowance to zero after a fresh
  all-vector run confirmed zero mismatches. The next CELT target is tv01 frame
  33, where the full-vector int16 comparison first differs by one LSB.
- Verified generated-table stability, the focused static transform and tv01
  stage oracles, `go test -count=1 ./...`,
  `go test -count=1 -tags opusref ./...`, `go vet ./...`, and
  `go test -race -count=1 ./...`.

### 2026-09-13: tv01 packet 33 scalar synthesis

- Replayed the complete packet sequence through tv01 packet 33 in both Go and
  the checked-in scalar libopus decoder, preserving CELT state, packet
  bandwidth, channel count, duration, and constituent-frame geometry.
- The installed libopus build's first one-LSB int16 difference is packet 33,
  sample 6881, which lies in constituent frame 3. That constituent matches the
  checked-in scalar C source exactly after synthesis, after the inactive comb
  filter, and after de-emphasis/PCM scaling on both channels.
- This rules out a source-level Go/C mismatch at that point; the installed
  build difference is consistent with its compiler/SIMD float path. The next
  gate is a vector-wide scalar-source stage scan that can identify the first
  actual CELT source-conformance mismatch without chasing build-specific
  sub-LSB noise.
- Verified the rebuilt scalar oracle, the focused packet-33 stage test,
  `go test -count=1 ./...`, and `go test -count=1 -tags opusref -run
  '^TestCGORef$' -v .`.

### 2026-09-13: vector-wide scalar CELT convergence

- Added an opt-in stateful oracle scan that compiles the checked-in scalar
  libopus source and compares normalized coefficients, fine-corrected energy,
  denormalized spectra, synthesis, comb filtering, and PCM after every CELT
  constituent frame. The oracle also covers libopus's mono-stream/stereo-output
  synthesis path without treating its duplicated output channel as a missing Go
  internal stage.
- The scan localized and removed four independent float-source mismatches:
  synthesis now derives band amplitudes after both fine-energy passes, the
  post-filter mirrors float32 multiply/add assignment order, near-midpoint
  `exp2f` band gains use a deterministic correctly-rounded fallback, and the
  special two-sample stereo resynthesis uses float32 intermediates in C order.
- A pure-CELT vector sweep found a fifth mismatch in narrow-band mono: Go used
  seven as the final-fine cutoff while libopus `MAX_FINE_BITS` is eight. The
  corrected cutoff consumes the eighth-bit refinement and preserves raw-tail
  bit order for later bands.
- All emitted scalar stage hashes now match for all three pure-CELT official
  vectors: tv01 (66,288 comparisons), tv07 (37,464 Go stages plus exact C-side
  mono output duplicates), and tv11 (18,012 comparisons). Installed libopus may
  still differ by one int16 LSB because its compiler/SIMD path is independent
  from the checked-in scalar oracle.
- The eighth-bit correction also improved installed-libopus int16 equality on
  tv07 from 99.451% to 99.999%, tv09 from 89.189% to 96.404%, and tv10 from
  78.914% to 84.982%, with every official-vector final range still exact.
- Verified all three scalar sweeps, `go vet ./...`, the full normal and
  `opusref` suites, the full race suite, and the verbose all-vector CGO oracle.
