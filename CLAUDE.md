# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

Additional live handoff notes from Codex are kept in `.claude/Codex.md`; check that file before continuing CELT oracle/diagnostic work.

## Commands

```bash
# Run all tests
go test ./...

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

No build step is needed — this is a library with no `main` package.

## Architecture

This is a pure Go implementation of the Opus audio codec (RFC 6716), with zero CGO or external dependencies (module: `github.com/darui3018823/opus`).

### Layer structure

```
opus.go / constants.go / errors.go    ← Public API (Encoder/Decoder)
internal/opus_framing.go              ← TOC byte parsing/generation (RFC 6716 §3.1)
internal/celt/                        ← CELT codec (music, MDCT-based) — primary implemented layer
internal/silk/                        ← SILK codec (speech, LPC-based) — scaffolded, incomplete
internal/dsp/                         ← FFT, MDCT, window functions, math utilities
internal/entcode/                     ← Entropy range coder (encode + decode)
internal/resampler/                   ← Polyphase FIR sample rate conversion
```

### Public API (`opus.go`)

- `NewEncoder(sampleRate, channels, application)` → `*Encoder`
- `Encoder.Encode(pcm []int16, frameSize)` / `EncodeFloat(pcm []float64, frameSize)`
- `NewDecoder(sampleRate, channels)` → `*Decoder`
- `Decoder.Decode(data []byte, pcm []int16)` / `DecodeFloat` / `DecodeFEC`

### Strict Phase 1 compliance

Currently only **48 kHz, 20 ms frames, CELT-only** (configs 20/22) are supported. Any other configuration returns `ErrBadArg`. This is intentional — see `IMPLEMENTATION_STATUS.md` for the roadmap to full Opus 1.3.1 compliance.

### Encoding data flow

PCM (int16 or float64) → CELT encoder (MDCT → band processing → PVQ quantization) → range coder → TOC byte prepended → Opus packet

### Decoding data flow

Opus packet → TOC parsed → range decoder → PVQ decoding → IMDCT → PCM output

### Key design notes

- Float64 is used as the primary numeric type; simulated fixed-point is used only in performance-critical sections.
- SILK is scaffolded but not functional in the current strict compliance phase.
- TOC byte encodes: config (upper 5 bits), stereo flag (bit 2), frame count code (lower 2 bits).
