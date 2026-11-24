//go:build arm64 && go1.27 && !purego

#include "textflag.h"

// One lag per lane: broadcast x[j], multiply by the contiguous y[k+j] and
// add, with separate FMUL and FADD (never a fused multiply-add), so each
// lag sums the same rounded products in the same order as the scalar loop.

// func xcorr16NEON(x, y *float32, n int, out *float32)
TEXT ·xcorr16NEON(SB), NOSPLIT, $0-32
	MOVD x+0(FP), R0
	MOVD y+8(FP), R1
	MOVD n+16(FP), R2
	MOVD out+24(FP), R3
	VEOR V0.B16, V0.B16, V0.B16
	VEOR V1.B16, V1.B16, V1.B16
	VEOR V2.B16, V2.B16, V2.B16
	VEOR V3.B16, V3.B16, V3.B16
	CBZ  R2, x16done

x16loop:
	VLD1R.P 4(R0), [V4.S4]
	VLD1    (R1), [V5.S4, V6.S4, V7.S4, V8.S4]
	ADD     $4, R1
	VFMUL   V4.S4, V5.S4, V5.S4
	VFMUL   V4.S4, V6.S4, V6.S4
	VFMUL   V4.S4, V7.S4, V7.S4
	VFMUL   V4.S4, V8.S4, V8.S4
	VFADD   V5.S4, V0.S4, V0.S4
	VFADD   V6.S4, V1.S4, V1.S4
	VFADD   V7.S4, V2.S4, V2.S4
	VFADD   V8.S4, V3.S4, V3.S4
	SUBS    $1, R2
	BNE     x16loop

x16done:
	VST1 [V0.S4, V1.S4, V2.S4, V3.S4], (R3)
	RET

// func xcorr4NEON(x, y *float32, n int, out *float32)
TEXT ·xcorr4NEON(SB), NOSPLIT, $0-32
	MOVD x+0(FP), R0
	MOVD y+8(FP), R1
	MOVD n+16(FP), R2
	MOVD out+24(FP), R3
	VEOR V0.B16, V0.B16, V0.B16
	CBZ  R2, x4done

x4loop:
	VLD1R.P 4(R0), [V4.S4]
	VLD1    (R1), [V5.S4]
	ADD     $4, R1
	VFMUL   V4.S4, V5.S4, V5.S4
	VFADD   V5.S4, V0.S4, V0.S4
	SUBS    $1, R2
	BNE     x4loop

x4done:
	VST1 [V0.S4], (R3)
	RET
