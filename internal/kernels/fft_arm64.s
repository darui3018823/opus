//go:build arm64 && go1.27 && !purego

#include "textflag.h"

// Four butterflies per pass, as in fft_amd64.s: VLD2/VST2 split the four
// complex values of a row into real and imaginary vectors and back. Each
// lane follows kf_bfly's scalar order with separate FMUL/FADD/FSUB.
//
// Rows: R11 = f[j], R12 = f[j+m], R13 = f[j+2m], R14 = f[j+3m],
// R15 = f[j+4m]; R10 twiddle block; R8 = m*8 bytes; R9 = mm*8 bytes.

// CMUL: (re, im) = (ar, ai) * (br, bi) with C_MUL's rounding. Clobbers
// V24-V27.
#define CMUL(ar, ai, br, bi, re, im) \
	VFMUL br.S4, ar.S4, V24.S4 \
	VFMUL bi.S4, ai.S4, V25.S4 \
	VFMUL bi.S4, ar.S4, V26.S4 \
	VFMUL br.S4, ai.S4, V27.S4 \
	VFSUB V25.S4, V24.S4, re.S4 \
	VFADD V27.S4, V26.S4, im.S4

#define ROWS4 \
	ADD R8, R11, R12 \
	ADD R8, R12, R13 \
	ADD R8, R13, R14

#define NEXT \
	ADD $32, R11

// func fftRadix4NEON(f, t *float32, m, groups, mm int)
TEXT ·fftRadix4NEON(SB), NOSPLIT, $0-40
	MOVD f+0(FP), R0
	MOVD t+8(FP), R1
	MOVD m+16(FP), R8
	MOVD groups+24(FP), R2
	MOVD mm+32(FP), R9
	LSL  $3, R8
	LSL  $3, R9

r4group:
	MOVD R0, R11
	MOVD R1, R10
	MOVD m+16(FP), R3
	LSR  $2, R3

r4block:
	ROWS4
	VLD2   (R11), [V0.S4, V1.S4]
	VLD2   (R12), [V2.S4, V3.S4]
	VLD2   (R13), [V4.S4, V5.S4]
	VLD2   (R14), [V6.S4, V7.S4]
	VLD1.P 64(R10), [V16.S4, V17.S4, V18.S4, V19.S4]
	VLD1.P 32(R10), [V20.S4, V21.S4]
	CMUL(V2, V3, V16, V17, V8, V9)     // s0
	CMUL(V4, V5, V18, V19, V10, V11)   // s1
	CMUL(V6, V7, V20, V21, V12, V13)   // s2
	VFSUB  V10.S4, V0.S4, V14.S4 // s5 = f0 - s1
	VFSUB  V11.S4, V1.S4, V15.S4
	VFADD  V10.S4, V0.S4, V0.S4  // center = f0 + s1
	VFADD  V11.S4, V1.S4, V1.S4
	VFADD  V12.S4, V8.S4, V22.S4 // s3 = s0 + s2
	VFADD  V13.S4, V9.S4, V23.S4
	VFSUB  V12.S4, V8.S4, V8.S4  // s4 = s0 - s2
	VFSUB  V13.S4, V9.S4, V9.S4
	VFADD  V22.S4, V0.S4, V2.S4  // f0 = center + s3
	VFADD  V23.S4, V1.S4, V3.S4
	VST2   [V2.S4, V3.S4], (R11)
	VFSUB  V22.S4, V0.S4, V2.S4  // f2m = center - s3
	VFSUB  V23.S4, V1.S4, V3.S4
	VST2   [V2.S4, V3.S4], (R13)
	VFADD  V9.S4, V14.S4, V2.S4  // fm = (s5.r + s4.i, s5.i - s4.r)
	VFSUB  V8.S4, V15.S4, V3.S4
	VST2   [V2.S4, V3.S4], (R12)
	VFSUB  V9.S4, V14.S4, V2.S4  // f3m = (s5.r - s4.i, s5.i + s4.r)
	VFADD  V8.S4, V15.S4, V3.S4
	VST2   [V2.S4, V3.S4], (R14)
	NEXT
	SUBS   $1, R3
	BNE    r4block
	ADD    R9, R0
	SUBS   $1, R2
	BNE    r4group
	RET

