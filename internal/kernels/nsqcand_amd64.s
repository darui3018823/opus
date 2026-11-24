//go:build amd64 && !purego

#include "go_asm.h"
#include "textflag.h"

// Four int32 lanes per XMM register, one per decision state. Every branch
// of the scalar code becomes a lane mask (all ones or zero), and every
// select becomes (a & m) | (b &^ m). The int32 operations wrap like the
// scalar code; the saturating ones detect overflow from the sign bits.

DATA nsqRandMul<>+0(SB)/4, $196314165
DATA nsqRandMul<>+4(SB)/4, $196314165
DATA nsqRandMul<>+8(SB)/4, $196314165
DATA nsqRandMul<>+12(SB)/4, $196314165
GLOBL nsqRandMul<>(SB), RODATA|NOPTR, $16

DATA nsqRandAdd<>+0(SB)/4, $907633515
DATA nsqRandAdd<>+4(SB)/4, $907633515
DATA nsqRandAdd<>+8(SB)/4, $907633515
DATA nsqRandAdd<>+12(SB)/4, $907633515
GLOBL nsqRandAdd<>(SB), RODATA|NOPTR, $16

DATA nsqMaxInt<>+0(SB)/4, $0x7fffffff
DATA nsqMaxInt<>+4(SB)/4, $0x7fffffff
DATA nsqMaxInt<>+8(SB)/4, $0x7fffffff
DATA nsqMaxInt<>+12(SB)/4, $0x7fffffff
GLOBL nsqMaxInt<>(SB), RODATA|NOPTR, $16

DATA nsqLimLo<>+0(SB)/4, $-31744 // -(31 << 10)
DATA nsqLimLo<>+4(SB)/4, $-31744
DATA nsqLimLo<>+8(SB)/4, $-31744
DATA nsqLimLo<>+12(SB)/4, $-31744
GLOBL nsqLimLo<>(SB), RODATA|NOPTR, $16

DATA nsqLimHi<>+0(SB)/4, $30720 // 30 << 10
DATA nsqLimHi<>+4(SB)/4, $30720
DATA nsqLimHi<>+8(SB)/4, $30720
DATA nsqLimHi<>+12(SB)/4, $30720
GLOBL nsqLimHi<>(SB), RODATA|NOPTR, $16

DATA nsqAdj<>+0(SB)/4, $80 // QUANT_LEVEL_ADJUST_Q10
DATA nsqAdj<>+4(SB)/4, $80
DATA nsqAdj<>+8(SB)/4, $80
DATA nsqAdj<>+12(SB)/4, $80
GLOBL nsqAdj<>(SB), RODATA|NOPTR, $16

DATA nsqK1024<>+0(SB)/4, $1024
DATA nsqK1024<>+4(SB)/4, $1024
DATA nsqK1024<>+8(SB)/4, $1024
DATA nsqK1024<>+12(SB)/4, $1024
GLOBL nsqK1024<>(SB), RODATA|NOPTR, $16

// BCAST32 broadcasts the int32 at m to the four lanes of x.
#define BCAST32(m, x) \
	MOVL   m, AX \
	MOVQ   AX, x \
	PSHUFD $0x00, x, x

// SMULW4: out = (int64(b) * c) >> 16 per lane, c a sign-extended constant
// in both qwords of cq. PMULDQ reads dwords 0 and 2; the odd lanes are
// moved down, multiplied, and their bits 16..47 shifted into the high
// dwords, then PBLENDW takes those.
#define SMULW4(b, cq, out, t) \
	MOVO    b, out \
	PMULDQ  cq, out \
	PSRLQ   $16, out \
	MOVO    b, t \
	PSRLQ   $32, t \
	PMULDQ  cq, t \
	PSLLQ   $16, t \
	PBLENDW $0xCC, t, out

// SEXT16 sign-extends the low 16 bits of each lane (as silk_SMULBB reads).
#define SEXT16(x) \
	PSLLL $16, x \
	PSRAL $16, x

// SELECT: x = (a & m) | (x &^ m); clobbers a.
#define SELECT(m, a, x) \
	PAND  m, a \
	MOVO  m, X15 \
	PANDN x, X15 \
	POR   a, X15 \
	MOVO  X15, x

// SATFIX: on overflow lanes (sign bit of ovf set), replace s by
// INT32_MAX or INT32_MIN chosen from the sign of a. Clobbers ovf and t.
#define SATFIX(a, s, ovf, t) \
	PSRAL $31, ovf \
	MOVO  a, t \
	PSRAL $31, t \
	PXOR  X14, t \
	SELECT(ovf, t, s)

