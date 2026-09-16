# libopus Bit-Exact Convergence Handoff

Date: 2026-09-15
Status: Active handoff (predecessor session stopped on rate limit)
Supersedes: none. Extends
[`../../plans/libopus-bit-exact-convergence-2026-09-08.md`](../../plans/libopus-bit-exact-convergence-2026-09-08.md).

## Branch topology at handoff

```text
main (a37d852)
 └─ dev/libopus-bit-exact   (+73)  convergence branch; tip b70e7a0
     └─ dev/silk-q1-oracle  (+20)  Q1 LPC/NLSF experiment + handoff docs + padding test fix
         └─ dev/silk-plc-exact     SILK PLC/CNG port (2026-09-15, this session)
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
| SILK decoder / PLC | PLC, CNG, glue, one-byte payload handling sample-exact vs libopus (2026-09-15, `dev/silk-plc-exact`) |
| SILK encoder Q1 (`LPC_in_pre` → `PredCoef_Q12`) | 13 fixtures exact vs C (`TestSILKQ1LPCNLSFOracle`, opusref) |
| SILK encoder VAD, pitch, LTP (Q2) | bit-exact on injected inputs (2026-09-15, `dev/silk-q2-ltp-oracle`); wired into the encoder |
| SILK encoder input pipeline | complete (2026-09-16, `dev/encoder-input-pipeline`): VAD flags, 5 ms look-ahead, Fs/250 CELT delay, high-pass, int16 front end, libopus encoder resampler; conditioned input / x_buf / VAD / HP state bit-exact vs the instrumented libopus encoder at 8/12/16/24/48 kHz; silence-shortcut policy open |
| SILK encoder Q3 noise shaping | bit-exact vs the instrumented libopus encoder on shared inputs (2026-09-16, `ad81c1f`) |
| SILK-only encoder, mono and stereo, CVBR and CBR 20 ms, with and without in-band FEC | **byte-identical to libopus 1.6.1** on all shared-state oracle cells (mono 24/32 kbps, +20 % loss, CBR; stereo 8/12/16 kHz), and against the real libopus encoder on 90/119 auto-mode cells = every SILK-only same-channel-count cell (2026-09-16, `e16f983`); onset/silence blocked by the Go silence shortcut |
| SILK encoder: silence shortcut; mode/channel/bandwidth policy (hybrid for 24/48 kHz input, stereo→mono downmix, decide_fec narrowing); hybrid/CELT encoder | not verified / not bit-exact |

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

Baseline measured 2026-09-15 03:58 JST on `dev/silk-q1-oracle` (`2e40474`):
`go build`, `go vet`, and `go test -count=1 ./...` pass. The `opusref` suite
has five failing tests, not the one documented by the predecessor:

| Test | Failure | Present on `dev/libopus-bit-exact` | Present on `main` |
|---|---|---|---|
| `TestOpusSILKABAgainstLibopusEncoder` speech-like-harmonic | matched loudness 8k -1.59 / 12k -1.93 / 16k -1.57 dB (gate ±1.5) | 8k passes at -1.50; 12k/16k not re-checked | pass |
| `TestCGOEncodeRefSILKOnlyExtendedDurationsStrict` 80/120 ms | count code 3, want 2 | yes | pass |
| `TestCGOEncodeRefSILKFEC` | fec=732 no=732 "LBRR absent?" | yes | pass |
| `TestCGOEncodeRefSILKFECMultiFrame` 40/60 ms | fec size == no-FEC size | yes | pass |
| `TestCGODecodeFECMatchesLibopus` 40/60 ms | LBRR mask parser rejects count code 3 | yes | pass |

The four non-loudness failures originate in the 2026-09-09 slice
`641e01a fix(encoder): keep CBR padding outside SILK frames`. The encoder
default is CBR (`opus.go` `rateMode: celt.RateModeCBR`), so every packet now
carries code-3 padding and equal sizes; `60918f3` relaxed only the non-strict
`TestCGOEncodeRefSILKOnly`. These look like stale test contracts rather than a
codec regression, but that must be proven, not assumed.

Phase 0 outcome (2026-09-15, 10:00-11:00 JST):

- The four padding failures were stale contracts: LBRR is present after
  `PacketUnpad` (+156 unpadded bytes on the 20 ms stream). Fixed in
  `00e5af3 test(opusref): inspect unpadded SILK packets for LBRR and frame count`
  on `dev/silk-q1-oracle`.
- Fixing the LBRR-mask parser exposed a real gap: `TestCGODecodeFECMatchesLibopus/60ms`
  diverged 0.4 dB on a PLC-filled LBRR subframe. Bisect: first bad commit is
  the Q1 port `7226070` (the encoder now marks a fixture subframe inactive),
  but the cause is the Go SILK PLC approximation. Ported libopus PLC/CNG/glue
  on child branch `dev/silk-plc-exact` (`e26d926`, `909e55e`); SILK PLC, FEC
  gaps, and one-byte payloads are now sample-exact (see the convergence plan's
  2026-09-15 slice).
- Full `opusref` suite after the port: only the speech-like-harmonic loudness
  gates (8k/12k/16k) fail. Normal and race suites green.
- Escalated the Q1 merge decision and the PLC-first reordering by webhook at
  10:2x JST; no answer yet. Default remains option 1.

Merge order proposal: `dev/silk-plc-exact` → `dev/silk-q1-oracle` (fast-forward),
then the Q1 decision governs the merge into `dev/libopus-bit-exact`.

### Phase 1 — SILK encoder Q2: pitch/LTP exactness

Status 2026-09-15: LTP correlation + gain quantization exact (`f38a13a`) and
pitch analysis exact on injected buffers (`0ce1c1b`), both on
`dev/silk-q2-ltp-oracle`; LTP now runs once per frame on `res_pitch`
(`ebd1d77`); the VAD is fixed-point exact and the first frame after reset is
unvoiced like libopus (`24943ae`), which leaves the 8k speech-harmonic
loudness gate failing by 0.12 dB (unvoiced first-frame bit cost → Q5 rate
control). Remaining in Q2: the quant-offset sparseness measure on `res_pitch`
and the `la_pitch` look-ahead (framing slice).

Blocking finding for Phase 4: the Go encoder has no SILK `LA_SHAPE_MS` (5 ms)
look-ahead delay and no Opus-layer `delay_compensation` (Fs/250), so its frame
boundaries do not contain the same audio as libopus'. A dedicated
framing/delay slice is required before packet-level byte comparison.

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

### Phase 1b — Encoder input pipeline (framing, delay, high-pass)

Spec: `.claude/specs/encoder-input-pipeline-libopus.md`. Must land before
Q3 exactness (shaping windows read the look-ahead) and before Phase 4.
Changes public latency (`Lookahead()` → 6.5 ms like libopus).

Status 2026-09-16: steps 0–3 landed on `dev/encoder-input-pipeline`
(`5e6482f` VAD flags, `70cae26` SILK 5 ms look-ahead buffer, `f7de2bf`
Opus-layer Fs/250 CELT delay; `Lookahead()` = 312 at 48 kHz; `a04f4ce`
hp_cutoff/dc_reject/variable_HP smoothing, unit-exact vs the libopus float
bodies; `81444af` int16 front end + instrumented libopus encoder oracle:
conditioned input, x_buf, VAD, HP state bit-exact through frame 0 on the
mono AB fixtures; `94938d2` libopus encoder resampler, delay equal to
libopus in every mode; `befc6f7`/`0173420`/`08befec` stage traces). The
oracle now names every differing NSQ input per frame: on frame 0 NLSF and
PredCoef match, Gains_Q16 drift from subframe 1 (Q5), unvoiced shaping
(AR_Q13/Tilt/LF) is zeroed on the Go side (Q3), Lambda_Q10 off by 1–2.
Q3 done (`63808a3`/`ad81c1f`): noise_shape_analysis_FLP bit-exact vs the
oracle on shared inputs and driving the NSQ; libopus target rate / bit
reservoir / TOC-adjusted SILK bit rate ported; loudness gate passes at all
rates (8k -0.64 / 12k -0.25 / 16k -0.01 dB). Q5 VBR done (`d0e2532`):
exact process_gains / gains_quant / residual energy, shell scale-down fix,
48 kHz homebrew-NSQ fallback removed; `9b4d0d0`/`179e7f6`/`51d24d4` payload
sizing, frame-counter NSQ seed, int16-scale Burg, inactive frames on the
exact path, Q5 rate-level tables → **SILK-only VBR: all 12 packets
byte-identical to libopus on all 15 shared-state oracle cells** (8/12/16/24/48
kHz × steady-voiced / harmonic / noise; gated by
`TestSILKEncoderInputPipelineOracle`). `3559e49`/`a5517f4`/`dcd288d`
(2026-09-16): exact `silk_LBRR_encode_FLP` + `silk_LTP_scale_ctrl` +
`decide_fec`, float32 sine-window frequency → oracle exact at 24 kbps,
24 kbps + 20 % loss and 32 kbps on all 60 shared-state cells, and
**`TestCGOEncodeRefSILKByteExact`: byte-identical to the real libopus
encoder (cgoref, auto mode) on 24/30 cells** (rest = libopus picks hybrid
at 32 kbps for 24/48 kHz input). `8fdbcf6`/`77a88dd`/`81e0c48`/`1bc9021`
(2026-09-16 night): exact `silk_stereo_LR_to_MS` + stereo packet flow,
double `silk_sigmoid`, `compute_silk_rate_for_hybrid` → **stereo SILK
packets byte-identical too; `TestCGOEncodeRefSILKByteExact` 49/66 cells,
i.e. every cell libopus codes as SILK-only with the same channel count**.
`46c8703`/`8798be9`/`e16f983` (2026-09-16 night): encode_frame_FLP
quantiser loop + cbr_bytes sizing/padding → **CBR mono and stereo SILK
packets byte-identical too (90/119 cgoref cells = every SILK-only
same-channel-count cell in CVBR, CVBR+FEC and CBR)**. Remaining: the
digital-silence shortcut policy (onset fixture; libopus codes silent
frames), mode/channel/bandwidth policy (hybrid for 24/48 kHz input,
stereo→mono downmix, decide_fec narrowing), hybrid/CELT, a
phase-insensitive scoreboard distance. Merge-back: `dev/libopus-bit-exact` was fast-forwarded to
`08c4201` (Q1/Q2/PLC) on 2026-09-16; the child branches
`dev/silk-plc-exact`, `dev/silk-q1-oracle`, `dev/silk-q2-ltp-oracle` were
deleted.

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
  (`silk_decode_core`, resampler) to find the first divergent sample in
  tv09/tv10.
- SILK PLC exactness: done 2026-09-15 (`dev/silk-plc-exact`). CELT PLC
  exactness remains (hybrid PLC oracles show 26-30 dB, not exact).

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
