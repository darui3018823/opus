package celt

import (
	"errors"
	"math"

	"github.com/darui3018823/opus/internal/dsp"
	"github.com/darui3018823/opus/internal/entcode"
)

// Decoder is a CELT decoder instance
type Decoder struct {
	mode          *Mode
	celtMode      *dsp.CELTMode     // long-block (N-point) IMDCT mode
	shortCeltMode *dsp.CELTMode     // short-block (NBase-point) IMDCT mode for transient frames
	bandProcs     [2]*BandProcessor // band processors per channel (index 0 = L/M, index 1 = R/S)
	overlap       [][]float64       // Overlap buffer per channel (NBase samples each)

	// Decoder state — oldBandE/oldLogE history in libopus' mean-subtracted
	// log2-amplitude domain. Layout is channel-major: c*numBands+i.
	prevEnergies  []float64
	prevEnergies2 []float64
	frameCount    int

	// Post-filter (one per channel)
	postFilter []*PostFilter

	// lastFinalRange is the range coder rng after the last Decode call.
	lastFinalRange uint32
}

// NewDecoder creates a new CELT decoder.
func NewDecoder(frameSize, sampleRate, channels int) (*Decoder, error) {
	return NewDecoderEx(frameSize, sampleRate, 0, channels)
}

// NewDecoderEx creates a CELT decoder with an explicit band count.
// numBands=0 means derive from sampleRate (same as NewDecoder).
// Use numBands>0 when the packet bandwidth differs from the output sampleRate
// (e.g. NB CELT packet decoded at 48kHz: numBands=13).
func NewDecoderEx(frameSize, sampleRate, numBands, channels int) (*Decoder, error) {
	if channels < 1 || channels > 2 {
		return nil, errors.New("celt: only mono and stereo supported")
	}

	mode := NewModeEx(frameSize, sampleRate, numBands, channels)

	overlap := mode.Overlap
	win := celtWindow(overlap)
	celtMode := dsp.NewCELTMode(frameSize, overlap, win)
	// Short-block mode (NBase=overlap samples) used for transient IMDCT synthesis.
	shortCeltMode := dsp.NewCELTMode(overlap, overlap, win)

	d := &Decoder{
		mode:          mode,
		celtMode:      celtMode,
		shortCeltMode: shortCeltMode,
		overlap:       make([][]float64, channels),
	}
	d.bandProcs[0] = NewBandProcessor(mode)
	if channels == 2 {
		d.bandProcs[1] = NewBandProcessor(mode)
	}

	for i := 0; i < channels; i++ {
		d.overlap[i] = make([]float64, mode.Overlap)
	}

	// oldBandE is zeroed by OPUS_RESET_STATE; oldLogE/oldLogE2 start at -28.
	nEBands := mode.Bands.NumBands * channels
	d.prevEnergies = make([]float64, nEBands)
	d.prevEnergies2 = make([]float64, nEBands)
	for i := range d.prevEnergies2 {
		d.prevEnergies2[i] = -28.0
	}

	// Initialize post-filters (one per channel)
	d.postFilter = make([]*PostFilter, channels)
	for i := range d.postFilter {
		d.postFilter[i] = NewPostFilter()
	}

	return d, nil
}

