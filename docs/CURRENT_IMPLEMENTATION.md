# Current Implementation Snapshot

Last reviewed: 2026-06-20

This document describes what the code currently implements. It is intentionally
more conservative than the roadmap and README marketing text: when this file
disagrees with older planning documents, treat this file as the current
code-derived status.

## Public Package

Module: `github.com/darui3018823/opus`

The public API is concentrated in `opus.go`, with constants in `constants.go`
and package-level error values in `errors.go`.

### Encoder

Implemented public entry points:

- `NewEncoder(sampleRate, channels int, application Application) (*Encoder, error)`
- `(*Encoder).Encode(pcm []int16, frameSize int) ([]byte, error)`
- `(*Encoder).EncodeFloat(pcm []float64, frameSize int) ([]byte, error)`
- `(*Encoder).Bitrate() int`
- `(*Encoder).Complexity() int`
- `(*Encoder).VBR() bool`
- `(*Encoder).Application() Application`
- `(*Encoder).SetBitrate(bitrate int) error`
- `(*Encoder).SetComplexity(complexity int) error`
- `(*Encoder).SetVBR(vbr bool)`
- `(*Encoder).SetVBRConstraint(constrained bool)`
- `(*Encoder).SetDTX(enabled bool)` / `(*Encoder).DTX() bool`
- `(*Encoder).SetPacketPadding(n int)`
- `(*Encoder).SetApplication(application Application)`
- `(*Encoder).SetSignalType(signal SignalType)`
- `(*Encoder).SignalType() SignalType`
- `(*Encoder).SetMaxBandwidth(bw int) error`
- `(*Encoder).SetBandwidth(bw int) error` / `(*Encoder).Bandwidth() int`
- `(*Encoder).Reset() error`

Accepted sample rates are `8000`, `12000`, `16000`, `24000`, and `48000`.
Accepted channel counts are mono and stereo.

The top-level encoder always creates an internal CELT encoder at 48 kHz and
uses a 20 ms internal CELT frame (`960` samples per channel) for the general
audio path. Non-48 kHz CELT input is resampled to 48 kHz before CELT encoding.
The CELT emitted TOC byte is CELT-only 20 ms, with the **coded bandwidth
selected per the input sample rate, target bitrate, explicit bandwidth settings,
and signal-content detector** (see Slice 2-6 and the post-2-6 notes below); it
is no longer always fullband.

As of SILK Encoder slices 11 and 12, the top-level encoder can also emit a
limited SILK-only path for low-bitrate speech: `ApplicationVOIP` or an explicit
voice signal hint, mono or stereo input, and target bitrates up to and including
40 kbps. Native 8/12/16 kHz input maps to SILK NB/MB/WB; 24/48 kHz voice input
is downsampled to a 16 kHz WB SILK layer and emits a WB SILK-only TOC config.
That path uses the internal SILK encoder, emits SILK-only duration configs for
20/40/60 ms Opus frames for mono, and packs longer supported durations as
standard multiple Opus frame streams. Stereo SILK packets use 20 ms streams for
60 ms and longer public packet durations to keep individual Opus frame payloads
inside the 1275-byte framing limit. Explicit `SignalMusic`,
restricted-low-delay, bitrates above 40 kbps, a forced bandwidth that cannot be
represented by the selected SILK layer, or, while bandwidth selection is
automatic, a max-bandwidth cap below the SILK bandwidth keep the encoder out of
the SILK-only path. An explicit forced bandwidth takes precedence over the
max-bandwidth cap. DTX, VBR/CVBR, and packet padding do not by themselves opt
the packet out of the supported SILK-only path. In CBR mode, undersized
SILK-only streams are padded up to the nominal per-stream bitrate target; VBR
and CVBR keep compact stream sizes, and digital-silence SILK streams use the
minimal one-byte SILK silence payload unless explicit packet padding is
requested.

As of SILK Encoder slice 13, high-bitrate 24/48 kHz voice input can emit hybrid
packets. The encoder writes a 16 kHz SILK low band and CELT high band into one
shared range stream in decoder-compatible order (`SILK -> hybrid redundancy flag
-> CELT start=17`). 24 kHz voice emits SWB hybrid
config 13; 48 kHz voice emits FB hybrid config 15 when the selected bandwidth is
fullband. Automatic signal-content bandwidth narrowing runs before hybrid mode
selection; if the analysed signal narrows below SWB, the encoder falls back to a
CELT-only packet using that narrower bandwidth. Explicit forced bandwidths still
take precedence over max-bandwidth caps. Hybrid is currently limited to 20 ms
Opus frames, optionally packed as standard multi-frame packets for longer public
frame sizes.

The encoder implements both directions of libopus-style SILK/hybrid↔CELT
transition redundancy. It tracks the previous packet's coding mode with
`Encoder.prevMode`. For hybrid→CELT, the switch is deferred by one packet: the
transitional packet stays hybrid and appends a trailing 5 ms (240 @ 48 kHz)
fullband CELT redundant frame (`celt_to_silk=0`) to the last sub-frame. For
CELT→SILK-only or CELT→hybrid, the first sub-frame carries a leading 5 ms CELT
redundant frame (`celt_to_silk=1`); SILK-only infers its length from the bytes
remaining after the position bit, while hybrid explicitly codes the redundancy
flag and byte count. Redundancy sizes use `computeRedundancyBytes` (libopus
`compute_redundancy_bytes`). Redundant frames are encoded on a dedicated reset
CELT encoder so they carry no overlap history; leading frames use the destination
band limit (17 for SILK wideband, 19/21 for hybrid SWB/FB). libopus 1.6.1 decodes
all three transition forms (verified by
`TestCGOEncodeRefHybridRedundancyTransition` and
`TestCGOEncodeRefCELTToSILKRedundancyTransition`).

