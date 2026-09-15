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