// func fftRadix3NEON(f, t *float32, epi float32, m, groups, mm int)
TEXT ·fftRadix3NEON(SB), NOSPLIT, $0-48
	MOVD  f+0(FP), R0
	MOVD  t+8(FP), R1
	FMOVS epi+16(FP), F30
	VDUP  V30.S[0], V30.S4
	MOVW  $0x3f000000, R4 // 0.5
	VDUP  R4, V31.S4
	MOVD  m+24(FP), R8
	MOVD  groups+32(FP), R2
	MOVD  mm+40(FP), R9
	LSL   $3, R8
	LSL   $3, R9

r3group:
	MOVD R0, R11
	MOVD R1, R10
	MOVD m+24(FP), R3
	LSR  $2, R3

r3block:
	ADD    R8, R11, R12
	ADD    R8, R12, R13
	VLD2   (R11), [V0.S4, V1.S4]
	VLD2   (R12), [V2.S4, V3.S4]
	VLD2   (R13), [V4.S4, V5.S4]
	VLD1.P 64(R10), [V16.S4, V17.S4, V18.S4, V19.S4]
	CMUL(V2, V3, V16, V17, V8, V9)   // s1
	CMUL(V4, V5, V18, V19, V10, V11) // s2
	VFADD  V10.S4, V8.S4, V12.S4 // s3 = s1 + s2
	VFADD  V11.S4, V9.S4, V13.S4
	VFSUB  V10.S4, V8.S4, V8.S4  // s0 = s1 - s2
	VFSUB  V11.S4, V9.S4, V9.S4
	VFMUL  V31.S4, V12.S4, V14.S4 // mid = f0 - 0.5*s3
	VFSUB  V14.S4, V0.S4, V14.S4
	VFMUL  V31.S4, V13.S4, V15.S4
	VFSUB  V15.S4, V1.S4, V15.S4
	VFMUL  V30.S4, V8.S4, V8.S4   // s0 *= epi
	VFMUL  V30.S4, V9.S4, V9.S4
	VFADD  V12.S4, V0.S4, V0.S4   // f0 += s3
	VFADD  V13.S4, V1.S4, V1.S4
	VST2   [V0.S4, V1.S4], (R11)
	VFADD  V9.S4, V14.S4, V2.S4   // f2m = (mid.r + s0.i, mid.i - s0.r)
	VFSUB  V8.S4, V15.S4, V3.S4
	VST2   [V2.S4, V3.S4], (R13)
	VFSUB  V9.S4, V14.S4, V2.S4   // fm = (mid.r - s0.i, mid.i + s0.r)
	VFADD  V8.S4, V15.S4, V3.S4
	VST2   [V2.S4, V3.S4], (R12)
	NEXT
	SUBS   $1, R3
	BNE    r3block
	ADD    R9, R0
	SUBS   $1, R2
	BNE    r3group
	RET

// func fftRadix5NEON(f, t, y *float32, m, groups, mm int)
// y: ya.r, ya.i, yb.r, yb.i, each broadcast to four lanes.
TEXT ·fftRadix5NEON(SB), NOSPLIT, $0-48
	MOVD f+0(FP), R0
	MOVD t+8(FP), R1
	MOVD y+16(FP), R4
	VLD1 (R4), [V28.S4, V29.S4, V30.S4, V31.S4] // yar, yai, ybr, ybi
	MOVD m+24(FP), R8
	MOVD groups+32(FP), R2
	MOVD mm+40(FP), R9
	LSL  $3, R8
	LSL  $3, R9

r5group:
	MOVD R0, R11
	MOVD R1, R10
	MOVD m+24(FP), R3
	LSR  $2, R3

