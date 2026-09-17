# libopus Bit-Exact Convergence Plan

Last updated: 2026-09-15
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
| SILK synthesis/PLC | Normal-frame synthesis, PLC, CNG, and post-loss glue are sample-exact on the SILK oracles (2026-09-15) | Extend exactness to mixed-mode official vectors and CELT PLC |
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

### 2026-09-15: SILK packet-loss concealment and comfort noise

- Reference: checked-in libopus 1.6.1 `silk/PLC.c`, `silk/CNG.c`,
  `silk/decode_frame.c`, `silk/decode_parameters.c`, `silk/decode_core.c`,
  `silk/dec_API.c`, and `src/opus_decoder.c::opus_decode_frame`.
- Re-established the baseline first: four `opusref` failures inherited from
  the 2026-09-09 CBR-padding slice were stale test contracts (raw packet
  sizes and count codes hid LBRR behind code-3 padding); the tests now inspect
  unpadded packets and LBRR flags. The remaining
  `TestCGODecodeFECMatchesLibopus/60ms` failure bisected to the Q1 encoder
  port, which made a fixture subframe inactive and exposed the Go PLC
  approximation on the LBRR gap.
- Replaced the heuristic SILK concealment with a fixed-point port of
  `silk_PLC_update`, `silk_PLC_conceal`, `silk_PLC_glue_frames`, `silk_CNG`,
  the post-loss `BWE_AFTER_LOSS_Q16` bandwidth expansion, the voiced-to-unvoiced
  LTP smoothing in `silk_decode_core`, the `LastGainIndex` reset on lost
  packets, and the decoder-control conventions (zero pitch lags, LTP
  coefficients, and LTP scale for non-voiced frames; `lagPrev` from the last
  subframe). The decoder keeps the previous frame's `exc_Q14` and a
  `MAX_FRAME_LENGTH` excitation buffer so the random-noise source indexes the
  same stale samples as C.
- A SILK payload of at most one byte is now concealed as a lost frame with a
  zero final range, as `opus_decode_frame` does; the previous Go-specific
  "digital silence" state was removed. The SILK-only encoder reports the last
  constituent frame's range and zero for one-byte frames, matching
  `opus_encode_native`, so encoder and decoder final ranges agree again on
  silent LBRR carriers.
- New oracles compare Go int16 output with libopus `opus_decode` sample for
  sample at the SILK internal rate: whole-packet losses (8/12/16 kHz,
  10/20/40/60 ms, single to triple losses, mono and stereo), in-band FEC with
  PLC-filled LBRR gaps, and one-byte payloads followed by explicit loss. All
  are exact. `TestCGORefSILKAndHybridPLC` now reports +Inf dB for SILK mono
  and stereo; its absolute 10 dB target floor became a parity-with-libopus
  check because the concealment quality is now libopus' own.
- Hybrid PLC still differs through the CELT concealment path; CELT PLC is the
  next decoder exactness gate.
- Verified the focused SILK oracles, `go vet ./...`, `go test -count=1 ./...`,
  `go test -count=1 -tags opusref ./...` (only the pre-existing
  speech-like-harmonic loudness gates fail), and `go test -race -count=1 ./...`.

### 2026-09-15: SILK LTP correlation and gain quantization

- Reference: checked-in libopus 1.6.1 `silk/float/find_LTP_FLP.c`,
  `silk/float/corrMatrix_FLP.c`, `silk/float/wrappers_FLP.c::silk_quant_LTP_gains_FLP`,
  `silk/quant_LTP_gains.c`, `silk/VQ_WMat_EC.c`, and `silk/lin2log.c`.
- Added `internal/cgoref/silk_q2_opusref.go`, which calls the C
  `silk_find_LTP_FLP` and `silk_quant_LTP_gains_FLP` on injected float32
  residuals, lags, and cumulative-gain state, and
  `TestSILKQ2LTPOracle` in `internal/silk`, which compares XX/xX by float32
  bits and every index, gain, and `sum_log_gain_Q7` by exact integer.
- Replaced the float64 approximation of the LTP VQ with exact ports: the
  correlation matrix updates multiply and subtract in float before joining the
  double accumulator, the normalisation floor and scaling are float32, the
  correlations are converted to Q17 with nearest-even `float2int`, and the
  codebook search runs the Q24/Q15/Q8 fixed-point arithmetic of
  `silk_VQ_WMat_EC_c` including the gain-penalty and `lin2log` bit-cost. The
  reported prediction gain uses the residual energy of the last codebook
  evaluated, as libopus does. `ltpSumLogGainQ7` is now an `int32`.
- All eight fixtures (8/12/16 kHz, 2 and 4 subframes, strong/weak/noisy
  voicing, long lags, gain-capped state) are exact. The production encoder's
  packets changed, so the perf-regression digests were regenerated; the
  libopus AB scoreboard moved in both directions (8 kHz steady-voiced and
  16 kHz steady-voiced lost margin, 8 kHz speech-harmonic and 16 kHz onset
  gained) and the matched-loudness gate now fails only at 12 kHz instead of
  8/12/16 kHz. The Go encoder still feeds this stage its own LPC residual
  without look-ahead rather than libopus' `res_pitch`, which is the next
  exactness gate.
- Finding recorded for Phase 4: libopus' SILK encoder codes each frame with a
  5 ms `LA_SHAPE_MS` look-ahead delay and the Opus layer adds `Fs/250` delay
  compensation, so the audio slice inside each libopus frame is offset from
  the Go encoder's. Encoder byte-exactness therefore needs a framing/delay
  restructuring slice before any full-packet comparison can be exact.
- Verified the Q1 and Q2 oracles, `go vet ./...`, `go test -count=1 ./...`,
  `go test -count=1 -tags opusref ./...` (only the 12 kHz speech-like-harmonic
  loudness gate fails), and `go test -race -count=1 ./...`.

### 2026-09-15: SILK pitch analysis in float32

- Reference: checked-in libopus 1.6.1 `silk/float/find_pitch_lags_FLP.c`,
  `silk/float/pitch_analysis_core_FLP.c`, `silk/float/apply_sine_window_FLP.c`,
  `silk/float/autocorrelation_FLP.c`, `silk/float/schur_FLP.c`,
  `silk/float/k2a_FLP.c`, `silk/float/bwexpander_FLP.c`,
  `celt/pitch.c::celt_pitch_xcorr_c`, `celt/pitch.h::xcorr_kernel_c`, and
  `silk/float/SigProc_FLP.h` (`silk_float2short_array`, `silk_log2`,
  `silk_max_float`).
- Added `cgoref.SILKPitchAnalysisCore` and `cgoref.SILKFindPitchLags`, which
  replay the C whitening chain with the exported libopus helpers on an
  injected `x_buf` (history, frame, and `la_pitch` look-ahead) and call the
  scalar-arch pitch core. `TestSILKQ2PitchOracle` compares autocorrelation,
  Schur residual energy and reflection coefficients, prediction gain, LPC
  coefficients, the whole residual, the voicing threshold, LTPCorr, lag and
  contour indices, and every subframe lag for ten fixtures (8/12/16 kHz,
  10/20 ms, complexity 0-10, with and without a previous lag, noise, and a
  long lag).
- Replaced the float64 pitch estimator with `pitch_core_flp32.go`: float32
  rounding at every `silk_float` operation, double accumulation only where C
  uses double (energies, inner products, the stage-1 recursive normalizer,
  the Schur recursion), the four-lag `xcorr_kernel` float accumulation order
  for stage 1 and stage 3, nearest-even `float2int` before the fixed-point
  decimators, and the `silk_max_float` macro semantics (the Schur denominator
  stays double). The encoder converts the VAD's float activity and tilt to Q8
  and Q15 integers and uses `SILK_FIX_CONST` Q16 thresholds like
  `silk_setup_complexity`.
- All ten fixtures are exact at every checkpoint. Packet digests and the AB
  scoreboard did not change, so the earlier float64 estimator was already
  producing identical decisions on these fixtures; the port removes the
  remaining numerical risk rather than a measured divergence.
