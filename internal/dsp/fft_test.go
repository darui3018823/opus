package dsp

import (
	"math"
	"testing"
)

func TestFFTBasic(t *testing.T) {
	// Test FFT of a simple impulse
	input := []Complex{
		{Real: 1, Imag: 0},
		{Real: 0, Imag: 0},
		{Real: 0, Imag: 0},
		{Real: 0, Imag: 0},
	}

	output := FFT(input)

	// FFT of an impulse should be all ones
	for i, c := range output {
		if math.Abs(c.Real-1.0) > 1e-10 || math.Abs(c.Imag) > 1e-10 {
			t.Errorf("FFT[%d] = %v, want {1, 0}", i, c)
		}
	}
}

func TestFFTIFFTRoundtrip(t *testing.T) {
	// Test that IFFT(FFT(x)) = x
	input := []Complex{
		{Real: 1, Imag: 0},
		{Real: 2, Imag: 0},
		{Real: 3, Imag: 0},
		{Real: 4, Imag: 0},
		{Real: 5, Imag: 0},
		{Real: 6, Imag: 0},
		{Real: 7, Imag: 0},
		{Real: 8, Imag: 0},
	}

	fft := FFT(input)
	ifft := IFFT(fft)

	for i := range input {
		if math.Abs(ifft[i].Real-input[i].Real) > 1e-10 ||
			math.Abs(ifft[i].Imag-input[i].Imag) > 1e-10 {
			t.Errorf("Roundtrip failed at %d: got %v, want %v", i, ifft[i], input[i])
		}
	}
}

func TestRealFFT(t *testing.T) {
	// Test real FFT
	input := []float64{1, 2, 3, 4, 5, 6, 7, 8}
	output := RealFFT(input)

	// Should return N/2 + 1 = 5 complex values
	if len(output) != 5 {
		t.Errorf("RealFFT output length = %d, want 5", len(output))
	}

	// DC component should be sum of inputs
	expectedDC := 1.0 + 2 + 3 + 4 + 5 + 6 + 7 + 8
	if math.Abs(output[0].Real-expectedDC) > 1e-10 {
		t.Errorf("DC component = %v, want %v", output[0].Real, expectedDC)
	}
}

func TestRealFFTIFFTRoundtrip(t *testing.T) {
	input := []float64{1, 2, 3, 4, 5, 6, 7, 8}
	fft := RealFFT(input)
	ifft := RealIFFT(fft, len(input))

	for i := range input {
		if math.Abs(ifft[i]-input[i]) > 1e-10 {
			t.Errorf("Roundtrip failed at %d: got %v, want %v", i, ifft[i], input[i])
		}
	}
}

func TestFFTConfig(t *testing.T) {
	// Test FFT with precomputed config
	cfg := NewFFTConfig(8)

	input := []Complex{
		{Real: 1, Imag: 0},
		{Real: 2, Imag: 0},
		{Real: 3, Imag: 0},
		{Real: 4, Imag: 0},
		{Real: 5, Imag: 0},
		{Real: 6, Imag: 0},
		{Real: 7, Imag: 0},
		{Real: 8, Imag: 0},
	}

	// Compare with basic FFT
	expected := FFT(input)
	actual := cfg.Execute(input)

	for i := range expected {
		if math.Abs(actual[i].Real-expected[i].Real) > 1e-10 ||
			math.Abs(actual[i].Imag-expected[i].Imag) > 1e-10 {
			t.Errorf("FFTConfig[%d] = %v, want %v", i, actual[i], expected[i])
		}
	}
}

func TestFFTConfigRoundtrip(t *testing.T) {
	cfg := NewFFTConfig(16)

	input := make([]Complex, 16)
	for i := range input {
		input[i] = Complex{Real: float64(i + 1), Imag: 0}
	}

	fft := cfg.Execute(input)
	ifft := cfg.ExecuteInverse(fft)

	for i := range input {
		if math.Abs(ifft[i].Real-input[i].Real) > 1e-10 ||
			math.Abs(ifft[i].Imag-input[i].Imag) > 1e-10 {
			t.Errorf("Roundtrip failed at %d: got %v, want %v", i, ifft[i], input[i])
		}
	}
}

func BenchmarkFFT128(b *testing.B) {
	input := make([]Complex, 128)
	for i := range input {
		input[i] = Complex{Real: float64(i), Imag: 0}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		FFT(input)
	}
}

func BenchmarkFFT1024(b *testing.B) {
	input := make([]Complex, 1024)
	for i := range input {
		input[i] = Complex{Real: float64(i), Imag: 0}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		FFT(input)
	}
}

func BenchmarkFFTConfig1024(b *testing.B) {
	cfg := NewFFTConfig(1024)
	input := make([]Complex, 1024)
	for i := range input {
		input[i] = Complex{Real: float64(i), Imag: 0}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cfg.Execute(input)
	}
}

func BenchmarkRealFFT1024(b *testing.B) {
	input := make([]float64, 1024)
	for i := range input {
		input[i] = float64(i)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		RealFFT(input)
	}
}
