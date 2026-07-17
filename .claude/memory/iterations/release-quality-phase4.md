# Phase 4 Iteration Log

Integration branch: `codex/phase4-release-quality`.

## Iteration 4-1: godoc and executable examples (Qualified)

### Implemented locally

- Expanded the `opus` and `oggopus` package documentation with state ownership,
  concurrency, slice lifetime, Ogg chaining, seeking, and CGO-scope contracts.
- Added usable comments for every exported declaration and public struct field
  in both packages, including units, buffer sizing, error conditions, and
  important constraints where relevant.
- Added `TestExportedAPIDocumented` to reject future exported declarations that
  have no doc comment.
- Added four deterministic public-API examples:
  - single-stream encode;
  - single-stream decode;
  - multistream encode/decode;
  - Ogg Opus Writer-to-Reader round trip.
- Corrected two code-derived status claims found during the audit:
  - automatic chained logical-stream continuation belongs to `oggopus.Reader`;
    each `Writer` emits one logical stream;
  - TOC/configuration packet helpers validate framing and frame byte limits but
    only the sample-rate duration helpers enforce the 120 ms packet limit.

Commits:

- `51320c2` `test: enforce exported API documentation`
- `23eb64a` `docs: complete public API comments`
- `783c493` `docs: add executable codec examples`
- `a28d588` `docs: align API claims with runtime behavior`
- `c1fbd31` `docs: clarify public API contracts`

### Qualification observations

The focused documentation and example checks passed:

```text
go doc -all .
go doc -all ./oggopus
go test -count=1 -run '^(TestExportedAPIDocumented|Example)' . ./oggopus
```

The Phase 4 standard gates passed from PowerShell:

```text
go generate ./...
git diff --exit-code
go vet ./...
go test -count=1 ./...
go test -count=1 -tags opusref ./...
```

The normal and `opusref` package suites completed without failure. No codec,
packet, range-coder, or public API behavior was changed.

### Decision

Adopted. Iteration 4-1 meets its acceptance criteria and is ready for Phase 4-2
(English and Japanese README refresh).

## Iteration 4-2: English and Japanese README refresh (Qualified)

### Implemented locally

- Replaced the stale, hand-maintained API dumps with matching English and
  Japanese structures centered on a code-derived support matrix.
- Added a copyable in-memory encode/decode example with correct interleaved
  buffer sizing and error handling.
- Documented the 12/12 RFC 8251 result and separated normal conformance tests
  from optional CGO/libopus `opusref` comparisons.
- Added explicit state ownership, concurrency, slice lifetime, child-stream,
  and codec `Reset` contracts.
- Added the bounded malformed-input/no-panic guarantee, current fuzz coverage,
  and its CPU, memory, quality, and denial-of-service limits.
- Listed deliberate limitations and out-of-scope work without overstating
  libopus encoder parity.
- Added direct links to root and Ogg API documentation, CTL parity, performance,
  current status, security reporting, and contribution guidance.

Commits:

- `8bed349` `docs: refresh English and Japanese READMEs`
- `332aba9` `docs: tighten README parity and contracts`

### Qualification observations

- Every repository-relative Markdown link in both READMEs resolved locally.
- English and Japanese headings and support-table row counts matched.
- The documented commands and linked files were checked against the current
  repository and `docs/CURRENT_IMPLEMENTATION.md`.
- The standard PowerShell gates passed:

```text
go generate ./...
git diff --exit-code
go vet ./...
go test -count=1 ./...
go test -count=1 -tags opusref ./...
```

### Decision

Adopted. Iteration 4-2 meets its acceptance criteria and is ready for Phase 4-3
(CI operating-system and architecture coverage).

## Iteration 4-3: CI operating-system and architecture coverage (Locally qualified)

### Implemented locally

- Expanded normal tests to six native GitHub-hosted cells:
  - Linux amd64 and arm64;
  - macOS Intel/amd64 and Apple Silicon/arm64;
  - Windows amd64 and Windows 11 arm64 (public preview).
- Split generated-file drift and `go vet` into one Ubuntu static-analysis job.
- Kept the downloaded 12/12 RFC 8251 vector run explicit in one Ubuntu job
  instead of repeating the download across the native matrix.