// Decode decodes a CELT frame to PCM samples
func (d *Decoder) Decode(frameData []byte) ([]float64, error) {
	if len(frameData) == 0 {
		d.lastFinalRange = 0x01000000
		return d.decodeLoss(), nil
	}

	// Capture total packet bits BEFORE any decoding so that the bit
	// allocation in decodeBandCoeffs uses the same budget as the encoder.
	totalBits := len(frameData) * 8

	// Initialize range decoder
	dec := entcode.NewDecoder(frameData)

	numBands := d.mode.Bands.NumBands
	lm := d.mode.LM
	ch := d.mode.Channels

	// === libopus celt_decode_with_ec symbol order ===

	// Silence flag (1 bit, logp 15) — first symbol in the stream.
	silence := false
	if dec.ECTell() >= totalBits {
		silence = true
	} else if dec.ECTell() == 1 {
		silence = dec.DecodeBitLogp(15)
	}
	if silence {
		dec.AdvanceTellTo(totalBits)
	}

	// Post-filter parameters — read BEFORE isTransient (start==0, ec_tell+16<=total_bits).
	pfPeriod := 0
	var pfTaps [3]float64
	pfEnabled := false
	if dec.ECTell()+16 <= totalBits {
		pfPeriod, pfTaps, pfEnabled = DecodePostFilterParams(dec, totalBits, lm)
	}

	// isTransient (logp 3, only when LM>0).
	isTransient := false
	if lm > 0 && dec.ECTell()+3 <= totalBits {
		isTransient = dec.DecodeBitLogp(3)
	}

	// intra/inter flag for coarse energy (logp 3).
	var intra bool
	if dec.ECTell()+3 <= totalBits {
		intra = dec.DecodeBitLogp(3)
	}

	// Coarse band log-energies (Laplace, forward).
	quantLogE := UnquantizeCoarseEnergy(
		dec, d.prevEnergies, d.prevEnergies2, intra, numBands, lm, ch, totalBits,
	)

	// Time-frequency allocation bits.
	tfRes := celtTFDecode(dec, totalBits, isTransient, numBands, lm)

	// Spread decision (default SPREAD_NORMAL).
	spread := 2
	if dec.ECTell()+4 <= totalBits {
		spread = dec.DecodeIcdf(spreadIcdf[:], 5)
	}

	// Per-band dynamic allocation boosts (BEFORE alloc_trim, libopus order).
	offsets := decodeDynalloc(dec, numBands, lm, ch, totalBits)

	// Allocation trim (7-bit ICDF, default 5 = neutral).
	allocTrim := 5
	if dec.ECTell()+6 <= totalBits {
		allocTrim = dec.DecodeIcdf(TrimICDF[:], 7)
	}

	// On silence, libopus forces band energies to the -28 dB floor.
	if silence {
		for i := range quantLogE {
			quantLogE[i] = -28.0
		}
	}

	// quantLogE[c*numBands+i] is mean-subtracted log2-amplitude. libopus
	// denormalise_bands adds eMeans[i] when applying the final gain.
	for i := 0; i < numBands; i++ {
		for c := 0; c < ch; c++ {
			amp := math.Exp2(quantLogE[c*numBands+i] + EMean(i))
			e := amp * amp
			if e < 1e-20 {
				e = 1e-20
			}
			d.bandProcs[c].bands[i].Energy = e
		}
	}

	d.bandProcs[0].InterpolateBandEnergies()
	if ch == 2 {
		d.bandProcs[1].InterpolateBandEnergies()
	}

	// Decode band coefficients: allocation, fine energy, PVQ, anti-collapse.
	// The Q3 bit budget is computed inside from len(frameData) and ec_tell_frac.
	// quant_all_bands also performs stereo (M/S→L/R) merge internally.
	_, _, err := d.decodeBandCoeffs(dec, len(frameData), allocTrim, isTransient, spread, tfRes, offsets)
	if err != nil {
		return nil, err
	}

	// Update oldLogE history with fine-corrected mean-subtracted values.
	copy(d.prevEnergies2, d.prevEnergies)
	for i := 0; i < numBands; i++ {
		for c := 0; c < ch; c++ {
			e := d.bandProcs[c].bands[i].Energy
			if e < 1e-20 {
				e = 1e-20
			}
			v := 0.5*math.Log2(e) - EMean(i)
			if v < -28.0 {
				v = -28.0
			}
			d.prevEnergies[c*numBands+i] = v
		}
	}

	// Apply energy denormalization per channel
	frameSize := d.mode.FrameSize
	mdctCoeffsPerCh := make([][]float64, ch)
	for c := 0; c < ch; c++ {
		d.bandProcs[c].DenormalizeBands()
		coeffs := d.bandProcs[c].AssembleMDCT()
		if len(coeffs) > frameSize {
			coeffs = coeffs[:frameSize]
		} else if len(coeffs) < frameSize {
			ext := make([]float64, frameSize)
			copy(ext, coeffs)
			coeffs = ext
		}
		mdctCoeffsPerCh[c] = coeffs
	}

	// Note: stereo M/S→L/R merge is handled inside quant_all_bands (quant_band_stereo).

	// Perform IMDCT per channel.
	// Transient frames (isTransient=true, lm>0) use M=2^lm separate NBase-point IMDCTs
	// (libopus "shortMdct" path). Non-transient frames use a single N-point IMDCT.
	output := make([]float64, frameSize*ch)
	nBase := d.mode.NBase // = NBase (e.g. 120 for 48kHz)
	M := 1 << uint(lm)    // number of sub-frames for transient

	for c := 0; c < ch; c++ {
		coeffs := mdctCoeffsPerCh[c]
		var samplesOut []float64

		if isTransient && lm > 0 {
			// Transient synthesis: M separate NBase-point IMDCTs.
			subTail := make([]float64, nBase)
			copy(subTail, d.overlap[c])

			samplesOut = make([]float64, frameSize)
			for k := 0; k < M; k++ {
				start := k * nBase
				end := start + nBase
				var subCoeffs []float64
				if end <= len(coeffs) {
					subCoeffs = coeffs[start:end]
				} else {
					subCoeffs = make([]float64, nBase)
					copy(subCoeffs, coeffs[start:])
				}
				subY := d.shortCeltMode.IMDCT(subCoeffs)
				subOut := d.shortCeltMode.InverseOverlapAdd(subY, subTail)
				copy(samplesOut[k*nBase:], subOut)
			}
			copy(d.overlap[c], subTail)
		} else {
			// Non-transient: single N-point IMDCT.
			y := d.celtMode.IMDCT(coeffs)
			samplesOut = d.celtMode.InverseOverlapAdd(y, d.overlap[c])
		}

		if pfEnabled {
			samplesOut = d.postFilter[c].Apply(samplesOut, pfPeriod, pfTaps)
		} else {
			d.postFilter[c].updateHistory(samplesOut)
		}

		for i := 0; i < len(samplesOut) && i < frameSize; i++ {
			output[i*ch+c] = samplesOut[i]
		}
	}

	d.lastFinalRange = dec.GetRng()
	d.frameCount++
	return output, nil
}

