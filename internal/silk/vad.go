package silk

import (
	"github.com/darui3018823/opus/internal/dsp"
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
	// immediateAttack reports a live (post-hangover) active frame without waiting
	// for the majority-vote smoother to accumulate history, so a silence->speech
	// transition does not lose its first 1-2 attack frames. Enabled only for the
	// mono SILK-only path; the stereo multi-frame conditional-coding stream is
	// sensitive to VAD-flag changes (they shift the per-frame entropy context and
	// break decoder/libopus bit-conformance), so stereo keeps the smoothed flag.
	immediateAttack bool
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

	// Onset attack must not be delayed: when the current frame is active
	// (directly, or kept active by hangover) report active immediately rather
	// than waiting for the majority-vote smoother to accumulate enough history.
	// The history smoother only acts as an additional activity floor that fills
	// brief gaps in otherwise-active runs, so it can never *suppress* a live
	// onset frame. Without this, a silence->speech transition lost its first
	// 1-2 active frames (the attack), which were then coded as digital silence.
	if v.immediateAttack {
		return decision || v.smoothDecision()
	}
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

// computeSpectralFlatness computes a coarse spectral flatness measure using FFT.
// Flatness near 1.0 indicates energy spread across bands, near 0.0 indicates
// concentrated tonal energy.
func (v *VAD) computeSpectralFlatness(signal []float64) float64 {
	if len(signal) < 4 {
		return 1.0
	}

	cx := make([]dsp.Complex, len(signal))
	for i, s := range signal {
		cx[i] = dsp.Complex{Real: s}
	}
	bins := dsp.AnyFFT(cx)

	const subbands = 8
	var bandEnergy [subbands]float64
	totalEnergy := 0.0
	nyquistBin := len(signal)/2 + 1
	for k := 1; k < nyquistBin && k < len(bins); k++ {
		power := bins[k].Real*bins[k].Real + bins[k].Imag*bins[k].Imag
		band := (k - 1) * subbands / (nyquistBin - 1)
		if band >= subbands {
			band = subbands - 1
		}
		bandEnergy[band] += power
		totalEnergy += power
	}

	if totalEnergy <= 1e-12 {
		return 1.0
	}

	maxBandRatio := 0.0
	for _, energy := range bandEnergy {
		ratio := energy / totalEnergy
		if ratio > maxBandRatio {
			maxBandRatio = ratio
		}
	}

	flatness := 1.0 - maxBandRatio
	if flatness < 0 {
		return 0
	}
	if flatness > 1 {
		return 1
	}
	return flatness
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

	activityFloor := 0.75 * v.energyThreshold
	if activityFloor < VADEnergyThresholdMin {
		activityFloor = VADEnergyThresholdMin
	}
	if energy < activityFloor {
		return false
	}

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

	threshold := (v.historySize + 2) / 3
	return count >= threshold
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
