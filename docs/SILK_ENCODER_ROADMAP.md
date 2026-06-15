# SILK Encoder Roadmap

Last updated: 2026-06-15

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

## Goals

- Keep emitted packets standard Opus packets that both this decoder and libopus
  can decode.
- Improve the limited SILK path incrementally before broadening into hybrid
  mode coverage.
- Preserve the working CELT encoder path and official-vector decoder coverage.
- Add tests before quality work when the next change needs objective comparison.

## Non-goals For This Phase

- Bit-exact matching with libopus encoder output.
- LBRR/FEC encoding.
- Ogg Opus container support.
- Multistream or surround.

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
