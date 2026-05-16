package celt

import (
	"errors"
	"math"

	"github.com/darui3018823/opus/internal/dsp"
	"github.com/darui3018823/opus/internal/entcode"
)

// Decoder is a CELT decoder instance
type Decoder struct {
	mode     *Mode
	celtMode *dsp.CELTMode
	bandProc *BandProcessor
	overlap  [][]float64 // Overlap buffer per channel (120 samples each)

	// Decoder state — two-tap energy history required by RFC 6716 §5.1.2
	prevEnergies  []float64 // Previous frame log-energies (ln domain)
	prevEnergies2 []float64 // Two-frames-ago log-energies (ln domain)
	frameCount    int       // Counts frames to decide intra/inter mode

	// Post-filter (one per channel)
	postFilter []*PostFilter
}

// NewDecoder creates a new CELT decoder
func NewDecoder(frameSize, sampleRate, channels int) (*Decoder, error) {
	if channels < 1 || channels > 2 {
		return nil, errors.New("celt: only mono and stereo supported")
	}

	mode := NewMode(frameSize, sampleRate, channels)

	celtMode := dsp.NewCELTMode(frameSize, MaxOverlap, Window120[:])

	d := &Decoder{
		mode:     mode,
		celtMode: celtMode,
		bandProc: NewBandProcessor(mode),
		overlap:  make([][]float64, channels),
	}

	for i := 0; i < channels; i++ {
		d.overlap[i] = make([]float64, MaxOverlap)
	}

	// Initialize energy history in log2-amplitude domain (matches libopus -28.0f).
	d.prevEnergies = make([]float64, mode.Bands.NumBands)
	d.prevEnergies2 = make([]float64, mode.Bands.NumBands)
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

	// Read intra/inter bit from bitstream (RFC 6716 §5.1.2 / libopus celt_decode_with_ec).
	// Mirrors the encoder: ec_dec_bit_logp(dec, 3) if budget allows.
	var intra bool
	tell := dec.Tell()
	if tell+3 <= totalBits {
		intra = dec.DecodeBitLogp(3)
	} else {
		intra = false
	}

	// RFC 6716 §5.1.2 — decode coarse band log-energies
	numBands := d.mode.Bands.NumBands
	lm := 3 // 20 ms → lm=3
	quantLogE := UnquantizeCoarseEnergy(
		dec,
		d.prevEnergies,
		d.prevEnergies2,
		intra,
		numBands,
		lm,
		d.mode.Channels,
		totalBits,
	)

	// Convert mean-subtracted log2-amplitude to linear energy.
	// actual_log2_amp = quantLogE[i] + eMeans[i]; linear energy = 2^(2*actual_log2_amp).
	for i := 0; i < numBands; i++ {
		actualLog2Amp := quantLogE[i] + EMean(i)
		amp := math.Exp2(actualLog2Amp)
		e := amp * amp
		if e < 1e-20 {
			e = 1e-20
		}
		d.bandProc.bands[i].Energy = e
	}

	// Update energy history
	copy(d.prevEnergies2, d.prevEnergies)
	copy(d.prevEnergies, quantLogE)

	d.bandProc.InterpolateBandEnergies()

	// Decode post-filter parameters (RFC 6716 §5.4.1) — BEFORE band coefficients.
	// logp=3 per libopus; two sets for LM>1 (10ms/20ms frames).
	pfPeriod, pfTaps, pfEnabled := DecodePostFilterParams(dec, totalBits, lm)

	// Read allocation trim (7-bit ICDF, 11 symbols 0-10; default 5 = neutral).
	allocTrim := 5
	if dec.Tell()+6 <= totalBits {
		allocTrim = dec.DecodeIcdf(TrimICDF[:], 7)
	}

	// For stereo: read dual-stereo flag and intensity-stereo boundary.
	// These bits must be consumed even if we don't fully use them.
	intensity := 0
	if d.mode.Channels == 2 {
		if dec.Tell()+1 <= totalBits {
			_ = dec.DecodeBitLogp(1) // dual_stereo flag
		}
		if dec.Tell()+1 <= totalBits {
			intensity = int(dec.DecodeUint(uint32(numBands + 1)))
		}
	}
	_ = intensity

	// Decode band coefficients using PVQ
	if err := d.decodeBandCoeffs(dec, totalBits, allocTrim); err != nil {
		return nil, err
	}

	// Apply energy denormalization
	d.bandProc.DenormalizeBands()

	// Assemble full MDCT spectrum
	mdctCoeffs := d.bandProc.AssembleMDCT()

	// Trim/pad MDCT spectrum to exactly frameSize coefficients.
	frameSize := d.mode.FrameSize
	if len(mdctCoeffs) > frameSize {
		mdctCoeffs = mdctCoeffs[:frameSize]
	} else if len(mdctCoeffs) < frameSize {
		ext := make([]float64, frameSize)
		copy(ext, mdctCoeffs)
		mdctCoeffs = ext
	}

	// Perform IMDCT for each channel
	output := make([]float64, d.mode.FrameSize*d.mode.Channels)

	for ch := 0; ch < d.mode.Channels; ch++ {
		// IMDCT (N=960, 2N-point DFT, small-overlap window)
		y := d.celtMode.IMDCT(mdctCoeffs)
		// Overlap-add with 120-sample tail
		samplesOut := d.celtMode.InverseOverlapAdd(y, d.overlap[ch])

		// Apply post-filter (RFC 6716 §5.4.1) if enabled
		if pfEnabled {
			samplesOut = d.postFilter[ch].Apply(samplesOut, pfPeriod, pfTaps)
		} else {
			d.postFilter[ch].updateHistory(samplesOut)
		}

		// Interleave into output
		for i := 0; i < len(samplesOut) && i < d.mode.FrameSize; i++ {
			output[i*d.mode.Channels+ch] = samplesOut[i]
		}
	}

	d.frameCount++
	return output, nil
}

