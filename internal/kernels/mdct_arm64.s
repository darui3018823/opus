//go:build arm64 && go1.27 && !purego

#include "textflag.h"

// Four rotations per pass, as in mdct_amd64.s. VLD2 splits float64 pairs
// into even and odd vectors; FCVTN rounds like float32(x), FCVTL widens
// exactly; REV64 followed by EXT #8 reverses four lanes.

#define REV4(v) \
	VREV64 v.S4, v.S4 \
	VEXT   $8, v.B16, v.B16, v.B16

// func imdctPreNEON(fout *float32, x *float64, trig *float32, bitrev *int, n4, blocks int)
TEXT ·imdctPreNEON(SB), NOSPLIT, $0-48
	MOVD fout+0(FP), R0
	MOVD x+8(FP), R1
	MOVD trig+16(FP), R2
	MOVD bitrev+24(FP), R3
	MOVD n4+32(FP), R4
	MOVD blocks+40(FP), R5
	ADD  R4<<4, R1, R6 // x[2n4]
	SUB  $64, R6       // backward window x[2n4-8-2i]
	ADD  R4<<2, R2, R7 // trig[n4+i]

preloop:
	VLD2.P  32(R1), [V0.D2, V1.D2] // x[2i], x[2i+2] | odd
	VLD2.P  32(R1), [V2.D2, V3.D2] // x[2i+4], x[2i+6] | odd
	VFCVTN  V0.D2, V4.S2
	VFCVTN2 V2.D2, V4.S4           // xp1
	VLD2    (R6), [V0.D2, V1.D2]   // odd slots y1, y3
	ADD     $32, R6, R8
	VLD2    (R8), [V2.D2, V3.D2]   // odd slots y5, y7
	VFCVTN  V1.D2, V5.S2
	VFCVTN2 V3.D2, V5.S4           // y1, y3, y5, y7
	REV4(V5)                       // xp2
	SUB     $64, R6

	VLD1.P 16(R2), [V6.S4] // t0
	VLD1.P 16(R7), [V7.S4] // t1
	VFMUL  V6.S4, V5.S4, V8.S4  // xp2*t0
	VFMUL  V7.S4, V4.S4, V9.S4  // xp1*t1
	VFADD  V9.S4, V8.S4, V10.S4 // yr
	VFMUL  V6.S4, V4.S4, V8.S4  // xp1*t0
	VFMUL  V7.S4, V5.S4, V9.S4  // xp2*t1
	VFSUB  V9.S4, V8.S4, V11.S4 // yi

	VZIP1 V10.S4, V11.S4, V12.S4 // (yi0, yr0, yi1, yr1)
	VZIP2 V10.S4, V11.S4, V13.S4
	MOVD.P 8(R3), R8
	ADD    R8<<3, R0, R8
	VST1   V12.D[0], (R8)
	MOVD.P 8(R3), R8
	ADD    R8<<3, R0, R8
	VST1   V12.D[1], (R8)
	MOVD.P 8(R3), R8
	ADD    R8<<3, R0, R8
	VST1   V13.D[0], (R8)
	MOVD.P 8(R3), R8
	ADD    R8<<3, R0, R8
	VST1   V13.D[1], (R8)

	SUBS $1, R5
	BNE  preloop
	RET

// STORE2D writes the four lanes of a and b interleaved as float64:
// a0, b0, a1, b1, a2, b2, a3, b3.
#define STORE2D(a, b, ptr) \
	VFCVTL  a.S2, V20.D2 \
	VFCVTL  b.S2, V21.D2 \
	VST2.P  [V20.D2, V21.D2], 32(ptr) \
	VFCVTL2 a.S4, V20.D2 \
	VFCVTL2 b.S4, V21.D2 \
	VST2.P  [V20.D2, V21.D2], 32(ptr)

// func imdctPostNEON(buf *float64, fout, trig *float32, n4, blocks int)
TEXT ·imdctPostNEON(SB), NOSPLIT, $0-40
	MOVD buf+0(FP), R0
	MOVD fout+8(FP), R1
	MOVD trig+16(FP), R2
	MOVD n4+24(FP), R4
	MOVD blocks+32(FP), R5
	ADD  R4<<3, R1, R6
	SUB  $32, R6       // fout[n4-4-i]
	ADD  R4<<2, R2, R7 // trig[n4+i]
	SUB  $16, R7, R9   // trig[n4-4-i]
	ADD  R4<<3, R2, R10
	SUB  $16, R10      // trig[2n4-4-i]
	ADD  R4<<4, R0, R11
	SUB  $64, R11      // buf[2n4-8-2i]

postloop:
	// Forward: re = .i, im = .r
	VLD2.P 32(R1), [V0.S4, V1.S4]
	VLD1.P 16(R2), [V2.S4] // t0
	VLD1.P 16(R7), [V3.S4] // t1
	VFMUL  V2.S4, V1.S4, V4.S4
	VFMUL  V3.S4, V0.S4, V5.S4
	VFADD  V5.S4, V4.S4, V6.S4 // yr = re*t0 + im*t1
	VFMUL  V3.S4, V1.S4, V4.S4
	VFMUL  V2.S4, V0.S4, V5.S4
	VFSUB  V5.S4, V4.S4, V7.S4 // yi = re*t1 - im*t0

	// Backward, reversed into lanes i..i+3
	VLD2   (R6), [V8.S4, V9.S4]
	REV4(V8)
	REV4(V9)
	VLD1   (R9), [V10.S4]
	REV4(V10) // t0'
	VLD1   (R10), [V11.S4]
	REV4(V11) // t1'
	VFMUL  V10.S4, V9.S4, V12.S4
	VFMUL  V11.S4, V8.S4, V13.S4
	VFADD  V13.S4, V12.S4, V14.S4 // yr2 = re*t0' + im*t1'
	VFMUL  V11.S4, V9.S4, V12.S4
	VFMUL  V10.S4, V8.S4, V13.S4
	VFSUB  V13.S4, V12.S4, V15.S4 // yi2 = re*t1' - im*t0'

	// buf[2i..] = yr0, yi2_0, yr1, yi2_1, ...
	STORE2D(V6, V15, R0)
	// buf[2n4-8-2i..] = yr2_3, yi_3, ..., yr2_0, yi_0
	REV4(V14)
	REV4(V7)
	MOVD R11, R12
	STORE2D(V14, V7, R12)

	SUB  $32, R6
	SUB  $16, R9
	SUB  $16, R10
	SUB  $64, R11
	SUBS $1, R5
	BNE  postloop
	RET
