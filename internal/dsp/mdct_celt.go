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

// CELTOverlapAdd performs the CELT TDAC mirror overlap-add matching libopus
// clt_mdct_backward. The carry buffer holds ov/2 samples from the previous frame.
//
// In libopus decode_mem layout (shifted by ov/2 relative to frame start):
//   x1 = y[ov/2-1-i]   (IMDCT output first-half, reversed: the "mirror region")
//   x2 = carry[i]       (y_prev[N-ov/2..N-1]: last ov/2 raw IMDCT samples of prev frame)
//   out[i]      = W[ov-1-i]*x2 - W[i]*x1    (for i=0..ov/2-1)
//   out[ov-1-i] = W[i]*x2      + W[ov-1-i]*x1
// Direct (no window): out[ov+j] = y[ov/2+j]   (for j=0..N-ov-1)
// New carry = y[N-ov/2..N-1]  (last ov/2 IMDCT samples, used as x2 next frame)
func (m *CELTMode) CELTOverlapAdd(y []float64, carry []float64) []float64 {
	N := m.N
	ov := m.Overlap
	half := ov / 2
	out := make([]float64, N)

	for i := 0; i < half; i++ {
		x1 := y[half-1-i] // y[ov/2-1], y[ov/2-2], ..., y[0]
		x2 := carry[i]
		wi := m.Window[i]
		wj := m.Window[ov-1-i]
		out[i] = wj*x2 - wi*x1
		out[ov-1-i] = wi*x2 + wj*x1
	}
	// Direct output: y[ov/2..ov/2+N-ov-1]
	for j := 0; j < N-ov; j++ {
		out[ov+j] = y[half+j]
	}

	// New carry = y[N-ov/2:N] (last ov/2 raw IMDCT samples, preserved as decode_mem future).
	copy(carry, y[N-half:])
	return out
}

// InverseOverlapAdd is the MDCT-IV overlap-add currently used by CELT synthesis.
// CELTOverlapAdd is a candidate replacement but not yet wired up (simple swap worsens tv07).
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
