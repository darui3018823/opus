# libopus Bit-Exact Convergence Handoff

Date: 2026-09-15
Status: Active handoff (predecessor session stopped on rate limit)
Supersedes: none. Extends
[`../../plans/libopus-bit-exact-convergence-2026-09-08.md`](../../plans/libopus-bit-exact-convergence-2026-09-08.md).

## Branch topology at handoff

```text
main (a37d852)
 └─ dev/libopus-bit-exact   (+73)  convergence branch; tip b70e7a0
     └─ dev/silk-q1-oracle  (+17)  Q1 LPC/NLSF experiment; tip 2e40474, clean tree
```

The predecessor followed the plan's work loop: child experiment branch per
slice, merge accepted slices back into `dev/libopus-bit-exact`. The Q1 slice
is complete at the oracle level but has **not** been merged back.

## Verified state (from committed evidence, 2026-09-13)

| Area | State |
|---|---|
| Decoder final range | 0 mismatches on all 12 official vectors (hard gate) |
| CELT decoder, pure-CELT vectors (tv01/07/11) | every emitted scalar-source stage hash matches checked-in libopus 1.6.1 |
| CELT decoder, mixed-mode vectors | not scanned at stage level; installed-libopus int16 equality tv09 96.4 %, tv10 85.0 % |
| SILK decoder / PLC | parameter/range coverage only; PLC explicitly non-bit-exact |
| SILK encoder Q1 (`LPC_in_pre` → `PredCoef_Q12`) | 13 fixtures exact vs C (`TestSILKQ1LPCNLSFOracle`, opusref) |
| SILK encoder Q2–Q7, CELT encoder, mode/rate policy | not bit-exact |

## Open decision (blocks Q1 merge-back)

`TestOpusSILKABAgainstLibopusEncoder` 8 kHz `speech-like-harmonic` matched
loudness moved from -1.50 dB (PASS) to -1.59 dB (FAIL, ±1.5 dB gate) relative
to `b70e7a0`. Root cause is the corrected libopus residual-rate table; reverting
it breaks the Q1 residual-index oracle. Plan rule: no threshold exceptions.

Options to put to the user:

1. Keep Q1 on its child branch until a later slice (gain/rate loop) restores
   the margin, then merge together.
2. Merge Q1 now and accept the failing quality subtest as a tracked known
   failure (violates the plan's "preserve quality gates" clause).
3. Widen the gate (rejected by the predecessor; listed for completeness).

Default while waiting: option 1. Continue on `dev/silk-q1-oracle` (or a child
of it) with upstream SILK encoder slices, which is where the loudness margin is
expected to come back from.

## Roadmap

Ordering follows `docs/GAP_ANALYSIS.md` (Q1 → Q2 → Q3/Q4 → Q5 → Q7) for the
encoder and first-divergence localization for the decoder. Each item is one
slice = one child branch = one accept/reject decision.

### Phase 0 — Re-establish baseline (first working session)

- Run `go build ./...`, `go vet ./...`, `go test -count=1 ./...`, and
  `go test -count=1 -tags opusref ./...` (PowerShell) on `dev/silk-q1-oracle`.
- Confirm the only opusref failure is the 8 kHz speech-harmonic loudness gate.
- Escalate the open decision above by webhook. Do not block on it.

### Phase 1 — SILK encoder Q2: pitch/LTP exactness

- Reference: `silk/float/find_pitch_lags_FLP.c`, `pitch_analysis_core_FLP.c`,
  `find_LTP_FLP.c`, `LTP_analysis_filter_FLP.c`, `quant_LTP_gains.c`.
- Extend the Q1 opusref harness upstream: inject identical pre-pitch state,
  compare pitch lags, contour index, LTP coefficients, `LTPredCodGain`, and
  quantized LTP gain indices bit-for-bit. `5302270` already calls the C LTP
  analysis directly; build on that.
- Port `silk_find_LTP_FLP` and `silk_quant_LTP_gains` faithfully (5-tap
  codebook selection replaces the simplified LTP gain).
- Expected side effect: voiced byte counts move toward libopus; re-measure the
  speech-harmonic loudness gate here.

### Phase 2 — SILK encoder Q3/Q4: noise shaping and NSQ

- Reference: `silk/float/noise_shape_analysis_FLP.c`, `silk/NSQ_del_dec.c`.
- Oracle checkpoints: shaping filter coefficients, tilt, harmonic shaping,
  gains pre/post, NSQ pulses and `sLTP` state after each subframe.
- Port until pulse vectors match for the fixture matrix.

### Phase 3 — SILK encoder Q5: gain loop and rate control

- Reference: `silk/float/encode_frame_FLP.c` gain-loop iterations,
  `silk/gain_quant.c`, `silk/control_SNR.c`.
- Target: per-frame `nBits` and gain indices equal to libopus on fixtures.
- This is where the loudness margin decision should resolve itself.

### Phase 4 — SILK-only encoder byte-exact gate

- Full-encoder deterministic fixtures (8/12/16 kHz, mono/stereo, complexity
  bands): compare packet bytes and final range against libopus. Report first
  divergent byte and the SILK symbol boundary containing it.
- Then hybrid low band with identical CELT input.

### Phase 5 — Decoder remainder

- Mixed-mode vectors (tv08/09/10/12): reuse the vector-wide scalar stage scan
  on their CELT constituents; add SILK fixed-point decoder state checkpoints
  (`silk_decode_core`, `silk_PLC`, resampler) to find the first divergent
  sample in tv09/tv10.
- SILK PLC and CELT PLC exactness (libopus fixed-point SILK is fully
  deterministic; there is no float-build excuse here).

### Phase 6 — CELT encoder and mode/rate policy

- CELT encoder: `celt_encoder.c` analysis (transient, tf, spreading, dynalloc,
  allocation trim, PVQ search) with stage hashes mirroring the decoder scan.
- `opus_encoder.c` mode/bandwidth/bitrate decisions over identical PCM and
  control sequences (`docs/MODE_RATE_POLICY_DIFF.md` is the inventory).

### Deferred

libopus 1.6.1 DRED/OSCE/DNN paths, per the convergence plan.

## Working rules carried over

- Pure Go default build; CGO only under `opusref` in `internal/cgoref`.
- No epsilon in exactness oracles; report first divergent index, C value, Go
  value.
- Each slice commits its oracle before its port when the port is large.
- Full gates per slice: `go test -count=1 ./...`, `go vet ./...`,
  `go test -count=1 -tags opusref ./...` (PowerShell), `go test -race -count=1 ./...`.
- Update `docs/CURRENT_IMPLEMENTATION.md`, `docs/GAP_ANALYSIS.md`, and the
  convergence plan's "Completed Slices" section with each accepted slice.
