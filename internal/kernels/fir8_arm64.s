//go:build arm64 && go1.27 && !purego

#include "textflag.h"

// One output: SMULL/SMULL2 widen the eight int16 products to int32 and an
// ADD leaves four partial sums; two ADDP levels sum four outputs into one
// vector (int32 wraps, so the order is free). Rounding and saturation:
// ((v >> 14) + 1) >> 1, then SQXTN.

// FIR8_ONE(acc): idx in R4; R0 buf, R1 table.
#define FIR8_ONE(acc) \
	ANDW   $0xffff, R4, R6 \
	MULW   R7, R6, R6 \
	LSRW   $16, R6, R6 \
	ADD    R6<<4, R1, R6 \
	LSRW   $16, R4, R8 \
	ADD    R8<<1, R0, R8 \
	VLD1   (R8), [V16.H8] \
	VLD1   (R6), [V17.H8] \
	VSMULL  V17.H4, V16.H4, V18.S4 \
	VSMULL2 V17.H8, V16.H8, V19.S4 \
	VADD   V19.S4, V18.S4, acc.S4 \
	ADDW   R5, R4, R4

// func fir8Interp4NEON(dst, buf *int16, table *FIR8Table, indexQ16, idxInc int32, n int)
TEXT ·fir8Interp4NEON(SB), NOSPLIT, $0-40
	MOVD  dst+0(FP), R2
	MOVD  buf+8(FP), R0
	MOVD  table+16(FP), R1
	MOVWU indexQ16+24(FP), R4
	MOVWU idxInc+28(FP), R5
	MOVD  n+32(FP), R3
	LSR   $2, R3
	MOVW  $12, R7
	MOVW  $1, R9
	VDUP  R9, V20.S4

fir8loop:
	FIR8_ONE(V0)
	FIR8_ONE(V1)
	FIR8_ONE(V2)
	FIR8_ONE(V3)
	VADDP  V1.S4, V0.S4, V4.S4
	VADDP  V3.S4, V2.S4, V5.S4
	VADDP  V5.S4, V4.S4, V6.S4
	VSSHR  $14, V6.S4, V6.S4
	VADD   V20.S4, V6.S4, V6.S4
	VSSHR  $1, V6.S4, V6.S4
	VSQXTN V6.S4, V7.H4
	VST1.P [V7.H4], 8(R2)
	SUBS   $1, R3
	BNE    fir8loop
	RET
