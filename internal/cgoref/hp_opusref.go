//go:build opusref

package cgoref

/*
#include <stdint.h>
#include <string.h>

typedef int16_t opus_int16;
typedef int32_t opus_int32;
typedef int opus_int;
typedef float opus_val32;
typedef float opus_res;

extern opus_int32 silk_lin2log(const opus_int32);
extern opus_int32 silk_log2lin(const opus_int32);

// hp_cutoff, dc_reject, and silk_biquad_res are static in
// src/opus_encoder.c, so the float-build bodies (libopus 1.6.1) are replayed
// verbatim here with the SILK fixed-point macros they use, compiled by the
// same C compiler as the reference library.

#define VERY_SMALL 1e-30f
#define SILK_FIX_CONST( C, Q ) ((opus_int32)((C) * ((opus_int64)1 << (Q)) + 0.5))
#define silk_LSHIFT(a, shift) ((opus_int32)((opus_uint32)(a) << (shift)))
#define silk_RSHIFT(a, shift) ((a) >> (shift))
#define silk_MUL(a32, b32) ((a32) * (b32))
#define silk_MLA(a32, b32, c32) silk_ADD32((a32),((b32) * (c32)))
#define silk_ADD32(a, b) ((a) + (b))
#define silk_SMULBB(a32, b32) ((opus_int32)((opus_int16)(a32)) * (opus_int32)((opus_int16)(b32)))
#define silk_SMULWB(a32, b32) ((((a32) >> 16) * (opus_int32)((opus_int16)(b32))) + ((((a32) & 0x0000FFFF) * (opus_int32)((opus_int16)(b32))) >> 16))
#define silk_SMLAWB(a32, b32, c32) ((a32) + ((((b32) >> 16) * (opus_int32)((opus_int16)(c32))) + ((((b32) & 0x0000FFFF) * (opus_int32)((opus_int16)(c32))) >> 16)))
#define silk_RSHIFT_ROUND(a, shift) ((shift) == 1 ? ((a) >> 1) + ((a) & 1) : (((a) >> ((shift) - 1)) + 1) >> 1)
#define silk_SMULWW(a32, b32) silk_MLA(silk_SMULWB((a32), (b32)), (a32), silk_RSHIFT_ROUND((b32), 16))
#define silk_DIV32_16(a32, b16) ((opus_int32)((a32) / (b16)))
typedef int64_t opus_int64;
typedef uint32_t opus_uint32;

static void silk_biquad_res(
    const opus_res        *in,
    const opus_int32      *B_Q28,
    const opus_int32      *A_Q28,
    opus_val32            *S,
    opus_res              *out,
    const opus_int32      len,
    int stride
)
{
    opus_int   k;
    opus_val32 vout;
    opus_val32 inval;
    opus_val32 A[2], B[3];

    A[0] = (opus_val32)(A_Q28[0] * (1.f/((opus_int32)1<<28)));
    A[1] = (opus_val32)(A_Q28[1] * (1.f/((opus_int32)1<<28)));
    B[0] = (opus_val32)(B_Q28[0] * (1.f/((opus_int32)1<<28)));
    B[1] = (opus_val32)(B_Q28[1] * (1.f/((opus_int32)1<<28)));
    B[2] = (opus_val32)(B_Q28[2] * (1.f/((opus_int32)1<<28)));

    for( k = 0; k < len; k++ ) {
        inval = in[ k*stride ];
        vout = S[ 0 ] + B[0]*inval;

        S[ 0 ] = S[1] - vout*A[0] + B[1]*inval;

        S[ 1 ] = - vout*A[1] + B[2]*inval + VERY_SMALL;

        out[ k*stride ] = vout;
    }
}

static void hp_cutoff(const opus_res *in, opus_int32 cutoff_Hz, opus_res *out, opus_val32 *hp_mem, int len, int channels, opus_int32 Fs)
{
   opus_int32 B_Q28[ 3 ], A_Q28[ 2 ];
   opus_int32 Fc_Q19, r_Q28, r_Q22;

   Fc_Q19 = silk_DIV32_16( silk_SMULBB( SILK_FIX_CONST( 1.5 * 3.14159 / 1000, 19 ), cutoff_Hz ), Fs/1000 );

   r_Q28 = SILK_FIX_CONST( 1.0, 28 ) - silk_MUL( SILK_FIX_CONST( 0.92, 9 ), Fc_Q19 );

   B_Q28[ 0 ] = r_Q28;
   B_Q28[ 1 ] = silk_LSHIFT( -r_Q28, 1 );
   B_Q28[ 2 ] = r_Q28;

   r_Q22  = silk_RSHIFT( r_Q28, 6 );
   A_Q28[ 0 ] = silk_SMULWW( r_Q22, silk_SMULWW( Fc_Q19, Fc_Q19 ) - SILK_FIX_CONST( 2.0,  22 ) );
   A_Q28[ 1 ] = silk_SMULWW( r_Q22, r_Q22 );

   silk_biquad_res( in, B_Q28, A_Q28, hp_mem, out, len, channels );
   if( channels == 2 ) {
       silk_biquad_res( in+1, B_Q28, A_Q28, hp_mem+2, out+1, len, channels );
   }
}

static void dc_reject(const float *in, opus_int32 cutoff_Hz, float *out, opus_val32 *hp_mem, int len, int channels, opus_int32 Fs)
{
   int i;
   float coef, coef2;
   coef = 6.3f*cutoff_Hz/Fs;
   coef2 = 1-coef;
   if (channels==2)
   {
      float m0, m2;
      m0 = hp_mem[0];
      m2 = hp_mem[2];
      for (i=0;i<len;i++)
      {
         opus_val32 x0, x1, out0, out1;
         x0 = in[2*i+0];
         x1 = in[2*i+1];
         out0 = x0-m0;
         out1 = x1-m2;
         m0 = coef*x0 + VERY_SMALL + coef2*m0;
         m2 = coef*x1 + VERY_SMALL + coef2*m2;
         out[2*i+0] = out0;
         out[2*i+1] = out1;
      }
      hp_mem[0] = m0;
      hp_mem[2] = m2;
   } else {
      float m0;
      m0 = hp_mem[0];
      for (i=0;i<len;i++)
      {
         opus_val32 x, y;
         x = in[i];
         y = x-m0;
         m0 = coef*x + VERY_SMALL + coef2*m0;
         out[i] = y;
      }
      hp_mem[0] = m0;
   }
}

// opus_encode_native's cutoff smoother: returns the new variable_HP_smth2_Q15
// and writes the cutoff in Hz.
static opus_int32 hp_smooth(opus_int32 smth2, opus_int32 hp_freq_smth1, opus_int32 *cutoff_Hz)
{
   smth2 = silk_SMLAWB( smth2, hp_freq_smth1 - smth2, SILK_FIX_CONST( 0.015f, 16 ) );
   *cutoff_Hz = silk_log2lin( silk_RSHIFT( smth2, 8 ) );
   return smth2;
}

static opus_int32 hp_min_cutoff_log(void)
{
   return silk_LSHIFT( silk_lin2log( 60 ), 8 );
}

#define silk_LIMIT_32(a, limit1, limit2) ((limit1) > (limit2) ? ((a) > (limit1) ? (limit1) : ((a) < (limit2) ? (limit2) : (a))) : ((a) > (limit2) ? (limit2) : ((a) < (limit1) ? (limit1) : (a))))
#define TYPE_VOICED 2
#define VARIABLE_HP_SMTH_COEF1 0.1f
#define VARIABLE_HP_MAX_DELTA_FREQ 0.4f
#define VARIABLE_HP_MIN_CUTOFF_HZ 60
#define VARIABLE_HP_MAX_CUTOFF_HZ 100

// silk_HP_variable_cutoff (silk/HP_variable_cutoff.c) on the fields it reads
// from silk_encoder_state; returns the updated variable_HP_smth1_Q15.
static opus_int32 hp_variable_cutoff(opus_int prevSignalType, opus_int fs_kHz, opus_int prevLag,
    opus_int quality_Q15, opus_int speech_activity_Q8, opus_int32 variable_HP_smth1_Q15)
{
   opus_int32 pitch_freq_Hz_Q16, pitch_freq_log_Q7, delta_freq_Q7;

   if( prevSignalType == TYPE_VOICED ) {
      pitch_freq_Hz_Q16 = silk_DIV32_16( silk_LSHIFT( silk_MUL( fs_kHz, 1000 ), 16 ), prevLag );
      pitch_freq_log_Q7 = silk_lin2log( pitch_freq_Hz_Q16 ) - ( 16 << 7 );

      pitch_freq_log_Q7 = silk_SMLAWB( pitch_freq_log_Q7, silk_SMULWB( silk_LSHIFT( -quality_Q15, 2 ), quality_Q15 ),
            pitch_freq_log_Q7 - ( silk_lin2log( SILK_FIX_CONST( VARIABLE_HP_MIN_CUTOFF_HZ, 16 ) ) - ( 16 << 7 ) ) );

      delta_freq_Q7 = pitch_freq_log_Q7 - silk_RSHIFT( variable_HP_smth1_Q15, 8 );
      if( delta_freq_Q7 < 0 ) {
         delta_freq_Q7 = silk_MUL( delta_freq_Q7, 3 );
      }

      delta_freq_Q7 = silk_LIMIT_32( delta_freq_Q7, -SILK_FIX_CONST( VARIABLE_HP_MAX_DELTA_FREQ, 7 ), SILK_FIX_CONST( VARIABLE_HP_MAX_DELTA_FREQ, 7 ) );

      variable_HP_smth1_Q15 = silk_SMLAWB( variable_HP_smth1_Q15,
            silk_SMULBB( speech_activity_Q8, delta_freq_Q7 ), SILK_FIX_CONST( VARIABLE_HP_SMTH_COEF1, 16 ) );

      variable_HP_smth1_Q15 = silk_LIMIT_32( variable_HP_smth1_Q15,
            silk_LSHIFT( silk_lin2log( VARIABLE_HP_MIN_CUTOFF_HZ ), 8 ),
            silk_LSHIFT( silk_lin2log( VARIABLE_HP_MAX_CUTOFF_HZ ), 8 ) );
   }
   return variable_HP_smth1_Q15;
}

static opus_int32 hp_smth1_initial(void)
{
   return silk_LSHIFT( silk_lin2log( SILK_FIX_CONST( VARIABLE_HP_MIN_CUTOFF_HZ, 16 ) ) - ( 16 << 7 ), 8 );
}
*/
import "C"

