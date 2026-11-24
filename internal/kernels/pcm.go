package kernels

import "math"

// Float32ToInt16 converts float samples to int16 as libopus FLOAT2INT16
// does: scale by 32768, clamp to [-32768, 32767] (NaN clamps to -32768) and
// round half to even. len(dst) must be at least len(src).
func Float32ToInt16(dst []int16, src []float32) {
	dst = dst[:len(src)]
	done := float32ToInt16Arch(dst, src)
	Float32ToInt16Generic(dst[done:], src[done:])
}

// Float32ToInt16Generic is the pure Go reference for Float32ToInt16.
func Float32ToInt16Generic(dst []int16, src []float32) {
	dst = dst[:len(src)]
	for i, v := range src {
		scaled := v * float32(32768)
		if !(scaled > -32768) {
			scaled = -32768
		}
		if !(scaled < 32767) {
			scaled = 32767
		}
		dst[i] = int16(math.RoundToEven(float64(scaled)))
	}
}
