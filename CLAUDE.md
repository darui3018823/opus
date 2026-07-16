# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

For current repository status, read `docs/CURRENT_IMPLEMENTATION.md`. It is the
code-derived snapshot and takes precedence over older roadmap or README claims
when they disagree.

## Documents under `.claude/`

`.claude/` contains task briefs, plans, specifications, and historical working
notes. These documents provide context for a specific piece of work; they are
not the authority for the repository's current implementation status. Before
relying on an older task or plan, compare it with `docs/CURRENT_IMPLEMENTATION.md`
and the current code.

Classify Markdown files by their primary purpose:

```text
.claude/
├── tasks/
│   ├── implementation/  # Bounded feature, API, or bug-fix work
│   ├── investigation/   # Root-cause analysis, experiments, and measurements
│   └── testing/         # Test-harness, fuzzing, and test-robustness work
├── plans/               # Multi-phase roadmaps and sequencing decisions
├── specs/               # Detailed design and implementation contracts
└── memory/
    ├── audits/          # Dated audit snapshots and historical assessments
    ├── handoffs/        # Dated cross-session handoff snapshots
    └── iterations/      # Completed phase/iteration decision logs
```

Follow these rules when adding or maintaining files:

- Put each Markdown file in exactly one category based on its main deliverable.
  If a task includes tests, keep it with the implementation or investigation
  unless the test infrastructure itself is the deliverable.
- Use lowercase kebab-case filenames. Include `YYYY-MM-DD` in dated snapshots;
  do not add `codex_task_`, `claude_`, or similar author/tool prefixes.
- Keep `.claude/`'s root free of Markdown files. Markdown documents under the
  categorized directories are tracked. Tool-owned `settings.local.json` and
  `scheduled_tasks.lock` remain untracked at the root and must not be moved
  into the document hierarchy.
- New or actively revised task briefs should state their objective, status or
  last-updated date, scope, acceptance criteria, and verification commands.
- Use repository-relative links with `/` separators. When moving a document,
  update references to it in the same change; do not leave a duplicate at the
  old path.
- Treat superseded plans, audits, handoffs, and iteration logs as historical
  snapshots. Record supersession in the document instead of rewriting old
  measurements to look current.
- Keep completed task documents when they contain useful decisions or
  reproducer data. Completion state belongs in the document, not in a separate
  `archive/` folder. Delete a document only when its information is duplicated
  elsewhere and no longer useful.

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
go test -run '^TestNewEncoder$' .

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
the command now lives at `cmd_diag/toccheck/main.go`, so
`go build ./...` and `go vet ./...` are clean.

## Architecture

This is a pure Go implementation of the Opus audio codec (RFC 6716), with no
runtime CGO dependency in the codec implementation (module:
`github.com/darui3018823/opus`). The `internal/cgoref` package is a
`//go:build opusref` libopus wrapper used only for golden/reference comparisons
(a `!opusref` stub keeps normal builds CGO-free).

### Layer structure

```
opus.go / constants.go / errors.go    <- Public API (Encoder/Decoder)
internal/opus_framing.go              <- TOC byte parsing/generation (RFC 6716 section 3.1)
internal/celt/                        <- CELT encoder and decoder
internal/silk/                        <- SILK decoder/encoder work, tables, LPC/NLSF/pitch/gain helpers
internal/dsp/                         <- FFT, MDCT/IMDCT, window functions, math utilities
internal/entcode/                     <- Entropy range coder (encode + decode)
internal/resampler/                   <- Opus-rate sample rate conversion
```

### Public API (`opus.go`)

- `NewEncoder(sampleRate, channels, application)` -> `*Encoder`
- `Encoder.Encode(pcm []int16, frameSize)` / `Encode24` / `EncodeFloat` /
  `EncodeFloat32`
- `NewDecoder(sampleRate, channels)` -> `*Decoder`
- `Decoder.Decode(data []byte, pcm []int16)` / `Decode24` / `DecodeFloat` /
  `DecodeFloat32` / `DecodeFEC` / `DecodePLC`
- Multistream, surround, projection/Ambisonics, repacketizer, packet extension,
  and Ogg Opus APIs are available in their package-level files/subpackages.

`DecodeFEC` extracts SILK LBRR for mono/stereo SILK-only and hybrid packets.
`DecodePLC` supports CELT-only, SILK-only, and hybrid streams after a
successful decode of the corresponding mode.

### Current implementation status

#### Decoder (complete)