import (
	"fmt"
	"unsafe"
)

// HPCutoff replays libopus' float hp_cutoff on interleaved float32 input with
// the given filter memory (4 floats), returning the filtered frame.
func HPCutoff(in []float32, cutoffHz int32, hpMem *[4]float32, frameSize, channels, fs int) ([]float32, error) {
	if len(in) < frameSize*channels || frameSize <= 0 {
		return nil, fmt.Errorf("cgoref: invalid hp_cutoff input")
	}
	out := make([]float32, frameSize*channels)
	C.hp_cutoff((*C.opus_res)(unsafe.Pointer(&in[0])), C.opus_int32(cutoffHz), (*C.opus_res)(unsafe.Pointer(&out[0])),
		(*C.opus_val32)(unsafe.Pointer(&hpMem[0])), C.int(frameSize), C.int(channels), C.opus_int32(fs))
	return out, nil
}

// DCReject replays libopus' float dc_reject.
func DCReject(in []float32, cutoffHz int32, hpMem *[4]float32, frameSize, channels, fs int) ([]float32, error) {
	if len(in) < frameSize*channels || frameSize <= 0 {
		return nil, fmt.Errorf("cgoref: invalid dc_reject input")
	}
	out := make([]float32, frameSize*channels)
	C.dc_reject((*C.float)(unsafe.Pointer(&in[0])), C.opus_int32(cutoffHz), (*C.float)(unsafe.Pointer(&out[0])),
		(*C.opus_val32)(unsafe.Pointer(&hpMem[0])), C.int(frameSize), C.int(channels), C.opus_int32(fs))
	return out, nil
}

