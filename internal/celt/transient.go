package celt

import (
	"math"
)

// TransientDetector detects transient events in audio signals
// Transients are sudden changes in signal energy that require special handling
type TransientDetector struct {
	mode          *Mode
	prevEnergy    float64   // Previous frame energy
	threshold     float64   // Detection threshold
	historyEnergy []float64 // Energy history for smoothing
}

// NewTransientDetector creates a new transient detector
func NewTransientDetector(mode *Mode) *TransientDetector {
	return &TransientDetector{
		mode:          mode,
		prevEnergy:    0,
		threshold:     3.0, // 3x energy increase = transient
		historyEnergy: make([]float64, 4),
	}
}

// Detect analyzes audio samples to detect transients
// Returns true if a transient is detected, and the transient position
func (td *TransientDetector) Detect(samples []float64) (bool, int) {
	if len(samples) == 0 {
		return false, 0
	}
	
	// Divide frame into analysis blocks
	blockSize := 64
	if blockSize > len(samples)/4 {
		blockSize = len(samples) / 4
	}
	if blockSize < 16 {
		blockSize = 16
	}
	
	numBlocks := len(samples) / blockSize
	if numBlocks < 2 {
		return false, 0
	}
	
	// Compute energy for each block
	blockEnergies := make([]float64, numBlocks)
	for i := 0; i < numBlocks; i++ {
		start := i * blockSize
		end := start + blockSize
		if end > len(samples) {
			end = len(samples)
		}
		
		energy := 0.0
		for j := start; j < end; j++ {
			energy += samples[j] * samples[j]
		}
		blockEnergies[i] = energy / float64(blockSize)
	}
	
	// Detect sudden energy increase
	maxRatio := 0.0
	transientPos := 0
	
	for i := 1; i < numBlocks; i++ {
		if blockEnergies[i-1] > 1e-10 {
			ratio := blockEnergies[i] / blockEnergies[i-1]
			if ratio > maxRatio {
				maxRatio = ratio
				transientPos = i * blockSize
			}
		}
	}
	
	// Check against historical average
	avgHistorical := td.computeHistoricalAverage()
	currentAvg := computeAverage(blockEnergies)
	
	historicalRatio := 1.0
	if avgHistorical > 1e-10 {
		historicalRatio = currentAvg / avgHistorical
	}
	
	// Update history
	td.updateHistory(currentAvg)
	
	// Transient if either instantaneous or historical ratio exceeds threshold
	isTransient := (maxRatio > td.threshold) || (historicalRatio > td.threshold*0.8)
	
	return isTransient, transientPos
}

// DetectAdvanced performs more sophisticated transient detection
// using multiple frequency bands
func (td *TransientDetector) DetectAdvanced(samples []float64, bandEnergies []float64) (bool, int) {
	// Basic detection
	basicTransient, pos := td.Detect(samples)
	
	// Check for transients in individual frequency bands
	bandTransient := false
	if len(bandEnergies) > 0 {
		// Look for sudden energy increases in high-frequency bands
		// (transients often have strong high-frequency content)
		numBands := len(bandEnergies)
		if numBands >= 3 {
			// Check top 1/3 of bands
			highBandStart := numBands * 2 / 3
			
			for i := highBandStart; i < numBands; i++ {
				if bandEnergies[i] > 1e-10 {
					// Compare to adjacent bands
					if i > 0 && bandEnergies[i] > bandEnergies[i-1]*2.0 {
						bandTransient = true
						break
					}
				}
			}
		}
	}
	
	return basicTransient || bandTransient, pos
}

// computeHistoricalAverage computes average of energy history
func (td *TransientDetector) computeHistoricalAverage() float64 {
	if len(td.historyEnergy) == 0 {
		return td.prevEnergy
	}
	
	sum := 0.0
	count := 0
	for _, e := range td.historyEnergy {
		if e > 0 {
			sum += e
			count++
		}
	}
	
	if count > 0 {
		return sum / float64(count)
	}
	return td.prevEnergy
}

// updateHistory updates the energy history buffer
func (td *TransientDetector) updateHistory(energy float64) {
	// Shift history
	for i := len(td.historyEnergy) - 1; i > 0; i-- {
		td.historyEnergy[i] = td.historyEnergy[i-1]
	}
	td.historyEnergy[0] = energy
	td.prevEnergy = energy
}

// computeAverage computes the average of a slice
func computeAverage(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	
	sum := 0.0
	for _, v := range values {
		sum += v
	}
	return sum / float64(len(values))
}

// Reset resets the detector state
func (td *TransientDetector) Reset() {
	td.prevEnergy = 0
	for i := range td.historyEnergy {
		td.historyEnergy[i] = 0
	}
}

// SetThreshold sets the detection threshold
func (td *TransientDetector) SetThreshold(threshold float64) {
	if threshold > 0 {
		td.threshold = threshold
	}
}

// ComputeTransientWeight computes a weighting factor based on transient strength
// Returns a value between 0.0 (no transient) and 1.0 (strong transient)
func (td *TransientDetector) ComputeTransientWeight(samples []float64) float64 {
	isTransient, _ := td.Detect(samples)
	
	if !isTransient {
		return 0.0
	}
	
	// Compute strength based on energy variation
	if len(samples) < 64 {
		return 0.5
	}
	
	// Split into two halves
	mid := len(samples) / 2
	
	energy1 := 0.0
	for i := 0; i < mid; i++ {
		energy1 += samples[i] * samples[i]
	}
	energy1 /= float64(mid)
	
	energy2 := 0.0
	for i := mid; i < len(samples); i++ {
		energy2 += samples[i] * samples[i]
	}
	energy2 /= float64(len(samples) - mid)
	
	// Compute ratio
	ratio := 1.0
	if energy1 > 1e-10 {
		ratio = energy2 / energy1
	}
	
	// Normalize to 0-1 range
	// ratio > 4.0 = full transient weight
	weight := math.Min(1.0, (ratio-1.0)/3.0)
	if weight < 0 {
		weight = 0
	}
	
	return weight
}