The top-level decoder accepts the five Opus rates, pre-creates CELT decoders
for bandwidth/frame/channel variants, and pre-creates SILK decoders for
8/12/16 kHz packet rates. CELT configs are routed through the CELT path; SILK
configs through the SILK packet path; hybrid configs (12-15) through the
combined SILK+CELT path. Hybrid SILK+CELT reconstruction is implemented (a
single range decoder runs SILK, the hybrid redundancy flag, then the CELT high
band; the two outputs are resampled and time-domain summed). Hybrid SILK→CELT
redundancy (the trailing 5 ms redundant CELT frame) is also implemented.

The official RFC 8251 vector suite is at **12/12 PASS** (all vectors RMSE <
0.001). `go build ./...`, `go vet ./...`, and `go test ./...` are all green.
The cgo/libopus reference `TestCGORef` (`go test -tags opusref`) also passes
all 12 vectors against libopus 1.6.1 (overall RMSE < 0.001). Note:
official-vector and `.bit`-based diagnostic tests `t.Skip` when `testdata/`
(git-ignored) is absent.

#### Encoder

The encoder implements the current CELT quality pipeline and limited public
SILK-only and hybrid speech paths. It emits standard RFC 6716 Opus packets that
libopus 1.6.1 decodes correctly. It is not bit-exact with libopus and does not
yet provide full libopus-equivalent SILK/hybrid mode selection or rate control.

**Implemented features:**

- Input: 8/12/16/24/48 kHz mono or stereo; non-48 kHz is resampled internally.
- **Bandwidth selection**: NB/WB/SWB/FB chosen from Nyquist ceiling, bitrate
  heuristic, and signal-driven FFT detection (`bandwidth_detect.go`). VOIP
  application prefers narrower tiers; `SetBandwidth`/`SetMaxBandwidth` for
  manual override.
- **Rate control**: CBR / VBR / CVBR (`SetVBR`, `SetVBRConstraint`).
- **Packetization**: 2.5/5/10 ms CELT packets and 20 ms multiples through
  120 ms (RFC 6716 §3.2 codes 0–3), including code-3 padding.
  `SetPacketPadding` for explicit padding.
- **Silence detection + DTX**: near-silent frames emit a minimal 2–3 byte
  packet; `SetDTX` enables discontinuous transmission.
- **Transient detection**: time-domain HPF masking (`transientAnalysis`) selects
  short blocks (8×120-sample MDCTs) to limit pre-echo. `patchTransientDecision`
  is a complementary band-energy fallback that promotes frames the time-domain
  detector misses.
- **tf_analysis**: per-band transform resolution RDO via 2-pass Viterbi.
- **dynalloc/alloc_trim/spread**: masking-follower dynamic allocation boost,
  spectral-tilt trim, spreading decision with recursive hysteresis.
- **Stereo decisions**: `stereoAnalysis` (dual vs joint M/S), intensity stereo
  with hysteresis (`hysteresisDecision`).
- **Anti-collapse**: consecutive-transient bit prevents spectral collapse.
- **Application coupling**: `SignalType` (Voice/Music) wired from `Application`;
  VOIP→voice lowers the patch-transient threshold for speech onsets.
- **SILK-only speech**: limited mono/stereo low-bitrate voice path for
  8/12/16 kHz input and downsampled 24/48 kHz WB voice.
- **Hybrid speech**: initial 24/48 kHz high-bitrate voice path with SILK low
  band plus CELT high band.
- **In-band FEC/LBRR**: SILK-only and hybrid encode/decode support for mono and
  stereo when `SetInbandFEC(true)` and non-zero packet-loss percentage are set.

**SNR (delay-aligned, self-decode, 64 kbps):**
sine 440 Hz ≈ 48 dB · sine 1 kHz ≈ 47 dB · sine 4 kHz ≈ 39 dB · sine 1 kHz stereo ≈ 43 dB.

**Known gaps:** SILK/hybrid encode is intentionally narrow and quality/rate
control is still below libopus parity.

### Encoding data flow

PCM (int16, int24-in-int32, float64, or float32) -> mode selection -> CELT,
SILK-only, or hybrid encoder path -> range coder/framing -> TOC byte prepended
-> Opus packet

### Decoding data flow

Opus packet -> TOC parsed -> CELT or SILK/hybrid packet path -> range decoder
and codec reconstruction -> optional resampler/channel adjustment -> PCM output

### Key design notes

- Float64 is used as the primary numeric type; simulated fixed-point is used only in performance-critical sections.
- TOC byte encodes: config (upper 5 bits), stereo flag (bit 2), frame count code (lower 2 bits).
- The decoder is RFC 8251 compliant (12/12 vectors PASS). The encoder emits
  valid Opus packets but is not bit-exact with libopus.