// LastFinalRange returns the range coder rng after the most recent Decode call.
// Used to validate bit-exact decoding against reference values.
func (d *Decoder) LastFinalRange() uint32 {
	return d.lastFinalRange
}

// CopyStateFrom transfers inter-frame CELT decoder history from another
// decoder instance. The public Opus decoder uses separate CELT instances for
// packet bandwidth/frame/channel variants, but the Opus stream has one logical
// CELT state across those variants.
func (d *Decoder) CopyStateFrom(src *Decoder) {
	if src == nil || src == d {
		return
	}

	for c := range d.overlap {
		sc := c
		if sc >= len(src.overlap) {
			sc = len(src.overlap) - 1
		}
		if sc >= 0 {
			copy(d.overlap[c], src.overlap[sc])
		}
	}

	dstBands := d.mode.Bands.NumBands
	srcBands := src.mode.Bands.NumBands
	for c := 0; c < d.mode.Channels; c++ {
		sc := c
		if sc >= src.mode.Channels {
			sc = src.mode.Channels - 1
		}
		for i := 0; i < dstBands; i++ {
			di := c*dstBands + i
			if i >= srcBands || sc < 0 {
				d.prevEnergies[di] = 0
				d.prevEnergies2[di] = -28
				continue
			}
			si := sc*srcBands + i
			d.prevEnergies[di] = src.prevEnergies[si]
			d.prevEnergies2[di] = src.prevEnergies2[si]
		}
	}

	for c := range d.postFilter {
		sc := c
		if sc >= len(src.postFilter) {
			sc = len(src.postFilter) - 1
		}
		if sc >= 0 {
			d.postFilter[c].copyFrom(src.postFilter[sc])
		}
	}

	d.lastFinalRange = src.lastFinalRange
}

// decodeBandEnergies is superseded by UnquantizeCoarseEnergy (RFC 6716 §5.1.2).
// Kept as an unexported no-op to avoid compilation errors if referenced elsewhere.
func (d *Decoder) decodeBandEnergies(_ *entcode.Decoder) error {
	return nil
}

