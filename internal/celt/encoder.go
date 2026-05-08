package celt

import (
	"errors"
	"math"

	"github.com/darui3018823/opus/internal/dsp"
	"github.com/darui3018823/opus/internal/entcode"
)

// Encoder is a CELT encoder instance
type Encoder struct {
	mode         *Mode
	mdct         *dsp.MDCT
	bandProc     *BandProcessor
	transientDet *TransientDetector
	overlap      [][]float64 // Overlap buffer per channel

	// Encoder configuration
	bitrate    int  // Target bitrate in bits per second
	complexity int  // Encoding complexity (0-10)
	vbr        bool // Variable bitrate mode

	// State — two-tap log-energy history for RFC 6716 §5.1.2
	prevBandEnergies  []float64 // Previous frame log-energies (ln domain)
	prevBandEnergies2 []float64 // Two-frames-ago log-energies (ln domain)
	frameCount        int       // Counts frames for intra/inter mode decision
}

// EncoderConfig holds encoder configuration
type EncoderConfig struct {
	Bitrate    int  // Target bitrate in bps
	Complexity int  // Complexity level (0-10)
	VBR        bool // Variable bitrate
}

// DefaultEncoderConfig returns default encoder configuration
func DefaultEncoderConfig() *EncoderConfig {
	return &EncoderConfig{
		Bitrate:    64000, // 64 kbps
		Complexity: 5,     // Medium complexity
		VBR:        false,
	}
}

// NewEncoder creates a new CELT encoder
func NewEncoder(frameSize, sampleRate, channels int, config *EncoderConfig) (*Encoder, error) {
	if channels < 1 || channels > 2 {
		return nil, errors.New("celt: only mono and stereo supported")
	}

	if config == nil {
		config = DefaultEncoderConfig()
	}

	mode := NewMode(frameSize, sampleRate, channels)

	// MDCT size must be power of 2
	mdctSize := 1
	for mdctSize < frameSize {
		mdctSize *= 2
	}

	mdct, err := dsp.NewMDCT(mdctSize)
	if err != nil {
		return nil, err
	}

	e := &Encoder{
		mode:         mode,
		mdct:         mdct,
		bandProc:     NewBandProcessor(mode),
		transientDet: NewTransientDetector(mode),
		overlap:      make([][]float64, channels),
		bitrate:      config.Bitrate,
		complexity:   config.Complexity,
		vbr:          config.VBR,
	}

	// Initialize overlap buffers
	for i := 0; i < channels; i++ {
		e.overlap[i] = make([]float64, mdctSize)
	}

	// Initialize energy history in log (ln) domain, same as decoder.
	initLogE := math.Log(1e-8)
	e.prevBandEnergies = make([]float64, mode.Bands.NumBands)
	e.prevBandEnergies2 = make([]float64, mode.Bands.NumBands)
	for i := range e.prevBandEnergies {
		e.prevBandEnergies[i] = initLogE
		e.prevBandEnergies2[i] = initLogE
	}

	return e, nil
}

