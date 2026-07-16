# SILK Encoder Quality — Step 3 (Q3+Q4) slice spec

> **Status: completed on 2026-06-17.** Implemented by `623d0ec`. This is a
> historical implementation contract; do not execute it as a current task.
> Use `docs/CURRENT_IMPLEMENTATION.md` for current behavior.

Branch `dev/silk-encoder`. Director: Claude. Implementer: Codex.
Goal = **libopus quality-following** (NOT bit-exact; decoder is the only RFC-normative part).

## What Step 3 is

Replace the two homegrown heuristics in `internal/silk/encoder.go` with faithful
ports of the libopus FLP/fixed reference, implemented **together as a set**
(single-state NSQ was already proven a no-op; do not split):

1. **Q3 — `silk_noise_shape_analysis_FLP`**
   Reference: `libopus/silk/float/noise_shape_analysis_FLP.c` (whole file, incl.
   the static helpers `warped_gain`, `warped_true2monic_coefs`, `limit_coefs`).
   Produces per-subframe noise-shaping coefficients:
   - `AR[k][shapingLPCOrder]` (float)            → quantize to `AR_Q13` (×8192)
   - `Tilt[k]`                                    → `Tilt_Q14` (×16384)
   - `LF_MA_shp[k]`, `LF_AR_shp[k]`               → packed `LF_shp_Q14` (see wrapper)
   - `HarmShapeGain[k]`                           → `HarmShapeGain_Q14` (×16384)
   - noise-shape `Gains[k]` (= sqrt(residual nrg), tweaked) — see "Gains" below
   - `quantOffsetType` (unvoiced sparseness branch)

2. **Q4 — `silk_NSQ_del_dec`** (the multi-state delayed-decision trellis)
   Reference: `libopus/silk/NSQ_del_dec.c` (whole file: `silk_NSQ_del_dec_c`,
   `silk_noise_shape_quantizer_del_dec`, `silk_nsq_del_dec_scale_states`).
   This is fixed-point. Port it faithfully in fixed-point Go using the existing
   Q-arith helpers (list below). It consumes the Q-format coefs from Q3 plus the
   gains, pitch lags, LTP coefs, Lambda, LTP_scale and emits `pulses` (Q0 int8
   range, stored as the Go `[]int16` pulses the rest of the encoder expects) and
   updates NSQ state.

The FLP→fixed conversion of the shaping coefs is in
`libopus/silk/float/wrappers_FLP.c` `silk_NSQ_wrapper_FLP` — follow it exactly
for the Q-scaling and the `LF_shp_Q14` packing:
```
LF_shp_Q14[i] = (float2int(LF_AR_shp[i]*16384) << 16) | (uint16)float2int(LF_MA_shp[i]*16384)
AR_Q13       = float2int(AR*8192)
Tilt_Q14     = float2int(Tilt*16384)
HarmShapeGain_Q14 = float2int(HarmShapeGain*16384)
Lambda_Q10   = float2int(Lambda*1024)
```
`silk_float2int` = round-to-nearest (Go: `int32(math.Floor(x + 0.5))`).

## Integration contract (keep Step 4 separate)

Replace **inside `internal/silk/encoder.go`** (and add a new file, e.g.
`internal/silk/nsq_del_dec.go` + `internal/silk/noise_shape.go`):

- `analyzeNoiseShape(...)` → call the new `silk_noise_shape_analysis_FLP` port.
  It needs the input signal **with `la_shape` look-ahead** (`la_shape = 3 or 5
  * fs_kHz` per complexity, see constants). The encoder currently passes only the
  frame; you must provide `la_shape` extra past+future samples. Use the available
  past input (the `pitchHist` / ltp history already kept) for the leading
  `la_shape` and the next-frame samples are NOT available, so window the trailing
  block against the frame end the way the current pitch code already handles
  look-ahead. **Simplest faithful approach:** mirror libopus by analysing windows
  `x_ptr = x - la_shape` advancing by `subfr_length`; build a buffer
  `[la_shape past] ++ [frame]` and clamp the last window's tail to frame end.
  Document any deviation in a comment.
- `closedLoopNSQWithRateScale(...)` → replace its inner per-sample quantizer with
  the `silk_NSQ_del_dec` port. **Keep** the existing gain-index selection / rate
  control (`selectRateControlPlan`, `excitationGainIndices`, `analysisGainIndices`)
  as the source of `Gains_Q16` for now — Step 4 will port `process_gains` properly.
  Convert the selected per-subframe gain indices → `Gains_Q16` via the existing
  `silkGainDequantQ16`.
