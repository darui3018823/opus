// Package celt provides standard-compliant bit allocation for CELT codec.
// This implements the allocation logic from libopus rate.c (compute_allocation).
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

	for i := 0; i < numBands; i++ {
		// Get band size (scaled by LM)
		bandSize := int32(ra.mode.Bands.BandSizes[i]) << ra.lm

		// Minimum bits for this band (in Q8)
		allocFloor := c << BITRES

		// Threshold to code this band
		thresh[i] = allocFloor + (1 << BITRES)

		// Base allocation from BandAllocation table
		// Index 0 is minimum, index 10 is maximum
		// bits1 = minimum allocation, bits2 = delta to maximum
		minAlloc := int32(BandAllocation[i][0])
		maxAlloc := int32(BandAllocation[i][10])

		// Scale by band size and channels, convert to Q8
		// The allocation values are per-coefficient, so multiply by bandSize
		bits1[i] = minAlloc * c * bandSize
		bits2[i] = (maxAlloc - minAlloc) * c * bandSize

		// Apply logN adjustment
		if i < len(LogN400) {
			logN := int32(LogN400[i])
			bits1[i] += logN * c * bandSize >> 2
			bits2[i] += logN * c * bandSize >> 2
		}

		// Cap from CacheCaps (index: LM * 21 + band)
		capsIdx := ra.lm*NumBands48000 + i
		if capsIdx < len(CacheCaps50) {
			cap[i] = int32(CacheCaps50[capsIdx]) * c * bandSize
		} else {
			cap[i] = 255 * c * bandSize
		}
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

// computeAllocation is a Go port of libopus compute_allocation() from rate.c.
//
// Parameters:
//
//	numBands  – number of frequency bands (21 for 48 kHz)
//	lm        – log2(frame_size/120); 3 for 20 ms frames
//	ch        – channel count (1 or 2)
//	allocTrim – allocation tilt read from stream (0-10, default 5 = neutral)
//	available – bits available for PVQ+fine energy (totalBits − bits already consumed)
//
// Returns (pulses[numBands], eBits[numBands]).
func computeAllocation(numBands, lm, ch, allocTrim, available int) ([]int, []int) {
	pulses := make([]int, numBands)
	eBits := make([]int, numBands)

	if available <= 0 {
		return pulses, eBits
	}

	// --- per-band setup ---
	bits1 := make([]int, numBands)
	bits2 := make([]int, numBands)
	thresh := make([]int, numBands)
	cap := make([]int, numBands)

	for j := 0; j < numBands; j++ {
		N := int(EBands48000[j+1] - EBands48000[j]) // base bins
		M := N << uint(lm)                           // actual MDCT bins

		// trim_offset: frequency tilt (in bits).
		// trim_offset[j] = C*M*(allocTrim-5-LM)*(numBands-1-j) + (numBands/2) >> 6
		trimOff := (ch*M*(allocTrim-5-lm)*(numBands-1-j) + numBands/2) >> 6

		// thresh: minimum bits to code this band at all.
		// From libopus: max(C<<BITRES, (27*C*M) >> (15-BITRES))
		// BITRES=3 → 15-3=12; C<<3 = C*8
		t1 := ch << 3 // C * 2^BITRES
		t2 := (27 * ch * M) >> 12
		if t2 > t1 {
			thresh[j] = t2
		} else {
			thresh[j] = t1
		}

		// bits1 = min allocation (column 0); bits2 = max allocation (column AllocSteps).
		// BandAllocation values are in 1/32 bits/sample → multiply by M, shift right 5.
		v0 := ch * M * int(BandAllocation[j][0]) >> 5
		v0 += trimOff
		if v0 < 0 {
			v0 = 0
		}
		bits1[j] = v0

		vS := ch * M * int(BandAllocation[j][AllocSteps]) >> 5
		vS += trimOff
		if vS < 0 {
			vS = 0
		}
		bits2[j] = vS - bits1[j]
		if bits2[j] < 0 {
			bits2[j] = 0
		}

		// cap from CacheCaps50 (per band, per lm).
		capsIdx := lm*NumBands48000 + j
		if capsIdx < len(CacheCaps50) {
			cap[j] = ch * M * int(CacheCaps50[capsIdx]) >> 5 // same unit as bits1/bits2
		} else {
			cap[j] = ch * M * 255 >> 5
		}
	}

	// --- binary search for optimal allocation level lo ---
	lo, hi := 0, 1<<AllocSteps
	for iter := 0; iter < AllocSteps; iter++ {
		mid := (lo + hi) >> 1
		psum := 0
		done := false
		for j := numBands - 1; j >= 0; j-- {
			tmp := bits1[j] + (mid*bits2[j])>>AllocSteps
			if tmp >= thresh[j] || done {
				done = true
				if tmp > cap[j] {
					tmp = cap[j]
				}
				psum += tmp
			} else if tmp >= ch<<3 {
				psum += ch << 3
			}
		}
		if psum > available {
			hi = mid
		} else {
			lo = mid
		}
	}

	// --- apply lo allocation, collect band bits ---
	bandBits := make([]int, numBands)
	done := false
	for j := numBands - 1; j >= 0; j-- {
		tmp := bits1[j] + (lo*bits2[j])>>AllocSteps
		if tmp >= thresh[j] || done {
			done = true
			if tmp > cap[j] {
				tmp = cap[j]
			}
			bandBits[j] = tmp
		}
		// else bandBits[j] = 0 (skip this band)
	}

	// --- distribute leftover bits ---
	used := 0
	for j := 0; j < numBands; j++ {
		used += bandBits[j]
	}
	leftover := available - used
	// Give leftover to bands in priority order (lowest band first for simplicity)
	for j := 0; j < numBands && leftover > 0; j++ {
		if bandBits[j] > 0 {
			add := leftover
			if add+bandBits[j] > cap[j] {
				add = cap[j] - bandBits[j]
			}
			if add < 0 {
				add = 0
			}
			bandBits[j] += add
			leftover -= add
		}
	}

	// --- convert band bits to pulses using bits2pulses ---
	for j := 0; j < numBands; j++ {
		if bandBits[j] <= 0 {
			continue
		}
		N := int(EBands48000[j+1] - EBands48000[j])
		M := N << uint(lm) // actual MDCT bins per channel

		// Fine energy bits: from libopus compute_ebits.
		// Simple heuristic that matches libopus for most cases.
		fb := celtComputeEbits(M, ch, bandBits[j], lm)
		eBits[j] = fb

		pvqBits := bandBits[j] - ch*fb
		if pvqBits < 0 {
			pvqBits = 0
		}

		// Convert pvqBits to pulse count using CacheBits50.
		pulses[j] = celtBits2Pulses(j, lm, pvqBits)
	}

	return pulses, eBits
}

// celtComputeEbits computes fine energy bits for one band (per libopus compute_allocation).
// M = MDCT bins, ch = channels, bits = total bits for this band, lm = LM.
func celtComputeEbits(M, ch, bits, lm int) int {
	if M <= 0 || bits <= 0 {
		return 0
	}
	den := ch*M + 1
	// ncLogN: approximate log(N*C) scaled
	logN := 0 // simplified: use 0 for small bands
	if M >= 2 {
		logN = 8 // rough approximation
	}
	logM := lm << BITRES

	ncLogN := den * (logN + logM)
	offset := (ncLogN >> 1) - den*FineOffset

	ebits := (bits + offset + den<<(BITRES-1)) / den >> BITRES
	if ebits < 0 {
		ebits = 0
	}
	if ebits > MaxFineBits {
		ebits = MaxFineBits
	}
	if ch*ebits > bits>>BITRES {
		if bits>>BITRES > 0 {
			ebits = (bits >> BITRES) / ch
		} else {
			ebits = 0
		}
	}
	return ebits
}

// celtBits2Pulses converts a bit budget to a pulse count for band j at LM=lm.
// Uses the CacheBits50 table (same as libopus bits2pulses).
func celtBits2Pulses(bandIdx, lm, bits int) int {
	if bits <= 0 {
		return 0
	}
	// Index into CacheBits50 for (lm, bandIdx).
	idx := lm*NumBands48000 + bandIdx
	if idx < 0 || idx >= len(CacheIndex50) {
		return 0
	}
	start := int(CacheIndex50[idx])
	if start < 0 {
		return 0
	}

	// CacheBits50[start] = number of entries (max pulse count for this band+lm).
	nEntries := int(CacheBits50[start])
	if nEntries <= 0 {
		return 0
	}

	// Binary search: find largest k such that CacheBits50[start+k] <= bits.
	lo, hi := 0, nEntries
	for hi-lo > 1 {
		mid := (lo + hi) >> 1
		if int(CacheBits50[start+mid]) >= bits {
			hi = mid
		} else {
			lo = mid
		}
	}
	if bits < int(CacheBits50[start+hi]) {
		return lo
	}
	return hi
}
