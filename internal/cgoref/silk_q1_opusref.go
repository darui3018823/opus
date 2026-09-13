//go:build opusref

package cgoref

/*
#cgo windows LDFLAGS: C:/msys64/mingw64/lib/libopus.a
#cgo linux LDFLAGS: -Wl,-Bstatic -lopus -Wl,-Bdynamic -lm

#include <stdint.h>
#include <stdlib.h>
#include <string.h>
#include <float.h>

typedef int8_t opus_int8;
typedef uint8_t opus_uint8;
typedef int16_t opus_int16;
typedef int32_t opus_int32;
typedef float silk_float;

typedef struct {
    const opus_int16 nVectors;
    const opus_int16 order;
    const opus_int16 quantStepSize_Q16;
    const opus_int16 invQuantStepSize_Q6;
    const opus_uint8 *CB1_NLSF_Q8;
    const opus_int16 *CB1_Wght_Q9;
    const opus_uint8 *CB1_iCDF;
    const opus_uint8 *pred_Q8;
    const opus_uint8 *ec_sel;
    const opus_uint8 *ec_iCDF;
    const opus_uint8 *ec_Rates_Q5;
    const opus_int16 *deltaMin_Q15;
} silk_NLSF_CB_struct;

extern const silk_NLSF_CB_struct silk_NLSF_CB_WB;
extern const silk_NLSF_CB_struct silk_NLSF_CB_NB_MB;

extern silk_float silk_burg_modified_FLP(silk_float *, const silk_float *, silk_float, int, int, int, int);
extern void silk_A2NLSF_FLP(opus_int16 *, const silk_float *, int);
extern void silk_NLSF2A(opus_int16 *, const opus_int16 *, int, int);
extern void silk_NLSF2A_FLP(silk_float *, const opus_int16 *, int, int);
extern void silk_interpolate(opus_int16 *, const opus_int16 *, const opus_int16 *, int, int);
extern void silk_LPC_analysis_filter_FLP(silk_float *, const silk_float *, const silk_float *, int, int);
extern double silk_energy_FLP(const silk_float *, int);
extern void silk_NLSF_VQ_weights_laroia(opus_int16 *, const opus_int16 *, int);
extern opus_int32 silk_NLSF_encode(opus_int8 *, opus_int16 *, const silk_NLSF_CB_struct *, const opus_int16 *, int, int, int);

typedef struct {
    silk_float burg_a[16];
    silk_float burg_residual;
    opus_int16 a2nlsf_q15[16];
    int interp_factor;
    int stage1_index;
    opus_int8 residual_indices[16];
    opus_int16 quantized_nlsf_q15[16];
    opus_int16 interpolated_nlsf_q15[16];
    opus_int16 pred0_q12[16];
    opus_int16 pred1_q12[16];
} go_silk_q1_result;

static int go_silk_q1_analyze(
    const silk_float *x,
    int x_len,
    int subfr_length,
    int nb_subfr,
    int order,
    silk_float min_inv_gain,
    int use_interpolated,
    int first_after_reset,
    const opus_int16 *prev_nlsf_q15,
    int speech_activity_q8,
    int n_survivors,
    int signal_type,
    go_silk_q1_result *out
) {
    silk_float full_a[16], candidate_a[16], interp_a[16];
    silk_float lpc_res[384];
    opus_int16 target_q15[16], interp_q15[16], weights_q2[16], interp_weights_q2[16];
    opus_int8 indices[17];
    const silk_NLSF_CB_struct *cb;
    silk_float res_nrg, res_nrg_2nd, res_nrg_interp;
    int i, k;

    if (!x || !prev_nlsf_q15 || !out || (order != 10 && order != 16) ||
        nb_subfr <= 0 || subfr_length <= order || x_len < subfr_length * nb_subfr ||
        subfr_length * nb_subfr > 384) {
        return -1;
    }
    memset(out, 0, sizeof(*out));
    memset(target_q15, 0, sizeof(target_q15));
    cb = order == 16 ? &silk_NLSF_CB_WB : &silk_NLSF_CB_NB_MB;

    res_nrg = silk_burg_modified_FLP(full_a, x, min_inv_gain, subfr_length, nb_subfr, order, 0);
    out->burg_residual = res_nrg;
    memcpy(out->burg_a, full_a, order * sizeof(silk_float));
    out->interp_factor = 4;

    if (use_interpolated && !first_after_reset && nb_subfr == 4) {
        res_nrg -= silk_burg_modified_FLP(candidate_a, x + 2 * subfr_length,
            min_inv_gain, subfr_length, 2, order, 0);
        silk_A2NLSF_FLP(target_q15, candidate_a, order);
        res_nrg_2nd = FLT_MAX;
        for (k = 3; k >= 0; k--) {
            silk_interpolate(interp_q15, prev_nlsf_q15, target_q15, k, order);
            silk_NLSF2A_FLP(interp_a, interp_q15, order, 0);
            silk_LPC_analysis_filter_FLP(lpc_res, interp_a, x, 2 * subfr_length, order);
            res_nrg_interp = (silk_float)(
                silk_energy_FLP(lpc_res + order, subfr_length - order) +
                silk_energy_FLP(lpc_res + order + subfr_length, subfr_length - order));
            if (res_nrg_interp < res_nrg) {
                res_nrg = res_nrg_interp;
                out->interp_factor = k;
            } else if (res_nrg_interp > res_nrg_2nd) {
                break;
            }
            res_nrg_2nd = res_nrg_interp;
        }
    }
    if (out->interp_factor == 4) {
        silk_A2NLSF_FLP(target_q15, full_a, order);
    }
    memcpy(out->a2nlsf_q15, target_q15, order * sizeof(opus_int16));

    silk_NLSF_VQ_weights_laroia(weights_q2, target_q15, order);
    if (use_interpolated && out->interp_factor < 4) {
        opus_int16 i_sqr_q15 = (opus_int16)(out->interp_factor * out->interp_factor << 11);
        silk_interpolate(interp_q15, prev_nlsf_q15, target_q15, out->interp_factor, order);
        silk_NLSF_VQ_weights_laroia(interp_weights_q2, interp_q15, order);
        for (i = 0; i < order; i++) {
            weights_q2[i] = (opus_int16)((weights_q2[i] >> 1) +
                (((int32_t)interp_weights_q2[i] * i_sqr_q15) >> 16));
        }
    }

    {
        int nlsf_mu_q20 = 3146 + (int)(((int64_t)-268435 * (int16_t)speech_activity_q8) >> 16);
        if (nb_subfr == 2) {
            nlsf_mu_q20 += nlsf_mu_q20 >> 1;
        }
        silk_NLSF_encode(indices, target_q15, cb, weights_q2,
            nlsf_mu_q20, n_survivors, signal_type);
    }
    out->stage1_index = indices[0];
    memcpy(out->residual_indices, indices + 1, order * sizeof(opus_int8));
    memcpy(out->quantized_nlsf_q15, target_q15, order * sizeof(opus_int16));

    silk_NLSF2A(out->pred1_q12, target_q15, order, 0);
    if (use_interpolated && out->interp_factor < 4) {
        silk_interpolate(out->interpolated_nlsf_q15, prev_nlsf_q15, target_q15,
            out->interp_factor, order);
        silk_NLSF2A(out->pred0_q12, out->interpolated_nlsf_q15, order, 0);
    } else {
        memcpy(out->interpolated_nlsf_q15, target_q15, order * sizeof(opus_int16));
        memcpy(out->pred0_q12, out->pred1_q12, order * sizeof(opus_int16));
    }
    return 0;
}

static int go_silk_build_lpc_in_pre(
    silk_float *out,
    const silk_float *x,
    int x_len,
    int frame_start,
    const int *subframe_lengths,
    const silk_float *inv_gains,
    const silk_float *ltp_coefs,
    const int *pitch_lags,
    int nb_subfr,
    int order,
    int voiced
) {
    int sf, i, j, dst = 0, cum = 0;
    if (!out || !x || !subframe_lengths || !inv_gains || nb_subfr <= 0) return -1;
    for (sf = 0; sf < nb_subfr; sf++) {
        int sub_len = subframe_lengths[sf];
        int x_ptr = frame_start + cum - order;
        silk_float inv_gain = inv_gains[sf];
        for (i = 0; i < sub_len + order; i++) {
            int pos = x_ptr + i;
            silk_float v = pos >= 0 && pos < x_len ? x[pos] : 0.0f;
            if (voiced && pitch_lags && ltp_coefs && pitch_lags[sf] > 0) {
                int lag_ptr = x_ptr - pitch_lags[sf] + i;
                for (j = 0; j < 5; j++) {
                    int lag_pos = lag_ptr + 2 - j;
                    silk_float delayed = lag_pos >= 0 && lag_pos < x_len ? x[lag_pos] : 0.0f;
                    v -= ltp_coefs[sf * 5 + j] * delayed;
                }
            }
            out[dst + i] = v * inv_gain;
        }
        dst += sub_len + order;
        cum += sub_len;
    }
    return dst;
}
*/
import "C"

