//go:build amd64 && !purego

#include "textflag.h"

// Four butterflies per pass: lanes hold butterflies j..j+3 of one group,
// with real and imaginary parts in separate registers (deinterleaved on
// load, interleaved on store). Each lane performs the scalar kf_bfly
// operations in the same order with MULPS/ADDPS/SUBPS (no FMA).
//
// Registers: SI group base, R11 row j, R12 twiddle block, R8 = m*8 bytes,
// BX = 3*m*8, R9 = mm*8, DX groups left, CX blocks left.

DATA fftHalf<>+0(SB)/4, $0x3f000000 // 0.5
DATA fftHalf<>+4(SB)/4, $0x3f000000
DATA fftHalf<>+8(SB)/4, $0x3f000000
DATA fftHalf<>+12(SB)/4, $0x3f000000
GLOBL fftHalf<>(SB), RODATA|NOPTR, $16

DATA fftSign<>+0(SB)/4, $0x80000000
DATA fftSign<>+4(SB)/4, $0x80000000
DATA fftSign<>+8(SB)/4, $0x80000000
DATA fftSign<>+12(SB)/4, $0x80000000
GLOBL fftSign<>(SB), RODATA|NOPTR, $16

// LOADC reads four complex values at a0 (and a16 = a0+16) into re, im.
#define LOADC(a0, a16, re, im, t) \
	MOVUPS a0, re \
	MOVUPS a16, t \
	MOVAPS re, im \
	SHUFPS $0x88, t, re \
	SHUFPS $0xDD, t, im

// STOREC writes re, im as four interleaved complex values.
#define STOREC(re, im, t1, t2, a0, a16) \
	MOVAPS   re, t1 \
	UNPCKLPS im, t1 \
	MOVUPS   t1, a0 \
	MOVAPS   re, t2 \
	UNPCKHPS im, t2 \
	MOVUPS   t2, a16

// CMUL: (ar, ai) *= twiddle (twr, twi), as C_MUL:
// re = ar*br - ai*bi, im = ar*bi + ai*br. Clobbers X12-X15.
#define CMUL(ar, ai, twr, twi) \
	MOVUPS twr, X14 \
	MOVUPS twi, X15 \
	MOVAPS ar, X12 \
	MULPS  X14, X12 \
	MOVAPS ai, X13 \
	MULPS  X15, X13 \
	SUBPS  X13, X12 \
	MULPS  X15, ar \
	MULPS  X14, ai \
	ADDPS  ai, ar \
	MOVAPS ar, ai \
	MOVAPS X12, ar

#define STAGE_SETUP \
	MOVQ f+0(FP), SI \
	MOVQ t+8(FP), DI

// func fftRadix4SSE(f, t *float32, m, groups, mm int)
TEXT ·fftRadix4SSE(SB), NOSPLIT, $0-40
	STAGE_SETUP
	MOVQ m+16(FP), R8
	MOVQ groups+24(FP), DX
	MOVQ mm+32(FP), R9
	SHLQ $3, R8
	SHLQ $3, R9
	LEAQ (R8)(R8*2), BX

r4group:
	MOVQ SI, R11
	MOVQ DI, R12
	MOVQ m+16(FP), CX
	SHRQ $2, CX

r4block:
	LOADC((R11), 16(R11), X0, X1, X12)
	LOADC((R11)(R8*1), 16(R11)(R8*1), X2, X3, X12)
	CMUL(X2, X3, 0(R12), 16(R12))               // s0
	LOADC((R11)(R8*2), 16(R11)(R8*2), X4, X5, X12)
	CMUL(X4, X5, 32(R12), 48(R12))              // s1
	LOADC((R11)(BX*1), 16(R11)(BX*1), X6, X7, X12)
	CMUL(X6, X7, 64(R12), 80(R12))              // s2

	MOVAPS X0, X8 // s5 = f0 - s1
	SUBPS  X4, X8
	MOVAPS X1, X9
	SUBPS  X5, X9
	ADDPS  X4, X0 // center = f0 + s1
	ADDPS  X5, X1
	MOVAPS X2, X10 // s3 = s0 + s2
	ADDPS  X6, X10
	MOVAPS X3, X11
	ADDPS  X7, X11
	SUBPS  X6, X2 // s4 = s0 - s2
	SUBPS  X7, X3

	MOVAPS X0, X4 // f0 = center + s3
	ADDPS  X10, X4
	MOVAPS X1, X5
	ADDPS  X11, X5
	STOREC(X4, X5, X12, X13, (R11), 16(R11))
	SUBPS  X10, X0 // f2m = center - s3
	SUBPS  X11, X1
	STOREC(X0, X1, X12, X13, (R11)(R8*2), 16(R11)(R8*2))
	MOVAPS X8, X4 // fm = (s5.r + s4.i, s5.i - s4.r)
	ADDPS  X3, X4
	MOVAPS X9, X5
	SUBPS  X2, X5
	STOREC(X4, X5, X12, X13, (R11)(R8*1), 16(R11)(R8*1))
	SUBPS  X3, X8 // f3m = (s5.r - s4.i, s5.i + s4.r)
	ADDPS  X2, X9
	STOREC(X8, X9, X12, X13, (R11)(BX*1), 16(R11)(BX*1))

	ADDQ $32, R11
	ADDQ $96, R12
	DECQ CX
	JNZ  r4block
	ADDQ R9, SI
	DECQ DX
	JNZ  r4group
	RET

