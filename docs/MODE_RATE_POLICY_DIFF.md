# SILK/Hybrid Mode-Rate-Quality Policy Diff

Last reviewed: 2026-07-15

Scope: Phase D-1 only. This document compares libopus 1.6.1 mode decision and
rate-control policy against the current Go implementation. It deliberately does
not change any mode gate; Phase D-2 must use the real-corpus scoreboard before
changing policy.

Reference files used:

- libopus `src/opus_encoder.c`
- libopus `silk/control_codec.c`
- libopus `silk/control_SNR.c`
- libopus `silk/float/process_gains_FLP.c`
- libopus `silk/float/noise_shape_analysis_FLP.c`
- current Go `opus.go`, `bandwidth_detect.go`, and `internal/silk/*`

## Summary

The current Go encoder has standards-compliant CELT, limited SILK-only, and
initial hybrid output, but its top-level policy is hand-gated. libopus makes the
same decisions through a coupled policy: analysis-derived voice/music estimates,
bitrate-equivalent thresholds with hysteresis, bandwidth switching, LBRR and DTX
state, SILK internal-rate control, hybrid SILK/CELT allocation, and stereo-width
reduction all feed one decision loop.

The most important current gap is not a missing bitstream primitive. It is that
Go selects SILK/hybrid only inside conservative voice gates, then relies on
simpler rate allocation once inside those gates.

## Mode Decision Diff

| Area | libopus 1.6.1 | Current Go | Status |
|---|---|---|---|
| Analysis input | Uses analysis state, `voice_ratio`, tonality/music heuristics, application, signal CTL, previous mode, and detected bandwidth. | Uses `ApplicationVOIP` or explicit `SignalVoice` for predictive-mode intent. `SignalMusic` keeps the packet on CELT. FFT bandwidth/sparsity only narrows bandwidth and protects sparse hybrid tones. | Partial |
| Forced mode | Has internal force-mode controls and restricted SILK/CELT applications. | Public `ApplicationRestrictedLowDelay` blocks predictive modes; no public force SILK/CELT mode. | Partial |
| SILK entry | Uses mode threshold tables for voice/music, bitrate, bandwidth, channel count, previous mode hysteresis, and FEC pressure. | Enters SILK-only only for voice intent, non-low-delay, non-disabled prediction, native SILK bandwidth compatibility, and bitrate <= 40 kbps mono / 48 kbps stereo, plus 8 kbps per channel with active FEC. | Partial |
| Hybrid entry | Uses the same mode decision loop and chooses hybrid when predictive mode and SWB/FB bandwidth are appropriate. | Enters hybrid only for voice intent, 20 ms base frames, 24/48 kHz SWB/FB bandwidth, bitrate above SILK-only and <= 112 kbps mono / 192 kbps stereo, plus 16 kbps per channel with active FEC. | Partial |
| Music predictive mode | libopus still has music-side thresholds and can use mode decisions based on analysis, not only caller intent. | `SignalMusic` explicitly prevents SILK/hybrid routing. | Unsupported |
| Hysteresis | Uses threshold/hysteresis tables and previous mode state. | Keeps previous mode only for CELT/SILK/hybrid transition redundancy and one-frame hybrid deferral; mode thresholds themselves have no hysteresis table. | Partial |
| Frame duration | `OPUS_SET_EXPERT_FRAME_DURATION` and `frame_size_select()` can alter the chosen analysis/encode frame duration. | Public frame duration is the caller's `frameSize`; expert duration CTL is not wired. | Unsupported |

## Bandwidth Policy Diff

| Area | libopus 1.6.1 | Current Go | Status |
|---|---|---|---|
| Automatic bandwidth | Uses mono/stereo voice/music threshold tables, equivalent bitrate, previous auto bandwidth, max bandwidth, user bandwidth, and detected bandwidth. | Uses Nyquist cap, forced/max bandwidth, coarse bitrate thresholds, and signal FFT narrowing. | Partial |
| SILK internal rate | `silk_control_audio_bandwidth()` and control state can switch NB/MB/WB with bitrate and bandwidth constraints. | SILK internal rate is fixed from input: 8/12/16 kHz native, 24/48 kHz downsampled to 16 kHz WB. | Partial |
| Hybrid bandwidth | Hybrid is tied to SWB/FB mode decision and available rate. | Hybrid uses 24 kHz SWB or 48 kHz FB/SWB after `selectHybridBandwidth`; sparse low-band tones can keep hybrid from being narrowed away. | Partial |
| Stereo width | libopus reduces stereo width at low bitrates, including hybrid/SILK width state. | Top-level stereo width reduction is not ported; stereo SILK uses mid/side coding but not libopus' full width policy. | Unsupported |

## Rate-Control Diff

