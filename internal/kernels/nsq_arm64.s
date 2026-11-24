//go:build arm64 && go1.27 && !purego

// The S4 and S2 variants are written from one template; one state per
// int32 lane. silk_SMULWB is
// SMULL/SMULL2 (int32 x int32 -> int64) then SHRN #16, which keeps bits
// 16..47 of the product: the scalar (int64 product) >> 16 as int32. Adds,
// subtracts and shifts wrap in int32 like the scalar code. The S2 variants
// serve two states and leave lanes 2 and 3 untouched.

#include "go_asm.h"
#include "textflag.h"

// func warpedAR4NEON(w *WarpedAR4, diff, lfAR, out *[4]int32)
TEXT ·warpedAR4NEON(SB), NOSPLIT, $0-32
	MOVD w+0(FP), R0
	MOVD diff+8(FP), R1
	MOVD lfAR+16(FP), R2
	MOVD out+24(FP), R3
	MOVD WarpedAR4_Order(R0), R4
	ADD  $WarpedAR4_Warp, R0, R5
	VLD1R (R5), [V3.S4] // warp
	ADD  $WarpedAR4_Ar, R0, R6 // ar row 0

	// tmp2 = diff + SMULWB(s[0], warp)
	VLD1 (R1), [V1.S4]
	VLD1 (R0), [V5.S4]
	VSMULL  V3.S2, V5.S2, V16.D2
	VSMULL2 V3.S4, V5.S4, V17.D2
	VSHRN   $16, V16.D2, V6.S2
	VSHRN2  $16, V17.D2, V6.S4
	VADD V6.S4, V1.S4, V1.S4
	// tmp1 = s[0] + SMULWB(s[1] - tmp2, warp)
	ADD $16, R0, R7
	VLD1 (R7), [V6.S4]
	VSUB V1.S4, V6.S4, V6.S4
	VSMULL  V3.S2, V6.S2, V16.D2
	VSMULL2 V3.S4, V6.S4, V17.D2
	VSHRN   $16, V16.D2, V7.S2
	VSHRN2  $16, V17.D2, V7.S4
	VADD V7.S4, V5.S4, V0.S4
	// s[0] = tmp2; n = order>>1 + SMULWB(tmp2, ar[0])
	VST1 [V1.S4], (R0)
	LSR  $1, R4, R8
	VDUP R8, V2.S4
	VLD1R (R6), [V4.S4]
	VSMULL  V4.S2, V1.S2, V16.D2
	VSMULL2 V4.S4, V1.S4, V17.D2
	VSHRN   $16, V16.D2, V6.S2
	VSHRN2  $16, V17.D2, V6.S4
	VADD V6.S4, V2.S4, V2.S4
	MOVD $2, R9 // j

warpedAR4NEONloop:
	ADD  $1, R9, R10
	CMP  R4, R10
	BGE  warpedAR4NEONtail
	ADD  R9<<4, R0, R11  // s[j]
	SUB  $16, R11, R12   // s[j-1]
	ADD  R9<<4, R6, R13  // ar[j]
	SUB  $16, R13, R14   // ar[j-1]
	// tmp2' = s[j-1] + SMULWB(s[j] - tmp1, warp)
	VLD1 (R11), [V5.S4]
	VSUB V0.S4, V5.S4, V6.S4
	VSMULL  V3.S2, V6.S2, V16.D2
	VSMULL2 V3.S4, V6.S4, V17.D2
	VSHRN   $16, V16.D2, V7.S2
	VSHRN2  $16, V17.D2, V7.S4
	VLD1 (R12), [V6.S4]
	VADD V7.S4, V6.S4, V8.S4
	// s[j-1] = tmp1; n += SMULWB(tmp1, ar[j-1])
	VST1 [V0.S4], (R12)
	VLD1R (R14), [V4.S4]
	VSMULL  V4.S2, V0.S2, V16.D2
	VSMULL2 V4.S4, V0.S4, V17.D2
	VSHRN   $16, V16.D2, V6.S2
	VSHRN2  $16, V17.D2, V6.S4
	VADD V6.S4, V2.S4, V2.S4
	VORR V8.B16, V8.B16, V1.B16 // tmp2 = tmp2'
	// tmp1' = s[j] + SMULWB(s[j+1] - tmp2, warp)
	ADD $16, R11, R15
	VLD1 (R15), [V6.S4]
	VSUB V1.S4, V6.S4, V6.S4
	VSMULL  V3.S2, V6.S2, V16.D2
	VSMULL2 V3.S4, V6.S4, V17.D2
	VSHRN   $16, V16.D2, V7.S2
	VSHRN2  $16, V17.D2, V7.S4
	VADD V7.S4, V5.S4, V0.S4
	// s[j] = tmp2; n += SMULWB(tmp2, ar[j])
	VST1 [V1.S4], (R11)
	VLD1R (R13), [V4.S4]
	VSMULL  V4.S2, V1.S2, V16.D2
	VSMULL2 V4.S4, V1.S4, V17.D2
	VSHRN   $16, V16.D2, V6.S2
	VSHRN2  $16, V17.D2, V6.S4
	VADD V6.S4, V2.S4, V2.S4
	ADD $2, R9
	B   warpedAR4NEONloop

