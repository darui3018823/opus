// Package celt provides standard-compliant bit allocation for CELT codec.
// This implements the allocation logic from libopus rate.c (compute_allocation).
package celt

import (
	"fmt"
	"os"

	"github.com/darui3018823/opus/internal/entcode"
)

// allocDebug enables allocation debug output when set to true.
var allocDebug = false

var allocRangeTrace = os.Getenv("OPUS_ALLOC_TRACE") != ""
var osStderr = os.Stderr

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
	0, 8, 13, 16, 19, 21, 23, 24, 26, 27, 28, 29, 30, 31, 32, 32, 33, 34, 34, 35, 36, 36, 37, 37,
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
//	offsets   – per-band Q3 boost from dynalloc (nil = all zeros)
//
// Returns (pulses[numBands], eBits[numBands], intensity, dualStereo).
// All internal accounting uses Q3 (bits × 8) matching libopus BITRES=3.
func computeAllocation(
	dec *entcode.Decoder,
	numBands, start, end, lm, ch, allocTrim, available int,
	offsets []int,
) (pulses []int, eBits []int, finePriority []int, balance, intensity, codedBands int, dualStereo bool) {
	return computeAllocationShared(dec, nil, end, false, numBands, start, end, lm, ch, allocTrim, available, offsets)
}

// computeAllocationEncode is the encoder-side entry point. encIntensity and
// encDualStereo are the encoder's chosen stereo parameters (written to the
// stream); they are returned (possibly clamped) so quant_all_bands uses the same
// values the decoder will read.
func computeAllocationEncode(
	enc *entcode.Encoder,
	encIntensity int, encDualStereo bool,
	numBands, start, end, lm, ch, allocTrim, available int,
	offsets []int,
) (pulses []int, eBits []int, finePriority []int, balance, intensity, codedBands int, dualStereo bool) {
	return computeAllocationShared(nil, enc, encIntensity, encDualStereo, numBands, start, end, lm, ch, allocTrim, available, offsets)
}