import (
	"fmt"
	"unsafe"
)

// SILKQ1Input is the complete state/input boundary consumed by the libopus
// SILK LPC and NLSF analysis oracle.
type SILKQ1Input struct {
	LPCInPre         []float32
	SubframeLength   int
	Subframes        int
	Order            int
	MinInvGain       float32
	UseInterpolated  bool
	FirstAfterReset  bool
	PrevNLSFQ15      []int16
	SpeechActivityQ8 int
	NLSFSurvivors    int
	SignalType       int
}

// SILKQ1Result exposes the encoder-side Q1 checkpoints from libopus 1.6.1.
type SILKQ1Result struct {
	BurgA               []float32
	BurgResidual        float32
	A2NLSFQ15           []int16
	InterpolationFactor int
	Stage1Index         int
	ResidualIndices     []int
	QuantizedNLSFQ15    []int16
	InterpolatedNLSFQ15 []int16
	PredCoef0Q12        []int16
	PredCoef1Q12        []int16
}

// SILKQ1Analyze runs the scalar libopus SILK LPC/NLSF stages on one frame.
func SILKQ1Analyze(in SILKQ1Input) (SILKQ1Result, error) {
	if len(in.LPCInPre) == 0 || len(in.PrevNLSFQ15) < in.Order {
		return SILKQ1Result{}, fmt.Errorf("invalid SILK Q1 input")
	}
	var out C.go_silk_q1_result
	code := C.go_silk_q1_analyze(
		(*C.silk_float)(unsafe.Pointer(&in.LPCInPre[0])), C.int(len(in.LPCInPre)),
		C.int(in.SubframeLength), C.int(in.Subframes), C.int(in.Order), C.silk_float(in.MinInvGain),
		boolToCInt(in.UseInterpolated), boolToCInt(in.FirstAfterReset),
		(*C.opus_int16)(unsafe.Pointer(&in.PrevNLSFQ15[0])), C.int(in.SpeechActivityQ8),
		C.int(in.NLSFSurvivors), C.int(in.SignalType), &out,
	)
	if code != 0 {
		return SILKQ1Result{}, fmt.Errorf("libopus SILK Q1 oracle rejected input: %d", int(code))
	}
	result := SILKQ1Result{
		BurgA:               make([]float32, in.Order),
		BurgResidual:        float32(out.burg_residual),
		A2NLSFQ15:           make([]int16, in.Order),
		InterpolationFactor: int(out.interp_factor),
		Stage1Index:         int(out.stage1_index),
		ResidualIndices:     make([]int, in.Order),
		QuantizedNLSFQ15:    make([]int16, in.Order),
		InterpolatedNLSFQ15: make([]int16, in.Order),
		PredCoef0Q12:        make([]int16, in.Order),
		PredCoef1Q12:        make([]int16, in.Order),
	}
	for i := 0; i < in.Order; i++ {
		result.BurgA[i] = float32(out.burg_a[i])
		result.A2NLSFQ15[i] = int16(out.a2nlsf_q15[i])
		result.ResidualIndices[i] = int(int8(out.residual_indices[i]))
		result.QuantizedNLSFQ15[i] = int16(out.quantized_nlsf_q15[i])
		result.InterpolatedNLSFQ15[i] = int16(out.interpolated_nlsf_q15[i])
		result.PredCoef0Q12[i] = int16(out.pred0_q12[i])
		result.PredCoef1Q12[i] = int16(out.pred1_q12[i])
	}
	return result, nil
}

