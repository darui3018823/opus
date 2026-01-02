// Package opus provides a Pure Go implementation of the Opus audio codec.
//
// This implementation provides CELT encoding and decoding without CGO dependencies,
// targeting compatibility with libopus while maintaining Go's safety and portability.
//
// Basic usage:
//
//	// Create encoder
//	enc, err := opus.NewEncoder(48000, 2, opus.ApplicationAudio)
//	if err != nil {
//		log.Fatal(err)
//	}
//	enc.SetBitrate(64000)
//
//	// Encode PCM samples
//	pcm := make([]int16, 960*2) // 20ms stereo at 48kHz
//	compressed, err := enc.Encode(pcm, 960)
//
//	// Create decoder
//	dec, err := opus.NewDecoder(48000, 2)
//	if err != nil {
//		log.Fatal(err)
//	}
//
//	// Decode
//	decoded := make([]int16, 960*2)
//	n, err := dec.Decode(compressed, decoded)
package opus

import (
	"fmt"

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
// sampleRate must be one of 8000, 12000, 16000, 24000, or 48000 Hz
// channels must be 1 (mono) or 2 (stereo)
// application specifies the encoding mode
func NewEncoder(sampleRate, channels int, application Application) (*Encoder, error) {
	// Validate parameters
	validRates := map[int]bool{8000: true, 12000: true, 16000: true, 24000: true, 48000: true}
	if !validRates[sampleRate] {
		return nil, fmt.Errorf("invalid sample rate: %d (must be 8000, 12000, 16000, 24000, or 48000)", sampleRate)
	}
	
	if channels != 1 && channels != 2 {
		return nil, fmt.Errorf("invalid channel count: %d (must be 1 or 2)", channels)
	}

	// Default frame size: 20ms
	frameSize := (sampleRate * 20) / 1000
	
	// Create CELT encoder
	celtFrameSize := celt.FrameSize20ms
	switch frameSize {
	case 120:
		celtFrameSize = celt.FrameSize2_5ms
	case 240:
		celtFrameSize = celt.FrameSize5ms
	case 480:
		celtFrameSize = celt.FrameSize10ms
	case 960:
		celtFrameSize = celt.FrameSize20ms
	case 1920:
		celtFrameSize = celt.FrameSize40ms
	case 2880:
		celtFrameSize = celt.FrameSize60ms
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

	return compressed, nil
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

	// Encode using CELT
	compressed, err := e.celtEncoder.Encode(pcm[:expectedSize])
	if err != nil {
		return nil, fmt.Errorf("CELT encoding failed: %w", err)
	}

	return compressed, nil
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
	celtFrameSize := celt.FrameSize20ms
	frameSize := (e.sampleRate * 20) / 1000
	switch frameSize {
	case 120:
		celtFrameSize = celt.FrameSize2_5ms
	case 240:
		celtFrameSize = celt.FrameSize5ms
	case 480:
		celtFrameSize = celt.FrameSize10ms
	case 960:
		celtFrameSize = celt.FrameSize20ms
	case 1920:
		celtFrameSize = celt.FrameSize40ms
	case 2880:
		celtFrameSize = celt.FrameSize60ms
	}
	
	newEnc, err := celt.NewEncoder(celtFrameSize, e.sampleRate, e.channels, celt.DefaultEncoderConfig())
	if err != nil {
		return err
	}
	e.celtEncoder = newEnc
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
// sampleRate must be one of 8000, 12000, 16000, 24000, or 48000 Hz
// channels must be 1 (mono) or 2 (stereo)
func NewDecoder(sampleRate, channels int) (*Decoder, error) {
	// Validate parameters
	validRates := map[int]bool{8000: true, 12000: true, 16000: true, 24000: true, 48000: true}
	if !validRates[sampleRate] {
		return nil, fmt.Errorf("invalid sample rate: %d (must be 8000, 12000, 16000, 24000, or 48000)", sampleRate)
	}
	
	if channels != 1 && channels != 2 {
		return nil, fmt.Errorf("invalid channel count: %d (must be 1 or 2)", channels)
	}

	// Default frame size: 20ms
	frameSize := (sampleRate * 20) / 1000
	
	// Create CELT decoder
	celtFrameSize := celt.FrameSize20ms
	switch frameSize {
	case 120:
		celtFrameSize = celt.FrameSize2_5ms
	case 240:
		celtFrameSize = celt.FrameSize5ms
	case 480:
		celtFrameSize = celt.FrameSize10ms
	case 960:
		celtFrameSize = celt.FrameSize20ms
	case 1920:
		celtFrameSize = celt.FrameSize40ms
	case 2880:
		celtFrameSize = celt.FrameSize60ms
	}

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
	// Decode using CELT
	floatPCM, err := d.celtDecoder.Decode(data)
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
	// Decode using CELT
	pcm, err := d.celtDecoder.Decode(data)
	if err != nil {
		return nil, fmt.Errorf("CELT decoding failed: %w", err)
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
	celtFrameSize := celt.FrameSize20ms
	frameSize := (d.sampleRate * 20) / 1000
	switch frameSize {
	case 120:
		celtFrameSize = celt.FrameSize2_5ms
	case 240:
		celtFrameSize = celt.FrameSize5ms
	case 480:
		celtFrameSize = celt.FrameSize10ms
	case 960:
		celtFrameSize = celt.FrameSize20ms
	case 1920:
		celtFrameSize = celt.FrameSize40ms
	case 2880:
		celtFrameSize = celt.FrameSize60ms
	}
	
	newDec, err := celt.NewDecoder(celtFrameSize, d.sampleRate, d.channels)
	if err != nil {
		return err
	}
	d.celtDecoder = newDec
	return nil
}

// GetLastPacketDuration returns the duration of the last decoded packet in samples
func (d *Decoder) GetLastPacketDuration() int {
	return d.frameSize
}
