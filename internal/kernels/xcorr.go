package kernels

// PitchXcorr32 is celt_pitch_xcorr_c in float32:
//
//	xcorr[i] = Σ_{j<length} x[j]*y[i+j]   for i < maxPitch,
//
// each lag summed sequentially in j order with every product rounded to
// float32 before it is added. len(x) must be at least length,
// len(y) at least length+maxPitch-1 and len(xcorr) at least maxPitch.
//
// The accelerated versions give each lag its own SIMD lane, so every lag
// still adds the same float32 products in the same order and the result is
// bit-identical to PitchXcorr32Generic.
func PitchXcorr32(x, y, xcorr []float32, length, maxPitch int) {
	if maxPitch <= 0 {
		return
	}
	// Bounds are checked once here so the assembly can run unchecked.
	_ = x[:length]
	_ = y[:length+maxPitch-1]
	_ = xcorr[:maxPitch]
	done := pitchXcorr32Arch(x, y, xcorr, length, maxPitch)
	pitchXcorr32Lags(x, y, xcorr, length, done, maxPitch)
}

// PitchXcorr32Generic is the pure Go reference for PitchXcorr32.
func PitchXcorr32Generic(x, y, xcorr []float32, length, maxPitch int) {
	if maxPitch <= 0 {
		return
	}
	pitchXcorr32Lags(x, y, xcorr, length, 0, maxPitch)
}

// pitchXcorr32Lags computes lags [from, to) in pure Go. Like libopus's
// xcorr_kernel it computes four lags per pass; each lag still sums its
// products in j order.
func pitchXcorr32Lags(x, y, xcorr []float32, length, from, to int) {
	x = x[:length]
	i := from
	for ; i+3 < to; i += 4 {
		y0 := y[i : i+length]
		y1 := y[i+1 : i+1+length]
		y2 := y[i+2 : i+2+length]
		y3 := y[i+3 : i+3+length]
		var s0, s1, s2, s3 float32
		for j, xj := range x {
			s0 += float32(xj * y0[j])
			s1 += float32(xj * y1[j])
			s2 += float32(xj * y2[j])
			s3 += float32(xj * y3[j])
		}
		xcorr[i], xcorr[i+1], xcorr[i+2], xcorr[i+3] = s0, s1, s2, s3
	}
	for ; i < to; i++ {
		yi := y[i : i+length]
		var sum float32
		for j, xj := range x {
			sum += float32(xj * yi[j])
		}
		xcorr[i] = sum
	}
}
