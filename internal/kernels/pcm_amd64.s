//go:build amd64 && !purego

#include "textflag.h"

DATA pcmScale<>+0(SB)/4, $0x47000000 // 32768
DATA pcmScale<>+4(SB)/4, $0x47000000
DATA pcmScale<>+8(SB)/4, $0x47000000
DATA pcmScale<>+12(SB)/4, $0x47000000
GLOBL pcmScale<>(SB), RODATA|NOPTR, $16

DATA pcmLow<>+0(SB)/4, $0xc7000000 // -32768
DATA pcmLow<>+4(SB)/4, $0xc7000000
DATA pcmLow<>+8(SB)/4, $0xc7000000
DATA pcmLow<>+12(SB)/4, $0xc7000000
GLOBL pcmLow<>(SB), RODATA|NOPTR, $16

DATA pcmHigh<>+0(SB)/4, $0x46fffe00 // 32767
DATA pcmHigh<>+4(SB)/4, $0x46fffe00
DATA pcmHigh<>+8(SB)/4, $0x46fffe00
DATA pcmHigh<>+12(SB)/4, $0x46fffe00
GLOBL pcmHigh<>(SB), RODATA|NOPTR, $16

// MAXPS/MINPS return their source operand when either input is NaN, so
// with the sample as destination and the bound as source:
//   max(x, -32768) = x > -32768 ? x : -32768  (NaN -> -32768)
//   min(x,  32767) = x <  32767 ? x :  32767
// which is exactly the scalar clamp. CVTPS2PL rounds half to even under the
// default MXCSR, and the clamped values fit int16, so PACKSSLW is exact.

// func float32ToInt16SSE2(dst *int16, src *float32, n int)
TEXT ·float32ToInt16SSE2(SB), NOSPLIT, $0-24
	MOVQ  dst+0(FP), DI
	MOVQ  src+8(FP), SI
	MOVQ  n+16(FP), CX
	SHRQ  $3, CX
	MOVUPS pcmScale<>(SB), X4
	MOVUPS pcmLow<>(SB), X5
	MOVUPS pcmHigh<>(SB), X6

loop:
	MOVUPS   (SI), X0
	MOVUPS   16(SI), X1
	MULPS    X4, X0
	MULPS    X4, X1
	MAXPS    X5, X0
	MAXPS    X5, X1
	MINPS    X6, X0
	MINPS    X6, X1
	CVTPS2PL X0, X0
	CVTPS2PL X1, X1
	PACKSSLW X1, X0
	MOVOU    X0, (DI)
	ADDQ     $32, SI
	ADDQ     $16, DI
	DECQ     CX
	JNZ      loop
	RET
