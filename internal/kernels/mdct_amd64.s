//go:build amd64 && !purego

#include "textflag.h"

// Four rotations per pass. Products are rounded before they are added
// (MULPS then ADDPS/SUBPS, no FMA), float64 inputs are rounded to float32
// with CVTPD2PS like float32(x), and outputs are widened with CVTPS2PD,
// which is exact.

// func imdctPreSSE(fout *float32, x *float64, trig *float32, bitrev *int, n4, blocks int)
TEXT ·imdctPreSSE(SB), NOSPLIT, $0-48
	MOVQ fout+0(FP), DI
	MOVQ x+8(FP), SI
	MOVQ trig+16(FP), R8
	MOVQ bitrev+24(FP), R9
	MOVQ n4+32(FP), DX
	MOVQ blocks+40(FP), CX
	// R10: x[2n4-8-2i] (backward window), R11: trig[n4+i]
	MOVQ DX, AX
	SHLQ $4, AX
	LEAQ -64(SI)(AX*1), R10
	LEAQ (R8)(DX*4), R11

preloop:
	// xp1 = x[2i], x[2i+2], x[2i+4], x[2i+6]
	MOVUPD   0(SI), X0
	CVTPD2PS X0, X0
	MOVUPD   16(SI), X1
	CVTPD2PS X1, X1
	MOVLHPS  X1, X0
	MOVUPD   32(SI), X2
	CVTPD2PS X2, X2
	MOVUPD   48(SI), X3
	CVTPD2PS X3, X3
	MOVLHPS  X3, X2
	SHUFPS   $0x88, X2, X0
	// xp2 = x[2n4-1-2i], x[2n4-3-2i], ... (odd slots of the window, reversed)
	MOVUPD   0(R10), X4
	CVTPD2PS X4, X4
	MOVUPD   16(R10), X1
	CVTPD2PS X1, X1
	MOVLHPS  X1, X4
	MOVUPD   32(R10), X5
	CVTPD2PS X5, X5
	MOVUPD   48(R10), X3
	CVTPD2PS X3, X3
	MOVLHPS  X3, X5
	SHUFPS   $0xDD, X5, X4
	SHUFPS   $0x1B, X4, X4

	MOVUPS (R8), X6  // t0
	MOVUPS (R11), X7 // t1
	MOVAPS X4, X8    // yr = xp2*t0 + xp1*t1
	MULPS  X6, X8
	MOVAPS X0, X9
	MULPS  X7, X9
	ADDPS  X9, X8
	MOVAPS X0, X9    // yi = xp1*t0 - xp2*t1
	MULPS  X6, X9
	MOVAPS X4, X10
	MULPS  X7, X10
	SUBPS  X10, X9

	MOVAPS   X9, X10 // (yi, yr) pairs
	UNPCKLPS X8, X10
	MOVAPS   X9, X11
	UNPCKHPS X8, X11
	MOVQ     0(R9), AX
	MOVQ     X10, (DI)(AX*8)
	MOVQ     8(R9), AX
	MOVHPS   X10, (DI)(AX*8)
	MOVQ     16(R9), AX
	MOVQ     X11, (DI)(AX*8)
	MOVQ     24(R9), AX
	MOVHPS   X11, (DI)(AX*8)

	ADDQ $64, SI
	SUBQ $64, R10
	ADDQ $16, R8
	ADDQ $16, R11
	ADDQ $32, R9
	DECQ CX
	JNZ  preloop
	RET

// STORE4D widens four float32 (lo pair, hi pair of x) to float64 at m.
#define STORE4D(x, m0, m16) \
	CVTPS2PD x, X14 \
	MOVUPD   X14, m0 \
	MOVHLPS  x, x \
	CVTPS2PD x, X14 \
	MOVUPD   X14, m16

// func imdctPostSSE(buf *float64, fout, trig *float32, n4, blocks int)
TEXT ·imdctPostSSE(SB), NOSPLIT, $0-40
	MOVQ buf+0(FP), DI
	MOVQ fout+8(FP), SI
	MOVQ trig+16(FP), R8
	MOVQ n4+24(FP), DX
	MOVQ blocks+32(FP), CX
	LEAQ -32(SI)(DX*8), R9   // fout[n4-4-i]
	LEAQ (R8)(DX*4), R10     // trig[n4+i]
	LEAQ -16(R8)(DX*4), R11  // trig[n4-4-i]
	LEAQ -16(R8)(DX*8), R12  // trig[2n4-4-i]
	MOVQ DX, AX
	SHLQ $4, AX
	LEAQ -64(DI)(AX*1), R13 // buf[2n4-8-2i]

postloop:
	// Forward: fout[i..i+3], re = .i, im = .r
	MOVUPS 0(SI), X0
	MOVUPS 16(SI), X2
	MOVAPS X0, X1
	SHUFPS $0x88, X2, X0
	SHUFPS $0xDD, X2, X1
	MOVUPS (R8), X2
	MOVUPS (R10), X3
	MOVAPS X1, X4 // yr = re*t0 + im*t1
	MULPS  X2, X4
	MOVAPS X0, X5
	MULPS  X3, X5
	ADDPS  X5, X4
	MOVAPS X1, X5 // yi = re*t1 - im*t0
	MULPS  X3, X5
	MOVAPS X0, X6
	MULPS  X2, X6
	SUBPS  X6, X5

	// Backward: fout[k1-3..k1] reversed to lanes i..i+3
	MOVUPS 0(R9), X6
	MOVUPS 16(R9), X2
	MOVAPS X6, X7
	SHUFPS $0x88, X2, X6
	SHUFPS $0xDD, X2, X7
	SHUFPS $0x1B, X6, X6
	SHUFPS $0x1B, X7, X7
	MOVUPS (R11), X8
	SHUFPS $0x1B, X8, X8
	MOVUPS (R12), X9
	SHUFPS $0x1B, X9, X9
	MOVAPS X7, X10 // yr2 = re*t0' + im*t1'
	MULPS  X8, X10
	MOVAPS X6, X11
	MULPS  X9, X11
	ADDPS  X11, X10
	MOVAPS X7, X11 // yi2 = re*t1' - im*t0'
	MULPS  X9, X11
	MOVAPS X6, X12
	MULPS  X8, X12
	SUBPS  X12, X11

	// buf[2i..2i+7] = yr0, yi2_0, yr1, yi2_1, ...
	MOVAPS   X4, X12
	UNPCKLPS X11, X12
	MOVAPS   X4, X13
	UNPCKHPS X11, X13
	STORE4D(X12, 0(DI), 16(DI))
	STORE4D(X13, 32(DI), 48(DI))

	// buf[2n4-8-2i..] = yr2_3, yi_3, yr2_2, yi_2, ..., yr2_0, yi_0
	SHUFPS   $0x1B, X10, X10
	SHUFPS   $0x1B, X5, X5
	MOVAPS   X10, X12
	UNPCKLPS X5, X12
	MOVAPS   X10, X13
	UNPCKHPS X5, X13
	STORE4D(X12, 0(R13), 16(R13))
	STORE4D(X13, 32(R13), 48(R13))

	ADDQ $32, SI
	SUBQ $32, R9
	ADDQ $16, R8
	ADDQ $16, R10
	SUBQ $16, R11
	SUBQ $16, R12
	ADDQ $64, DI
	SUBQ $64, R13
	DECQ CX
	JNZ  postloop
	RET
