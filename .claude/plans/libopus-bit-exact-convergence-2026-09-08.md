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
