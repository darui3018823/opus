package opus

import (
	"fmt"

	framing "github.com/darui3018823/opus/internal"
	"github.com/darui3018823/opus/internal/celt"
)

// Application specifies the encoding mode (use constants from package)
type Application = int

// Encoder represents an Opus encoder instance
type Encoder struct {
	sampleRate  int
	channels    int
	application Application

	// CELT encoder
	celtEncoder *celt.Encoder

	// Configuration
	bitrate    int
	complexity int
	vbr        bool
	frameSize  int
}

// NewEncoder creates a new Opus encoder
//
// sampleRate must be 48000 Hz. (Other rates are technically valid in Opus but require SILK/Resampling support which is not yet fully implemented for strict compliance).
// channels must be 1 (mono) or 2 (stereo)
// application specifies the encoding mode
func NewEncoder(sampleRate, channels int, application Application) (*Encoder, error) {
	// Validate parameters
	if sampleRate != 48000 {
		return nil, fmt.Errorf("sample rate %d is not yet supported in this strict-compliance build (only 48000 Hz is supported)", sampleRate)
	}

	if channels != 1 && channels != 2 {
		return nil, fmt.Errorf("invalid channel count: %d (must be 1 or 2)", channels)
	}

	// Default frame size: 20ms
	frameSize := (sampleRate * 20) / 1000

	// Create CELT encoder
	// Note: We force 20ms frame size for now as per correct TOC generation support.
	celtFrameSize := celt.FrameSize20ms
	switch frameSize {
	case 960:
		celtFrameSize = celt.FrameSize20ms
	default:
		return nil, fmt.Errorf("frame size other than 20ms (960 samples) is not yet supported")
	}

	celtEnc, err := celt.NewEncoder(celtFrameSize, sampleRate, channels, celt.DefaultEncoderConfig())
	if err != nil {
		return nil, fmt.Errorf("failed to create CELT encoder: %w", err)
	}

	enc := &Encoder{
		sampleRate:  sampleRate,
		channels:    channels,
		application: application,
		celtEncoder: celtEnc,
		bitrate:     64000, // Default bitrate
		complexity:  5,     // Default complexity
		vbr:         true,  // Default VBR on
		frameSize:   frameSize,
	}

	// Apply default settings
	enc.celtEncoder.SetBitrate(enc.bitrate)
	enc.celtEncoder.SetComplexity(enc.complexity)

	return enc, nil
}

// Encode encodes PCM audio samples
//
// pcm contains interleaved 16-bit PCM samples (left, right, left, right, ...)
// frameSize is the number of samples per channel
// Returns compressed Opus packet
func (e *Encoder) Encode(pcm []int16, frameSize int) ([]byte, error) {
	expectedSize := frameSize * e.channels
	if len(pcm) < expectedSize {
		return nil, fmt.Errorf("insufficient PCM data: got %d, need %d", len(pcm), expectedSize)
	}

	// Generate TOC byte first to ensure we can strictly comply
	toc, err := framing.GenerateTOC(e.channels, frameSize)
	if err != nil {
		return nil, fmt.Errorf("failed to generate TOC: %w", err)
	}

	// Convert int16 to float64
	floatPCM := make([]float64, expectedSize)
	for i := 0; i < expectedSize; i++ {
		floatPCM[i] = float64(pcm[i]) / 32768.0
	}

	// Encode using CELT
	compressed, err := e.celtEncoder.Encode(floatPCM)
	if err != nil {
		return nil, fmt.Errorf("CELT encoding failed: %w", err)
	}

	// Prepend TOC byte
	packet := append([]byte{toc}, compressed...)

	return packet, nil
}

// EncodeFloat encodes floating-point PCM samples
//
// pcm contains interleaved float64 samples in range [-1.0, 1.0]
// frameSize is the number of samples per channel
func (e *Encoder) EncodeFloat(pcm []float64, frameSize int) ([]byte, error) {
	expectedSize := frameSize * e.channels
	if len(pcm) < expectedSize {
		return nil, fmt.Errorf("insufficient PCM data: got %d, need %d", len(pcm), expectedSize)
	}

	// Generate TOC byte
	toc, err := framing.GenerateTOC(e.channels, frameSize)
	if err != nil {
		return nil, fmt.Errorf("failed to generate TOC: %w", err)
	}

	// Encode using CELT
	compressed, err := e.celtEncoder.Encode(pcm[:expectedSize])
	if err != nil {
		return nil, fmt.Errorf("CELT encoding failed: %w", err)
	}

	// Prepend TOC byte
	packet := append([]byte{toc}, compressed...)

	return packet, nil
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
	// VBR implementation would be applied here
}

// SetApplication changes the application mode
func (e *Encoder) SetApplication(application Application) {
	e.application = application
	// Application-specific optimizations would be applied here
}