// func fftRadix3SSE(f, t *float32, epi float32, m, groups, mm int)
TEXT ·fftRadix3SSE(SB), NOSPLIT, $0-48
	STAGE_SETUP
	MOVSS  epi+16(FP), X11
	SHUFPS $0x00, X11, X11
	MOVUPS fftHalf<>(SB), X10
	MOVQ   m+24(FP), R8
	MOVQ   groups+32(FP), DX
	MOVQ   mm+40(FP), R9
	SHLQ   $3, R8
	SHLQ   $3, R9

r3group:
	MOVQ SI, R11
	MOVQ DI, R12
	MOVQ m+24(FP), CX
	SHRQ $2, CX

r3block:
	LOADC((R11)(R8*1), 16(R11)(R8*1), X2, X3, X12)
	CMUL(X2, X3, 0(R12), 16(R12))               // s1
	LOADC((R11)(R8*2), 16(R11)(R8*2), X4, X5, X12)
	CMUL(X4, X5, 32(R12), 48(R12))              // s2
	MOVAPS X2, X6 // s3 = s1 + s2
	ADDPS  X4, X6
	MOVAPS X3, X7
	ADDPS  X5, X7
	SUBPS  X4, X2 // s0 = s1 - s2
	SUBPS  X5, X3
	LOADC((R11), 16(R11), X0, X1, X12)

	MOVAPS X6, X8 // mid = f0 - 0.5*s3
	MULPS  X10, X8
	MOVAPS X0, X4
	SUBPS  X8, X4
	MOVAPS X7, X8
	MULPS  X10, X8
	MOVAPS X1, X5
	SUBPS  X8, X5
	MULPS  X11, X2 // s0 *= epi
	MULPS  X11, X3

	ADDPS X6, X0 // f0 += s3
	ADDPS X7, X1
	STOREC(X0, X1, X12, X13, (R11), 16(R11))
	MOVAPS X4, X6 // f2m = (mid.r + s0.i, mid.i - s0.r)
	ADDPS  X3, X6
	MOVAPS X5, X7
	SUBPS  X2, X7
	STOREC(X6, X7, X12, X13, (R11)(R8*2), 16(R11)(R8*2))
	SUBPS  X3, X4 // fm = (mid.r - s0.i, mid.i + s0.r)
	ADDPS  X2, X5
	STOREC(X4, X5, X12, X13, (R11)(R8*1), 16(R11)(R8*1))

	ADDQ $32, R11
	ADDQ $64, R12
	DECQ CX
	JNZ  r3block
	ADDQ R9, SI
	DECQ DX
	JNZ  r3group
	RET

// func fftRadix5SSE(f, t, y *float32, m, groups, mm int)
// y holds ya.r, ya.i, yb.r, yb.i, each broadcast to four lanes.
TEXT ·fftRadix5SSE(SB), NOSPLIT, $0-48
	STAGE_SETUP
	MOVQ y+16(FP), R10
	MOVQ m+24(FP), R8
	MOVQ groups+32(FP), DX
	MOVQ mm+40(FP), R9
	SHLQ $3, R8
	SHLQ $3, R9
	LEAQ (R8)(R8*2), BX

r5group:
	MOVQ SI, R11
	MOVQ DI, R12
	MOVQ m+24(FP), CX
	SHRQ $2, CX

