package silk

import (
	"testing"
)

func TestLPCAnalysis(t *testing.T) {
	// Create test signal (simple sine wave)
	signal := make([]float64, 160) // 20ms at 8kHz
	for i := range signal {
		signal[i] = 0.5
	}

	lpc := NewLPCAnalysis(LPCOrderNB)
	err := lpc.Analyze(signal)
	if err != nil {
		t.Fatalf("LPC analysis failed: %v", err)
	}

	coeffs := lpc.GetCoefficients()
	if len(coeffs) != LPCOrderNB {
		t.Errorf("Expected %d coefficients, got %d", LPCOrderNB, len(coeffs))
	}

	gain := lpc.GetGain()
	if gain <= 0 {
		t.Errorf("Expected positive gain, got %f", gain)
	}
}

func TestLPCResidual(t *testing.T) {
	// Create test signal
	signal := make([]float64, 160)
	for i := range signal {
		signal[i] = float64(i % 10) / 10.0
	}

	lpc := NewLPCAnalysis(LPCOrderNB)
	err := lpc.Analyze(signal)
	if err != nil {
		t.Fatalf("LPC analysis failed: %v", err)
	}

	// Compute residual
	residual := lpc.ComputeResidual(signal)
	if len(residual) != len(signal) {
		t.Errorf("Residual length mismatch: expected %d, got %d", len(signal), len(residual))
	}

	// Synthesize back
	synthesized := lpc.Synthesize(residual)
	if len(synthesized) != len(signal) {
		t.Errorf("Synthesized length mismatch: expected %d, got %d", len(signal), len(synthesized))
	}
}

func TestPitchAnalysis(t *testing.T) {
	// Create periodic signal
	signal := make([]float64, 320) // 40ms at 8kHz
	period := 40                   // 200 Hz pitch
	for i := range signal {
		signal[i] = 0.5
		if i%period < period/2 {
			signal[i] = 1.0
		}
	}

	pa := NewPitchAnalyzer(SampleRate8kHz)
	lag, gain := pa.Analyze(signal)

	if lag < PitchLagMin || lag > PitchLagMax {
		t.Errorf("Pitch lag %d out of range [%d, %d]", lag, PitchLagMin, PitchLagMax)
	}

	if gain < 0.0 || gain > 1.0 {
		t.Errorf("Pitch gain %f out of range [0, 1]", gain)
	}

	t.Logf("Detected pitch: lag=%d, gain=%.3f", lag, gain)
}

func TestPitchSubframes(t *testing.T) {
	// Create signal
	signal := make([]float64, 320)
	for i := range signal {
		signal[i] = float64(i%50) / 50.0
	}

	pa := NewPitchAnalyzer(SampleRate8kHz)
	lags, gains := pa.AnalyzeSubframes(signal, PitchSubframes)

	if len(lags) != PitchSubframes {
		t.Errorf("Expected %d lags, got %d", PitchSubframes, len(lags))
	}

	if len(gains) != PitchSubframes {
		t.Errorf("Expected %d gains, got %d", PitchSubframes, len(gains))
	}

	for i := 0; i < PitchSubframes; i++ {
		if lags[i] < 0 {
			t.Errorf("Subframe %d: negative lag %d", i, lags[i])
		}
		if gains[i] < 0.0 || gains[i] > 1.0 {
			t.Errorf("Subframe %d: gain %f out of range", i, gains[i])
		}
	}
}

func TestPitchFilter(t *testing.T) {
	signal := make([]float64, 160)
	for i := range signal {
		signal[i] = 1.0
	}

	pa := NewPitchAnalyzer(SampleRate8kHz)
	lag := 40
	gain := 0.5

	filtered := pa.ApplyPitchFilter(signal, lag, gain)
	if len(filtered) != len(signal) {
		t.Errorf("Filtered length mismatch")
	}

	// Synthesize back
	synthesized := pa.SynthesizePitch(filtered, lag, gain)
	if len(synthesized) != len(signal) {
		t.Errorf("Synthesized length mismatch")
	}
}

func TestLSFConversion(t *testing.T) {
	// Create LPC analyzer
	lpc := NewLPCAnalysis(LPCOrderNB)
	
	// Create simple test signal
	signal := make([]float64, 160)
	for i := range signal {
		signal[i] = 0.5
	}
	
	err := lpc.Analyze(signal)
	if err != nil {
		t.Fatalf("LPC analysis failed: %v", err)
	}

	// Convert to LSF
	lsf := lpc.ToLSF()
	if len(lsf) != LPCOrderNB {
		t.Errorf("Expected %d LSF values, got %d", LPCOrderNB, len(lsf))
	}

	// Check LSF ordering (should be monotonically increasing)
	for i := 1; i < len(lsf); i++ {
		if lsf[i] <= lsf[i-1] {
			t.Errorf("LSF not monotonically increasing at index %d: %f <= %f", i, lsf[i], lsf[i-1])
		}
	}
}

func BenchmarkLPCAnalysis(b *testing.B) {
	signal := make([]float64, 160)
	for i := range signal {
		signal[i] = float64(i) / 160.0
	}

	lpc := NewLPCAnalysis(LPCOrderNB)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = lpc.Analyze(signal)
	}
}

func BenchmarkPitchAnalysis(b *testing.B) {
	signal := make([]float64, 320)
	for i := range signal {
		signal[i] = float64(i%50) / 50.0
	}

	pa := NewPitchAnalyzer(SampleRate8kHz)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = pa.Analyze(signal)
	}
}