// spreadIcdf is the libopus spread_icdf table from celt/bands.c.
// Decoded as ec_dec_icdf(ec, spread_icdf, 5); logp=5 → 32 total probability.
// Symbols: 0=SPREAD_NONE, 1=SPREAD_LIGHT, 2=SPREAD_NORMAL, 3=SPREAD_AGGRESSIVE.
var spreadIcdf = [4]uint8{25, 23, 2, 0}

// decodeBandCoeffs decodes PVQ-quantized band coefficients using bit allocation.
// remaining is the PVQ budget (totalBits - tell, before stereo/skip bits).
// Stereo parameters (intensity, dualStereo) are decoded inside computeAllocation.
// Returns intensity and dualStereo for use by the caller's M/S conversion.
func (d *Decoder) decodeBandCoeffs(dec *entcode.Decoder, lenBytes, allocTrim int, isTransient bool, spread int, tfRes, offsets []int) (intensity int, dualStereo bool, err error) {
	numBands := d.mode.Bands.NumBands
	lm := d.mode.LM
	ch := d.mode.Channels

	// libopus: bits = (len*8 << BITRES) - ec_tell_frac(dec) - 1   (Q3 / eighth-bits)
	bitsQ3 := lenBytes*8<<3 - dec.TellFrac() - 1

	// Anti-collapse reservation (Q3 = 1<<BITRES) for transient frames with LM>=2.
	// The bit itself is a RAW bit read AFTER PVQ (see below).
	antiCollapseRsv := 0
	if isTransient && lm >= 2 && bitsQ3 >= (lm+2)<<3 {
		antiCollapseRsv = 1 << 3
	}
	bitsQ3 -= antiCollapseRsv
	if bitsQ3 < 0 {
		bitsQ3 = 0
	}

	// libopus-faithful compute_allocation: pulses[] are per-band Q3 PVQ budgets,
	// balance is the leftover, codedBands the last coded band.
	pulses, eBits, finePriority, balance, intensityV, codedBands, dualStereoV := computeAllocation(dec, numBands, lm, ch, allocTrim, bitsQ3, offsets)
	intensity, dualStereo = intensityV, dualStereoV

	// Fine energy — raw bits from END (do NOT affect forward rng). FORWARD band order.
	for i := 0; i < numBands; i++ {
		fb := eBits[i]
		if fb <= 0 {
			continue
		}
		for c := 0; c < ch; c++ {
			q2 := int(dec.DecodeBits(uint(fb)))
			d.bandProcs[c].ApplyFineEnergy(i, q2, fb)
		}
	}

	// quant_all_bands: decode all PVQ bands into the interleaved normalised MDCT X[].
	M := 1 << uint(lm)
	frameLen := M * int(EBands48000[numBands])
	X := make([]float64, ch*frameLen)
	var Y []float64
	if ch == 2 {
		Y = X[frameLen:]
	}
	collapse := make([]byte, numBands*ch)
	totalBitsQ3 := lenBytes*8<<3 - antiCollapseRsv
	seed := QuantAllBands(dec, 0, numBands, X[:frameLen], Y, collapse, pulses, isTransient,
		spread, dualStereo, intensity, tfRes, totalBitsQ3, balance, lm, codedBands,
		dec.GetRng(), false)

	// Anti-collapse bit — RAW bit (ec_dec_bits), read AFTER PVQ. Does not affect rng.
	antiCollapseOn := false
	if antiCollapseRsv > 0 {
		antiCollapseOn = dec.DecodeBits(1) != 0
	}

	// Final fine energy pass — consumes any remaining raw bits after PVQ and
	// anti-collapse reservation. Like libopus, priority 0 bands are refined first.
	bitsLeft := lenBytes*8 - dec.ECTell()
	for prio := 0; prio < 2; prio++ {
		for i := 0; i < numBands && bitsLeft >= ch; i++ {
			if eBits[i] >= MaxFineEnergy || finePriority[i] != prio {
				continue
			}
			for c := 0; c < ch; c++ {
				q2 := int(dec.DecodeBits(1))
				d.bandProcs[c].ApplyFinalFineEnergy(i, q2, eBits[i])
				bitsLeft--
			}
		}
	}

	if antiCollapseOn {
		d.antiCollapse(X, collapse, pulses, lm, frameLen, seed)
	}

	// Copy decoded (unit-norm) band coefficients back into the band processors.
	for c := 0; c < ch; c++ {
		base := c * frameLen
		for i := 0; i < numBands; i++ {
			b := d.bandProcs[c].bands[i]
			s := M * int(EBands48000[i])
			if base+s+b.Size <= len(X) {
				copy(b.Coeffs, X[base+s:base+s+b.Size])
			}
		}
	}

	return intensity, dualStereo, nil
}