// Encode encodes PCM samples to a CELT frame
func (e *Encoder) Encode(samples []float64) ([]byte, error) {
	expectedSize := e.mode.FrameSize * e.mode.Channels
	if len(samples) != expectedSize {
		return nil, errors.New("celt: invalid input size")
	}

	// Detect transients (result used for future extensions)
	monoSamples := e.convertToMono(samples)
	_, _ = e.transientDet.Detect(monoSamples)

	// Encode each channel
	allCoeffs := make([][]float64, e.mode.Channels)
	allEnergies := make([][]float64, e.mode.Channels)

	for ch := 0; ch < e.mode.Channels; ch++ {
		// Extract channel samples
		chSamples := e.extractChannel(samples, ch)

		// Perform MDCT analysis
		mdctSize := e.mdct.Size()

		// Pad channel samples to mdctSize if necessary
		mdctInput := make([]float64, mdctSize)
		copySize := min(len(chSamples), mdctSize)
		copy(mdctInput, chSamples[:copySize])

		// Forward MDCT with overlap
		coeffs, err := e.mdct.ForwardOverlap(mdctInput, e.overlap[ch])
		if err != nil {
			return nil, err
		}
		allCoeffs[ch] = coeffs

		// Compute band energies
		bandEnergies := e.computeBandEnergies(coeffs)
		allEnergies[ch] = bandEnergies
	}

	// Use first channel energies for bit allocation
	bandEnergies := allEnergies[0]

	// Calculate target bits for this frame
	frameDuration := float64(e.mode.FrameSize) / float64(e.mode.SampleRate)
	targetBits := int(float64(e.bitrate) * frameDuration)

	// Perform bit allocation
	bitAlloc := NewBitAllocation(e.mode, targetBits)
	bitAlloc.Allocate(bandEnergies)

	// Encode frame
	enc := entcode.NewEncoder(targetBits / 8)

	// Encode band energies using RFC 6716 §5.1.2 coarse energy coding.
	intra := e.frameCount == 0
	logBandEnergies := make([]float64, e.mode.Bands.NumBands)
	for i, en := range bandEnergies {
		if en > 1e-30 {
			logBandEnergies[i] = math.Log(en)
		} else {
			logBandEnergies[i] = math.Log(1e-30)
		}
	}
	quantLogE := QuantizeCoarseEnergy(
		enc,
		logBandEnergies,
		e.prevBandEnergies,
		e.prevBandEnergies2,
		intra,
		e.mode.Bands.NumBands,
		3, // lm=3 for 20ms frames
		e.mode.Channels,
	)
	// Update two-tap energy state
	copy(e.prevBandEnergies2, e.prevBandEnergies)
	copy(e.prevBandEnergies, quantLogE)

	// Quantize and encode band coefficients
	for i := 0; i < e.mode.Bands.NumBands; i++ {
		// Extract band coefficients for first channel
		bandStart := e.mode.Bands.BandStart[i]
		bandSize := e.mode.Bands.BandSizes[i]
		bandEnd := bandStart + bandSize

		if bandEnd > len(allCoeffs[0]) {
			bandEnd = len(allCoeffs[0])
		}

		bandCoeffs := allCoeffs[0][bandStart:bandEnd]

		// Normalize band
		_ = NormalizeBand(bandCoeffs)

		// Get pulse count from bit allocation
		pulses := bitAlloc.GetPulseCount(i)

		// Encode using recursive PVQ splitting (RFC 6716 §4.3.4)
		PVQEncode(enc, bandCoeffs, pulses)

		// Encode fine energy
		fineEnergy := bitAlloc.GetFineEnergy(i)
		if fineEnergy > 0 {
			enc.EncodeUint(uint32(fineEnergy), 4)
		}
	}

	// Flush encoder
	enc.Flush()

	e.frameCount++

	// Pad output to exactly targetBits/8 bytes so that the decoder can use
	// len(packet)*8 as its bit budget and compute the same bit allocation.
	encoded := enc.Bytes()
	targetBytes := targetBits / 8
	if targetBytes < 1 {
		targetBytes = 1
	}
	if len(encoded) < targetBytes {
		padded := make([]byte, targetBytes)
		copy(padded, encoded)
		encoded = padded
	}

	return encoded, nil
}

// convertToMono converts interleaved stereo to mono for analysis
func (e *Encoder) convertToMono(samples []float64) []float64 {
	if e.mode.Channels == 1 {
		return samples
	}

	mono := make([]float64, len(samples)/2)
	for i := 0; i < len(mono); i++ {
		mono[i] = (samples[i*2] + samples[i*2+1]) * 0.5
	}
	return mono
}

// extractChannel extracts a single channel from interleaved samples
func (e *Encoder) extractChannel(samples []float64, channel int) []float64 {
	if e.mode.Channels == 1 {
		return samples
	}

	result := make([]float64, len(samples)/e.mode.Channels)
	for i := 0; i < len(result); i++ {
		result[i] = samples[i*e.mode.Channels+channel]
	}
	return result
}

// computeBandEnergies computes energy for each frequency band
func (e *Encoder) computeBandEnergies(coeffs []float64) []float64 {
	energies := make([]float64, e.mode.Bands.NumBands)

	for i := 0; i < e.mode.Bands.NumBands; i++ {
		bandStart := e.mode.Bands.BandStart[i]
		bandSize := e.mode.Bands.BandSizes[i]
		bandEnd := bandStart + bandSize

		if bandEnd > len(coeffs) {
			bandEnd = len(coeffs)
		}

		energy := 0.0
		for j := bandStart; j < bandEnd; j++ {
			energy += coeffs[j] * coeffs[j]
		}

		energies[i] = energy
	}

	return energies
}

// Reset resets the encoder state
func (e *Encoder) Reset() {
	// Clear overlap buffers
	for ch := 0; ch < e.mode.Channels; ch++ {
		for i := range e.overlap[ch] {
			e.overlap[ch][i] = 0
		}
	}

	// Reset energy history
	initLogE := math.Log(1e-8)
	for i := range e.prevBandEnergies {
		e.prevBandEnergies[i] = initLogE
		e.prevBandEnergies2[i] = initLogE
	}

	e.frameCount = 0

	// Reset transient detector
	e.transientDet.Reset()
}

// SetBitrate sets the target bitrate
func (e *Encoder) SetBitrate(bitrate int) {
	if bitrate > 0 {
		e.bitrate = bitrate
	}
}

// SetComplexity sets the encoding complexity
func (e *Encoder) SetComplexity(complexity int) {
	if complexity >= 0 && complexity <= 10 {
		e.complexity = complexity
	}
}
