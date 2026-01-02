package dsp

import (
	"errors"
	"math"
)

// FFT performs a Cooley-Tukey FFT on the input data.
// The input size must be a power of 2.
// This implementation is based on the classic radix-2 decimation-in-time algorithm.
func FFT(input []Complex) ([]Complex, error) {
	n := len(input)
	if n <= 1 {
		return input, nil
	}

	if !IsPowerOf2(n) {
		return nil, errors.New("dsp: FFT size must be a power of 2")
	}

	// Create output array
	output := make([]Complex, n)
	copy(output, input)

	// Bit-reverse ordering
	bits := Log2(n)
	for i := 0; i < n; i++ {
		j := BitReverse(i, bits)
		if j > i {
			output[i], output[j] = output[j], output[i]
		}
	}

	// FFT computation using Cooley-Tukey algorithm
	for size := 2; size <= n; size *= 2 {
		halfSize := size / 2
		step := 2 * math.Pi / float64(size)

		for start := 0; start < n; start += size {
			for k := 0; k < halfSize; k++ {
				// Twiddle factor: e^(-2πik/size)
				angle := -step * float64(k)
				twiddle := Complex{
					Real: math.Cos(angle),
					Imag: math.Sin(angle),
				}

				even := output[start+k]
				odd := output[start+k+halfSize].Mul(twiddle)

				output[start+k] = even.Add(odd)
				output[start+k+halfSize] = even.Sub(odd)
			}
		}
	}

	return output, nil
}

// IFFT performs an inverse FFT.
func IFFT(input []Complex) ([]Complex, error) {
	n := len(input)
	if n <= 1 {
		return input, nil
	}

	// Conjugate the input
	conjugated := make([]Complex, n)
	for i, c := range input {
		conjugated[i] = c.Conj()
	}

	// Perform FFT on conjugated input
	output, err := FFT(conjugated)
	if err != nil {
		return nil, err
	}

	// Conjugate and scale the output
	scale := 1.0 / float64(n)
	for i := range output {
		output[i] = output[i].Conj().MulScalar(scale)
	}

	return output, nil
}

// RealFFT performs FFT on real-valued input, exploiting symmetry.
// Returns only the first n/2+1 complex values (the rest are conjugate symmetric).
func RealFFT(input []float64) ([]Complex, error) {
	n := len(input)
	if !IsPowerOf2(n) {
		return nil, errors.New("dsp: FFT size must be a power of 2")
	}

	// Convert to complex
	complexInput := make([]Complex, n)
	for i, v := range input {
		complexInput[i] = Complex{Real: v, Imag: 0}
	}

	// Perform full FFT
	fullFFT, err := FFT(complexInput)
	if err != nil {
		return nil, err
	}

	// Return first half + DC
	return fullFFT[:n/2+1], nil
}

// RealIFFT performs inverse FFT to produce real-valued output.
func RealIFFT(input []Complex, outputSize int) ([]float64, error) {
	if !IsPowerOf2(outputSize) {
		return nil, errors.New("dsp: IFFT size must be a power of 2")
	}

	// Reconstruct full spectrum using symmetry
	fullSpectrum := make([]Complex, outputSize)
	copy(fullSpectrum, input)

	// Fill in conjugate symmetric part
	for i := outputSize/2 + 1; i < outputSize; i++ {
		fullSpectrum[i] = fullSpectrum[outputSize-i].Conj()
	}

	// Perform IFFT
	complexOutput, err := IFFT(fullSpectrum)
	if err != nil {
		return nil, err
	}

	// Extract real part
	output := make([]float64, outputSize)
	for i, c := range complexOutput {
		output[i] = c.Real
	}

	return output, nil
}

// FFTConfig holds precomputed twiddle factors for efficient repeated FFT operations.
type FFTConfig struct {
	size     int
	bits     int
	twiddles []Complex
}

// NewFFTConfig creates a new FFT configuration with precomputed twiddle factors.
func NewFFTConfig(size int) (*FFTConfig, error) {
	if !IsPowerOf2(size) {
		return nil, errors.New("dsp: FFT size must be a power of 2")
	}

	cfg := &FFTConfig{
		size:     size,
		bits:     Log2(size),
		twiddles: make([]Complex, size/2),
	}

	// Precompute twiddle factors
	for i := 0; i < size/2; i++ {
		angle := -2 * math.Pi * float64(i) / float64(size)
		cfg.twiddles[i] = Complex{
			Real: math.Cos(angle),
			Imag: math.Sin(angle),
		}
	}

	return cfg, nil
}

// Execute performs FFT using precomputed twiddle factors.
func (cfg *FFTConfig) Execute(input []Complex) ([]Complex, error) {
	n := cfg.size
	if len(input) != n {
		return nil, errors.New("dsp: input size does not match FFT config size")
	}

	// Create output array
	output := make([]Complex, n)
	copy(output, input)

	// Bit-reverse ordering
	for i := 0; i < n; i++ {
		j := BitReverse(i, cfg.bits)
		if j > i {
			output[i], output[j] = output[j], output[i]
		}
	}

	// FFT computation
	for size := 2; size <= n; size *= 2 {
		halfSize := size / 2
		tableStep := n / size

		for start := 0; start < n; start += size {
			for k := 0; k < halfSize; k++ {
				twiddle := cfg.twiddles[k*tableStep]

				even := output[start+k]
				odd := output[start+k+halfSize].Mul(twiddle)

				output[start+k] = even.Add(odd)
				output[start+k+halfSize] = even.Sub(odd)
			}
		}
	}

	return output, nil
}

// ExecuteInverse performs inverse FFT using precomputed twiddle factors.
func (cfg *FFTConfig) ExecuteInverse(input []Complex) ([]Complex, error) {
	n := cfg.size

	// Conjugate the input
	conjugated := make([]Complex, n)
	for i, c := range input {
		conjugated[i] = c.Conj()
	}

	// Perform FFT
	output, err := cfg.Execute(conjugated)
	if err != nil {
		return nil, err
	}

	// Conjugate and scale the output
	scale := 1.0 / float64(n)
	for i := range output {
		output[i] = output[i].Conj().MulScalar(scale)
	}

	return output, nil
}