- Still open in this stage: the encoder has no `la_pitch` look-ahead (the LPC
  window ends at the frame boundary), the VAD is a float port of the
  fixed-point `silk_VAD_GetSA_Q8`, and the Go-specific first-frame long-lag
  guard remains in front of the voicing decision.
- Verified the Q1/Q2 oracles, `go vet ./...`, `go test -count=1 ./...`,
  `go test -count=1 -tags opusref ./...` (only the 12 kHz speech-like-harmonic
  loudness gate fails), and `go test -race -count=1 ./...`.

### 2026-09-15: LTP quantization on the pitch-analysis residual

- Reference: `silk/float/encode_frame_FLP.c` (`res_pitch`/`res_pitch_frame`)
  and `silk/float/find_pred_coefs_FLP.c`, which correlate the pitch-analysis
  residual once per frame and update `sum_log_gain_Q7` once.
- The Go frame pipeline consulted the LTP quantizer from three stages
  (gain bootstrap, gain-plan selection, and the final index encode), each
  time on its own quantized-LPC residual and each time advancing
  `sum_log_gain_Q7`. The encoder now keeps the whitened residual from
  `silkFindPitchLags` (`res_pitch`, history plus frame plus `LTP_ORDER`
  zeros standing in for the missing look-ahead), computes the LTP result once
  per frame, caches it, and re-applies the single `sum_log_gain_Q7` update
  from every consumer so the rate-control state restores cannot lose it.
- Scoreboard (gap_SNR_matched, negative = Go ahead): speech-like-harmonic
  8k -6.94 → -8.55, 12k -4.87 → -5.47, 16k -3.65 → -5.03; steady-voiced
  8k -1.86 → -2.25, 12k -1.18 → -1.48, 16k +0.41 → +1.19; onset
  8k -1.30 → -1.20, 12k -2.58 → -0.38, 16k -1.57 → +2.20. Every
  `TestOpusSILKABAgainstLibopusEncoder` gate now passes, including the three
  matched-loudness gates that had been failing since the Q1 port
  (8k -1.25 dB, 12k -1.18 dB, 16k -0.92 dB). The internal quality baseline's
  decoded-pitch tracker was made octave-robust because a perfectly periodic
  decoded frame correlates equally at the fundamental and its octave.
- The perf-regression packet digests were regenerated (stable across runs).
- Verified `go vet ./...`, `go test -count=1 ./...`,
  `go test -count=1 -tags opusref ./...`, and `go test -race -count=1 ./...`.

### 2026-09-15: fixed-point SILK VAD and first-frame voicing rule

- Reference: checked-in libopus 1.6.1 `silk/VAD.c` (`silk_VAD_Init`,
  `silk_VAD_GetSA_Q8_c`, `silk_VAD_GetNoiseLevels`), `silk/ana_filt_bank_1.c`,
  `silk/sigm_Q15.c`, and `silk/float/find_pitch_lags_FLP.c`'s
  `first_frame_after_reset` gate.
- Replaced the float approximation of the VAD with the fixed-point port
  (Q8 activity, Q15 tilt and band quality, int32 noise-level and smoothed-SNR
  state). Because `silk_VAD_GetSA_Q8_c` takes the whole encoder state,
  `cgoref.SILKVAD` replays the C body verbatim on a private state using the
  exported `silk_ana_filt_bank_1`, `silk_lin2log`, and `silk_sigm_Q15`;
  `TestSILKVADOracle` compares every frame of five 40-frame sequences (8/12/16
  kHz, 10/20 ms, speech-like, noise-then-tone, quiet, loud noise) and all are
  identical. The pitch stage now receives the Q8/Q15 integers directly.
- Applied libopus' rule that the pitch estimator does not run on the first
  frame after a reset (coded unvoiced). This replaces the Go-specific
  16 kHz first-frame long-lag guard, which had approximated the same
  behaviour for one case; running the estimator on the first frame drops the
  16 kHz speech-like-harmonic matched loudness to -8 dB, so the rule is
  confirmed as the fix rather than a regression.
- Scoreboard: 8k steady-voiced -2.25 → -2.94, 8k speech-harmonic -8.55 →
  -5.03, 12k steady-voiced -1.48 → -0.15, 12k onset -0.38 → +0.91, 16k
  steady-voiced +1.19 → +2.88, 16k speech-harmonic -5.03 → -5.47. Matched
  loudness is 8k -1.62 dB (FAIL by 0.12 dB), 12k -0.95 dB, 16k -1.05 dB. The
  8 kHz gate is left failing without a threshold change; the unvoiced first
  frame is expensive under the Go rate control (a 24 kbps CVBR frame of a pure
  tone reaches 119 bytes), which points at the gain-loop/rate-control slice.
  `TestEncoderSILKOnlyVBRAndDTXDoNotUseCBRPadding` now asserts the absence of
  padding instead of a byte bound that depended on the old voiced first frame.
- Verified `TestSILKVADOracle`, the Q1/Q2 oracles, `go vet ./...`,
  `go test -count=1 ./...`, `go test -count=1 -tags opusref ./...` (only the
  8 kHz loudness gate fails), and `go test -race -count=1 ./...`.

### 2026-09-15: quantizer-offset sparseness on the pitch residual

- Reference: `silk/float/noise_shape_analysis_FLP.c` sparseness processing.
- The unvoiced quantizer-offset decision now measures the 2 ms segment energy
  variation of `res_pitch` in silk_float (`(float)nSamples + (float)energy`,
  `silk_log2`, float accumulation) instead of a Go-specific LPC/LTP excitation.
  Scoreboard moved marginally (8k speech-harmonic loudness -1.62 → -1.52 dB,
  12k steady-voiced -0.15 → +0.86); the 8 kHz loudness gate still fails by
  0.02 dB. Packet digests regenerated.
- Recorded the encoder input pipeline (look-ahead, delay compensation,
  high-pass conditioning, VAD flag semantics) as a specification in
  `.claude/specs/encoder-input-pipeline-libopus.md`; it is the next slice and
  a prerequisite for Q3 exactness and Phase 4.
- Verified `go vet ./...`, `go test -count=1 ./...`,
  `go test -count=1 -tags opusref ./...` (only the 8 kHz loudness gate fails),
  and `go test -race -count=1 ./...`.

### 2026-09-15: VAD flags from the fixed-point activity (input pipeline step 0)

- Reference: `silk/float/encode_frame_FLP.c::silk_encode_do_VAD_FLP` and
  `silk/enc_API.c` (VAD flags patched into the reserved header bits after all
  frames are coded; `nBits -= nBitsUsedLBRR` charges LBRR to the regular
  frames).
- Removed the Go-specific energy/flatness/zero-crossing VAD (`vad.go`) that
  decided the per-frame VAD flags. `EncodeMulti` now runs the fixed-point
  `silk_VAD_GetSA_Q8` port once per frame in frame order before coding,
  derives the flag from `speech_activity_Q8 >= SILK_FIX_CONST(0.05, 8)`,
  tracks `noSpeechCounter` like libopus, and reuses the stored per-frame
  activity/tilt/quality inside `encodeRangeFrame`, so the VAD state advances
  exactly once per frame (also for LBRR re-encodes of the same frame).
- `TestLBRRRegularPathDeterminism` now compares FEC and non-FEC encoder state
  only until LBRR bits are spent: the exact VAD marks a long stationary tone
  inactive after a few hundred milliseconds, which exposed that the Go rate
  control (like libopus) charges LBRR bits to the regular budget, so the old
  unconditional state-equality assertion was not a libopus property.
- Scoreboard: onsets improved (8k -0.94 → -3.49, 12k +0.91 → -0.70, 16k
  +1.98 → +1.44); matched loudness 8k -1.46 (PASS), 12k -1.52 (FAIL by
  0.02 dB), 16k -0.64. Packet digests regenerated.
- Verified `go vet ./...`, `go test -count=1 ./...`,
  `go test -count=1 -tags opusref ./...` (only the 12 kHz loudness gate
  fails), and `go test -race -count=1 ./...`.

### 2026-09-15: SILK look-ahead buffer and Opus-layer delay (input pipeline steps 1–2)

