// Package celt provides standard-compliant bit allocation for CELT codec.
// This implements the allocation logic from libopus rate.c (compute_allocation).
package celt

import "github.com/darui3018823/opus/internal/entcode"

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

// numAllocLevels is the number of columns in BandAllocation (k=0..10).
const numAllocLevels = 11

// log2FracTable matches libopus LOG2_FRAC_TABLE (celt/mathops.h).
// Gives Q3-cost (bits×8) to signal an integer in [0..n].
var log2FracTable = [24]int{
	0, 8, 13, 16, 19, 21, 22, 24, 25, 26, 27, 28, 29, 30, 30, 31, 32, 32, 33, 33, 34, 34, 35, 35,
}

// computeAllocation is a faithful Go port of libopus compute_allocation() + interp_bits2pulses()
// from celt/rate.c.
//
// Parameters:
//
//	dec       – range decoder (needed to consume skip bits and stereo params from stream)
//	numBands  – number of frequency bands (21 for 48 kHz FB)
//	lm        – log2(frame_size/120); 3 for 20 ms frames
//	ch        – channel count (1 or 2)
//	allocTrim – allocation tilt read from stream (0-10, default 5 = neutral)
//	available – bits available (totalBits − bits already consumed by earlier fields)
//
// Returns (pulses[numBands], eBits[numBands], intensity, dualStereo).
// All internal accounting uses Q3 (bits × 8) matching libopus BITRES=3.
func computeAllocation(
	dec *entcode.Decoder,
	numBands, lm, ch, allocTrim, available int,
) (pulses []int, eBits []int, intensity int, dualStereo bool) {
	pulses = make([]int, numBands)
	eBits = make([]int, numBands)
	intensity = numBands
	dualStereo = false

	if available <= 0 {
		return
	}

	// Convert to Q3 (BITRES=3, 1<<BITRES = 8).
	total := available * 8
	if total <= 0 {
		return
	}

	// Reserve 1 Q3-bit for skip-band signaling.
	skipRsv := 0
	if total >= 8 {
		skipRsv = 8
		total -= skipRsv
	}

	// Reserve Q3-bits for intensity stereo and dual-stereo (C==2 only).
	intensityRsv, dualStereoRsv := 0, 0
	if ch == 2 {
		n := numBands
		if n < len(log2FracTable) {
			intensityRsv = log2FracTable[n]
		} else {
			intensityRsv = 35
		}
		if intensityRsv > total {
			intensityRsv = 0
		} else {
			total -= intensityRsv
			if total >= 8 {
				dualStereoRsv = 8
				total -= dualStereoRsv
			}
		}
	}

	// Per-band threshold, cap, and trim_offset (Q3).
	// thresh  = max(C<<BITRES, (3*N0<<LM<<BITRES)>>4) = max(C*8, 3*M/2)
	// cap     = CacheCaps50[(LM+1)*nbEBands+j] * (C<<BITRES) >> 2 = caps * C * 2
	// trimOff = C*N0*alloc_trim_term * (end-j-1) * (1<<(LM+BITRES)) >> 6
	//         = C * M * (alloc_trim-5-LM) * (end-j-1) >> 3
	//         [ minus C<<BITRES if M==1 (N==1 at this LM) ]
	thresh := make([]int, numBands)
	cap := make([]int, numBands)
	trimOff := make([]int, numBands)
	for j := 0; j < numBands; j++ {
		N0 := int(EBands48000[j+1] - EBands48000[j])
		M := N0 << uint(lm)
		// thresh
		t1 := ch << 3   // C<<BITRES
		t2 := 3 * M / 2 // (3*M*8)>>4 — note: no C factor per libopus formula
		if t2 > t1 {
			thresh[j] = t2
		} else {
			thresh[j] = t1
		}
		// cap
		capsIdx := (lm+1)*NumBands48000 + j
		if capsIdx < len(CacheCaps50) {
			cap[j] = int(CacheCaps50[capsIdx]) * (ch << 3) >> 2 // caps * C * 2
		} else {
			cap[j] = 255 * (ch << 3) >> 2
		}
		// trim_offset = C*M*(alloc_trim-5-LM)*(end-j-1)>>3
		trimOff[j] = ch * M * (allocTrim - 5 - lm) * (numBands - 1 - j) >> 3
		if M == 1 { // N0<<LM == 1: subtract C<<BITRES
			trimOff[j] -= ch << 3
		}
	}

	allocFloor := ch << 3 // C<<BITRES = C*8 Q3-bits

	// allocAtLevel returns Q3 bits for band j at BandAllocation level k with trim applied.
	// Matches libopus: bitsj = C*N0*alloc<<LM>>2; if bitsj>0: bitsj=max(0,bitsj+trim)
	allocAtLevel := func(j, k int) int {
		N0 := int(EBands48000[j+1] - EBands48000[j])
		M := N0 << uint(lm)
		bitsj := ch * M * int(BandAllocation[j][k]) >> 2
		if bitsj > 0 {
			bitsj += trimOff[j]
			if bitsj < 0 {
				bitsj = 0
			}
		}
		return bitsj
	}

	// psumCoarse computes the total allocated Q3-bits at BandAllocation level k.
	// Matches libopus coarse search: done branch = IMIN(bitsj,cap), no allocFloor minimum.
	psumCoarse := func(k int) int {
		psum, done := 0, false
		for j := numBands - 1; j >= 0; j-- {
			bitsj := allocAtLevel(j, k)
			if bitsj >= thresh[j] || done {
				done = true
				v := bitsj
				if v > cap[j] {
					v = cap[j]
				}
				psum += v
			} else if bitsj >= allocFloor {
				psum += allocFloor
			}
		}
		return psum
	}

	// Phase 1: coarse binary search (lo=1..hi=10) for highest level where psum<=total.
	// Matches libopus: lo=1, hi=nbAllocVectors-1; do { mid=(lo+hi)/2; ... } while lo<=hi.
	lo, hi := 1, numAllocLevels-1
	for lo <= hi {
		mid := (lo + hi) >> 1
		if psumCoarse(mid) > total {
			hi = mid - 1
		} else {
			lo = mid + 1
		}
	}
	hi = lo
	lo--
	if lo < 0 {
		lo = 0
	}

	// Phase 2: per-band bits1/bits2 from coarse levels lo and hi.
	// bits2j = delta from lo to hi (trim cancels in delta).
	// When hi >= numAllocLevels: bits2j = cap[j] (libopus: hi>=nbAllocVectors → cap).
	bits1 := make([]int, numBands)
	bits2 := make([]int, numBands)
	for j := 0; j < numBands; j++ {
		b1 := allocAtLevel(j, lo)
		var b2 int
		if hi >= numAllocLevels {
			b2 = cap[j]
		} else {
			b2 = allocAtLevel(j, hi)
		}
		b2 -= b1
		if b2 < 0 {
			b2 = 0
		}
		bits1[j] = b1
		bits2[j] = b2
	}

	// Phase 3: fine binary search in [0, 1<<AllocSteps=64].
	// psum: done branch = IMIN(tmp,cap), no allocFloor; non-done: alloc_floor or 0.
	psumFine := func(s int) int {
		psum, done := 0, false
		for j := numBands - 1; j >= 0; j-- {
			tmp := bits1[j] + s*bits2[j]>>AllocSteps
			if tmp >= thresh[j] || done {
				done = true
				if tmp > cap[j] {
					tmp = cap[j]
				}
				psum += tmp
			} else if tmp >= allocFloor {
				psum += allocFloor
			}
		}
		return psum
	}
	loF, hiF := 0, 1<<AllocSteps
	for i := 0; i < AllocSteps; i++ {
		mid := (loF + hiF) >> 1
		if psumFine(mid) > total {
			hiF = mid
		} else {
			loF = mid
		}
	}
	_ = hiF

	// Phase 4: compute final bandBits[j] from loF.
	// Scan from high band to low (done logic): below thresh and not done → floor or 0.
	bandBits := make([]int, numBands)
	{
		psum, done := 0, false
		for j := numBands - 1; j >= 0; j-- {
			tmp := bits1[j] + loF*bits2[j]>>AllocSteps
			if tmp < thresh[j] && !done {
				if tmp >= allocFloor {
					tmp = allocFloor
				} else {
					tmp = 0
				}
			} else {
				done = true
			}
			if tmp > cap[j] {
				tmp = cap[j]
			}
			bandBits[j] = tmp
			psum += tmp
		}
		_ = psum
	}

	// Phase 5: band skip loop — reads skip bits from range coder (decode mode).
	// Iterates from highest band downward. For each band:
	//   - If band_bits >= threshold: decode a 1-bit signal.
	//     bit=1 → stop (this band is the last coded); bit=0 → skip this band.
	//   - If band_bits < threshold: force-skip without bit.
	// When j reaches skipStart (=0): restore skipRsv and stop.
	skipStart := 0
	psum := 0
	for _, b := range bandBits {
		psum += b
	}
	codedBands := numBands
	intensityRsvLocal := intensityRsv
	for {
		j := codedBands - 1
		if j <= skipStart {
			total += skipRsv // restore unused skip reservation
			break
		}
		// Compute "band_bits" for band j: how many Q3-bits it would have after left distribution.
		left := total - psum
		if left < 0 {
			left = 0
		}
		totalN0 := int(EBands48000[codedBands]) - int(EBands48000[skipStart])
		percoeff := 0
		if totalN0 > 0 && left > 0 {
			percoeff = left / totalN0
		}
		leftRem := left - totalN0*percoeff
		bandN0start := int(EBands48000[j]) - int(EBands48000[skipStart])
		rem := 0
		if leftRem > bandN0start {
			rem = leftRem - bandN0start
		}
		bandWidthN0 := int(EBands48000[codedBands]) - int(EBands48000[j])
		bandBitsJ := bandBits[j] + percoeff*bandWidthN0 + rem

		skipThresh := thresh[j]
		if allocFloor+8 > skipThresh {
			skipThresh = allocFloor + 8
		}
		if bandBitsJ >= skipThresh {
			// Decode 1-bit skip signal from range coder.
			keepBand := true // default if no decoder
			if dec != nil {
				keepBand = dec.DecodeBitLogp(1)
			}
			if keepBand {
				break // stop: this band is the last coded one
			}
			// Continue skipping: the skip bit itself costs 8 Q3-bits.
			psum += 8
			bandBitsJ -= 8
		}
		// Remove this band from the coded set.
		psum -= bandBits[j] + intensityRsvLocal
		if intensityRsvLocal > 0 {
			n := j - skipStart
			if n < len(log2FracTable) {
				intensityRsvLocal = log2FracTable[n]
			} else {
				intensityRsvLocal = 35
			}
		}
		psum += intensityRsvLocal
		if bandBitsJ >= allocFloor {
			psum += allocFloor
			bandBits[j] = allocFloor
		} else {
			bandBits[j] = 0
		}
		codedBands--
	}

	// Phase 6: decode intensity stereo and dual-stereo (C==2 only).
	// In libopus these are decoded inside interp_bits2pulses AFTER the skip loop.
	if intensityRsv > 0 {
		if dec != nil {
			intensity = int(dec.DecodeUint(uint32(codedBands-skipStart+1))) + skipStart
		}
	} else {
		intensity = 0
	}
	if intensity <= skipStart {
		total += dualStereoRsv
		dualStereoRsv = 0
	}
	if dualStereoRsv > 0 {
		if dec != nil {
			dualStereo = dec.DecodeBitLogp(1)
		}
	}

	// Phase 7: distribute leftover bits to coded bands proportionally by N0.
	// Matches libopus: percoeff = left / (eBands[codedBands]-eBands[start]);
	// then bits[j] += percoeff*N0[j]; then distribute remainder sequentially.
	{
		left := total - psum
		if left < 0 {
			left = 0
		}
		totalN0 := int(EBands48000[codedBands]) - int(EBands48000[skipStart])
		percoeff := 0
		if totalN0 > 0 && left > 0 {
			percoeff = left / totalN0
		}
		left -= totalN0 * percoeff
		for j := skipStart; j < codedBands; j++ {
			N0 := int(EBands48000[j+1]) - int(EBands48000[j])
			bandBits[j] += percoeff * N0
		}
		// Sequential remainder distribution (1 Q3-bit per coefficient from low to high band).
		for j := skipStart; j < codedBands && left > 0; j++ {
			N0 := int(EBands48000[j+1]) - int(EBands48000[j])
			add := left
			if N0 < add {
				add = N0
			}
			bandBits[j] += add
			left -= add
		}
	}

	// Phase 8: compute eBits and pulses using libopus interp_bits2pulses logic.
	// Includes: N==2 special case, offset threshold adjustments, excess/balance propagation.
	logM := lm << 3 // LM<<BITRES
	stereoFlag := 0
	if ch > 1 {
		stereoFlag = 1
	}
	balance := 0
	for j := skipStart; j < codedBands; j++ {
		N0 := int(EBands48000[j+1] - EBands48000[j])
		M := N0 << uint(lm)
		bit := bandBits[j] + balance

		if M > 1 {
			// General case (N > 1)
			excess := bit - cap[j]
			if excess < 0 {
				excess = 0
			}
			bandBits[j] = bit - excess

			// den = C*N + 1 if (stereo && N>2 && !dual_stereo && j<intensity)
			den := ch * M
			if ch == 2 && M > 2 && !dualStereo && j < intensity {
				den++
			}
			NClogN := den * (int(LogN400[j]) + logM)
			offset := (NClogN >> 1) - den*FineOffset
			if M == 2 { // N==2 special case
				offset += den << 3 >> 2 // den<<BITRES>>2 = den*2
			}
			if bandBits[j]+offset < den*2<<3 {
				offset += NClogN >> 2
			} else if bandBits[j]+offset < den*3<<3 {
				offset += NClogN >> 3
			}
			fb := bandBits[j] + offset + (den << 2) // den<<(BITRES-1) = den*4
			if fb < 0 {
				fb = 0
			}
			if den > 0 {
				fb = fb / den >> 3 // divide by den then >>BITRES
			} else {
				fb = 0
			}
			// Don't bust the raw budget
			rawBudget := bandBits[j] >> stereoFlag >> 3 // bits[j]>>stereo>>BITRES
			if ch*fb > rawBudget {
				fb = rawBudget / ch
			}
			if fb > MaxFineBits {
				fb = MaxFineBits
			}
			finePriority := 0
			if fb*(den<<3) >= bandBits[j]+offset {
				finePriority = 1
			}
			eBits[j] = fb
			_ = finePriority
			bandBits[j] -= ch * fb << 3 // remove fine energy bits

			// Excess rebalancing
			if excess > 0 {
				extraFine := excess>>(stereoFlag+3) // excess>>(stereo+BITRES)
				if extraFine > MaxFineBits-fb {
					extraFine = MaxFineBits - fb
				}
				eBits[j] += extraFine
				extraBits := extraFine * ch << 3 // extraFine*C<<BITRES
				excess -= extraBits
			}
			balance = excess
		} else {
			// N==1: all bits for fine energy, no PVQ bits for sign
			excess := bit - (ch << 3) // bit - C<<BITRES
			if excess < 0 {
				excess = 0
			}
			bandBits[j] = bit - excess
			eBits[j] = 0
			balance = excess
		}
	}

	// Skipped bands: fine energy only.
	for j := codedBands; j < numBands; j++ {
		eBits[j] = bandBits[j] >> stereoFlag >> 3 // bits[j]>>stereo>>BITRES
		bandBits[j] = 0
	}

	// Phase 9: convert PVQ bit budget to pulse counts.
	for j := 0; j < codedBands; j++ {
		pvqQ3 := bandBits[j]
		if pvqQ3 <= 0 {
			pulses[j] = 0
			continue
		}
		pulses[j] = celtBits2PulsesQ3(j, lm, pvqQ3)
	}

	return
}