r5block:
	LOADC((R11), 16(R11), X0, X1, X12)           // s0
	LOADC((R11)(R8*1), 16(R11)(R8*1), X2, X3, X12)
	CMUL(X2, X3, 0(R12), 16(R12))               // s1
	LOADC((R11)(R8*4), 16(R11)(R8*4), X4, X5, X12)
	CMUL(X4, X5, 96(R12), 112(R12))             // s4
	MOVAPS X2, X6 // s7 = s1 + s4
	ADDPS  X4, X6
	MOVAPS X3, X7
	ADDPS  X5, X7
	SUBPS  X4, X2 // s10 = s1 - s4
	SUBPS  X5, X3
	LOADC((R11)(R8*2), 16(R11)(R8*2), X4, X5, X12)
	CMUL(X4, X5, 32(R12), 48(R12))              // s2
	LOADC((R11)(BX*1), 16(R11)(BX*1), X8, X9, X12)
	CMUL(X8, X9, 64(R12), 80(R12))              // s3
	MOVAPS X4, X10 // s8 = s2 + s3
	ADDPS  X8, X10
	MOVAPS X5, X11
	ADDPS  X9, X11
	SUBPS  X8, X4 // s9 = s2 - s3
	SUBPS  X9, X5

	// f0 = s0 + (s7 + s8)
	MOVAPS X6, X8
	ADDPS  X10, X8
	ADDPS  X0, X8
	MOVAPS X7, X9
	ADDPS  X11, X9
	ADDPS  X1, X9
	STOREC(X8, X9, X12, X13, (R11), 16(R11))

	// s5 = s0 + (s7*ya.r + s8*yb.r)
	MOVUPS 0(R10), X14
	MOVUPS 32(R10), X15
	MOVAPS X6, X8
	MULPS  X14, X8
	MOVAPS X10, X12
	MULPS  X15, X12
	ADDPS  X12, X8
	ADDPS  X0, X8
	MOVAPS X7, X9
	MULPS  X14, X9
	MOVAPS X11, X12
	MULPS  X15, X12
	ADDPS  X12, X9
	ADDPS  X1, X9

	// s6 = (s10.i*ya.i + s9.i*yb.i, -(s10.r*ya.i + s9.r*yb.i))
	MOVUPS 16(R10), X14
	MOVUPS 48(R10), X15
	MOVAPS X3, X12
	MULPS  X14, X12
	MOVAPS X5, X13
	MULPS  X15, X13
	ADDPS  X13, X12
	MOVAPS X2, X13
	MULPS  X14, X13
	MULPS  X4, X15
	ADDPS  X15, X13
	MOVUPS fftSign<>(SB), X14
	XORPS  X14, X13

	// fm = s5 - s6, f4m = s5 + s6
	MOVAPS X8, X14
	SUBPS  X12, X14
	MOVAPS X9, X15
	SUBPS  X13, X15
	ADDPS  X12, X8
	ADDPS  X13, X9
	STOREC(X14, X15, X12, X13, (R11)(R8*1), 16(R11)(R8*1))
	STOREC(X8, X9, X12, X13, (R11)(R8*4), 16(R11)(R8*4))

	// s11 = s0 + (s7*yb.r + s8*ya.r)
	MOVUPS 32(R10), X14
	MOVUPS 0(R10), X15
	MOVAPS X6, X8
	MULPS  X14, X8
	MOVAPS X10, X12
	MULPS  X15, X12
	ADDPS  X12, X8
	ADDPS  X0, X8
	MOVAPS X7, X9
	MULPS  X14, X9
	MOVAPS X11, X12
	MULPS  X15, X12
	ADDPS  X12, X9
	ADDPS  X1, X9

	// s12 = (s9.i*ya.i - s10.i*yb.i, s10.r*yb.i - s9.r*ya.i)
	MOVUPS 16(R10), X14
	MOVUPS 48(R10), X15
	MOVAPS X5, X12
	MULPS  X14, X12
	MOVAPS X3, X13
	MULPS  X15, X13
	SUBPS  X13, X12
	MOVAPS X2, X13
	MULPS  X15, X13
	MULPS  X14, X4
	SUBPS  X4, X13

	// f2m = s11 + s12, f3m = s11 - s12
	MOVAPS X8, X14
	ADDPS  X12, X14
	MOVAPS X9, X15
	ADDPS  X13, X15
	SUBPS  X12, X8
	SUBPS  X13, X9
	STOREC(X14, X15, X12, X13, (R11)(R8*2), 16(R11)(R8*2))
	STOREC(X8, X9, X12, X13, (R11)(BX*1), 16(R11)(BX*1))

	ADDQ $32, R11
	ADDQ $128, R12
	DECQ CX
	JNZ  r5block
	ADDQ R9, SI
	DECQ DX
	JNZ  r5group
	RET
