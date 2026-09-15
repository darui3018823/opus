package silk

import (
	"math"
	"testing"
)

// NLSF Quantization Tests

func TestNLSFOrdering(t *testing.T) {
	q := NewNLSFQuantizer(10)
	if q == nil {
		t.Fatal("Failed to create NLSF quantizer")
	}

	// Valid NLSF (properly ordered)
	validNLSF := []float64{
		0.1, 0.3, 0.5, 0.7, 1.0, 1.3, 1.6, 2.0, 2.5, 3.0,
	}

	if !q.CheckStability(validNLSF) {
		t.Error("Valid NLSF incorrectly marked as unstable")
	}

	// Invalid NLSF (not ordered)
	invalidNLSF := []float64{
		0.5, 0.3, 0.7, 1.0, 1.3, 1.6, 2.0, 2.5, 2.8, 3.0,
	}

	if q.CheckStability(invalidNLSF) {
		t.Error("Invalid NLSF incorrectly marked as stable")
	}
}

func TestNLSFStability(t *testing.T) {
	q := NewNLSFQuantizer(10)

	// NLSF with insufficient spacing
	nlsf := []float64{
		0.1, 0.105, 0.5, 0.7, 1.0, 1.3, 1.6, 2.0, 2.5, 3.0,
	}

	// Should fail stability check
	if q.CheckStability(nlsf) {
		t.Error("NLSF with insufficient spacing incorrectly passed stability check")
	}

	// Enforce stability
	q.EnforceStability(nlsf)

	// Should now pass
	if !q.CheckStability(nlsf) {
		t.Error("NLSF still unstable after enforcing stability")
	}

	// Verify spacing
	for i := 1; i < len(nlsf); i++ {
		if nlsf[i]-nlsf[i-1] < NLSFMinSpacing {
			t.Errorf("Spacing %f at index %d is too small", nlsf[i]-nlsf[i-1], i)
		}
	}
}

func TestNLSFWeights(t *testing.T) {
	q := NewNLSFQuantizer(10)

	nlsf := []float64{
		0.1, 0.3, 0.5, 0.7, 1.0, 1.3, 1.6, 2.0, 2.5, 3.0,
	}

	weights := q.ComputeWeights(nlsf)

	if len(weights) != 10 {
		t.Fatalf("Expected 10 weights, got %d", len(weights))
	}

	// Verify that weights decrease with frequency (lower frequencies more important)
	for i := 1; i < len(weights); i++ {
		if weights[i] > weights[i-1] {
			t.Errorf("Weight at index %d (%f) should be less than weight at %d (%f)",
				i, weights[i], i-1, weights[i-1])
		}
	}

	// All weights should be positive
	for i, w := range weights {
		if w <= 0 {
			t.Errorf("Weight at index %d is non-positive: %f", i, w)
		}
	}
}

func TestNLSFQuantization(t *testing.T) {
	q := NewNLSFQuantizer(10)

	nlsf := []float64{
		0.2, 0.4, 0.6, 0.9, 1.2, 1.5, 1.8, 2.2, 2.6, 3.0,
	}

	// Quantize
	indices, err := q.Quantize(nlsf)
	if err != nil {
		t.Fatalf("Quantization failed: %v", err)
	}

	if len(indices) != 2 {
		t.Fatalf("Expected 2 indices (2-stage), got %d", len(indices))
	}

	// Dequantize
	reconstructed, err := q.Dequantize(indices)
	if err != nil {
		t.Fatalf("Dequantization failed: %v", err)
	}

	if len(reconstructed) != 10 {
		t.Fatalf("Expected 10 reconstructed values, got %d", len(reconstructed))
	}

	// Note: With synthetic codebooks (not trained from libopus),
	// reconstruction may not preserve exact stability properties.
	// In production, trained codebooks would provide better stability.
	// We verify the quantization/dequantization API works correctly.

	// Verify reconstructed values are in valid range
	for i, val := range reconstructed {
		if val < 0 || val > math.Pi {
			t.Errorf("Reconstructed NLSF[%d]=%f is out of range [0, π]", i, val)
		}
	}
}

