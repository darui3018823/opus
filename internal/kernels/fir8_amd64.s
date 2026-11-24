//go:build amd64 && !purego

#include "textflag.h"

// One output: X<acc> = PMADDWD(buf[idx>>16 : +8], table[phase]), which
// leaves four int32 pair sums; idx advances by idxInc.
#define FIR8_ONE(acc) \
	MOVL  AX, DX \
	ANDL  $0xFFFF, DX \
	IMULL $12, DX \
	SHRL  $16, DX \
	SHLL  $4, DX \
	MOVL  AX, R9 \
	SHRL  $16, R9 \
	MOVOU (SI)(R9*2), acc \
	MOVOU (R8)(DX*1), X7 \
	PMADDWL X7, acc \
	ADDL  BX, AX

// func fir8Interp4SSE2(dst, buf *int16, table *FIR8Table, indexQ16, idxInc int32, n int)
TEXT ·fir8Interp4SSE2(SB), NOSPLIT, $0-40
	MOVQ dst+0(FP), DI
	MOVQ buf+8(FP), SI
	MOVQ table+16(FP), R8
	MOVL indexQ16+24(FP), AX
	MOVL idxInc+28(FP), BX
	MOVQ n+32(FP), CX
	SHRQ $2, CX
	// X6 = four int32 ones for the rounding step.
	PCMPEQL X6, X6
	PSRLL   $31, X6

loop:
	FIR8_ONE(X0)
	FIR8_ONE(X1)
	FIR8_ONE(X2)
	FIR8_ONE(X3)
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
	// silk_RSHIFT_ROUND(v, 15) = ((v >> 14) + 1) >> 1, then saturate.
	PSRAL    $14, X0
	PADDL    X6, X0
	PSRAL    $1, X0
	PACKSSLW X0, X0
	MOVQ     X0, (DI)
	ADDQ     $8, DI
	DECQ     CX
	JNZ      loop
	RET
