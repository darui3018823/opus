# libopus Core Parity Plan

Last updated: 2026-07-21
Status: Complete

## Objective

Improve the repository from the 2026-07-19 completeness audit baseline toward
a more credible libopus 1.6.1 core-codec replacement. Keep every codec change
measurement-driven and preserve standards interoperability, decoder
conformance, and packet/final-range regression evidence.

## Current Baseline

- The 2026-07-19 audit rates the Pure Go product at about 95% and the RFC
  6716/core libopus replacement at about 89%.
- Decoder, loss recovery, framing, multistream, projection, and Ogg support are
  the strongest areas.
- The largest core gaps are encoder mode/rate/quality policy, CELT stereo music
  quality at 24/32 kbit/s, and predictive stereo encode cost.
- DRED, QEXT, OSCE, and DNN processing are not implemented. Packet extension
  transport does not imply extension codec support.

## Scope Decision

This phase targets RFC 6716 and the core libopus APIs. libopus 1.6.1 neural and
experimental extension processing (DRED, QEXT, OSCE, and DNN blobs) is outside
the implementation scope. The public documentation must say so directly.

## Work Plan

1. Separate API surface, semantic parity, and evidence in the CTL matrix.
2. Correct known overstatements for lookahead, LSB depth, and packet-loss CTL
   error handling, and publish the core-versus-extension claim boundary.
3. Measure CELT 24/32 kbit/s stereo-music candidates independently. Adopt only
   changes that improve the target cells without a material aggregate or byte
   regression.
4. Profile predictive stereo encoding and implement output-preserving
   allocation/CPU reductions, guarded by the 64-frame packet/final-range
   digest and the long-stream benchmark.
5. Implement one independently measurable surround-mask consumer or aggregate
   CTL convenience slice without coupling it to encoder mode-policy changes.
6. Run full normal, `opusref`, race, vector, quality, and performance gates;
   update the code-derived implementation snapshot and record adopted/rejected
   experiments.

Mode-policy threshold work remains paused until a concrete corpus target can
demonstrate a net per-bit win. The two candidates rejected in the 2026-07-17
policy iteration are not to be reintroduced without new evidence.

## Acceptance Criteria

- Product claims clearly target core libopus behavior and do not imply DRED,
  QEXT, OSCE, DNN, Opus Custom, or C ABI compatibility.
- The CTL matrix distinguishes an exposed Go API from equivalent semantics and
  cites a test, implementation note, or explicit gap for each group.
- Every adopted codec/performance change has a before/after measurement and a
  regression guard; rejected candidates leave no production-code residue.
- Existing packet/final-range digests, official vectors, and libopus
  interoperability tests continue to pass.
- `docs/CURRENT_IMPLEMENTATION.md` and a dated iteration record describe the
  resulting implementation rather than planned behavior.

## Verification

```text
go test -count=1 ./...
go vet ./...
go test -count=1 -tags opusref ./...
go test -race -count=1 ./...
go test -count=1 -run '^TestOfficialVectors$' -v .
go test -run '^$' -bench '^BenchmarkPerf(LongStream)?$/encode/(silk|hybrid)/(mono|stereo)/48k/20ms$' -benchmem .
```

The opt-in corpus command and its required environment are documented in
`docs/REAL_CORPUS_SCOREBOARD.md`.

## Results

- Published the core-libopus claim boundary and split CTL coverage into API
  surface, semantic parity, and evidence. DRED, QEXT, OSCE, DNN processing,
  Opus Custom, the C ABI, and bit-exact encoder output remain outside scope.
- Added CELT-only stereo tonality-slope allocation trim. The focused 24 kbit/s
  cell improved from 5.886 to 5.726 dB and the 32 kbit/s cell from about 5.21
  to 4.948 dB. The full saved corpus improved from 5.61 to 5.55 dB at 24
  kbit/s and from 5.49 to 5.40 dB at 32 kbit/s, with all loss-0 byte totals
  unchanged.
- Reused predictive NLSF search destinations. Same-window SILK stereo encoder
  cost fell 7.4% in bytes/op and 34.3% in allocations; hybrid stereo fell
  7.9% and 47.4%, respectively. Packet/final-range digests and long-stream
  bounded-heap evidence remained unchanged.
- Added first-stream getters and broadcast setters for aggregate multistream
  core codec controls. Surround inherits these controls through embedding;
  projection-specific convenience coverage remains partial.
- Updated the code-derived snapshot and dated iteration records for each
  adopted implementation change.

## Final Validation

All normal and `opusref` suites, `go vet ./...`, the packet/final-range
regression gates, and the saved 140-cell corpus completed successfully during
the implementation checkpoints. The final branch-wide checks also passed:

```text
go test -race -count=1 ./...                         PASS
go test -count=1 -run '^TestOfficialVectors$' -v .  PASS (12/12)
```

The highest official-vector RMSE was 0.000809 (`testvector12`). Remaining work
is deliberately carried forward: evidence-backed mode/rate policy, further
CELT 24/32 kbit/s stereo quality, absolute predictive encoder cost, stereo
savings/dynamic allocation, and projection-specific aggregate conveniences.
