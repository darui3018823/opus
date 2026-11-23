//go:build amd64 && !purego

#include "go_asm.h"
#include "textflag.h"

// Lane layout: each XMM register holds two decision states, one per
// 64-bit half, with the state's int32 in the low dword. PMULDQ multiplies
// the low dwords as signed 32x32->64, PSRLQ $16 leaves bits 16..47 of the
// product in the low dword - the same bits as libopus's (int64 product)
// >> 16 truncated to int32 - and PADDL/PSUBL/PSLLL wrap in int32 like the
// scalar code. The high dwords hold garbage that is never read.
// Register set A (states 0, 1): tmp1 X0, tmp2 X1, n X2.
// Register set B (states 2, 3): tmp1 X5, tmp2 X6, n X7.
// X13 = warp, X12 = current coefficient, X3/X4/X8/X9 scratch.

// LOADPAIR spreads the two int32 at m into the low dwords of x's halves.
#define LOADPAIR(m, x) \
	MOVQ   m, x \
	PSHUFD $0x50, x, x

// STOREPAIR packs the low dwords of x's halves to the two int32 at m.
#define STOREPAIR(x, m) \
	PSHUFD $0x08, x, X10 \
	MOVQ   X10, m

// MULACC: acc += (b * coef) >> 16, via scratch t.
#define MULACC(acc, b, coef, t) \
	MOVO   b, t \
	PMULDQ coef, t \
	PSRLQ  $16, t \
	PADDL  t, acc

// func warpedAR4SSE41(w *WarpedAR4, diff, lfAR, out *[4]int32)
TEXT ·warpedAR4SSE41(SB), NOSPLIT, $0-32
	MOVQ  w+0(FP), SI
	MOVQ  diff+8(FP), AX
	MOVQ  lfAR+16(FP), BX
	MOVQ  out+24(FP), DI
	MOVOU WarpedAR4_Warp(SI), X13
	MOVQ  WarpedAR4_Order(SI), R11

	// tmp2 = SMLAWB(diff, s[0], warp)
	LOADPAIR(0(AX), X1)
	LOADPAIR(8(AX), X6)
	LOADPAIR(0(SI), X3)
	LOADPAIR(8(SI), X8)
	MULACC(X1, X3, X13, X4)
	MULACC(X6, X8, X13, X9)

	// tmp1 = SMLAWB(s[0], s[1] - tmp2, warp)
	MOVO X3, X0
	MOVO X8, X5
	LOADPAIR(16(SI), X4)
	LOADPAIR(24(SI), X9)
	PSUBL  X1, X4
	PSUBL  X6, X9
	PMULDQ X13, X4
	PMULDQ X13, X9
	PSRLQ  $16, X4
	PSRLQ  $16, X9
	PADDL  X4, X0
	PADDL  X9, X5

	// s[0] = tmp2
	STOREPAIR(X1, 0(SI))
	STOREPAIR(X6, 8(SI))

	// n = order >> 1; n += SMULWB(tmp2, ar[0])
	MOVQ   R11, R12
	SHRQ   $1, R12
	MOVQ   R12, X2
	PSHUFD $0x00, X2, X2
	MOVO   X2, X7
	MOVOU  WarpedAR4_Ar(SI), X12
	MULACC(X2, X1, X12, X4)
	MULACC(X7, X6, X12, X9)

	MOVQ $2, R10 // j

loop:
	LEAQ 1(R10), R12
	CMPQ R12, R11
	JGE  tail
	MOVQ R10, R13
	SHLQ $4, R13 // byte offset of row j

	// tmp2' = SMLAWB(s[j-1], s[j] - tmp1, warp)
	LOADPAIR((SI)(R13*1), X4)
	LOADPAIR(8(SI)(R13*1), X9)
	PSUBL  X0, X4
	PSUBL  X5, X9
	PMULDQ X13, X4
	PMULDQ X13, X9
	PSRLQ  $16, X4
	PSRLQ  $16, X9
	LOADPAIR(-16(SI)(R13*1), X3)
	LOADPAIR(-8(SI)(R13*1), X8)
	PADDL  X4, X3
	PADDL  X9, X8

	// s[j-1] = tmp1; n += SMULWB(tmp1, ar[j-1])
	STOREPAIR(X0, -16(SI)(R13*1))
	STOREPAIR(X5, -8(SI)(R13*1))
	MOVOU WarpedAR4_Ar-16(SI)(R13*1), X12
	MULACC(X2, X0, X12, X4)
	MULACC(X7, X5, X12, X9)
	MOVO  X3, X1
	MOVO  X8, X6

	// tmp1' = SMLAWB(s[j], s[j+1] - tmp2', warp)
	LOADPAIR(16(SI)(R13*1), X4)
	LOADPAIR(24(SI)(R13*1), X9)
	PSUBL  X1, X4
	PSUBL  X6, X9
	PMULDQ X13, X4
	PMULDQ X13, X9
	PSRLQ  $16, X4
	PSRLQ  $16, X9
	LOADPAIR((SI)(R13*1), X0)
	LOADPAIR(8(SI)(R13*1), X5)
	PADDL  X4, X0
	PADDL  X9, X5

	// s[j] = tmp2'; n += SMULWB(tmp2', ar[j])
	STOREPAIR(X1, (SI)(R13*1))
	STOREPAIR(X6, 8(SI)(R13*1))
	MOVOU WarpedAR4_Ar(SI)(R13*1), X12
	MULACC(X2, X1, X12, X4)
	MULACC(X7, X6, X12, X9)

	ADDQ $2, R10
	JMP  loop

