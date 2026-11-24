//go:build amd64 && !purego

#include "textflag.h"

// One output: X<acc> = PMADDWD(buf[i:i+8], taps[0:8]) + PMADDWD(buf[i+8:i+16],
// taps[8:16]), four int32 partial sums. The products are exact and the int32
// sums wrap, so their order does not matter.
#define LPC_ONE(off, acc) \
	MOVOU   off(SI), acc \
	MOVOU   off+16(SI), X7 \
	PMADDWL X8, acc \
	PMADDWL X9, X7 \
	PADDL   X7, acc

// func lpcAnalysis4SSE2(out *int16, buf *int16, in *int32, taps *[16]int16, n int)
TEXT ·lpcAnalysis4SSE2(SB), NOSPLIT, $0-40
	MOVQ  out+0(FP), DI
	MOVQ  buf+8(FP), SI
	MOVQ  in+16(FP), DX
	MOVQ  taps+24(FP), R8
	MOVQ  n+32(FP), CX
	SHRQ  $2, CX
	MOVOU (R8), X8
	MOVOU 16(R8), X9
	// X10 = four int32 ones for the rounding step.
	PCMPEQL X10, X10
	PSRLL   $31, X10

loop:
	LPC_ONE(0, X0)
	LPC_ONE(2, X1)
	LPC_ONE(4, X2)
	LPC_ONE(6, X3)
	// Transpose-add so lane k holds the full sum of output k.
	MOVO       X0, X4
	PUNPCKLLQ  X1, X0
	PUNPCKHLQ  X1, X4
	PADDL      X4, X0
	MOVO       X2, X5
	PUNPCKLLQ  X3, X2
	PUNPCKHLQ  X3, X5
	PADDL      X5, X2
	MOVO       X0, X4
	PUNPCKLQDQ X2, X0
	PUNPCKHQDQ X2, X4
	PADDL      X4, X0
	// v = (in << 12) - sum; silk_RSHIFT_ROUND(v, 12) = ((v >> 11) + 1) >> 1;
	// PACKSSDW saturates to int16.
	MOVOU    (DX), X5
	PSLLL    $12, X5
	PSUBL    X0, X5
	PSRAL    $11, X5
	PADDL    X10, X5
	PSRAL    $1, X5
	PACKSSLW X5, X5
	MOVQ     X5, (DI)
	ADDQ     $8, DI
	ADDQ     $8, SI
	ADDQ     $16, DX
	DECQ     CX
	JNZ      loop
	RET
