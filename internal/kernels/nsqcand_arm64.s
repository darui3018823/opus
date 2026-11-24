//go:build arm64 && go1.27 && !purego

#include "go_asm.h"
#include "textflag.h"

// The four states of NSQCand4 in the four int32 lanes, as in
// nsqcand_amd64.s: branches become lane masks and VBSL selects. NEON has
// the saturating add/subtract (SQADD/SQSUB) and the rounding shift
// (SRSHR, computed without intermediate overflow) that the SSE version
// builds from sign tests.

// SMULW4(b, c, out): out = (int64(b) * c) >> 16, c an int32 broadcast.
#define SMULW4(b, c, out) \
	VSMULL  c.S2, b.S2, V30.D2 \
	VSMULL2 c.S4, b.S4, V31.D2 \
	VSHRN   $16, V30.D2, out.S2 \
	VSHRN2  $16, V31.D2, out.S4

// BCAST(field, v): broadcast an int32 field of the struct (R0).
#define BCAST(field, v) \
	MOVW field(R0), R5 \
	VDUP R5, v.S4

// SEXT16: sign-extend the low 16 bits of each lane (as silk_SMULBB reads).
#define SEXT16(v) \
	VSHL  $16, v.S4, v.S4 \
	VSSHR $16, v.S4, v.S4

// func nsqCand4NEON(c *NSQCand4)
TEXT ·nsqCand4NEON(SB), NOSPLIT, $0-8
	MOVD c+0(FP), R0

	// Seed = silk_RAND(Seed); V13 = lanes whose new seed is negative.
	ADD  $NSQCand4_Seed, R0, R1
	VLD1 (R1), [V0.S4]
	MOVW $196314165, R5
	VDUP R5, V1.S4
	VMUL V1.S4, V0.S4, V0.S4
	MOVW $907633515, R5
	VDUP R5, V1.S4
	VADD V1.S4, V0.S4, V0.S4
	VST1 [V0.S4], (R1)
	VSSHR $31, V0.S4, V13.S4

	// V2 = n_LF = (SMULWB(Shape, LF lo) + SMULWT(LF_AR, LF hi)) << 2
	MOVW NSQCand4_LFLo(R0), R5
	VDUP R5, V20.S4
	MOVW NSQCand4_LFHi(R0), R5
	VDUP R5, V21.S4
	ADD  $NSQCand4_Shape, R0, R1
	VLD1 (R1), [V1.S4]
	SMULW4(V1, V20, V2)
	ADD  $NSQCand4_LFAR, R0, R1
	VLD1 (R1), [V1.S4]
	SMULW4(V1, V21, V3)
	VADD V3.S4, V2.S4, V2.S4
	VSHL $2, V2.S4, V2.S4

	// V1 = n_AR, V11 = LPC_pred; V3 = SUB_SAT(n_LTP + LPC_pred, ADD_SAT(n_AR, n_LF))
	ADD    $NSQCand4_NAR, R0, R1
	VLD1   (R1), [V1.S4]
	ADD    $NSQCand4_LPCPred, R0, R1
	VLD1   (R1), [V11.S4]
	VSQADD V2.S4, V1.S4, V3.S4
	BCAST(NSQCand4_NLTP, V4)
	VADD   V11.S4, V4.S4, V4.S4
	VSQSUB V3.S4, V4.S4, V3.S4
	VSRSHR $4, V3.S4, V3.S4 // silk_RSHIFT_ROUND(tmp1, 4)

	// V6 = r = x_Q10 - tmp1, negated where the seed is negative, limited.
	BCAST(NSQCand4_XQ10, V6)
	VSUB V3.S4, V6.S4, V6.S4
	VEOR V13.B16, V6.B16, V6.B16
	VSUB V13.S4, V6.S4, V6.S4
	MOVW $-31744, R5
	VDUP R5, V4.S4
	VSMAX V4.S4, V6.S4, V6.S4
	MOVW $30720, R5
	VDUP R5, V4.S4
	VSMIN V4.S4, V6.S4, V6.S4

	// V10 = offset, V7 = d = r - offset, V5 = q1_Q0
	BCAST(NSQCand4_Offset, V10)
	VSUB V10.S4, V6.S4, V7.S4
	MOVW NSQCand4_Lambda(R0), R5
	CMPW $2048, R5
	BLE  plainq
	BCAST(NSQCand4_Rdo, V8)
	VSUB  V8.S4, V7.S4, V3.S4
	VSSHR $10, V3.S4, V3.S4 // (d - rdo) >> 10
	VADD  V8.S4, V7.S4, V4.S4
	VSSHR $10, V4.S4, V4.S4 // (d + rdo) >> 10
	VSSHR $31, V7.S4, V5.S4 // d < 0: -1, else 0
	VNEG  V8.S4, V9.S4
	VCMGT V7.S4, V9.S4, V9.S4 // -rdo > d
	VBSL  V5.B16, V4.B16, V9.B16
	VORR  V9.B16, V9.B16, V5.B16
	VCMGT V8.S4, V7.S4, V9.S4 // d > rdo
	VBSL  V5.B16, V3.B16, V9.B16
	VORR  V9.B16, V9.B16, V5.B16
	B     haveq

plainq:
	VSSHR $10, V7.S4, V5.S4

