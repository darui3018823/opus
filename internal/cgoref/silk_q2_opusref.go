//go:build opusref

package cgoref

/*
#include <stdint.h>
#include <string.h>

typedef int8_t opus_int8;
typedef int16_t opus_int16;
typedef int32_t opus_int32;
typedef float silk_float;

extern void silk_find_LTP_FLP(silk_float *, silk_float *, const silk_float *, const int *, int, int, int);
extern int silk_pitch_analysis_core_FLP(const silk_float *, int *, opus_int16 *, opus_int8 *, silk_float *, int, silk_float, silk_float, int, int, int, int);
extern void silk_apply_sine_window_FLP(silk_float *, const silk_float *, int, int);
extern void silk_autocorrelation_FLP(silk_float *, const silk_float *, int, int, int);
extern silk_float silk_schur_FLP(silk_float *, const silk_float *, int);
extern void silk_k2a_FLP(silk_float *, const silk_float *, opus_int32);
extern void silk_bwexpander_FLP(silk_float *, int, silk_float);
extern void silk_LPC_analysis_filter_FLP(silk_float *, const silk_float *, const silk_float *, int, int);

typedef struct {
    int pitch_out[4];
    opus_int16 lag_index;
    opus_int8 contour_index;
    silk_float ltp_corr;
    int unvoiced;
} go_silk_pitch_core_result;

static void go_silk_pitch_core(const silk_float *frame, silk_float ltp_corr_in, int prev_lag,
                               silk_float thres1, silk_float thres2, int fs_khz, int complexity, int nb_subfr,
                               go_silk_pitch_core_result *out) {
    memset(out, 0, sizeof(*out));
    out->ltp_corr = ltp_corr_in;
    out->unvoiced = silk_pitch_analysis_core_FLP(frame, out->pitch_out, &out->lag_index, &out->contour_index,
                                                 &out->ltp_corr, prev_lag, thres1, thres2, fs_khz, complexity, nb_subfr, 0);
}

typedef struct {
    silk_float auto_corr[17];
    silk_float refl_coef[16];
    silk_float A[16];
    silk_float res_nrg;
    silk_float pred_gain;
    silk_float thrhld;
    go_silk_pitch_core_result core;
} go_silk_find_pitch_result;

// go_silk_find_pitch_lags replays silk_find_pitch_lags_FLP on an explicit
// x_buf of buf_len = ltp_mem_length + frame_length + la_pitch samples.
static void go_silk_find_pitch_lags(const silk_float *x_buf, int buf_len, silk_float *res,
                                    int la_pitch, int pitch_lpc_win_length,
                                    int order, int fs_khz, int nb_subfr, int pe_complexity,
                                    int speech_activity_q8, int prev_signal_type, int input_tilt_q15,
                                    int pitch_est_threshold_q16, int prev_lag, silk_float ltp_corr_in, int run_core,
                                    go_silk_find_pitch_result *out) {
    silk_float Wsig[24 * 16];
    const silk_float *x_buf_ptr;
    silk_float *Wsig_ptr;
    memset(out, 0, sizeof(*out));

    x_buf_ptr = x_buf + buf_len - pitch_lpc_win_length;
    Wsig_ptr = Wsig;
    silk_apply_sine_window_FLP(Wsig_ptr, x_buf_ptr, 1, la_pitch);
    Wsig_ptr += la_pitch;
    x_buf_ptr += la_pitch;
    memcpy(Wsig_ptr, x_buf_ptr, (pitch_lpc_win_length - (la_pitch << 1)) * sizeof(silk_float));
    Wsig_ptr += pitch_lpc_win_length - (la_pitch << 1);
    x_buf_ptr += pitch_lpc_win_length - (la_pitch << 1);
    silk_apply_sine_window_FLP(Wsig_ptr, x_buf_ptr, 2, la_pitch);

    silk_autocorrelation_FLP(out->auto_corr, Wsig, pitch_lpc_win_length, order + 1, 0);
    out->auto_corr[0] += out->auto_corr[0] * 1e-3f + 1;
    out->res_nrg = silk_schur_FLP(out->refl_coef, out->auto_corr, order);
    out->pred_gain = out->auto_corr[0] / (out->res_nrg > 1.0f ? out->res_nrg : 1.0f);
    silk_k2a_FLP(out->A, out->refl_coef, order);
    silk_bwexpander_FLP(out->A, order, 0.99f);
    silk_LPC_analysis_filter_FLP(res, out->A, x_buf, buf_len, order);

    if (run_core) {
        silk_float thrhld = 0.6f;
        thrhld -= 0.004f * order;
        thrhld -= 0.1f * speech_activity_q8 * (1.0f / 256.0f);
        thrhld -= 0.15f * (prev_signal_type >> 1);
        thrhld -= 0.1f * input_tilt_q15 * (1.0f / 32768.0f);
        out->thrhld = thrhld;
        go_silk_pitch_core(res, ltp_corr_in, prev_lag, pitch_est_threshold_q16 / 65536.0f, thrhld,
                           fs_khz, pe_complexity, nb_subfr, &out->core);
    }
}
extern void silk_quant_LTP_gains_FLP(silk_float *, opus_int8 *, opus_int8 *, opus_int32 *, silk_float *, const silk_float *, const silk_float *, int, int, int);

// go_silk_find_ltp runs silk_find_LTP_FLP on a residual buffer. frame_off is the
// index of the first frame sample inside r; the samples before it are the
// history that the lagged columns read.
static void go_silk_find_ltp(silk_float *XX, silk_float *xX, const silk_float *r, int frame_off,
                             const int *lag, int subfr_length, int nb_subfr) {
    silk_find_LTP_FLP(XX, xX, r + frame_off, lag, subfr_length, nb_subfr, 0);
}

static void go_silk_quant_ltp_gains(silk_float *B, opus_int8 *cbk_index, opus_int8 *periodicity_index,
                                    opus_int32 *sum_log_gain_Q7, silk_float *pred_gain_dB,
                                    const silk_float *XX, const silk_float *xX, int subfr_len, int nb_subfr) {
    silk_quant_LTP_gains_FLP(B, cbk_index, periodicity_index, sum_log_gain_Q7, pred_gain_dB, XX, xX, subfr_len, nb_subfr, 0);
}
*/
import "C"