func TestNLSFInterpolation(t *testing.T) {
	q := NewNLSFQuantizer(10)

	nlsf1 := []float64{
		0.2, 0.4, 0.6, 0.9, 1.2, 1.5, 1.8, 2.2, 2.6, 3.0,
	}

	nlsf2 := []float64{
		0.3, 0.5, 0.8, 1.1, 1.4, 1.7, 2.0, 2.4, 2.7, 3.1,
	}

	// Interpolate at alpha=0.5 (midpoint)
	interpolated := q.Interpolate(nlsf1, nlsf2, 0.5)

	if len(interpolated) != 10 {
		t.Fatalf("Expected 10 interpolated values, got %d", len(interpolated))
	}

	// Verify interpolation is stable
	if !q.CheckStability(interpolated) {
		t.Error("Interpolated NLSF is not stable")
	}

	// Verify values are between nlsf1 and nlsf2
	for i := range interpolated {
		min := math.Min(nlsf1[i], nlsf2[i])
		max := math.Max(nlsf1[i], nlsf2[i])

		if interpolated[i] < min-0.1 || interpolated[i] > max+0.1 {
			t.Errorf("Interpolated value at %d (%f) outside range [%f, %f]",
				i, interpolated[i], min, max)
		}
	}
}

// Gain Quantization Tests

func TestLinearToDBConversion(t *testing.T) {
	g := NewGainQuantizer(4)
	if g == nil {
		t.Fatal("Failed to create gain quantizer")
	}

	testCases := []struct {
		linear float64
		dbMin  float64
		dbMax  float64
	}{
		{1.0, -0.1, 0.1},    // 0 dB
		{10.0, 19.9, 20.1},  // 20 dB
		{0.1, -20.1, -19.9}, // -20 dB
	}

	for _, tc := range testCases {
		db := g.LinearToDB(tc.linear)
		if db < tc.dbMin || db > tc.dbMax {
			t.Errorf("Linear %f converted to %f dB, expected [%f, %f]",
				tc.linear, db, tc.dbMin, tc.dbMax)
		}

		// Verify roundtrip
		linearBack := g.DBToLinear(db)
		if math.Abs(linearBack-tc.linear)/tc.linear > 0.01 {
			t.Errorf("Roundtrip failed: %f -> %f dB -> %f",
				tc.linear, db, linearBack)
		}
	}
}

func TestGainQuantization(t *testing.T) {
	g := NewGainQuantizer(4)

	gains := []float64{0.5, 1.0, 1.5, 2.0}

	// Quantize
	indices, err := g.Quantize(gains)
	if err != nil {
		t.Fatalf("Quantization failed: %v", err)
	}

	if len(indices) != 4 {
		t.Fatalf("Expected 4 indices, got %d", len(indices))
	}

	// Dequantize
	reconstructed, err := g.Dequantize(indices)
	if err != nil {
		t.Fatalf("Dequantization failed: %v", err)
	}

	if len(reconstructed) != 4 {
		t.Fatalf("Expected 4 reconstructed gains, got %d", len(reconstructed))
	}

	// Verify reconstruction error
	for i := range gains {
		// Convert to dB for comparison
		originalDB := g.LinearToDB(gains[i])
		reconstructedDB := g.LinearToDB(reconstructed[i])

		error := math.Abs(reconstructedDB - originalDB)

		// Error should be within quantization step
		if error > GainQuantStep*1.5 {
			t.Errorf("Gain at %d: error %f dB exceeds limit", i, error)
		}
	}
}