| Area | libopus 1.6.1 | Current Go | Status |
|---|---|---|---|
| User bitrate conversion | `user_bitrate_to_bitrate()` accounts for frame size, overhead, and max packet limits. | `SetBitrate` supports auto/max and numeric rates; auto follows a simpler frame-size/sample-rate/channel formula. | Partial |
| SILK target rate | `silk_control_encoder()` sets SILK target rate, LBRR state, packet loss, DTX, complexity, internal sample rate, and SNR. | Top-level pushes bitrate/rate mode/FEC/loss to the internal SILK encoder; several SILK analysis components are ported, but not the full control loop. | Partial |
| Hybrid split | libopus subtracts SILK rate from total rate for CELT high band and adjusts CELT VBR/CVBR behavior in hybrid. | Hybrid writes SILK first into one range stream, then gives CELT the remaining packet budget. VBR uses `hybridAdaptiveTargetBytes()` based on high-band activity. Smoke scoreboard found some current cells still report encode-size errors instead of valid packets. | Partial |
| CVBR/VBR | libopus defaults to constrained VBR and uses reservoir/constraint behavior across modes. | CELT has VBR/CVBR; SILK/hybrid use simplified target sizing and natural-size paths in selected cases. | Partial |
| DTX | libopus DTX decision is integrated with SILK and top-level mode state. | Public DTX exists; digital silence emits minimal packets and is handled specially, but broader libopus DTX mode decision is not fully equivalent. | Partial |
| LBRR/FEC | libopus LBRR setup adjusts gain increases based on previous LBRR and packet loss. | LBRR/FEC encode/decode is implemented for mono/stereo SILK-only and hybrid; gain-increase scheduling is ported in the internal SILK layer. | Mostly supported |

## SILK Quality-Control Diff

| Area | libopus 1.6.1 | Current Go | Status |
|---|---|---|---|
| SNR target | `silk_control_SNR()` maps target rate to residual quantizer SNR tables. | Table mapping is ported and feeds noise-shape/process-gains paths. | Supported |
| Noise shape | `noise_shape_analysis_FLP` uses speech activity, input quality, coding quality, LTPCorr, tilt, harmonic shaping, and bandwidth expansion. | Large parts are ported; several staging guards remain around stereo/hybrid/transparent NLSF paths. | Partial |
| Pitch/LTP | libopus uses full pitch analysis, LTP coding, and delayed-decision NSQ integration. | Pitch search and delayed-decision NSQ have been substantially ported, but not every mode-control and co-optimization path is equivalent. | Partial |
| Stereo SILK | libopus has mature stereo prediction, width, and bitrate allocation policy. | Stereo mid/side encode/decode and LBRR exist; stereo policy remains narrower and less fully psychoacoustic. | Partial |

## Transition/Redundancy Diff

| Area | libopus 1.6.1 | Current Go | Status |
|---|---|---|---|
| CELT <-> SILK/hybrid transition | Uses 5 ms redundant CELT frames, celt-to-silk flags, redundancy byte limits, and previous-mode state. | Both directions are implemented for SILK-only and hybrid, including one-frame hybrid deferral and decoder crossfades. | Mostly supported |
| PLC/FEC continuity | libopus has integrated PLC/FEC state update through decoder control. | Public PLC and FEC are implemented for CELT-only, SILK-only, and hybrid; SILK PLC is not bit-exact but continuity is guarded. | Mostly supported |

## D-2 Candidate Gates To Measure First

Do not change these gates without a real-corpus scoreboard cell showing a
per-bit improvement or an interoperability necessity.

| Candidate | Current gate | Measurement needed |
|---|---|---|
| Music/auto predictive entry | `SignalMusic` blocks SILK/hybrid. | Compare music and mixed corpus at 16/24/32/48/64 kbps; require `gap_snr_matched_db` and `ratio_bytes_matched` to improve. |
| SILK-only upper bound | 40 kbps mono / 48 kbps stereo, plus FEC extension. | Speech/noisy/onset corpus at 32/48/64 kbps, with and without loss. |
| Hybrid lower/upper bounds | Above SILK-only, up to 112 kbps mono / 192 kbps stereo. | 24/48 kHz speech and mixed corpus; reject cells with encode-size errors until rate allocation is fixed. |
| Automatic bandwidth narrowing | Coarse Go thresholds plus FFT narrowing. | Compare forced bandwidth vs auto cells; track first TOC config and matched SNR. |
| Stereo width policy | No libopus-equivalent top-level width reduction. | Stereo speech and mixed corpus; track L/R SNR, correlation, clipping, and bytes. |

## Guardrails

- Keep range-coder changes out of Phase D unless a failing bitstream requires
  them.
- Treat higher SNR with materially higher `own_bytes` as a rate trade-off, not a
  quality win.
- Use `docs/REAL_CORPUS_SCOREBOARD.md` before any D-2 branch and keep one
  policy gate per branch.
