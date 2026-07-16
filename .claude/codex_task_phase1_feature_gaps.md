# Codex Task: Phase 1 Pure Go Feature Gaps

Source: `.claude/plan_pure_go_completeness_2026-07-16.md` Phase 1.
Status authority: `docs/CURRENT_IMPLEMENTATION.md`.
Integration branch: `codex/feature-gaps`.
Status: complete through required iterations 1-1 to 1-5 on 2026-07-16.

## Iteration protocol

Implement and commit one public feature at a time in this order:

1. Multistream/surround `DecodeFEC`.
2. Multistream/surround `DecodePLC`.
3. Ogg Opus granule-position seek.
4. Ogg Opus chained logical streams.
5. Expert frame-duration encoder control.

For every iteration:

- Preserve existing encoder and decoder packet behavior outside the new API.
- Do not change range-coder bit ordering or the SILK environment gates.
- Add focused unit tests and `opusref` interoperability coverage where libopus
  exposes the corresponding operation.
- Run `go vet ./...`, `go test -count=1 ./...`, and
  `go test -count=1 -tags opusref ./...` from PowerShell.
- Record the implementation, measurements, decision, and reason in
  `.claude/phase1_iteration_log.md`.
- Use Conventional Commits and leave the branch unpushed for review.

## Feature-specific acceptance

- FEC: parse self-delimited multistream packets and route each stream's packet
  to its decoder's existing FEC path; validate Go round trips and libopus
  encode to Go FEC decode under packet loss.
- PLC: call every component decoder's existing PLC path for the requested
  frame size, remap coupled/mono outputs, and guard non-zero output, bounded
  energy decay, and continuity.
- Seek: define explicit seekability requirements, use Ogg granule positions,
  and handle pre-skip, end trim, beginning/end/page boundaries exactly.
- Chaining: continue after EOS with fresh identification/comment headers and
  reset stream-local decoder/granule state; test at least two logical streams.
- Frame duration: expose libopus-equivalent expert frame-duration choices while
  retaining caller-frame validation and packet framing compatibility.

Optional multiplexed demux and arbitrary projection encoder matrices remain
out of scope pending explicit user approval.