Supported public encode packet durations are exact 20 ms multiples from 20 ms
through 120 ms (`frameSize == base20ms * 1..6`). Unsupported frame sizes and
durations over the Opus 120 ms packet limit are rejected with
`ErrUnsupportedFrameSize`.

### Phase 2: Production CELT Encoder (In Progress)

#### Slice 2-1: VBR/CVBR Rate Control (Complete)
- **Status:** Complete
- Added `celt.RateMode` enum (`RateModeCBR`, `RateModeVBR`, `RateModeCVBR`).
- Implemented CVBR bit reservoir tracking (`vbrOffset`) across frames.
- Replaced target padding logic with proper target length constraints, avoiding desyncs.
- Plumbed `SetVBR` and `SetVBRConstraint` via top-level `Encoder`.
- Created comprehensive VBR packet size variance and roundtrip tests.

#### Slice 2-2: Multi-frame Packets (Complete)
- **Status:** Complete
- Added `packOpusFrames` (inverse of `splitOpusFrames`) and `encodeOpusFrameLength`
  (inverse of `parseOpusFrameLength`), building RFC 6716 §3.2 count code 0/1/2/3
  packets and choosing the most compact code (equal-size CBR → code 1 / 3-CBR;
  variable sizes → code 2 / 3-VBR).
- The top-level `Encoder` now packs multi-frame packets: a requested `frameSize`
  that is an exact 2..6× multiple of the 20 ms base is split into that many
  consecutive 20 ms CELT frames (resampler and CELT state stay continuous across
  chunks) and packed into one packet, enabling 40 ms / 60 ms output. The TOC
  per-frame config stays 20 ms; duration is expressed via the count code.
- Tests: `encodeOpusFrameLength`↔`parseOpusFrameLength` round-trip over 0..1275,
  `packOpusFrames`→`splitOpusFrames` identity across counts/size profiles, and
  end-to-end 40/60 ms encode→decode in both CBR and VBR.

#### Slice 2-3: VBR Shrink Wiring + Code-3 Padding (Complete)
- **Status:** Complete
- Wired the CELT VBR/CVBR path through `entcode.Encoder.Shrink` (shrinking the
  range coder to the activity-derived target before any symbols are written, so
  the coarse-energy and allocation decisions stay budget-symmetric with the
  decoder).
- Added top-level code-3 packet padding: `encodePaddingCount`,
  `packOpusFramesPadded`, and `(*Encoder).SetPacketPadding(n)`.

#### Slice 2-4: Silence Detection / DTX (Complete)
- **Status:** Complete
- The CELT encoder now detects digital silence after analysis (summed SIG-domain
  band energy below a fixed threshold) and emits a minimal silence frame: only
  the logp-15 silence flag is written, mirroring the decoder's silence handling
  (it advances the tell to the packet end so all later symbol guards fail and
  forces the band energies to the -28 dB floor). The encoder updates its
  inter-frame predictor to the -28 floor and seeds the fold/final range from the
  range value right after the silence bit, keeping it bit-symmetric with the
  decoder's post-frame state.
- Silence-frame sizing: VBR/CVBR (and DTX) keep the minimal flushed packet
  (~2-3 bytes); plain CBR with DTX off pads silent frames to the full target so
  the constant-bitrate contract holds.
- Added `(*Encoder).SetDTX(bool)` / `DTX()` at both the CELT and top-level
  layers. With DTX enabled, silent frames are emitted as minimal packets even in
  CBR, and multi-frame packing switches to the variable-length path (silent and
  loud frames in one packet have different sizes).
- Tests: `TestCeltSilenceRoundTrip` (enc/dec final-range symmetry + silent
  reconstruction, mono+stereo), `TestCeltSilenceMinimalSize`,
  `TestCeltSilenceCBRPaddedSize`, `TestEncoderDTXSilencePackets`,
  `TestEncoderDTXOffCBRFixedSize`, `TestEncoderDTXMultiFrame`. 12/12 official
  vectors unchanged.

#### Slice 2-5a: Allocation Shaping (Complete)
- **Status:** Complete
- Replaced the CELT encoder's fixed baseline decisions (spread = NORMAL,
  dynalloc offsets = 0, alloc_trim = 5) with float ports of the libopus
  `celt_encoder.c` analysis functions (`internal/celt/celt_analysis.go`):
  - `spreadingDecision`: per-band tonality from the normalised spectrum with
    recursive averaging and hysteresis (uniform spread_weight simplification).
  - `dynallocAnalysis`: a masking "follower" over band log energies producing
    per-band boost counts; the internal 2/3-budget break is dropped because
    `dynallocEncode` already clamps the coded boost against the real range-coder
    budget and the per-band cap, symmetric with the decoder.
  - `allocTrimAnalysis`: trim index from spectral tilt plus, for stereo,
    low-frequency inter-channel correlation.
- These feed the existing symbol writers, so the decoder reads them unchanged;
  enc/dec final-range symmetry is preserved.
- Delay-aligned round-trip SNR improved (sine440 25.1→35.9, sine1k 22.7→38.9,
  sine4k 27.3→28.9, sine1k-stereo 26.8→34.7 dB); the
  `TestEncoderRoundTripAlignedSNR` thresholds were raised to 24..30 dB.