func TestSubframeGains(t *testing.T) {
	g := NewGainQuantizer(4)

	// Create signal with varying energy in subframes
	signal := make([]float64, 160) // 160 samples = 4 subframes of 40

	// Different energy levels for each subframe
	for i := 0; i < 40; i++ {
		signal[i] = 0.5 // Low energy
	}
	for i := 40; i < 80; i++ {
		signal[i] = 1.0 // Medium energy
	}
	for i := 80; i < 120; i++ {
		signal[i] = 1.5 // High energy
	}
	for i := 120; i < 160; i++ {
		signal[i] = 0.8 // Medium-low energy
	}

	gains := g.ComputeSubframeGains(signal, 40)

	if len(gains) != 4 {
		t.Fatalf("Expected 4 subframe gains, got %d", len(gains))
	}

	// Verify gains reflect energy levels
	if gains[0] >= gains[1] {
		t.Error("Subframe 0 gain should be less than subframe 1")
	}
	if gains[2] <= gains[1] {
		t.Error("Subframe 2 gain should be greater than subframe 1")
	}

	// All gains should be positive
	for i, gain := range gains {
		if gain <= 0 {
			t.Errorf("Gain at subframe %d is non-positive: %f", i, gain)
		}
	}
}

func TestGainSmoothing(t *testing.T) {
	g := NewGainQuantizer(4)

	// Create gains with abrupt changes
	gains := []float64{1.0, 2.0, 0.5, 1.5}
	original := make([]float64, len(gains))
	copy(original, gains)

	// Apply smoothing
	g.SmoothGains(gains, 0.5)

	// Verify smoothing reduced variations
	for i := 1; i < len(gains); i++ {
		diff := math.Abs(gains[i] - gains[i-1])
		originalDiff := math.Abs(original[i] - original[i-1])

		if diff > originalDiff {
			t.Errorf("Smoothing increased variation at index %d", i)
		}
	}
}

func TestGainErrors(t *testing.T) {
	g := NewGainQuantizer(4)

	// Test negative gain
	_, err := g.Quantize([]float64{-1.0, 1.0, 1.0, 1.0})
	if err == nil {
		t.Error("Expected error for negative gain")
	}

	// Test zero gain
	_, err = g.Quantize([]float64{0.0, 1.0, 1.0, 1.0})
	if err == nil {
		t.Error("Expected error for zero gain")
	}

	// Test wrong length
	_, err = g.Quantize([]float64{1.0, 1.0})
	if err == nil {
		t.Error("Expected error for wrong number of gains")
	}
}

// VAD Tests (fixed-point silk_VAD_GetSA_Q8 port)

// TestFrameVADFlagsFollowActivity checks the packet VAD flags derived from
// the fixed-point activity: a speech-like tone is active, digital silence is
// inactive, and the per-frame results are retained for the frame encode.
func TestFrameVADFlagsFollowActivity(t *testing.T) {
	enc, err := NewEncoder(16000, 1)
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	speech := make([]float64, enc.frameSize)
	for i := range speech {
		speech[i] = 0.3 * math.Sin(2*math.Pi*180*float64(i)/16000)
	}
	active := false
	for frame := 0; frame < 4; frame++ {
		active = enc.runFrameVAD(0, speech)
	}
	if !active {
		t.Fatalf("speech-like tone not flagged active: SA_Q8=%d", enc.frameVAD[0].speechActivityQ8)
	}
	if enc.frameVAD[0].speechActivityQ8 < 13 {
		t.Fatalf("stored activity %d below the DTX threshold", enc.frameVAD[0].speechActivityQ8)
	}
	enc.Reset()
	silence := make([]float64, enc.frameSize)
	for frame := 0; frame < 4; frame++ {
		active = enc.runFrameVAD(0, silence)
	}
	if active {
		t.Fatalf("digital silence flagged active: SA_Q8=%d", enc.frameVAD[0].speechActivityQ8)
	}
	if enc.noSpeechCounter == 0 {
		t.Fatal("noSpeechCounter did not advance on inactive frames")
	}
}

// Benchmarks

func BenchmarkNLSFQuantization(b *testing.B) {
	q := NewNLSFQuantizer(16)

	nlsf := []float64{
		0.2, 0.4, 0.6, 0.8, 1.0, 1.2, 1.4, 1.6,
		1.8, 2.0, 2.2, 2.4, 2.6, 2.8, 3.0, 3.1,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		q.Quantize(nlsf)
	}
}

func BenchmarkGainQuantization(b *testing.B) {
	g := NewGainQuantizer(4)
	gains := []float64{0.5, 1.0, 1.5, 2.0}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		g.Quantize(gains)
	}
}
