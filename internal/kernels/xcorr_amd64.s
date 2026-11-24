//go:build amd64 && !purego

#include "textflag.h"

// Each kernel gives every lag its own lane: on step j it broadcasts x[j],
// multiplies it by the contiguous y[k+j] for all k in the block and adds
// the rounded products to the per-lag accumulators. Every lag therefore
// adds the same float32 products in the same j order as the scalar loop.
// MULPS/ADDPS round like MULSS/ADDSS and there is no FMA.

// func xcorr32AVX(x, y *float32, n int, out *float32)
TEXT ·xcorr32AVX(SB), NOSPLIT, $0-32
	MOVQ x+0(FP), SI
	MOVQ y+8(FP), DI
	MOVQ n+16(FP), CX
	MOVQ out+24(FP), DX
	VXORPS Y0, Y0, Y0
	VXORPS Y1, Y1, Y1
	VXORPS Y2, Y2, Y2
	VXORPS Y3, Y3, Y3
	TESTQ CX, CX
	JLE  avx32done

avx32loop:
	VBROADCASTSS (SI), Y4
	VMULPS (DI), Y4, Y5
	VMULPS 32(DI), Y4, Y6
	VMULPS 64(DI), Y4, Y7
	VMULPS 96(DI), Y4, Y8
	VADDPS Y5, Y0, Y0
	VADDPS Y6, Y1, Y1
	VADDPS Y7, Y2, Y2
	VADDPS Y8, Y3, Y3
	ADDQ $4, SI
	ADDQ $4, DI
	DECQ CX
	JNZ  avx32loop

avx32done:
	VMOVUPS Y0, (DX)
	VMOVUPS Y1, 32(DX)
	VMOVUPS Y2, 64(DX)
	VMOVUPS Y3, 96(DX)
	VZEROUPPER
	RET

// func xcorr8AVX(x, y *float32, n int, out *float32)
TEXT ·xcorr8AVX(SB), NOSPLIT, $0-32
	MOVQ x+0(FP), SI
	MOVQ y+8(FP), DI
	MOVQ n+16(FP), CX
	MOVQ out+24(FP), DX
	VXORPS Y0, Y0, Y0
	TESTQ CX, CX
	JLE  avx8done

avx8loop:
	VBROADCASTSS (SI), Y4
	VMULPS (DI), Y4, Y5
	VADDPS Y5, Y0, Y0
	ADDQ $4, SI
	ADDQ $4, DI
	DECQ CX
	JNZ  avx8loop

avx8done:
	VMOVUPS Y0, (DX)
	VZEROUPPER
	RET

// func xcorr16SSE(x, y *float32, n int, out *float32)
TEXT ·xcorr16SSE(SB), NOSPLIT, $0-32
	MOVQ x+0(FP), SI
	MOVQ y+8(FP), DI
	MOVQ n+16(FP), CX
	MOVQ out+24(FP), DX
	XORPS X0, X0
	XORPS X1, X1
	XORPS X2, X2
	XORPS X3, X3
	TESTQ CX, CX
	JLE  sse16done

sse16loop:
	MOVSS  (SI), X4
	SHUFPS $0x00, X4, X4
	MOVUPS (DI), X5
	MOVUPS 16(DI), X6
	MOVUPS 32(DI), X7
	MOVUPS 48(DI), X8
	MULPS  X4, X5
	MULPS  X4, X6
	MULPS  X4, X7
	MULPS  X4, X8
	ADDPS  X5, X0
	ADDPS  X6, X1
	ADDPS  X7, X2
	ADDPS  X8, X3
	ADDQ   $4, SI
	ADDQ   $4, DI
	DECQ   CX
	JNZ    sse16loop

sse16done:
	MOVUPS X0, (DX)
	MOVUPS X1, 16(DX)
	MOVUPS X2, 32(DX)
	MOVUPS X3, 48(DX)
	RET

// func xcorr4SSE(x, y *float32, n int, out *float32)
TEXT ·xcorr4SSE(SB), NOSPLIT, $0-32
	MOVQ x+0(FP), SI
	MOVQ y+8(FP), DI
	MOVQ n+16(FP), CX
	MOVQ out+24(FP), DX
	XORPS X0, X0
	TESTQ CX, CX
	JLE  sse4done

sse4loop:
	MOVSS  (SI), X4
	SHUFPS $0x00, X4, X4
	MOVUPS (DI), X5
	MULPS  X4, X5
	ADDPS  X5, X0
	ADDQ   $4, SI
	ADDQ   $4, DI
	DECQ   CX
	JNZ    sse4loop

sse4done:
	MOVUPS X0, (DX)
	RET
