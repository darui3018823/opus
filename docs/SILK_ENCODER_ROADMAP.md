# SILK Encoder Roadmap

Last updated: 2026-06-16

This roadmap starts from the implementation snapshot in
`docs/CURRENT_IMPLEMENTATION.md`. If this document and the current snapshot
disagree about what is already implemented, treat the current snapshot as the
source of truth.

## Current Baseline

The public encoder has two encode paths:

- General audio still uses the CELT encoder.
- A narrow SILK-only path is available for low-bitrate speech when the encoder
  is configured for `ApplicationVOIP` or `SignalVoice` and the target bitrate is
  at most 40 kbps. Native 8/12/16 kHz input maps to SILK NB/MB/WB, while 24/48
  kHz input is downsampled to WB SILK. Mono and stereo SILK-only packets are
  supported.

The internal SILK encoder can write decoder-compatible mono and stereo 10 ms /
20 ms range streams, pack multiple SILK frames into one shared stream, encode
structured pulses, make simple voiced pitch/LTP decisions, select input-adaptive
NLSF indices, and run a first closed-loop NSQ-style pulse search with simple
noise-shaping feedback. Stereo uses conservative mid/side coding with zero
stereo predictors. It is intentionally not a libopus-equivalent SILK encoder
yet.

## Phase Transition: Foundation Done, Quality Next (2026-06-16)

Slices 1-13 below built a **structurally complete** SILK/hybrid encode skeleton:
decoder-compatible mono/stereo/downsampled/hybrid streams that both this decoder
and libopus 1.6.1 accept. Measured self-decode SNR is still low (voiced
~9-10 dB, speech-harmonic ~4.4 dB) and rate control leaks badly on hard signals
(unvoiced noise reached ~536 B / 20 ms ≈ 200+ kbps despite a 40 kbps target).

From here the goal is **quality**, not new structure. The remaining work is to
replace each simplified analysis stage with a faithful port of the libopus SILK
encoder chain, climbing from the bottom of the chain upward.

## Quality Goal (Restated)

- Target: **perceptual / SNR parity with the libopus SILK encoder at a matched
  bitrate** — NOT bit-exact output (the RFC only specifies the decoder, so
  bit-exact encode is impossible and not a goal).
- Keep every emitted packet a standard Opus packet that this decoder and libopus
  both decode (never regress the Slice 7/13 cross-checks).
- Preserve the CELT encoder path and the 12/12 official-vector decoder coverage.
- Every quality slice must move a measured number on the scoreboard below, or
  prove non-regression while improving robustness.

## Non-goals For The Quality Phase

- Bit-exact matching with libopus encoder output.
- LBRR/FEC encoding (deferred to after Q7).
- Ogg Opus container support.
- Multistream or surround.

## The Scoreboard (Q0) — North Star

Progress toward libopus is measured by an **A/B harness**, not by self-decode SNR
alone:

```
fixture ─┬→ our encoder   → our decoder → SNR_a, bytes_a
         └→ libopus encoder → our decoder → SNR_b, bytes_b

score per fixture = gap_SNR = (SNR_b - SNR_a)   [dB we are behind libopus]
                    ratio_bytes = bytes_a / bytes_b   [our bit overspend]
```

Every quality slice (Q1+) states an **exit metric in terms of shrinking
`gap_SNR` and/or `ratio_bytes`** on named fixtures, and must keep the existing
self-decode quality-baseline tests (Slice 8) green or improved. The harness runs
under the `opusref` build tag (needs gcc + libopus). See
`[[feedback_cgo_powershell]]`: CGO/`opusref` tests run from the PowerShell tool,
not the Bash tool.

## Operating Model (Director / Implementer Split)

- **Director (Claude):** writes each slice spec (the exact libopus function being
  ported, the encoder symbol order, the exit metric, the required test), reviews
  the diff, runs the scoreboard + `opusref` cross-check, and decides commit-go.
- **Implementer (Codex):** implements one slice at a time from the written spec.
- One slice per branch/commit; bisectable; never merge a slice that regresses a
  cross-check or the official vectors.

## Quality Phases

The order follows the libopus SILK encode chain bottom-up: build the analysis
stages, then the quantizer that consumes them, then the control loop around it,
then broaden. Q3 (shaping) and Q4 (NSQ) are tightly coupled and detailed/tuned
together.