import (
	"fmt"
	"unsafe"
)

// SILKFindLTP runs libopus silk_find_LTP_FLP. r holds the LPC residual with
// frameOff history samples before the frame; lags has one entry per subframe.
// It returns the per-subframe 5x5 correlation matrices and 5-vectors.
func SILKFindLTP(r []float32, frameOff int, lags []int, subfrLength int) (xx []float32, xX []float32, err error) {
	nbSubfr := len(lags)
	if nbSubfr < 1 || nbSubfr > 4 || frameOff < 0 || len(r) < frameOff+nbSubfr*subfrLength+5 {
		return nil, nil, fmt.Errorf("invalid find_LTP input")
	}
	for k, lag := range lags {
		if frameOff+k*subfrLength-(lag+2)-4 < 0 {
			return nil, nil, fmt.Errorf("lag %d in subframe %d exceeds history %d", lag, k, frameOff)
		}
	}
	cLags := make([]C.int, nbSubfr)
	for i, lag := range lags {
		cLags[i] = C.int(lag)
	}
	xx = make([]float32, nbSubfr*25)
	xX = make([]float32, nbSubfr*5)
	C.go_silk_find_ltp(
		(*C.silk_float)(unsafe.Pointer(&xx[0])), (*C.silk_float)(unsafe.Pointer(&xX[0])),
		(*C.silk_float)(unsafe.Pointer(&r[0])), C.int(frameOff),
		(*C.int)(unsafe.Pointer(&cLags[0])), C.int(subfrLength), C.int(nbSubfr),
	)
	return xx, xX, nil
}

// SILKQuantLTPGainsResult holds the outputs of silk_quant_LTP_gains_FLP.
type SILKQuantLTPGainsResult struct {
	B                []float32 // quantized LTP taps, nb_subfr * 5
	CodebookIndices  []int
	PeriodicityIndex int
	SumLogGainQ7     int32
	PredGainDB       float32
}

// SILKQuantLTPGains runs libopus silk_quant_LTP_gains_FLP (the float wrapper
// around the fixed-point silk_quant_LTP_gains and silk_VQ_WMat_EC).
func SILKQuantLTPGains(xx, xX []float32, subfrLength, nbSubfr int, sumLogGainQ7 int32) (SILKQuantLTPGainsResult, error) {
	if nbSubfr < 1 || nbSubfr > 4 || len(xx) < nbSubfr*25 || len(xX) < nbSubfr*5 {
		return SILKQuantLTPGainsResult{}, fmt.Errorf("invalid quant_LTP_gains input")
	}
	b := make([]float32, nbSubfr*5)
	cbk := make([]C.opus_int8, nbSubfr)
	var per C.opus_int8
	sum := C.opus_int32(sumLogGainQ7)
	var predGain C.silk_float
	C.go_silk_quant_ltp_gains(
		(*C.silk_float)(unsafe.Pointer(&b[0])), &cbk[0], &per, &sum, &predGain,
		(*C.silk_float)(unsafe.Pointer(&xx[0])), (*C.silk_float)(unsafe.Pointer(&xX[0])),
		C.int(subfrLength), C.int(nbSubfr),
	)
	res := SILKQuantLTPGainsResult{
		B:                b,
		CodebookIndices:  make([]int, nbSubfr),
		PeriodicityIndex: int(per),
		SumLogGainQ7:     int32(sum),
		PredGainDB:       float32(predGain),
	}
	for i := range cbk {
		res.CodebookIndices[i] = int(cbk[i])
	}
	return res, nil
}

// SILKPitchCoreResult holds silk_pitch_analysis_core_FLP outputs.
type SILKPitchCoreResult struct {
	PitchOut     []int
	LagIndex     int
	ContourIndex int
	LTPCorr      float32
	Voiced       bool
}

