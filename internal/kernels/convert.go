package kernels

// Float64sToFloat32s sets dst[i] = float32(src[i]) for i < len(src).
func Float64sToFloat32s(dst []float32, src []float64) {
	dst = dst[:len(src)]
	done := f64to32Arch(dst, src)
	for i := done; i < len(src); i++ {
		dst[i] = float32(src[i])
	}
}

// Float32sToFloat64s sets dst[i] = float64(src[i]) for i < len(src).
func Float32sToFloat64s(dst []float64, src []float32) {
	dst = dst[:len(src)]
	done := f32to64Arch(dst, src)
	for i := done; i < len(src); i++ {
		dst[i] = float64(src[i])
	}
}

// HasComb reports whether the comb filter and the conversions around it
// have vector versions on this CPU.
func HasComb() bool { return hasFFT() }
