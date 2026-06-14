# Current Implementation Snapshot

Last reviewed: 2026-06-14

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
- `(*Encoder).SetBitrate(bitrate int) error`
- `(*Encoder).SetComplexity(complexity int) error`
- `(*Encoder).SetVBR(vbr bool)`
- `(*Encoder).SetVBRConstraint(constrained bool)`
- `(*Encoder).SetDTX(enabled bool)` / `(*Encoder).DTX() bool`
- `(*Encoder).SetPacketPadding(n int)`
- `(*Encoder).SetApplication(application Application)`
- `(*Encoder).Reset() error`

Accepted sample rates are `8000`, `12000`, `16000`, `24000`, and `48000`.
Accepted channel counts are mono and stereo.

The top-level encoder always creates an internal CELT encoder at 48 kHz and
uses a 20 ms internal CELT frame (`960` samples per channel). Non-48 kHz input
is resampled to 48 kHz before CELT encoding. The emitted TOC byte is generated
as CELT-only fullband 20 ms.

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
- Not yet done (future quality work): real `tf_analysis` (per-band tf_res RDO is
  still flat 0), the complexity≥8 second long-block MDCT for `bandLogE2`, and
  intensity/dual-stereo decisions.

Current encoder limitations:

- `application` is stored but does not currently drive SILK/CELT/hybrid mode
  selection.
- `SetVBR` stores the setting but does not currently alter the CELT encoder
  packetization path.
- `EncodeFloat` uses `float64`; there is no public `EncodeFloat32` method.
- The public encoder does not expose a SILK-only or hybrid encoder path.
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
`0`, `1`, `2`, and `3` are split into per-frame payloads. SILK packet splitting
has separate handling for shared SILK range streams.

Current decoder limitations:

- `DecodeFEC` currently uses CELT packet-loss concealment from the fullband
  20 ms decoder and does not decode FEC data from the supplied packet.
- There is no public `DecodeFloat32` method.
- There is no public `DecodePLC(pcm, frameSize)` method; CELT PLC exists
  internally and is reached through `DecodeFEC`.
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
`ec_tell`'s `nbits_total`). Round-trip guards: `TestEncodeBitsRawRoundtrip` and
`TestEncodeUintLargeFtRoundtrip` (the `ec_enc_uint` ftb>UintBits split path).

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

The CELT encoder performs MDCT analysis, band energy computation, coarse energy
coding, bit allocation, and PVQ encoding, but it is still a simplified path and
is not documented as bit-exact.

### `internal/silk`

The SILK package contains:

- SILK decoder state for 8/12/16/24 kHz construction
- 10 ms and 20 ms decoder construction
- NLSF codebooks and NLSF-to-LPC conversion
- gain, pitch, LTP, pulse, shell, and stereo helper tables
- LPC, pitch, NLSF, gain, and VAD helpers
- a simplified SILK encoder used by internal tests, not by the top-level Opus
  encoder

The public Opus decoder instantiates SILK decoders for 8/12/16 kHz packet
rates. Hybrid configs (12-15) are fully reconstructed in `opus.go`: a single
range decoder runs SILK, the hybrid redundancy flag, then the CELT high band,
and the two outputs are resampled and time-domain summed. The hybrid SILK->CELT
redundancy frame (celt_to_silk=0) is also handled.

## Test Status

Command checked:

```bash
go test ./...
```

Result on 2026-06-14: passing (`go build ./...`, `go vet ./...`, and
`go test ./...` all exit 0).

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
- The cgo/libopus reference comparison runs under `go test -tags opusref` and
  needs a C toolchain plus libopus (reported `libopus 1.6.1` locally); it passes
  all 12 vectors. Normal builds use a `!opusref` stub so the codec stays
  CGO-free.
- The former `cmd_diag` duplicate-`main` build failure is fixed (`toc_check.go`
  moved to `cmd_diag/toccheck`).

The decoder passes the full official Opus test-vector suite and the libopus
reference comparison.

## Known Gaps

- No public multistream, surround, or Ogg Opus container API.
- No public float32 encode/decode API.
- No public `DecodePLC(pcm, frameSize)` API matching the README examples.
- No top-level SILK-only encoder selection.
- No top-level hybrid encoder.
- FEC decode is currently a PLC fallback, not packet FEC extraction.
- Application mode, VBR, and some CTL-style constants are not wired to full
  libopus-compatible behavior.
- Decoder parity is achieved on the official vectors and the libopus reference;
  the open correctness work is now on the encoder side (bit-exact CELT and the
  SILK/hybrid encoder paths).

## Practical Use Today

The codebase is a Pure Go Opus implementation with a decoder that passes all 12
official RFC 8251 vectors and the libopus 1.6.1 reference comparison. The
encoder is still a simplified CELT-only path and is not yet bit-exact, so
encode-side compatibility claims remain in progress.