tail:
	// s[order-1] = tmp1; n += SMULWB(tmp1, ar[order-1])
	LEAQ -1(R11), R13
	SHLQ $4, R13
	STOREPAIR(X0, (SI)(R13*1))
	STOREPAIR(X5, 8(SI)(R13*1))
	MOVOU WarpedAR4_Ar(SI)(R13*1), X12
	MULACC(X2, X0, X12, X4)
	MULACC(X7, X5, X12, X9)

	// n = ((n << 1) + SMULWB(lfAR, tilt)) << 2
	PSLLL $1, X2
	PSLLL $1, X7
	MOVOU WarpedAR4_Tilt(SI), X12
	LOADPAIR(0(BX), X3)
	LOADPAIR(8(BX), X8)
	MULACC(X2, X3, X12, X4)
	MULACC(X7, X8, X12, X9)
	PSLLL $2, X2
	PSLLL $2, X7

	STOREPAIR(X2, 0(DI))
	STOREPAIR(X7, 8(DI))
	RET

// func warpedAR2SSE41(w *WarpedAR4, diff, lfAR, out *[4]int32)
// Lanes 0 and 1 only (register set A); lanes 2 and 3 are left untouched.
TEXT ·warpedAR2SSE41(SB), NOSPLIT, $0-32
	MOVQ  w+0(FP), SI
	MOVQ  diff+8(FP), AX
	MOVQ  lfAR+16(FP), BX
	MOVQ  out+24(FP), DI
	MOVOU WarpedAR4_Warp(SI), X13
	MOVQ  WarpedAR4_Order(SI), R11

	// tmp2 = SMLAWB(diff, s[0], warp)
	LOADPAIR(0(AX), X1)
	LOADPAIR(0(SI), X3)
	MULACC(X1, X3, X13, X4)

	// tmp1 = SMLAWB(s[0], s[1] - tmp2, warp)
	MOVO X3, X0
	LOADPAIR(16(SI), X4)
	PSUBL  X1, X4
	PMULDQ X13, X4
	PSRLQ  $16, X4
	PADDL  X4, X0

	// s[0] = tmp2
	STOREPAIR(X1, 0(SI))

	// n = order >> 1; n += SMULWB(tmp2, ar[0])
	MOVQ   R11, R12
	SHRQ   $1, R12
	MOVQ   R12, X2
	PSHUFD $0x00, X2, X2
	MOVOU  WarpedAR4_Ar(SI), X12
	MULACC(X2, X1, X12, X4)

	MOVQ $2, R10 // j

loop2:
	LEAQ 1(R10), R12
	CMPQ R12, R11
	JGE  tail2
	MOVQ R10, R13
	SHLQ $4, R13 // byte offset of row j

	// tmp2' = SMLAWB(s[j-1], s[j] - tmp1, warp)
	LOADPAIR((SI)(R13*1), X4)
	PSUBL  X0, X4
	PMULDQ X13, X4
	PSRLQ  $16, X4
	LOADPAIR(-16(SI)(R13*1), X3)
	PADDL  X4, X3

	// s[j-1] = tmp1; n += SMULWB(tmp1, ar[j-1])
	STOREPAIR(X0, -16(SI)(R13*1))
	MOVOU WarpedAR4_Ar-16(SI)(R13*1), X12
	MULACC(X2, X0, X12, X4)
	MOVO  X3, X1

	// tmp1' = SMLAWB(s[j], s[j+1] - tmp2', warp)
	LOADPAIR(16(SI)(R13*1), X4)
	PSUBL  X1, X4
	PMULDQ X13, X4
	PSRLQ  $16, X4
	LOADPAIR((SI)(R13*1), X0)
	PADDL  X4, X0

	// s[j] = tmp2'; n += SMULWB(tmp2', ar[j])
	STOREPAIR(X1, (SI)(R13*1))
	MOVOU WarpedAR4_Ar(SI)(R13*1), X12
	MULACC(X2, X1, X12, X4)

	ADDQ $2, R10
	JMP  loop2

tail2:
	// s[order-1] = tmp1; n += SMULWB(tmp1, ar[order-1])
	LEAQ -1(R11), R13
	SHLQ $4, R13
	STOREPAIR(X0, (SI)(R13*1))
	MOVOU WarpedAR4_Ar(SI)(R13*1), X12
	MULACC(X2, X0, X12, X4)

	// n = ((n << 1) + SMULWB(lfAR, tilt)) << 2
	PSLLL $1, X2
	MOVOU WarpedAR4_Tilt(SI), X12
	LOADPAIR(0(BX), X3)
	MULACC(X2, X3, X12, X4)
	PSLLL $2, X2

	STOREPAIR(X2, 0(DI))
	RET
