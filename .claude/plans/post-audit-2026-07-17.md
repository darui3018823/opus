# Post-Audit Completion Plan (2026-07-17)

Status: **Active; Phase 1 completed and adopted**

Last updated: 2026-07-17

Basis: `.claude/memory/audits/libopus-completeness-2026-07-17.md`

Predecessor: `.claude/plans/pure-go-completeness-2026-07-16.md`

## Objective

Raise the Pure Go Opus implementation from the 2026-07-17 audit baseline
(approximately 93% as a Pure Go library and 81% as a complete libopus 1.6.1
replacement) by addressing the five highest-value remaining areas in a
measurement-driven order.

The approved phase structure is:

1. CELT/music worst-case quality gap
2. PLC/FEC duration, packed-packet, and state semantics
3. SILK/hybrid mode-rate-quality policy
4. SILK/hybrid encoder allocation and runtime cost
5. Surround psychoacoustic parity

Phase 1 is divided into five slices. Phases 2 through 5 retain the four
follow-up areas selected after the audit.

## Scope and Guardrails

- Work from current `main`, but use one bounded branch per slice or independent
  iteration.
- Keep one behavioral hypothesis per change. Do not mix quality, API, and
  performance changes in one iteration.
- Compare quality at matched bytes. Higher quality obtained by materially
  increasing packet size is a rate trade-off, not a win.
- Do not change range-coder symbol order unless a demonstrated bitstream bug
  requires it.
- Any encoder byte change must retain encoder/decoder final-range agreement and
  libopus cross-decode compatibility.
- Preserve the 12/12 RFC 8251 decoder-vector result throughout the plan.
- Record adopted and rejected experiments. A rejected measurement is a valid
  result and must not be hidden behind a permanent experimental gate.
- Update `docs/CURRENT_IMPLEMENTATION.md` only after behavior has actually
  changed; plans and task files are not implementation-status authority.

## Common Verification

Every production slice must pass, from PowerShell:

```powershell
go vet ./...
go test -count=1 ./...
go test -count=1 -tags opusref ./...
go test -count=1 -run '^TestOfficialVectors$' -v .
```

Quality-changing slices additionally run the real-corpus scoreboard:

```powershell
$env:OPUS_REAL_CORPUS = "1"
go test -count=1 -tags opusref -run TestOpusRealCorpusMatchedBitrateScoreboard -v .
```

Performance-changing slices use the public benchmark harness with at least
five samples and `-benchmem`; compare results with `docs/PERF_BASELINE.md`.

---

## Phase 1: CELT/Music Worst-Case Quality Gap

### Goal

Explain and reduce the measured matched-bitrate CELT/music loss, currently
concentrated in the synthetic stereo-chords cells with approximately 7.7–9.7 dB
libopus advantage at 24–64 kbps. Do not mask the gap by routing the signal to a
different coding mode.

### Slice 1-1: Preserve a Small Reproducer

Create a deterministic, redistributable fixture or generator that reproduces
the worst CELT/music behavior without depending on an ignored local corpus
file.

The reproducer must capture:

- sample rate, channels, frame duration, bitrate, VBR/CVBR mode, and signal
  hint;
- Go packet bytes and libopus matched-bitrate byte accounting;
- own/libopus/matched SNR;
- first and subsequent TOC configurations;
- stability across repeated runs.

Acceptance criteria:

- the focused test or diagnostic reproduces a material Go-vs-libopus gap on the
  current baseline;
- byte matching is close enough to make the quality comparison meaningful;
- the fixture is small and legal to commit, or is generated entirely in code;
- no production behavior changes in this slice.

### Slice 1-2: Isolate the Dominant Decision

Use controlled diagnostic ablations to measure the contribution of:

- TF analysis and transient/block decisions;
- dynamic allocation and allocation trim;
- stereo, intensity, and dual-stereo decisions;
- automatic bandwidth selection and rate targeting.