haveq:
	// V8 = q > 0, V9 = q < 0, V12 = q < -1
	VCMGT $0, V5.S4, V8.S4
	VSSHR $31, V5.S4, V9.S4
	VEOR  V14.B16, V14.B16, V14.B16
	VNOT  V14.B16, V14.B16 // -1
	VCMGT V5.S4, V14.S4, V12.S4

	// V7 = q1 = (q << 10) + offset + (q > 0 ? -80 : q < 0 ? 80 : 0)
	MOVW $80, R5
	VDUP R5, V3.S4
	VAND V9.B16, V3.B16, V4.B16
	VNEG V3.S4, V14.S4
	VAND V8.B16, V14.B16, V14.B16
	VORR V14.B16, V4.B16, V4.B16
	VSHL $10, V5.S4, V7.S4
	VADD V10.S4, V7.S4, V7.S4
	VADD V4.S4, V7.S4, V7.S4

	// V0 = q2 = q1 + 1024 - (q == 0 || q == -1 ? 80 : 0)
	VORR V12.B16, V8.B16, V4.B16
	VBIC V4.B16, V3.B16, V4.B16 // 80 &^ (pos | negBig)
	MOVW $1024, R5
	VDUP R5, V14.S4
	VADD V14.S4, V7.S4, V0.S4
	VSUB V4.S4, V0.S4, V0.S4

	BCAST(NSQCand4_Lambda16, V10)

	// V15 = rd1 = (SMULBB(q < 0 ? -q1 : q1, lambda) + SMULBB(r-q1, r-q1)) >> 10
	VEOR  V9.B16, V7.B16, V15.B16
	VSUB  V9.S4, V15.S4, V15.S4
	SEXT16(V15)
	VMUL  V10.S4, V15.S4, V15.S4
	VSUB  V7.S4, V6.S4, V3.S4
	SEXT16(V3)
	VMUL  V3.S4, V3.S4, V3.S4
	VADD  V3.S4, V15.S4, V15.S4
	VSSHR $10, V15.S4, V15.S4

	// V5 = rd2 = (SMULBB(q < -1 ? -q2 : q2, lambda) + SMULBB(r-q2, r-q2)) >> 10
	VEOR  V12.B16, V0.B16, V5.B16
	VSUB  V12.S4, V5.S4, V5.S4
	SEXT16(V5)
	VMUL  V10.S4, V5.S4, V5.S4
	VSUB  V0.S4, V6.S4, V3.S4
	SEXT16(V3)
	VMUL  V3.S4, V3.S4, V3.S4
	VADD  V3.S4, V5.S4, V5.S4
	VSSHR $10, V5.S4, V5.S4

	// Rank: V8 = rd1 < rd2. Rank 0 takes (q1, rd1) there, else (q2, rd2);
	// rank 1 is the other: c1 = q1 ^ q2 ^ c0.
	VCMGT V15.S4, V5.S4, V8.S4
	VEOR  V0.B16, V7.B16, V3.B16 // q1 ^ q2
	VORR  V8.B16, V8.B16, V9.B16
	VBSL  V0.B16, V7.B16, V9.B16 // rank-0 q
	VEOR  V9.B16, V3.B16, V3.B16 // rank-1 q
	VORR  V9.B16, V9.B16, V0.B16
	VEOR  V5.B16, V15.B16, V4.B16 // rd1 ^ rd2
	VORR  V8.B16, V8.B16, V9.B16
	VBSL  V5.B16, V15.B16, V9.B16 // rank-0 rd
	VEOR  V9.B16, V4.B16, V4.B16  // rank-1 rd
	ADD   $NSQCand4_RD, R0, R1
	VLD1  (R1), [V5.S4]
	VADD  V5.S4, V9.S4, V9.S4
	VADD  V5.S4, V4.S4, V4.S4
	ADD   $NSQCand4_Q, R0, R1
	VST1  [V0.S4], (R1)
	ADD   $(NSQCand4_Q+16), R0, R1
	VST1  [V3.S4], (R1)
	ADD   $NSQCand4_RDc, R0, R1
	VST1  [V9.S4], (R1)
	ADD   $(NSQCand4_RDc+16), R0, R1
	VST1  [V4.S4], (R1)

	BCAST(NSQCand4_LTPPred, V10)
	BCAST(NSQCand4_XQ10Sh, V12)

// CANDIDATE(q, off): the candidate's LPC_exc, xq, Diff, LF_AR, sLTP_shp.
#define CANDIDATE(q, off) \
	VSHL   $4, q.S4, q.S4 \
	VEOR   V13.B16, q.B16, q.B16 \
	VSUB   V13.S4, q.S4, q.S4 \
	VADD   V10.S4, q.S4, q.S4 \
	ADD    $(NSQCand4_LPCExc+off), R0, R1 \
	VST1   [q.S4], (R1) \
	VADD   V11.S4, q.S4, q.S4 \
	ADD    $(NSQCand4_XQ+off), R0, R1 \
	VST1   [q.S4], (R1) \
	VSUB   V12.S4, q.S4, q.S4 \
	ADD    $(NSQCand4_Diff+off), R0, R1 \
	VST1   [q.S4], (R1) \
	VSUB   V1.S4, q.S4, q.S4 \
	ADD    $(NSQCand4_LFOut+off), R0, R1 \
	VST1   [q.S4], (R1) \
	VSQSUB V2.S4, q.S4, V5.S4 \
	ADD    $(NSQCand4_LTPShp+off), R0, R1 \
	VST1   [V5.S4], (R1)

	CANDIDATE(V0, 0)
	CANDIDATE(V3, 16)
	RET
