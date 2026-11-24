//go:build arm64 && go1.27 && !purego

#include "textflag.h"

// Four consecutive outputs per pass, as in comb_amd64.s, with separate
// FMUL and FADD in the scalar order. period >= 6 keeps each block's reads
// behind it.

// TAP1: y += coef * x[b]           (b at R5)
// TAP2: y += coef * (x[b+k] + x[b-k])
#define TAP1(coef, base, y) \
	VLD1  (base), [V16.S4] \
	VFMUL coef.S4, V16.S4, V16.S4 \
	VFADD V16.S4, y.S4, y.S4

#define TAP2(coef, plus, minus, y) \
	VLD1  (plus), [V16.S4] \
	VLD1  (minus), [V17.S4] \
	VFADD V17.S4, V16.S4, V16.S4 \
	VFMUL coef.S4, V16.S4, V16.S4 \
	VFADD V16.S4, y.S4, y.S4

// func combConstNEON(x *float32, period, blocks int, g0, g1, g2 float32)
TEXT ·combConstNEON(SB), NOSPLIT, $0-36
	MOVD  x+0(FP), R0
	MOVD  period+8(FP), R1
	MOVD  blocks+16(FP), R2
	FMOVS g0+24(FP), F10
	VDUP  V10.S[0], V10.S4
	FMOVS g1+28(FP), F11
	VDUP  V11.S[0], V11.S4
	FMOVS g2+32(FP), F12
	VDUP  V12.S[0], V12.S4
	SUB   R1<<2, R0, R5 // x[pos - period]

cloop:
	VLD1 (R0), [V0.S4]
	TAP1(V10, R5, V0)
	ADD  $4, R5, R6
	SUB  $4, R5, R7
	TAP2(V11, R6, R7, V0)
	ADD  $8, R5, R6
	SUB  $8, R5, R7
	TAP2(V12, R6, R7, V0)
	VST1.P [V0.S4], 16(R0)
	ADD  $16, R5
	SUBS $1, R2
	BNE  cloop
	RET

// func combOverlapNEON(x *float32, p0, p1, blocks int, gains *CombGains, w *float32)
TEXT ·combOverlapNEON(SB), NOSPLIT, $0-48
	MOVD x+0(FP), R0
	MOVD p0+8(FP), R1
	MOVD p1+16(FP), R2
	MOVD blocks+24(FP), R3
	MOVD gains+32(FP), R4
	MOVD w+40(FP), R8
	VLD1R.P 4(R4), [V20.S4]
	VLD1R.P 4(R4), [V21.S4]
	VLD1R.P 4(R4), [V22.S4]
	VLD1R.P 4(R4), [V23.S4]
	VLD1R.P 4(R4), [V24.S4]
	VLD1R   (R4), [V25.S4]
	FMOVS $1.0, F26
	VDUP  V26.S[0], V26.S4
	SUB   R1<<2, R0, R5 // x[pos - p0]
	SUB   R2<<2, R0, R9 // x[pos - p1]

oloop:
	VLD1.P 16(R8), [V1.S4]
	VFMUL  V1.S4, V1.S4, V1.S4 // f = w*w
	VFSUB  V1.S4, V26.S4, V2.S4 // 1 - f
	VFMUL  V20.S4, V2.S4, V3.S4 // coefficients
	VFMUL  V21.S4, V2.S4, V4.S4
	VFMUL  V22.S4, V2.S4, V5.S4
	VFMUL  V23.S4, V1.S4, V6.S4
	VFMUL  V24.S4, V1.S4, V7.S4
	VFMUL  V25.S4, V1.S4, V8.S4

	VLD1 (R0), [V0.S4]
	TAP1(V3, R5, V0)
	ADD  $4, R5, R6
	SUB  $4, R5, R7
	TAP2(V4, R6, R7, V0)
	ADD  $8, R5, R6
	SUB  $8, R5, R7
	TAP2(V5, R6, R7, V0)
	TAP1(V6, R9, V0)
	ADD  $4, R9, R6
	SUB  $4, R9, R7
	TAP2(V7, R6, R7, V0)
	ADD  $8, R9, R6
	SUB  $8, R9, R7
	TAP2(V8, R6, R7, V0)
	VST1.P [V0.S4], 16(R0)

	ADD  $16, R5
	ADD  $16, R9
	SUBS $1, R3
	BNE  oloop
	RET