func (d *Decoder) antiCollapse(X []float64, collapse []byte, pulses []int, lm, frameLen int, seed uint32) {
	numBands := d.mode.Bands.NumBands
	ch := d.mode.Channels
	M := 1 << uint(lm)

	for i := 0; i < numBands; i++ {
		n0 := int(EBands48000[i+1] - EBands48000[i])
		if n0 <= 0 {
			continue
		}
		depth := ((1 + pulses[i]) / n0) >> uint(lm)
		thresh := 0.5 * math.Exp2(-0.125*float64(depth))
		sqrt1 := 1.0 / math.Sqrt(float64(n0*M))

		for c := 0; c < ch; c++ {
			prev1 := d.prevEnergies[c*numBands+i]
			prev2 := d.prevEnergies2[c*numBands+i]
			if ch == 1 && len(d.prevEnergies) >= 2*numBands {
				prev1 = max(prev1, d.prevEnergies[numBands+i])
				prev2 = max(prev2, d.prevEnergies2[numBands+i])
			}

			energy := d.bandProcs[c].bands[i].Energy
			if energy < 1e-20 {
				energy = 1e-20
			}
			logE := 0.5*math.Log2(energy) - EMean(i)
			eDiff := logE - min(prev1, prev2)
			if eDiff < 0 {
				eDiff = 0
			}

			r := 2.0 * math.Exp2(-eDiff)
			if lm == 3 {
				r *= math.Sqrt2
			}
			r = min(thresh, r) * sqrt1

			offset := c*frameLen + int(EBands48000[i])*M
			if offset+n0*M > len(X) {
				continue
			}
			renorm := false
			mask := collapse[i*ch+c]
			for k := 0; k < M; k++ {
				if mask&(1<<uint(k)) != 0 {
					continue
				}
				for j := 0; j < n0; j++ {
					seed = celtLCGRand(seed)
					v := r
					if seed&0x8000 == 0 {
						v = -v
					}
					X[offset+(j<<uint(lm))+k] = v
				}
				renorm = true
			}
			if renorm {
				renormaliseVector(X[offset:], n0*M, 1.0)
			}
		}
	}
}

// decodeLoss performs packet loss concealment
func (d *Decoder) decodeLoss() []float64 {
	// Simple PLC: fade out previous frame
	output := make([]float64, d.mode.FrameSize*d.mode.Channels)

	for ch := 0; ch < d.mode.Channels; ch++ {
		// Fade out overlap buffer
		for i := 0; i < len(d.overlap[ch]) && i < d.mode.FrameSize; i++ {
			fade := 1.0 - float64(i)/float64(d.mode.FrameSize)
			output[i*d.mode.Channels+ch] = d.overlap[ch][i] * fade * 0.5
		}
	}

	// Decay previous energies in log2-amplitude domain: subtract log2(1/0.8)
	const logDecay = 0.32193 // log2(1.25) ≈ 0.322
	for i := range d.prevEnergies {
		d.prevEnergies[i] -= logDecay
		d.prevEnergies2[i] -= logDecay
	}

	return output
}

// DecodePLC performs packet loss concealment (public API)
func (d *Decoder) DecodePLC() ([]float64, error) {
	return d.decodeLoss(), nil
}

// Reset resets the decoder state
func (d *Decoder) Reset() {
	// Clear overlap buffers
	for ch := 0; ch < d.mode.Channels; ch++ {
		for i := range d.overlap[ch] {
			d.overlap[ch][i] = 0
		}
	}

	// Reset energy history.
	for i := range d.prevEnergies {
		d.prevEnergies[i] = 0
		d.prevEnergies2[i] = -28.0
	}

	// Reset post-filters
	for _, pf := range d.postFilter {
		pf.Reset()
	}

	d.frameCount = 0
}

