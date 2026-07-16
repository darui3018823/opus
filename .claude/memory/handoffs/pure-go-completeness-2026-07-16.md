# Pure Go Completeness Handoff - 2026-07-16

> **Historical handoff.** The branches described below were subsequently
> merged into `main` by PR #22 (`60cb602`) on 2026-07-17. Current status is in
> `docs/CURRENT_IMPLEMENTATION.md` and the successor plan under `.claude/plans/`.

## Branches

- `codex/d2-mode-hysteresis`: Phase 0 complete and unpushed. Iteration 2 was
  byte-identical to the accepted baseline; closeout commits are `0f17758` and
  `d1cb315`.
- `codex/feature-gaps`: Phase 1 required iterations 1-1 through 1-5 complete,
  independently reviewed, fully tested, and unpushed. Final documentation
  commit is `39fbaa9`.
- `codex/robustness`: current branch, stacked on `codex/feature-gaps`. Phase 2
  task/log checkpoint is `07d92b6`; Phase 2-1 through Phase 2-3 are now
  qualified and adopted into nightly/manual fuzz CI as documented in
  `phase2_iteration_log.md`.

## Phase 1 Result

Implemented and committed:

- Multistream/surround FEC decode, including CELT PLC fallback and libopus
  interoperability.
- Multistream/surround PLC decode with mapping and continuity guards.
- Ogg Opus packet timing, pre-skip/end-trim metadata, and granule-position
  `SeekPCM` with 80 ms pre-roll.
- Automatic chained logical-stream reading with per-link metadata and
  current-link seeking.
- Expert frame-duration control for 2.5 through 120 ms on single-stream,
  forced-mono, multistream, surround, and projection encoders, cross-checked
  against libopus 1.6.1.

Independent final review fixes:

- `acd4c2b`: reject divergent per-stream expert durations before state advances.
- `85191df`: keep seek scans inside the current chained link.
- `7d8ebb0`: reject physical EOF before final logical EOS.
- `85eb9bb`: preflight deterministic PLC/FEC child failures before any earlier
  multistream child advances.

After those fixes, all passed:

```text
go vet ./...
go test -count=1 ./...
go test -count=1 -tags opusref ./...
```

Optional Phase 1-6 (multiplexed Ogg demux and custom projection encoder
matrices) remains deliberately unstarted pending explicit user approval.

## Phase 2-1 Result

Implemented and committed:

- `6acc6b8`: checkpointed the initial `FuzzDecoderSequence` harness.
- `1847187`: added temporary slow-input tracing while isolating the apparent
  stall.
- `1abf955`: added verified SILK/FEC and hybrid decoder sequence seeds.
- `7b93b15`: split operation descriptors from payload bytes, added a per-input
  decode/PLC work budget, resized PCM scratch buffers by rate/channel, and
  re-enabled arbitrary FEC error-path probing.
- `ecd3b8c`: added `FuzzDecoderSequence` to the fuzz CI matrix for amd64/arm64,
  with explicit job/test timeouts, `-parallel=1`, and `-fuzzminimizetime=10x`.
- `5d91ec3`: recorded the post-adoption gate results.

Qualification:

```text
go test -run='^$' -fuzz='^FuzzDecoderSequence$' -fuzztime=30m -fuzzminimizetime=10x -parallel=1 -timeout=31m -v .

PASS: 1,036,678 executions, 1,565 new interesting inputs, zero crashes
```

The earlier 30-second "stall" was isolated as Go fuzz minimization time for new
coverage, not a decoder call that exceeded a target-body watchdog. The CI target
therefore uses bounded minimization explicitly.

After adoption, all passed:

```text
go vet ./...
go test -count=1 ./...
go test -count=1 -tags opusref ./...
```

Remaining Phase 2-1 follow-up candidates, not blockers for adoption:

- Add more valid transition seeds for CELT/SILK/hybrid mode and bandwidth
  switching.
- Add a separate state-preservation oracle comparing an errored decoder against
  a control decoder that skipped the rejected operation.

## Phase 2-2 Result

