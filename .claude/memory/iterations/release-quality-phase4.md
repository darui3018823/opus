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
