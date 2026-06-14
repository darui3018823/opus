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
- `(*Encoder).SetApplication(application Application)`
- `(*Encoder).Reset() error`

Accepted sample rates are `8000`, `12000`, `16000`, `24000`, and `48000`.
Accepted channel counts are mono and stereo.

The top-level encoder always creates an internal CELT encoder at 48 kHz and
uses a 20 ms internal CELT frame (`960` samples per channel). Non-48 kHz input
is resampled to 48 kHz before CELT encoding. The emitted TOC byte is generated
as CELT-only fullband 20 ms.

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
  separate cgo/libopus reference RMSE comparison test still fails.

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

Important caveat: `EncodeBits` currently writes uniform bits through the range
coder rather than through a true end-of-packet raw-bit buffer. Decoder-side raw
bit behavior is closer to libopus than encoder-side raw-bit behavior.

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
rates. Hybrid configs are routed through the SILK packet path in `opus.go`, but
hybrid CELT high-band composition is not complete.

## Test Status

Command checked:

```bash
go test ./...
```

Result on 2026-06-11: failing.

Passing package-level tests in that run:

- `internal/celt`
- `internal/dsp`
- `internal/entcode`
- `internal/resampler`
- `internal/testing`

Official-vector status (update 2026-06-14): **all 12 RFC 8251 vectors PASS**
with RMSE < 0.001 (`TestOfficialVectors`). testvector01 — previously the last
failure and long mislabelled "heavy SILK" — is in fact CELT-only fullband
stereo; it was fixed by correcting the code-3 multi-byte padding parse in
`splitOpusFrames` (RFC 6716 §3.2.5: each 0xFF count byte = 254 padding-data
bytes plus a continuation).

Remaining non-green items in `go test ./...`:

- Root package cgo/libopus reference comparison fails RMSE thresholds. The local
  run reported `libopus 1.6.1` from the cgo reference helper.
- `cmd_diag` does not build because both `cmd_diag/main.go` and
  `cmd_diag/toc_check.go` define `main`.

The decoder now passes the full official Opus test-vector suite; the open items
above are the cgo reference comparison and the `cmd_diag` build, not decoder
correctness on the official vectors.

## Known Gaps

- No public multistream, surround, or Ogg Opus container API.
- No public float32 encode/decode API.
- No public `DecodePLC(pcm, frameSize)` API matching the README examples.
- No top-level SILK-only encoder selection.
- No top-level hybrid encoder.
- Hybrid decode routing exists, but complete SILK+CELT hybrid reconstruction is
  not finished.
- FEC decode is currently a PLC fallback, not packet FEC extraction.
- Application mode, VBR, and some CTL-style constants are not wired to full
  libopus-compatible behavior.
- Official vector and libopus reference parity remain open correctness work.

## Practical Use Today

The codebase is best treated as an in-progress Pure Go Opus implementation with
substantial CELT, SILK, range-coder, DSP, and resampler work already present.
For production use or compatibility claims, first close the failing vector,
oracle, and reference-comparison tests.
