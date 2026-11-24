//go:build arm64 && go1.27 && !purego

#include "textflag.h"

// One output: SMULL/SMULL2 widen the sixteen int16 products to int32 and
// ADDs leave four partial sums; two ADDP levels sum four outputs into one
// vector. The products are exact and the int32 sums wrap, so their order
// does not matter.

// LPC_ONE(off, acc): R1 buf; V8, V9 taps.
#define LPC_ONE(off, acc) \
	ADD     $off, R1, R6 \
	VLD1    (R6), [V16.H8, V17.H8] \
	VSMULL  V8.H4, V16.H4, V18.S4 \
	VSMULL2 V8.H8, V16.H8, V19.S4 \
	VADD    V19.S4, V18.S4, acc.S4 \
	VSMULL  V9.H4, V17.H4, V18.S4 \
	VSMULL2 V9.H8, V17.H8, V19.S4 \
	VADD    V18.S4, acc.S4, acc.S4 \
	VADD    V19.S4, acc.S4, acc.S4

// func lpcAnalysis4NEON(out *int16, buf *int16, in *int32, taps *[16]int16, n int)
TEXT ·lpcAnalysis4NEON(SB), NOSPLIT, $0-40
	MOVD out+0(FP), R0
	MOVD buf+8(FP), R1
	MOVD in+16(FP), R2
	MOVD taps+24(FP), R3
	MOVD n+32(FP), R4
	LSR  $2, R4
	VLD1 (R3), [V8.H8, V9.H8]
	MOVW $1, R9
	VDUP R9, V20.S4

lpcloop:
	LPC_ONE(0, V0)
	LPC_ONE(2, V1)
	LPC_ONE(4, V2)
	LPC_ONE(6, V3)
	VADDP  V1.S4, V0.S4, V4.S4
	VADDP  V3.S4, V2.S4, V5.S4
	VADDP  V5.S4, V4.S4, V6.S4
	// v = (in << 12) - sum; silk_RSHIFT_ROUND(v, 12) = ((v >> 11) + 1) >> 1;
	// SQXTN saturates to int16.
	VLD1.P 16(R2), [V10.S4]
	VSHL   $12, V10.S4, V10.S4
	VSUB   V6.S4, V10.S4, V10.S4
	VSSHR  $11, V10.S4, V10.S4
	VADD   V20.S4, V10.S4, V10.S4
	VSSHR  $1, V10.S4, V10.S4
	VSQXTN V10.S4, V11.H4
	VST1.P [V11.H4], 8(R0)
	ADD    $8, R1, R1
	SUBS   $1, R4
	BNE    lpcloop
	RET