warpedAR4NEONtail:
	SUB $1, R4, R10
	ADD R10<<4, R0, R11
	ADD R10<<4, R6, R13
	VST1 [V0.S4], (R11)
	VLD1R (R13), [V4.S4]
	VSMULL  V4.S2, V0.S2, V16.D2
	VSMULL2 V4.S4, V0.S4, V17.D2
	VSHRN   $16, V16.D2, V6.S2
	VSHRN2  $16, V17.D2, V6.S4
	VADD V6.S4, V2.S4, V2.S4
	// n = ((n << 1) + SMULWB(lfAR, tilt)) << 2
	VSHL $1, V2.S4, V2.S4
	ADD $WarpedAR4_Tilt, R0, R5
	VLD1R (R5), [V4.S4]
	VLD1 (R2), [V5.S4]
	VSMULL  V4.S2, V5.S2, V16.D2
	VSMULL2 V4.S4, V5.S4, V17.D2
	VSHRN   $16, V16.D2, V6.S2
	VSHRN2  $16, V17.D2, V6.S4
	VADD V6.S4, V2.S4, V2.S4
	VSHL $2, V2.S4, V2.S4
	VST1 [V2.S4], (R3)
	RET

// func warpedAR2NEON(w *WarpedAR4, diff, lfAR, out *[4]int32)
TEXT ·warpedAR2NEON(SB), NOSPLIT, $0-32
	MOVD w+0(FP), R0
	MOVD diff+8(FP), R1
	MOVD lfAR+16(FP), R2
	MOVD out+24(FP), R3
	MOVD WarpedAR4_Order(R0), R4
	ADD  $WarpedAR4_Warp, R0, R5
	VLD1R (R5), [V3.S4] // warp
	ADD  $WarpedAR4_Ar, R0, R6 // ar row 0

	// tmp2 = diff + SMULWB(s[0], warp)
	VLD1 (R1), [V1.S2]
	VLD1 (R0), [V5.S2]
	VSMULL  V3.S2, V5.S2, V16.D2
	VSHRN   $16, V16.D2, V6.S2
	VADD V6.S2, V1.S2, V1.S2
	// tmp1 = s[0] + SMULWB(s[1] - tmp2, warp)
	ADD $16, R0, R7
	VLD1 (R7), [V6.S2]
	VSUB V1.S2, V6.S2, V6.S2
	VSMULL  V3.S2, V6.S2, V16.D2
	VSHRN   $16, V16.D2, V7.S2
	VADD V7.S2, V5.S2, V0.S2
	// s[0] = tmp2; n = order>>1 + SMULWB(tmp2, ar[0])
	VST1 [V1.S2], (R0)
	LSR  $1, R4, R8
	VDUP R8, V2.S2
	VLD1R (R6), [V4.S4]
	VSMULL  V4.S2, V1.S2, V16.D2
	VSHRN   $16, V16.D2, V6.S2
	VADD V6.S2, V2.S2, V2.S2
	MOVD $2, R9 // j