// SILKBuildLPCInPre applies libopus' float LPC_in_pre construction order.
func SILKBuildLPCInPre(x []float32, subframeLengths []int, invGains []float32, ltpCoefs []float32, pitchLags []int, order int, voiced bool) ([]float32, error) {
	if len(x) == 0 || len(subframeLengths) == 0 || len(invGains) < len(subframeLengths) {
		return nil, fmt.Errorf("invalid LPC_in_pre input")
	}
	lengths := make([]C.int, len(subframeLengths))
	lags := make([]C.int, len(subframeLengths))
	frameLen := 0
	for i := range subframeLengths {
		lengths[i] = C.int(subframeLengths[i])
		frameLen += subframeLengths[i]
		if i < len(pitchLags) {
			lags[i] = C.int(pitchLags[i])
		}
	}
	coefs := ltpCoefs
	if len(coefs) < len(subframeLengths)*5 {
		coefs = make([]float32, len(subframeLengths)*5)
		copy(coefs, ltpCoefs)
	}
	out := make([]float32, frameLen+order*len(subframeLengths))
	code := C.go_silk_build_lpc_in_pre(
		(*C.silk_float)(unsafe.Pointer(&out[0])), (*C.silk_float)(unsafe.Pointer(&x[0])), C.int(len(x)),
		C.int(len(x)-frameLen), (*C.int)(unsafe.Pointer(&lengths[0])),
		(*C.silk_float)(unsafe.Pointer(&invGains[0])), (*C.silk_float)(unsafe.Pointer(&coefs[0])),
		(*C.int)(unsafe.Pointer(&lags[0])), C.int(len(subframeLengths)), C.int(order), boolToCInt(voiced),
	)
	if code != C.int(len(out)) {
		return nil, fmt.Errorf("libopus LPC_in_pre oracle returned %d, want %d", int(code), len(out))
	}
	return out, nil
}
