package celt

import (
	"math"
)

// StereoMode represents different stereo coding modes
type StereoMode int

const (
	// StereoModeLeftRight - Independent left and right channels
	StereoModeLeftRight StereoMode = iota
	// StereoModeMidSide - Mid/side (sum/difference) coding
	StereoModeMidSide
	// StereoModeIntensity - Intensity stereo (share coefficients)
	StereoModeIntensity
)

// StereoProcessor handles stereo encoding/decoding operations
type StereoProcessor struct {
	// Stereo prediction state
	predStrength float64
	// Mid/side balance
	midSideBalance float64
}

// NewStereoProcessor creates a new stereo processor
func NewStereoProcessor() *StereoProcessor {
	return &StereoProcessor{
		predStrength:   0.0,
		midSideBalance: 0.5,
	}
}

// AnalyzeStereo analyzes stereo signal and determines optimal coding mode
func (sp *StereoProcessor) AnalyzeStereo(left, right []float64) StereoMode {
	if len(left) != len(right) || len(left) == 0 {
		return StereoModeLeftRight
	}

	// Calculate correlation between channels
	var sumLR, sumLL, sumRR float64
	for i := 0; i < len(left); i++ {
		sumLR += left[i] * right[i]
		sumLL += left[i] * left[i]
		sumRR += right[i] * right[i]
	}

	// Normalize to get correlation coefficient
	denom := math.Sqrt(sumLL * sumRR)
	if denom < 1e-10 {
		return StereoModeLeftRight
	}

	correlation := sumLR / denom

	// High correlation (>0.85) suggests mid/side coding
	if correlation > 0.85 {
		return StereoModeMidSide
	}

	// Very high correlation (>0.95) could use intensity stereo
	if correlation > 0.95 {
		return StereoModeIntensity
	}

	return StereoModeLeftRight
}

// EncodeMidSide converts left/right to mid/side representation
// Mid = (L + R) / sqrt(2), Side = (L - R) / sqrt(2)
func (sp *StereoProcessor) EncodeMidSide(left, right, mid, side []float64) {
	invSqrt2 := 1.0 / math.Sqrt(2.0)
	for i := 0; i < len(left); i++ {
		mid[i] = (left[i] + right[i]) * invSqrt2
		side[i] = (left[i] - right[i]) * invSqrt2
	}
}

// DecodeMidSide converts mid/side back to left/right
// L = (Mid + Side) / sqrt(2), R = (Mid - Side) / sqrt(2)
func (sp *StereoProcessor) DecodeMidSide(mid, side, left, right []float64) {
	invSqrt2 := 1.0 / math.Sqrt(2.0)
	for i := 0; i < len(mid); i++ {
		left[i] = (mid[i] + side[i]) * invSqrt2
		right[i] = (mid[i] - side[i]) * invSqrt2
	}
}

// ComputeBalance computes the mid/side balance parameter
// Returns value in [0, 1] where 0.5 is neutral
func (sp *StereoProcessor) ComputeBalance(mid, side []float64) float64 {
	var midEnergy, sideEnergy float64
	for i := 0; i < len(mid); i++ {
		midEnergy += mid[i] * mid[i]
		sideEnergy += side[i] * side[i]
	}

	totalEnergy := midEnergy + sideEnergy
	if totalEnergy < 1e-10 {
		return 0.5
	}

	// Balance parameter: ratio of mid energy to total
	balance := midEnergy / totalEnergy
	
	// Update running average
	sp.midSideBalance = 0.9*sp.midSideBalance + 0.1*balance
	
	return sp.midSideBalance
}

// Decorrelate applies decorrelation to stereo channels
// This reduces redundancy between L and R channels
func (sp *StereoProcessor) Decorrelate(left, right []float64, strength float64) {
	// Strength in [0, 1]: 0 = no decorrelation, 1 = full decorrelation
	if strength < 0.01 || len(left) != len(right) {
		return
	}

	// Simple time-domain decorrelation using prediction
	for i := 1; i < len(left); i++ {
		// Predict right from left
		pred := strength * left[i]
		right[i] -= pred
	}

	sp.predStrength = strength
}

// Correlate reverses the decorrelation (for decoder)
func (sp *StereoProcessor) Correlate(left, right []float64) {
	if sp.predStrength < 0.01 || len(left) != len(right) {
		return
	}

	// Reverse the prediction
	for i := 1; i < len(left); i++ {
		pred := sp.predStrength * left[i]
		right[i] += pred
	}
}

// IntensityStereo applies intensity stereo coding
// Shares magnitude across channels, only encodes phase difference
func (sp *StereoProcessor) IntensityStereo(left, right []float64, threshold int) {
	// For high frequency bands above threshold, share coefficients
	for i := threshold; i < len(left) && i < len(right); i++ {
		// Use average magnitude
		mag := 0.5 * (math.Abs(left[i]) + math.Abs(right[i]))
		
		// Preserve signs
		if left[i] < 0 {
			left[i] = -mag
		} else {
			left[i] = mag
		}
		
		if right[i] < 0 {
			right[i] = -mag
		} else {
			right[i] = mag
		}
	}
}

// QuantizeStereoParams quantizes stereo parameters for encoding
func (sp *StereoProcessor) QuantizeStereoParams() (balance, strength int) {
	// Quantize balance to 4 bits (0-15)
	balance = int(sp.midSideBalance * 15.0)
	if balance < 0 {
		balance = 0
	}
	if balance > 15 {
		balance = 15
	}

	// Quantize strength to 3 bits (0-7)
	strength = int(sp.predStrength * 7.0)
	if strength < 0 {
		strength = 0
	}
	if strength > 7 {
		strength = 7
	}

	return balance, strength
}

// DequantizeStereoParams dequantizes stereo parameters from bitstream
func (sp *StereoProcessor) DequantizeStereoParams(balance, strength int) {
	sp.midSideBalance = float64(balance) / 15.0
	sp.predStrength = float64(strength) / 7.0
}