// func nsqCand4SSE41(c *NSQCand4)
TEXT ·nsqCand4SSE41(SB), NOSPLIT, $0-8
	MOVQ  c+0(FP), SI
	MOVOU nsqMaxInt<>(SB), X14

	// Seed = silk_RAND(Seed); X13 = lanes whose new seed is negative.
	MOVOU  NSQCand4_Seed(SI), X0
	MOVOU  nsqRandMul<>(SB), X3
	PMULLD X3, X0
	MOVOU  nsqRandAdd<>(SB), X3
	PADDL  X3, X0
	MOVOU  X0, NSQCand4_Seed(SI)
	MOVO   X0, X13
	PSRAL  $31, X13

	// X2 = n_LF_Q14 = (SMULWB(Shape, LF lo) + SMULWT(LF_AR, LF hi)) << 2
	MOVOU      NSQCand4_Shape(SI), X1
	MOVQ       NSQCand4_LFLo(SI), X12
	PUNPCKLQDQ X12, X12
	SMULW4(X1, X12, X2, X3)
	MOVOU      NSQCand4_LFAR(SI), X1
	MOVQ       NSQCand4_LFHi(SI), X12
	PUNPCKLQDQ X12, X12
	SMULW4(X1, X12, X3, X4)
	PADDL      X3, X2
	PSLLL      $2, X2

	// X3 = tmp1 = ADD_SAT32(n_AR, n_LF); X1 = n_AR, X11 = LPC_pred.
	MOVOU NSQCand4_NAR(SI), X1
	MOVO  X1, X3
	PADDL X2, X3
	MOVO  X1, X4
	PXOR  X3, X4
	MOVO  X2, X5
	PXOR  X3, X5
	PAND  X5, X4
	SATFIX(X1, X3, X4, X5)

	// X4 = tmp2 = n_LTP + LPC_pred
	BCAST32(NSQCand4_NLTP(SI), X4)
	MOVOU NSQCand4_LPCPred(SI), X11
	PADDL X11, X4

	// X5 = SUB_SAT32(tmp2, tmp1)
	MOVO  X4, X5
	PSUBL X3, X5
	MOVO  X4, X6
	PXOR  X3, X6
	MOVO  X4, X7
	PXOR  X5, X7
	PAND  X7, X6
	SATFIX(X4, X5, X6, X7)

	// silk_RSHIFT_ROUND(tmp1, 4) = ((tmp1 >> 3) + 1) >> 1
	PCMPEQL X7, X7
	PSRLL   $31, X7
	PSRAL   $3, X5
	PADDL   X7, X5
	PSRAL   $1, X5

	// X6 = r_Q10 = x_Q10 - tmp1, negated where the seed is negative,
	// limited to [-(31 << 10), 30 << 10].
	BCAST32(NSQCand4_XQ10(SI), X6)
	PSUBL  X5, X6
	PXOR   X13, X6
	PSUBL  X13, X6
	MOVOU  nsqLimLo<>(SB), X7
	PMAXSD X7, X6
	MOVOU  nsqLimHi<>(SB), X7
	PMINSD X7, X6

	// X10 = offset, X7 = d = r - offset
	BCAST32(NSQCand4_Offset(SI), X10)
	MOVO  X6, X7
	PSUBL X10, X7

	// X5 = q1_Q0
	MOVL NSQCand4_Lambda(SI), AX
	CMPL AX, $2048
	JLE  plainq
	BCAST32(NSQCand4_Rdo(SI), X8)
	MOVO    X7, X3
	PSUBL   X8, X3
	PSRAL   $10, X3      // (d - rdo) >> 10
	MOVO    X7, X4
	PADDL   X8, X4
	PSRAL   $10, X4      // (d + rdo) >> 10
	PXOR    X5, X5
	PCMPGTL X7, X5       // d < 0: -1, else 0
	PXOR    X9, X9
	PSUBL   X8, X9
	PCMPGTL X7, X9       // d < -rdo
	SELECT(X9, X4, X5)
	MOVO    X7, X9
	PCMPGTL X8, X9       // d > rdo
	SELECT(X9, X3, X5)
	JMP     haveq

plainq:
	MOVO  X7, X5
	PSRAL $10, X5

