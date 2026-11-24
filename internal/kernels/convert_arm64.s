//go:build arm64 && go1.27 && !purego

#include "textflag.h"

// FCVTN rounds like float32(x) under the default FPCR; FCVTL is exact.

// func f64to32NEON(dst *float32, src *float64, blocks int)
TEXT ·f64to32NEON(SB), NOSPLIT, $0-24
	MOVD dst+0(FP), R0
	MOVD src+8(FP), R1
	MOVD blocks+16(FP), R2

c64loop:
	VLD1.P  32(R1), [V0.D2, V1.D2]
	VFCVTN  V0.D2, V2.S2
	VFCVTN2 V1.D2, V2.S4
	VST1.P  [V2.S4], 16(R0)
	SUBS    $1, R2
	BNE     c64loop
	RET

// func f32to64NEON(dst *float64, src *float32, blocks int)
TEXT ·f32to64NEON(SB), NOSPLIT, $0-24
	MOVD dst+0(FP), R0
	MOVD src+8(FP), R1
	MOVD blocks+16(FP), R2

c32loop:
	VLD1.P  16(R1), [V0.S4]
	VFCVTL  V0.S2, V1.D2
	VFCVTL2 V0.S4, V2.D2
	VST1.P  [V1.D2, V2.D2], 32(R0)
	SUBS    $1, R2
	BNE     c32loop
	RET

// func float32ToInt16NEON(dst *int16, src *float32, blocks int)
// Eight samples per pass: scale, clamp with compare masks so NaN goes to
// -32768 as in the scalar !(x > -32768) test (FMAX/FMIN would keep NaN),
// round half to even with FRINTN, convert and narrow.
TEXT ·float32ToInt16NEON(SB), NOSPLIT, $0-24
	MOVD dst+0(FP), R0
	MOVD src+8(FP), R1
	MOVD blocks+16(FP), R2
	MOVW $0x47000000, R4 // 32768
	VDUP R4, V20.S4
	MOVW $0xc7000000, R4 // -32768
	VDUP R4, V21.S4
	MOVW $0x46fffe00, R4 // 32767
	VDUP R4, V22.S4

pcmloop:
	VLD1.P  32(R1), [V0.S4, V1.S4]
	VFMUL   V20.S4, V0.S4, V0.S4
	VFMUL   V20.S4, V1.S4, V1.S4
	VFCMGT  V21.S4, V0.S4, V2.S4 // x > -32768
	VFCMGT  V21.S4, V1.S4, V3.S4
	VBSL    V21.B16, V0.B16, V2.B16
	VBSL    V21.B16, V1.B16, V3.B16
	VFCMGT  V2.S4, V22.S4, V0.S4 // 32767 > x
	VFCMGT  V3.S4, V22.S4, V1.S4
	VBSL    V22.B16, V2.B16, V0.B16
	VBSL    V22.B16, V3.B16, V1.B16
	VFRINTN V0.S4, V0.S4
	VFRINTN V1.S4, V1.S4
	VFCVTZS V0.S4, V0.S4
	VFCVTZS V1.S4, V1.S4
	VXTN    V0.S4, V2.H4
	VXTN2   V1.S4, V2.H8
	VST1.P  [V2.H8], 16(R0)
	SUBS    $1, R2
	BNE     pcmloop
	RET
