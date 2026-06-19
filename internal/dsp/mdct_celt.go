package dsp

import "math"

// CELTMode holds parameters for the CELT MDCT with small-overlap model.
type CELTMode struct {
	N       int       // frame size (e.g. 960)
	Overlap int       // overlap size (e.g. 120)
	Window  []float64 // synthesis window of length Overlap (rising ramp, 0→1)
}

// NewCELTMode creates a CELT mode.  window is the overlap-region window (length = overlap).
func NewCELTMode(N, overlap int, window []float32) *CELTMode {
	w := make([]float64, overlap)
	for i, v := range window {
		w[i] = float64(v)
	}
	return &CELTMode{N: N, Overlap: overlap, Window: w}
}

// IMDCT computes the N-point CELT inverse MDCT and returns N raw (unwindowed)
// time-domain samples.  Windowing is deferred to InverseOverlapAdd.
//
// Formula (RFC 6716 §5.5.2):
//
//	Y[n] = (2/N) * sum_{k=0}^{N-1} X[k] * cos(π/N * (k+1/2) * (n + N/2 + 1/2))
//	     = (2/N) * Re( exp(iπ(2n+N+1)/(4N)) * DFT_{2N}(a')[n] )
//	where a'[k] = X[k] * exp(iπk(N+1)/(2N))  (zero-padded to 2N)
func (m *CELTMode) IMDCT(X []float64) []float64 {
	N := m.N

	// Pre-multiply X[k] by exp(iπk/(2N)) so the half-bin term in
	// (k+1/2)*(n+N/2+1/2) can be represented by an integer DFT bin.
	a := make([]Complex, 2*N)
	for k := 0; k < N; k++ {
		ang := math.Pi * float64(k) / float64(2*N)
		a[k] = Complex{Real: X[k] * math.Cos(ang), Imag: X[k] * math.Sin(ang)}
	}

	// 2N-point DFT.
	Z := AnyFFT(a)

	// Post-multiply:
	// Y[n] = Re(exp(iπ(2j+1)/(4N)) * Z[(2N-j) mod 2N]) * (2/N),
	// where j = n + N/2.
	scale := 2.0 / float64(N)
	y := make([]float64, N)
	for n := 0; n < N; n++ {
		j := n + N/2
		ang := math.Pi * float64(2*j+1) / float64(4*N)
		zn := Z[(2*N-j)%(2*N)]
		y[n] = (math.Cos(ang)*zn.Real - math.Sin(ang)*zn.Imag) * scale
	}

	return y
}

// IMDCTRaw computes the raw libopus clt_mdct_backward buffer before the final
// TDAC mirror/window step. Unlike IMDCT, this is not normalised by 2/N and the
// samples are in libopus' internal half-overlap-shifted order.
func (m *CELTMode) IMDCTRaw(X []float64) []float64 {
	N := m.N
	N2 := N
	N4 := N / 2

	f := make([]Complex, N4)
	for i := 0; i < N4; i++ {
		xp1 := X[2*i]
		xp2 := X[N2-1-2*i]
		t0 := math.Cos(2 * math.Pi * (float64(i) + 0.125) / float64(2*N))
		t1 := math.Cos(2 * math.Pi * (float64(N4+i) + 0.125) / float64(2*N))
		yr := xp2*t0 + xp1*t1
		yi := xp1*t0 - xp2*t1
		f[i] = Complex{Real: yi, Imag: yr}
	}

	z := AnyFFT(f)
	buf := make([]float64, N2)
	for i, c := range z {
		buf[2*i] = c.Real
		buf[2*i+1] = c.Imag
	}

	for i := 0; i < (N4+1)/2; i++ {
		yp0 := 2 * i
		yp1 := N2 - 2 - 2*i

		re := buf[yp0+1]
		im := buf[yp0]
		t0 := math.Cos(2 * math.Pi * (float64(i) + 0.125) / float64(2*N))
		t1 := math.Cos(2 * math.Pi * (float64(N4+i) + 0.125) / float64(2*N))
		yr := re*t0 + im*t1
		yi := re*t1 - im*t0

		re = buf[yp1+1]
		im = buf[yp1]
		buf[yp0] = yr
		buf[yp1+1] = yi

		t0 = math.Cos(2 * math.Pi * (float64(N4-i-1) + 0.125) / float64(2*N))
		t1 = math.Cos(2 * math.Pi * (float64(N2-i-1) + 0.125) / float64(2*N))
		yr = re*t0 + im*t1
		yi = re*t1 - im*t0
		buf[yp1] = yr
		buf[yp0+1] = yi
	}

	return buf
}