- Reference: `silk/float/encode_frame_FLP.c` (`x_buf = ltp_mem | frame |
  LA_SHAPE_MS`, new input at `x_frame + LA_SHAPE_MS*fs_kHz`, `silk_memmove`
  after the frame) and `src/opus_encoder.c` (`delay_buffer` / `pcm_buf`,
  `delay_compensation = Fs/250`, `OPUS_GET_LOOKAHEAD`).
- `internal/silk.Encoder` keeps `xBuf`; `encodeRangeFrame` runs the VAD on
  the caller's frame, pushes it at `x_frame + LA_SHAPE`, and codes
  `x_frame[0:frame]` (previous 5 ms + current 15 ms). Pitch analysis reads
  the real `la_pitch` window, the LTP residual comes from the whitened
  look-ahead instead of zero padding, and the noise-shape windows read the
  real `la_shape` past the frame. The side encoder of a mid-only stereo frame
  does not advance, like libopus.
- `opus.Encoder` keeps `delayBuffer`; CELT (main and redundant frames, all
  frame sizes) codes the input delayed by Fs/250 while SILK and the
  bandwidth analysis see the current frame. `Lookahead()` now reports
  Fs/400 + Fs/250 (312 at 48 kHz; Fs/400 for restricted low delay).
- Test contracts that encoded fixture accidents were corrected:
  `TestCGOSILKFECGapExact` picks the FEC carrier from the encoded LBRR masks;
  `TestCGOEncodeRefSILKFEC` averages FEC-vs-PLC recovery over nine frames
  (single-frame recovery of the ~2-byte LBRR swings by several dB with the
  frame's pitch decisions); `TestHybridCVBROnsetBudgetOvershoot` moves its
  low-band burst so the reset onset still overshoots; the hybrid→CELT
  transition test budgets 16 kbps because the delayed SILK frame carries the
  warm-up tone; `TestDecoderPLCSILKAndHybrid` bounds the second concealed
  frame at 1.5× (libopus rises 1.48× on the new packets, Go within 0.5 %);
  `surroundAlignedSNR` compared an absolute error against a ratio and so
  always scored delay 0 — fixed, and the mask-trim test now scores the
  steady state with a +1.5 dB center gate (measured +2.4 dB).
- Scoreboard (`TestOpusSILKABAgainstLibopusEncoder`): SNR gates all pass;
  matched loudness on speech-like-harmonic 8k -1.71 / 12k -1.16 / 16k
  -1.76 dB (8k and 16k outside ±1.5; before: -1.46 / -1.52 / -0.64). Toggling
  the pitch and noise-shape look-ahead individually moves these by ±0.5 dB
  either way, so the shift is the framing itself acting on the unfaithful
  gain loop (Q5), not a look-ahead bug. Packet digests regenerated.
- End-to-end delay versus libopus (noise fixture, libopus decoder): CELT
  312 = 312; SILK/hybrid 278 vs 312 — the SILK API resampler port is the
  remaining alignment item (spec "Open items").
- Verified `go vet ./...`, `go test -count=1 ./...`,
  `go test -count=1 -tags opusref ./...` (only the 8/16 kHz loudness gates
  fail), and `go test -race -count=1 ./...`.

### 2026-09-16: input high-pass conditioning (input pipeline step 3)

- Reference: `src/opus_encoder.c` (`hp_cutoff`, `silk_biquad_res` float
  body, `dc_reject` float body, the `variable_HP_smth2_Q15` smoother, the
  float-API NaN/energy guard) and `silk/HP_variable_cutoff.c`.
- `encoder_input.go`: every packet is conditioned before SILK and the CELT
  delay buffer see it — VOIP runs the SILK-fixed-point-coefficient biquad at
  `silk_log2lin(variable_HP_smth2_Q15 >> 8)` Hz, other applications the 3 Hz
  DC blocker; the smoother follows the SILK encoder's
  `variable_HP_smth1_Q15` for SILK/hybrid packets and the 60 Hz minimum for
  CELT-only; a non-finite or ≥1e9 frame energy zeroes the frame and the
  filter memory. The bandwidth analysis and the digital-silence shortcut keep
  reading the raw input (libopus analyses `pcm`, not `pcm_buf`).
- `internal/silk/hp_variable_cutoff.go`: `silk_HP_variable_cutoff` port run
  once per frame before the frame's VAD result is installed.
- Oracles: `TestCGOInputConditioningExact` (hp_cutoff at 60/73/100 Hz,
  dc_reject, smoother; 8–48 kHz, mono/stereo, state carried over 6 frames,
  float32 bits) and `TestSILKHPVariableCutoffOracle` (400 random steps at
  8/12/16 kHz) compare against the libopus 1.6.1 bodies compiled by the
  reference C compiler (`internal/cgoref/hp_opusref.go`).
- Tests that scored a VOIP encoder against the raw input now score against
  `Encoder.lastConditionedInput` (the high-pass shifts the phase of low
  harmonics by a fraction of a sample, capping an integer-aligned SNR):
  `TestEncoderSILKOnlyQualityBaseline`,
  `TestEncoderHybridToCELTRedundancyStateContinuity`.
- Scoreboard (matched gap, positive = libopus ahead; before → after):
  8k steady -2.68 → +1.01, 8k harmonic -3.25 → -0.94, 8k onset -3.21 →
  -0.89, 12k steady +0.75 → +2.04, 16k steady +3.52 → +4.72, 16k onset -0.53
  → +2.60; matched loudness 8k -2.33 / 12k -1.87 / 16k -0.59 dB (8k and 12k
  outside ±1.5). Part of the previous Go advantage was the missing high-pass
  (the Go output kept low-frequency content the raw-input reference rewards)
  and the rest is the cutoff-trajectory phase effect noted in the spec; the
  loudness gap itself is phase-insensitive and remains the Q5 gain-loop item.
  Packet digests regenerated.
- Verified `go vet ./...`, `go test -count=1 ./...`,
  `go test -count=1 -tags opusref ./...` (only the 8/12 kHz loudness gates
  fail), and `go test -race -count=1 ./...`.

### 2026-09-16: SILK int16 front end and the encoder input oracle (input pipeline step 4)

- Reference: `silk/enc_API.c` (`RES2INT16` = `FLOAT2INT16`, `silk_resampler`
  encoder direction with `delay_matrix_enc`, `inputBuf + 1`),
  `silk/float/encode_frame_FLP.c` (eight ±1e-6f anti-denormal offsets).
- `internal/silk/front_end.go`: each input frame is quantised to the int16
  grid (float32 ×32768, saturate, round-half-even) and delayed by the
  equal-rate resampler delay plus one sample (mono; stereo applies the
  offset inside `lrToMS`), then pushed into `x_buf` with the float32 offsets.
  Values stay int16/32768 (or (int16 ± 1e-6f)/32768) in float64, so the
  ×32768 conversions downstream recover the libopus float32 values exactly.
  The side encoder keeps its delay line across the mid-only reactivation
  reset (libopus keeps the resampler state).
- Oracle: `scripts/oracle/build_encoder.ps1` + `scripts/oracle/enc_oracle.c`
  build the checked-in libopus 1.6.1 (scalar, `-ffp-contract=off`) with
  `opus_encoder.c`/`encode_frame_FLP.c` instrumented to dump `pcm_buf`,
  `inputBuf`, `x_buf`, `speech_activity_Q8`, the HP smoothers, and the packet
  bytes per frame. `TestSILKEncoderInputPipelineOracle` (opusref; skips
  without the exe) runs it on the mono AB fixtures and gates bit-exactness
  of the conditioned input, `x_buf`, VAD activity, and HP state for every
  frame up to the first packet difference.
- Result: steady-voiced, speech-like-harmonic, and (LCG) unvoiced-noise at
  8/12/16 kHz are exact through frame 0 (first packet difference at frame 0:
  the remaining analysis/rate-control stages). `onset` exposes the Go
  digital-silence shortcut (spec "Open items"). Packet digests regenerated.
- Verified `go vet ./...`, `go test -count=1 ./...`,
  `go test -count=1 -tags opusref ./...` (only the 8 kHz loudness gate
  fails, -2.61 dB), and `go test -race -count=1 ./...`.

### 2026-09-16: libopus encoder resampler, stage traces, and the divergence map

- `94938d2` feat(encoder): the Opus layer feeds SILK through
  `silk.NewEncoderResampler` (encoder direction of the bit-exact
  `silk_resampler` port: `delay_matrix_enc`, `forEnc` init; exact against
  `silk_resampler(forEnc=1)` for every 8/12/16/24/48 → 8/12/16 kHz pair,
  `TestSILKEncoderResamplerOracle`). End-to-end delay now equals libopus in
  every mode (48 kHz: 312; 24 kHz: 155/156; 16 kHz: 104 samples).
  `TestEncoderHybrid24kUnvoicedNoiseDoesNotCollapse` scores low-band energy
  instead of the sign of a noise-waveform alignment scale.
- `66543b8` the pipeline oracle covers 24/48 kHz input (wideband): the
  conditioned input and x_buf stay bit-exact through the resampler.
- `befc6f7` `build_encoder.ps1` also instruments find_LPC, find_pred_coefs,
  noise_shape_analysis, process_gains, wrappers (NSQ inputs) and NSQ_del_dec
  with anchor-checked replacements; `0173420` adds `silk.FrameTrace`
  (`Encoder.LastFrameTrace`) and the oracle test lists every differing NSQ
  input per frame; `08befec` hands the NSQ pitchL = 0 and LTP_scale_Q14 = 0
  for non-voiced frames like wrappers_FLP.c (trace-only, digests unchanged).
- Divergence map on identical input (frame 0 after reset, unvoiced, 8/12/16
  kHz): NLSF_Q15 and PredCoef_Q12 match libopus; Gains_Q16 match on subframe
  0 and drift on 1–3 (process_gains / gain loop, Q5); AR_Q13, Tilt_Q14,
  LF_shp_Q14 differ because the Go unvoiced path deliberately zeroes the
  shaping (Q3); Lambda_Q10 is off by 1–2. From frame 1 on, NLSF_Q15 differs
  as a consequence (gain-scaled LPC input). Order of attack: Q3 unvoiced
  shaping exactness → process_gains and the encode_frame_FLP gain loop (Q5)
  → seed/pulses follow.

### 2026-09-16: Q3 noise-shape analysis exact (`63808a3`, `ad81c1f`)

- Reference: `silk/float/noise_shape_analysis_FLP.c`, `silk/float/wrappers_FLP.c`
  (Q13/Q14/Q10 conversions), `silk/float/process_gains_FLP.c` (Lambda),
  `silk/enc_API.c` (TargetRate_bps: packet budget minus the LBRR usage
  average, bit reservoir `nBitsExceeded`, in-packet balance), `silk/control_codec.c`
  (`warping_Q16 = fs_kHz * SILK_FIX_CONST(0.015, 16)`), `src/opus_encoder.c`
  (SILK bit rate = `bits_to_bitrate(bitrate_to_bits(bitrate) - 8)`, i.e. the
  TOC byte amortised: 23600 bps at 24 kbps / 20 ms).
- `internal/silk/noise_shape_flp32.go`: float32-faithful
  `silkNoiseShapeAnalysisFLP32` (float32 constants, `silk_log2` via log10,
  double warped autocorrelation/Schur, float sqrt/pow casts). Verified
  bit-exact against the oracle's `SHAPE_*_FLP` dumps (AR, Gains, LF_MA/AR,
  Tilt, HarmShapeGain, input/coding quality) on every frame whose inputs the
  two encoders share (frame 0 on all fixtures, every frame on unvoiced
  noise, frame 1 once the target-rate reservoir matched).