Implemented and committed on `codex/robustness`:

- `oggopus/stream_fuzz_test.go` adds `FuzzOggOpusReaderWriter`, a bounded
  schema-driven Ogg Opus Writer-to-Reader fuzz target covering one to three
  chained links, valid packet timing, pre-skip/end-trim discard metadata,
  deterministic read results, structured corruption, large comment continuation,
  and one bounded seek replay per input.
- `.github/workflows/fuzz.yml` adds `FuzzOggOpusReaderWriter` to the
  nightly/manual amd64/arm64 fuzz matrix.
- `docs/CURRENT_IMPLEMENTATION.md` and `.claude/memory/iterations/robustness-phase2.md` record
  the adopted coverage and qualification result.

Qualification:

```text
go test -run='^$' -fuzz='^FuzzOggOpusReaderWriter$' -fuzztime=30m -fuzzminimizetime=10x -parallel=1 -timeout=31m -v ./oggopus

PASS: 831,659 executions, 98 new interesting inputs, zero crashes
```

Post-adoption gates already passed:

```text
go vet ./...
go test -count=1 ./...
go test -count=1 -tags opusref ./...
```

## Phase 2 Design Notes / Next Work

The decoder sequence and Ogg Reader/Writer targets have been adopted into CI.

## Phase 2-3 Result

Implemented and committed on `codex/robustness`:

- `encoder_sequence_fuzz_test.go` adds `FuzzEncoderSequence`, a bounded
  setter/input operation-sequence target for single-stream encoders.
- It covers all four public encode APIs, bitrate/complexity/rate-mode/FEC/DTX,
  padding, force-channel, LSB-depth, expert-frame-duration, prediction,
  phase-inversion, bandwidth, signal, application, and reset controls.
- PCM generation includes exact, short, extra, zero-length, and invalid
  frame-size calls, plus silence, full-scale integer extremes, out-of-range
  24-bit values, out-of-range floats, and `NaN` / `+Inf` / `-Inf`.
- The oracle compares two fresh encoders for deterministic errors, packet bytes,
  and observable state; every successful packet must parse and decode through a
  fresh decoder without guard-sample writes.
- `.github/workflows/fuzz.yml` adds `FuzzEncoderSequence` to the nightly/manual
  amd64/arm64 fuzz matrix.

Qualification:

```text
go test -run='^$' -fuzz='^FuzzEncoderSequence$' -fuzztime=30m -fuzzminimizetime=10x -parallel=1 -timeout=31m -v .

PASS: 129,078 executions, 1,514 new interesting inputs, zero crashes
```

Post-adoption gates passed:

```text
go vet ./...
go test -count=1 ./...
go test -count=1 -tags opusref ./...
```

The next planned Phase 2-4 task is local-only `opusref` differential decoder
fuzzing. The detailed task file is
`.claude/tasks/testing/opusref-differential-fuzz-phase2-4.md`.

## Operational Notes

- User's standing objective for this branch is to keep moving toward a complete
  Pure Go Opus library. Treat
  `.claude/plans/pure-go-completeness-2026-07-16.md` as the roadmap, with
  `docs/CURRENT_IMPLEMENTATION.md` taking precedence for code-derived status.
- The user explicitly allowed SubAgents/parallel review when useful for bounded
  subtasks.
- Commit messages are Conventional Commits in English.
- Commit frequently; the user explicitly permits temporary-test and WIP
  commits for shutdown safety.
- Do not push integration branches unless requested.
- Read `docs/CURRENT_IMPLEMENTATION.md` before implementation or documentation
  claims.
- Safety filtering flagged one delegated fuzz-reproduction attempt in Codex, but
  local Go fuzzing and tests completed normally. Keep future wording and tool
  use scoped to local authorized repository testing.
- A notification webhook was recorded here during the original handoff. It has
  been removed from the tracked document; any previously exposed credential
  should be rotated before reuse.
- The optional Phase 1-6 and v1.0 tag require explicit user approval.
- `claude -p` was attempted once but failed due a local authentication/connector
  conflict and produced no useful result.
