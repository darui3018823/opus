package silk

import (
	"fmt"
	"math"
)

// Gain quantization for SILK codec
// Gains control the amplitude of the signal and are quantized in log domain (dB)

// GainQuantizer handles gain quantization and dequantization
type GainQuantizer struct {
	numSubframes int
}

// NewGainQuantizer creates a new gain quantizer
func NewGainQuantizer(numSubframes int) *GainQuantizer {
	if numSubframes < 1 || numSubframes > MaxSubframes {
		return nil
	}
	return &GainQuantizer{numSubframes: numSubframes}
}

// Quantize quantizes gain values (linear domain) to dB indices
func (g *GainQuantizer) Quantize(gains []float64) ([]int, error) {
	if len(gains) != g.numSubframes {
		return nil, fmt.Errorf("expected %d gains, got %d", g.numSubframes, len(gains))
	}

	indices := make([]int, g.numSubframes)

	for i, gain := range gains {
		if gain <= 0 {
			return nil, fmt.Errorf("gain must be positive, got %f", gain)
		}

		// Convert to dB
		gainDB := g.LinearToDB(gain)

		// Quantize to index
		indices[i] = g.quantizeDB(gainDB)
	}

	return indices, nil
}

// Dequantize reconstructs gain values from quantized indices
func (g *GainQuantizer) Dequantize(indices []int) ([]float64, error) {
	if len(indices) != g.numSubframes {
		return nil, fmt.Errorf("expected %d indices, got %d", g.numSubframes, len(indices))
	}

	gains := make([]float64, g.numSubframes)

	for i, index := range indices {
		// Dequantize index to dB
		gainDB := g.dequantizeDB(index)

		// Convert to linear
		gains[i] = g.DBToLinear(gainDB)
	}

	return gains, nil
}

// LinearToDB converts linear gain to dB
func (g *GainQuantizer) LinearToDB(linearGain float64) float64 {
	if linearGain <= 0 {
		return GainMinDB
	}
	db := 20.0 * math.Log10(linearGain)
	return g.clampDB(db)
}

// DBToLinear converts dB gain to linear
func (g *GainQuantizer) DBToLinear(dbGain float64) float64 {
	dbGain = g.clampDB(dbGain)
	return math.Pow(10.0, dbGain/20.0)
}

// QuantizeSubframeGains quantizes gains for all subframes with temporal prediction
func (g *GainQuantizer) QuantizeSubframeGains(gains []float64, prevGain float64) ([]int, error) {
	if len(gains) != g.numSubframes {
		return nil, fmt.Errorf("expected %d gains, got %d", g.numSubframes, len(gains))
	}

	indices := make([]int, g.numSubframes)
	predictedGain := prevGain

	for i, gain := range gains {
		if gain <= 0 {
			return nil, fmt.Errorf("gain must be positive at index %d, got %f", i, gain)
		}

		// Compute prediction error
		predError := gain / predictedGain
		predErrorDB := 20.0 * math.Log10(predError)

		// Quantize prediction error
		indices[i] = g.quantizeDB(predErrorDB)

		// Update prediction for next subframe
		quantErrorDB := g.dequantizeDB(indices[i])
		predictedGain = predictedGain * g.DBToLinear(quantErrorDB)
	}

	return indices, nil
}

// quantizeDB quantizes a dB value to an index
func (g *GainQuantizer) quantizeDB(dbValue float64) int {
	// Clamp to valid range
	dbValue = g.clampDB(dbValue)

	// Quantize with step size
	index := int(math.Round((dbValue - GainMinDB) / GainQuantStep))

	// Ensure within valid range
	maxIndex := int((GainMaxDB - GainMinDB) / GainQuantStep)
	if index < 0 {
		index = 0
	}
	if index > maxIndex {
		index = maxIndex
	}

	return index
}

// dequantizeDB dequantizes an index to a dB value
func (g *GainQuantizer) dequantizeDB(index int) float64 {
	return GainMinDB + float64(index)*GainQuantStep
}

// clampDB clamps a dB value to valid range
func (g *GainQuantizer) clampDB(dbValue float64) float64 {
	if dbValue < GainMinDB {
		return GainMinDB
	}
	if dbValue > GainMaxDB {
		return GainMaxDB
	}
	return dbValue
}

// ComputeSubframeGains computes gains for each subframe from residual energy
func (g *GainQuantizer) ComputeSubframeGains(residual []float64, subframeSize int) []float64 {
	if subframeSize <= 0 {
		return nil
	}

	gains := make([]float64, g.numSubframes)

	for i := 0; i < g.numSubframes; i++ {
		start := i * subframeSize
		end := start + subframeSize

		if end > len(residual) {
			end = len(residual)
		}

		if start >= len(residual) {
			gains[i] = 1.0
			continue
		}

		// Compute RMS energy
		energy := 0.0
		for j := start; j < end; j++ {
			energy += residual[j] * residual[j]
		}
		energy /= float64(end - start)

		// Convert to gain (sqrt of energy)
		gains[i] = math.Sqrt(energy)

		// Ensure minimum gain
		if gains[i] < 0.001 {
			gains[i] = 0.001
		}
	}

	return gains
}

// SmoothGains applies temporal smoothing to gain trajectory
func (g *GainQuantizer) SmoothGains(gains []float64, smoothingFactor float64) {
	if len(gains) < 2 || smoothingFactor <= 0 || smoothingFactor >= 1 {
		return
	}

	// Apply exponential smoothing
	for i := 1; i < len(gains); i++ {
		gains[i] = smoothingFactor*gains[i-1] + (1-smoothingFactor)*gains[i]
	}
}