- `internal/silk/target_rate.go`: the libopus per-frame target rate drives
  `SNR_dB_Q7`; `opus.go` hands SILK the TOC-adjusted bit rate; the warping
  off-by-one (15729 vs 15728 at 16 kHz) fixed.
- `ad81c1f`: the NSQ now takes AR_Q13/LF_shp/Tilt/HarmShapeGain/Lambda from
  the exact analysis for every frame type; the Go-specific neutral shaping
  for unvoiced and stereo components is gone. Scoreboard: **matched loudness
  8k -0.64 / 12k -0.25 / 16k -0.01 dB (all pass)** — the long-standing
  loudness gap was the unfaithful shaping — and matched SNR gaps move toward
  zero (8k steady -1.68, 8k harmonic -1.54, 12k steady +0.19, 16k steady
  +0.79, 16k onset +2.42, unvoiced-noise 8k +0.99 / 12k +0.65 / 16k -0.16).
- Test contracts: `TestLBRRNormalDecodeConsumesRedundancy` compares packet 0
  exactly and later packets within 15 dB (libopus charges LBRR to the
  target rate, so regular frames legitimately differ);
  `TestHybridCVBROnsetBudgetOvershoot` retired (its SILK overshoot can no
  longer be produced by a benign fixture; `TestHybridCVBROnsetFinalRange`
  keeps the allocation guard); the hybrid-mono PLC parity gate allows 0.5 dB
  because the CELT PLC is not a bit-exact port. Packet digests regenerated.
- Remaining frame-0 divergence: Gains_Q16 from subframe 1 (process_gains /
  the encode_frame_FLP gain loop, Q5), then seed/pulses follow.
- Verified `go vet ./...`, `go test -count=1 ./...`,
  `go test -count=1 -tags opusref ./...` (all pass), `go test -race`.

### 2026-09-16: Q5 (VBR) exact gains — first byte-identical SILK packets (`d0e2532`)

