//go:build amd64 && !purego

#include "textflag.h"

// Same lane layout as warpedar_amd64.s: two states per XMM register, one
// per 64-bit half, int32 value in the low dword.

#define LOADPAIR(m, x) \
	MOVQ   m, x \
	PSHUFD $0x50, x, x

#define STOREPAIR(x, m) \
	PSHUFD $0x08, x, X10 \
	MOVQ   X10, m

// func lpcPred4SSE41(row *[4]int32, a *[16][2]int64, order int, out *[4]int32)
// row points at H[i+15]; tap t reads the row t rows earlier.
TEXT ·lpcPred4SSE41(SB), NOSPLIT, $0-32
	MOVQ row+0(FP), SI
	MOVQ a+8(FP), DX
	MOVQ order+16(FP), CX
	MOVQ out+24(FP), DI
	MOVQ CX, R8
	SHRQ $1, R8
	MOVQ R8, X0
	PSHUFD $0x00, X0, X0 // acc A = order >> 1
	MOVO X0, X1          // acc B

lpc4loop:
	MOVOU  (DX), X12
	LOADPAIR(0(SI), X2)
	LOADPAIR(8(SI), X3)
	PMULDQ X12, X2
	PMULDQ X12, X3
	PSRLQ  $16, X2
	PSRLQ  $16, X3
	PADDL  X2, X0
	PADDL  X3, X1
	SUBQ   $16, SI
	ADDQ   $16, DX
	DECQ   CX
	JNZ    lpc4loop

	PSLLL $4, X0
	PSLLL $4, X1
	STOREPAIR(X0, 0(DI))
	STOREPAIR(X1, 8(DI))
	RET

// func lpcPred2SSE41(row *[4]int32, a *[16][2]int64, order int, out *[4]int32)
TEXT ·lpcPred2SSE41(SB), NOSPLIT, $0-32
	MOVQ row+0(FP), SI
	MOVQ a+8(FP), DX
	MOVQ order+16(FP), CX
	MOVQ out+24(FP), DI
	MOVQ CX, R8
	SHRQ $1, R8
	MOVQ R8, X0
	PSHUFD $0x00, X0, X0

lpc2loop:
	MOVOU  (DX), X12
	LOADPAIR(0(SI), X2)
	PMULDQ X12, X2
	PSRLQ  $16, X2
	PADDL  X2, X0
	SUBQ   $16, SI
	ADDQ   $16, DX
	DECQ   CX
	JNZ    lpc2loop

	PSLLL $4, X0
	STOREPAIR(X0, 0(DI))
	RET
