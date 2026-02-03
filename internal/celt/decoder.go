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

// decodeBandEnergies decodes the band energies from bitstream
// decodeBandEnergies decodes the band energies from bitstream
func (d *Decoder) decodeBandEnergies(dec *entcode.Decoder) error {
	// Uses Laplace distribution for coarse energy
	// Matches Encoder's encodeBandEnergies

	energyBits := make([]int, d.mode.Bands.NumBands)

	for i := 0; i < d.mode.Bands.NumBands; i++ {
		// Temporal prediction
		predicted := d.prevEnergies[i]
		// In strictly compliant CELT, prediction depends on transient flag, etc.
		// Matching encoder: predicted *= 0.9 if !transient (assuming persistent for now)
		predicted *= 0.9

		predictedLog := 0.0
		if predicted > 1e-10 {
			predictedLog = math.Log(predicted)
		}

		// Decode quantized difference
		// Using placeholder stats fs=6000, decay=6000 used in encoder
		quantized := dec.DecodeLaplace(6000, 6000)

		// Reconstruct log energy
		diff := float64(quantized) / 2.0
		logEnergy := predictedLog + diff

		d.prevEnergies[i] = math.Exp(logEnergy)

		// For bandProc (which expects int "energyBits" in old format), we might need to adjust.
		// But wait, bandProc.DecodeBandEnergies expects "energyBits".
		// Actually, let's look at bandProc.DecodeBandEnergies.
		// It takes `energyBits []int` and does `logEnergy := float64(energyBits[i]) * 0.5`.
		// So `energyBits[i]` SHOULD BE `quantized`?
		// No, `energyBits` in the old code represented the ABSOLUTE log energy in 0.5dB steps.
		// Here `quantized` is the DIFFERENCE.

		// We need to pass the ABSOLUTE quantized value to bandProc?
		// Or update bandProc to take float energies directly?
		// Let's look at bandProc.DecodeBandEnergies:
		// "logEnergy := float64(energyBits[i]) * 0.5"
		// "bp.bands[i].Energy = math.Exp(logEnergy)"

		// So if we have `logEnergy` (float), we can reverse map it to `energyBits` for compatibility,
		// OR calculate `bp.bands[i].Energy` directly here.

		// Let's set it in energyBits for compatibility with the existing bandProc method.
		// logEnergy = val * 0.5 => val = logEnergy * 2.0
		energyBits[i] = int(math.Round(logEnergy * 2.0))
	}

	d.bandProc.DecodeBandEnergies(energyBits)
	d.bandProc.InterpolateBandEnergies()

	return nil
}

// decodeBandCoeffs decodes PVQ-quantized band coefficients
func (d *Decoder) decodeBandCoeffs(dec *entcode.Decoder) error {
	// Decode each band
	for i := 0; i < d.mode.Bands.NumBands; i++ {
		// Determine number of pulses for this band
		// In full CELT, this comes from bit allocation
		// For now, use a safe heuristic to avoid uint32 overflow in standard PVQ
		// Large N and K causes C(N+K-1, K) to exceed 2^32

		band := d.bandProc.bands[i]

		pulses := 1
		// Previously we used Size/2, which is too aggressive for large bands without splitting.

		// Check if codebook size fits in uint32
		// If 0, it means overflow or invalid, skip this band
		if icwrs(band.Size, pulses) == 0 {
			continue
		}

		// Decode using recursive PVQ
		// We don't check codebookSize anymore.
		d.bandProc.DecodeBandCoeffs(dec, i, pulses)

		// Apply fine energy
		fineEnergy := int(dec.DecodeUint(2)) // 2 bits fine energy
		d.bandProc.ApplyFineEnergy(i, fineEnergy-2)
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
