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

// Allocate performs bit allocation across all bands.
// Allocation is proportional to sqrt(bandSize) so that both encoder and
// decoder compute identical pulse counts given the same total-bit budget.
// Energy is still encoded separately; it is not used for allocation here
// to ensure bit-exact agreement between encoder and decoder.
func (ba *BitAllocation) Allocate(bandEnergies []float64) error {
	numBands := ba.mode.Bands.NumBands

	// Reserve bits for header overhead
	const overheadBits = 20
	availableBits := ba.targetBits - overheadBits
	if availableBits < 0 {
		availableBits = 0
	}

	// Reserve fixed bits for coarse energy coding
	coarseEnergyBits := numBands * 4
	remainingBits := availableBits - coarseEnergyBits
	if remainingBits < 0 {
		remainingBits = 0
	}

	// Allocate proportionally to sqrt(bandSize) — energy-independent so that
	// encoder and decoder always compute the same result from the same budget.
	totalWeight := 0.0
	weights := make([]float64, numBands)
	for i := 0; i < numBands; i++ {
		weights[i] = math.Sqrt(float64(ba.mode.Bands.BandSizes[i]))
		totalWeight += weights[i]
	}

	for i := 0; i < numBands; i++ {
		if totalWeight > 0 {
			ba.bandBits[i] = int(float64(remainingBits) * weights[i] / totalWeight)
		} else {
			ba.bandBits[i] = remainingBits / numBands
		}
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

// computePulseCount converts a bit budget to the largest pulse count k
// such that the PVQ codebook V(bandSize, k) can be indexed with at most
// the given number of bits.  Uses the correct CWRS codebook size.
func computePulseCount(bits, bandSize int) int {
	if bits <= 0 || bandSize <= 0 {
		return 0
	}

	maxIndex := uint32(1) << uint(bits)

	// Find the largest k where V(bandSize, k) <= maxIndex
	k := 0
	for {
		next := cwrsV(bandSize, k+1)
		if next == 0 || next > maxIndex {
			break
		}
		k++
		// Safety cap to avoid runaway iteration on very large bit budgets
		if k >= bandSize*4 {
			break
		}
	}

	return k
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
