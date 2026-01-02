package celt

import (
	"math"
)

// BitAllocation handles dynamic bit allocation across frequency bands
type BitAllocation struct {
	mode        *Mode
	targetBits  int   // Total bits available for this frame
	bandBits    []int // Bits allocated to each band
	fineEnergy  []int // Fine energy bits per band
	pulseCounts []int // Pulse counts per band (for PVQ)
}

// NewBitAllocation creates a new bit allocation instance
func NewBitAllocation(mode *Mode, targetBits int) *BitAllocation {
	return &BitAllocation{
		mode:        mode,
		targetBits:  targetBits,
		bandBits:    make([]int, mode.Bands.NumBands),
		fineEnergy:  make([]int, mode.Bands.NumBands),
		pulseCounts: make([]int, mode.Bands.NumBands),
	}
}

// Allocate performs bit allocation across all bands
// This implements a simplified version of CELT's rate allocation
func (ba *BitAllocation) Allocate(bandEnergies []float64) error {
	numBands := ba.mode.Bands.NumBands

	// Reserve bits for header and overhead
	overheadBits := 20 // Simplified overhead estimate
	availableBits := ba.targetBits - overheadBits
	if availableBits < 0 {
		availableBits = 0
	}

	// Compute band importance based on energy
	importance := make([]float64, numBands)
	totalImportance := 0.0

	for i := 0; i < numBands; i++ {
		// Log energy with floor to avoid log(0)
		energy := bandEnergies[i]
		if energy < 1e-10 {
			energy = 1e-10
		}

		// Importance is roughly proportional to log energy
		// Weighted by band size (larger bands get more weight)
		bandSize := float64(ba.mode.Bands.BandSizes[i])
		importance[i] = math.Log(energy) * math.Sqrt(bandSize)

		// Add a small bias to ensure all bands get at least some bits
		importance[i] += 1.0

		totalImportance += importance[i]
	}

	// Allocate coarse energy bits (fixed allocation)
	coarseEnergyBits := numBands * 4 // 4 bits per band for coarse energy
	remainingBits := availableBits - coarseEnergyBits
	if remainingBits < 0 {
		remainingBits = 0
	}

	// Distribute remaining bits proportionally to importance
	for i := 0; i < numBands; i++ {
		if totalImportance > 0 {
			proportion := importance[i] / totalImportance
			ba.bandBits[i] = int(float64(remainingBits) * proportion)
		} else {
			ba.bandBits[i] = remainingBits / numBands
		}

		// Ensure minimum allocation
		if ba.bandBits[i] < 0 {
			ba.bandBits[i] = 0
		}
	}

	// Split band bits into fine energy and PVQ pulses
	for i := 0; i < numBands; i++ {
		// Reserve 0-3 bits for fine energy
		fineEnergyBits := min(3, ba.bandBits[i]/4)
		ba.fineEnergy[i] = fineEnergyBits

		// Remaining bits go to PVQ pulses
		pvqBits := ba.bandBits[i] - fineEnergyBits

		// Convert bits to pulse count (simplified)
		// More bits = more pulses for higher fidelity
		bandSize := ba.mode.Bands.BandSizes[i]
		ba.pulseCounts[i] = computePulseCount(pvqBits, bandSize)
	}

	return nil
}

// computePulseCount converts bit budget to pulse count for PVQ
func computePulseCount(bits, bandSize int) int {
	if bits <= 0 {
		return 0
	}

	// Rough heuristic: more bits allow more pulses
	// The actual relationship depends on the PVQ codebook size
	// codebook_size = C(N+K-1, K) where N=bandSize, K=pulses

	// Start with a guess
	pulses := bits / 2
	if pulses < 1 {
		pulses = 1
	}

	// Limit pulses based on band size
	maxPulses := bandSize * 2
	if pulses > maxPulses {
		pulses = maxPulses
	}

	return pulses
}

// GetBandBits returns bits allocated to a specific band
func (ba *BitAllocation) GetBandBits(bandIdx int) int {
	if bandIdx < 0 || bandIdx >= len(ba.bandBits) {
		return 0
	}
	return ba.bandBits[bandIdx]
}

// GetPulseCount returns the pulse count for a specific band
func (ba *BitAllocation) GetPulseCount(bandIdx int) int {
	if bandIdx < 0 || bandIdx >= len(ba.pulseCounts) {
		return 0
	}
	return ba.pulseCounts[bandIdx]
}

// GetFineEnergy returns fine energy bits for a specific band
func (ba *BitAllocation) GetFineEnergy(bandIdx int) int {
	if bandIdx < 0 || bandIdx >= len(ba.fineEnergy) {
		return 0
	}
	return ba.fineEnergy[bandIdx]
}

// TotalAllocatedBits returns the total number of bits allocated
func (ba *BitAllocation) TotalAllocatedBits() int {
	total := 0
	for _, bits := range ba.bandBits {
		total += bits
	}
	return total
}

// RefineBitAllocation performs iterative refinement of bit allocation
// to better match the target bit rate
func (ba *BitAllocation) RefineBitAllocation() {
	target := ba.targetBits - 20 // Account for overhead
	current := ba.TotalAllocatedBits()

	// If we're close enough, we're done
	if abs(current-target) < 10 {
		return
	}

	// If we have too many bits, reduce from least important bands
	if current > target {
		excess := current - target
		for i := len(ba.bandBits) - 1; i >= 0 && excess > 0; i-- {
			reduction := min(excess, ba.bandBits[i]/2)
			ba.bandBits[i] -= reduction
			excess -= reduction
		}
	} else {
		// If we have too few bits, add to most important bands
		deficit := target - current
		for i := 0; i < len(ba.bandBits) && deficit > 0; i++ {
			addition := min(deficit, 10) // Add up to 10 bits at a time
			ba.bandBits[i] += addition
			deficit -= addition
		}
	}
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
