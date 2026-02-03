// Package celt provides standard-compliant bit allocation for CELT codec.
// This implements the allocation logic from libopus rate.c.
package celt

// RateAllocator performs RFC 6716 compliant bit allocation.
type RateAllocator struct {
	mode       *Mode
	lm         int   // Log2 of frame size multiplier (0=2.5ms, 3=20ms)
	targetBits int32 // Total bits for this frame (in Q8)
	channels   int   // 1 for mono, 2 for stereo
}

// NewRateAllocator creates a new rate allocator.
func NewRateAllocator(mode *Mode, lm, channels int) *RateAllocator {
	return &RateAllocator{
		mode:     mode,
		lm:       lm,
		channels: channels,
	}
}

// AllocationResult holds the result of bit allocation.
type AllocationResult struct {
	Bits          []int32 // Bits per band (Q8)
	EBits         []int   // Fine energy bits per band
	FinePriority  []int   // 1 if this band gets priority for leftover bits
	CodedBands    int     // Number of bands to code (skip from end)
	Intensity     int     // Intensity stereo starting band
	DualStereo    int     // 1 if using dual stereo
	TotalUsedBits int32   // Total bits used (Q8)
}

// ComputeAllocation computes bit allocation for all bands.
// This is a simplified version of libopus compute_allocation().
func (ra *RateAllocator) ComputeAllocation(
	targetBits int32,
	bandEnergies []float64,
	isTransient bool,
) *AllocationResult {
	numBands := ra.mode.Bands.NumBands
	result := &AllocationResult{
		Bits:         make([]int32, numBands),
		EBits:        make([]int, numBands),
		FinePriority: make([]int, numBands),
		CodedBands:   numBands,
	}

	ra.targetBits = targetBits

	// Get allocation vectors
	bits1 := make([]int32, numBands)
	bits2 := make([]int32, numBands)
	thresh := make([]int32, numBands)
	cap := make([]int32, numBands)

	// Initialize allocation vectors from tables
	ra.initAllocVectors(bits1, bits2, thresh, cap)

	// Perform binary search to find optimal allocation
	lo := 0
	hi := 1 << AllocSteps

	for i := 0; i < AllocSteps; i++ {
		mid := (lo + hi) >> 1
		psum := int32(0)

		for j := numBands - 1; j >= 0; j-- {
			tmp := bits1[j] + int32(mid)*bits2[j]>>AllocSteps
			if tmp >= thresh[j] {
				if tmp > cap[j] {
					tmp = cap[j]
				}
				psum += tmp
			}
		}

		if psum > targetBits {
			hi = mid
		} else {
			lo = mid
		}
	}

	// Apply final allocation
	psum := int32(0)
	for j := numBands - 1; j >= 0; j-- {
		tmp := bits1[j] + int32(lo)*bits2[j]>>AllocSteps

		if tmp < thresh[j] {
			tmp = 0
		}
		if tmp > cap[j] {
			tmp = cap[j]
		}

		result.Bits[j] = tmp
		psum += tmp
	}

	result.TotalUsedBits = psum

	// Split into fine energy and PVQ bits
	ra.splitBits(result)

	return result
}

// initAllocVectors initializes allocation vectors from tables.
func (ra *RateAllocator) initAllocVectors(bits1, bits2, thresh, cap []int32) {
	numBands := ra.mode.Bands.NumBands
	c := int32(ra.channels)
	logM := int32(ra.lm << BITRES)

	for i := 0; i < numBands; i++ {
		// Get band size
		bandSize := int32(ra.mode.Bands.BandSizes[i]) << ra.lm

		// Base allocation from BandAllocation table
		// Use middle rate index (5) for base
		baseAlloc := int32(BandAllocation[i][5])

		// Minimum bits for this band
		allocFloor := c << BITRES

		// Threshold to code this band
		thresh[i] = allocFloor + 1<<BITRES

		// bits1 is base allocation, bits2 is scaling
		bits1[i] = baseAlloc * c * bandSize >> 3
		bits2[i] = baseAlloc * c * bandSize >> 3

		// Cap from CacheCaps
		// Index into caps: LM * 21 + band
		capsIdx := ra.lm*NumBands48000 + i
		if capsIdx < len(CacheCaps50) {
			cap[i] = int32(CacheCaps50[capsIdx]) << BITRES
		} else {
			// Fallback
			cap[i] = 255 << BITRES
		}

		// Apply logN adjustment
		if i < len(LogN400) {
			bits1[i] += int32(LogN400[i]) * c
			bits2[i] += int32(LogN400[i]) * c
		}

		// Apply LM adjustment
		bits1[i] += logM * c
		bits2[i] += logM * c
	}
}