// decodeDynalloc decodes per-band dynamic allocation boosts between alloc_trim and
// compute_allocation. Matches libopus celt_decode_with_ec dynalloc loop.
// Returns Q3 boost per band. Even when all boosts are 0, the range coder state advances.
func decodeDynalloc(dec *entcode.Decoder, numBands, lm, ch, totalBits int) []int {
	offsets := make([]int, numBands)
	dynallocLogp := 6
	// libopus: total_bits<<=BITRES (Q3), decreases as boosts are applied;
	// tell tracked via ec_tell_frac (Q3).
	totalQ3 := totalBits << 3
	tell := dec.TellFrac()

	for j := 0; j < numBands; j++ {
		N0 := int(EBands48000[j+1] - EBands48000[j])
		M := N0 << uint(lm)
		width := ch * M
		// quanta = IMIN(width<<BITRES, IMAX(6<<BITRES, width))
		quanta := width << 3
		hi := width
		if hi < 48 {
			hi = 48
		}
		if quanta > hi {
			quanta = hi
		}
		// cap[j] = (caps[nbEBands*(2*LM+C-1)+j]+64)*C*N>>2  (boost upper bound)
		capsIdx := NumBands48000*(2*lm+ch-1) + j
		capVal := 255
		if capsIdx >= 0 && capsIdx < len(CacheCaps50) {
			capVal = int(CacheCaps50[capsIdx])
		}
		capj := (capVal + 64) * width >> 2

		loopLogp := dynallocLogp
		boost := 0
		for tell+(loopLogp<<3) < totalQ3 && boost < capj {
			flag := dec.DecodeBitLogp(uint(loopLogp))
			tell = dec.TellFrac()
			if !flag {
				break
			}
			boost += quanta
			totalQ3 -= quanta
			loopLogp = 1
		}
		offsets[j] = boost
		if boost > 0 {
			dynallocLogp--
			if dynallocLogp < 2 {
				dynallocLogp = 2
			}
		}
	}
	return offsets
}

// tfSelectTable[lm][8] matches libopus tf_select_table in celt/bands.c.
// Index layout: [4*isTransient + 2*tfSelect + tfChanged].
var tfSelectTable = [4][8]int{
	{0, -1, 0, -1, 0, -1, 0, -1}, // LM=0
	{0, -1, 0, -2, 1, 0, 1, -1},  // LM=1
	{0, -2, 0, -3, 2, 0, 1, -1},  // LM=2
	{0, -2, 0, -3, 3, 0, 1, -1},  // LM=3
}

// celtTFDecode reads the time-frequency allocation bits and returns tf_res[i]
// per band. Faithful port of libopus tf_decode() (celt/bands.c).
func celtTFDecode(dec *entcode.Decoder, totalBits int, isTransient bool, numBands, lm int) []int {
	tfRes := make([]int, numBands)
	isT := 0
	if isTransient {
		isT = 1
	}
	budget := totalBits // dec->storage*8
	logp := 4
	if isTransient {
		logp = 2
	}
	// tf_select_rsv = LM>0 && ec_tell+logp+1 <= budget
	tfSelectRsv := 0
	if lm > 0 && dec.ECTell()+logp+1 <= budget {
		tfSelectRsv = 1
		budget--
	}

	curr := 0
	tfChanged := 0
	for i := 0; i < numBands; i++ {
		if dec.ECTell()+logp <= budget {
			if dec.DecodeBitLogp(uint(logp)) {
				curr ^= 1
			}
			tfChanged |= curr
		}
		tfRes[i] = curr
		if isTransient {
			logp = 4
		} else {
			logp = 5
		}
	}

	lmC := lm
	if lmC < 0 {
		lmC = 0
	}
	if lmC > 3 {
		lmC = 3
	}
	tfSelect := 0
	if tfSelectRsv != 0 &&
		tfSelectTable[lmC][4*isT+0+tfChanged] != tfSelectTable[lmC][4*isT+2+tfChanged] {
		if dec.DecodeBitLogp(1) {
			tfSelect = 1
		}
	}
	for i := 0; i < numBands; i++ {
		tfRes[i] = tfSelectTable[lmC][4*isT+2*tfSelect+tfRes[i]]
	}
	return tfRes
}
