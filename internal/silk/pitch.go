package silk

import (
	"math"
)

// PitchAnalysis performs pitch analysis for SILK encoding.
// This is crucial for voice quality in low-bitrate speech coding.

// PitchAnalyzer finds the pitch period (fundamental frequency) in speech.
type PitchAnalyzer struct {
	sampleRate int
	minLag     int
	maxLag     int
	history    []float64 // Previous samples for correlation
}

// NewPitchAnalyzer creates a new pitch analyzer.
func NewPitchAnalyzer(sampleRate int) *PitchAnalyzer {
	return &PitchAnalyzer{
		sampleRate: sampleRate,
		minLag:     PitchLagMin,
		maxLag:     PitchLagMax,
		history:    make([]float64, PitchLagMax),
	}
}

// Analyze finds the pitch lag and gain using autocorrelation.
func (pa *PitchAnalyzer) Analyze(signal []float64) (lag int, gain float64) {
	n := len(signal)
	if n < pa.maxLag {
		return 0, 0.0
	}

	// Compute energy of signal
	energy := 0.0
	for i := 0; i < n; i++ {
		energy += signal[i] * signal[i]
	}
	if energy < 1e-10 {
		return 0, 0.0
	}

	// Find best lag using normalized cross-correlation
	bestLag := pa.minLag
	bestCorr := 0.0

	for lag := pa.minLag; lag <= pa.maxLag && lag < n; lag++ {
		// Compute correlation
		corr := 0.0
		lagEnergy := 0.0
		for i := lag; i < n; i++ {
			corr += signal[i] * signal[i-lag]
			lagEnergy += signal[i-lag] * signal[i-lag]
		}

		// Normalize correlation
		if lagEnergy > 1e-10 {
			normCorr := corr / math.Sqrt(energy*lagEnergy)
			if normCorr > bestCorr {
				bestCorr = normCorr
				bestLag = lag
			}
		}
	}

	// Compute gain at best lag
	gain = bestCorr
	if gain < 0.0 {
		gain = 0.0
	} else if gain > 1.0 {
		gain = 1.0
	}

	return bestLag, gain
}

// AnalyzeSubframes performs pitch analysis on multiple subframes.
func (pa *PitchAnalyzer) AnalyzeSubframes(signal []float64, numSubframes int) ([]int, []float64) {
	n := len(signal)
	subframeLen := n / numSubframes

	lags := make([]int, numSubframes)
	gains := make([]float64, numSubframes)

	for i := 0; i < numSubframes; i++ {
		start := i * subframeLen
		end := (i + 1) * subframeLen
		if end > n {
			end = n
		}

		subframe := signal[start:end]
		lags[i], gains[i] = pa.Analyze(subframe)
	}

	return lags, gains
}

// RefineP pitch performs sub-sample pitch refinement using interpolation.
func (pa *PitchAnalyzer) RefinePitch(signal []float64, coarseLag int) (fineLag float64, gain float64) {
	// Simple refinement around coarse lag
	// Full implementation would use parabolic interpolation
	
	if coarseLag < pa.minLag || coarseLag > pa.maxLag || coarseLag >= len(signal) {
		return float64(coarseLag), 0.0
	}

	// Check neighboring lags
	bestLag := float64(coarseLag)
	bestCorr := 0.0
	n := len(signal)

	for offset := -1.0; offset <= 1.0; offset += 0.5 {
		testLag := float64(coarseLag) + offset
		if testLag < float64(pa.minLag) || int(testLag) >= n {
			continue
		}

		// Compute correlation with interpolation
		corr := 0.0
		energy := 0.0
		count := 0

		for i := int(testLag) + 1; i < n; i++ {
			// Linear interpolation for fractional lag
			frac := testLag - math.Floor(testLag)
			idx := int(math.Floor(testLag))
			if idx >= 0 && idx+1 < i {
				interpVal := signal[i-idx-1]*(1.0-frac) + signal[i-idx]*frac
				corr += signal[i] * interpVal
				energy += interpVal * interpVal
				count++
			}
		}

		if energy > 1e-10 && count > 0 {
			normCorr := corr / math.Sqrt(energy*float64(count))
			if normCorr > bestCorr {
				bestCorr = normCorr
				bestLag = testLag
			}
		}
	}

	gain = bestCorr
	if gain < 0.0 {
		gain = 0.0
	} else if gain > 1.0 {
		gain = 1.0
	}

	return bestLag, gain
}

// ApplyPitchFilter applies pitch-based prediction filter.
func (pa *PitchAnalyzer) ApplyPitchFilter(signal []float64, lag int, gain float64) []float64 {
	n := len(signal)
	filtered := make([]float64, n)

	for i := 0; i < n; i++ {
		if i >= lag {
			filtered[i] = signal[i] - gain*signal[i-lag]
		} else {
			filtered[i] = signal[i]
		}
	}

	return filtered
}

// SynthesizePitch synthesizes signal using pitch prediction.
func (pa *PitchAnalyzer) SynthesizePitch(residual []float64, lag int, gain float64) []float64 {
	n := len(residual)
	signal := make([]float64, n)

	for i := 0; i < n; i++ {
		if i >= lag {
			signal[i] = residual[i] + gain*signal[i-lag]
		} else {
			signal[i] = residual[i]
		}
	}

	return signal
}

// UpdateHistory updates the pitch analysis history buffer.
func (pa *PitchAnalyzer) UpdateHistory(signal []float64) {
	// Keep last maxLag samples for next analysis
	n := len(signal)
	if n >= len(pa.history) {
		copy(pa.history, signal[n-len(pa.history):])
	} else {
		// Shift history and append new samples
		copy(pa.history, pa.history[n:])
		copy(pa.history[len(pa.history)-n:], signal)
	}
}