- Reference: `silk/float/encode_frame_FLP.c` (with `useCBR == 0` the
  quantiser loop breaks after the first pass whenever it fits `maxBits`, so
  VBR gains are `silk_process_gains_FLP`'s), `silk/float/process_gains_FLP.c`,
  `silk/gain_quant.c` (`silk_gains_quant` with the double-step delta rule),
  `silk/float/residual_energy_FLP.c`, `silk/float/find_pred_coefs_FLP.c`
  (LPC_in_pre scaled by `1.0f / Gains[i]`, the unquantised shape gains;
  `minInvGain` from the exact `coding_quality`), `silk/encode_pulses.c`
  (`combine_and_check` scale-down against `silk_max_pulses_table` {8, 10,
  12, 16} at every shell level, not only the block total).
- `internal/silk/process_gains_flp32.go`: float32 ports of residual energy,
  process_gains and gains_quant; in VBR/CVBR `encodeRangeFrame` codes these
  gains and their delta symbols directly (the Go budget search now serves
  CBR only). The 48 kHz mono homebrew-NSQ fallback (2026-06 conformance
  workaround) is removed; the pulse encoder scales blocks down like
  `combine_and_check` (a block whose pair/quad/octet sums exceeded the table
  was mis-coded before).
- Result (`TestSILKEncoderInputPipelineOracle`): the first packet is
  **byte-identical to libopus** for 13 of 16 cells (8/12/16/24/48 kHz ×
  steady-voiced / speech-like-harmonic / unvoiced-noise; 48 kHz noise also
  frame 1). Frame-0 leftovers: 8k noise, 12k steady, 24k harmonic (all
  traced stages equal, ±1 byte: an entropy-coding corner). From frame 1 on,
  NLSF_Q15 (state-dependent LPC input) and the NSQ seed/pulses diverge.
- Test contracts: `TestEncoderSILKOnlyVoicedRateModeContract` now requires
  the `OPUS_SILK_RC_SNR` A/B switch to leave VBR output unchanged (it only
  steers the CBR search). Packet digests regenerated. Full opusref suite
  passes.

### 2026-09-16: SILK-only VBR packets byte-identical to libopus (`9b4d0d0`, `179e7f6`, `51d24d4`)

- `9b4d0d0` fix(encoder): a SILK-only payload is sized `(ec_tell + 7) >> 3`
  before the range coder flush (opus_encode_native drops the carry byte
  `ec_enc_done` may add) and trailing zero bytes are stripped down to two
  bytes when no redundancy follows.
- `179e7f6` fix(silk): the NSQ is seeded with `frameCounter++ & 3` like
  `silk_encode_frame_FLP`; `silk_find_LPC_FLP` runs on LPC_in_pre in int16
  scale (Burg's absolute `1e-9f` regulariser is scale-dependent, so the
  [-1,1] domain analysis was not exact); `minInvGain` in silk_float order;
  inactive (`TYPE_NO_VOICE_ACTIVITY`) frames go through the same pitch
  whitening, LPC/NLSF, gains, shaping and delayed-decision NSQ as unvoiced
  frames (only the coded type differs) instead of the Go zero-pulse shortcut.
- `51d24d4` fix(silk): the pulse rate level is chosen with
  `silk_rate_levels_BITS_Q5` / `silk_pulses_per_block_BITS_Q5` (first minimum
  wins) instead of float ICDF costs.
- Result (`TestSILKEncoderInputPipelineOracle`, 24 kbps CVBR, 12 frames,
  8/12/16/24/48 kHz mono): **all 12 packets byte-identical to libopus on all
  15 fixtures whose encoder state stays shared** (steady-voiced,
  speech-like-harmonic, unvoiced-noise); the test now fails on any packet
  difference for those fixtures. The `onset` fixture still diverges at the
  Go digital-silence shortcut (frames 0–1 silent) — the remaining policy
  item before the SILK-only VBR encoder can be called byte-exact.
- Verified `go vet ./...`, `go test -count=1 ./...`,
  `go test -count=1 -tags opusref ./...` (all pass), `go test -race`.

### 2026-09-16: In-band FEC (LBRR) and the real libopus encoder (`3559e49`, `a5517f4`, `dcd288d`, `f3a706d`)

- Reference: `silk/float/encode_frame_FLP.c` (`silk_LBRR_encode_FLP`: the
  regular indices with `GainsIndices[0] += LBRR_GainIncreases` at a run
  start, `silk_gains_dequant` from `LBRRprevLastGainIndex`, the frame seed,
  the regular shaping/Lambda and the pre-frame NSQ state),
  `silk/control_codec.c` (`silk_setup_LBRR`: 7 after a packet without
  LBRR_enabled, else `max(7 - loss*0.2, 3)`), `silk/enc_API.c`
  (`curr_nBitsUsedLBRR` counted from after the VAD/LBRR placeholder; LBRR
  frames coded with `CODE_CONDITIONALLY` when the previous frame is LBRR
  coded), `silk/encode_indices.c` (delta lag coding in [-8, 11], the copied
  `LTP_scaleIndex`), `silk/float/LTP_scale_ctrl_FLP.c` (round_loss from
  `PacketLoss_perc * nFramesPerPacket`, squared with LBRR_flag, thresholds
  `log2lin(2900|3900 - SNR_dB_Q7)`), `src/opus_encoder.c` (`decide_fec`,
  `compute_equiv_rate`, `fec_thresholds`).
- `3559e49` fix(silk): the LBRR port above replaces the Go LBRR (Lambda x16,
  seed 0, re-derived deltas, absolute-only lags, LTP scale 0). Voiced frames
  now code `LTP_scaleIndex` from the loss estimate; regular conditional
  frames code lag deltas. CBR still quantises the LBRR copy from
  `silk_process_gains_FLP`'s gains like libopus (the Go CBR search codes the
  regular frame).
- `a5517f4` fix(silk): `apply_sine_window_FLP`'s `PI / (length + 1)` is a
  float32 division; the double quotient rounds differently for the 12 kHz
  shaping slope (73), which flipped one shaping coefficient at 12 kHz /
  32 kbps (found through the new `ref-speech` oracle fixture).
- `dcd288d` fix(encoder): `LBRR_coded` per packet from `decide_fec` /
  `compute_equiv_rate` (hysteresis on the previous decision); pending LBRR
  is still emitted when the decision turns off. libopus also narrows the
  bandwidth to make FEC fit above 5 % loss — the Go SILK bandwidth follows
  the input rate, so that part is recorded as open.
- Results: `TestSILKEncoderInputPipelineOracle{,FEC,32k}` — 24 kbps, 24 kbps
  + 20 % loss, 32 kbps; 8/12/16/24/48 kHz × steady-voiced /
  speech-like-harmonic / unvoiced-noise / ref-speech: **all 12 packets
  byte-identical to libopus on all 60 shared-state cells** (onset still
  diverges at the silence shortcut). `TestCGOEncodeRefSILKByteExact` — the
  Go encoder against the real libopus 1.6.1 encoder (`cgoref`, no
  instrumentation, automatic mode/bandwidth: VOIP, CVBR, complexity 5,
  voice) with and without FEC at 16/24/32 kbps: **24 of 30 cells
  byte-identical for 14 packets**; the other 6 are libopus choosing hybrid
  at 32 kbps for 24/48 kHz input (mode policy, logged).
- Re-baselined: `TestCGOEncodeRefSILKFEC` (CBR) now requires +2 dB FEC over
  PLC (Go 18.8 vs 15.7 dB; libopus 20.5 vs 16.4 dB on the same fixture — the
  CBR gain loop is the remaining gap); the multistream FEC and fuzz-seed
  tests use 28 kbps so `decide_fec` codes LBRR. Verified `go vet ./...`,
  `go test -count=1 ./...`, `go test -count=1 -tags opusref ./...`.

### 2026-09-16: Stereo SILK packets byte-identical to libopus (`8fdbcf6`, `77a88dd`, `81e0c48`, `1bc9021`)

