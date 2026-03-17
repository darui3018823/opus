package opus

import (
	"fmt"

	framing "github.com/darui3018823/opus/internal"
	"github.com/darui3018823/opus/internal/celt"
	"github.com/darui3018823/opus/internal/resampler"
)

// Application specifies the encoding mode (use constants from package)
type Application = int

// Encoder represents an Opus encoder instance
type Encoder struct {
	sampleRate  int
	channels    int
	application Application

	// CELT encoder (always operates at 48kHz internally)
	celtEncoder *celt.Encoder

	// Resampler for non-48kHz input rates
	inputResampler *resampler.Resampler // inRate -> 48kHz

	// Configuration
	bitrate    int
	complexity int
	vbr        bool
	frameSize  int // frame size in samples at sampleRate

	// Internal 48kHz frame size (always 960 for 20ms)
	internalFrameSize int
}

// isValidOpusRate returns true if the sample rate is one of the five valid Opus rates.
func isValidOpusRate(rate int) bool {
	switch rate {
	case 8000, 12000, 16000, 24000, 48000:
		return true
	}
	return false
}

// NewEncoder creates a new Opus encoder
//
// sampleRate must be one of: 8000, 12000, 16000, 24000, 48000 Hz
// channels must be 1 (mono) or 2 (stereo)
// application specifies the encoding mode
func NewEncoder(sampleRate, channels int, application Application) (*Encoder, error) {
	// Validate parameters
	if !isValidOpusRate(sampleRate) {
		return nil, fmt.Errorf("invalid sample rate %d: must be 8000, 12000, 16000, 24000, or 48000", sampleRate)
	}

	if channels != 1 && channels != 2 {
		return nil, fmt.Errorf("invalid channel count: %d (must be 1 or 2)", channels)
	}

	// Frame size at the caller's sample rate (20ms)
	frameSize := (sampleRate * 20) / 1000

	// Internal CELT frame size is always 960 samples (20ms at 48kHz)
	internalFrameSize := 960

	// Create CELT encoder at 48kHz
	celtEnc, err := celt.NewEncoder(celt.FrameSize20ms, 48000, channels, celt.DefaultEncoderConfig())
	if err != nil {
		return nil, fmt.Errorf("failed to create CELT encoder: %w", err)
	}

	enc := &Encoder{
		sampleRate:        sampleRate,
		channels:          channels,
		application:       application,
		celtEncoder:       celtEnc,
		bitrate:           64000, // Default bitrate
		complexity:        5,     // Default complexity
		vbr:               true,  // Default VBR on
		frameSize:         frameSize,
		internalFrameSize: internalFrameSize,
	}

	// Create resampler if needed (non-48kHz rates)
	if sampleRate != 48000 {
		r, err := resampler.NewResampler(sampleRate, 48000, channels, resampler.QualityDefault)
		if err != nil {
			return nil, fmt.Errorf("failed to create input resampler: %w", err)
		}
		enc.inputResampler = r
	}

	// Apply default settings
	enc.celtEncoder.SetBitrate(enc.bitrate)
	enc.celtEncoder.SetComplexity(enc.complexity)

	return enc, nil
}

// Encode encodes PCM audio samples
//
// pcm contains interleaved 16-bit PCM samples (left, right, left, right, ...)
// frameSize is the number of samples per channel (at the encoder's sample rate)
// Returns compressed Opus packet
func (e *Encoder) Encode(pcm []int16, frameSize int) ([]byte, error) {
	expectedSize := frameSize * e.channels
	if len(pcm) < expectedSize {
		return nil, fmt.Errorf("insufficient PCM data: got %d, need %d", len(pcm), expectedSize)
	}

	// Convert int16 to float64
	floatPCM := make([]float64, expectedSize)
	for i := 0; i < expectedSize; i++ {
		floatPCM[i] = float64(pcm[i]) / 32768.0
	}

	return e.encodeFloat(floatPCM, frameSize)
}

// EncodeFloat encodes floating-point PCM samples
//
// pcm contains interleaved float64 samples in range [-1.0, 1.0]
// frameSize is the number of samples per channel (at the encoder's sample rate)
func (e *Encoder) EncodeFloat(pcm []float64, frameSize int) ([]byte, error) {
	expectedSize := frameSize * e.channels
	if len(pcm) < expectedSize {
		return nil, fmt.Errorf("insufficient PCM data: got %d, need %d", len(pcm), expectedSize)
	}

	return e.encodeFloat(pcm[:expectedSize], frameSize)
}

