package silk

import (
	"math"
)

// VAD (Voice Activity Detection) detects presence of speech vs silence/noise
// Uses multiple metrics: energy, spectral flatness, zero crossing rate

// VAD represents a voice activity detector
type VAD struct {
	history         []bool  // Detection history for smoothing
	historySize     int     // Size of history buffer
	energyThreshold float64 // Adaptive energy threshold
	hangoverFrames  int     // Number of frames to keep active after speech
	hangoverCount   int     // Current hangover counter
}

// NewVAD creates a new voice activity detector
func NewVAD() *VAD {
	return &VAD{
		history:         make([]bool, VADHistorySize),
		historySize:     VADHistorySize,
		energyThreshold: VADEnergyThresholdDefault,
		hangoverFrames:  VADHangoverFrames,
		hangoverCount:   0,
	}
}

// Detect detects voice activity in a signal frame
func (v *VAD) Detect(signal []float64) bool {
	if len(signal) == 0 {
		return false
	}

	// Compute multiple metrics
	energy := v.computeEnergy(signal)
	spectralFlatness := v.computeSpectralFlatness(signal)
	zeroCrossingRate := v.computeZeroCrossingRate(signal)

	// Decision based on multiple metrics
	decision := v.makeDecision(energy, spectralFlatness, zeroCrossingRate)

	// Apply hangover logic
	if decision {
		v.hangoverCount = v.hangoverFrames
	} else if v.hangoverCount > 0 {
		v.hangoverCount--
		decision = true
	}

	// Update history
	v.updateHistory(decision)

	// Apply history-based smoothing
	return v.smoothDecision()
}

// computeEnergy computes signal energy
func (v *VAD) computeEnergy(signal []float64) float64 {
	energy := 0.0
	for _, sample := range signal {
		energy += sample * sample
	}
	return energy / float64(len(signal))
}

// computeSpectralFlatness computes spectral flatness measure
// Flatness near 1.0 indicates noise, near 0.0 indicates tonal (speech)
func (v *VAD) computeSpectralFlatness(signal []float64) float64 {
	// Compute magnitude spectrum (simplified - using time domain as proxy)
	magnitudes := make([]float64, len(signal))
	for i := range signal {
		magnitudes[i] = math.Abs(signal[i]) + 1e-10 // Add epsilon to avoid log(0)
	}

	// Geometric mean
	geometricMean := 0.0
	for _, mag := range magnitudes {
		geometricMean += math.Log(mag)
	}
	geometricMean = math.Exp(geometricMean / float64(len(magnitudes)))

	// Arithmetic mean
	arithmeticMean := 0.0
	for _, mag := range magnitudes {
		arithmeticMean += mag
	}
	arithmeticMean /= float64(len(magnitudes))

	// Spectral flatness
	if arithmeticMean <= 0 {
		return 1.0
	}
	return geometricMean / arithmeticMean
}

// computeZeroCrossingRate computes zero crossing rate
// High ZCR indicates unvoiced speech or noise
func (v *VAD) computeZeroCrossingRate(signal []float64) float64 {
	if len(signal) < 2 {
		return 0.0
	}

	crossings := 0
	for i := 1; i < len(signal); i++ {
		if (signal[i] >= 0 && signal[i-1] < 0) || (signal[i] < 0 && signal[i-1] >= 0) {
			crossings++
		}
	}

	return float64(crossings) / float64(len(signal)-1)
}

// makeDecision makes VAD decision based on multiple metrics
func (v *VAD) makeDecision(energy, spectralFlatness, zeroCrossingRate float64) bool {
	// Energy-based decision (primary)
	energyDecision := energy > v.energyThreshold

	// Spectral flatness indicates speech (low flatness = tonal)
	spectralDecision := spectralFlatness < VADSpectralFlatnessThreshold

	// Zero crossing rate helps distinguish voiced speech
	zcrDecision := zeroCrossingRate < VADZeroCrossingThreshold

	// Weighted combination
	// Energy is most important, spectral flatness is secondary, ZCR is tertiary
	score := 0.0
	if energyDecision {
		score += 0.5
	}
	if spectralDecision {
		score += 0.3
	}
	if zcrDecision {
		score += 0.2
	}

	// Update adaptive threshold based on energy
	v.updateThreshold(energy)

	return score >= 0.4 // Slightly lower threshold for better sensitivity
}

// updateThreshold updates the adaptive energy threshold
func (v *VAD) updateThreshold(currentEnergy float64) {
	// Exponential moving average
	alpha := 0.1
	v.energyThreshold = alpha*currentEnergy + (1-alpha)*v.energyThreshold

	// Ensure minimum threshold
	if v.energyThreshold < VADEnergyThresholdMin {
		v.energyThreshold = VADEnergyThresholdMin
	}
}

// updateHistory updates detection history
func (v *VAD) updateHistory(decision bool) {
	// Shift history
	for i := 0; i < v.historySize-1; i++ {
		v.history[i] = v.history[i+1]
	}
	v.history[v.historySize-1] = decision
}

// smoothDecision applies smoothing based on history
func (v *VAD) smoothDecision() bool {
	// Count positive detections in history
	count := 0
	for _, h := range v.history {
		if h {
			count++
		}
	}

	// Majority vote
	return count > v.historySize/2
}

// Reset resets VAD state
func (v *VAD) Reset() {
	for i := range v.history {
		v.history[i] = false
	}
	v.hangoverCount = 0
	v.energyThreshold = VADEnergyThresholdDefault
}

// GetEnergyThreshold returns current energy threshold
func (v *VAD) GetEnergyThreshold() float64 {
	return v.energyThreshold
}

// SetEnergyThreshold sets the energy threshold
func (v *VAD) SetEnergyThreshold(threshold float64) {
	if threshold > 0 {
		v.energyThreshold = threshold
	}
}