- Reference: `silk/stereo_LR_to_MS.c`, `silk/stereo_find_predictor.c`,
  `silk/stereo_quant_pred.c`, `silk/enc_API.c` (stereo flow: VAD/LBRR flag
  placeholder + `ec_enc_patch_initial_bits`, LR_to_MS per frame with the
  packet `TargetRate_bps` and the mid channel's previous
  `speech_activity_Q8`, mid-only flag coded only when the side VAD is 0, side
  frame skipped only when `MStargetRates_bps[1] == 0`, partial side reset +
  `CODE_INDEPENDENTLY_NO_LTP_SCALING` after a mid-only frame, LBRR mid-only
  flag = previous packet's value), `silk/float/SigProc_FLP.h`
  (`silk_sigmoid` in double), `src/opus_encoder.c`
  (`compute_silk_rate_for_hybrid`).
- `8fdbcf6` fix(silk): fixed-point `silk_stereo_LR_to_MS` port
  (`internal/silk/stereo_pred.go`), gated by a cgoref oracle of the libopus
  function itself (`TestSILKStereoLRToMSOpusRef`, 160 frames × 8/12/16 kHz ×
  10/20 ms); `entcode.Encoder.PatchInitialBits`; the stereo packet flow
  above; the SILK bitrate follows the whole packet rate (5–80 kbps, the
  40 kbps cap removed); `speech_activity_Q8` starts at 0 like
  `silk_init_encoder`.
- `77a88dd` fix(silk): `silk_sigmoid` evaluated in double (one ulp of
  coding_quality flipped at the mid/side rates); `nFramesPerPacket` set on
  both stereo channels (LTP_scaleIndex was 0 for stereo frames with FEC).
- `81e0c48` fix(encoder): hybrid SILK rate = `compute_silk_rate_for_hybrid`
  (the lifted cap had passed the whole rate to SILK in hybrid mode, which
  desynchronised `TestCGOHybridTrellisFinalRange`).
- `1bc9021` test(opusref): stereo encoder oracle (`--silk-enc ... <channels>`,
  `[SILK_ENC_STEREO]`, `[SILK_ENC_CH]`, per-channel stage split) and 18
  stereo cells in `TestCGOEncodeRefSILKByteExact`.
- Results: `TestSILKEncoderStereoOracle` — 8k/24k, 16k/32k, 8k/24k+FEC,
  16k/32k+FEC, 12k/40k+FEC: all 14 packets byte-identical.
  `TestCGOEncodeRefSILKByteExact` against the real libopus encoder: **49 of
  66 cells byte-identical (14 packets)** — every mono and stereo cell that
  libopus codes as SILK-only with the same channel count; the other 17 are
  policy: libopus picks hybrid for 24/48 kHz mono input (all rates without
  FEC, 32 kbps with FEC) and downmixes stereo to one stream at 16 kbps
  (20 kbps with FEC, then also MB at 16 kHz).
- Verified `go vet ./...`, `go test -count=1 ./...`,
  `go test -count=1 -tags opusref ./...` (all pass); hybrid/stereo perf
  digests regenerated.

### 2026-09-16: CBR SILK packets byte-identical — encode_frame_FLP quantiser loop (`46c8703`, `8798be9`, `e16f983`)

- Reference: `silk/float/encode_frame_FLP.c` (the loop: gainMult_Q8
  bracketing/interpolation, gain locks, Lambda × 1.5 + quantOffsetType 0
  after two busted passes, damage control, restore of the fitting pass,
  `useCBR`/`bits_margin`), `silk/enc_API.c` (per-frame maxBits scaling
  2/5 · 3/4 · 3/5, useCBR on the last frame, mid channel half budget),
  `src/opus_encoder.c` (`cbr_bytes = (bitrate_to_bits + 4) / 8`, SILK rate
  `bits_to_bitrate(cbr_bytes*8 - 8)`, `maxBits = (max_data_bytes - 1) * 8`,
  hybrid CBR steal-25 % rule with SILK in VBR, CVBR hybrid cap via
  `compute_silk_rate_for_hybrid`), `src/repacketizer.c` (`opus_packet_pad`:
  code 3 with padding, CBR layout when frame sizes are equal).
- `46c8703` fix(silk): `internal/silk/encode_frame_loop.go` replaces the Go
  CBR budget search whenever process_gains is available; `entcode.Encoder`
  Clone/Restore; Lambda captured once per frame (`lambda32`); `SetMaxBits`.
- `8798be9` fix(encoder): CBR packet sizing/padding and hybrid SILK maxBits.
- `e16f983` test(opusref): `--silk-enc ... <channels> <vbr>`,
  `[SILK_ENC_LOOP]`/`[SILK_ENC_LOOP_DAMAGE]`/`[SILK_ENC_LOOP_RESTORE]` dumps,
  `TestSILKEncoderInputPipelineOracleCBR` (20 cells, 12 packets, loop
  passes identical), CBR cells in `TestCGOEncodeRefSILKByteExact`.
- Results against the real libopus encoder: **CBR mono 8/12/16/24/48 kHz ×
  16/24/32 kbps and CBR stereo 8/12/16 kHz × 20–48 kbps all 14 packets
  byte-identical** (90 identical cells overall across CVBR/FEC/CBR; the
  remaining 29 are libopus' hybrid / mono-downmix policy). The CBR FEC test
  now measures the same recovery as libopus (~19.7 dB vs ~15.6 dB) and its
  +3 dB gate is back.
- Verified `go vet ./...`, `go test -count=1 ./...`,
  `go test -count=1 -tags opusref ./...` (all pass); SILK/hybrid perf
  digests regenerated (CBR packets are now cbr_bytes long).

### 2026-09-17: Stream channel decision and mono-stream coding of stereo input (`38072d0`, `8a93b3e`, `93e4b68`)

- Reference: `src/opus_encoder.c` (stream_channels from
  `stereo_music_threshold` / `stereo_voice_threshold` interpolated by
  `voice_est`, ±1000 hysteresis, the delayed stereo→mono transition via
  `silk_mode.toMono`), `silk/enc_API.c` (nChannelsAPI 2 / nChannelsInternal
  1: `RES2INT16(L + R)` halved with rounding before the mono channel's
  resampler, first-mono-frame averaging with the side resampler, mono→stereo
  re-init of the side channel with the resampler copied from the mono
  channel and the stereo predictor/norm/width state restarted, LBRR flags
  cleared on a channel transition).
- `38072d0` feat(silk): `SetStreamChannels` / `DownmixToMono` and the
  transition rules (`front_end.go`); per-channel SNR rate divides by the
  stream channel count.
- `8a93b3e` feat(encoder): `decideStreamChannels` (`encoder_fec.go`) with
  `voiceEst` (127 voice / 0 music / 115 VOIP / 48 other — libopus' tonality
  analysis, which only runs at complexity ≥ 7, is not ported), TOC and
  resampler handling in `encodeSILKOnlyPacket` / `silkInput`.
- `93e4b68` test(opusref): `TestCGOEncodeRefSILKStreamChannels` — stereo
  input at 16/20 kbps and a 32→16→32 kbps schedule at 8/16/48 kHz:
  stereo → toMono → mono → stereo, all packets byte-identical.
- `TestCGOEncodeRefSILKByteExact`: **84 of 99 cells byte-identical**
  (CVBR, CVBR+FEC, CBR × mono 8–48 kHz × 16/24/32 kbps, stereo 8/12/16 kHz
  × 16–48 kbps); the 15 remaining are libopus policy the Go encoder does not
  mirror yet: hybrid for 24/48 kHz mono input (14) and `decide_fec`'s
  bandwidth narrowing to MB at 16 kHz / 20 kbps stereo with FEC (1).
- Verified `go vet ./...`, `go test -count=1 ./...`,
  `go test -count=1 -tags opusref ./...` (all pass).

### 2026-09-17: Automatic bandwidth decision → SILK internal rate (`d683ded`, `8e60c26`)

- Reference: `src/opus_encoder.c` (`mono/stereo_voice/music_bandwidth_thresholds`,
  the FB→NB walk with hysteresis against `auto_bandwidth`, MB→WB promotion,
  the input-rate Nyquist caps, `max_bandwidth`), `silk/control_audio_bandwidth.c`
  (`fs_kHz == 0`: internal rate = min(desiredInternal, API rate)).
- `d683ded` feat(encoder): `decideAutoBandwidth` / `selectSILKInternalRate`
  (`encoder_fec.go`) choose the SILK internal rate before the first SILK
  packet after (re)initialisation and rebuild the SILK encoder + encoder
  resamplers at that rate (`rebuildSILKEncoder`). Not ported: mid-stream
  switching (`allowBandwidthSwitch`, the sLP transition filter, its
  redundancy frame) — the rate stays once frames were coded.
- `8e60c26` test(opusref): `TestCGOEncodeRefSILKAutoBandwidth` — 12/16/48 kHz
  mono voice at 6/8/9/10/12 kbps: NB below the 9 kbps threshold (SILK at
  8 kHz through the 12/16/48→8 kHz resampler), WB above; all 12 packets
  byte-identical to the real libopus encoder.
- Verified `go vet ./...`, `go test -count=1 ./...`,
  `go test -count=1 -tags opusref ./...` (all pass).

### 2026-09-17: All complexity settings byte-identical — plain silk_NSQ (`16fcef8`, `899e978`)

- `16fcef8` test(opusref): the encoder oracle takes a complexity argument
  (mono comparison also at complexity 8); `TestCGOEncodeRefSILKComplexity`
  sweeps complexity 0–10 at 8/12/16 kHz against the real libopus encoder.
  Result: 0 and 2–10 identical, 1 diverged from frame 4 in the pulses only.
- Cause: `silk_NSQ_wrapper_FLP` runs the plain `silk_NSQ` when
  `nStatesDelayedDecision == 1 && warping_Q16 == 0` (complexity 0 and 1);
  the Go single-state delayed-decision quantiser differs in Q-format and
  rounding (Q14 saturating combination vs Q12 with `RSHIFT_ROUND`, Q20 vs
  Q10 rate-distortion comparison), which happened to coincide at
  complexity 0 (12th-order shaping, 3 ms look-ahead) but not at 1.
- `899e978` fix(silk): `internal/silk/nsq_plain.go` ports `silk_NSQ`,
  `silk_nsq_scale_states`, `silk_NSQ_noise_shape_feedback_loop_c` and
  `silk_noise_shape_quantizer`; the wrapper dispatch follows libopus.
  **Every complexity setting is now byte-identical** (36 cells × 12 packets).
  At complexity ≥ 7 libopus also runs the tonality analysis for ≥ 16 kHz
  input; with a voice signal hint it only feeds `detected_bandwidth`, which
  cannot lower a 16 kHz input below WB, so those cells match as well.
- Verified `go vet ./...`, `go test -count=1 ./...`,
  `go test -count=1 -tags opusref ./...` (all pass).

### 2026-09-17: CELT-only baseline measurement (`2cca199`)

- `2cca199` fix(celt): CBR CELT payload = `cbr_bytes - 1` (packets were one
  byte long; the CBR size tests follow the libopus convention now).
- First CELT-only comparison against the real libopus encoder
  (RESTRICTED_LOWDELAY, 48 kHz mono, CBR, fullband forced, harmonic
  fixture): packet sizes and TOC match; **no packet is byte-identical
  yet**. At complexity 0 several packets share long prefixes (62 bytes of
  160 at 64 kbps, 12 bytes on another) while others diverge at byte 1 —
  i.e. the frame-level decisions coded first (transient / tf / silence,
  first-frame state) differ on some frames and the band coding is close on
  the others. At complexity 5 and 10 every packet diverges at byte 1 (the
  pitch pre/postfilter decision and gains come first).
- Bandwidth policy note: with the automatic bandwidth the Go CELT path
  narrowed this fixture to SWB (`narrowAutoBandwidth` analyses the signal)
  while libopus stays FB below complexity 7 (rate-based decision only; the
  tonality analysis narrows only at complexity ≥ 7). Whether to keep the Go
  analysis-based narrowing is a policy decision (quality feature vs libopus
  fidelity) — raised with the user.
- Next for CELT exactness: an instrumented `celt_encode_with_ec` oracle
  (transient_analysis, tf_analysis, band energies, alloc, PVQ) following
  the SILK oracle pattern (`scripts/oracle/build_encoder.ps1`).

## Phase 5 (next): CELT encoder exactness — plan (written 2026-09-17 02:40)

State of the Go CELT encoder relevant to exactness: float64 throughout
(libopus float build is float32 `opus_val32`/`celt_sig`, KISS FFT in
float32), no pitch prefilter (`pf_on` is always 0; libopus runs
`run_prefilter` from complexity 5), own transient/tf/alloc heuristics that
follow libopus structurally but not arithmetically.

Ordered steps, each gated by an instrumented `celt_encode_with_ec` oracle
(extend `scripts/oracle/build_encoder.ps1` to patch `celt/celt_encoder.c`,
`celt/bands.c`, `celt/quant_bands.c`, `celt/vq.c`, dumping per frame; Go
side exposes a `celt.FrameTrace` like `silk.FrameTrace`):

1. Input path: `celt_preemphasis` (float32, `coef[0]` 0.85, upsample
   scaling), `compute_mdcts` (`clt_mdct_forward_c` with the float32 KISS
   FFT, window `mode->window`), `compute_band_energies` / `amp2Log2` —
   compare `bandLogE` bit for bit. Fixture: RESTRICTED_LOWDELAY 48 kHz mono
   (no delay buffer / HP interplay), then VOIP.
2. `transient_analysis` (float32, `tf_estimate`, `tf_chan`, weak
   transients, tone detection in 1.6.1), `patch_transient_decision`,
   `secondMdct` at complexity ≥ 8.
3. `quant_coarse_energy` (two-pass with `intra` decision, `budget`,
   Laplace coding) — first integer checkpoint: identical coarse energy
   symbols and `tell`.
4. `tf_analysis` / `tf_encode`, `alloc_trim_analysis`, `dynalloc_analysis`
   (`spread_decision`, `stereo_analysis`), `clt_compute_allocation` — the
   allocation is integer once its float inputs match.
5. `quant_all_bands`: `alg_quant` (float32 PVQ search, `op_pvq_search_c`),
   `quant_band` recursion, `haar1`, `intensity`/`dual_stereo`,
   `anti_collapse`, `quant_fine_energy`, `quant_energy_finalise`.
6. `run_prefilter` (pitch downsample, `pitch_search`, `remove_doubling`,
   comb filter, tapset decision, gain quantisation) for complexity ≥ 5.
7. VBR: `compute_vbr`, the `st->vbr_reservoir` / `vbr_drift` / `nbCompressedBytes`
   loop and `ec_enc_shrink`; CBR done (`2cca199`).
8. Stereo (`compute_stereo_width` also drives the Opus-layer mode/threshold
   interpolation), intensity/dual-stereo decisions, then hybrid (SILK exact
   + CELT exact + `compute_silk_rate_for_hybrid` already done).

Blocked-on-CELT items from the SILK side: hybrid mode policy for 24/48 kHz
input, SILK internal-rate switching (needs the CELT redundancy frame),
`decide_fec` bandwidth narrowing in hybrid.

### 2026-09-17: Phase 5 step 1 started — CELT oracle + exact pre-emphasis (`c34af75`, `63e5128`)

- `63e5128` test(opusref): `--celt-enc` oracle mode and the
  `celt_encode_with_ec` instrumentation (`[CELT_ENC_FRAME]`, `_IN`, `_FREQ`,
  `_BANDE`, `_BANDLOGE`, `_COARSE`, `_TF_RES`); `celt.FrameTrace` on the Go
  encoder; `TestCELTEncoderOracle` reports per-stage float32 statistics and
  decisions (framing gated only).
- `c34af75` fix(celt): `celt_preemphasis` in float32 with
  `preemph[0] = 0.8500061035f` → the CELT analysis input (`in`) is
  **bit-identical** to libopus on every frame (complexity 0, where no
  prefilter runs).
- Baseline after the fix (complexity 0, 64 kbps CBR, ref-speech): MDCT
  coefficients differ at float32 rounding level (maxAbs ≈ 1e-4 on the int16
  scale, 1–5 % of coefficients bit-equal) — the float64 MDCT vs libopus'
  float32 `clt_mdct_forward` + KISS FFT; band energies follow (maxAbs ≈
  1e-4). Coarse-energy `tell` differs (Go 51 vs C 53 on frame 0): the intra
  decision (Go: first frame only; libopus: two-pass inside
  `quant_coarse_energy` at complexity ≥ 4 plus `delayedIntra`) and Laplace
  coding must be checked once the energies match. At complexity 5 libopus
  enables the pitch prefilter (`pf_on`, `tell` 17) which the Go encoder
  lacks entirely.
- Next: float32-faithful `clt_mdct_forward` (window, pre-rotation, KISS FFT
  `kf_bfly*` order, post-rotation) with the oracle's `[CELT_ENC_FREQ]`, then
  `compute_band_energies`/`amp2Log2` (`celt_sqrt`/`celt_log2` float32
  approximations), then `quant_coarse_energy`.

### 2026-09-18: Phase 5 — CELT-only encoder byte-identical (mono + stereo, complexity 0–10, CBR/CVBR)

Commits `249dc5f` … `58a392d` (2026-09-17 02:58 – 2026-09-18 03:37 JST). Every
stage of `celt_encode_with_ec` is now a float32-faithful port checked
against the instrumented plain-C libopus 1.6.1 build (`--celt-enc` oracle,
`TestCELTEncoderOracle`, **84 cells × 20 frames byte-identical, all gated**):

- `249dc5f` `clt_mdct_forward` (float32 fold, KISS twiddles, `1/N4` pre-scale,
  float KISS FFT), `compute_band_energies` (`1e-27f` + sequential float32
  inner product, `celt_sqrt`), `amp2Log2` with the non-FLOAT_APPROX
  `celt_log2`, `normalise_bands`.
- `0b68b09` `quant_coarse_energy` (`delayedIntra` follower, two-pass intra
  search at complexity ≥ 4, `max_decay`, `energyError` bias, no −28 clamp),
  `quant_fine_energy`, `quant_energy_finalise`.
- `c0c15cd` `interp_bits2pulses` skip decision (`depth_threshold` 7/9/0,
  `prev = lastCodedBands`, `signalBandwidth`), float `max_decay =
  min(16, 0.125·nbAvailableBytes)`; oracle matrix with per-stage dumps
  (spread, dynalloc, trim, allocation, pulses, fine bits, final tell).
- `20817ab` `tone_detect`/`tone_lpc`, `transient_analysis` (tone gate, weak
  transients), `dynalloc_analysis` (spread_weight masking model, tone
  compensation, 2/3 cap, `effectiveBytes` gate), `spreading_decision`
  (weights, hf_average/tapset), `stereo_itheta` + `celt_atan2p_norm`; the
  libopus spread rules (hybrid / short blocks / complexity < 3 / small budget).
- `3d74fee` VBR: range coder starts at the 1275-byte cap, constrained bound
  shrink up front, budget guards on the pre-target `total_bits`, then
  `compute_vbr` (dynalloc boost, transient boost, depth floor, 0.67 damping,
  temporal VBR `spec_avg`) + reservoir/drift/offset and the final shrink;
  `op_pvq_search_c` in float32. The Go heuristic VBR is gone.
- `42ed426` `run_prefilter`: `pitch_downsample` (autocorrelation, LPC, FIR),
  `pitch_search`, `remove_doubling`, the tone shortcut, gain thresholds and
  continuity rules, float `comb_filter` crossfade, before/after energy check,
  `prefilter_mem`/`in_mem`, PCM silence test (`overlap_max`, `lsb_depth`),
  post-filter parameter coding.
- `5b4880e` `analysis.c` + `mlp.c` (tonality analysis with the 1.6.1 MLP
  weights, `tonality_get_info`) run by the Opus layer at complexity ≥ 7 and
  consumed by CELT (prefilter gain, dynalloc leak boost, `compute_vbr`
  activity/tonality/pitch_change, signal bandwidth, float32
  `alloc_trim_analysis`); `lastCodedBands` ±1 hysteresis; input LSB depth
  16 for int16 API calls.
- `180ba40` stereo decisions (dual stereo, intensity with the 1.6.1
  hysteresis table) between dynalloc and the trim; trim guard
  `tell_frac + 6 bits ≤ total − boost`; oracle fixture snapped to the int16
  grid on both sides (C/Go `sin()` last-ulp differences).
- `b1bfa30` `theta_rdo` (complexity ≥ 8: code each joint band twice, keep the
  higher weighted correlation, encoder/ctx save/restore), float32
  `intensity_stereo`/`stereo_split`, fold seed = final range value
  (`st->rng = enc->rng`), Opus-layer `stereo_fade` (equiv_rate < 32 kb/s);
  per-band `[CELT_ENC_QAB]` tell dump.
- `58a392d` `TestCGOEncodeRefCELTByteExact` reports identity against the
  *linked* libopus: the msys2 build uses the SSE/AVX RTCD kernels
  (`celt_inner_prod`, `xcorr_kernel`, `comb_filter_const`, …) whose float
  summation order differs, so packets diverge in band energies / PVQ while
  sizes match (2/36 cells identical). The plain-C build is the reference the
  oracle reproduces; there is no libopus API to disable RTCD at runtime.

Remaining for the CELT side: hybrid (SILK+CELT in one range coder, `silk_info`,
start band 17, hybrid VBR/CBR sizing, SILK stereo width for the fade,
transition redundancy), CELT at 8–24 kHz input (resampler path), the
digital-silence shortcut (policy decision pending), the Opus-layer
mode/bandwidth policy (`mode_thresholds`, `voice_est` with the analysis).

### 2026-09-18: hybrid byte-identical, libopus mode policy behind a switch (`00b2e03`, `d203140`)

- `00b2e03` feat(encoder): hybrid packets follow `opus_encode_native` /
  `celt_encode_with_ec`: `max_data_bytes`/`cbr_bytes` sizing with the rounded
  CBR bitrate, `bits_target` − TOC, the SILK share from
  `compute_silk_rate_for_hybrid` and its `maxBits` rules, the CELT share as
  the hybrid CELT bitrate (unconstrained VBR) or `OPUS_BITRATE_MAX` in CBR,
  `CELT_SET_SILK_INFO` (signal type / offset of the last SILK frame) driving
  the weak-transient rule, hybrid `tf_res` and the hybrid VBR target
  (tonal/noisy offsets, transient boost, 37-bit redundancy floor), `HB_gain`
  fade, SILK's smoothed stereo width for the CELT stereo fade, and no
  80 kb/s cap on the SILK rate (libopus has none). `--hybrid-enc` oracle +
  `TestHybridEncoderOracle`: 48 kHz mono 48/64/96 kbps and stereo
  64/96/160 kbps × CBR/CVBR × complexity 0/5/10 — **36/36 cells
  byte-identical, gated**.
- `d203140` feat(encoder): `decideLibopusMode` ports the automatic mode /
  channel / bandwidth policy (`compute_equiv_rate`, `voice_est` incl. the
  analysis' `voice_ratio`, `compute_stereo_width`, `mode_thresholds`
  interpolation, VOIP bias, ±4000 hysteresis, FEC/DTX/tiny-packet
  overrides, bandwidth thresholds + caps + `detected_bandwidth`, SILK↔hybrid
  by bandwidth), decided before the high-pass conditioning. **Behind
  `SetLibopusModePolicy(true)`** — the default stays the Go policy until
  the user decides, because flipping it changes the mode of ~15 existing
  encoder tests (e.g. libopus codes 48 kHz voice at 16–64 kbps as *hybrid
  FB*, CELT-only from ~76 kbps equiv; the Go policy keeps SILK-only up to
  40 kbps). `--auto-enc` oracle + `TestAutoModeOracle` (VOIP/AUDIO ×
  voice/music/auto × mono/stereo × 12–128 kbps, 12 frames): 49/60 cells
  byte-identical with the libopus policy (gated); the rest need (a) a stereo
  input coded as a *mono* hybrid/CELT stream (libopus `stream_channels=1`
  with `CC=2, C=1` MDCT averaging; 12 kbps stereo without a voice hint) and
  (b) a VOIP CELT-only prefilter pitch near-tie at frame 11 (Go 264 vs C 263
  after 10 identical frames; the `hp_cutoff`-conditioned input, suspect the
  pitch search / `remove_doubling` float order).

Remaining (encoder): the two items above, SILK↔CELT transition redundancy
(`to_celt` deferral for SILK-only→CELT, `prev_channels` bookkeeping on every
path), CELT/hybrid at 8–24 kHz input, the digital-silence shortcut (policy),
mid-stream SILK rate switching (`allowBandwidthSwitch`), `decide_fec`
narrowing in hybrid. The linked SIMD libopus stays a non-goal.

### 2026-09-18 (later): automatic mode 60/60 (`8e423ac`, `538956f`)

- `8e423ac` fix(celt): the VOIP CELT-only "pitch near-tie" was not a float
  order problem: Go's `combFilterMaxPeriod` was 1022 where libopus has
  `COMBFILTER_MAXPERIOD 1024`, so the prefilter history and the
  downsampled pitch buffer were shifted by one sample and the pitch search
  range was two lags short. Found by dumping `pitch_buf` on *exact* frames
  (`[CELT_ENC_PITCH]`/`[CELT_ENC_PITCH2]`/`[SILK_CELT_ENC_PITCH_BUF]` in
  the oracle, `FrameTrace.PitchSearch/PitchRaw/PitchGain/PitchBuf` in Go):
  it already differed everywhere while the decisions happened to agree.
  Lesson: compare intermediates on matching frames too, not only at the
  first packet difference.
- `538956f` feat(encoder): a stereo input coded as a mono stream.
  `celt.Encoder.SetStreamChannels` (CELT_SET_CHANNELS): the analysis
  buffers, tone/transient detectors and the prefilter run on CC input
  channels, `compute_mdcts` averages the two MDCTs (CC=2, C=1) and
  everything from the band energies on uses C — the silence peak window
  (`C*(N-overlap)` interleaved samples, a libopus quirk), `equiv_rate`,
  `tf_chan=0`, the energy bias/error, and `oldBandE` is mirrored to the
  second channel at the end of the frame. Also `energyError` is cleared
  every frame and `consec_transient` advances when the transient bit could
  not be coded (`transient_got_disabled`). Opus layer: the TOC of CELT-only
  and hybrid packets carries `stream_channels`, a mono-stream hybrid
  packet downmixes its SILK part and sizes the SILK share / redundancy from
  the stream channels, the stereo width comes from the mono equivalent
  rate, and `prev_channels` is recorded after every packet. Under the Go
  policy hybrid/CELT packets always carry the input channel count.
  **`TestAutoModeOracle`: 60/60 cells byte-identical, all gated.**

Remaining (encoder): SILK↔CELT transition redundancy (`to_celt` deferral,
SILK re-init + prefill on CELT→SILK), CELT/hybrid at 8–24 kHz input, the
digital-silence shortcut (policy), mid-stream SILK rate switching,
`decide_fec` narrowing in hybrid, the default policy switch (user
decision). The linked SIMD libopus stays a non-goal.