// HPSmooth replays the opus_encode_native cutoff smoother: it returns the
// updated variable_HP_smth2_Q15 and the cutoff in Hz.
func HPSmooth(smth2, hpFreqSmth1 int32) (int32, int32) {
	var cutoff C.opus_int32
	next := C.hp_smooth(C.opus_int32(smth2), C.opus_int32(hpFreqSmth1), &cutoff)
	return int32(next), int32(cutoff)
}

// HPMinCutoffLog returns silk_LSHIFT(silk_lin2log(VARIABLE_HP_MIN_CUTOFF_HZ), 8),
// the initial variable_HP_smth2_Q15.
func HPMinCutoffLog() int32 {
	return int32(C.hp_min_cutoff_log())
}

// SILKHPVariableCutoff replays silk_HP_variable_cutoff on the encoder-state
// fields it reads and returns the updated variable_HP_smth1_Q15.
func SILKHPVariableCutoff(prevSignalType, fsKHz, prevLag, qualityQ15, speechActivityQ8 int, smth1 int32) int32 {
	return int32(C.hp_variable_cutoff(C.opus_int(prevSignalType), C.opus_int(fsKHz), C.opus_int(prevLag),
		C.opus_int(qualityQ15), C.opus_int(speechActivityQ8), C.opus_int32(smth1)))
}

// SILKHPSmth1Initial returns silk_init_encoder's variable_HP_smth1_Q15.
func SILKHPSmth1Initial() int32 {
	return int32(C.hp_smth1_initial())
}