- Kept CGO/libopus reference checks in the existing Ubuntu-only `opusref`
  workflow.
- Corrected every workflow from the nonexistent `actions/checkout@v7` reference
  to the current `actions/checkout@v6`. Retained current
  `actions/setup-go@v6` and `actions/upload-artifact@v7` references.
- Updated both READMEs to distinguish native test coverage and the Windows
  arm64 preview status.

Commit:

- `d5d34a2` `ci: expand native OS and architecture coverage`

### Qualification observations

- GitHub's current hosted-runner reference was checked for all six labels.
- Official action repositories were checked for current major versions.
- `actionlint` accepted every workflow:

```text
go run github.com/rhysd/actionlint/cmd/actionlint@latest -color
```

- Supplemental `CGO_ENABLED=0 go build ./...` checks passed for
  `linux/{amd64,arm64}`, `darwin/{amd64,arm64}`, and
  `windows/{amd64,arm64}`. These local builds are not presented as test
  coverage; the workflow cells are explicitly native tests.
- The standard PowerShell gates passed:

```text
go generate ./...
git diff --exit-code
go vet ./...
go test -count=1 ./...
go test -count=1 -tags opusref ./...
```

### Decision

Adopted for local integration. The workflow syntax, local gates, and all target
cross-builds pass. Final qualification still requires the configured hosted
jobs to run on a pushed PR; no push or PR was performed without user approval.
Iteration 4-4 may proceed independently.

## Iteration 4-4: release and semver hygiene (Qualified)

### Implemented locally

- Audited the complete release path from `VERSION` and the generator through
  generated constants, tests, support policy, existing tags, and publication
  instructions. Confirmed `VERSION`, `version_gen.go`, and the latest tag all
  use the `1.2.0` / `v1.2.0` baseline.
- Strengthened `TestVersionMetadata` so normal tests compare the generated
  public version with the repository `VERSION` source, in addition to checking
  its numeric components.
- Made the generator accept only canonical `major.minor.patch` values and added
  focused tests for malformed, prefixed, prerelease, signed, empty, and
  leading-zero forms.
- Added `docs/RELEASE_CHECKLIST.md`, covering accumulated-diff SemVer selection,
  version generation, documentation and release-note preparation, local and
  hosted gates, explicit approval, annotated tags, module-proxy smoke testing,
  and recovery before and after publication.
- Recorded that the audited post-`v1.2.0` diff contains backward-compatible
  public API additions and would therefore be minor-release material if it
  remains in the eventual candidate. No next version was selected.
- Added `docs/V2_API_CANDIDATES.md` for breaking proposals found during the
  naming and sentinel-error audit. The v1 API and error identities were not
  broken.
- Clarified three Ogg sentinel comments without changing their identities or
  runtime behavior, including retaining the unreachable-in-normal-iteration
  `ErrAfterEOS` sentinel as deprecated for v1 compatibility.
- Updated the security table to support `main` and the current stable `v1.2.x`
  line, and linked the release/v2 documents from both READMEs and the current
  implementation snapshot.

Commits:

- `d5644a2` `test: harden release version validation`
- `31aea30` `docs: define release and v2 compatibility policy`

### Qualification observations

- `VERSION`, `version_gen.go`, and `v1.2.0` remained unchanged; no version bump,
  tag, push, PR, or GitHub Release was performed.
- Existing tags were found to mix lightweight and annotated forms. The new
  checklist leaves historical tags untouched and standardizes future tags as
  annotated, with an explicit signing/provenance decision before creation.
- Repository-relative links in the two READMEs and changed release/security
  documents resolved, and English/Japanese README heading parity remained
  intact.
- `actionlint`, the four executable examples, the exported-documentation
  guard, and an explicit 12/12 RFC 8251 vector run passed.
- The release-oriented race gate passed:

```text
go test -race -count=1 ./...
```

- The Phase 4 standard PowerShell gates passed:

```text
go generate ./...
git diff --exit-code
go vet ./...
go test -count=1 ./...
go test -count=1 -tags opusref ./...
```

### Decision

Adopted. Iteration 4-4 meets its acceptance criteria. Phase 4 implementation is
locally complete. Iteration 4-3 remains locally qualified until the configured
native hosted jobs run on a pushed PR; release version selection and every
publication action still require explicit user approval.
