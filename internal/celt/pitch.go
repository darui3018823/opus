package celt

import (
	"math"
)

// PitchAnalyzer performs pitch detection and prediction
type PitchAnalyzer struct {
	// Pitch lag (in samples)
	lag int
	// Pitch gain (prediction strength)
	gain float64
	// Previous frame buffer for pitch prediction
	prevFrame []float64
	// Sample rate
	sampleRate int
}

// NewPitchAnalyzer creates a new pitch analyzer
func NewPitchAnalyzer(frameSize, sampleRate int) *PitchAnalyzer {
	return &PitchAnalyzer{
		lag:        0,
		gain:       0.0,
		prevFrame:  make([]float64, frameSize),
		sampleRate: sampleRate,
	}
}

// Analyze detects pitch lag and gain from input signal
func (pa *PitchAnalyzer) Analyze(input []float64) (lag int, gain float64) {
	if len(input) < 64 {
		return 0, 0.0
	}

	// Pitch search range: 50-500 Hz corresponds to lags
	minLag := pa.sampleRate / 500 // High pitch limit
	maxLag := pa.sampleRate / 50  // Low pitch limit

	if minLag < 16 {
		minLag = 16
	}
	if maxLag > len(input)/2 {
		maxLag = len(input) / 2
	}

	bestLag := 0
	bestCorr := 0.0

	// Autocorrelation-based pitch detection
	for testLag := minLag; testLag <= maxLag; testLag++ {
		var corr, energy float64
		
		// Compute normalized correlation
		for i := 0; i < len(input)-testLag; i++ {
			corr += input[i] * input[i+testLag]
			energy += input[i+testLag] * input[i+testLag]
		}

		// Normalize by energy
		if energy > 1e-10 {
			corr /= math.Sqrt(energy)
		}

		if corr > bestCorr {
			bestCorr = corr
			bestLag = testLag
		}
	}

	// Pitch gain is the correlation value (limited to [0, 1])
	bestGain := bestCorr
	if bestGain > 0.95 {
		bestGain = 0.95 // Limit to avoid instability
	}
	if bestGain < 0.0 {
		bestGain = 0.0
	}

	pa.lag = bestLag
	pa.gain = bestGain

	return bestLag, bestGain
}

// ApplyPrediction applies pitch prediction to remove redundancy
// This is used in the encoder
func (pa *PitchAnalyzer) ApplyPrediction(input, residual []float64) {
	if pa.gain < 0.01 || pa.lag == 0 || len(input) < pa.lag {
		copy(residual, input)
		return
	}

	// Compute residual = input - gain * input[lag samples ago]
	for i := 0; i < len(input); i++ {
		if i >= pa.lag {
			residual[i] = input[i] - pa.gain*input[i-pa.lag]
		} else {
			// Use previous frame for initial samples
			if i < len(pa.prevFrame) && pa.lag-i < len(pa.prevFrame) {
				residual[i] = input[i] - pa.gain*pa.prevFrame[len(pa.prevFrame)-(pa.lag-i)]
			} else {
				residual[i] = input[i]
			}
		}
	}

	// Store current frame for next prediction
	copy(pa.prevFrame, input)
}

// SynthesizePrediction synthesizes signal from residual using pitch prediction
// This is used in the decoder
func (pa *PitchAnalyzer) SynthesizePrediction(residual, output []float64) {
	if pa.gain < 0.01 || pa.lag == 0 || len(residual) < pa.lag {
		copy(output, residual)
		return
	}

	// Synthesize: output = residual + gain * output[lag samples ago]
	for i := 0; i < len(residual); i++ {
		if i >= pa.lag {
			output[i] = residual[i] + pa.gain*output[i-pa.lag]
		} else {
			// Use previous frame for initial samples
			if i < len(pa.prevFrame) && pa.lag-i < len(pa.prevFrame) {
				output[i] = residual[i] + pa.gain*pa.prevFrame[len(pa.prevFrame)-(pa.lag-i)]
			} else {
				output[i] = residual[i]
			}
		}
	}

	// Store synthesized frame for next prediction
	copy(pa.prevFrame, output)
}

// QuantizePitch quantizes pitch parameters for encoding
func (pa *PitchAnalyzer) QuantizePitch() (lagCode, gainCode int) {
	// Quantize lag with logarithmic scale (6 bits = 64 values)
	minLag := pa.sampleRate / 500
	maxLag := pa.sampleRate / 50
	if pa.lag < minLag {
		pa.lag = minLag
	}
	if pa.lag > maxLag {
		pa.lag = maxLag
	}

	// Map lag to 0-63
	lagRange := maxLag - minLag
	if lagRange > 0 {
		lagCode = ((pa.lag - minLag) * 63) / lagRange
	} else {
		lagCode = 0
	}
	if lagCode < 0 {
		lagCode = 0
	}
	if lagCode > 63 {
		lagCode = 63
	}

	// Quantize gain to 4 bits (16 values)
	gainCode = int(pa.gain * 15.0)
	if gainCode < 0 {
		gainCode = 0
	}
	if gainCode > 15 {
		gainCode = 15
	}

	return lagCode, gainCode
}

// DequantizePitch dequantizes pitch parameters from bitstream
func (pa *PitchAnalyzer) DequantizePitch(lagCode, gainCode int) {
	// Dequantize lag
	minLag := pa.sampleRate / 500
	maxLag := pa.sampleRate / 50
	lagRange := maxLag - minLag

	pa.lag = minLag + (lagCode*lagRange)/63
	if pa.lag < minLag {
		pa.lag = minLag
	}
	if pa.lag > maxLag {
		pa.lag = maxLag
	}

	// Dequantize gain
	pa.gain = float64(gainCode) / 15.0
	if pa.gain > 0.95 {
		pa.gain = 0.95
	}
}

// PostFilter applies post-filtering to decoded signal
// This enhances perceptual quality
func (pa *PitchAnalyzer) PostFilter(signal []float64, strength float64) {
	if pa.gain < 0.01 || pa.lag == 0 || strength < 0.01 {
		return
	}

	// Apply gentle pitch-based filtering
	effectiveGain := pa.gain * strength

	for i := pa.lag; i < len(signal); i++ {
		// Enhance periodicity
		signal[i] += effectiveGain * 0.3 * signal[i-pa.lag]
	}
}

// Reset resets the pitch analyzer state
func (pa *PitchAnalyzer) Reset() {
	pa.lag = 0
	pa.gain = 0.0
	for i := range pa.prevFrame {
		pa.prevFrame[i] = 0
	}
}