// decodeBandEnergies is superseded by UnquantizeCoarseEnergy (RFC 6716 §5.1.2).
// Kept as an unexported no-op to avoid compilation errors if referenced elsewhere.
func (d *Decoder) decodeBandEnergies(_ *entcode.Decoder) error {
	return nil
}

// decodeBandCoeffs decodes PVQ-quantized band coefficients using bit allocation.
// totalBits is the full packet bit count; allocTrim is the decoded alloc trim (0-10, default 5).
func (d *Decoder) decodeBandCoeffs(dec *entcode.Decoder, totalBits, allocTrim int) error {
	if totalBits < 0 {
		totalBits = 0
	}

	numBands := d.mode.Bands.NumBands
	lm := 3 // 20ms
	ch := d.mode.Channels

	// Compute bits remaining for PVQ after what's been consumed so far.
	consumed := dec.Tell()
	remaining := totalBits - consumed
	if remaining < 0 {
		remaining = 0
	}

	// Run libopus-style compute_allocation.
	pulses, eBits := computeAllocation(numBands, lm, ch, allocTrim, remaining)

	// Decode PVQ per band.
	for i := 0; i < numBands; i++ {
		band := d.bandProc.bands[i]
		k := pulses[i]

		if k <= 0 || band.Size <= 0 {
			continue
		}
		if icwrs(band.Size, k) == 0 {
			continue
		}
		d.bandProc.DecodeBandCoeffs(dec, i, k)
	}

	// Fine energy refinement — raw bits from END of packet.
	for i := numBands - 1; i >= 0; i-- {
		fb := eBits[i]
		if fb <= 0 {
			continue
		}
		raw := dec.DecodeBits(uint(fb))
		// Center: [0, 2^fb) → [-2^(fb-1), 2^(fb-1))
		half := int(uint(1) << uint(fb-1))
		d.bandProc.ApplyFineEnergy(i, int(raw)-half)
	}

	return nil
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
