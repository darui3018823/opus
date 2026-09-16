//go:build opusref

package cgoref

/*
#include <stdint.h>
#include <string.h>

typedef int8_t  opus_int8;
typedef int16_t opus_int16;
typedef int32_t opus_int32;
typedef int opus_int;

// stereo_enc_state (silk/structs.h, MAX_FRAMES_PER_PACKET = 3), laid out
// field for field so the libopus function can run on it.
typedef struct {
    opus_int16 pred_prev_Q13[2];
    opus_int16 sMid[2];
    opus_int16 sSide[2];
    opus_int32 mid_side_amp_Q0[4];
    opus_int16 smth_width_Q14;
    opus_int16 width_prev_Q14;
    opus_int16 silent_side_len;
    opus_int8  predIx[3][2][3];
    opus_int8  mid_only_flags[3];
} stereo_enc_state_box;

extern void silk_stereo_LR_to_MS(void *state, opus_int16 x1[], opus_int16 x2[], opus_int8 ix[2][3],
    opus_int8 *mid_only_flag, opus_int32 mid_side_rates_bps[], opus_int32 total_rate_bps,
    opus_int prev_speech_act_Q8, opus_int toMono, opus_int fs_kHz, opus_int frame_length);

static void stereo_box_init(stereo_enc_state_box *b)
{
    memset(b, 0, sizeof(*b));
    b->mid_side_amp_Q0[1] = 1;
    b->mid_side_amp_Q0[3] = 1;
    b->smth_width_Q14 = 1 << 14;
}
static void stereo_box_run(stereo_enc_state_box *b, opus_int16 *x1, opus_int16 *x2, opus_int8 *ix,
    opus_int8 *mid_only, opus_int32 *rates, opus_int32 total_rate, int prev_act, int toMono, int fs_kHz, int frame_length)
{
    silk_stereo_LR_to_MS(b, x1 + 2, x2 + 2, (opus_int8 (*)[3])ix, mid_only, rates, total_rate, prev_act, toMono, fs_kHz, frame_length);
}
*/
import "C"

import "unsafe"

// SILKStereoLRToMS wraps libopus' silk_stereo_LR_to_MS with its state.
type SILKStereoLRToMS struct {
	box C.stereo_enc_state_box
}

// NewSILKStereoLRToMS returns the state silk_InitEncoder leaves behind.
func NewSILKStereoLRToMS() *SILKStereoLRToMS {
	s := &SILKStereoLRToMS{}
	C.stereo_box_init(&s.box)
	return s
}

// SILKStereoLRToMSResult holds one call's outputs: the mid frame (inputBuf +
// 1, frameLength samples), the side residual, the predictor indices, the
// mid-only flag and the mid/side rates.
type SILKStereoLRToMSResult struct {
	Mid, Side    []int16
	Ix           [2][3]int8
	MidOnly      bool
	MidSideRates [2]int32
}

// Run converts one frame: left/right hold frameLength samples.
func (s *SILKStereoLRToMS) Run(left, right []int16, fsKHz, frameLength int, totalRateBps int32, prevSpeechActQ8 int, toMono bool) SILKStereoLRToMSResult {
	x1 := make([]int16, frameLength+2)
	x2 := make([]int16, frameLength+2)
	copy(x1[2:], left[:frameLength])
	copy(x2[2:], right[:frameLength])
	var ix [6]int8
	var midOnly int8
	var rates [2]int32
	tm := 0
	if toMono {
		tm = 1
	}
	C.stereo_box_run(&s.box,
		(*C.opus_int16)(unsafe.Pointer(&x1[0])), (*C.opus_int16)(unsafe.Pointer(&x2[0])),
		(*C.opus_int8)(unsafe.Pointer(&ix[0])), (*C.opus_int8)(unsafe.Pointer(&midOnly)),
		(*C.opus_int32)(unsafe.Pointer(&rates[0])), C.opus_int32(totalRateBps),
		C.int(prevSpeechActQ8), C.int(tm), C.int(fsKHz), C.int(frameLength))
	res := SILKStereoLRToMSResult{
		Mid:          append([]int16(nil), x1[1:frameLength+1]...),
		Side:         append([]int16(nil), x2[1:frameLength+1]...),
		MidOnly:      midOnly != 0,
		MidSideRates: rates,
	}
	for n := 0; n < 2; n++ {
		for k := 0; k < 3; k++ {
			res.Ix[n][k] = ix[3*n+k]
		}
	}
	return res
}
