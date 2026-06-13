package dsp

import (
	"errors"
	"math"
)

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
// For non-power-of-2 sizes (e.g. 960), a direct cosine computation is used.
func NewMDCT(size int) (*MDCT, error) {
	if size <= 0 {
		return nil, errors.New("dsp: MDCT size must be positive")
	}

	mdct := &MDCT{
		size:     size,
		window:   Window(WindowVorbis, 2*size),
		twiddles: make([]Complex, size),
	}

	if IsPowerOf2(size) {
		fftSize := size / 2
		fftCfg, err := NewFFTConfig(fftSize)
		if err != nil {
			return nil, err
		}
		mdct.fftSize = fftSize
		mdct.fftCfg = fftCfg
	}
	// For non-power-of-2 sizes, fftCfg is nil → direct computation path.

	// Precompute twiddle factors for pre/post-rotation (used by FFT path)
	for i := 0; i < size; i++ {
		angle := math.Pi * (float64(i) + 0.5) / float64(size)
		mdct.twiddles[i] = Complex{
			Real: math.Cos(angle),
			Imag: math.Sin(angle),
		}
	}

	return mdct, nil
}

// Size returns the MDCT output size (number of coefficients)
func (m *MDCT) Size() int {
	return m.size
}

// Forward performs the forward MDCT transform.
// Input: 2*N samples, Output: N coefficients.
func (m *MDCT) Forward(input []float64) ([]float64, error) {
	n := m.size
	if len(input) != 2*n {
		return nil, errors.New("dsp: MDCT input must be 2*N samples")
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
	fftOutput, err := m.fftCfg.Execute(fftInput)
	if err != nil {
		return nil, err
	}

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

	return output, nil
}

// Inverse performs the inverse MDCT transform (IMDCT).
// Input: N coefficients, Output: 2*N samples (before overlap-add).
func (m *MDCT) Inverse(input []float64) ([]float64, error) {
	n := m.size
	if len(input) != n {
		return nil, errors.New("dsp: IMDCT input must be N coefficients")
	}

	if m.fftCfg != nil {
		return m.inverseFft(input)
	}
	return m.inverseDirect(input), nil
}

// inverseFft is the FFT-based IMDCT (power-of-2 sizes only).
func (m *MDCT) inverseFft(input []float64) ([]float64, error) {
	n := m.size

	// Pre-rotation
	fftInput := make([]Complex, m.fftSize)
	for i := 0; i < m.fftSize; i++ {
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

	fftOutput, err := m.fftCfg.ExecuteInverse(fftInput)
	if err != nil {
		return nil, err
	}

	output := make([]float64, 2*n)
	scale := 2.0 / float64(n)

	for i := 0; i < m.fftSize; i++ {
		angle := math.Pi * (float64(i) + 0.5) / float64(2*n)
		cos := math.Cos(angle)
		sin := math.Sin(angle)

		re := (fftOutput[i].Real*cos - fftOutput[i].Imag*sin) * scale
		im := (fftOutput[i].Real*sin + fftOutput[i].Imag*cos) * scale

		output[i] = re
		output[n+i] = im
		output[n-1-i] = -re
		output[2*n-1-i] = im
	}

	for i := 0; i < 2*n; i++ {
		output[i] *= m.window[i]
	}

	return output, nil
}

// inverseDirect computes IMDCT via direct DCT-IV sum — correct for any N.
// O(N²), intended for non-power-of-2 sizes like 960.
func (m *MDCT) inverseDirect(input []float64) []float64 {
	n := m.size
	output := make([]float64, 2*n)
	scale := 2.0 / float64(n)
	for sn := 0; sn < 2*n; sn++ {
		v := 0.0
		base := math.Pi / float64(n) * (float64(sn) + 0.5 + float64(n)/2.0)
		for k := 0; k < n; k++ {
			v += input[k] * math.Cos(base*(float64(k)+0.5))
		}
		output[sn] = v * scale * m.window[sn]
	}
	return output
}

// ForwardOverlap performs forward MDCT with proper overlap handling for streaming.
func (m *MDCT) ForwardOverlap(input []float64, overlap []float64) ([]float64, error) {
	n := m.size
	if len(input) != n || len(overlap) != n {
		return nil, errors.New("dsp: ForwardOverlap requires N input samples and N overlap samples")
	}

	// Combine overlap and new input
	combined := make([]float64, 2*n)
	copy(combined[:n], overlap)
	copy(combined[n:], input)

	// Perform forward MDCT
	return m.Forward(combined)
}

// InverseOverlap performs inverse MDCT with proper overlap-add for streaming.
func (m *MDCT) InverseOverlap(coeffs []float64, overlap []float64) ([]float64, error) {
	n := m.size
	if len(coeffs) != n || len(overlap) != n {
		return nil, errors.New("dsp: InverseOverlap requires N coefficients and N overlap buffer")
	}

	// Perform inverse MDCT
	output, err := m.Inverse(coeffs)
	if err != nil {
		return nil, err
	}

	// Output first half with overlap-add
	result := make([]float64, n)
	for i := 0; i < n; i++ {
		result[i] = output[i] + overlap[i]
	}

	// Save second half for next overlap
	copy(overlap, output[n:])

	return result, nil
}