Temporary instrumentation may be used during the investigation, but permanent
production toggles are not the deliverable.

Acceptance criteria:

- produce an evidence table ranking the four areas by effect on quality, bytes,
  and final-range behavior;
- identify one dominant cause or a clearly described interaction that is small
  enough for a single implementation slice;
- record inconclusive and negative ablations as well as improvements.

### Slice 1-3: Implement One Root-Cause Fix

Implement only the highest-value cause identified by Slice 1-2. Do not combine
multiple CELT policy or allocation rewrites.

Acceptance criteria:

- materially reduce the focused worst-case gap at comparable bytes;
- preserve valid Opus packet output and encoder/decoder final-range agreement;
- retain libopus cross-decode compatibility;
- avoid a compensating regression in adjacent music bitrates.

The default adoption target is at least a 2 dB absolute reduction or a 25%
relative reduction in the focused gap, with packet-byte movement within 5%.
If the evidence shows that a different threshold is statistically appropriate,
record the reason before judging the slice.

### Slice 1-4: Prove Broad Non-Regression

Run the complete conformance and corpus gates for the candidate from Slice 1-3.

Required evidence:

- common verification commands pass;
- focused CELT final-range and libopus cross-decode tests pass;
- all 140 existing real-corpus scoreboard cells encode successfully;
- speech-oriented class average matched gap does not regress by more than
  0.3 dB;
- own-byte totals do not materially increase;
- no new worst cell is created in music or mixed content.

### Slice 1-5: Adoption Decision and Baseline Update

Adopt or reject the candidate using the evidence from Slices 1-1 through 1-4.

If adopted:

- update `docs/REAL_CORPUS_SCOREBOARD.md` with before/after results;
- add the focused reproducer to the permanent regression suite;
- update `docs/CURRENT_IMPLEMENTATION.md` and relevant quality notes;
- record the production commit and verification commands.

If rejected:

- revert temporary production changes;
- retain the reproducer and investigation record when they remain useful;
- document why the candidate failed and nominate the next measured cause.

Phase 1 is complete only after an explicit adopt/reject decision. Completing an
experiment is not the same as adopting its code.

**Decision (2026-07-17): Adopted.** The permanent generated reproducer ranked
the dominant cause as over-aggressive CELT constrained-VBR startup targeting.
The adopted libopus-style two-thirds damping keeps focused and corpus byte
totals unchanged while reducing the stereo-chords matched gap by 2.86–9.12 dB
at 24–64 kbps. All common verification commands, all 12 official vectors, and
the full 140-cell scoreboard passed. The evidence and rejected ablations are in
`.claude/memory/iterations/celt-music-phase1-2026-07-17.md`.

---

## Phase 2: PLC/FEC Semantic Parity

### Goal

Close the strict loss-recovery API and state differences identified by the
audit while preserving v1 compatibility.

Scope:

- explicit lost-duration semantics for FEC;
- packed multi-frame SILK/hybrid FEC;
- PLC behavior before the first successful packet;
- the accepted PLC frame-size set;
- decoder `FinalRange` after PLC and FEC;
- consistent contracts across single-stream, multistream, and surround;
- decision on float32, float64, and signed-24-bit convenience APIs.

Before implementation, decide whether each change is an additive v1 API, an
internal semantic correction, or a v2 candidate. Do not silently break the
existing `DecodeFEC(data, pcm)` contract.

Acceptance criteria:

- focused Go/libopus semantic comparison tests cover each adopted behavior;
- multi-frame and state-transition tests prove no partial state advancement on
  error;
- public comments and `docs/CTL_PARITY.md` describe remaining differences
  exactly;
- common verification passes.

---

## Phase 3: SILK/Hybrid Mode-Rate-Quality Policy

### Goal

Improve predictive-mode selection and rate policy one measured gate at a time.

Candidate areas include:

- automatic voice/music predictive entry;
- SILK-only upper boundaries;
- hybrid lower and upper boundaries;
- SILK internal-rate selection;
- stereo-width policy;
- broader VBR/CVBR/DTX control integration.

Protocol:

- one gate per branch and scoreboard run;
- use `docs/MODE_RATE_POLICY_DIFF.md` to define the exact libopus difference;
- require a target corpus class and bitrate/loss cells before implementation;
- reject changes that only move bytes or shift a loss into another class;
- do not use mode routing to conceal the Phase 1 CELT quality problem.

Acceptance criteria for each adopted gate:

- target cells improve at matched bytes;
- non-target speech/music classes remain within the documented regression
  budget;
- packet mode transitions, redundancy, FEC, and final range remain valid;
- common verification and the full real-corpus scoreboard pass.

Phase 3 stops when two consecutive well-motivated gate candidates fail to
produce a net per-bit win, unless a standards-interoperability defect requires
continued work.

---

## Phase 4: SILK/Hybrid Encoder Allocation and Runtime Cost

### Goal

Reduce the remaining absolute allocation and latency cost of realtime
SILK/hybrid encoding without changing packet bytes or decoded PCM.

Work order:

1. capture CPU and allocation profiles for mono/stereo SILK and hybrid;
2. rank retained allocation sites by bytes/op and allocs/op;
3. optimize one hotspot per iteration using state-owned reusable buffers or
   bounded stack storage;
4. compare against the immediate parent and the published baseline;
5. retain only measured wins.

Acceptance criteria for each iteration:

- at least 5% improvement in time, bytes/op, or allocs/op for the target
  workload;
- no material regression in another SILK/hybrid benchmark;
- representative packet bytes and final ranges are unchanged;
- common verification passes;
- update `docs/PERF_BASELINE.md` for adopted cumulative results.

Add a long-running stream benchmark before declaring Phase 4 complete so GC
and buffer-retention behavior is measured beyond isolated 20 ms calls.

---

## Phase 5: Surround Psychoacoustic Parity

### Goal

Replace the remaining simplified surround allocation decisions with a measured
energy-mask and channel-role analysis that approaches libopus behavior while
preserving mapping and interoperability.

Scope:

- document the libopus surround analysis and energy-mask inputs;
- build deterministic 5.1 and 7.1 fixtures including LFE, correlated fronts,
  diffuse surrounds, and silent/duplicate mappings;
- measure current per-stream rate and bandwidth allocation;
- implement one psychoacoustic decision at a time;
- retain family 0/1/255 mapping and Appendix B framing behavior.

Acceptance criteria:

- measurable improvement in channel-weighted quality or bitrate allocation on
  the target fixtures;
- no LFE starvation, side/rear collapse, or coupled-stream spatial regression;
- Go/libopus multistream packet interoperability remains green;
- PLC/FEC and expert-frame-duration behavior remain unchanged;
- common verification passes.

Phase 5 does not include arbitrary projection encoder matrix generation or Ogg
multiplexed demux; those remain separate optional features.

---

## Status

- [x] Phase 1: CELT/music worst-case quality gap — adopted 2026-07-17
  - [x] Slice 1-1: preserve a small reproducer
  - [x] Slice 1-2: isolate the dominant decision
  - [x] Slice 1-3: implement one root-cause fix
  - [x] Slice 1-4: prove broad non-regression
  - [x] Slice 1-5: adoption decision and baseline update
- [ ] Phase 2: PLC/FEC semantic parity
- [ ] Phase 3: SILK/hybrid mode-rate-quality policy
- [ ] Phase 4: SILK/hybrid encoder allocation and runtime cost
- [ ] Phase 5: surround psychoacoustic parity

## Completion Definition

This plan is complete when all five phases have an explicit adopted, rejected,
or intentionally deferred conclusion backed by measurements; all adopted work
is merged with the common verification green; and current-status documentation
describes the resulting limits without relying on this plan as proof.
