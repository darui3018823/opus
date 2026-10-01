//go:build opusref

package cgoref

/*
#include <stdint.h>
#include <string.h>

typedef int16_t opus_int16;
typedef int32_t opus_int32;
typedef int opus_int;

extern void silk_ana_filt_bank_1(const opus_int16 *, opus_int32 *, opus_int16 *, opus_int16 *, const opus_int32);
extern opus_int32 silk_lin2log(const opus_int32);
extern opus_int silk_sigm_Q15(opus_int);

// The VAD entry point takes the whole silk_encoder_state, which cannot be
// constructed here without libopus' private headers, so the body of
// silk_VAD_GetSA_Q8_c and silk_VAD_GetNoiseLevels (silk/VAD.c, libopus 1.6.1)
// is replayed verbatim on a private copy of silk_VAD_state, calling the
// exported helpers for the filter bank, lin2log, and sigmoid.

#define VAD_N_BANDS 4
#define VAD_INTERNAL_SUBFRAMES_LOG2 2
#define VAD_INTERNAL_SUBFRAMES (1 << VAD_INTERNAL_SUBFRAMES_LOG2)
#define VAD_NOISE_LEVEL_SMOOTH_COEF_Q16 1024
#define VAD_NOISE_LEVELS_BIAS 50
#define VAD_NEGATIVE_OFFSET_Q5 128
#define VAD_SNR_FACTOR_Q16 45000
#define VAD_SNR_SMOOTH_COEF_Q18 4096

typedef struct {
    opus_int32 AnaState[2];
    opus_int32 AnaState1[2];
    opus_int32 AnaState2[2];
    opus_int32 XnrgSubfr[VAD_N_BANDS];
    opus_int32 NrgRatioSmth_Q8[VAD_N_BANDS];
    opus_int16 HPstate;
    opus_int32 NL[VAD_N_BANDS];
    opus_int32 inv_NL[VAD_N_BANDS];
    opus_int32 NoiseLevelBias[VAD_N_BANDS];
    opus_int32 counter;
} go_vad_state;

static opus_int32 v_smulwb(opus_int32 a, opus_int32 b) { return (opus_int32)(((int64_t)a * (opus_int16)b) >> 16); }
static opus_int32 v_smlawb(opus_int32 a, opus_int32 b, opus_int32 c) { return a + v_smulwb(b, c); }
static opus_int32 v_smulww(opus_int32 a, opus_int32 b) { return (opus_int32)(((int64_t)a * b) >> 16); }
static opus_int32 v_smlabb(opus_int32 a, opus_int32 b, opus_int32 c) { return a + (opus_int32)((opus_int16)b) * (opus_int32)((opus_int16)c); }
static opus_int32 v_add_pos_sat32(opus_int32 a, opus_int32 b) { return (((uint32_t)(a + b)) & 0x80000000) ? INT32_MAX : (a + b); }
static int v_clz32(opus_int32 x) { return x == 0 ? 32 : __builtin_clz((uint32_t)x); }
static opus_int32 v_ror32(opus_int32 a32, opus_int rot) {
    uint32_t x = (uint32_t)a32; uint32_t r = (uint32_t)rot; uint32_t m = (uint32_t)-rot;
    if (rot == 0) return a32;
    if (rot < 0) return (opus_int32)((x << m) | (x >> (32 - m)));
    return (opus_int32)((x << (32 - r)) | (x >> r));
}
static opus_int32 v_sqrt_approx(opus_int32 x) {
    opus_int32 y, lz, frac_Q7;
    if (x <= 0) return 0;
    lz = v_clz32(x);
    frac_Q7 = v_ror32(x, 24 - lz) & 0x7f;
    if (lz & 1) y = 32768; else y = 46214;
    y >>= (lz >> 1);
    y = v_smlawb(y, y, (opus_int32)((opus_int16)213 * (opus_int16)frac_Q7));
    return y;
}

static void go_vad_init(go_vad_state *s) {
    int b;
    memset(s, 0, sizeof(*s));
    for (b = 0; b < VAD_N_BANDS; b++) {
        opus_int32 v = VAD_NOISE_LEVELS_BIAS / (b + 1);
        s->NoiseLevelBias[b] = v > 1 ? v : 1;
    }
    for (b = 0; b < VAD_N_BANDS; b++) {
        s->NL[b] = 100 * s->NoiseLevelBias[b];
        s->inv_NL[b] = INT32_MAX / s->NL[b];
    }
    s->counter = 15;
    for (b = 0; b < VAD_N_BANDS; b++) s->NrgRatioSmth_Q8[b] = 100 * 256;
}

static void go_vad_get_noise_levels(const opus_int32 pX[VAD_N_BANDS], go_vad_state *psSilk_VAD) {
    opus_int k;
    opus_int32 nl, nrg, inv_nrg;
    opus_int coef, min_coef;
    if (psSilk_VAD->counter < 1000) {
        min_coef = 32767 / ((psSilk_VAD->counter >> 4) + 1);
        psSilk_VAD->counter++;
    } else {
        min_coef = 0;
    }
    for (k = 0; k < VAD_N_BANDS; k++) {
        nl = psSilk_VAD->NL[k];
        nrg = v_add_pos_sat32(pX[k], psSilk_VAD->NoiseLevelBias[k]);
        inv_nrg = INT32_MAX / nrg;
        if (nrg > (nl << 3)) {
            coef = VAD_NOISE_LEVEL_SMOOTH_COEF_Q16 >> 3;
        } else if (nrg < nl) {
            coef = VAD_NOISE_LEVEL_SMOOTH_COEF_Q16;
        } else {
            coef = v_smulwb(v_smulww(inv_nrg, nl), VAD_NOISE_LEVEL_SMOOTH_COEF_Q16 << 1);
        }
        coef = coef > min_coef ? coef : min_coef;
        psSilk_VAD->inv_NL[k] = v_smlawb(psSilk_VAD->inv_NL[k], inv_nrg - psSilk_VAD->inv_NL[k], coef);
        nl = INT32_MAX / psSilk_VAD->inv_NL[k];
        nl = nl < 0x00FFFFFF ? nl : 0x00FFFFFF;
        psSilk_VAD->NL[k] = nl;
    }
}

static const opus_int32 go_tiltWeights[VAD_N_BANDS] = {30000, 6000, -12000, -12000};

typedef struct {
    opus_int speech_activity_Q8;
    opus_int input_tilt_Q15;
    opus_int input_quality_bands_Q15[VAD_N_BANDS];
} go_vad_result;

static void go_vad_get_sa_q8(go_vad_state *psSilk_VAD, const opus_int16 *pIn, int frame_length, int fs_kHz, go_vad_result *out) {
    opus_int SA_Q15, pSNR_dB_Q7, input_tilt;
    opus_int decimated_framelength1, decimated_framelength2, decimated_framelength;
    opus_int dec_subframe_length, dec_subframe_offset, SNR_Q7, i, b, s;
    opus_int32 sumSquared, smooth_coef_Q16;
    opus_int16 HPstateTmp;
    opus_int16 X[16 * 20 + 16 * 10];
    opus_int32 Xnrg[VAD_N_BANDS];
    opus_int32 NrgToNoiseRatio_Q8[VAD_N_BANDS];
    opus_int32 speech_nrg, x_tmp;
    opus_int X_offset[VAD_N_BANDS];

    decimated_framelength1 = frame_length >> 1;
    decimated_framelength2 = frame_length >> 2;
    decimated_framelength = frame_length >> 3;
    X_offset[0] = 0;
    X_offset[1] = decimated_framelength + decimated_framelength2;
    X_offset[2] = X_offset[1] + decimated_framelength;
    X_offset[3] = X_offset[2] + decimated_framelength2;

    silk_ana_filt_bank_1(pIn, &psSilk_VAD->AnaState[0], X, &X[X_offset[3]], frame_length);
    silk_ana_filt_bank_1(X, &psSilk_VAD->AnaState1[0], X, &X[X_offset[2]], decimated_framelength1);
    silk_ana_filt_bank_1(X, &psSilk_VAD->AnaState2[0], X, &X[X_offset[1]], decimated_framelength2);

    X[decimated_framelength - 1] = X[decimated_framelength - 1] >> 1;
    HPstateTmp = X[decimated_framelength - 1];
    for (i = decimated_framelength - 1; i > 0; i--) {
        X[i - 1] = X[i - 1] >> 1;
        X[i] -= X[i - 1];
    }
    X[0] -= psSilk_VAD->HPstate;
    psSilk_VAD->HPstate = HPstateTmp;

    for (b = 0; b < VAD_N_BANDS; b++) {
        int sh = (VAD_N_BANDS - b) < (VAD_N_BANDS - 1) ? (VAD_N_BANDS - b) : (VAD_N_BANDS - 1);
        decimated_framelength = frame_length >> sh;
        dec_subframe_length = decimated_framelength >> VAD_INTERNAL_SUBFRAMES_LOG2;
        dec_subframe_offset = 0;
        Xnrg[b] = psSilk_VAD->XnrgSubfr[b];
        for (s = 0; s < VAD_INTERNAL_SUBFRAMES; s++) {
            sumSquared = 0;
            for (i = 0; i < dec_subframe_length; i++) {
                x_tmp = X[X_offset[b] + i + dec_subframe_offset] >> 3;
                sumSquared = v_smlabb(sumSquared, x_tmp, x_tmp);
            }
            if (s < VAD_INTERNAL_SUBFRAMES - 1) {
                Xnrg[b] = v_add_pos_sat32(Xnrg[b], sumSquared);
            } else {
                Xnrg[b] = v_add_pos_sat32(Xnrg[b], sumSquared >> 1);
            }
            dec_subframe_offset += dec_subframe_length;
        }
        psSilk_VAD->XnrgSubfr[b] = sumSquared;
    }

    go_vad_get_noise_levels(&Xnrg[0], psSilk_VAD);

    sumSquared = 0;
    input_tilt = 0;
    for (b = 0; b < VAD_N_BANDS; b++) {
        speech_nrg = Xnrg[b] - psSilk_VAD->NL[b];
        if (speech_nrg > 0) {
            if ((Xnrg[b] & 0xFF800000) == 0) {
                NrgToNoiseRatio_Q8[b] = (Xnrg[b] << 8) / (psSilk_VAD->NL[b] + 1);
            } else {
                NrgToNoiseRatio_Q8[b] = Xnrg[b] / ((psSilk_VAD->NL[b] >> 8) + 1);
            }
            SNR_Q7 = silk_lin2log(NrgToNoiseRatio_Q8[b]) - 8 * 128;
            sumSquared = v_smlabb(sumSquared, SNR_Q7, SNR_Q7);
            if (speech_nrg < ((opus_int32)1 << 20)) {
                SNR_Q7 = v_smulwb(v_sqrt_approx(speech_nrg) << 6, SNR_Q7);
            }
            input_tilt = v_smlawb(input_tilt, go_tiltWeights[b], SNR_Q7);
        } else {
            NrgToNoiseRatio_Q8[b] = 256;
        }
    }
    sumSquared = sumSquared / VAD_N_BANDS;
    pSNR_dB_Q7 = (opus_int16)(3 * v_sqrt_approx(sumSquared));
    SA_Q15 = silk_sigm_Q15(v_smulwb(VAD_SNR_FACTOR_Q16, pSNR_dB_Q7) - VAD_NEGATIVE_OFFSET_Q5);
    out->input_tilt_Q15 = (silk_sigm_Q15(input_tilt) - 16384) << 1;

    speech_nrg = 0;
    for (b = 0; b < VAD_N_BANDS; b++) {
        speech_nrg += (b + 1) * ((Xnrg[b] - psSilk_VAD->NL[b]) >> 4);
    }
    if (frame_length == 20 * fs_kHz) {
        speech_nrg = speech_nrg >> 1;
    }
    if (speech_nrg <= 0) {
        SA_Q15 = SA_Q15 >> 1;
    } else if (speech_nrg < 16384) {
        speech_nrg = speech_nrg << 16;
        speech_nrg = v_sqrt_approx(speech_nrg);
        SA_Q15 = v_smulwb(32768 + speech_nrg, SA_Q15);
    }
    out->speech_activity_Q8 = (SA_Q15 >> 7) < 255 ? (SA_Q15 >> 7) : 255;

    smooth_coef_Q16 = v_smulwb(VAD_SNR_SMOOTH_COEF_Q18, v_smulwb((opus_int32)SA_Q15, SA_Q15));
    if (frame_length == 10 * fs_kHz) {
        smooth_coef_Q16 >>= 1;
    }
    for (b = 0; b < VAD_N_BANDS; b++) {
        psSilk_VAD->NrgRatioSmth_Q8[b] = v_smlawb(psSilk_VAD->NrgRatioSmth_Q8[b],
            NrgToNoiseRatio_Q8[b] - psSilk_VAD->NrgRatioSmth_Q8[b], smooth_coef_Q16);
        SNR_Q7 = 3 * (silk_lin2log(psSilk_VAD->NrgRatioSmth_Q8[b]) - 8 * 128);
        out->input_quality_bands_Q15[b] = silk_sigm_Q15((SNR_Q7 - 16 * 128) >> 4);
    }
}
*/
import "C"

