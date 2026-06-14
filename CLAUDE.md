# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

Additional live handoff notes from Codex are kept in `.claude/Codex.md`; check that file before continuing CELT oracle/diagnostic work.

For current repository status, read `docs/CURRENT_IMPLEMENTATION.md`. It is the
code-derived snapshot and takes precedence over older roadmap or README claims
when they disagree.

## Commands

```bash
# Run all tests. Library packages pass; official-vector and cgo tests need
# extra data/toolchain (see docs/CURRENT_IMPLEMENTATION.md).
go test ./...

# Run the cgo/libopus reference comparison (needs gcc + libopus; PowerShell on Windows)
go test -tags opusref -run TestCGORef .

# Run tests with verbose output
go test -v ./...

# Run a single package's tests
go test ./internal/dsp/
go test ./internal/celt/
go test ./internal/silk/
go test ./internal/entcode/
go test ./internal/resampler/

# Run a single test by name
go test -run TestEncoderCreation ./...

# Run with coverage
go test -cover ./...

# Run benchmarks
go test -bench=. ./...

# Format and vet
go fmt ./...
go vet ./...
```

This repository is primarily a library, but it also contains diagnostic command
packages under `cmd_diag*`. The former duplicate-`main` build failure is fixed:
`toc_check.go` now lives in its own command package `cmd_diag/toccheck`, so
`go build ./...` and `go vet ./...` are clean.

## Architecture

This is a pure Go implementation of the Opus audio codec (RFC 6716), with no
runtime CGO dependency in the codec implementation (module:
`github.com/darui3018823/opus`). The `internal/cgoref` package is a
`//go:build opusref` libopus wrapper used only for golden/reference comparisons
(a `!opusref` stub keeps the package empty for normal, CGO-free builds).

### Layer structure

```
opus.go / constants.go / errors.go    <- Public API (Encoder/Decoder)
internal/opus_framing.go              <- TOC byte parsing/generation (RFC 6716 section 3.1)
internal/celt/                        <- CELT codec work: decoder parity path plus simplified encoder
internal/silk/                        <- SILK decoder/encoder work, tables, LPC/NLSF/pitch/gain helpers
internal/dsp/                         <- FFT, MDCT/IMDCT, window functions, math utilities
internal/entcode/                     <- Entropy range coder (encode + decode)
internal/resampler/                   <- Opus-rate sample rate conversion
```

### Public API (`opus.go`)

- `NewEncoder(sampleRate, channels, application)` -> `*Encoder`
- `Encoder.Encode(pcm []int16, frameSize)` / `EncodeFloat(pcm []float64, frameSize)`
- `NewDecoder(sampleRate, channels)` -> `*Decoder`
- `Decoder.Decode(data []byte, pcm []int16)` / `DecodeFloat` / `DecodeFEC`

There is no public `EncodeFloat32`, `DecodeFloat32`, or `DecodePLC(pcm,
frameSize)` API at the current snapshot. Use `EncodeFloat`/`DecodeFloat` for
float64 data; `DecodeFEC` currently falls back to CELT PLC behavior.

### Current implementation status

The top-level encoder accepts 8/12/16/24/48 kHz mono or stereo input, resamples
non-48 kHz input to 48 kHz, and emits CELT-only fullband 20 ms packets. The
`application` and `SetVBR` values are stored but do not currently drive full
libopus-compatible mode selection or packetization.

The top-level decoder accepts the five Opus rates, pre-creates CELT decoders
for bandwidth/frame/channel variants, and pre-creates SILK decoders for
8/12/16 kHz packet rates. CELT configs are routed through the CELT path; SILK
configs through the SILK packet path; hybrid configs (12-15) through the
combined SILK+CELT path. Hybrid SILK+CELT reconstruction is implemented (a
single range decoder runs SILK, the hybrid redundancy flag, then the CELT high
band; the two outputs are resampled and time-domain summed). Hybrid SILK->CELT
**redundancy** (the trailing 5 ms redundant CELT frame, celt_to_silk=0) is also
implemented: the main CELT layer decodes from a length reduced by
redundancy_bytes, then a reset CELT decoder decodes the redundant frame from the
packet tail, the last 2.5 ms is crossfaded, and the redundant frame's state
seeds the next CELT-only packet. See `docs/CURRENT_IMPLEMENTATION.md` and the
`project-hybrid-architecture` / `project-tv10-celt-stereo` memories.

The official RFC 8251 vector suite is at **12/12 PASS** (all vectors RMSE <
0.001). testvector01 — long mislabelled "heavy SILK" — is in fact CELT-only
fullband stereo (configs 28-31); its residual was a code-3 multi-byte padding
parse bug in `splitOpusFrames` (only one 0xFF continuation was handled), which
fed the CELT decoder the wrong bytes on packets with a `41 ff ff ..` padding
run, causing full-scale clipping. Fixed to loop per RFC 6716 §3.2.5 (each 0xFF
count byte = 254 padding-data bytes + continuation). `go build ./...`,
`go vet ./...`, and `go test ./...` are all green (the `cmd_diag` duplicate-main
build failure is fixed). The cgo/libopus reference comparison `TestCGORef`
(`go test -tags opusref`) also passes all 12 vectors against libopus 1.6.1
(overall RMSE < 0.001). Note: official-vector and `.bit`-based diagnostic tests
`t.Skip` when `testdata/` (git-ignored) is absent.

### Encoding data flow

PCM (int16 or float64) -> optional resampler to 48 kHz -> CELT encoder (MDCT,
band processing, PVQ quantization) -> range coder -> TOC byte prepended ->
Opus packet

### Decoding data flow

Opus packet -> TOC parsed -> CELT or SILK/hybrid packet path -> range decoder
and codec reconstruction -> optional resampler/channel adjustment -> PCM output

### Key design notes

- Float64 is used as the primary numeric type; simulated fixed-point is used only in performance-critical sections.
- TOC byte encodes: config (upper 5 bits), stereo flag (bit 2), frame count code (lower 2 bits).
- Official Opus bit-exact compliance is still in progress. Do not claim all
  official vectors pass until the failing tests are fixed.
