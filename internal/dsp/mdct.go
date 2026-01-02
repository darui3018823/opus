package dsp

import "math"

// MDCT represents a Modified Discrete Cosine Transform configuration.
// MDCT is used extensively in audio coding, particularly in CELT.
type MDCT struct {
	size     int        // Transform size (number of output coefficients)
	fftSize  int        // Internal FFT size (size/2)
	fftCfg   *FFTConfig // Precomputed FFT configuration
	window   []float64  // Window function
	twiddles []Complex  // Pre-rotation twiddle factors
}

// NewMDCT creates a new MDCT configuration.
// size is the number of output coefficients (input is 2*size samples).
func NewMDCT(size int) *MDCT {
	if !IsPowerOf2(size) {
		panic("dsp: MDCT size must be a power of 2")
	}

	fftSize := size / 2
	mdct := &MDCT{
		size:     size,
		fftSize:  fftSize,
		fftCfg:   NewFFTConfig(fftSize),
		window:   Window(WindowVorbis, 2*size),
		twiddles: make([]Complex, size),
	}

	// Precompute twiddle factors for pre/post-rotation
	for i := 0; i < size; i++ {
		angle := math.Pi * (float64(i) + 0.5) / float64(size)
		mdct.twiddles[i] = Complex{
			Real: math.Cos(angle),
			Imag: math.Sin(angle),
		}
	}

	return mdct
}

// Forward performs the forward MDCT transform.
// Input: 2*N samples, Output: N coefficients.
func (m *MDCT) Forward(input []float64) []float64 {
	n := m.size
	if len(input) != 2*n {
		panic("dsp: MDCT input must be 2*N samples")
	}

	// Apply window
	windowed := make([]float64, 2*n)
	for i := 0; i < 2*n; i++ {
		windowed[i] = input[i] * m.window[i]
	}

	// Pre-rotation and folding
	fftInput := make([]Complex, m.fftSize)
	for i := 0; i < m.fftSize; i++ {
		// Fold the windowed signal
		x1 := windowed[i]
		x2 := windowed[2*n-1-i]
		x3 := windowed[n+i]
		x4 := windowed[n-1-i]

		re := x1 - x2
		im := x3 + x4

		// Apply pre-rotation twiddle
		angle := -math.Pi * (float64(i) + 0.5) / float64(2*n)
		cos := math.Cos(angle)
		sin := math.Sin(angle)

		fftInput[i] = Complex{
			Real: re*cos - im*sin,
			Imag: re*sin + im*cos,
		}
	}

	// Perform FFT
	fftOutput := m.fftCfg.Execute(fftInput)

	// Post-rotation to get MDCT coefficients
	output := make([]float64, n)
	for i := 0; i < n; i++ {
		// Post-rotation
		angle := math.Pi * (float64(i) + 0.5) / float64(n)
		cos := math.Cos(angle)
		sin := math.Sin(angle)

		k := i % m.fftSize
		output[i] = 2.0 * (fftOutput[k].Real*cos + fftOutput[k].Imag*sin)
	}

	return output
}

// Inverse performs the inverse MDCT transform (IMDCT).
// Input: N coefficients, Output: 2*N samples.
func (m *MDCT) Inverse(input []float64) []float64 {
	n := m.size
	if len(input) != n {
		panic("dsp: IMDCT input must be N coefficients")
	}

	// Pre-rotation
	fftInput := make([]Complex, m.fftSize)
	for i := 0; i < m.fftSize; i++ {
		// Gather two coefficients and pre-rotate
		angle1 := math.Pi * (float64(i) + 0.5) / float64(n)
		angle2 := math.Pi * (float64(i+m.fftSize) + 0.5) / float64(n)

		cos1 := math.Cos(angle1)
		sin1 := math.Sin(angle1)
		cos2 := math.Cos(angle2)
		sin2 := math.Sin(angle2)

		fftInput[i] = Complex{
			Real: input[i]*cos1 + input[i+m.fftSize]*cos2,
			Imag: input[i]*sin1 + input[i+m.fftSize]*sin2,
		}
	}

	// Perform IFFT
	fftOutput := m.fftCfg.ExecuteInverse(fftInput)

	// Post-rotation and unfolding
	output := make([]float64, 2*n)
	scale := 2.0 / float64(n)

	for i := 0; i < m.fftSize; i++ {
		angle := math.Pi * (float64(i) + 0.5) / float64(2*n)
		cos := math.Cos(angle)
		sin := math.Sin(angle)

		re := fftOutput[i].Real*cos - fftOutput[i].Imag*sin
		im := fftOutput[i].Real*sin + fftOutput[i].Imag*cos

		re *= scale
		im *= scale

		// Unfold to output
		output[i] = re
		output[n+i] = im
		output[n-1-i] = -re
		output[2*n-1-i] = im
	}

	// Apply synthesis window
	for i := 0; i < 2*n; i++ {
		output[i] *= m.window[i]
	}

	return output
}

// ForwardOverlap performs forward MDCT with proper overlap handling for streaming.
func (m *MDCT) ForwardOverlap(input []float64, overlap []float64) []float64 {
	n := m.size
	if len(input) != n || len(overlap) != n {
		panic("dsp: ForwardOverlap requires N input samples and N overlap samples")
	}

	// Combine overlap and new input
	combined := make([]float64, 2*n)
	copy(combined[:n], overlap)
	copy(combined[n:], input)

	// Perform forward MDCT
	return m.Forward(combined)
}

// InverseOverlap performs inverse MDCT with proper overlap-add for streaming.
func (m *MDCT) InverseOverlap(coeffs []float64, overlap []float64) []float64 {
	n := m.size
	if len(coeffs) != n || len(overlap) != n {
		panic("dsp: InverseOverlap requires N coefficients and N overlap buffer")
	}

	// Perform inverse MDCT
	output := m.Inverse(coeffs)

	// Output first half with overlap-add
	result := make([]float64, n)
	for i := 0; i < n; i++ {
		result[i] = output[i] + overlap[i]
	}

	// Save second half for next overlap
	copy(overlap, output[n:])

	return result
}
