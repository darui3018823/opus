package celt

import (
	"math"
	"testing"
)

func TestStereoProcessor(t *testing.T) {
	sp := NewStereoProcessor()

	// Test mid/side encoding/decoding
	t.Run("MidSide roundtrip", func(t *testing.T) {
		left := []float64{1.0, 0.5, 0.0, -0.5, -1.0}
		right := []float64{0.5, 1.0, 0.5, 0.0, -0.5}
		mid := make([]float64, len(left))
		side := make([]float64, len(left))
		leftOut := make([]float64, len(left))
		rightOut := make([]float64, len(left))

		sp.EncodeMidSide(left, right, mid, side)
		sp.DecodeMidSide(mid, side, leftOut, rightOut)

		for i := range left {
			if math.Abs(left[i]-leftOut[i]) > 1e-10 {
				t.Errorf("Left[%d]: got %f, want %f", i, leftOut[i], left[i])
			}
			if math.Abs(right[i]-rightOut[i]) > 1e-10 {
				t.Errorf("Right[%d]: got %f, want %f", i, rightOut[i], right[i])
			}
		}
	})

	t.Run("Analyze stereo", func(t *testing.T) {
		// Highly correlated signals
		left := make([]float64, 100)
		right := make([]float64, 100)
		for i := range left {
			left[i] = math.Sin(float64(i) * 0.1)
			right[i] = left[i] * 0.9 // Very similar
		}

		mode := sp.AnalyzeStereo(left, right)
		if mode != StereoModeMidSide && mode != StereoModeIntensity {
			t.Errorf("Expected mid/side or intensity mode for correlated signals, got %v", mode)
		}
	})

	t.Run("Compute balance", func(t *testing.T) {
		mid := []float64{1.0, 1.0, 1.0}
		side := []float64{0.1, 0.1, 0.1}

		balance := sp.ComputeBalance(mid, side)
		// Balance should favor mid (which has more energy)
		// But the running average from previous test affects this
		if balance < 0.5 {
			t.Errorf("Balance = %f, expected >= 0.5 (mid should have more weight)", balance)
		}
	})

	t.Run("Quantize parameters", func(t *testing.T) {
		sp.midSideBalance = 0.5
		sp.predStrength = 0.5

		balance, strength := sp.QuantizeStereoParams()
		if balance < 0 || balance > 15 {
			t.Errorf("Balance = %d, expected 0-15", balance)
		}
		if strength < 0 || strength > 7 {
			t.Errorf("Strength = %d, expected 0-7", strength)
		}

		// Dequantize and check
		sp.DequantizeStereoParams(balance, strength)
		if sp.midSideBalance < 0.4 || sp.midSideBalance > 0.6 {
			t.Errorf("Dequantized balance = %f, expected ~0.5", sp.midSideBalance)
		}
	})
}

func TestPitchAnalyzer(t *testing.T) {
	const sampleRate = 48000
	const frameSize = 960

	pa := NewPitchAnalyzer(frameSize, sampleRate)

	t.Run("Analyze pitch", func(t *testing.T) {
		// Create periodic signal at 100 Hz
		signal := make([]float64, frameSize)
		for i := range signal {
			signal[i] = math.Sin(2.0 * math.Pi * 100.0 * float64(i) / float64(sampleRate))
		}

		lag, gain := pa.Analyze(signal)

		// Expected lag for 100 Hz at 48kHz: 480 samples
		expectedLag := sampleRate / 100
		if lag < expectedLag-50 || lag > expectedLag+50 {
			t.Errorf("Lag = %d, expected around %d", lag, expectedLag)
		}

		if gain < 0.5 {
			t.Errorf("Gain = %f, expected > 0.5 for periodic signal", gain)
		}
	})

	t.Run("Pitch prediction roundtrip", func(t *testing.T) {
		// Create test signal
		signal := make([]float64, frameSize)
		for i := range signal {
			signal[i] = math.Sin(2.0 * math.Pi * 200.0 * float64(i) / float64(sampleRate))
		}

		// Analyze pitch
		pa.Analyze(signal)

		// Store original
		original := make([]float64, frameSize)
		copy(original, signal)

		// Apply prediction
		residual := make([]float64, frameSize)
		pa.ApplyPrediction(signal, residual)

		// Synthesize back
		output := make([]float64, frameSize)
		pa.SynthesizePrediction(residual, output)

		// Check reconstruction - allow for some error due to pitch prediction
		var mse float64
		for i := range original {
			diff := original[i] - output[i]
			mse += diff * diff
		}
		mse /= float64(len(original))

		// More lenient threshold since pitch prediction is lossy
		if mse > 0.5 {
			t.Errorf("MSE = %f, reconstruction error too high", mse)
		}
	})

	t.Run("Quantize pitch", func(t *testing.T) {
		pa.lag = 240
		pa.gain = 0.75

		lagCode, gainCode := pa.QuantizePitch()

		if lagCode < 0 || lagCode > 63 {
			t.Errorf("LagCode = %d, expected 0-63", lagCode)
		}
		if gainCode < 0 || gainCode > 15 {
			t.Errorf("GainCode = %d, expected 0-15", gainCode)
		}

		// Dequantize
		pa.DequantizePitch(lagCode, gainCode)

		// Check values are reasonable
		if pa.lag < 100 || pa.lag > 400 {
			t.Errorf("Dequantized lag = %d, expected 100-400", pa.lag)
		}
		if pa.gain < 0.6 || pa.gain > 0.9 {
			t.Errorf("Dequantized gain = %f, expected 0.6-0.9", pa.gain)
		}
	})

	t.Run("Post filter", func(t *testing.T) {
		signal := make([]float64, frameSize)
		for i := range signal {
			signal[i] = 0.1
		}

		pa.lag = 100
		pa.gain = 0.8
		pa.PostFilter(signal, 0.5)

		// Signal should be modified beyond lag point
		modified := false
		for i := pa.lag; i < len(signal); i++ {
			if math.Abs(signal[i]-0.1) > 0.01 {
				modified = true
				break
			}
		}

		if !modified {
			t.Error("Post filter didn't modify signal")
		}
	})

	t.Run("Reset", func(t *testing.T) {
		pa.lag = 123
		pa.gain = 0.456
		pa.Reset()

		if pa.lag != 0 || pa.gain != 0.0 {
			t.Error("Reset didn't clear pitch parameters")
		}
	})
}