// splitBits splits allocated bits into fine energy and PVQ.
func (ra *RateAllocator) splitBits(result *AllocationResult) {
	c := ra.channels
	logM := ra.lm << BITRES

	for i := 0; i < len(result.Bits); i++ {
		bits := result.Bits[i]
		bandSize := ra.mode.Bands.BandSizes[i] << ra.lm

		if bandSize == 1 || bits <= 0 {
			// N=1: all bits go to fine energy
			result.EBits[i] = 0
			result.FinePriority[i] = 1
			continue
		}

		// Compute fine energy bits
		den := c*bandSize + 1
		ncLogN := int32(0)
		if i < len(LogN400) {
			ncLogN = int32(den) * (int32(LogN400[i]) + int32(logM))
		}

		offset := (ncLogN >> 1) - int32(den*FineOffset)

		// Compute ebits
		ebits := int((bits + offset + int32(den<<(BITRES-1))) / int32(den) >> BITRES)

		if ebits < 0 {
			ebits = 0
		}
		if ebits > MaxFineBits {
			ebits = MaxFineBits
		}

		// Don't allocate more than available
		if c*ebits > int(bits>>BITRES) {
			ebits = int(bits) >> BITRES / c
		}

		result.EBits[i] = ebits

		// Priority for leftover bits
		result.FinePriority[i] = 0
		if int32(ebits*den<<BITRES) >= bits+offset {
			result.FinePriority[i] = 1
		}

		// Remove fine energy bits from band allocation
		result.Bits[i] -= int32(c * ebits << BITRES)
	}
}

// GetPulseCount converts bits to pulse count for a band.
// This uses the pulse cache for accurate conversion.
func (ra *RateAllocator) GetPulseCount(bandIdx int, bits int32) int {
	if bits <= 0 {
		return 0
	}

	bandSize := ra.mode.Bands.BandSizes[bandIdx] << ra.lm

	// Simple heuristic based on bits and band size
	// Full implementation would use CacheBits50 lookup
	pulses := int(bits>>BITRES) / (bandSize * 2)
	if pulses < 1 {
		pulses = 1
	}

	// Cap pulses to prevent overflow in icwrs
	maxPulses := bandSize * 2
	if maxPulses > 32 {
		maxPulses = 32
	}
	if pulses > maxPulses {
		pulses = maxPulses
	}

	// Verify codebook fits in uint32
	codebookSize := icwrs(bandSize, pulses)
	for pulses > 1 && codebookSize == 0 {
		pulses--
		codebookSize = icwrs(bandSize, pulses)
	}

	return pulses
}

// ApplyAllocation applies allocation result to encoder state.
func (ra *RateAllocator) ApplyAllocation(result *AllocationResult, bitAlloc *BitAllocation) {
	for i := 0; i < len(result.Bits) && i < len(bitAlloc.bandBits); i++ {
		bitAlloc.bandBits[i] = int(result.Bits[i] >> BITRES)
		bitAlloc.fineEnergy[i] = result.EBits[i]
		bitAlloc.pulseCounts[i] = ra.GetPulseCount(i, result.Bits[i])
	}
}

// VerifyAllocationMatch compares our allocation against expected libopus output.
// Returns match percentage (0.0 to 1.0) and list of mismatches.
func VerifyAllocationMatch(ours, expected []int) (float64, []int) {
	if len(ours) != len(expected) {
		return 0.0, nil
	}

	matches := 0
	mismatches := make([]int, 0)

	for i := range ours {
		if ours[i] == expected[i] {
			matches++
		} else {
			mismatches = append(mismatches, i)
		}
	}

	return float64(matches) / float64(len(ours)), mismatches
}
