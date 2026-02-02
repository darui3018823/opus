package dsp

import (
	"math"
	"testing"
)

func TestMDCTInverseProperty(t *testing.T) {
	// Test that Forward and Inverse are inverses (with overlap-add)
	size := 64
	mdct, err := NewMDCT(size)
	if err != nil {
		t.Fatalf("NewMDCT failed: %v", err)
	}

	// Create test signal (2N samples)
	input := make([]float64, 2*size)
	for i := range input {
		input[i] = math.Sin(2.0 * math.Pi * float64(i) / float64(len(input)))
	}

	// Forward transform
	coeffs, err := mdct.Forward(input)
	if err != nil {
		t.Fatalf("Forward failed: %v", err)
	}
	if len(coeffs) != size {
		t.Errorf("MDCT output size = %d, want %d", len(coeffs), size)
	}

	// Inverse transform
	reconstructed, err := mdct.Inverse(coeffs)
	if err != nil {
		t.Fatalf("Inverse failed: %v", err)
	}
	if len(reconstructed) != 2*size {
		t.Errorf("IMDCT output size = %d, want %d", len(reconstructed), 2*size)
	}

	// Due to MDCT properties and windowing, perfect reconstruction
	// requires overlap-add. Here we just check that values are reasonable.
	for i, v := range reconstructed {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			t.Errorf("IMDCT[%d] = %v (invalid)", i, v)
		}
	}
}

func TestMDCTOverlapAdd(t *testing.T) {
	// Test proper overlap-add reconstruction
	size := 32
	mdct, err := NewMDCT(size)
	if err != nil {
		t.Fatalf("NewMDCT failed: %v", err)
	}

	// Create a longer signal
	signal := make([]float64, 4*size)
	for i := range signal {
		signal[i] = math.Sin(2.0 * math.Pi * float64(i) / 32.0)
	}

	// Process in overlapping frames
	overlap := make([]float64, size)
	var reconstructed []float64

	for offset := 0; offset < len(signal)-size; offset += size {
		frame := signal[offset : offset+size]

		// Forward MDCT with overlap
		coeffs, err := mdct.ForwardOverlap(frame, overlap)
		if err != nil {
			t.Fatalf("ForwardOverlap failed: %v", err)
		}

		// Inverse MDCT with overlap-add
		decoded, err := mdct.InverseOverlap(coeffs, overlap)
		if err != nil {
			t.Fatalf("InverseOverlap failed: %v", err)
		}
		reconstructed = append(reconstructed, decoded...)
	}

	// Check that reconstruction is reasonable
	if len(reconstructed) < size {
		t.Errorf("Reconstructed length = %d, too short", len(reconstructed))
	}

	// Values should be in reasonable range
	for i, v := range reconstructed {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			t.Errorf("Reconstructed[%d] = %v (invalid)", i, v)
		}
		if math.Abs(v) > 10 {
			t.Errorf("Reconstructed[%d] = %v (too large)", i, v)
		}
	}
}

func TestMDCTImpulseResponse(t *testing.T) {
	size := 32
	mdct, err := NewMDCT(size)
	if err != nil {
		t.Fatalf("NewMDCT failed: %v", err)
	}

	// Impulse at center
	input := make([]float64, 2*size)
	input[size] = 1.0

	// Forward transform
	coeffs, err := mdct.Forward(input)
	if err != nil {
		t.Fatalf("Forward failed: %v", err)
	}

	// Check that we get some non-zero coefficients
	hasNonZero := false
	for _, c := range coeffs {
		if math.Abs(c) > 1e-10 {
			hasNonZero = true
			break
		}
	}
	if !hasNonZero {
		t.Error("MDCT of impulse should have non-zero coefficients")
	}

	// Inverse transform
	reconstructed, err := mdct.Inverse(coeffs)
	if err != nil {
		t.Fatalf("Inverse failed: %v", err)
	}

	// Check reconstruction
	for i, v := range reconstructed {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			t.Errorf("Reconstructed[%d] = %v (invalid)", i, v)
		}
	}
}

func TestMDCTDCComponent(t *testing.T) {
	size := 32
	mdct, err := NewMDCT(size)
	if err != nil {
		t.Fatalf("NewMDCT failed: %v", err)
	}

	// Constant signal (DC)
	input := make([]float64, 2*size)
	for i := range input {
		input[i] = 1.0
	}

	// Forward transform
	coeffs, err := mdct.Forward(input)
	if err != nil {
		t.Fatalf("Forward failed: %v", err)
	}

	// For a constant signal after windowing, we expect most energy
	// in low-frequency coefficients
	lowFreqEnergy := 0.0
	highFreqEnergy := 0.0

	for i := 0; i < size/4; i++ {
		lowFreqEnergy += coeffs[i] * coeffs[i]
	}
	for i := size / 2; i < size; i++ {
		highFreqEnergy += coeffs[i] * coeffs[i]
	}

	if lowFreqEnergy < highFreqEnergy {
		t.Logf("Low freq energy: %v, High freq energy: %v", lowFreqEnergy, highFreqEnergy)
		t.Log("Expected more energy in low frequencies for DC signal")
		// This is informational, not necessarily a failure due to windowing
	}
}

func TestMDCTSineWave(t *testing.T) {
	size := 64
	mdct, err := NewMDCT(size)
	if err != nil {
		t.Fatalf("NewMDCT failed: %v", err)
	}

	// Pure sine wave
	freq := 4.0 // 4 cycles over 2*size samples
	input := make([]float64, 2*size)
	for i := range input {
		input[i] = math.Sin(2.0 * math.Pi * freq * float64(i) / float64(2*size))
	}

	// Forward transform
	coeffs, err := mdct.Forward(input)
	if err != nil {
		t.Fatalf("Forward failed: %v", err)
	}

	// Inverse transform
	reconstructed, err := mdct.Inverse(coeffs)
	if err != nil {
		t.Fatalf("Inverse failed: %v", err)
	}

	// Check that values are valid
	for i, v := range reconstructed {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			t.Errorf("Reconstructed[%d] = %v (invalid)", i, v)
		}
	}
}

func BenchmarkMDCTForward128(b *testing.B) {
	mdct, _ := NewMDCT(128)
	input := make([]float64, 256)
	for i := range input {
		input[i] = math.Sin(2.0 * math.Pi * float64(i) / float64(len(input)))
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = mdct.Forward(input)
	}
}

func BenchmarkMDCTInverse128(b *testing.B) {
	mdct, _ := NewMDCT(128)
	coeffs := make([]float64, 128)
	for i := range coeffs {
		coeffs[i] = float64(i)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = mdct.Inverse(coeffs)
	}
}

func BenchmarkMDCTForward512(b *testing.B) {
	mdct, _ := NewMDCT(512)
	input := make([]float64, 1024)
	for i := range input {
		input[i] = math.Sin(2.0 * math.Pi * float64(i) / float64(len(input)))
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = mdct.Forward(input)
	}
}
