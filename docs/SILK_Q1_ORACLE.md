# SILK Q1 LPC/NLSF Oracle

This note maps the libopus 1.6.1 SILK encoder LPC/NLSF analysis to the Go
implementation and records the exact boundary exercised by the `opusref` test.
It does not claim complete encoder compatibility.

## C-to-Go mapping

| libopus 1.6.1 source | C responsibility | Go implementation | Oracle checkpoint |
|---|---|---|---|
| `silk/float/encode_frame_FLP.c` | Calls prediction analysis after pitch residual and gain analysis | `internal/silk/encoder.go`: `encodeRangeFrame` | Fixture state and call order |
| `silk/float/find_pred_coefs_FLP.c` | Builds gain-scaled, subframe-stacked `LPC_in_pre`; applies LTP filtering for voiced input | `internal/silk/lpc_in_pre.go`: `buildLPCInPre`; `internal/silk/encoder.go`: active-frame domain setup | `LPC_in_pre` |
| `silk/float/find_LPC_FLP.c` | Full-frame and last-half Burg analysis; A2NLSF conversion; `k=3..0` interpolation search | `internal/silk/find_lpc.go`: `silkFindLPCFLP`, `silkLPCAnalysisFilterFLP32` | A2NLSF result and interpolation factor |
| `silk/float/burg_modified_FLP.c` | Modified Burg recursion and residual energy | `internal/silk/burg.go`: `silkBurgModifiedFLP`, float-input energy and inner-product helpers | Burg coefficients and residual energy |
| `silk/float/wrappers_FLP.c` | Float/fixed A2NLSF and NLSF2A boundaries; process wrapper | `internal/silk/burg.go`: `silkA2NLSFFLP`; `internal/silk/find_lpc.go`: `silkNLSF2AFLP`; `internal/silk/nlsf_encode.go`: `silkProcessNLSFs` | A2NLSF and `PredCoef_Q12` |
| `silk/process_NLSFs.c` | Laroia weights, interpolation weight merge, NLSF quantization, predictor reconstruction | `internal/silk/nlsf_encode.go`: `silkProcessNLSFs`, `silkNLSFWeightsLaroia` | weights, quantized/interpolated NLSF, `PredCoef_Q12` |
| `silk/NLSF_encode.c` | Stabilization, stage-1 VQ, residual delayed-decision quantization and RD selection | `internal/silk/nlsf_encode.go`: `silkNLSFEncode`, `silkNLSFDelDecQuant`, `nlsfUnpack`; `internal/silk/decoder.go`: `silkNLSFStabilize` | stabilized target, VQ errors, stage-1 index, residual indices, quantized NLSF |
| `silk/interpolate.c` | Q2 interpolation of previous and current NLSFs | `internal/silk/find_lpc.go`: `silkInterpolate` | interpolated NLSF |
| `silk/control_codec.c` | Complexity-dependent `useInterpolatedNLSFs` and survivor count | `internal/silk/noise_shape.go`: `silkComplexityConfig`; `internal/silk/nlsf_encode.go`: `nlsfQuantSurvivors` | complexity 3 off and complexity 4+ on fixtures |

The relevant C call chain is:

`silk_Encode` → `silk_control_codec` / `silk_setup_complexity` →
`silk_encode_frame_FLP` → `silk_find_pred_coefs_FLP` →
`silk_find_LPC_FLP` → `silk_process_NLSFs_FLP` →
`silk_process_NLSFs` → `silk_NLSF_encode`.

## Oracle boundary

`internal/cgoref/silk_q1_opusref.go` is compiled only with `//go:build
opusref`. It links the libopus static archive so the test can call internal SILK
symbols. Normal builds remain Pure Go.

`TestSILKQ1LPCNLSFOracle` supplies identical `float32` input and identical
encoder state at the Q1 boundary to C and Go. It compares every floating-point
checkpoint by exact `float32` bits and every fixed-point checkpoint by exact
integer value. No epsilon is used. On failure it reports the fixture, sample
rate, channel/mode label, frame number, first differing index, C value, Go
value, and difference.

The following checkpoints are compared in execution order:

1. `LPC_in_pre`
2. Full-frame and second-half Burg LPC coefficients, plus residual energy
3. A2NLSF result and interpolation factor
4. NLSF weights, stabilized target, and stage-1 VQ errors
5. residual entropy tables, candidate delayed-decision path, and candidate RD
6. stage-1 index and residual indices
7. quantized and interpolated NLSF Q15
8. both `PredCoef_Q12` sets

