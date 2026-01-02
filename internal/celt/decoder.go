package celt

import (
	"errors"
	"github.com/darui3018823/opus/internal/dsp"
	"github.com/darui3018823/opus/internal/entcode"
)

// Decoder is a CELT decoder instance
type Decoder struct {
	mode      *Mode
	mdct      *dsp.MDCT
	bandProc  *BandProcessor
	overlap   [][]float64 // Overlap buffer per channel
	
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
	
	mdct := dsp.NewMDCT(mdctSize)
	
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
		samples := d.mdct.InverseOverlap(mdctCoeffs, d.overlap[ch])
		
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
func (d *Decoder) decodeBandEnergies(dec *entcode.Decoder) error {
	// Simplified energy decoding
	// In full CELT, this uses:
	// - Coarse energy (quantized in log domain)
	// - Fine energy (refinement bits)
	// - Temporal prediction from previous frame
	
	energyBits := make([]int, d.mode.Bands.NumBands)
	
	for i := 0; i < d.mode.Bands.NumBands; i++ {
		// Decode coarse energy (simplified - just read a few bits)
		// In reality, this uses a more complex entropy coding scheme
		bits := int(dec.DecodeUint(4)) // 4 bits per band for demo
		
		// Apply temporal prediction
		// predicted = previous * decay
		predicted := d.prevEnergies[i] * 0.9
		
		// Quantized energy relative to prediction
		energyBits[i] = bits - 8 // Center around 0
		
		// Update previous energy
		logEnergy := float64(energyBits[i]) * 0.5
		d.prevEnergies[i] = predicted * dsp.MaxFloat(0.1, dsp.MinFloat(10.0, 
			(1.0 + logEnergy*0.1)))
	}
	
	d.bandProc.DecodeBandEnergies(energyBits)
	d.bandProc.InterpolateBandEnergies()
	
	return nil
}

// decodeBandCoeffs decodes PVQ-quantized band coefficients
func (d *Decoder) decodeBandCoeffs(dec *entcode.Decoder) error {
	// Decode each band
	for i := 0; i < d.mode.Bands.NumBands; i++ {
		band := d.bandProc.bands[i]
		
		// Determine number of pulses for this band
		// In full CELT, this comes from bit allocation
		// For now, use a simple heuristic based on band size
		pulses := band.Size / 2
		if pulses < 1 {
			pulses = 1
		}
		if pulses > 20 {
			pulses = 20
		}
		
		// Calculate PVQ codebook size
		codebookSize := icwrs(band.Size, pulses)
		if codebookSize == 0 {
			continue
		}
		
		// Decode PVQ index (simplified - in reality uses range coder)
		// For demo, just use a simple pattern
		pvqIndex := uint32(i) % codebookSize
		
		// Decode band coefficients
		d.bandProc.DecodeBandCoeffs(i, pvqIndex, pulses)
		
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