### Q0 — libopus A/B scoreboard harness

Status: Complete (2026-06-16)

- Goal: the A/B harness above, reusing the existing `cgoref` libopus wrapper for
  the libopus-**encode** side (Slice 7/13 only used libopus decode).
- libopus ref: `opus_encoder_create` / `opus_encode_float` in `internal/cgoref`
  (extended the `opusref` wrapper to expose encode).
- Implemented: `cgoref.Encoder` (`NewEncoder`/`SetBitrate`/`SetComplexity`/
  `SetVoiceMode`/`Encode`/`Close`) + `!opusref` stub; `opus_silk_ab_test.go`
  (`TestOpusSILKABAgainstLibopusEncoder`) over the Slice 8 fixtures, mono
  8/12/16 kHz, 24 kbps, complexity 5, VOIP/voice. Run from PowerShell:
  `go test -tags opusref -run TestOpusSILKABAgainstLibopusEncoder .`

#### Q0.5 — bitrate-matched scoreboard column (Complete 2026-06-16)

The first `gap_SNR` lies whenever the two encoders spend different bytes (on
noise we appeared to "win" by 4 dB only by spending 8.7× the bytes). Q0.5 adds a
third reference: re-encode the same input with libopus at **our effective
bitrate** (`matchedBitrate = bytesA*8*rate / (frames*frameSize)`), giving
`gap_SNR_matched = SNR(libopus@our-bitrate) − SNR(ours)` = the honest
equal-bits quality gap, plus `ratio_bytes_matched` (≈1.0 confirms the match).

#### Q1 Baseline (libopus 1.6.1, 24 kbps target = 720 B / 12×20 ms frames)

Effective bitrate = `bytes / 30` kbps. libopus sits at/below 24 kbps everywhere;
our encoder ignores the budget in both directions. `gap_SNR_matched` is the
scoreboard going forward.

| fixture | our kbps | libopus kbps | gap_SNR (unfair) | **gap_SNR_matched** |
|---|---|---|---|---|
| silence | ~24.4 (pads to full) | ~2.7 | 0.0 | 0.0 |
| unvoiced-noise | 110–215 | ~25 | −4.0…+2.2¹ | **+2.3…+3.2** |
| steady-voiced | ~30 | ~13 | 3.6–3.9 | **3.7–3.9** |
| speech-harmonic | ~40 | ~15 | 6.0–6.5 | **6.1–6.6** |
| onset | ~33 | ~10 | 1.5–2.3 | **1.6–2.3** |

¹ The "win" on noise was pure byte-overspend illusion; at equal bits we trail by
2.3–3.2 dB. Q0.5 made this visible.

**Honest reading:** at equal bitrate we trail libopus by **2.3–6.6 dB on every
non-silent fixture**, worst on speech-harmonic — a genuine analysis-quality
deficit (LPC/pitch/LTP/NSQ), independent of the separate rate-control problem.

**Two findings the scoreboard surfaced immediately:**

1. **Rate control is non-functional in both directions** — easy signals pad to
   full budget (silence 24 vs 2.7 kbps), hard signals explode (noise up to
   215 vs 25 kbps). libopus uses VBR to move up and down intelligently.
2. **Voiced/speech quality trails 2.3–6.6 dB at equal bitrate** — worst on
   speech-harmonic.

**Director note on ordering:** with Q0.5 the scoreboard is now honest at any
byte mismatch, so Q1/Q2 (LPC, pitch/LTP) stay next as planned. Q5 (gain/rate
control) is co-critical rather than late — revisit promoting it after Q2 if the
byte explosion still obscures listening/robustness even though `gap_SNR_matched`
itself is now fair.

### Q1 — LPC / NLSF analysis fidelity

- Goal: real autocorrelation + Burg LPC, windowing, LPC→LSF→NLSF conversion,
  NLSF rate-distortion quantization with the actual codebook weights, and LSF
  interpolation between subframes. Replaces `bestNLSFStage1` / `refineNLSFResidual`.
- libopus ref: `silk_find_LPC_FLP`, `silk_A2NLSF`, `silk_NLSF_encode`,
  `silk_NLSF_VQ_weights_laroia`, `silk_interpolate`.
