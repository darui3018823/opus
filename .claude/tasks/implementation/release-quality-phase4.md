# Phase 4 Release Quality and Developer Experience

Last updated: 2026-07-17

Source: `.claude/plans/pure-go-completeness-2026-07-16.md` Phase 4.

Status authority: `docs/CURRENT_IMPLEMENTATION.md`.

Integration branch: `codex/phase4-release-quality`.

Status: iteration 4-1 qualified; iteration 4-2 is next.

## Objective

Improve the documentation, examples, CI coverage, and release process of the
already released v1 module without changing codec behavior or breaking the
public API.

The repository's current version source and latest tag are both `1.2.0` /
`v1.2.0`. This phase is post-v1.2 release-quality work, not preparation for an
initial v1.0 release.

## Iteration Protocol

Complete the following concerns in order, with one concern per iteration:

1. `4-1`: godoc and executable examples.
2. `4-2`: English and Japanese README refresh.
3. `4-3`: CI operating-system and architecture coverage.
4. `4-4`: release and semver hygiene.

For each iteration:

- Start from the Phase 4 integration branch and keep the change limited to the
  named concern.
- Treat `docs/CURRENT_IMPLEMENTATION.md` and the code as current-state
  authority; do not copy stale feature claims from historical roadmaps.
- Preserve public API compatibility, encoded packet bytes, decoded PCM, and
  range-coder behavior.
- Add focused tests or mechanical checks for changed behavior and documents
  wherever practical.
- Use Conventional Commits and record the result, verification, and decision
  in `.claude/memory/iterations/release-quality-phase4.md`.
- Qualify the iteration before integrating it into
  `codex/phase4-release-quality`.

## 4-1: godoc and Executable Examples

Audit every exported identifier in the public `opus` and `oggopus` packages.
Comments must describe usable behavior, units, ownership/lifetime rules,
statefulness, errors, and important constraints where relevant. Do not add
comments that merely repeat identifier names.

Deliverables:

- Complete and consistent package documentation for `opus` and `oggopus`.
- Useful Go doc comments for every exported public identifier.
- Four deterministic, executable examples:
  - encode;
  - decode;
  - Ogg Opus writer-to-reader round trip;
  - multistream encode/decode.
- Examples must use only generated in-memory data: no network, external audio
  files, CGO, or non-deterministic output.

Acceptance:

- `go doc -all .` and `go doc -all ./oggopus` expose no undocumented public
  identifier and no stale behavior claim.
- The four examples compile and run as part of the normal test suite.
- Examples demonstrate error handling and valid frame/buffer sizing without
  presenting test-only helpers as public usage.

## 4-2: README Refresh

Refresh `README.md` and `README_ja.md` together. Keep their structure and
claims equivalent while allowing idiomatic wording in each language.

Required content:

- concise installation and minimal encode/decode usage;
- a code-derived feature/support table;
- explicit current limitations and deliberately out-of-scope features;
- 12/12 RFC 8251 vector status and the role of optional `opusref` checks;
- fuzz/input-safety guarantees and their limits;
- concurrency, state ownership, slice lifetime, and reset contracts;
- links to detailed API, CTL parity, performance, security, and contribution
  documents.

Acceptance:

- Every implementation claim agrees with `docs/CURRENT_IMPLEMENTATION.md` and
  the public API.
- English/Japanese headings, support tables, examples, and internal links stay
  in semantic parity.
- Repository-relative links and example commands are validated locally.

## 4-3: CI Matrix

Audit the existing workflows before changing them. Extend normal validation
across Linux, macOS, and Windows and across amd64/arm64 where current
GitHub-hosted runners can execute the suite natively. Use a documented
cross-build check for combinations that cannot run natively rather than
claiming unexecuted test coverage.

Requirements:

- Keep generated-file drift, `go vet`, normal tests, and RFC-vector coverage
  explicit.
- Keep the CGO/libopus `opusref` job on its supported Ubuntu environment.
- Preserve or improve workflow concurrency, least-privilege permissions,
  artifact handling, and failure visibility.
- Avoid duplicate expensive work when one matrix job already proves the same
  invariant.
- Confirm runner labels and action versions against current official GitHub
  documentation during implementation.

Acceptance:

- The resulting matrix clearly distinguishes native test coverage from
  cross-build-only coverage.
- Workflow YAML is valid, all locally reproducible commands pass, and the PR
  checks complete successfully on every configured job.

## 4-4: Release and Semver Hygiene

Audit the release path from `VERSION` through generated `version_gen.go`,
tests, supported-version documentation, release notes, and tag/release
instructions.

Requirements:

- Use `v1.2.0` as the current released baseline.
- Verify generated version drift remains mechanically rejected.
- Review exported error values and API naming for inconsistencies, but do not
  make breaking changes within v1. Record any justified breaking proposal as
  a separate v2 candidate.
- Add or refresh a repeatable checklist for preparing the next release,
  including tests, generated files, documentation, version selection, tag,
  release notes, and rollback/recovery steps.
- Do not assume whether the next version is a patch or minor release; base that
  decision on the accumulated changes.

Version bumps, tags, pushes, and GitHub Release publication are out of scope
unless the user explicitly approves them after reviewing the Phase 4 result.

## Phase Acceptance Criteria

- All four iterations are qualified and recorded.
- Public documentation and examples accurately describe the shipped v1.2-era
  implementation.
- Normal CI covers the practical OS/architecture matrix and labels any
  cross-build-only cells honestly.
- The repository has a repeatable post-v1.2 release checklist without an
  unapproved version bump or release action.
- Standard gates pass from PowerShell:

```text
go generate ./...
git diff --exit-code
go vet ./...
go test -count=1 ./...
go test -count=1 -tags opusref ./...
```

## Out of Scope

- Codec quality, rate-control, performance, or range-coder changes.
- Optional Phase 1-6 features.
- DRED/QEXT codec implementation.
- Breaking public API changes in v1.
- Choosing or publishing the next release without explicit user approval.