// CLTMDCTForward performs the CELT forward MDCT, the analysis counterpart of
// CLTMDCTBackward. It is a faithful float port of libopus clt_mdct_forward_c
// (celt/mdct.c) for the non-subdivided (stride=1) case, using the same trig
// convention as IMDCTRaw (cos(2π(i+0.125)/(2N))).
//
// Input `in` is the analysis buffer of length N+Overlap: the first Overlap
// samples are the windowed transition from the previous frame and the remaining
// N samples are the current frame (libopus celt_encoder compute_mdcts passes
// in+b*N over a buffer of stride B*N+overlap). Output is N MDCT coefficients
// matching the layout consumed by CLTMDCTBackward.
//
// scale is the post-FFT scale factor making CLTMDCTBackward(CLTMDCTForward(x))
// reconstruct x through overlap-add; it pairs with the "raw" (unnormalised)
// IMDCTRaw used by the backward path.
func (m *CELTMode) CLTMDCTForward(in []float64) []float64 {
	N := m.N        // libopus N2 (e.g. 960)
	nFull := 2 * N  // libopus N (e.g. 1920) — argument to the trig table
	N2 := N         // 960
	N4 := N / 2     // 480
	ov := m.Overlap // 120
	w := m.Window   // length ov
	// The backward path (IMDCTRaw) is unnormalised; the forward/backward pair has
	// intrinsic gain N, so 2/N here yields a unity-gain transform pair.
	scale := 2.0 / float64(N)

	f := make([]float64, N2)

	// Window, shuffle, fold (libopus clt_mdct_forward "Window, shuffle, fold").
	xp1 := ov >> 1
	xp2 := N2 - 1 + (ov >> 1)
	wp1 := ov >> 1
	wp2 := (ov >> 1) - 1
	yp := 0
	nfold := (ov + 3) >> 2
	i := 0
	for ; i < nfold; i++ {
		f[yp] = in[xp1+N2]*w[wp2] + in[xp2]*w[wp1]
		yp++
		f[yp] = in[xp1]*w[wp1] - in[xp2-N2]*w[wp2]
		yp++
		xp1 += 2
		xp2 -= 2
		wp1 += 2
		wp2 -= 2
	}
	// Flat middle (no window).
	wp1 = 0
	wp2 = ov - 1
	for ; i < N4-nfold; i++ {
		f[yp] = in[xp2]
		yp++
		f[yp] = in[xp1]
		yp++
		xp1 += 2
		xp2 -= 2
	}
	for ; i < N4; i++ {
		f[yp] = -in[xp1-N2]*w[wp1] + in[xp2]*w[wp2]
		yp++
		f[yp] = in[xp1]*w[wp2] + in[xp2+N2]*w[wp1]
		yp++
		xp1 += 2
		xp2 -= 2
		wp1 += 2
		wp2 -= 2
	}

	// Pre-rotation.
	fc := make([]Complex, N4)
	for i := 0; i < N4; i++ {
		t0 := math.Cos(2 * math.Pi * (float64(i) + 0.125) / float64(nFull))
		t1 := math.Cos(2 * math.Pi * (float64(N4+i) + 0.125) / float64(nFull))
		re := f[2*i]
		im := f[2*i+1]
		yr := re*t0 - im*t1
		yi := im*t0 + re*t1
		fc[i] = Complex{Real: yr * scale, Imag: yi * scale}
	}

	z := AnyFFT(fc)

	// Post-rotation (stride=1): out[2i]=yr from the low end, out[N2-1-2i]=yi.
	out := make([]float64, N2)
	for i := 0; i < N4; i++ {
		t0 := math.Cos(2 * math.Pi * (float64(i) + 0.125) / float64(nFull))
		t1 := math.Cos(2 * math.Pi * (float64(N4+i) + 0.125) / float64(nFull))
		fr := z[i].Real
		fi := z[i].Imag
		yr := fi*t1 - fr*t0
		yi := fr*t1 + fi*t0
		out[2*i] = yr
		out[N2-1-2*i] = yi
	}
	return out
}

// CLTMDCTBackward performs the CELT inverse transform and TDAC mirror/window
// step matching libopus clt_mdct_backward. carry holds the ov/2 future samples
// preserved from the previous call in libopus' decode_mem layout.
func (m *CELTMode) CLTMDCTBackward(X []float64, carry []float64) []float64 {
	N := m.N
	ov := m.Overlap
	half := ov / 2
	raw := m.IMDCTRaw(X)
	buf := make([]float64, N+half)
	copy(buf[:half], carry)
	copy(buf[half:], raw)

	for i := 0; i < half; i++ {
		x1 := buf[ov-1-i]
		x2 := buf[i]
		wi := m.Window[i]
		wj := m.Window[ov-1-i]
		buf[i] = wj*x2 - wi*x1
		buf[ov-1-i] = wi*x2 + wj*x1
	}

	copy(carry, buf[N:N+half])
	for i := half; i < len(carry); i++ {
		carry[i] = 0
	}
	return buf[:N]
}

// InverseOverlapAdd is the MDCT-IV overlap-add currently used by CELT synthesis.
func (m *CELTMode) InverseOverlapAdd(y []float64, tail []float64) []float64 {
	N := m.N
	ov := m.Overlap
	out := make([]float64, N)
	for i := 0; i < ov; i++ {
		out[i] = y[i]*m.Window[i] + tail[i]*m.Window[ov-1-i]
	}
	for i := ov; i < N; i++ {
		out[i] = y[i]
	}
	copy(tail, y[N-ov:])
	return out
}