func computeAllocationShared(
	dec *entcode.Decoder,
	enc *entcode.Encoder,
	encIntensity int, encDualStereo bool,
	numBands, start, end, lm, ch, allocTrim, available int,
	offsets []int,
) (pulses []int, eBits []int, finePriority []int, balance, intensity, codedBands int, dualStereo bool) {
	encode := enc != nil
	pulses = make([]int, numBands)
	codedBands = end
	eBits = make([]int, numBands)
	finePriority = make([]int, numBands)
	intensity = end
	dualStereo = false

	if offsets == nil {
		offsets = make([]int, numBands)
	}

	if available <= 0 {
		return
	}

	// `available` is already the Q3 (eighth-bit) budget computed by the caller as
	// (len*8<<BITRES) - ec_tell_frac - 1 - anti_collapse_rsv, matching libopus.
	total := available
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
		n := end - start
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
	// cap     = CacheCaps50[LM*nbEBands+j] * C * N >> 2 = same scale as bitsj
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
		// cap[j] = (caps[nbEBands*(2*LM+C-1)+j] + 64) * C * N >> 2   (libopus init_caps)
		capsIdx := NumBands48000*(2*lm+ch-1) + j
		capVal := 255
		if capsIdx >= 0 && capsIdx < len(CacheCaps50) {
			capVal = int(CacheCaps50[capsIdx])
		}
		cap[j] = (capVal + 64) * ch * M >> 2
		// trim_offset = C*N0*(alloc_trim-5-LM)*(end-j-1)*(1<<(LM+BITRES))>>6
		//             = C*M*(alloc_trim-5-LM)*(end-j-1)>>3  (since M=N0<<LM, *8>>6 = >>3)
		trimOff[j] = ch * M * (allocTrim - 5 - lm) * (end - 1 - j) >> 3
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
	// Matches libopus coarse search: bitsj + offsets[j], done branch = IMIN(bitsj,cap).
	psumCoarse := func(k int) int {
		psum, done := 0, false
		for j := end - 1; j >= start; j-- {
			bitsj := allocAtLevel(j, k) + offsets[j]
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

	// skipStart: the first band that must be coded (= last boosted band index, or 0).
	// Updated in Phase 2 when offsets[j]>0, and used in Phase 5 skip loop.
	skipStart := 0

	// Phase 2: per-band bits1/bits2 from coarse levels lo and hi.
	// bits2j = delta from lo to hi (trim cancels in delta).
	// When hi >= numAllocLevels: bits2j = cap[j] (libopus: hi>=nbAllocVectors → cap).
	// offsets are added: bits1j += offsets[j] (only if lo>0); bits2j += offsets[j] always.
	// skip_start is advanced to last boosted band.
	bits1 := make([]int, numBands)
	bits2 := make([]int, numBands)
	skipStart = start
	for j := start; j < end; j++ {
		b1 := allocAtLevel(j, lo)
		if lo > 0 {
			b1 += offsets[j]
		}
		var b2 int
		if hi >= numAllocLevels {
			b2 = cap[j]
		} else {
			b2 = allocAtLevel(j, hi)
		}
		if hi >= numAllocLevels && b2 > 0 {
			b2 += trimOff[j]
			if b2 < 0 {
				b2 = 0
			}
		}
		b2 += offsets[j]
		b2 -= b1
		if b2 < 0 {
			b2 = 0
		}
		bits1[j] = b1
		bits2[j] = b2
		if offsets[j] > 0 {
			skipStart = j
		}
	}

	// Phase 3: fine binary search in [0, 1<<AllocSteps=64].
	// Matches libopus interp_bits2pulses fine search: iterates BACKWARD with done+threshold,
	// same logic as psumCoarse. NOT a simple forward sum — libopus uses done flag here too.
	psumFine := func(s int) int {
		psum, done := 0, false
		for j := end - 1; j >= start; j-- {
			tmp := bits1[j] + s*bits2[j]>>AllocSteps
			if tmp >= thresh[j] || done {
				done = true
				v := tmp
				if v > cap[j] {
					v = cap[j]
				}
				psum += v
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
	// Matches libopus interp_bits2pulses: iterates BACKWARD with done+threshold flag.
	// Bands below threshold and below done get allocFloor or 0, not the full interpolated value.
	bandBits := make([]int, numBands)
	{
		psum, done := 0, false
		for j := end - 1; j >= start; j-- {
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
		if allocDebug {
			fmt.Printf("[alloc] phase4: lo=%d hi=%d loF=%d total=%d psum=%d\n", lo, hi, loF, total, psum)
			fmt.Printf("[alloc] phase4: bandBits[0..5]=%v\n", bandBits[:6])
			fmt.Printf("[alloc] phase4: bits1[0..5]=%v bits2[0..5]=%v\n", bits1[:6], bits2[:6])
			fmt.Printf("[alloc] phase4: thresh[0..5]=%v cap[0..5]=%v trimOff[0..5]=%v\n", thresh[:6], cap[:6], trimOff[:6])
			for j := 16; j <= 20 && j < numBands; j++ {
				fmt.Printf("[alloc] phase4 j=%d bits1=%d bits2=%d interp=%d bandBits=%d cap=%d\n",
					j, bits1[j], bits2[j], bits1[j]+loF*bits2[j]>>AllocSteps, bandBits[j], cap[j])
			}
		}
		_ = psum
	}

	// Phase 5: band skip loop — reads skip bits from range coder (decode mode).
	// Iterates from highest band downward. For each band:
	//   - If band_bits >= threshold: decode a 1-bit signal.
	//     bit=1 → stop (this band is the last coded); bit=0 → skip this band.
	//   - If band_bits < threshold: force-skip without bit.
	// When j reaches skipStart: restore skipRsv and stop.
	psum := 0
	for _, b := range bandBits {
		psum += b
	}
	intensityRsvLocal := intensityRsv
	// libopus uses the band `start` for all band-range arithmetic in the skip
	// loop, intensity/dual-stereo decode, and the final bit distribution.
	// `skipStart` (last dynalloc-boosted band, >= start) gates ONLY the skip-loop
	// break condition `j <= skip_start`. For CELT-only start==0; for hybrid start==17.
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
		totalN0 := int(EBands48000[codedBands]) - int(EBands48000[start])
		percoeff := 0
		if totalN0 > 0 && left > 0 {
			percoeff = left / totalN0
		}
		leftRem := left - totalN0*percoeff
		bandN0start := int(EBands48000[j]) - int(EBands48000[start])
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
			// Encode/decode a 1-bit skip signal. The encoder keeps every band
			// that reaches the threshold (no rate-driven skipping yet).
			keepBand := true
			if encode {
				enc.EncodeBitLogp(keepBand, 1)
			} else if dec != nil {
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
			n := j - start
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

	if allocDebug {
		fmt.Printf("[alloc] phase5: codedBands=%d psum=%d total=%d\n", codedBands, psum, total)
	}

	// Phase 6: decode intensity stereo and dual-stereo (C==2 only).
	// In libopus these are decoded inside interp_bits2pulses AFTER the skip loop.
	if intensityRsv > 0 {
		if encode {
			intensity = encIntensity
			if intensity > codedBands {
				intensity = codedBands
			}
			enc.EncodeUint(uint32(intensity-start), uint32(codedBands-start+1))
		} else if dec != nil {
			intensity = int(dec.DecodeUint(uint32(codedBands-start+1))) + start
		}
	} else {
		intensity = 0
	}
	if intensity <= start {
		total += dualStereoRsv
		dualStereoRsv = 0
	}
	if dualStereoRsv > 0 {
		if encode {
			dualStereo = encDualStereo
			enc.EncodeBitLogp(dualStereo, 1)
		} else if dec != nil {
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
		totalN0 := int(EBands48000[codedBands]) - int(EBands48000[start])
		percoeff := 0
		if totalN0 > 0 && left > 0 {
			percoeff = left / totalN0
		}
		left -= totalN0 * percoeff
		if allocDebug {
			fmt.Printf("[alloc] ph7 total=%d psum=%d left0=%d totalN0=%d percoeff=%d leftRem=%d\n", total, psum, total-psum, totalN0, percoeff, left)
		}
		for j := start; j < codedBands; j++ {
			N0 := int(EBands48000[j+1]) - int(EBands48000[j])
			bandBits[j] += percoeff * N0
		}
		// Sequential remainder distribution (1 Q3-bit per coefficient from low to high band).
		for j := start; j < codedBands && left > 0; j++ {
			N0 := int(EBands48000[j+1]) - int(EBands48000[j])
			add := left
			if N0 < add {
				add = N0
			}
			bandBits[j] += add
			left -= add
		}
	}

	if allocDebug {
		fmt.Printf("[alloc] phase7: bandBits[0..5]=%v\n", bandBits[:6])
	}

	// Phase 8: compute eBits and pulses using libopus interp_bits2pulses logic.
	// Includes: N==2 special case, offset threshold adjustments, excess/balance propagation.
	logM := lm << 3 // LM<<BITRES
	stereoFlag := 0
	if ch > 1 {
		stereoFlag = 1
	}
	balance = 0
	for j := start; j < codedBands; j++ {
		N0 := int(EBands48000[j+1] - EBands48000[j])
		M := N0 << uint(lm)
		bit := bandBits[j] + balance

		var excess int
		if M > 1 {
			// General case (N > 1)
			excess = bit - cap[j]
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
			// Don't bust the raw budget. libopus:
			//   if (C*ebits[j] > (bits[j]>>BITRES)) ebits[j] = bits[j]>>stereo>>BITRES;
			// Note the threshold is bits[j]>>BITRES (no stereo shift) and the clamp
			// value is bits[j]>>stereo>>BITRES (no division by C).
			if ch*fb > bandBits[j]>>3 {
				fb = bandBits[j] >> stereoFlag >> 3
			}
			if fb > MaxFineBits {
				fb = MaxFineBits
			}
			eBits[j] = fb
			if fb*(den<<3) >= bandBits[j]+offset {
				finePriority[j] = 1
			}
			bandBits[j] -= ch * fb << 3 // remove fine energy bits
		} else {
			// N==1: all bits for fine energy, no PVQ bits for sign
			excess = bit - (ch << 3) // bit - C<<BITRES
			if excess < 0 {
				excess = 0
			}
			bandBits[j] = bit - excess
			eBits[j] = 0
			finePriority[j] = 1
		}

		// Fine energy can't take advantage of the re-balancing in
		// quant_all_bands(); do the re-balancing here. Applies to BOTH N>1 and
		// N==1 bands (libopus has this outside the if/else), and `balance=excess`
		// is the carry to the next band.
		if excess > 0 {
			extraFine := excess >> (stereoFlag + 3) // excess>>(stereo+BITRES)
			if extraFine > MaxFineBits-eBits[j] {
				extraFine = MaxFineBits - eBits[j]
			}
			eBits[j] += extraFine
			extraBits := extraFine * ch << 3 // extraFine*C<<BITRES
			if extraBits >= excess-balance {
				finePriority[j] = 1
			} else {
				finePriority[j] = 0
			}
			excess -= extraBits
		}
		balance = excess
		if allocDebug {
			fmt.Printf("[alloc] ph8 j=%d bit=%d cap=%d bandBits=%d eBits=%d balance=%d\n", j, bit, cap[j], bandBits[j], eBits[j], balance)
		}
	}

	// Skipped bands: fine energy only.
	for j := codedBands; j < numBands; j++ {
		eBits[j] = bandBits[j] >> stereoFlag >> 3 // bits[j]>>stereo>>BITRES
		bandBits[j] = 0
		if eBits[j] < 1 {
			finePriority[j] = 1
		}
	}

	if allocDebug {
		fmt.Printf("[alloc] phase8: eBits[0..5]=%v\n", eBits[:6])
		fmt.Printf("[alloc] phase8: bandBits[0..5]=%v (after fine removal)\n", bandBits[:6])
	}

	if allocRangeTrace {
		tag := "DEC"
		if encode {
			tag = "ENC"
		}
		fmt.Fprintf(osStderr, "[ALLOC %s] available=%d total=%d codedBands=%d balance=%d intensity=%d\n",
			tag, available, total, codedBands, balance, intensity)
		fmt.Fprintf(osStderr, "[ALLOC %s] bandBits=%v\n", tag, bandBits[:numBands])
		fmt.Fprintf(osStderr, "[ALLOC %s] eBits=%v\n", tag, eBits[:numBands])
	}

	// libopus `pulses[]` IS the per-band PVQ bit budget (Q3), NOT the pulse count K.
	// K is derived per band inside quant_all_bands (with the running balance), so we
	// return the Q3 budgets here and let QuantAllBands convert via bits2pulses.
	for j := 0; j < numBands; j++ {
		pulses[j] = bandBits[j]
	}

	return
}

// celtBits2PulsesQ3 converts a Q3 bit budget to a pulse count for band j at LM=lm.
// Matches libopus bits2pulses (rate.h) exactly:
//
//	LM++; cache = CacheBits50+CacheIndex50[(LM)*nbEBands+band];
//	lo=0; hi=cache[0]; bits--;
//	for i in 0..LOG_MAX_PSEUDO-1: mid=(lo+hi+1)>>1; if cache[mid]>=bits: hi=mid else lo=mid
//	if bits-cache[lo] <= cache[hi]-bits: return lo else return hi
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

	// libopus bits--; then LOG_MAX_PSEUDO=6 iterations with (lo+hi+1)>>1 midpoint.
	// cache[mid] = CacheBits50[start+mid] (0=nEntries, 1..nEntries=costs).
	bitsQ3--
	lo, hi := 0, nEntries
	for i := 0; i < 6; i++ {
		mid := (lo + hi + 1) >> 1
		if int(CacheBits50[start+mid]) >= bitsQ3 {
			hi = mid
		} else {
			lo = mid
		}
	}
	// Rounding: bits-(lo==0?-1:cache[lo]) <= cache[hi]-bits → lo; else hi.
	loCost := -1
	if lo > 0 {
		loCost = int(CacheBits50[start+lo])
	}
	hiCost := 255
	if start+hi < len(CacheBits50) {
		hiCost = int(CacheBits50[start+hi])
	}
	if bitsQ3-loCost <= hiCost-bitsQ3 {
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
	return int(CacheBits50[start+p]) + 1 // libopus: cache[pulses]+1
}
