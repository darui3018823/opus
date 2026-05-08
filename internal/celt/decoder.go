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
	mdct     *dsp.MDCT
	bandProc *BandProcessor
	overlap  [][]float64 // Overlap buffer per channel

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

	// MDCT size must be power of 2
	// For CELT, we use the next power of 2 >= frameSize/2
	mdctSize := 1
	for mdctSize < frameSize {
		mdctSize *= 2
	}

	mdct, err := dsp.NewMDCT(mdctSize)
	if err != nil {
		return nil, err
	}

	d := &Decoder{
		mode:     mode,
		mdct:     mdct,
		bandProc: NewBandProcessor(mode),
		overlap:  make([][]float64, channels),
	}

	// Initialize overlap buffers (size N, where MDCT outputs 2*N samples)
	for i := 0; i < channels; i++ {
		d.overlap[i] = make([]float64, mdctSize)
	}

	// Initialize energy history in log (ln) domain.
	// libopus initialises prevLogE to -28 dB_log2 ≈ -19.4 nats.
	initLogE := math.Log(1e-8) // very small initial energy
	d.prevEnergies = make([]float64, mode.Bands.NumBands)
	d.prevEnergies2 = make([]float64, mode.Bands.NumBands)
	for i := range d.prevEnergies {
		d.prevEnergies[i] = initLogE
		d.prevEnergies2[i] = initLogE
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

	// Decide intra vs inter mode: first frame is always intra.
	intra := d.frameCount == 0

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
	)

	// Convert log-energies (ln domain) to linear and store in bandProc
	for i := 0; i < numBands; i++ {
		e := math.Exp(quantLogE[i])
		if e < 1e-10 {
			e = 1e-10
		}
		d.bandProc.bands[i].Energy = e
	}

	// Update energy history
	copy(d.prevEnergies2, d.prevEnergies)
	copy(d.prevEnergies, quantLogE)

	d.bandProc.InterpolateBandEnergies()

	// Decode band coefficients using PVQ
	if err := d.decodeBandCoeffs(dec, totalBits); err != nil {
		return nil, err
	}

	// Decode post-filter parameters (RFC 6716 §5.4.1)
	// Post-filter params appear in the bitstream after energy, before PVQ
	// in libopus — but our simplified pipeline reads them here.
	pfPeriod, pfTaps, pfEnabled := DecodePostFilterParams(dec, d.mode.FrameSize)

	// Apply energy denormalization
	d.bandProc.DenormalizeBands()

	// Assemble full MDCT spectrum
	mdctCoeffs := d.bandProc.AssembleMDCT()

	// Ensure we have enough coefficients for MDCT
	mdctSize := d.mdct.Size()
	if len(mdctCoeffs) < mdctSize {
		extended := make([]float64, mdctSize)
		copy(extended, mdctCoeffs)
		mdctCoeffs = extended
	} else if len(mdctCoeffs) > mdctSize {
		mdctCoeffs = mdctCoeffs[:mdctSize]
	}

	// Perform IMDCT for each channel
	output := make([]float64, d.mode.FrameSize*d.mode.Channels)

	for ch := 0; ch < d.mode.Channels; ch++ {
		// IMDCT with overlap-add
		samples, err := d.mdct.InverseOverlap(mdctCoeffs, d.overlap[ch])
		if err != nil {
			return nil, err
		}

		// Truncate or pad to frame size
		samplesOut := samples
		if len(samples) > d.mode.FrameSize {
			samplesOut = samples[:d.mode.FrameSize]
		}

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
// totalBits is the full packet bit count captured before any decoding.
func (d *Decoder) decodeBandCoeffs(dec *entcode.Decoder, totalBits int) error {
	if totalBits < 0 {
		totalBits = 0
	}

	// Collect band energies for allocation
	bandEnergies := make([]float64, d.mode.Bands.NumBands)
	for i, band := range d.bandProc.bands {
		bandEnergies[i] = band.Energy
	}

	// Run bit allocation with full packet budget (mirrors encoder)
	ba := NewBitAllocation(d.mode, totalBits)
	if err := ba.Allocate(bandEnergies); err != nil {
		return err
	}

	for i := 0; i < d.mode.Bands.NumBands; i++ {
		band := d.bandProc.bands[i]
		pulses := ba.GetPulseCount(i)

		if pulses <= 0 || band.Size <= 0 {
			continue
		}

		if icwrs(band.Size, pulses) == 0 {
			continue
		}

		// Decode using recursive PVQ splitting
		d.bandProc.DecodeBandCoeffs(dec, i, pulses)

		// Fine energy refinement
		fineBits := ba.GetFineEnergy(i)
		if fineBits > 0 {
			fineRange := uint32(1) << uint(fineBits)
			fineVal := int(dec.DecodeUint(fineRange))
			// Center around zero
			d.bandProc.ApplyFineEnergy(i, fineVal-int(fineRange/2))
		}
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

	// Decay previous energies (in log domain: subtract ln(1/0.8) ≈ 0.223)
	const logDecay = 0.22314 // ln(0.8) magnitude
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
	initLogE := math.Log(1e-8)
	for i := range d.prevEnergies {
		d.prevEnergies[i] = initLogE
		d.prevEnergies2[i] = initLogE
	}

	// Reset post-filters
	for _, pf := range d.postFilter {
		pf.Reset()
	}

	d.frameCount = 0
}
