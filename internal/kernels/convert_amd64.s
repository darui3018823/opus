//go:build amd64 && !purego

#include "textflag.h"

// CVTPD2PS rounds like float32(x) (default MXCSR); CVTPS2PD is exact.

// func f64to32SSE2(dst *float32, src *float64, blocks int)
TEXT ·f64to32SSE2(SB), NOSPLIT, $0-24
	MOVQ dst+0(FP), DI
	MOVQ src+8(FP), SI
	MOVQ blocks+16(FP), CX

c64loop:
	MOVUPD   0(SI), X0
	MOVUPD   16(SI), X1
	CVTPD2PS X0, X0
	CVTPD2PS X1, X1
	MOVLHPS  X1, X0
	MOVUPS   X0, 0(DI)
	ADDQ     $32, SI
	ADDQ     $16, DI
	DECQ     CX
	JNZ      c64loop
	RET

// func f32to64SSE2(dst *float64, src *float32, blocks int)
TEXT ·f32to64SSE2(SB), NOSPLIT, $0-24
	MOVQ dst+0(FP), DI
	MOVQ src+8(FP), SI
	MOVQ blocks+16(FP), CX

c32loop:
	MOVUPS   0(SI), X0
	CVTPS2PD X0, X1
	MOVHLPS  X0, X0
	CVTPS2PD X0, X2
	MOVUPD   X1, 0(DI)
	MOVUPD   X2, 16(DI)
	ADDQ     $16, SI
	ADDQ     $32, DI
	DECQ     CX
	JNZ      c32loop
	RET
