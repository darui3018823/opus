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

	// Decoder state
	prevEnergies []float64 // Previous frame energies for interpolation
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

	// Initialize previous energies
	d.prevEnergies = make([]float64, mode.Bands.NumBands)
	for i := range d.prevEnergies {
		d.prevEnergies[i] = 1.0 // Start with unit energy
	}

	return d, nil
}

// Decode decodes a CELT frame to PCM samples
func (d *Decoder) Decode(frameData []byte) ([]float64, error) {
	if len(frameData) == 0 {
		return d.decodeLoss(), nil
	}

	// Initialize range decoder
	dec := entcode.NewDecoder(frameData)

	// Decode band energies
	if err := d.decodeBandEnergies(dec); err != nil {
		return nil, err
	}

	// Decode band coefficients using PVQ
	if err := d.decodeBandCoeffs(dec); err != nil {
		return nil, err
	}

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

		// Interleave into output
		for i := 0; i < len(samplesOut) && i < d.mode.FrameSize; i++ {
			output[i*d.mode.Channels+ch] = samplesOut[i]
		}
	}

	return output, nil
}

// laplaceICDF tables for coarse energy decoding.
// These approximate the Laplace distribution at different decay rates.
// Lower bands use a narrower distribution (smaller spread), higher bands wider.
// Each table is a descending ICDF with total probability 1<<7 = 128.
// The DecodeIcdf function in entcode expects []uint8.
var (
	// Narrow Laplace for low-frequency bands (bands 0-5)
	laplaceNarrowICDF = []uint8{
		127, 124, 118, 106, 88, 68, 48, 32, 20, 12, 6, 3, 1, 0,
	}
	// Medium Laplace for mid-frequency bands (bands 6-13)
	laplaceMediumICDF = []uint8{
		127, 122, 112, 96, 76, 58, 42, 30, 20, 13, 8, 5, 3, 1, 0,
	}
	// Wide Laplace for high-frequency bands (bands 14+)
	laplaceWideICDF = []uint8{
		127, 120, 106, 88, 68, 50, 36, 25, 17, 11, 7, 4, 2, 1, 0,
	}
)

// decodeLaplace decodes a Laplace-coded signed integer using the range decoder.
// bandIdx selects the Laplace distribution width.
func decodeLaplace(dec *entcode.Decoder, bandIdx int) int {
	// Select ICDF table based on band index
	var icdf []uint8
	switch {
	case bandIdx < 6:
		icdf = laplaceNarrowICDF
	case bandIdx < 14:
		icdf = laplaceMediumICDF
	default:
		icdf = laplaceWideICDF
	}

	// Decode unsigned magnitude using the ICDF table (ftb=7 means ft=128)
	sym := dec.DecodeIcdf(icdf, 7)

	// Convert symbol index to signed value.
	// Symbol 0 → 0, symbols 1,2 → +1,-1, symbols 3,4 → +2,-2, etc.
	if sym == 0 {
		return 0
	}
	magnitude := (sym + 1) / 2
	if sym&1 == 0 {
		return -magnitude
	}
	return magnitude
}

// decodeBandEnergies decodes band energies from the bitstream.
// Uses log-domain coarse energy with inter-frame prediction,
// per RFC 6716 §5.4.1.
func (d *Decoder) decodeBandEnergies(dec *entcode.Decoder) error {
	// Inter-frame prediction coefficient (~0.906, matching RFC 6716)
	const alpha = 29.0 / 32.0
	// Mean log-energy per band (used as base prediction; simplified to a
	// gentle spectral tilt).
	const tiltPerBand = -0.03

	for i := 0; i < d.mode.Bands.NumBands; i++ {
		// Decode Laplace-coded residual
		residual := decodeLaplace(dec, i)

		// Predicted energy in log2 domain
		predicted := alpha*d.prevEnergies[i] + tiltPerBand*float64(i)

		// Reconstruct log2 energy: residual is in half-steps (~3 dB each)
		logEnergy := predicted + float64(residual)*0.5

		// Update state
		d.prevEnergies[i] = logEnergy

		// Convert from log2 domain to linear energy and apply to band
		d.bandProc.bands[i].Energy = math.Exp2(logEnergy)
	}

	// Interpolate any bands that ended up with negligible energy
	d.bandProc.InterpolateBandEnergies()

	return nil
}

// decodeBandCoeffs decodes PVQ-quantized band coefficients using bit allocation
func (d *Decoder) decodeBandCoeffs(dec *entcode.Decoder) error {
	// Compute bit budget from remaining bytes in the range decoder
	totalBits := dec.BytesLeft() * 8
	if totalBits < 0 {
		totalBits = 0
	}

	// Collect band energies for allocation
	bandEnergies := make([]float64, d.mode.Bands.NumBands)
	for i, band := range d.bandProc.bands {
		bandEnergies[i] = band.Energy
	}

	// Run bit allocation
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

	// Decay previous energies
	for i := range d.prevEnergies {
		d.prevEnergies[i] *= 0.8
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

	// Reset energies
	for i := range d.prevEnergies {
		d.prevEnergies[i] = 1.0
	}
}