r5block:
	ROWS4
	ADD    R8, R14, R15
	VLD2   (R11), [V0.S4, V1.S4] // s0
	VLD2   (R12), [V2.S4, V3.S4]
	VLD2   (R13), [V4.S4, V5.S4]
	VLD2   (R14), [V6.S4, V7.S4]
	VLD2   (R15), [V8.S4, V9.S4]
	VLD1.P 64(R10), [V16.S4, V17.S4, V18.S4, V19.S4]
	VLD1.P 64(R10), [V20.S4, V21.S4, V22.S4, V23.S4]
	CMUL(V2, V3, V16, V17, V10, V11) // s1
	CMUL(V4, V5, V18, V19, V2, V3)   // s2
	CMUL(V6, V7, V20, V21, V4, V5)   // s3
	CMUL(V8, V9, V22, V23, V6, V7)   // s4
	VFADD  V6.S4, V10.S4, V12.S4 // s7 = s1 + s4
	VFADD  V7.S4, V11.S4, V13.S4
	VFSUB  V6.S4, V10.S4, V10.S4 // s10 = s1 - s4
	VFSUB  V7.S4, V11.S4, V11.S4
	VFADD  V4.S4, V2.S4, V14.S4  // s8 = s2 + s3
	VFADD  V5.S4, V3.S4, V15.S4
	VFSUB  V4.S4, V2.S4, V2.S4   // s9 = s2 - s3
	VFSUB  V5.S4, V3.S4, V3.S4

	// f0 = s0 + (s7 + s8)
	VFADD V14.S4, V12.S4, V16.S4
	VFADD V16.S4, V0.S4, V16.S4
	VFADD V15.S4, V13.S4, V17.S4
	VFADD V17.S4, V1.S4, V17.S4
	VST2  [V16.S4, V17.S4], (R11)

	// s5 = s0 + (s7*ya.r + s8*yb.r)
	VFMUL V28.S4, V12.S4, V16.S4
	VFMUL V30.S4, V14.S4, V18.S4
	VFADD V18.S4, V16.S4, V16.S4
	VFADD V16.S4, V0.S4, V16.S4
	VFMUL V28.S4, V13.S4, V17.S4
	VFMUL V30.S4, V15.S4, V18.S4
	VFADD V18.S4, V17.S4, V17.S4
	VFADD V17.S4, V1.S4, V17.S4
	// s6 = (s10.i*ya.i + s9.i*yb.i, -(s10.r*ya.i + s9.r*yb.i))
	VFMUL V29.S4, V11.S4, V18.S4
	VFMUL V31.S4, V3.S4, V19.S4
	VFADD V19.S4, V18.S4, V18.S4
	VFMUL V29.S4, V10.S4, V19.S4
	VFMUL V31.S4, V2.S4, V20.S4
	VFADD V20.S4, V19.S4, V19.S4
	VFNEG V19.S4, V19.S4
	// fm = s5 - s6, f4m = s5 + s6
	VFSUB V18.S4, V16.S4, V20.S4
	VFSUB V19.S4, V17.S4, V21.S4
	VST2  [V20.S4, V21.S4], (R12)
	VFADD V18.S4, V16.S4, V20.S4
	VFADD V19.S4, V17.S4, V21.S4
	VST2  [V20.S4, V21.S4], (R15)

	// s11 = s0 + (s7*yb.r + s8*ya.r)
	VFMUL V30.S4, V12.S4, V16.S4
	VFMUL V28.S4, V14.S4, V18.S4
	VFADD V18.S4, V16.S4, V16.S4
	VFADD V16.S4, V0.S4, V16.S4
	VFMUL V30.S4, V13.S4, V17.S4
	VFMUL V28.S4, V15.S4, V18.S4
	VFADD V18.S4, V17.S4, V17.S4
	VFADD V17.S4, V1.S4, V17.S4
	// s12 = (s9.i*ya.i - s10.i*yb.i, s10.r*yb.i - s9.r*ya.i)
	VFMUL V29.S4, V3.S4, V18.S4
	VFMUL V31.S4, V11.S4, V19.S4
	VFSUB V19.S4, V18.S4, V18.S4
	VFMUL V31.S4, V10.S4, V19.S4
	VFMUL V29.S4, V2.S4, V20.S4
	VFSUB V20.S4, V19.S4, V19.S4
	// f2m = s11 + s12, f3m = s11 - s12
	VFADD V18.S4, V16.S4, V20.S4
	VFADD V19.S4, V17.S4, V21.S4
	VST2  [V20.S4, V21.S4], (R13)
	VFSUB V18.S4, V16.S4, V20.S4
	VFSUB V19.S4, V17.S4, V21.S4
	VST2  [V20.S4, V21.S4], (R14)

	NEXT
	SUBS $1, R3
	BNE  r5block
	ADD  R9, R0
	SUBS $1, R2
	BNE  r5group
	RET
