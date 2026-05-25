package celt

import (
	"errors"
	"math"

	"github.com/darui3018823/opus/internal/dsp"
	"github.com/darui3018823/opus/internal/entcode"
)

// Decoder is a CELT decoder instance
type Decoder struct {
	mode      *Mode
	celtMode  *dsp.CELTMode
	bandProcs [2]*BandProcessor // band processors per channel (index 0 = L/M, index 1 = R/S)
	overlap   [][]float64       // Overlap buffer per channel (120 samples each)

	// Decoder state — two-tap energy history required by RFC 6716 §5.1.2
	// prevEnergies[i*C+c] = actual log2-amplitude for band i, channel c.
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

	d := &Decoder{
		mode:     mode,
		celtMode: celtMode,
		overlap:  make([][]float64, channels),
	}
	d.bandProcs[0] = NewBandProcessor(mode)
	if channels == 2 {
		d.bandProcs[1] = NewBandProcessor(mode)
	}

	for i := 0; i < channels; i++ {
		d.overlap[i] = make([]float64, mode.Overlap)
	}

	// Initialize energy history in log2-amplitude domain (matches libopus -28.0f).
	// Size = numBands * channels: stereo stores [band*C+ch] layout.
	nEBands := mode.Bands.NumBands * channels
	d.prevEnergies = make([]float64, nEBands)
	d.prevEnergies2 = make([]float64, nEBands)
	for i := range d.prevEnergies {
		d.prevEnergies[i] = -28.0
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

	// Read isTransient bit — written BEFORE intra by encoder when LM>0 (RFC 6716 §5.2.2).
	isTransient := false
	if lm > 0 && dec.Tell()+3 <= totalBits {
		isTransient = dec.DecodeBitLogp(3)
	}

	// Read intra/inter bit for coarse energy coding (RFC 6716 §5.1.2).
	var intra bool
	if dec.Tell()+3 <= totalBits {
		intra = dec.DecodeBitLogp(3)
	}

	// RFC 6716 §5.1.2 — decode coarse band log-energies
	quantLogE := UnquantizeCoarseEnergy(
		dec,
		d.prevEnergies,
		d.prevEnergies2,
		intra,
		numBands,
		lm,
		ch,
		totalBits,
	)

	// Read time-frequency allocation bits (RFC 6716 §5.2.2).
	// These must be consumed even though we don't implement TF transforms.
	celtTFDecode(dec, totalBits, isTransient, numBands, lm)

	// quantLogE[i*C+c] = actual log2-amplitude for band i, channel c.
	for i := 0; i < numBands; i++ {
		for c := 0; c < ch; c++ {
			amp := math.Exp2(quantLogE[i*ch+c])
			e := amp * amp
			if e < 1e-20 {
				e = 1e-20
			}
			d.bandProcs[c].bands[i].Energy = e
		}
	}

	// Update energy history (size = numBands * channels)
	copy(d.prevEnergies2, d.prevEnergies)
	copy(d.prevEnergies, quantLogE)

	d.bandProcs[0].InterpolateBandEnergies()
	if ch == 2 {
		d.bandProcs[1].InterpolateBandEnergies()
	}

	// Decode post-filter parameters (RFC 6716 §5.4.1) — BEFORE band coefficients.
	pfPeriod, pfTaps, pfEnabled := DecodePostFilterParams(dec, totalBits, lm)

	// Read allocation trim (7-bit ICDF, 11 symbols 0-10; default 5 = neutral).
	allocTrim := 5
	if dec.Tell()+6 <= totalBits {
		allocTrim = dec.DecodeIcdf(TrimICDF[:], 7)
	}

	// Compute bit budget for PVQ allocation (after alloc_trim, before stereo/skip bits).
	// Stereo parameters and skip bits are read inside computeAllocation, matching libopus.
	remaining := totalBits - dec.Tell()
	if remaining < 0 {
		remaining = 0
	}

	// Decode band coefficients using PVQ (stereo params decoded inside decodeBandCoeffs).
	_, dualStereo, err := d.decodeBandCoeffs(dec, remaining, allocTrim, isTransient, totalBits)
	if err != nil {
		return nil, err
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

	// For M/S stereo: convert M→L, S→R via stereo_merge (L=(M+S)/√2, R=(M-S)/√2).
	// bandProcs[0] holds M, bandProcs[1] holds S.
	if ch == 2 && !dualStereo {
		invSqrt2 := 1.0 / math.Sqrt(2.0)
		M := mdctCoeffsPerCh[0]
		S := mdctCoeffsPerCh[1]
		L := make([]float64, frameSize)
		R := make([]float64, frameSize)
		for j := 0; j < frameSize; j++ {
			L[j] = (M[j] + S[j]) * invSqrt2
			R[j] = (M[j] - S[j]) * invSqrt2
		}
		mdctCoeffsPerCh[0] = L
		mdctCoeffsPerCh[1] = R
	}

	// Perform IMDCT per channel
	output := make([]float64, frameSize*ch)
	for c := 0; c < ch; c++ {
		y := d.celtMode.IMDCT(mdctCoeffsPerCh[c])
		samplesOut := d.celtMode.InverseOverlapAdd(y, d.overlap[c])

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
func (d *Decoder) decodeBandCoeffs(dec *entcode.Decoder, remaining, allocTrim int, isTransient bool, totalBits int) (intensity int, dualStereo bool, err error) {
	if remaining < 0 {
		remaining = 0
	}

	numBands := d.mode.Bands.NumBands
	lm := d.mode.LM
	ch := d.mode.Channels

	// Anti-collapse reservation: libopus subtracts 1 raw-bit (= 8 Q3-bits) from allocation
	// budget for transient frames with LM>=2. The actual bit is read from the range coder
	// AFTER compute_allocation and fine energy, but BEFORE PVQ.
	antiCollapseRsv := 0
	if isTransient && lm >= 2 && remaining >= lm+2 {
		antiCollapseRsv = 1
		remaining--
	}

	// Run libopus-faithful compute_allocation (reads skip bits and stereo params from dec).
	var pulses, eBits []int
	pulses, eBits, intensity, dualStereo = computeAllocation(dec, numBands, lm, ch, allocTrim, remaining)

	// Fine energy refinement — raw bits from END of packet (independent of range coder).
	// libopus unquant_fine_energy iterates FORWARD (band 0 → numBands-1).
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

	// Anti-collapse bit — range coder, read BEFORE spread and PVQ.
	if antiCollapseRsv > 0 && dec.Tell()+1 <= totalBits {
		dec.DecodeBitLogp(1) // anti_collapse_on (not used for PLC in this implementation)
	}

	// Spread decision — range coder, read BEFORE PVQ (libopus quant_all_bands, decode mode).
	// libopus reads spread for ALL frames (mono and stereo); no C>1 guard in the source.
	if dec.Tell()+4 <= totalBits {
		_ = dec.DecodeIcdf(spreadIcdf[:], 5)
	}

	// Decode PVQ per band.
	for i := 0; i < numBands; i++ {
		k := pulses[i]
		bandSize := d.bandProcs[0].bands[i].Size

		if k <= 0 || bandSize <= 0 || icwrs(bandSize, k) == 0 {
			continue
		}

		if ch == 1 || i >= intensity {
			// Mono or intensity stereo: only the M/combined channel (ch0) is coded.
			d.bandProcs[0].DecodeBandCoeffs(dec, i, k)
		} else {
			// M/S or dual-stereo: decode both channels using same pulse count.
			d.bandProcs[0].DecodeBandCoeffs(dec, i, k)
			if icwrs(d.bandProcs[1].bands[i].Size, k) != 0 {
				d.bandProcs[1].DecodeBandCoeffs(dec, i, k)
			}
		}
	}

	return intensity, dualStereo, nil
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

	// Reset energy history
	for i := range d.prevEnergies {
		d.prevEnergies[i] = -28.0
		d.prevEnergies2[i] = -28.0
	}

	// Reset post-filters
	for _, pf := range d.postFilter {
		pf.Reset()
	}

	d.frameCount = 0
}

// tfSelectTable[lm][8] matches libopus tf_select_table in celt/bands.c.
// Index layout: [4*isTransient + 2*tfSelect + tfChanged].
var tfSelectTable = [4][8]int{
	{0, -1, 0, -1, 0, -1, 0, -1}, // LM=0
	{0, -1, 0, -2, 1, 0, 1, -1},  // LM=1
	{0, -2, 0, -3, 2, 0, 1, -1},  // LM=2
	{0, -2, 0, -3, 3, 0, 2, -1},  // LM=3
}

// celtTFDecode reads the time-frequency allocation bits from the range coder.
// Matches libopus tf_decode() in celt/bands.c.
// The TF transform is not implemented; this function only consumes the bits.
func celtTFDecode(dec *entcode.Decoder, totalBits int, isTransient bool, numBands, lm int) {
	if lm == 0 {
		return
	}
	logp := 4
	if isTransient {
		logp = 2
	}

	budget := totalBits
	tell := dec.Tell()

	// Reserve one bit for tf_select if budget allows.
	tfSelectRsv := lm > 0 && tell+logp+1 <= budget
	if tfSelectRsv {
		budget--
	}

	curr := 0
	tfChanged := 0
	for i := 0; i < numBands; i++ {
		tell = dec.Tell()
		if tell+logp <= budget {
			if dec.DecodeBitLogp(uint(logp)) {
				curr ^= 1
			}
			tell = dec.Tell()
		}
		if curr != 0 {
			tfChanged = 1
		}
		if isTransient {
			logp = 4
		} else {
			logp = 5
		}
		_ = tell
	}

	// Read tf_select ONLY if it would produce a different result (libopus condition).
	// For non-transient frames with tfChanged=0, table[lm][0]==table[lm][2]==0, so skip.
	if tfSelectRsv {
		lmC := lm
		if lmC < 0 {
			lmC = 0
		}
		if lmC > 3 {
			lmC = 3
		}
		isT := 0
		if isTransient {
			isT = 1
		}
		if tfSelectTable[lmC][4*isT+0+tfChanged] != tfSelectTable[lmC][4*isT+2+tfChanged] {
			dec.DecodeBitLogp(1)
		}
	}
}