- Exit: residual energy (`lpcResidualEnergy`) drops on voiced fixtures;
  `gap_SNR` shrinks on steady-voiced and speech-harmonic.
- Risk: medium; NLSF quantization grammar must stay decoder-compatible.

### Q2 — Pitch & LTP

- Goal: full multi-stage pitch search (correlation, contour, voiced threshold,
  VAD-gated) and real per-subframe 5-tap LTP with codebook quantization + LTP
  scaling. Replaces `analyzePitch` / flat contour / `selectLTPGain`.
- libopus ref: `silk_find_pitch_lags_FLP`, `silk_pitch_analysis_core_FLP`,
  `silk_find_LTP_FLP`, `silk_quant_LTP_gains`, `silk_VAD_GetSA_Q8`.
- Exit: large `gap_SNR` shrink on voiced fixtures; pitch continuity stable
  (`pitchMaxDelta` bounded); voiced packet bytes move toward `ratio_bytes`→1.
- Risk: medium-high; pitch lag/contour symbol order is intricate.

### Q3 — Noise-shaping analysis + prefilter

- Goal: compute the shaping that makes SILK sound like SILK — AR/MA shaping
  coefficients, spectral tilt, HF/LF shaping, harmonic shaping gain, per-subframe
  input gains, and the Lambda rate/distortion weight. Replaces the lone
  output-error feedback term.
- libopus ref: `silk_noise_shape_analysis_FLP`, `silk_prefilter_FLP`.
- Exit: in-band SNR up; sets up Q4 (tested jointly — shaping is only fully
  observable through the NSQ that consumes it).
- Risk: high; coefficients feed Q4 directly.

### Q4 — Real NSQ (delayed-decision)

- Goal: replace `simpleNSQ` / `closedLoopNSQ` with a faithful delayed-decision
  noise-shaping quantizer using the Q3 coefficients and Lagrangian
  rate/distortion over multiple candidate states. The centerpiece quality lever.
- libopus ref: `silk_NSQ_del_dec_FLP` (and `silk_NSQ_FLP` for the
  non-delayed/low-complexity path).
- Exit: the single largest `gap_SNR` reduction across all voiced/speech
  fixtures; `ratio_bytes` improves at fixed quality.
- Risk: high; largest and most coupled change. Likely split into sub-slices
  (single-state shaped NSQ first, then delayed decision).

### Q5 — Gain processing & rate-control loop

- Goal: proper gain quantization plus the feedback loop that adjusts
  gains/Lambda and re-quantizes to hit the target bit budget. **Fixes the
  unvoiced-noise byte blowup.** Replaces the energy→index heuristic.
- libopus ref: `silk_process_gains_FLP`, `silk_gains_quant`, and the
  `silk_encode_frame` bit-feedback loop.
- Exit: `ratio_bytes` → ~1 on ALL fixtures including unvoiced noise; packet size
  tracks target bitrate; CBR/VBR sizing honest.
- Risk: medium; mostly a control loop around existing stages.

### Q6 — Stereo prediction

- Goal: replace the conservative zero stereo predictors with real mid/side
  prediction weights and the side-channel / only-middle decision driven by
  prediction gain.
- libopus ref: `silk_stereo_LR_to_MS`, `silk_stereo_encode_pred`,
  `silk_stereo_find_predictor`.
- Exit: stereo `gap_SNR` shrinks; stereo bytes drop vs dual-mono baseline.
- Risk: medium; must preserve decoder stereo state transitions.

### Q7 — Finishing: complexity scaling, hybrid redundancy, cleanup

- Goal: make `SetComplexity` actually scale search effort (NSQ states, pitch
  search depth), enable hybrid redundancy where it helps, polish mode
  transitions, and delete the dead `encodeLegacyAnalysisFrame` / `encodeFrame`
  reference paths once Q1-Q5 fully supersede them.
- Exit: complexity 0..10 trades CPU for `gap_SNR` monotonically; cross-checks and
  official vectors stay green; dead code removed.
- Risk: low-medium; mostly consolidation.

## Suggested Quality Ordering