- **Lambda**: compute `Lambda_Q10` from the `silk_process_gains_FLP` formula
  (`libopus/silk/float/process_gains_FLP.c` lines 92-100), it is a cheap
  deterministic weighted sum:
  ```
  Lambda = LAMBDA_OFFSET
         + LAMBDA_DELAYED_DECISIONS * nStatesDelayedDecision
         + LAMBDA_SPEECH_ACT        * speech_activity        (proxy 1.0)
         + LAMBDA_INPUT_QUALITY     * input_quality          (proxy 1.0)
         + LAMBDA_CODING_QUALITY    * coding_quality
         + LAMBDA_QUANT_OFFSET      * quant_offset
  ```
  with `quant_offset = silkQuantizationOffsetsQ10[signalType>>1][quantOffsetType]/1024`.
- **NSQ state** must persist across frames on the `Encoder` (mirror
  `silk_nsq_state`): `xq[]` (ltp_mem_length+frame_length int16), `sLTP_shp_Q14[]`,
  `sLPC_Q14[NSQ_LPC_BUF_LENGTH]`, `sAR2_Q14[MAX_SHAPE_LPC_ORDER]`, `sLF_AR_shp_Q14`,
  `sDiff_shp_Q14`, `lagPrev`, `prev_gain_Q16`, `sLTP_buf_idx`, `sLTP_shp_buf_idx`,
  `rewhite_flag`. Reset all in `(*Encoder).Reset`. These **replace** the ad-hoc
  `lpcState`/`ltpState`/`prevGainQ16` currently used by the old NSQ; migrate
  carefully or keep both if other code reads them (the decoder is unaffected — it
  only consumes the bitstream).

### Proxies for the scalar gain-control knobs (Q3)

The current encoder has no faithful VAD-bands / SNR tracking. Use these proxies
(tunable later via the scoreboard; document each in a comment):
- `speech_activity_Q8` = 256 (1.0)  [matches existing pitch-proxy]
- `input_quality_bands_Q15[0,1]` = 32768 (1.0) → `input_quality` = 1.0
- `SNR_dB_Q7`: derive from target bitrate. If the encoder lacks one, use the same
  value the existing gain/rate path implies; a reasonable proxy is the libopus
  `calc_SNR`-style mapping, but a constant ~ 30 dB (Q7 = 30*128) is acceptable as a
  starting point — **flag it** so we can tune.
- `predGain` (a.k.a. LTPredCodGain/predGain): compute from the LPC analysis as the
  prediction gain `sqrt(signal_energy / residual_energy)` using the existing
  `lpcResidualEnergy`. Used only for `BWExp` strength and the voiced SNR tweak.
- `LTPCorr` = existing `ltpCorrState` / the pitch `ltpCorr` for the frame.
- `warping_Q16` = `fs_kHz * round(WARPING_MULTIPLIER * 65536)` for complexity ≥ 4,
  else 0 (per `control_codec.c` complexity table).
- `nStatesDelayedDecision` & `shapingLPCOrder` & `la_shape`: per the complexity
  table below.

## Constants (from libopus define.h / tuning_parameters.h)

```
TYPE_UNVOICED=1 TYPE_VOICED=2  (Go: SignalTypeUnvoiced=1, SignalTypeVoiced=2 — already match)
MAX_NB_SUBFR=4  SUB_FRAME_LENGTH_MS=5  LA_SHAPE_MS=5
MAX_LPC_ORDER=16  NSQ_LPC_BUF_LENGTH=16  LTP_ORDER=5
MAX_SHAPE_LPC_ORDER=24  HARM_SHAPE_FIR_TAPS=3
DECISION_DELAY=40  QUANT_LEVEL_ADJUST_Q10=80
SHAPE_LPC_WIN_MAX=15*MAX_FS_KHZ
shapeWinLength = SUB_FRAME_LENGTH_MS*fs_kHz + 2*la_shape

WARPING_MULTIPLIER=0.015  SHAPE_WHITE_NOISE_FRACTION=3e-5  BANDWIDTH_EXPANSION=0.94
FIND_PITCH_WHITE_NOISE_FRACTION=1e-3
BG_SNR_DECR_dB=2.0  HARM_SNR_INCR_dB=2.0  ENERGY_VARIATION_THRESHOLD_QNT_OFFSET=0.6
HARMONIC_SHAPING=0.3  HIGH_RATE_OR_LOW_QUALITY_HARMONIC_SHAPING=0.2
HP_NOISE_COEF=0.25  HARM_HP_NOISE_COEF=0.35
LOW_FREQ_SHAPING=4.0  LOW_QUALITY_LOW_FREQ_SHAPING_DECR=0.5  SUBFR_SMTH_COEF=0.4
USE_HARM_SHAPING=1  MIN_QGAIN_DB=2 (verify in define.h)
LAMBDA_OFFSET=1.2 LAMBDA_SPEECH_ACT=-0.2 LAMBDA_DELAYED_DECISIONS=-0.05
LAMBDA_INPUT_QUALITY=-0.1 LAMBDA_CODING_QUALITY=-0.2 LAMBDA_QUANT_OFFSET=0.8
```