haveq:
	// X8 = q > 0, X9 = q < 0, X4 = q < -1
	PXOR    X3, X3
	MOVO    X5, X8
	PCMPGTL X3, X8
	PXOR    X9, X9
	PCMPGTL X5, X9
	PCMPEQL X4, X4
	PCMPGTL X5, X4

	// X7 = q1 = (q << 10) + offset + (q > 0 ? -adj : q < 0 ? adj : 0)
	MOVOU nsqAdj<>(SB), X3
	MOVO  X3, X12
	PAND  X9, X12
	PXOR  X0, X0
	PSUBL X3, X0
	PAND  X8, X0
	POR   X12, X0
	MOVO  X5, X7
	PSLLL $10, X7
	PADDL X10, X7
	PADDL X0, X7

	// X0 = q2 = q1 + 1024 - (q == 0 || q == -1 ? adj : 0)
	MOVO  X8, X12
	POR   X4, X12
	PANDN X3, X12
	MOVO  X7, X0
	MOVOU nsqK1024<>(SB), X3
	PADDL X3, X0
	PSUBL X12, X0

	// X1 is n_AR (kept), X2 n_LF (kept). X10 = Lambda16.
	BCAST32(NSQCand4_Lambda16(SI), X10)

	// X12 = rd1 = (SMULBB(q < 0 ? -q1 : q1, lambda) + SMULBB(r-q1, r-q1)) >> 10
	MOVO   X7, X12
	PXOR   X9, X12
	PSUBL  X9, X12
	SEXT16(X12)
	PMULLD X10, X12
	MOVO   X6, X3
	PSUBL  X7, X3
	SEXT16(X3)
	PMULLD X3, X3
	PADDL  X3, X12
	PSRAL  $10, X12

	// X5 = rd2 = (SMULBB(q < -1 ? -q2 : q2, lambda) + SMULBB(r-q2, r-q2)) >> 10
	MOVO   X0, X5
	PXOR   X4, X5
	PSUBL  X4, X5
	SEXT16(X5)
	PMULLD X10, X5
	MOVO   X6, X3
	PSUBL  X0, X3
	SEXT16(X3)
	PMULLD X3, X3
	PADDL  X3, X5
	PSRAL  $10, X5

	// Rank: X8 = rd1 < rd2. Rank 0 takes (q1, rd1) there, else (q2, rd2);
	// rank 1 is the other: c1 = q1 ^ q2 ^ c0.
	MOVO    X5, X8
	PCMPGTL X12, X8
	MOVO    X7, X3   // X3 = q1 ^ q2
	PXOR    X0, X3
	MOVO    X7, X4
	SELECT(X8, X4, X0) // X0 = rank-0 q
	PXOR    X0, X3     // X3 = rank-1 q
	MOVO    X12, X9    // X9 = rd1 ^ rd2
	PXOR    X5, X9
	MOVO    X12, X4
	SELECT(X8, X4, X5) // X5 = rank-0 rd
	PXOR    X5, X9     // X9 = rank-1 rd
	MOVOU   NSQCand4_RD(SI), X4
	PADDL   X4, X5
	PADDL   X4, X9
	MOVOU   X0, NSQCand4_Q+0(SI)
	MOVOU   X3, NSQCand4_Q+16(SI)
	MOVOU   X5, NSQCand4_RDc+0(SI)
	MOVOU   X9, NSQCand4_RDc+16(SI)

	// Scalars for both ranks: X10 = LTP_pred, X12 = x_Q10 << 4.
	BCAST32(NSQCand4_LTPPred(SI), X10)
	BCAST32(NSQCand4_XQ10Sh(SI), X12)

#define CANDIDATE(q, off) \
	PSLLL $4, q \
	PXOR  X13, q \
	PSUBL X13, q \
	PADDL X10, q \
	MOVOU q, (NSQCand4_LPCExc+off)(SI) \
	PADDL X11, q \
	MOVOU q, (NSQCand4_XQ+off)(SI) \
	PSUBL X12, q \
	MOVOU q, (NSQCand4_Diff+off)(SI) \
	PSUBL X1, q \
	MOVOU q, (NSQCand4_LFOut+off)(SI) \
	MOVO  q, X5 \
	PSUBL X2, X5 \
	MOVO  q, X6 \
	PXOR  X2, X6 \
	MOVO  q, X7 \
	PXOR  X5, X7 \
	PAND  X7, X6 \
	SATFIX(q, X5, X6, X7) \
	MOVOU X5, (NSQCand4_LTPShp+off)(SI)

	CANDIDATE(X0, 0)
	CANDIDATE(X3, 16)
	RET