1. **Q0** scoreboard (prerequisite — nothing is "proven better" without it).
2. **Q1** LPC/NLSF (foundation of the residual everything else shapes).
3. **Q2** pitch/LTP (biggest single voiced-quality lever after LPC).
4. **Q3 + Q4** shaping + real NSQ (the core, tuned together).
5. **Q5** gain/rate control (fixes the bit blowup, makes bitrate honest).
6. **Q6** stereo prediction.
7. **Q7** complexity scaling, redundancy, cleanup.

Rationale: you cannot honestly tune a quantizer (Q4) before its inputs (Q1-Q3)
are faithful, and you cannot claim a quality win at all without the Q0
scoreboard. Q5 is somewhat independent and high-value/low-risk, but is placed
after the core so the bit budget is being spent on a good signal.

---

## Appendix: Foundation Slices 1-13 (Complete)

Slices 1-6 are recorded in git history (`feat(silk): ...` commits
`1e7378e`..`d64fb28`). Slices 7-13 below are kept verbatim as the record of the
structural foundation the quality phase builds on.

## Slice 7: libopus Decoder Cross-check For SILK Encode

Status: Complete (2026-06-15)

Implemented:

- Added `TestCGOEncodeRefSILKOnly` under the `opusref` build tag.
- Covered mono 8/12/16 kHz, `ApplicationVOIP`, explicit `SignalVoice`, and
  20/40/60 ms packet durations.
- Verified SILK-only TOC configs, count codes, decoded duration, output length,
  bounded peak/RMS behavior, and coarse aligned decoder-vs-libopus similarity.
- Fixed public SILK-only packetization so 40/60 ms packets use SILK duration
  configs and longer supported packets use standard multiple Opus frame streams
  instead of a non-standard shared stream across count-code frames.

Purpose: prove that the new SILK-only packets are not accepted only by the
project's own decoder.

Scope:

- Add `opusref` tests for top-level SILK-only encode packets.
- Cover mono 8/12/16 kHz.
- Cover `ApplicationVOIP` and/or explicit `SignalVoice`.
- Cover at least 20 ms, 40 ms, and 60 ms packets.
- Verify the packet TOC selects SILK-only configs, not CELT-only configs.
- Verify decoded duration and output length.
- Compare this decoder and libopus decoder output at a coarse level: no decode
  error, no clipping explosion, broadly similar energy, and acceptable aligned
  SNR/RMSE for the current simple encoder.

Out of scope:

- Quality tuning.
- Stereo SILK.
- 24/48 kHz downsampling into SILK.
- Hybrid packets.

Exit criteria:

- `go test ./...` passes.
- `go test -tags opusref` passes for the new SILK encode cross-checks when
  libopus is available.

## Slice 8: SILK Quality Baseline And Regression Metrics

Status: Complete (2026-06-15)

Implemented:

- Added deterministic synthetic mono fixtures for silence, unvoiced/noise-like
  input, steady voiced tones, speech-like harmonics, and onset frames.
- Added `TestSILKInternalQualityBaseline` for the internal SILK encoder and
  `TestEncoderSILKOnlyQualityBaseline` for the public SILK-only path.
- Logged packet size, decoded RMS/peak/clipping count, aligned SNR/RMSE,
  delay/scale, and steady-pitch continuity metrics.
- Added loose regression guards for silence output, packet duration, dead
  decoded output, energy runaway, severe aligned-SNR drops, and steady-pitch
  continuity without treating the current simple NSQ as final quality.

Purpose: make future NSQ and analysis changes measurable.

Scope:

- Add synthetic mono fixtures for silence, unvoiced/noise-like input, steady
  voiced tones, speech-like harmonic signals, and onset frames.
- Record round-trip metrics for internal SILK and top-level SILK-only encode:
  packet size, decoded energy, peak, clipping count, aligned SNR/RMSE, and pitch
  continuity where useful.
- Use thresholds that catch clear regressions without pretending the simple NSQ
  is final quality.
- Keep metrics deterministic and cheap enough for normal `go test ./...`.

Out of scope:

- Large external corpora.
- Subjective quality scoring.
- libopus encoder comparison.

Exit criteria:

- Tests fail on obvious silence regression, energy explosion, packet duration
  errors, or severe quality drops.
- Metrics provide a stable baseline for Slice 9.

## Slice 9: NSQ And Noise-shaping Improvement

Status: Complete (2026-06-15)

Implemented:

- Added encoder-side LPC/LTP synthesis state so the pulse search can mirror the
  decoder's synthesis loop across frames.
- Replaced the public encode path's residual-only pulse quantization with a
  closed-loop NSQ-style search over decoder-visible output error.
- Mirrored the decoder's gain adjustment, seed sign flip, quantization offset,
  LPC prediction, LTP prediction, and re-whitening behavior used by the current
  mono encoder parameters.
- Added a simple output-error feedback term as the first noise-shaping loop.
- Kept the previous residual-only helper covered as a reference and added a
  voiced synthesis comparison test proving the closed-loop path improves over
  residual-only pulse choice for the deterministic voiced fixture.

Purpose: move from simple residual quantization toward useful SILK speech
quality.

Scope:

- Improve gain-aware residual pulse quantization.
- Refine quantization offset and seed-sign handling against decoder synthesis.
- Improve voiced residual handling after LTP prediction.
- Add a first noise-shaping loop or an incremental port of libopus NSQ behavior.
- Use Slice 8 metrics to prove improvement or at least non-regression.

Out of scope:

- Full delayed-decision NSQ in one step, unless the implementation naturally
  becomes simpler that way.
- Stereo.
- Hybrid.

Exit criteria:

- `go test ./...` passes.
- Slice 8 quality metrics improve for at least voiced speech-like fixtures or
  show no regression while improving robustness.
- libopus decode cross-checks from Slice 7 remain green.

## Slice 10: Mode And Rate Selection Polish

Status: Complete (2026-06-15)

Implemented:

- Hardened the top-level SILK-only decision boundary: mono native 8/12/16 kHz
  speech, bitrate <= 40 kbps, non-restricted-low-delay, and no forced/max
  bandwidth below the native SILK bandwidth.
- Made explicit `SignalMusic` keep even a VOIP encoder on CELT; `SignalVoice`
  remains the explicit opt-in for non-VOIP applications, and `SignalAuto`
  follows the application default.
- Added packet-TOC tests for the 40 kbps boundary, application/signal
  interaction, stereo and 48 kHz CELT fallbacks, forced/max bandwidth
  interaction, and VBR/DTX/padding interaction.
- Updated public docs to describe the limited SILK-only speech path without
  requiring readers to inspect the encoder code.

Purpose: make public SILK selection predictable and documented.

Scope:

- Harden the decision boundary between CELT and SILK.
- Verify interactions among `Application`, `SignalType`, bitrate, forced
  bandwidth, maximum bandwidth, DTX, VBR, and packet duration.
- Keep music/general audio on CELT unless explicitly and safely routed.
- Add tests for bitrate boundaries around the 40 kbps SILK limit.
- Document what "limited SILK-only speech path" means for public users.

Out of scope:

- New codec modes.
- Quality changes.

Exit criteria:

- Mode selection has explicit tests for the supported and unsupported cases.
- Documentation no longer requires reading code to know when SILK is selected.

## Slice 11: 24/48 kHz Voice Input To SILK Downsampling

Status: Complete (2026-06-15)

Implemented:

- The public encoder now creates a 16 kHz SILK encoder for 24/48 kHz input and
  resamples low-bitrate voice input into that WB SILK layer before encoding.
- 24/48 kHz VOIP or explicit `SignalVoice` packets at <=40 kbps emit SILK-only
  WB TOC configs and decode back to the requested output sample rate.
- Explicit forced bandwidths that cannot be represented by the WB SILK layer
  keep the encoder on CELT; max-bandwidth caps below WB also keep CELT.
- Added top-level round-trip and mode-selection tests for 24/48 kHz input.

Purpose: make the SILK speech path useful for common 48 kHz microphone input.

Scope:

- Allow mono 24/48 kHz voice input to select SILK when the application/signal
  and bitrate indicate low-bitrate speech.
- Downsample to the appropriate SILK internal rate, likely 16 kHz WB as the
  first supported target.
- Emit the correct SILK-only TOC config.
- Preserve CELT behavior for non-voice or higher-bitrate inputs.
- Add top-level round-trip and libopus decode tests for 48 kHz input.

Out of scope:

- Stereo downsampling.
- Hybrid high-band preservation.
- Arbitrary sample rates outside the existing public Opus rates.

Exit criteria:

- 48 kHz mono VOIP/voice at low bitrate emits valid SILK-only packets and
  decodes to the requested output rate.
- Existing CELT cross-checks remain green.

## Slice 12: Stereo SILK

Status: Complete (2026-06-15)

Implemented:

- The internal SILK encoder now writes stereo packets as mid/side SILK channel
  streams using the decoder's existing stereo packet order.
- Stereo predictor symbols are encoded conservatively as zero predictors; side
  frames are always present, with `only_middle` coded false when the side VAD is
  inactive.
- The public SILK-only mode-selection path now allows stereo low-bitrate voice
  where the selected SILK bandwidth is representable.
- Added top-level mode-selection and round-trip coverage, including 48 kHz
  stereo voice input downsampled to WB SILK.

Purpose: extend SILK-only encode beyond mono without disturbing the mono path.

Scope:

- Implement SILK stereo mid/side encode.
- Encode stereo predictor symbols.
- Support side-channel skip / only-middle decisions.
- Preserve mono/stereo state transitions expected by the decoder.
- Add internal trace symmetry tests and top-level encode/decode tests.
- Add libopus decoder cross-checks for supported stereo SILK packets.

Out of scope:

- Hybrid stereo.
- Surround/multistream.
- LBRR/FEC.

Exit criteria:

- Mono SILK behavior remains unchanged.
- Stereo SILK packets decode successfully with this decoder and libopus.
- Mode selection only enables stereo SILK where tests cover it.

## Slice 13: Hybrid Encoder

Status: Complete (2026-06-16)

Implemented:

- Added shared-range encoder hooks for SILK and CELT so a hybrid Opus frame can
  write `SILK -> hybrid redundancy flag -> CELT high-band start=17` into one
  entropy stream.
- Wired top-level high-bitrate 24/48 kHz voice mode selection to hybrid packets:
  24 kHz emits SWB hybrid config 13, and 48 kHz emits FB hybrid config 15 when
  bandwidth selection allows fullband.
- Hybrid selection now applies the signal-driven auto-bandwidth detector before
  accepting the hybrid route; low-bandwidth 24/48 kHz voice falls back to
  CELT-only instead of forcing a SWB/FB hybrid packet.
- Tightened bandwidth-control precedence around the limited SILK/hybrid mode
  selection: `SetBandwidth` is an explicit force, and a lower `SetMaxBandwidth`
  cap applies only while selection is automatic.
- Left hybrid redundancy disabled for this first slice.
- Added normal round-trip tests for 20 ms and multi-frame hybrid packets and an
  `opusref` libopus decoder cross-check for broadband hybrid packets.

Purpose: add Opus hybrid encode after the SILK low band is robust enough to be a
useful base layer.

Scope:

- Encode a 16 kHz SILK low band and CELT high band into one shared range stream.
- Write symbols in the decoder-compatible order:
  `SILK -> hybrid redundancy flag -> CELT high-band start=17`.
- Start with redundancy disabled unless enabling it is needed for correctness.
- Support SWB/FB hybrid configs for 10 ms and 20 ms as separate sub-slices if
  needed.
- Add internal final-range/trace symmetry checks where practical.
- Add this decoder and libopus decoder cross-checks.
- Keep the narrow public mode-selection policy documented and tested rather than
  implying full libopus mode/rate-control parity.

Out of scope:

- Full opus_encoder.c rate control parity.
- LBRR/FEC.
- Complex mode transitions and redundancy tuning beyond the minimum required
  for valid packets.

Exit criteria:

- Hybrid packets decode successfully in this decoder and libopus.
- The SILK-only and CELT-only paths remain covered and stable.
- Documentation clearly distinguishes SILK-only, CELT-only, and hybrid encode
  support.

## Suggested Ordering

1. Slice 7: standards cross-check.
2. Slice 8: quality/regression baseline.
3. Slice 9: NSQ quality work.
4. Slice 10: public mode selection polish.
5. Slice 11: 24/48 kHz voice downsampling into SILK.
6. Slice 12: stereo SILK.
7. Slice 13: hybrid encoder.

The ordering is intentional: prove external decoder compatibility first, add
metrics before quality work, improve mono speech quality before broadening input
rates, and leave stereo/hybrid until the mono SILK base is stable.