// encodeFloat is the internal encoding path shared by Encode and EncodeFloat.
func (e *Encoder) encodeFloat(pcm []float64, frameSize int) ([]byte, error) {
	var celtInput []float64

	if e.inputResampler != nil {
		// Resample from sampleRate to 48kHz
		resampled := e.inputResampler.Process(pcm)
		// The resampled output should be approximately internalFrameSize * channels samples.
		// Pad or trim to exact size for CELT.
		targetLen := e.internalFrameSize * e.channels
		celtInput = padOrTrim(resampled, targetLen)
	} else {
		celtInput = pcm
	}

	// Generate TOC byte using CELT-only fullband config for all rates
	// (we always encode internally at 48kHz with CELT)
	toc, err := framing.GenerateTOCExt(framing.ModeCELTOnly, framing.BandwidthFullband, e.channels, framing.FrameSize20ms)
	if err != nil {
		return nil, fmt.Errorf("failed to generate TOC: %w", err)
	}

	// Encode using CELT at 48kHz
	compressed, err := e.celtEncoder.Encode(celtInput)
	if err != nil {
		return nil, fmt.Errorf("CELT encoding failed: %w", err)
	}

	// Prepend TOC byte
	packet := append([]byte{toc}, compressed...)

	return packet, nil
}

// padOrTrim adjusts a slice to exactly targetLen, padding with zeros or trimming.
func padOrTrim(data []float64, targetLen int) []float64 {
	if len(data) == targetLen {
		return data
	}
	result := make([]float64, targetLen)
	copy(result, data)
	return result
}

// SetBitrate sets the target bitrate in bits per second
func (e *Encoder) SetBitrate(bitrate int) error {
	if bitrate < 6000 || bitrate > 510000 {
		return fmt.Errorf("invalid bitrate: %d (must be between 6000 and 510000)", bitrate)
	}
	e.bitrate = bitrate
	e.celtEncoder.SetBitrate(bitrate)
	return nil
}

// SetComplexity sets the computational complexity (0-10)
// Higher values use more CPU but may provide better quality
func (e *Encoder) SetComplexity(complexity int) error {
	if complexity < 0 || complexity > 10 {
		return fmt.Errorf("invalid complexity: %d (must be 0-10)", complexity)
	}
	e.complexity = complexity
	e.celtEncoder.SetComplexity(complexity)
	return nil
}

// SetVBR enables or disables variable bitrate mode
func (e *Encoder) SetVBR(vbr bool) {
	e.vbr = vbr
}

// SetApplication changes the application mode
func (e *Encoder) SetApplication(application Application) {
	e.application = application
}

// Reset resets the encoder state
func (e *Encoder) Reset() error {
	e.celtEncoder.Reset()
	if e.inputResampler != nil {
		e.inputResampler.Reset()
	}
	e.celtEncoder.SetBitrate(e.bitrate)
	e.celtEncoder.SetComplexity(e.complexity)
	return nil
}

// Decoder represents an Opus decoder instance
type Decoder struct {
	sampleRate int
	channels   int

	// CELT decoder (always operates at 48kHz internally)
	celtDecoder *celt.Decoder

	// Resampler for non-48kHz output rates
	outputResampler *resampler.Resampler // 48kHz -> outRate

	frameSize         int // frame size in samples at sampleRate
	internalFrameSize int // always 960 (20ms at 48kHz)
}

// NewDecoder creates a new Opus decoder
//
// sampleRate must be one of: 8000, 12000, 16000, 24000, 48000 Hz
// channels must be 1 (mono) or 2 (stereo)
func NewDecoder(sampleRate, channels int) (*Decoder, error) {
	// Validate parameters
	if !isValidOpusRate(sampleRate) {
		return nil, fmt.Errorf("invalid sample rate %d: must be 8000, 12000, 16000, 24000, or 48000", sampleRate)
	}

	if channels != 1 && channels != 2 {
		return nil, fmt.Errorf("invalid channel count: %d (must be 1 or 2)", channels)
	}

	// Frame size at the caller's sample rate (20ms)
	frameSize := (sampleRate * 20) / 1000

	// Internal CELT frame size
	internalFrameSize := 960

	// Create CELT decoder at 48kHz
	celtDec, err := celt.NewDecoder(celt.FrameSize20ms, 48000, channels)
	if err != nil {
		return nil, fmt.Errorf("failed to create CELT decoder: %w", err)
	}

	dec := &Decoder{
		sampleRate:        sampleRate,
		channels:          channels,
		celtDecoder:       celtDec,
		frameSize:         frameSize,
		internalFrameSize: internalFrameSize,
	}

	// Create resampler if needed (non-48kHz rates)
	if sampleRate != 48000 {
		r, err := resampler.NewResampler(48000, sampleRate, channels, resampler.QualityDefault)
		if err != nil {
			return nil, fmt.Errorf("failed to create output resampler: %w", err)
		}
		dec.outputResampler = r
	}

	return dec, nil
}