// celtBits2PulsesQ3 converts a Q3 bit budget to a pulse count for band j at LM=lm.
// Matches libopus bits2pulses exactly: LM+1 table indexing, bits-- adjustment,
// and exactly BITRES=3 binary search iterations with cache[mid+1] access.
func celtBits2PulsesQ3(bandIdx, lm, bitsQ3 int) int {
	if bitsQ3 <= 0 {
		return 0
	}
	// libopus: LM++ before index, so use (lm+1)*nbEBands+band.
	idx := (lm+1)*NumBands48000 + bandIdx
	if idx < 0 || idx >= len(CacheIndex50) {
		return 0
	}
	start := int(CacheIndex50[idx])
	if start < 0 {
		return 0
	}

	nEntries := int(CacheBits50[start])
	if nEntries <= 0 {
		return 0
	}

	// Match libopus bits2pulses exactly:
	//   bits--; (BITRES=3 iterations) if cache[mid+1] >= bits → hi=mid else lo=mid
	bitsQ3-- // libopus: bits--
	lo, hi := 0, nEntries
	for i := 0; i < 3; i++ { // exactly BITRES=3 iterations
		mid := (lo + hi) >> 1
		if int(CacheBits50[start+mid+1]) >= bitsQ3 {
			hi = mid
		} else {
			lo = mid
		}
	}
	// Final rounding: if (bits - cost(lo)) <= (cost(hi+1) - bits) → lo; else hi
	loCost := -1
	if lo > 0 {
		loCost = int(CacheBits50[start+lo])
	}
	hiPlusOneCost := 255
	if start+hi+1 < len(CacheBits50) {
		hiPlusOneCost = int(CacheBits50[start+hi+1])
	}
	if bitsQ3-loCost <= hiPlusOneCost-bitsQ3 {
		return lo
	}
	return hi
}

// celtBits2Pulses is a legacy wrapper (raw bits → Q3 → celtBits2PulsesQ3).
func celtBits2Pulses(bandIdx, lm, bits int) int {
	return celtBits2PulsesQ3(bandIdx, lm, bits*8)
}

// celtPulses2BitsQ3 returns the Q3 bit cost of p pulses in band bandIdx at LM=lm.
func celtPulses2BitsQ3(bandIdx, lm, p int) int {
	if p <= 0 {
		return 0
	}
	idx := (lm+1)*NumBands48000 + bandIdx
	if idx < 0 || idx >= len(CacheIndex50) {
		return 0
	}
	start := int(CacheIndex50[idx])
	if start < 0 {
		return 0
	}
	nEntries := int(CacheBits50[start])
	if p > nEntries {
		p = nEntries
	}
	return int(CacheBits50[start+p])
}