// Reset resets the encoder state
func (e *Encoder) Reset() error {
	// Reset CELT encoder state
	e.celtEncoder.Reset()

	// Re-apply settings just in case
	e.celtEncoder.SetBitrate(e.bitrate)
	e.celtEncoder.SetComplexity(e.complexity)
	return nil
}

// Decoder represents an Opus decoder instance
type Decoder struct {
	sampleRate int
	channels   int

	// CELT decoder
	celtDecoder *celt.Decoder

	frameSize int
}

// NewDecoder creates a new Opus decoder
//
// sampleRate must be 48000 Hz.
// channels must be 1 (mono) or 2 (stereo)
func NewDecoder(sampleRate, channels int) (*Decoder, error) {
	// Validate parameters
	if sampleRate != 48000 {
		return nil, fmt.Errorf("sample rate %d is not yet supported in this strict-compliance build (only 48000 Hz is supported)", sampleRate)
	}

	if channels != 1 && channels != 2 {
		return nil, fmt.Errorf("invalid channel count: %d (must be 1 or 2)", channels)
	}

	// Default frame size: 20ms
	frameSize := (sampleRate * 20) / 1000

	// Create CELT decoder
	// Assume 20ms for now
	celtFrameSize := celt.FrameSize20ms

	celtDec, err := celt.NewDecoder(celtFrameSize, sampleRate, channels)
	if err != nil {
		return nil, fmt.Errorf("failed to create CELT decoder: %w", err)
	}

	dec := &Decoder{
		sampleRate:  sampleRate,
		channels:    channels,
		celtDecoder: celtDec,
		frameSize:   frameSize,
	}

	return dec, nil
}

// Decode decodes an Opus packet to PCM samples
//
// data is the compressed Opus packet
// pcm is the output buffer for 16-bit PCM samples (will be resized if needed)
// Returns the number of samples per channel decoded
func (d *Decoder) Decode(data []byte, pcm []int16) (int, error) {
	if len(data) < 1 {
		// Empty packet might be treated as packet loss (PLC) in higher layers,
		// but here we expect at least a TOC byte unless it's strictly empty.
		if len(data) == 0 {
			return 0, fmt.Errorf("empty packet")
		}
	}

	// Parse TOC
	toc := data[0]
	config, _, _ := framing.ParseTOC(toc)

	// Check compatibility
	// Configs 20-31 are CELT-only.
	// We only strictly support CELT currently.
	// Configs 0-11: SILK-only
	// Configs 12-15: SILK-only (SWB)
	// Configs 16-19: Hybrid
	if config < 20 {
		return 0, fmt.Errorf("unsupported Opus mode (config %d): SILK/Hybrid layers are not yet implemented", config)
	}

	// Payload is everything after TOC
	payload := data[1:]

	// Decode using CELT
	// Note: Standard Opus might have multiple frames in one packet (Code 1, 2, 3).
	// ParseTOC returns countCode. If != 0, we have multi-frame packet.
	_, _, countCode := framing.ParseTOC(toc)
	if countCode != 0 {
		// For Phase 1, we don't handle multi-frame packets yet (requires length delimiting parsing)
		return 0, fmt.Errorf("multi-frame packets (code %d) are not yet supported", countCode)
	}

	floatPCM, err := d.celtDecoder.Decode(payload)
	if err != nil {
		return 0, fmt.Errorf("CELT decoding failed: %w", err)
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
	if len(data) < 1 {
		if len(data) == 0 {
			return nil, fmt.Errorf("empty packet")
		}
	}

	toc := data[0]
	config, _, countCode := framing.ParseTOC(toc)

	if config < 20 {
		return nil, fmt.Errorf("unsupported Opus mode (config %d): SILK/Hybrid layers are not yet implemented", config)
	}

	if countCode != 0 {
		return nil, fmt.Errorf("multi-frame packets (code %d) are not yet supported", countCode)
	}

	payload := data[1:]

	// Decode using CELT
	pcm, err := d.celtDecoder.Decode(payload)
	if err != nil {
		return nil, fmt.Errorf("CELT decoding failed: %w", err)
	}

	return pcm, nil
}

// DecodeFEC decodes forward error correction data
// This is used for packet loss concealment
func (d *Decoder) DecodeFEC(data []byte, pcm []int16) (int, error) {
	// PLC/FEC currently delegates to CELT PLC.
	// If data is nil/empty, it's pure PLC.
	// If data is provided, it's FEC.

	// For Phase 1, we delegate to CELT PLC, but warn about missing SILK/FEC logic if applicable.

	// FEC decoding (currently uses PLC)
	floatPCM, err := d.celtDecoder.DecodePLC()
	if err != nil {
		return 0, fmt.Errorf("PLC decoding failed: %w", err)
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
	// Reset CELT decoder state
	d.celtDecoder.Reset()
	return nil
}

// GetLastPacketDuration returns the duration of the last decoded packet in samples
func (d *Decoder) GetLastPacketDuration() int {
	return d.frameSize
}