// Decode decodes an Opus packet to PCM samples
//
// data is the compressed Opus packet
// pcm is the output buffer for 16-bit PCM samples (will be resized if needed)
// Returns the number of samples per channel decoded
func (d *Decoder) Decode(data []byte, pcm []int16) (int, error) {
	if len(data) == 0 {
		return 0, fmt.Errorf("empty packet")
	}

	// Parse TOC
	toc := data[0]
	config, _, countCode := framing.ParseTOC(toc)

	// Check compatibility: we only support CELT-only configs (16-31)
	if config < 16 {
		return 0, fmt.Errorf("unsupported Opus mode (config %d): SILK/Hybrid layers are not yet implemented", config)
	}

	if countCode != 0 {
		return 0, fmt.Errorf("multi-frame packets (code %d) are not yet supported", countCode)
	}

	// Payload is everything after TOC
	payload := data[1:]

	// Decode using CELT at 48kHz
	floatPCM, err := d.celtDecoder.Decode(payload)
	if err != nil {
		return 0, fmt.Errorf("CELT decoding failed: %w", err)
	}

	// Resample from 48kHz to output sample rate if needed
	if d.outputResampler != nil {
		floatPCM = d.outputResampler.Process(floatPCM)
		// Ensure we have exactly frameSize * channels samples
		targetLen := d.frameSize * d.channels
		floatPCM = padOrTrim(floatPCM, targetLen)
	}

	// Convert float64 to int16
	if len(pcm) < len(floatPCM) {
		return 0, fmt.Errorf("output buffer too small: got %d, need %d", len(pcm), len(floatPCM))
	}

	for i := 0; i < len(floatPCM); i++ {
		sample := floatPCM[i] * 32767.0
		if sample > 32767.0 {
			sample = 32767.0
		}
		if sample < -32768.0 {
			sample = -32768.0
		}
		pcm[i] = int16(sample)
	}

	samplesPerChannel := len(floatPCM) / d.channels
	return samplesPerChannel, nil
}

// DecodeFloat decodes an Opus packet to floating-point PCM samples
//
// data is the compressed Opus packet
// Returns float64 samples in range [-1.0, 1.0]
func (d *Decoder) DecodeFloat(data []byte) ([]float64, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("empty packet")
	}

	toc := data[0]
	config, _, countCode := framing.ParseTOC(toc)

	if config < 16 {
		return nil, fmt.Errorf("unsupported Opus mode (config %d): SILK/Hybrid layers are not yet implemented", config)
	}

	if countCode != 0 {
		return nil, fmt.Errorf("multi-frame packets (code %d) are not yet supported", countCode)
	}

	payload := data[1:]

	// Decode using CELT at 48kHz
	pcm, err := d.celtDecoder.Decode(payload)
	if err != nil {
		return nil, fmt.Errorf("CELT decoding failed: %w", err)
	}

	// Resample from 48kHz to output sample rate if needed
	if d.outputResampler != nil {
		pcm = d.outputResampler.Process(pcm)
		targetLen := d.frameSize * d.channels
		pcm = padOrTrim(pcm, targetLen)
	}

	return pcm, nil
}

// DecodeFEC decodes forward error correction data
// This is used for packet loss concealment
func (d *Decoder) DecodeFEC(data []byte, pcm []int16) (int, error) {
	// FEC decoding (currently uses PLC)
	floatPCM, err := d.celtDecoder.DecodePLC()
	if err != nil {
		return 0, fmt.Errorf("PLC decoding failed: %w", err)
	}

	// Resample if needed
	if d.outputResampler != nil {
		floatPCM = d.outputResampler.Process(floatPCM)
		targetLen := d.frameSize * d.channels
		floatPCM = padOrTrim(floatPCM, targetLen)
	}

	// Convert to int16
	if len(pcm) < len(floatPCM) {
		return 0, fmt.Errorf("output buffer too small")
	}

	for i := 0; i < len(floatPCM); i++ {
		sample := floatPCM[i] * 32767.0
		if sample > 32767.0 {
			sample = 32767.0
		}
		if sample < -32768.0 {
			sample = -32768.0
		}
		pcm[i] = int16(sample)
	}

	samplesPerChannel := len(floatPCM) / d.channels
	return samplesPerChannel, nil
}

// Reset resets the decoder state
func (d *Decoder) Reset() error {
	d.celtDecoder.Reset()
	if d.outputResampler != nil {
		d.outputResampler.Reset()
	}
	return nil
}

// GetLastPacketDuration returns the duration of the last decoded packet in samples
func (d *Decoder) GetLastPacketDuration() int {
	return d.frameSize
}
