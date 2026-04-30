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

	// Capture total packet bits BEFORE any decoding so that the bit
	// allocation in decodeBandCoeffs uses the same budget as the encoder.
	totalBits := len(frameData) * 8

	// Initialize range decoder
	dec := entcode.NewDecoder(frameData)

	// Decode band energies
	if err := d.decodeBandEnergies(dec); err != nil {
		return nil, err
	}

	// Decode band coefficients using PVQ
	if err := d.decodeBandCoeffs(dec, totalBits); err != nil {
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

// decodeBandEnergies decodes band energies from the bitstream.
// Mirrors encodeBandEnergies in encoder.go exactly: same Laplace parameters,
// same natural-log prediction model, same quantization step.
func (d *Decoder) decodeBandEnergies(dec *entcode.Decoder) error {
	for i := 0; i < d.mode.Bands.NumBands; i++ {
		// Decode residual using same Laplace params as encoder (fs=6000, decay=6000)
		residual := dec.DecodeLaplace(6000, 6000)

		// Temporal prediction mirrors encoder: prevEnergies in linear domain.
		predicted := d.prevEnergies[i] * 0.9
		predictedLog := 0.0
		if predicted > 1e-10 {
			predictedLog = math.Log(predicted)
		}

		// Reconstruct log energy from quantized residual.
		// Encoder: quantized = round(diff * 2.0)  →  diff = residual * 0.5
		diff := float64(residual) * 0.5
		logEnergy := predictedLog + diff

		// Convert to linear; clamp to avoid zero/denormals.
		energy := math.Exp(logEnergy)
		if energy < 1e-10 {
			energy = 1e-10
		}

		// Update state in linear domain (same as encoder's prevBandEnergies).
		d.prevEnergies[i] = energy
		d.bandProc.bands[i].Energy = energy
	}

	d.bandProc.InterpolateBandEnergies()
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
