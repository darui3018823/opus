//go:build amd64 && !purego

#include "textflag.h"

// Four consecutive outputs per pass, one per lane. Every product is
// rounded before it is added (MULPS then ADDPS, no FMA), in the scalar
// order. The caller guarantees period >= 6, so a block reads only samples
// written before it.

DATA combOne<>+0(SB)/4, $0x3f800000
DATA combOne<>+4(SB)/4, $0x3f800000
DATA combOne<>+8(SB)/4, $0x3f800000
DATA combOne<>+12(SB)/4, $0x3f800000
GLOBL combOne<>(SB), RODATA|NOPTR, $16

#define BCASTF(m, x) \
	MOVSS  m, x \
	SHUFPS $0x00, x, x

// TAP adds coef * x[b] (b at 0(base)).
#define TAP1(coef, base, y, t) \
	MOVUPS 0(base), t \
	MULPS  coef, t \
	ADDPS  t, y

// TAP2 adds coef * (x[b+k] + x[b-k]), k*4 = off.
#define TAP2(coef, base, plus, minus, y, t, u) \
	MOVUPS plus(base), t \
	MOVUPS minus(base), u \
	ADDPS  u, t \
	MULPS  coef, t \
	ADDPS  t, y

// func combConstSSE(x *float32, period, blocks int, g0, g1, g2 float32)
// x points at the first output.
TEXT ·combConstSSE(SB), NOSPLIT, $0-36
	MOVQ x+0(FP), DI
	MOVQ period+8(FP), AX
	MOVQ blocks+16(FP), CX
	BCASTF(g0+24(FP), X10)
	BCASTF(g1+28(FP), X11)
	BCASTF(g2+32(FP), X12)
	SHLQ $2, AX
	MOVQ DI, SI
	SUBQ AX, SI // SI -> x[pos - period]

cloop:
	MOVUPS 0(DI), X0
	TAP1(X10, SI, X0, X1)
	TAP2(X11, SI, 4, -4, X0, X1, X2)
	TAP2(X12, SI, 8, -8, X0, X1, X2)
	MOVUPS X0, 0(DI)
	ADDQ   $16, DI
	ADDQ   $16, SI
	DECQ   CX
	JNZ    cloop
	RET

// func combOverlapSSE(x *float32, p0, p1, blocks int, gains *CombGains, w *float32)
TEXT ·combOverlapSSE(SB), NOSPLIT, $0-48
	MOVQ x+0(FP), DI
	MOVQ p0+8(FP), AX
	MOVQ p1+16(FP), BX
	MOVQ blocks+24(FP), CX
	MOVQ gains+32(FP), DX
	MOVQ w+40(FP), R8
	SHLQ $2, AX
	SHLQ $2, BX
	MOVQ DI, SI
	SUBQ AX, SI // SI -> x[pos - p0]
	MOVQ DI, R9
	SUBQ BX, R9 // R9 -> x[pos - p1]
	MOVUPS combOne<>(SB), X15

oloop:
	MOVUPS 0(R8), X1 // f = w*w, omf = 1 - f
	MULPS  X1, X1
	MOVAPS X15, X2
	SUBPS  X1, X2

	BCASTF(0(DX), X3) // coefficients for this block
	MULPS  X2, X3
	BCASTF(4(DX), X4)
	MULPS  X2, X4
	BCASTF(8(DX), X5)
	MULPS  X2, X5
	BCASTF(12(DX), X6)
	MULPS  X1, X6
	BCASTF(16(DX), X7)
	MULPS  X1, X7
	BCASTF(20(DX), X8)
	MULPS  X1, X8

	MOVUPS 0(DI), X0
	TAP1(X3, SI, X0, X9)
	TAP2(X4, SI, 4, -4, X0, X9, X10)
	TAP2(X5, SI, 8, -8, X0, X9, X10)
	TAP1(X6, R9, X0, X9)
	TAP2(X7, R9, 4, -4, X0, X9, X10)
	TAP2(X8, R9, 8, -8, X0, X9, X10)
	MOVUPS X0, 0(DI)

	ADDQ $16, DI
	ADDQ $16, SI
	ADDQ $16, R9
	ADDQ $16, R8
	DECQ CX
	JNZ  oloop
	RET