// SILKPitchAnalysisCore runs libopus silk_pitch_analysis_core_FLP (scalar
// arch) on an injected residual frame.
func SILKPitchAnalysisCore(frame []float32, ltpCorrIn float32, prevLag int, thres1, thres2 float32, fsKHz, complexity, nbSubfr int) (SILKPitchCoreResult, error) {
	need := (20 + nbSubfr*5) * fsKHz
	if len(frame) < need || (nbSubfr != 2 && nbSubfr != 4) {
		return SILKPitchCoreResult{}, fmt.Errorf("invalid pitch core input: len=%d need=%d", len(frame), need)
	}
	var out C.go_silk_pitch_core_result
	C.go_silk_pitch_core((*C.silk_float)(unsafe.Pointer(&frame[0])), C.silk_float(ltpCorrIn), C.int(prevLag),
		C.silk_float(thres1), C.silk_float(thres2), C.int(fsKHz), C.int(complexity), C.int(nbSubfr), &out)
	res := SILKPitchCoreResult{
		PitchOut:     make([]int, nbSubfr),
		LagIndex:     int(out.lag_index),
		ContourIndex: int(out.contour_index),
		LTPCorr:      float32(out.ltp_corr),
		Voiced:       out.unvoiced == 0,
	}
	for i := 0; i < nbSubfr; i++ {
		res.PitchOut[i] = int(out.pitch_out[i])
	}
	return res, nil
}

// SILKFindPitchLagsInput describes one silk_find_pitch_lags_FLP call.
type SILKFindPitchLagsInput struct {
	XBuf                 []float32 // ltp_mem_length + frame_length + la_pitch samples
	LTPMemLength         int
	FrameLength          int
	LAPitch              int
	PitchLPCWinLength    int
	Order                int
	FsKHz                int
	NbSubfr              int
	PEComplexity         int
	SpeechActivityQ8     int
	PrevSignalType       int
	InputTiltQ15         int
	PitchEstThresholdQ16 int
	PrevLag              int
	LTPCorrIn            float32
	RunCore              bool
}

// SILKFindPitchLagsResult holds the whitening stages and the core result.
type SILKFindPitchLagsResult struct {
	AutoCorr  []float32
	ReflCoef  []float32
	A         []float32
	ResNrg    float32
	PredGain  float32
	Threshold float32
	Residual  []float32
	Core      SILKPitchCoreResult
}

// SILKFindPitchLags replays libopus silk_find_pitch_lags_FLP with its
// exported helpers on an explicit input buffer.
func SILKFindPitchLags(in SILKFindPitchLagsInput) (SILKFindPitchLagsResult, error) {
	bufLen := in.LTPMemLength + in.FrameLength + in.LAPitch
	if len(in.XBuf) != bufLen || in.Order < 1 || in.Order > 16 || in.PitchLPCWinLength > bufLen {
		return SILKFindPitchLagsResult{}, fmt.Errorf("invalid find_pitch_lags input")
	}
	res := make([]float32, bufLen)
	var out C.go_silk_find_pitch_result
	C.go_silk_find_pitch_lags(
		(*C.silk_float)(unsafe.Pointer(&in.XBuf[0])), C.int(bufLen), (*C.silk_float)(unsafe.Pointer(&res[0])),
		C.int(in.LAPitch), C.int(in.PitchLPCWinLength),
		C.int(in.Order), C.int(in.FsKHz), C.int(in.NbSubfr), C.int(in.PEComplexity),
		C.int(in.SpeechActivityQ8), C.int(in.PrevSignalType), C.int(in.InputTiltQ15),
		C.int(in.PitchEstThresholdQ16), C.int(in.PrevLag), C.silk_float(in.LTPCorrIn), boolToCInt(in.RunCore), &out,
	)
	r := SILKFindPitchLagsResult{
		AutoCorr:  make([]float32, in.Order+1),
		ReflCoef:  make([]float32, in.Order),
		A:         make([]float32, in.Order),
		ResNrg:    float32(out.res_nrg),
		PredGain:  float32(out.pred_gain),
		Threshold: float32(out.thrhld),
		Residual:  res,
	}
	for i := 0; i <= in.Order; i++ {
		r.AutoCorr[i] = float32(out.auto_corr[i])
	}
	for i := 0; i < in.Order; i++ {
		r.ReflCoef[i] = float32(out.refl_coef[i])
		r.A[i] = float32(out.A[i])
	}
	r.Core = SILKPitchCoreResult{
		PitchOut:     make([]int, in.NbSubfr),
		LagIndex:     int(out.core.lag_index),
		ContourIndex: int(out.core.contour_index),
		LTPCorr:      float32(out.core.ltp_corr),
		Voiced:       out.core.unvoiced == 0,
	}
	for i := 0; i < in.NbSubfr; i++ {
		r.Core.PitchOut[i] = int(out.core.pitch_out[i])
	}
	return r, nil
}