Complexity table (`control_codec.c` silk_setup_complexity), index by `e.complexity`:
| cx | shapingLPCOrder | la_shape | nStatesDelayedDecision | warping |
|----|-----------------|----------|------------------------|---------|
| <1 | 12 | 3*fs | 1 | 0 |
| <2 | 14 | 5*fs | 1 | 0 |
| <3 | 12 | 3*fs | 2 | 0 |
| <4 | 14 | 5*fs | 2 | 0 |
| <6 | 16 | 5*fs | 2 | warp |
| <8 | 20 | 5*fs | 3 | warp |
| ≥8 | 24 | 5*fs | 4 (MAX_DEL_DEC_STATES) | warp |

Default encoder complexity is 5 → shapingLPCOrder 16, 2 states, warping on.

## Reuse — these Go helpers already exist (do NOT re-port)

Q-arith (`internal/silk/*.go`): `silkSMLAWB silkSMULWB silkSMULWW silkSMLAWW
silkSMLABB silkSMULBB silkSMMUL silkRShiftRound silkAddSat32 silkLShiftSat32
silkSAT16 silkInverse32VarQ silkDIV32VarQ silkAbs32 silkCLZ32 silkBWExpander32
silkLPCAnalysisFilter silkGainDequantQ16 clamp16`.

FLP (`internal/silk/pitch_flp.go`): `silkApplySineWindowFLP silkAutocorrelationFLP
silkSchurFLP silkK2aFLP silkBwexpanderFLP silkInnerProductFLP silkFloat2Short`.

Tables (`internal/silk/tables.go`): `silkQuantizationOffsetsQ10`
`silkLTPScalesTable`.

## Missing helpers to add

- `silk_warped_autocorrelation_FLP` (`libopus/silk/float/warped_autocorrelation_FLP.c`)
- `silk_energy_FLP` (sum of squares), `silk_log2` (= log2; libopus `silk_log2` is
  `3.32192809489f*log10`), `silk_sigmoid` (= `1/(1+exp(-x))`)
- `silk_RAND` macro: `(907633515 + state*196314165)` (int32 overflow wrap)
- `silk_noise_shape_quantizer_short_prediction`: the inner LPC dot product
  (Q10 accumulation, order rounded) — see `NSQ.c`/`NSQ_del_dec.c`. Port the plain
  C version (no SIMD).
- 32-bit overflow-wrapping ops used by NSQ_del_dec: `silk_ADD32_ovflw`,
  `silk_SUB32_ovflw`, `silk_ADD_SAT32`, `silk_SUB_SAT32`, `silk_SUB_LSHIFT32`,
  `silk_LIMIT_32`, `silk_RSHIFT_ROUND`, `silk_LSHIFT32`, `silk_RSHIFT32`,
  `silk_SMLAWT` (use high 16 bits), `silk_int32_MAX`. Add any missing ones.

## Tests + verification (Director will run these)

Implement, then run (PowerShell for opusref/CGO per repo convention):
1. `go build ./... && go vet ./... && go test ./...`  (must be green; official
   12/12 decoder vectors unaffected — encoder change only).
2. Unit tests in a new `internal/silk/nsq_del_dec_test.go`:
   - del-dec NSQ on a known excitation reproduces a self-decoding round-trip
     (encode pulses → existing SILK decoder → finite, correlated output).
   - voiced periodic tone: pulses are sparse and self-decode with positive SNR.
   - warped vs non-warped shaping both run without panics across fs 8/12/16 kHz.
3. Scoreboard (the exit criterion):
   `go test -tags opusref -run TestOpusSILKABAgainstLibopusEncoder .`
   Compare `gap_SNR_matched` for unvoiced-noise and onset vs the current
   baseline. **Exit target: within -2 dB of libopus on unvoiced-noise & onset**
   while `ratio_bytes_matched` stays ~ comparable.

## Notes / pitfalls

- Encoder signal is `[-1,1]` float; libopus NSQ input `x16` is int16. Scale input
  by ×32768 before NSQ (the wrapper does `float2int(x[i])` on a signal already in
  int16 domain). Mirror the existing pitch code's ×32768 convention.
- The decoder is untouched and is the oracle — as long as pulses + signalled
  gains/LPC/LTP are self-consistent, decode works. enc-internal NSQ state need not
  match the decoder bit-exactly.
- Keep `encodePulses` / shell coding path unchanged; the del-dec NSQ only changes
  *which* pulses are produced.
- Do not delete the old `simpleNSQ`/`closedLoopNSQ` until the new path is proven;
  guard the switch so it is easy to A/B.
- This is large. If you must stage it, land Q3 (shaping coefs feeding the OLD
  quantizer) and Q4 (del-dec) in one branch but you may commit in two reviewable
  commits — but both must be present before scoreboard.
