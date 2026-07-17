# CLAUDE.md

This file is the repository entry point for Claude Code and other coding
agents.

## Start here

Read `docs/CURRENT_IMPLEMENTATION.md` before making implementation or
documentation claims. It is the code-derived status snapshot and takes
precedence over older roadmaps, task notes, and README text when they disagree.

Repository-specific operational rules live under `.claude/rules/`. Read every
rule applicable to the work before starting:

- `.claude/rules/index.md` — rule inventory and tracking status; read this
  first.
- `.claude/rules/documentation-rules.md` — status authority, classification,
  naming, tracking, and lifecycle of documents under `.claude/`.
- `.claude/rules/webhook-rules.md` — local notification integration. This file
  and its local configuration are intentionally ignored; consult them only when
  sending a notification.
- `.claude/rules/user-preferences.md` — local user workflow preferences. This
  file is intentionally ignored but applies to normal repository work.

Task briefs, plans, specifications, and historical working notes are organized
under `.claude/`. Use them as task context, not as proof of the current
implementation. The complete directory layout and maintenance rules are in
`.claude/rules/documentation-rules.md`.

## Commands

```bash
# Run all tests. Official-vector and cgo tests need extra data/toolchain;
# see docs/CURRENT_IMPLEMENTATION.md.
go test ./...

# Run the cgo/libopus reference comparison (needs gcc + libopus).
go test -tags opusref -run TestCGORef .

# Run tests with verbose output or run one test.
go test -v ./...
go test -run '^TestNewEncoder$' .

# Run an individual internal package.
go test ./internal/dsp/
go test ./internal/celt/
go test ./internal/silk/
go test ./internal/entcode/
go test ./internal/resampler/

# Coverage and benchmarks.
go test -cover ./...
go test -bench=. ./...

# Format, vet, and build.
go fmt ./...
go vet ./...
go build ./...
```

The repository is primarily a library. Diagnostic commands live under
`cmd_diag/`; for example, the TOC checker is at `cmd_diag/toccheck/main.go`.

## Architecture

This is a pure Go implementation of the Opus audio codec (RFC 6716), with no
runtime CGO dependency in the codec implementation. The module is
`github.com/darui3018823/opus`. The `internal/cgoref` package is an
`//go:build opusref` libopus wrapper used only for reference comparisons; a
`!opusref` stub keeps normal builds CGO-free.

```text
opus.go / constants.go / errors.go  <- single-stream public API
multistream.go / surround.go        <- multistream and surround APIs
projection.go                       <- projection and Ambisonics APIs
repacketizer.go / extensions.go     <- packet transformation APIs
oggopus/                             <- Ogg and Ogg Opus container APIs
internal/opus_framing.go            <- RFC 6716 TOC and framing helpers
internal/celt/                      <- CELT encoder and decoder
internal/silk/                      <- SILK encoder and decoder
internal/dsp/                       <- FFT, MDCT, windows, and math helpers
internal/entcode/                   <- entropy range coder
internal/resampler/                 <- Opus-rate sample-rate conversion
```

The public encoder and decoder are rooted in `opus.go`. The encoder selects a
CELT, SILK-only, or hybrid path and writes RFC 6716 packet framing. The decoder
parses the TOC and routes the packet through the matching codec path before
optional resampling and channel adjustment. For exact supported APIs, test
status, known gaps, and implementation caveats, use
`docs/CURRENT_IMPLEMENTATION.md` rather than duplicating that changing detail
here.
