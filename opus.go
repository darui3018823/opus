package opus

import (
	"fmt"

	framing "github.com/darui3018823/opus/internal"
	"github.com/darui3018823/opus/internal/celt"
	"github.com/darui3018823/opus/internal/resampler"
	"github.com/darui3018823/opus/internal/silk"
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

// silkRateInfo holds a SILK decoder and its associated resampler for one SILK sample rate.
type silkRateInfo struct {
	dec       *silk.Decoder
	resampler *resampler.Resampler // silkRate -> outputRate (nil if same rate)
}

// Decoder represents an Opus decoder instance
type Decoder struct {
	sampleRate int
	channels   int

	// CELT decoders indexed by [bwIdx 0-3][lmIdx 0-3][chIdx 0-1].
	// bwIdx: 0=NB(13bands), 1=WB(17bands), 2=SWB(19bands), 3=FB(21bands)
	// lmIdx: 0=2.5ms, 1=5ms, 2=10ms, 3=20ms
	// chIdx: 0=mono, 1=stereo
	// CELT always runs at 48kHz internally; bandwidth only limits numBands.
	celtDecoders [4][4][2]*celt.Decoder

	// SILK decoders indexed by [rateIdx 0-2][frameIdx 0=10ms,1=20ms][chIdx 0=mono,1=stereo].
	// rateIdx: 0=8kHz, 1=12kHz, 2=16kHz
	silkDecoders [3][2][2]*silkRateInfo

	// Resampler for non-48kHz CELT output rates
	celtResampler *resampler.Resampler // 48kHz -> outRate

	frameSize         int // frame size in samples at sampleRate
	internalFrameSize int // always 960 (20ms at 48kHz)
}

// silkRateIdx maps a SILK rate in kHz to an index 0-2.
func silkRateIdx(rateKHz int) int {
	switch rateKHz {
	case 8:
		return 0
	case 12:
		return 1
	default: // 16
		return 2
	}
}

// celtBWNumBands maps bandwidth index (0=NB,1=WB,2=SWB,3=FB) to numBands.
var celtBWNumBands = [4]int{13, 17, 19, 21}

// celtLMFrameSize maps LM (0-3) to CELT frame size at 48kHz.
var celtLMFrameSize = [4]int{120, 240, 480, 960}

// celtConfigBWIdx returns the bandwidth index (0=NB,1=WB,2=SWB,3=FB) for a CELT config (16-31).
func celtConfigBWIdx(config int) int {
	return (config - 16) / 4
}

// celtConfigLMIdx returns the LM index (0=2.5ms,1=5ms,2=10ms,3=20ms) for a CELT config (16-31).
func celtConfigLMIdx(config int) int {
	return config & 3
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

	dec := &Decoder{
		sampleRate:        sampleRate,
		channels:          channels,
		frameSize:         frameSize,
		internalFrameSize: internalFrameSize,
	}

	// Create CELT decoders for all 4 bandwidths × 4 frame sizes × 2 channel counts.
	// CELT always runs at 48kHz internally; bandwidth only controls numBands.
	for bw := 0; bw < 4; bw++ {
		numBands := celtBWNumBands[bw]
		for lm := 0; lm < 4; lm++ {
			fs := celtLMFrameSize[lm]
			for ch := 1; ch <= 2; ch++ {
				if ch > channels && channels != 2 {
					continue // only create stereo decoder when output is stereo
				}
				d, err := celt.NewDecoderEx(fs, 48000, numBands, ch)
				if err != nil {
					return nil, fmt.Errorf("failed to create CELT decoder (bw=%d lm=%d ch=%d): %w", bw, lm, ch, err)
				}
				dec.celtDecoders[bw][lm][ch-1] = d
			}
		}
	}

	// Pre-create SILK decoders for all 3 rates × 2 frame durations × 2 channel configs
	silkRates := []int{8000, 12000, 16000}
	frameMsArr := []int{10, 20}
	for ri, silkRate := range silkRates {
		for fi, frameMs := range frameMsArr {
			for ch := 1; ch <= 2; ch++ {
				sd, err := silk.NewDecoderWithFrameMs(silkRate, ch, frameMs)
				if err != nil {
					return nil, fmt.Errorf("failed to create SILK decoder (rate=%d ch=%d frameMs=%d): %w", silkRate, ch, frameMs, err)
				}
				var rs *resampler.Resampler
				if silkRate != sampleRate {
					rs, err = resampler.NewResampler(silkRate, sampleRate, ch, resampler.QualityDefault)
					if err != nil {
						return nil, fmt.Errorf("failed to create SILK resampler (%d->%d, ch=%d): %w", silkRate, sampleRate, ch, err)
					}
				}
				dec.silkDecoders[ri][fi][ch-1] = &silkRateInfo{dec: sd, resampler: rs}
			}
		}
	}

	// Create resampler for CELT output: 48kHz → sampleRate
	if sampleRate != 48000 {
		r, err := resampler.NewResampler(48000, sampleRate, channels, resampler.QualityDefault)
		if err != nil {
			return nil, fmt.Errorf("failed to create CELT output resampler: %w", err)
		}
		dec.celtResampler = r
	}

	return dec, nil
}

// Decode decodes an Opus packet to PCM samples
//
// data is the compressed Opus packet
// pcm is the output buffer for 16-bit PCM samples
// Returns the number of samples per channel decoded (clamped to buffer size)
func (d *Decoder) Decode(data []byte, pcm []int16) (int, error) {
	floatPCM, err := d.DecodeFloat(data)
	if err != nil {
		return 0, err
	}

	// Clamp to buffer size
	n := len(floatPCM)
	if n > len(pcm) {
		n = len(pcm)
	}

	// Convert float64 to int16
	for i := 0; i < n; i++ {
		sample := floatPCM[i] * 32768.0
		if sample > 32767.0 {
			sample = 32767.0
		}
		if sample < -32768.0 {
			sample = -32768.0
		}
		pcm[i] = int16(sample)
	}

	samplesPerChannel := n / d.channels
	return samplesPerChannel, nil
}

func parseOpusFrameLength(data []byte) (int, int, error) {
	if len(data) < 1 {
		return 0, 0, fmt.Errorf("missing frame length")
	}
	n := int(data[0])
	if n < 252 {
		return n, 1, nil
	}
	if len(data) < 2 {
		return 0, 0, fmt.Errorf("truncated extended length")
	}
	return int(data[1])*4 + n, 2, nil
}

// splitOpusFrames splits an Opus packet payload into individual frame payloads
// based on the count code (RFC 6716 §3.3).
// countCode: 0=1 frame, 1=2 equal frames, 2=2 unequal frames, 3=arbitrary frames
func splitOpusFrames(payload []byte, countCode int) ([][]byte, error) {
	switch countCode {
	case 0:
		return [][]byte{payload}, nil

	case 1:
		// Two frames of equal size
		if len(payload)%2 != 0 {
			return nil, fmt.Errorf("code 1: odd payload length %d", len(payload))
		}
		half := len(payload) / 2
		return [][]byte{payload[:half], payload[half:]}, nil

	case 2:
		// Two frames: first frame length is encoded in first byte (or two bytes)
		if len(payload) < 1 {
			return nil, fmt.Errorf("code 2: empty payload")
		}
		n1, used, err := parseOpusFrameLength(payload)
		if err != nil {
			return nil, fmt.Errorf("code 2: %w", err)
		}
		payload = payload[used:]
		if n1 > len(payload) {
			return nil, fmt.Errorf("code 2: frame1 length %d > remaining %d", n1, len(payload))
		}
		return [][]byte{payload[:n1], payload[n1:]}, nil

	case 3:
		// Multiple frames: see RFC 6716 §3.2.5
		if len(payload) < 1 {
			return nil, fmt.Errorf("code 3: empty payload")
		}
		frameCount := int(payload[0] & 0x3F) // lower 6 bits = frame count
		vbr := (payload[0] & 0x80) != 0      // VBR flag
		padding := (payload[0] & 0x40) != 0  // padding flag
		payload = payload[1:]

		if frameCount == 0 || frameCount > 48 {
			return nil, fmt.Errorf("code 3: invalid frame count %d", frameCount)
		}

		// Skip padding
		if padding {
			if len(payload) < 1 {
				return nil, fmt.Errorf("code 3: missing padding count")
			}
			padLen := int(payload[0])
			payload = payload[1:]
			if padLen == 255 {
				if len(payload) < 1 {
					return nil, fmt.Errorf("code 3: missing extended padding count")
				}
				padLen += int(payload[0]) - 1
				payload = payload[1:]
			}
			if padLen > len(payload) {
				return nil, fmt.Errorf("code 3: padding %d > payload %d", padLen, len(payload))
			}
			payload = payload[:len(payload)-padLen]
		}

		if vbr {
			// VBR: the first M-1 frame lengths are stored first, followed by all frame payloads.
			sizes := make([]int, frameCount)
			lastSize := len(payload)
			for i := 0; i < frameCount-1; i++ {
				n, used, err := parseOpusFrameLength(payload)
				if err != nil {
					return nil, fmt.Errorf("code 3 VBR: frame %d: %w", i, err)
				}
				payload = payload[used:]
				sizes[i] = n
				lastSize -= used + n
				if lastSize < 0 {
					return nil, fmt.Errorf("code 3 VBR: frame %d length %d exceeds remaining payload", i, n)
				}
			}
			sizes[frameCount-1] = lastSize
			frames := make([][]byte, frameCount)
			offset := 0
			for i, n := range sizes {
				if offset+n > len(payload) {
					return nil, fmt.Errorf("code 3 VBR: frame %d length %d > remaining %d", i, n, len(payload)-offset)
				}
				frames[i] = payload[offset : offset+n]
				offset += n
			}
			return frames, nil
		}
		// CBR: all frames equal size
		if len(payload)%frameCount != 0 {
			return nil, fmt.Errorf("code 3 CBR: payload %d not divisible by frameCount %d", len(payload), frameCount)
		}
		frameSize := len(payload) / frameCount
		frames := make([][]byte, frameCount)
		for i := 0; i < frameCount; i++ {
			frames[i] = payload[i*frameSize : (i+1)*frameSize]
		}
		return frames, nil

	default:
		return nil, fmt.Errorf("unknown count code %d", countCode)
	}
}

// silkConfigRateKHz returns the SILK internal rate in kHz for a given config.
// Config 0-3: NB (8kHz), 4-7: MB (12kHz), 8-11: WB (16kHz), 12-15: Hybrid (16kHz SILK layer).
func silkConfigRateKHz(config int) int {
	switch {
	case config < 4:
		return 8
	case config < 8:
		return 12
	default:
		return 16 // configs 8-15 (WB SILK and Hybrid)
	}
}

// silkConfigFrameMs returns the Opus frame duration in ms for a config.
func silkConfigFrameMs(config int) int {
	if config >= 12 {
		// Hybrid: 10ms (even config) or 20ms (odd config)
		if config&1 == 0 {
			return 10
		}
		return 20
	}
	// SILK: lower 2 bits of config within the group
	switch config & 3 {
	case 0:
		return 10
	case 1:
		return 20
	case 2:
		return 40
	case 3:
		return 60
	}
	return 20
}

// silkSubframesPerOpusFrame returns the number of 20ms SILK sub-frames per Opus frame.
// For 10ms configs, returns 1 (decoded as half-frame by the SILK decoder).
func silkSubframesPerOpusFrame(config int) int {
	ms := silkConfigFrameMs(config)
	switch ms {
	case 10:
		return 1 // 10ms SILK frame
	case 20:
		return 1
	case 40:
		return 2
	case 60:
		return 3
	}
	return 1
}

// is10msConfig returns true for SILK/Hybrid 10ms configs.
func is10msConfig(config int) bool {
	return silkConfigFrameMs(config) == 10
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
	config, stereo, countCode := framing.ParseTOC(toc)

	payload := data[1:]

	if config < 16 {
		// SILK or Hybrid mode
		pktChannels := 1
		if stereo {
			pktChannels = 2
		}
		return d.decodeSILKPacket(payload, countCode, config, pktChannels)
	}

	// CELT-only mode: split payload into individual frame payloads and decode each
	frames, err := splitOpusFrames(payload, countCode)
	if err != nil {
		return nil, fmt.Errorf("failed to split frames: %w", err)
	}

	pktChannels := 1
	if stereo {
		pktChannels = 2
	}

	// Select the CELT decoder matching this packet's bandwidth and frame size.
	bwIdx := celtConfigBWIdx(config)
	lmIdx := celtConfigLMIdx(config)
	chIdx := pktChannels - 1
	if chIdx < 0 {
		chIdx = 0
	}
	if chIdx > 1 {
		chIdx = 1
	}
	activeCeltDec := d.celtDecoders[bwIdx][lmIdx][chIdx]
	if activeCeltDec == nil {
		// Fallback: mono decoder
		activeCeltDec = d.celtDecoders[bwIdx][lmIdx][0]
	}
	if activeCeltDec == nil {
		return nil, fmt.Errorf("no CELT decoder for config=%d (bw=%d lm=%d ch=%d)", config, bwIdx, lmIdx, pktChannels)
	}

	var allPCM []float64
	for _, frame := range frames {
		pcm, err := activeCeltDec.Decode(frame)
		if err != nil {
			return nil, fmt.Errorf("CELT decoding failed: %w", err)
		}

		// Resample from 48kHz to output sample rate if needed
		if d.celtResampler != nil {
			pcm = d.celtResampler.Process(pcm)
		}
		// Convert packet channels → output channels
		pcm = adjustChannels(pcm, pktChannels, d.channels)
		// Compute expected frame size at output rate
		targetLen := celtFrameSamples(config, d.sampleRate) * d.channels
		pcm = padOrTrim(pcm, targetLen)
		allPCM = append(allPCM, pcm...)
	}

	return allPCM, nil
}

// celtFrameDurationMs returns the frame duration in ms for CELT configs (16-31).
func celtFrameDurationMs(config int) int {
	switch config & 3 {
	case 0:
		return 2
	case 1:
		return 5
	case 2:
		return 10
	case 3:
		return 20
	}
	return 20
}

func celtFrameSamples(config, sampleRate int) int {
	switch config & 3 {
	case 0:
		return sampleRate / 400 // 2.5 ms
	case 1:
		return sampleRate / 200 // 5 ms
	case 2:
		return sampleRate / 100 // 10 ms
	case 3:
		return sampleRate / 50 // 20 ms
	}
	return sampleRate / 50
}

// adjustChannels converts between mono and stereo.
// If inputCh == outputCh, returns data unchanged.
// If inputCh=2, outputCh=1: downmix (average L+R).
// If inputCh=1, outputCh=2: upmix (duplicate).
func adjustChannels(data []float64, inputCh, outputCh int) []float64 {
	if inputCh == outputCh {
		return data
	}
	if inputCh == 2 && outputCh == 1 {
		// Downmix stereo to mono
		n := len(data) / 2
		out := make([]float64, n)
		for i := 0; i < n; i++ {
			out[i] = (data[i*2] + data[i*2+1]) * 0.5
		}
		return out
	}
	if inputCh == 1 && outputCh == 2 {
		// Upmix mono to stereo
		out := make([]float64, len(data)*2)
		for i, v := range data {
			out[i*2] = v
			out[i*2+1] = v
		}
		return out
	}
	return data
}

// splitSILKOpusFrames splits a SILK/Hybrid packet payload into per-Opus-frame SILK streams.
// The framing follows RFC 6716 §3.2 for all count codes.
// Returns the individual SILK range-coded streams (one per Opus frame).
func splitSILKOpusFrames(payload []byte, countCode int) ([][]byte, error) {
	// makeEmpty returns n empty byte slices.
	makeEmpty := func(n int) [][]byte {
		frames := make([][]byte, n)
		for i := range frames {
			frames[i] = []byte{}
		}
		return frames
	}

	switch countCode {
	case 0:
		// Single Opus frame: entire payload is one SILK range stream
		return [][]byte{payload}, nil
	case 1:
		// Two equal Opus frames: for SILK, the entire payload is one SILK range stream
		// encoding both frames sequentially (VAD bits for both frames precede both).
		return [][]byte{payload}, nil
	case 2:
		// Two SILK Opus frames share one range-coded stream after the
		// self-delimiting length header.
		stream, _ := silkCode2Stream(payload)
		return [][]byte{stream}, nil
	case 3:
		stream, _, err := silkCode3Stream(payload)
		if err != nil {
			return makeEmpty(1), err
		}
		return [][]byte{stream}, nil
	default:
		return [][]byte{payload}, nil
	}
}

// silkCode3Stream strips the Opus code-3 frame-count and padding header, then
// returns the single SILK range-coded stream and the signaled Opus frame count.
// For SILK, the bytes after the code-3 header are one range-coded stream that
// contains all Opus frames sequentially; the VBR bit does not introduce per-frame
// SILK range streams.
func silkCode3Stream(payload []byte) ([]byte, int, error) {
	if len(payload) < 1 {
		return nil, 0, fmt.Errorf("code 3: empty payload")
	}

	m := payload[0]
	frameCount := int(m & 0x3F)
	padding := (m & 0x40) != 0
	payload = payload[1:]

	if frameCount == 0 {
		frameCount = 1
	}

	if padding {
		padLen := 0
		for {
			if len(payload) < 1 {
				return []byte{}, frameCount, nil
			}
			n := int(payload[0])
			payload = payload[1:]
			if n == 255 {
				padLen += 254
				continue
			}
			padLen += n
			break
		}
		if padLen >= len(payload) {
			return []byte{}, frameCount, nil
		}
		payload = payload[:len(payload)-padLen]
	}

	return payload, frameCount, nil
}

// silkCode2Stream strips the code-2 length prefix and returns the remaining
// bytes as one SILK range-coded stream for the two signaled Opus frames.
func silkCode2Stream(payload []byte) ([]byte, int) {
	if len(payload) < 1 {
		return []byte{}, 2
	}
	n1 := int(payload[0])
	payload = payload[1:]
	if n1 >= 252 {
		if len(payload) < 1 {
			return []byte{}, 2
		}
		payload = payload[1:]
	}
	return payload, 2
}

// decodeSILKPacket decodes a SILK or Hybrid-mode packet.
//
// For SILK code-0 and code-1: the ENTIRE payload is ONE SILK range-coded stream
// encoding all Opus frames sequentially.
// For SILK code-2: the payload after the length prefix is ONE SILK stream for both frames.
// For SILK code-3: the payload after the M/padding header is ONE SILK stream for all frames.
//
// pktChannels is the number of channels in the packet (from TOC stereo bit).
func (d *Decoder) decodeSILKPacket(payload []byte, countCode, config, pktChannels int) ([]float64, error) {
	rateKHz := silkConfigRateKHz(config)
	ri := silkRateIdx(rateKHz)
	ci := pktChannels - 1

	// Determine Opus frame duration and SILK sub-frames per Opus frame
	frameDurationMs := silkConfigFrameMs(config)
	// subframesPerOpusFrame: how many 20ms (or 10ms) SILK frames are in one Opus frame
	subframesPerOpusFrame := silkSubframesPerOpusFrame(config)

	// Select frame-duration-specific decoder
	fi := 1 // 20ms decoder (also handles 40ms/60ms via nFrames=2,3)
	if frameDurationMs == 10 {
		fi = 0
	}

	if d.silkDecoders[ri][fi][ci] == nil {
		return nil, fmt.Errorf("SILK decoder not initialized for rate=%dkHz frameMs=%d ch=%d", rateKHz, frameDurationMs, pktChannels)
	}
	info := d.silkDecoders[ri][fi][ci]
	var monoPeer *silkRateInfo
	var stereoPeer *silkRateInfo
	monoPeer = d.silkDecoders[ri][fi][0]
	stereoPeer = d.silkDecoders[ri][fi][1]
	if pktChannels == 2 && monoPeer != nil && monoPeer.dec != nil {
		info.dec.CopyPrimaryStateFrom(monoPeer.dec)
	}

	// For code-0 and code-1: entire payload is ONE SILK stream with all Opus frames.
	// For code-3: strip only the Opus code-3 header/padding; the remaining
	// bytes are still one SILK range-coded stream for all signaled Opus frames.
	var silkStreams [][]byte
	var nSilkFramesPerStream int

	if config >= 12 {
		var err error
		silkStreams, err = splitOpusFrames(payload, countCode)
		if err != nil {
			silkStreams = [][]byte{payload}
		}
		nSilkFramesPerStream = subframesPerOpusFrame
	} else if countCode <= 1 {
		// Code 0 or 1: one SILK range stream for all Opus frames
		opusFrameCount := 1
		if countCode == 1 {
			opusFrameCount = 2
		}
		silkStreams = [][]byte{payload}
		nSilkFramesPerStream = opusFrameCount * subframesPerOpusFrame
	} else if countCode == 3 && config < 12 {
		stream, opusFrameCount, err := silkCode3Stream(payload)
		if err != nil {
			return nil, err
		}
		silkStreams = [][]byte{stream}
		nSilkFramesPerStream = opusFrameCount * subframesPerOpusFrame
	} else if countCode == 2 && config < 12 {
		stream, opusFrameCount := silkCode2Stream(payload)
		silkStreams = [][]byte{stream}
		nSilkFramesPerStream = opusFrameCount * subframesPerOpusFrame
	} else {
		var err error
		silkStreams, err = splitSILKOpusFrames(payload, countCode)
		if err != nil {
			silkStreams = [][]byte{payload}
		}
		nSilkFramesPerStream = subframesPerOpusFrame
	}

	// Compute expected samples per stream at output rate
	opusFrameCountPerStream := nSilkFramesPerStream / subframesPerOpusFrame
	if opusFrameCountPerStream < 1 {
		opusFrameCountPerStream = 1
	}
	samplesPerStream := (d.sampleRate * frameDurationMs * opusFrameCountPerStream / 1000) * d.channels
	if samplesPerStream < 1 {
		samplesPerStream = (d.sampleRate * frameDurationMs / 1000) * d.channels
	}

	var allPCM []float64
	for _, stream := range silkStreams {
		// Decode SILK sub-frames from this stream
		pcm, err := info.dec.DecodeMulti(stream, nSilkFramesPerStream)
		if err != nil {
			pcm = make([]float64, samplesPerStream)
		}

		// Resample from SILK rate to output rate if needed
		if info.resampler != nil {
			pcm = info.resampler.Process(pcm)
		}

		// Adjust channels from packet channels to output channels
		pcm = adjustChannels(pcm, pktChannels, d.channels)

		// Pad or trim to exact expected length
		pcm = padOrTrim(pcm, samplesPerStream)
		allPCM = append(allPCM, pcm...)
	}

	if pktChannels == 1 && stereoPeer != nil && stereoPeer.dec != nil {
		stereoPeer.dec.CopyPrimaryStateFrom(info.dec)
	} else if pktChannels == 2 && monoPeer != nil && monoPeer.dec != nil {
		monoPeer.dec.CopyPrimaryStateFrom(info.dec)
	}

	return allPCM, nil
}

// DecodeFEC decodes forward error correction data
// This is used for packet loss concealment
func (d *Decoder) DecodeFEC(data []byte, pcm []int16) (int, error) {
	// FEC decoding (currently uses PLC from FB 20ms decoder)
	fbDec := d.celtDecoders[3][3][d.channels-1]
	if fbDec == nil {
		fbDec = d.celtDecoders[3][3][0]
	}
	floatPCM, err := fbDec.DecodePLC()
	if err != nil {
		return 0, fmt.Errorf("PLC decoding failed: %w", err)
	}

	// Resample if needed
	if d.celtResampler != nil {
		floatPCM = d.celtResampler.Process(floatPCM)
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
	for bw := range d.celtDecoders {
		for lm := range d.celtDecoders[bw] {
			for ch := range d.celtDecoders[bw][lm] {
				if d.celtDecoders[bw][lm][ch] != nil {
					d.celtDecoders[bw][lm][ch].Reset()
				}
			}
		}
	}
	for ri := range d.silkDecoders {
		for fi := range d.silkDecoders[ri] {
			for ci := range d.silkDecoders[ri][fi] {
				info := d.silkDecoders[ri][fi][ci]
				if info != nil {
					info.dec.Reset()
					if info.resampler != nil {
						info.resampler.Reset()
					}
				}
			}
		}
	}
	if d.celtResampler != nil {
		d.celtResampler.Reset()
	}
	return nil
}

// GetLastPacketDuration returns the duration of the last decoded packet in samples
func (d *Decoder) GetLastPacketDuration() int {
	return d.frameSize
}