import (
	"fmt"
	"unsafe"
)

// SILKVAD is a libopus-equivalent SILK VAD state for oracle tests.
type SILKVAD struct {
	state C.go_vad_state
}

// SILKVADResult holds one frame's VAD outputs.
type SILKVADResult struct {
	SpeechActivityQ8    int
	InputTiltQ15        int
	InputQualityBandQ15 [4]int
}

// NewSILKVAD returns a VAD state initialized like silk_VAD_Init.
func NewSILKVAD() *SILKVAD {
	v := &SILKVAD{}
	C.go_vad_init(&v.state)
	return v
}

// Process runs silk_VAD_GetSA_Q8 on one int16 frame.
func (v *SILKVAD) Process(pcm []int16, fsKHz int) (SILKVADResult, error) {
	if len(pcm) == 0 || len(pcm)%8 != 0 || len(pcm) > 320 {
		return SILKVADResult{}, fmt.Errorf("invalid VAD frame length %d", len(pcm))
	}
	var out C.go_vad_result
	C.go_vad_get_sa_q8(&v.state, (*C.opus_int16)(unsafe.Pointer(&pcm[0])), C.int(len(pcm)), C.int(fsKHz), &out)
	r := SILKVADResult{SpeechActivityQ8: int(out.speech_activity_Q8), InputTiltQ15: int(out.input_tilt_Q15)}
	for b := 0; b < 4; b++ {
		r.InputQualityBandQ15[b] = int(out.input_quality_bands_Q15[b])
	}
	return r, nil
}