- 12/12 official vectors unchanged.

#### Slice 2-5b: Transient Detection + Short-Block MDCT + Anti-Collapse (Complete)
- **Status:** Complete
- Added a float port of libopus `transient_analysis` (`internal/celt/celt_analysis.go`,
  the `!FIXED_POINT` branch): a high-pass filter plus forward/backward leaky
  masking integrators produce a bitrate-normalised temporal noise-to-mask ratio
  (`mask_metric`) compared against the same `>200` threshold libopus uses. The
  signal scale cancels in the metric, so the encoder's ×32768 domain matches the
  threshold. The weak-transient and tone-detection refinements (low-bitrate
  hybrid only) are omitted.
- The CELT encoder now runs detection on the time-domain pre-emphasis buffers
  before the MDCT. On a detected transient it computes `M = 2^LM` interleaved
  `NBase`-point forward MDCTs (a new encoder `shortCeltMode`, the analysis
  counterpart of the decoder's transient synthesis) into the `coeff[b+i*M]`
  layout the decoder expects, instead of one long block. The `isTransient`
  symbol is coded as before; `tfEncode` already branches on it.
- Anti-collapse: the encoder tracks `consec_transient` and codes the
  anti-collapse bit as `consec_transient < 2` (libopus semantics), read
  pre-update and advanced at end of frame. The decoder applies anti-collapse
  using the PVQ collapse masks, unchanged.
- Verification: `TestTransientAnalysisDetection` (attack vs steady tone),
  `TestCLTMDCTShortBlockRoundtrip` (dsp short forward/backward pair reconstructs
  through chained overlap-add, 149 dB), and `TestCeltTransientRoundTrip` (every
  frame's final range matches across the transient↔steady boundary, and short
  blocks cut pre-echo ~1.8× vs forced long blocks on an impulse-in-silence).
- 12/12 official vectors unchanged; steady-sine aligned SNR unchanged (steady
  tones don't trigger detection).
- Not yet done at 2-5b (see 2-5c below for the stereo decisions): real
  `tf_analysis` (per-band tf_res RDO is still flat 0) and the complexity≥8 second
  long-block MDCT for `bandLogE2`.

#### Slice 2-5c: Stereo Decisions (intensity / dual_stereo) (Complete)
- **Status:** Complete
- Replaced the CELT encoder's fixed stereo parameters (intensity band = `end`,
  i.e. intensity stereo disabled, and `dual_stereo = false`) with float ports of
  the libopus stereo decisions (`internal/celt/celt_analysis.go`):
  - `stereoAnalysis` (libopus `bands.c stereo_analysis`): chooses dual stereo
    (independent L/R) vs joint mid/side from an L1-norm entropy proxy comparing
    the L/R and M/S representations over the low bands.
  - `hysteresisDecision` + `intensityThresholds`/`intensityHysteresis` (libopus
    `celt_encoder.c`): picks the intensity-stereo starting band from the
    equivalent bitrate in kbps, biased toward the previous frame's choice. The
    encoder keeps the previous value in a new `intensity` state field (zeroed by
    `Reset`, matching libopus `OPUS_RESET_STATE`).
- These feed `computeAllocationEncode`, which writes both into the stream; the
  decoder reads them unchanged, so enc/dec final-range symmetry is preserved (any
  in-range choice round-trips). At the default 64 kbps stereo the intensity band
  resolves to 15, so the high bands switch to single-channel coding.
- Delay-aligned stereo SNR is unchanged-to-slightly-better (sine1k-stereo
  34.7→35.3 dB); the tone sits below the intensity band so the freed high-band
  bits don't cost quality.
- Verification: `TestStereoAnalysisDecision` (L==R → mid/side, decorrelated →
  dual), `TestIntensityHysteresis` (64 kbps → band 15, sticky near the
  boundary), and `TestCeltStereoDecisionRoundTrip` (correlated/anti-correlated/
  decorrelated stereo all decode with matching final range).
- 12/12 official vectors unchanged; `go build/vet/test ./...` green.
- Not yet done (future quality work): real `tf_analysis` (per-band tf_res RDO is
  still flat 0) and the complexity≥8 second long-block MDCT for `bandLogE2`.

#### Slice 2-6: Bandwidth Selection (Phase 4 lite) (Complete)
- **Status:** Complete
- The CELT encoder gained a configurable coded-band count (`(*celt.Encoder).SetEndBand`),
  so it can code NB (13 bands), WB (17), SWB (19), or FB (21) instead of always
  fullband. The value must match the band count the decoder derives from the
  packet's TOC config; `endBand` is a config field, not cleared by `Reset`.
- The top-level encoder selects the coded bandwidth per frame from the input
  sample rate's Nyquist limit, a coarse bitrate ceiling, and the explicit
  bandwidth settings, then sets the CELT end-band and emits the matching TOC
  config (CELT-only NB/WB/SWB/FB, 20 ms). Selection is config-driven (not
  signal-driven), so all frames in a multi-frame packet share one config.
  - Nyquist mapping: 8 kHz → NB, 12/16 kHz → WB (CELT has no medium band, so
    12 kHz rounds up to WB rather than dropping 4–6 kHz), 24 kHz → SWB, 48 kHz → FB.
  - Bitrate ceiling (heuristic, conservative; default 64 kbps stays FB):
    <16 kbps → NB, <28 → WB, <44 → SWB, else FB.
- New public API: `SetMaxBandwidth(bw)` (caps automatic selection only),
  `SetBandwidth(bw)` (forces a bandwidth, still clamped to Nyquist;
  `BandwidthAuto` returns to auto), and `Bandwidth()` (reports the current
  config-driven choice). A new `BandwidthAuto` constant was added.
- Verification: `TestBandwidthSelection` (Nyquist/cap/force/bitrate logic),
  `TestBandwidthRoundTrip` (each forced bandwidth emits the right config and
  decodes a 1 kHz tone back: NB 47, WB 45, SWB 40, FB 39 dB aligned SNR),
  `TestTOCByteMultiRate` (updated: each input rate emits its Nyquist bandwidth),
  and the libopus cross-check `TestCGOEncodeRefBandwidth` (libopus decodes every
  bandwidth-limited stream: NB 46.7, WB 44.8, SWB 39.7, FB 39.0 dB).
- 12/12 official vectors unchanged; `go build/vet/test ./...` green.

#### Post-2-6: Signal-driven bandwidth, signal hints, and review fixes (Complete)
- **Status:** Complete
- Automatic bandwidth selection now narrows the config-driven ceiling using a
  signal-content detector (`bandwidth_detect.go`): input PCM is downmixed,
  Hann-windowed, FFT analysed, and mapped to NB/WB/SWB/FB by the highest active
  bin above a -50 dB relative threshold. Narrowing uses hysteresis and never
  widens beyond sample-rate/bitrate/manual caps.
- `ApplicationVOIP` and `SignalVoice` use voice-oriented bitrate thresholds;
  `ApplicationAudio`, `ApplicationRestrictedLowDelay`, and `SignalMusic` use
  music/general thresholds. Public `SetSignalType` / `SignalType` expose this
  content hint independently of `SetApplication`.
- The same automatic bandwidth narrowing is applied before hybrid selection, so
  low-bandwidth 24/48 kHz voice input does not enter hybrid solely because the
  sample rate and target bitrate would otherwise allow it. Hybrid selection
  preserves the config-driven SWB/FB bandwidth for spectrally sparse
  tonal/harmonic voice, preventing steady voiced frames from falling to NB
  CELT-only merely because their highest harmonic is below 4 kHz; dense
  low-bandwidth signals still narrow and fall back to CELT-only.
- Mode-selection precedence now matches the public bandwidth controls:
  `SetBandwidth` is an explicit force and is not further restricted by a
  previously configured `SetMaxBandwidth` cap; the max cap applies only after
  returning to `BandwidthAuto`.
- Public getters were added for encoder bitrate, complexity, VBR state, and
  application mode.
- v1.1.1 fixed library-review issues: stereo CELT/hybrid packets can be decoded
  by mono decoders by using stereo CELT state and downmixing after decode; encode
  and decode paths enforce the Opus 120 ms packet duration limit; `Decode`
  returns `ErrBufferTooSmall` instead of silently truncating; `GetLastPacketDuration`
  reports the actual decoded duration; mono SILK multi-frame LBRR side flags are
  consumed to keep the range decoder aligned; CELT reset clears the final-range
  folding seed; low-budget transient fallback recomputes long-block coefficients;
  and raw-only entropy `EncodeBits` flushes correctly.

Current encoder limitations:

- `application` and `SignalType` drive encoder heuristics and can select the
  limited low-bitrate SILK-only path, including stereo and 24/48 kHz input
  downsampled to WB SILK, and the initial high-bitrate 24/48 kHz hybrid voice
  path.
- VBR/CVBR affects CELT target sizing and packet sizes, but the rate controller
  is still a simplified CELT-only implementation rather than libopus-equivalent
  full mode/rate control.
- `EncodeFloat` uses `float64`; there is no public `EncodeFloat32` method.
- The public encoder exposes limited SILK-only and hybrid speech paths; it does
  not yet expose full libopus-equivalent SILK/hybrid mode selection.
- The CELT encoder path is functional but not verified as bit-exact against
  libopus.

### Decoder

Implemented public entry points:

- `NewDecoder(sampleRate, channels int) (*Decoder, error)`
- `(*Decoder).Decode(data []byte, pcm []int16) (int, error)`
- `(*Decoder).DecodeFloat(data []byte) ([]float64, error)`
- `(*Decoder).DecodeFEC(data []byte, pcm []int16) (int, error)`
- `(*Decoder).Reset() error`
- `(*Decoder).GetLastPacketDuration() int`

Accepted sample rates are `8000`, `12000`, `16000`, `24000`, and `48000`.
Accepted channel counts are mono and stereo.

`NewDecoder` pre-creates:

- CELT decoders for 4 bandwidth groups, 4 CELT frame durations, and mono/stereo.
- SILK decoders for 8/12/16 kHz, 10/20 ms frame decoders, and mono/stereo.
- Resamplers when the packet-internal rate differs from the requested output
  rate.

`DecodeFloat` parses the Opus TOC byte. Configs `< 16` go through the SILK or
hybrid path; configs `>= 16` go through the CELT-only path. CELT count codes
`0`, `1`, `2`, and `3` are split into per-frame payloads. SILK 40/60 ms TOC
configs decode multiple 20 ms SILK subframes from each Opus frame stream.
Packets whose decoded duration exceeds 120 ms are rejected as invalid.

Current decoder limitations:

- `DecodeFEC` currently uses CELT packet-loss concealment from the fullband
  20 ms decoder and does not decode FEC data from the supplied packet.
- There is no public `DecodeFloat32` method.
- There is no public `DecodePLC(pcm, frameSize)` method; CELT PLC exists
  internally and is reached through `DecodeFEC`.
- `GetLastPacketDuration` reports the duration in output samples per channel of
  the last successfully decoded packet; before any decode it reports the default
  20 ms duration for the decoder sample rate.
- The decoder passes all 12 official RFC 8251 vectors (RMSE < 0.001). The
  separate cgo/libopus reference comparison (`TestCGORef`, `go test -tags opusref`)
  also passes all 12 vectors against libopus 1.6.1 (overall RMSE < 0.001).

## Internal Packages

### `internal`

`internal/opus_framing.go` contains TOC parsing and generation helpers:

- `ParseTOC`
- `ParseTOCConfig`
- `BandwidthForRate`
- `GenerateTOCExt`
- legacy `GenerateTOC` for CELT-only fullband 20 ms packets

The config mapping follows RFC 6716 Table 2 for SILK-only, hybrid, and
CELT-only modes.

### `internal/entcode`

The entropy coder implements libopus-style range encoder/decoder state:

- ICDF symbol coding
- `DecodeBitLogp` / `EncodeBitLogp`
- `DecodeUint` / `EncodeUint`
- decoder-side raw bits from the end of packet via `DecodeBits`
- Laplace roundtrip helpers

As of the encoder Phase 0 work, `EncodeBits` is a true end-of-packet raw-bit
writer (ported from libopus `ec_enc_bits`/`ec_enc_done`): raw bits accumulate in
an end-window and are flushed LSB-first to the tail of the packet, symmetric with
the decoder's `DecodeBits`. `Bytes()` assembles the forward range bytes, a zeroed
gap, and the raw tail at the absolute end; it still returns the minimal front
buffer when no raw bits are used. `Tell()` now counts raw bits (matching
`ec_tell`'s `nbits_total`). Round-trip guards: `TestEncodeBitsRawRoundtrip`,
`TestEncodeBitsRawOnlyRoundtrip`, and `TestEncodeUintLargeFtRoundtrip` (the
`ec_enc_uint` ftb>UintBits split path).

### `internal/dsp`

DSP support includes:

- complex arithmetic and vector helpers
- radix-2 FFT/IFFT plus `FFTConfig`
- `AnyFFT`/`AnyIFFT` helpers for arbitrary lengths
- real FFT wrappers
- MDCT/IMDCT and overlap-add helpers
- CELT-oriented IMDCT mode in `mdct_celt.go`
- Hann, Hamming, Blackman, sine, and Vorbis windows

### `internal/resampler`

The resampler accepts the five Opus rates and mono/stereo audio. It uses
generated filter coefficients, Kaiser-window helpers, per-channel processing,
and state reset support. Tests cover identity conversion, upsampling,
downsampling, stereo handling, roundtrips for all Opus rates, and all rate
pairs.

### `internal/celt`

The CELT package contains the most active Opus parity work:

- mode and band configuration for 48 kHz CELT bands
- coarse energy quantization/unquantization
- rate allocation using libopus allocation tables
- PVQ/CWRS decoding and related libopus parity tests
- time-frequency decode, dynamic allocation, fine energy, anti-collapse, and
  post-filter paths in the decoder
- transient detection, pitch analysis, stereo helpers, and a simplified encoder

The decoder tracks energy history, log-energy history, overlap state,
post-filter state, preemphasis memory, and `lastFinalRange` for reference
comparison. The public Opus decoder copies CELT state between the pre-created
decoder variants so one logical stream can switch frame size, bandwidth, or
channel layout.

The CELT encoder performs MDCT analysis, transient/patch-transient decisions,
TF analysis, band energy computation, coarse energy coding, bit allocation,
dynamic allocation, stereo/intensity decisions, anti-collapse, final fine energy,
and PVQ encoding. It emits standard CELT-only Opus packets and is cross-checked
against libopus decoding, but it is not documented as bit-exact with libopus's
encoder.

### `internal/silk`

The SILK package contains:

- SILK decoder state for 8/12/16/24 kHz construction
- 10 ms and 20 ms decoder construction
- NLSF codebooks and NLSF-to-LPC conversion
- gain, pitch, LTP, pulse, shell, and stereo helper tables
- LPC, pitch, NLSF, gain, and VAD helpers
- SILK Encoder slice 1/2/3/4/5 foundation: the internal encoder can create 10 ms or
  20 ms mono range streams with the decoder-compatible SILK ordering (all VAD
  flags, LBRR flag, then per-frame type/gain/NLSF/interp/seed/pulse symbols)
  and can pack multiple SILK frames into one shared range stream. Slice 2
  replaced the zero-excitation placeholder with structurally correct SILK
  pulse coding: rate-level selection, per-shell-block pulse counts, shell split
  symbols, LSB support, and sign coding now round-trip through the decoder
  trace with non-zero decoded excitation. Slice 3 started voiced analysis:
  active frames can be classified as voiced using normalized pitch correlation,
  encode absolute pitch lag, flat pitch contour, LTP codebook gains, and LTP
  scale in the decoder-compatible order, and drive pulse coding from a
  short-term/LTP residual rather than raw samples. Slice 4 replaced the fixed
  NLSF payload with input-adaptive stage-1 selection and bounded stage-2
  residual indices, chosen by decoder-compatible LPC residual energy and
  verified by range encode/decode trace symmetry. Slice 5 adds the first simple
  NSQ/residual-quantization body: gain coding now returns the decoder-visible
  subframe gain indices, and LPC/LTP residual samples are quantized into SILK
  pulses using the same gain scale, quantization offset, seed sign flip, and
  LSB-capable shell path that the decoder consumes. Slice 9 replaces the public
  encode path's residual-only pulse choice with a first closed-loop NSQ-style
  synthesis search: the encoder now carries LPC/LTP synthesis state, mirrors the
  decoder's gain, seed, LPC, LTP, quantization-offset, and gain-adjustment
  semantics while selecting pulses, and feeds output error forward as a simple
  noise-shaping term. This is still not a full libopus delayed-decision NSQ, and
  the analysis remains intentionally simple: no LPC-to-NLSF root solve and no
  full rate-controlled SILK NSQ.
- SILK Encoder slice 6 wires that internal mono encoder into the public Opus
  encoder for low-bitrate VOIP/voice packets at 8/12/16 kHz. It packs one shared
  SILK range stream for 20/40/60 ms single-Opus-frame packets and uses standard
  Opus frame packing for longer supported durations; stereo uses 20 ms streams
  for public durations of 60 ms and above to stay within the Opus per-frame
  payload-size limit. Slice 7 added libopus
  decoder cross-checks for the public mono SILK encode path and fixed the
  packetization so libopus accepts those packets. Slice 11 adds 24/48 kHz voice
  input downsampling to a 16 kHz WB SILK layer, Slice 12 adds conservative
  stereo SILK mid/side packet writing, and Q6 replaces its original zero
  predictors with adaptive low/high-band least-squares predictors. The
  predictors use the libopus SILK quantization table and joint index coding,
  and the side encoder receives the prediction residual. Slice 13 adds the
  first public hybrid mode for high-bitrate 24/48 kHz voice. The public
  integration is still intentionally narrow. Slice 8 adds
  deterministic quality/regression baselines for
  the internal mono SILK encoder and the public SILK-only path, covering silence,
  unvoiced/noise-like input, steady voiced tones, speech-like harmonics, and
  onset frames. The public SILK-only baseline also covers stereo 16 kHz and
  48 kHz voice paths with channel-level SNR, L/R RMS, mid/side RMS, and
  correlation guards. The tests log packet size, decoded energy, peak, clipping
  count, aligned SNR/RMSE, and pitch continuity where useful, while guarding
  against silence regressions, dead output, energy runaway, duration errors,
  side-channel collapse, and severe quality drops. Slice 10 hardens the public
  mode/rate boundary with tests for
  the 40 kbps SILK limit, application and signal hints, pre-Slice-11/12
  channel/input-rate exclusions, forced/max bandwidth interaction, and
  VBR/DTX/padding interaction. The public SILK-only integration now also pads
  undersized CBR streams to the nominal bitrate target while leaving VBR/CVBR
  and DTX-silence streams compact. Q1a of the quality phase has started the
  LPC/NLSF fidelity work: the encoder builds a windowed LPC spectral-envelope
  NLSF target, uses libopus NLSF codebook weights to rank stage-1 candidates,
  seeds encodable residual indices from that target, and then keeps the selected
  candidate on the decoder-compatible reconstruction path with a spectral
  peak-gain guard around the current simple NSQ. This is an incremental quality
  slice, not a full libopus-equivalent `silk_find_LPC_FLP` / `silk_A2NLSF` /
  `silk_NLSF_encode` port, and active NLSF interpolation is still deferred. Q5a
  has started gain/rate-control work for unvoiced SILK frames: the encoder
  snapshots frame synthesis state, tries candidate gain boosts and NSQ
  pulse-rate penalties against a flushed range-byte estimate, and selects the
  first non-collapsing plan that fits or best approaches the 20 ms bitrate
  target. Q5b tightened that loop by removing the over-large gain-boost
  candidate that collapsed unvoiced noise energy, relaxing the activity floor
  enough for compact non-collapsed plans, and rejecting candidates whose
  synthesized frame RMS is far below the input. This bounds the unvoiced-noise
  byte blowup while leaving the voiced path to the Step 4 SNR/gain pipeline. Q5c
  keeps public SILK-only digital-silence streams minimal even in CBR mode by
  emitting the one-byte SILK silence payload instead of padding them to the
  nominal packet size; explicit packet padding still applies when requested.
  Voiced mono SILK-only frames now use the SNR-derived gains as a VBR target:
  the encoder tries the natural trellis NSQ result once, keeps it when it fits
  the per-frame byte ceiling, and only falls back to the budget-search clamp on
  overflow. This natural-size path is enabled only when the public encoder is in
  VBR or constrained-VBR mode. Plain CBR keeps every active mono SILK-only
  stream at the nominal packet size (for example, 61 bytes including the TOC
  for 20 ms at 24 kbps); silence remains minimal. The top-level encoder now
  passes its rate-mode contract explicitly into the SILK encoder instead of
  allowing voiced content to bypass CBR padding. The `silk_control_SNR` table
  is still copied from libopus, but the
  voiced SNR-target VBR path now backs the effective target down at 24 kbps and
  below before `gain_mult`/`process_gains` (22.5 dB for NB, 20 dB for MB/WB)
  because the simplified voiced trellis/NSQ lands well above libopus's
  steady-tone operating point at the raw table values; short-lag voiced frames
  use rate-specific backoff. The former NB-only 30 dB backoff and 64x
  pulse-rate penalty were removed because they reduced the 8 kHz onset target
  to 0 dB and dominated the onset quality loss; NB short-lag frames now use the
  normal 22.5 dB NB backoff and unscaled trellis rate weight.
  `OPUS_SILK_RC_SNR=0` restores the previous
  budget-fitting/padding behaviour for A/B comparisons; unvoiced, silence,
  stereo, and hybrid paths keep their previous rate-control behaviour. Stereo
  voiced SILK-only and hybrid frames now also use delayed-decision trellis NSQ.
  Stereo components retain budget-based rate control and neutral
  component-domain spectral shaping. Flushed stereo trellis streams are not
  zero-extended because those bytes alter libopus's tail-symbol decode; CBR
  fill instead uses Opus code-3 packet padding, which leaves the entropy stream
  unchanged while fixing undersized active packets at the nominal packet size. A cgo
  final-range regression verifies encoder/libopus entropy-state agreement
  across consecutive stereo and hybrid voiced packets. Hybrid trellis initially
  exposed a decoder-side RMS divergence even though the entropy final range
  matched libopus: the fixed-point NLSF-to-LPC port omitted libopus's final
  inverse-prediction-gain stability check and iterative bandwidth expansion.
  That stabilization is now ported, including the exact chirp update. Q3a/Q4a
  starts the shaping/NSQ handoff by computing per-subframe shaping controls
  (feedback, spectral tilt, LF/HF shaping, voiced harmonic shaping, and a
  Lambda-style pulse penalty scale) and feeding them into the current
  single-state closed-loop NSQ. This replaces the former single scalar
  output-error feedback term, but it is still not a faithful libopus
  `silk_noise_shape_analysis_FLP` / `silk_prefilter_FLP` /
  `silk_NSQ_del_dec_FLP` port. Q2 (`internal/silk/pitch_flp.go`) replaces the
  former home-brew single-lag full-frame autocorrelation with a faithful float
  port of `silk_find_pitch_lags_FLP` + `silk_pitch_analysis_core_FLP`: an
  LPC-residual whitening front end feeds a three-stage hierarchical search
  (4 kHz coarse correlation, 8 kHz stage-2 lag codebook, original-rate stage-3
  pitch contour) using the fixed-point `down2`/`down2_3` decimators and the
  stage-2/3 lag codebooks. The voiced path now encodes the real absolute lag
  index and pitch contour index (no longer a flat contour) and reconstructs the
  per-subframe lags from the encoded indices so the NSQ uses the same lags the
  decoder will. This is the pitch slice only; LTP-gain quantization and voiced
  rate control remain simplified, so voiced/onset packets still spend more bytes
  than libopus. `silk_VAD_GetSA_Q8` is now ported for encoder analysis as a
  four-band SILK VAD state feeding speech activity, input tilt, and input
  quality into pitch and noise-shape analysis, while the older homebrew VAD is
  still used for the bitstream VAD flag decision. Its live-onset decision
  bypasses the five-frame majority-vote attack delay for mono and single-frame
  stereo mid/side components, so initial harmonic stereo frames reach pitch
  analysis. Multi-frame stereo streams retain smoothed flags to preserve their
  shared conditional-gain entropy context. The homebrew NSQ used by
  inactive/unvoiced frames now hands its reconstructed LPC, LTP, gain, and lag
  state to the delayed-decision trellis before a voiced frame. This keeps
  encoder and decoder synthesis aligned across unvoiced-to-voiced transitions;
  the 8/12 kHz speech-harmonic alignment-scale regression is covered by
  sample-level state and public quality guards. The libopus A/B harness now
  compares constrained VBR on both encoders, forces the matched-bitrate
  libopus run to the same SILK bandwidth as the Go packet, and reports an
  explicit RMS loudness difference in dB. On the speech-harmonic fixture the
  matched loudness differences are within 0.75 dB at 8/12/16 kHz; the former
  approximately 0.57 alignment scale is no longer present.

The public Opus decoder instantiates SILK decoders for 8/12/16 kHz packet
rates. Hybrid configs (12-15) are fully reconstructed in `opus.go`: a single
range decoder runs SILK, the hybrid redundancy header, then the CELT high band,
and the two outputs are resampled and time-domain summed. Both redundancy
directions are handled: trailing SILK→CELT frames crossfade the packet tail and
seed subsequent CELT state, while leading CELT→SILK/hybrid frames replace the
first 2.5 ms and crossfade into the new mode over the next 2.5 ms.

## Test Status

Commands checked:

```bash
go vet ./...
go test -count=1 ./...
go test -count=1 -tags opusref ./...
```

Result on 2026-06-16: passing (`go vet ./...`, `go test -count=1 ./...`,
and `go test -count=1 -tags opusref ./...` exit 0).

Q3a/Q4a verification on 2026-06-17: passing (`go vet ./...`,
`go test -count=1 ./...`,
`go test -count=1 ./internal/silk -run TestSILKInternalQualityBaseline -v`,
and `go test -count=1 -tags opusref -run "TestCGOEncodeRefSILKOnly|TestOpusSILKABAgainstLibopusEncoder" -v .`).
Q5a verification on 2026-06-17: passing (`go vet ./...`,
`go test -count=1 ./...`, and
`go test -count=1 -tags opusref -run TestOpusSILKABAgainstLibopusEncoder -v .`).
Voiced SNR-VBR rate-control verification on 2026-06-18: passing
(`go test ./...` and
`go test -tags opusref -run TestOpusSILKABAgainstLibopusEncoder -v .`).

Passing package-level tests:

- root package `opus` (including `TestOfficialVectors`, 12/12)
- `internal/celt`
- `internal/dsp`
- `internal/entcode`
- `internal/resampler`
- `internal/silk`
- `internal/testing`

Official-vector status (update 2026-06-14): **all 12 RFC 8251 vectors PASS**
with RMSE < 0.001 (`TestOfficialVectors`). testvector01 — previously the last
failure and long mislabelled "heavy SILK" — is in fact CELT-only fullband
stereo; it was fixed by correcting the code-3 multi-byte padding parse in
`splitOpusFrames` (RFC 6716 §3.2.5: each 0xFF count byte = 254 padding-data
bytes plus a continuation).

Notes:

- Official-vector and `.bit`-based diagnostic tests `t.Skip` when `testdata/`
  (git-ignored) is absent; CI downloads `opus_testvectors-rfc8251.tar.gz` into
  `testdata/opus_newvectors/` so they run for real.
- The dedicated Ubuntu `opusref` workflow installs `libopus-dev` and exports
  the header search path reported by `pkg-config --cflags opus` through
  `CGO_CFLAGS`. This is required because distro packages install `opus.h`
  below an `opus/` include directory rather than at the compiler's default
  include root.
- The cgo/libopus reference comparison runs under `go test -tags opusref` and
  needs a C toolchain plus libopus (reported `libopus 1.6.1` locally); it passes
  all 12 vectors. Normal builds use a `!opusref` stub so the codec stays
  CGO-free.
- The pure-Go **encoder** is also cross-validated against libopus under the same
  tag: `TestCGOEncodeRef` encodes synthetic signals with our encoder and decodes
  the packets with libopus 1.6.1, then measures delay-aligned SNR. libopus
  reconstructs the encoder's output to within ~0.01 dB of our own decoder
  (about 48 dB for 440 Hz, 47 dB for 1 kHz, 39 dB for 4 kHz, and 43 dB for
  stereo 1 kHz after signal-driven bandwidth detection), and
  `TestCGOEncodeRefSilence` confirms silent input decodes to silence in libopus.
  This shows the encoder emits genuinely standard-compliant Opus, not a stream
  only our own decoder accepts. (Still not bit-exact against libopus's encoder,
  which is not required.)
- `TestCGOEncodeRefSILKOnly` cross-checks the limited public SILK-only encoder
  path with libopus for 8/12/16 kHz mono, VOIP and explicit voice routing, and
  20/40/60 ms packet durations. It verifies SILK-only TOC configs, decoded
  duration, output length, bounded peaks/energy, and coarse decoder/libopus
  reconstruction similarity. Normal tests now also cover 24/48 kHz downsampled
  voice input and stereo SILK round trips.
- `TestCGOEncodeRefHybrid` cross-checks the initial public hybrid encoder path
  with libopus for 24 kHz SWB mono and 48 kHz FB mono/stereo voice packets using
  broadband fixtures so the signal-driven narrowing still permits hybrid.
- `TestSILKInternalQualityBaseline`, `TestEncoderSILKOnlyQualityBaseline`, and
  `TestEncoderSILKOnlyStereoQualityBaseline` provide the Slice 8 SILK
  quality/regression baseline using deterministic synthetic fixtures. They run
  in normal `go test ./...` and log packet size, RMS/peak/clipping, aligned
  SNR/RMSE, delay/scale, steady-pitch continuity, and stereo L/R correlation plus
  mid/side energy where useful.
- `TestEncoderSILKOnlyModeSelectionMatrix` and
  `TestEncoderSILKOnlyVBRDTXAndPaddingStillSelectSILK` cover the Slice 10 public
  mode/rate selection boundary for supported SILK-only cases and CELT fallbacks,
  including the forced-bandwidth-over-max-bandwidth precedence.
- `TestEncoderHybridSelectionBoundariesStrict` covers the initial hybrid
  selection boundary, including low-bandwidth auto fallback to CELT and forced
  bandwidth overriding a lower max-bandwidth cap.
- The former `cmd_diag` duplicate-`main` build failure is fixed (`toc_check.go`
  moved to `cmd_diag/toccheck`).

The decoder passes the full official Opus test-vector suite and the libopus
reference comparison.

## Known Gaps

- No public multistream, surround, or Ogg Opus container API.
- No public float32 encode/decode API.
- No public `DecodePLC(pcm, frameSize)` API matching the README examples.
- Top-level SILK-only encoder selection exists only for low-bitrate VOIP/voice.
- Top-level hybrid encoder selection exists only for high-bitrate 24/48 kHz
  VOIP/voice; there is no full libopus-equivalent hybrid mode/rate-control
  coverage.
- FEC decode is currently a PLC fallback, not packet FEC extraction.
- Application/signal mode, VBR/CVBR, and some CTL-style constants are not wired
  to full libopus-compatible mode/rate-control behavior.
- Decoder parity is achieved on the official vectors and the libopus reference;
  the open correctness work is now on the encoder side (bit-exact CELT and the
  broader SILK/hybrid encoder paths).

## Practical Use Today

The codebase is a Pure Go Opus implementation with a decoder that passes all 12
official RFC 8251 vectors and the libopus 1.6.1 reference comparison. The
encoder has a CELT path with the current quality pipeline, standard packet
output, and libopus decode cross-checks, plus a narrow low-bitrate SILK-only
speech path, and an initial high-bitrate 24/48 kHz hybrid voice path. It is not
bit-exact with libopus and does not yet provide full SILK/hybrid mode selection.
