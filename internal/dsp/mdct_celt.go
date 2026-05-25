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

// IMDCT computes the N-point CELT inverse MDCT-IV and returns N raw (unwindowed)
// time-domain samples.  Windowing is deferred to InverseOverlapAdd, matching the
// libopus synthesis model where the OLA step applies both the rising window to the
// current frame and the falling window to the stored tail.
//
// Formula (standard MDCT-IV):
//
//	Y[n] = (2/N) * sum_{k=0}^{N-1} X[k] * cos(π/N * (k+1/2) * (n + N/2 + 1/2))
//	     = (2/N) * Re( exp(iπ(2n+N+1)/(4N)) * DFT_{2N}(a')[n] )
//	where a'[k] = X[k] * exp(iπk(N+1)/(2N))  (zero-padded to 2N)
func (m *CELTMode) IMDCT(X []float64) []float64 {
	N := m.N

	// Pre-multiply X[k] by exp(iπk(N+1)/(2N)) and zero-pad to length 2N.
	a := make([]Complex, 2*N)
	for k := 0; k < N; k++ {
		ang := math.Pi * float64(k) * float64(N+1) / float64(2*N)
		a[k] = Complex{Real: X[k] * math.Cos(ang), Imag: X[k] * math.Sin(ang)}
	}

	// 2N-point DFT via Bluestein's algorithm (AnyFFT handles arbitrary sizes).
	Z := AnyFFT(a)

	// Post-multiply and take real part to get the MDCT-IV output.
	// The correct index into Z is (2N-n) mod 2N (the negative-frequency component),
	// because the Bluestein pre-rotation produces Z[n] = sum_k X[k]*exp(iπk(N+1-2n)/(2N))
	// but the MDCT-IV requires sum_k X[k]*exp(iπk(2n+N+1)/(2N)) = Z[(2N-n) % 2N].
	scale := 2.0 / float64(N)
	y := make([]float64, N)
	for n := 0; n < N; n++ {
		ang := math.Pi * float64(2*n+N+1) / float64(4*N)
		zn := Z[(2*N-n)%(2*N)]
		// Re( (cos+i·sin) · (zn.Real+i·zn.Imag) ) = cos·zn.Real − sin·zn.Imag
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
	// Non-overlap region: direct (synthesis window = 1 in the middle, raw in the tail).
	for i := ov; i < N; i++ {
		out[i] = y[i]
	}

	// Save the last ov raw samples as the tail for the next frame.
	copy(tail, y[N-ov:])
	return out
}