## Verified fixture matrix

The following deterministic fixtures matched libopus 1.6.1 exactly at all
checkpoints on 2026-09-13:

| Fixture | Rate | Domain | Frame | Subframes | Signal | Complexity | Interpolation factor |
|---|---:|---|---:|---:|---|---:|---:|
| `nb-unvoiced-reset-10ms` | 8 kHz | mono SILK | reset | 2 | unvoiced | 5 | 4 |
| `mb-voiced-reset-20ms` | 12 kHz | mono SILK | reset | 4 | voiced | 5 | 4 |
| `wb-unvoiced-steady-20ms-no-interp` | 16 kHz | mono SILK | steady | 4 | unvoiced | 3 | 4 |
| `nb-voiced-steady-20ms-interp` | 8 kHz | mono SILK | steady | 4 | voiced | 5 | 0 |
| `mb-unvoiced-steady-10ms` | 12 kHz | mono SILK | steady | 2 | unvoiced | 5 | 4 |
| `wb-voiced-steady-20ms-interp` | 16 kHz | mono SILK | steady | 4 | voiced | 8 | 3 |
| `wb-stereo-mid-steady-20ms` | 16 kHz | stereo mid SILK | steady | 4 | voiced | 5 | 2 |
| `wb-hybrid-low-reset-20ms` | 16 kHz | hybrid low band | reset | 4 | unvoiced | 5 | 4 |
| `nb-unvoiced-steady-c0` | 8 kHz | mono SILK | steady | 4 | unvoiced | 0 | 4 |
| `mb-voiced-steady-c1` | 12 kHz | mono SILK | steady | 4 | voiced | 1 | 4 |
| `wb-unvoiced-steady-c2` | 16 kHz | mono SILK | steady | 4 | unvoiced | 2 | 4 |
| `mb-voiced-steady-c4` | 12 kHz | mono SILK | steady | 4 | voiced | 4 | 2 |
| `wb-unvoiced-steady-c6` | 16 kHz | mono SILK | steady | 4 | unvoiced | 6 | 0 |

## Corrections established by the oracle

- `LPC_in_pre`, Burg outputs, LPC filtering, and residual-energy boundaries now
  round at the corresponding `silk_float` locations and preserve C operation
  order.
- Float-to-fixed conversion rounds a `float32` value to nearest-even, matching
  the libopus 1.6.1 x86_64 `float2int` boundary.
- A2NLSF output is no longer stabilized before `silk_NLSF_encode`; the first C
  stabilization point is inside NLSF encoding.
- NLSF stabilization and delayed-decision arithmetic use the C bounds, tie
  order, fixed-width intermediates, shifts, and fallback ordering.
- The NB/MB and WB residual-rate tables now contain all 72 libopus values in
  their original positions.
- Complexity levels 0–3 disable NLSF interpolation and levels 4–10 enable it;
  the frame-length and reset gates remain in `silkFindLPCFLP`.
- Active stereo components and hybrid low-band frames now construct the same
  gain-scaled/LTP-filtered Q1 input domain shape as mono frames instead of
  falling back to raw signal analysis.

## Remaining boundary

The oracle deliberately injects identical gains, pitch lags, LTP coefficients,
previous NLSF, and activity state into C and Go. It therefore verifies the Q1
implementation from `LPC_in_pre` onward, but not whether the earlier Go pitch,
LTP, gain, stereo analysis, or hybrid resampling stages select the same state as
the full libopus encoder. Stereo and hybrid have direct Q1-domain fixtures, and
the production encoder separately enables their domain-construction path. Full
encoder-state parity for those modes remains to be measured in their respective
later work.

The full `opusref` comparison retains the known speech-like-harmonic loudness
failure. Relative to base commit `b70e7a0`, its 8 kHz subtest moved from the
threshold edge (`-1.50 dB`, PASS, 207 own bytes) to `-1.59 dB` (FAIL, 205 own
bytes). The change starts with correction of the libopus residual-rate table;
reverting that table makes the Q1 residual-index oracle fail. No threshold or
fixture-specific exception is applied. Restoring the quality margin requires
work in the upstream/downstream gain and rate-control loop, outside this Q1
boundary.