warpedAR2NEONloop:
	ADD  $1, R9, R10
	CMP  R4, R10
	BGE  warpedAR2NEONtail
	ADD  R9<<4, R0, R11  // s[j]
	SUB  $16, R11, R12   // s[j-1]
	ADD  R9<<4, R6, R13  // ar[j]
	SUB  $16, R13, R14   // ar[j-1]
	// tmp2' = s[j-1] + SMULWB(s[j] - tmp1, warp)
	VLD1 (R11), [V5.S2]
	VSUB V0.S2, V5.S2, V6.S2
	VSMULL  V3.S2, V6.S2, V16.D2
	VSHRN   $16, V16.D2, V7.S2
	VLD1 (R12), [V6.S2]
	VADD V7.S2, V6.S2, V8.S2
	// s[j-1] = tmp1; n += SMULWB(tmp1, ar[j-1])
	VST1 [V0.S2], (R12)
	VLD1R (R14), [V4.S4]
	VSMULL  V4.S2, V0.S2, V16.D2
	VSHRN   $16, V16.D2, V6.S2
	VADD V6.S2, V2.S2, V2.S2
	VORR V8.B16, V8.B16, V1.B16 // tmp2 = tmp2'
	// tmp1' = s[j] + SMULWB(s[j+1] - tmp2, warp)
	ADD $16, R11, R15
	VLD1 (R15), [V6.S2]
	VSUB V1.S2, V6.S2, V6.S2
	VSMULL  V3.S2, V6.S2, V16.D2
	VSHRN   $16, V16.D2, V7.S2
	VADD V7.S2, V5.S2, V0.S2
	// s[j] = tmp2; n += SMULWB(tmp2, ar[j])
	VST1 [V1.S2], (R11)
	VLD1R (R13), [V4.S4]
	VSMULL  V4.S2, V1.S2, V16.D2
	VSHRN   $16, V16.D2, V6.S2
	VADD V6.S2, V2.S2, V2.S2
	ADD $2, R9
	B   warpedAR2NEONloop

warpedAR2NEONtail:
	SUB $1, R4, R10
	ADD R10<<4, R0, R11
	ADD R10<<4, R6, R13
	VST1 [V0.S2], (R11)
	VLD1R (R13), [V4.S4]
	VSMULL  V4.S2, V0.S2, V16.D2
	VSHRN   $16, V16.D2, V6.S2
	VADD V6.S2, V2.S2, V2.S2
	// n = ((n << 1) + SMULWB(lfAR, tilt)) << 2
	VSHL $1, V2.S2, V2.S2
	ADD $WarpedAR4_Tilt, R0, R5
	VLD1R (R5), [V4.S4]
	VLD1 (R2), [V5.S2]
	VSMULL  V4.S2, V5.S2, V16.D2
	VSHRN   $16, V16.D2, V6.S2
	VADD V6.S2, V2.S2, V2.S2
	VSHL $2, V2.S2, V2.S2
	VST1 [V2.S2], (R3)
	RET

// func lpcPred4NEON(row *[4]int32, a *[16][2]int64, order int, out *[4]int32)
TEXT ·lpcPred4NEON(SB), NOSPLIT, $0-32
	MOVD row+0(FP), R0
	MOVD a+8(FP), R1
	MOVD order+16(FP), R2
	MOVD out+24(FP), R3
	LSR  $1, R2, R4
	VDUP R4, V2.S4

lpcPred4NEONloop:
	VLD1 (R0), [V5.S4]
	VLD1R (R1), [V4.S4]
	VSMULL  V4.S2, V5.S2, V16.D2
	VSMULL2 V4.S4, V5.S4, V17.D2
	VSHRN   $16, V16.D2, V6.S2
	VSHRN2  $16, V17.D2, V6.S4
	VADD V6.S4, V2.S4, V2.S4
	SUB  $16, R0
	ADD  $16, R1
	SUBS $1, R2
	BNE  lpcPred4NEONloop
	VSHL $4, V2.S4, V2.S4
	VST1 [V2.S4], (R3)
	RET

// func lpcPred2NEON(row *[4]int32, a *[16][2]int64, order int, out *[4]int32)
TEXT ·lpcPred2NEON(SB), NOSPLIT, $0-32
	MOVD row+0(FP), R0
	MOVD a+8(FP), R1
	MOVD order+16(FP), R2
	MOVD out+24(FP), R3
	LSR  $1, R2, R4
	VDUP R4, V2.S2

lpcPred2NEONloop:
	VLD1 (R0), [V5.S2]
	VLD1R (R1), [V4.S4]
	VSMULL  V4.S2, V5.S2, V16.D2
	VSHRN   $16, V16.D2, V6.S2
	VADD V6.S2, V2.S2, V2.S2
	SUB  $16, R0
	ADD  $16, R1
	SUBS $1, R2
	BNE  lpcPred2NEONloop
	VSHL $4, V2.S2, V2.S2
	VST1 [V2.S2], (R3)
	RET

