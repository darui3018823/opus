# CLAUDE.md

This file is the single source of truth for repository-specific instructions
used by Claude Code, Junie, and other coding agents. `AGENTS.md` is only an entry
point that loads this file and must not duplicate its rules.

## Start here

Read `docs/CURRENT_IMPLEMENTATION.md` before making implementation or
documentation claims. It is the code-derived status snapshot and takes
precedence over older roadmaps, task notes, and README text when they disagree.

All contributions, formatting, and commit messages must strictly comply with
`CONTRIBUTING.md` and repository rules. Never bypass repository standards.

Repository-specific operational rules live under `.claude/rules/`. Read every
rule applicable to the work before starting:

- `.claude/rules/index.md` — rule inventory and tracking status; read this
  first.
- `.claude/rules/documentation-rules.md` — status authority, classification,
  naming, tracking, and lifecycle of documents under `.claude/`.
- `.claude/rules/commit-rules.md` — strict Conventional Commits prefix enforcement.
- `.claude/rules/webhook-rules.md` — local notification integration. This file
  and its local configuration are intentionally ignored; consult them only when
  sending a notification.
- `.claude/rules/user-preferences.md` — local user workflow preferences. This
  file is intentionally ignored but applies to normal repository work.

Task briefs, plans, specifications, and historical working notes are organized
under `.claude/`. Use them as task context, not as proof of the current
implementation. The complete directory layout and maintenance rules are in
`.claude/rules/documentation-rules.md`.

## Working rules

- **Strict adherence to CONTRIBUTING.md**: Always run `go fmt ./...` and `go vet ./...` before completing tasks. Ensure all relevant unit tests pass.
- **Pure Go boundary**: Preserve pure Go runtime execution. Keep `internal/cgoref` libopus dependencies strictly behind the `//go:build opusref` build tag; default builds must remain completely CGO-free.
- **Evidence-based verification**: Never claim bit-exactness, parity, or audio convergence based solely on code inspection. Verify claims with explicit test execution (`go test -tags opusref ...`, official RFC 6716 test vectors, or package tests).
- **Narrow and clean changes**: Keep edits focused on the task at hand. Do not modify or revert unrelated working-tree edits.
- **Pre-finish inspection**: Inspect `git diff` and `git status` before declaring a task complete, and ensure no temporary debugging code or credentials are left behind.

## Commits

All commits in this repository must strictly adhere to [Conventional Commits](https://www.conventionalcommits.org/) with an explicit prefix (e.g. `<type>(<scope>): <subject>` or `<type>: <subject>`).

- **Prefix is mandatory**: Commits without an approved type prefix are strictly forbidden. Unprefixed commits will be rejected by the repository's Git `commit-msg` hook.
- **Allowed prefixes**: `feat`, `fix`, `test`, `docs`, `refactor`, `perf`, `build`, `ci`, `chore`, `style`, `revert`.
- **Common scopes**: `celt`, `silk`, `dsp`, `packet`, `api`, `decoder`, `encoder`, `opusref`, `oggopus`, `multistream`, `surround`, `resampler`, `entcode`.
- See `.claude/rules/commit-rules.md` and `CONTRIBUTING.md` for full requirements and scope mappings.

Examples:
- `fix(celt): separate anti-collapse energy history`
- `test(opusref): trace mono scalar CELT stages`
- `docs: update CELT handoff notes`
- `chore: update inspection profile`

## Commands & Useful checks

```bash
# Quick working-tree and diff check
git status --short
git diff --check

# Format, vet, and build (run before submitting changes)
go fmt ./...
go vet ./...
go build ./...

# Run all tests (official-vector and cgo tests need extra data/toolchain; see docs/CURRENT_IMPLEMENTATION.md)
go test ./...

# Run the cgo/libopus reference comparison (needs gcc + libopus)
go test -tags opusref -run TestCGORef .

# Run an individual internal package
go test ./internal/dsp/
go test ./internal/celt/
go test ./internal/silk/
go test ./internal/entcode/
go test ./internal/resampler/

# Run tests with verbose output or run a specific test
go test -v ./...
go test -run '^TestNewEncoder$' .

# Coverage and benchmarks
go test -cover ./...
go test -bench=. ./...
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
