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

// InverseOverlapAdd performs the CELT overlap-add using a small tail buffer of
// length Overlap.  Windowing is applied here rather than in IMDCT:
//   - current frame's first Overlap samples get the rising window (Window[0..ov-1])
//   - the stored tail (previous frame's last Overlap raw samples) gets the falling window
//
// Returns N output samples; updates tail in-place with the last Overlap raw samples of y.
func (m *CELTMode) InverseOverlapAdd(y []float64, tail []float64) []float64 {
	N := m.N
	ov := m.Overlap
	out := make([]float64, N)

	// OLA region: rising_window * current + falling_window * prev_tail.
	for i := 0; i < ov; i++ {
		out[i] = y[i]*m.Window[i] + tail[i]*m.Window[ov-1-i]
	}
	// Non-overlap region: direct.
	for i := ov; i < N; i++ {
		out[i] = y[i]
	}

	// Save the last ov raw samples as the tail for the next frame.
	copy(tail, y[N-ov:])
	return out
}
