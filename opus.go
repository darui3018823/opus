package opus

import (
	"fmt"
	"math"

	framing "github.com/darui3018823/opus/internal"
	"github.com/darui3018823/opus/internal/celt"
	"github.com/darui3018823/opus/internal/entcode"
	"github.com/darui3018823/opus/internal/resampler"
	"github.com/darui3018823/opus/internal/silk"
)

// Application selects encoder tuning for voice, general audio, or restricted
// low-delay operation. Valid values are the Application* constants.
type Application = int

// EncoderProfile selects constructor defaults without changing the encoded
// Opus format or the available controls.
type EncoderProfile int

const (
	// EncoderProfileLegacy preserves NewEncoder's historical defaults:
	// 64 kbit/s, complexity 5, and CBR.
	EncoderProfileLegacy EncoderProfile = iota
	// EncoderProfileLibopus uses libopus-style defaults: automatic bitrate,
	// complexity 9, and constrained VBR.
	EncoderProfileLibopus
)

// SignalType is a content hint that lets the encoder tune heuristics for the
// dominant signal type without changing the bitstream format.
type SignalType = celt.SignalType

const (
	// SignalAuto lets the encoder derive a hint from the Application setting
	// (VOIP → voice, Audio/RestrictedLowDelay → music).
	SignalAuto SignalType = celt.SignalUnknown
	// SignalVoice marks speech-leaning content. The encoder uses narrower
	// bandwidth tiers (matching ApplicationVOIP) and switches to short blocks
	// more eagerly on plosive onsets.
	SignalVoice SignalType = celt.SignalVoice
	// SignalMusic marks music or general audio content, applying wider
	// bandwidth tiers and standard transient sensitivity.
	SignalMusic SignalType = celt.SignalMusic
)

// Encoder represents the state of one Opus stream encoder.
//
// An Encoder is stateful, must not be copied after first use, and is not safe
// for concurrent use. Calls to Encode, configuration methods, getters, and
// Reset on the same instance must be serialized by the caller. Separate
// Encoder instances may be used concurrently.
//
// Encode methods borrow the input PCM only for the duration of the call and
// return a packet owned by the caller.
type Encoder struct {
	sampleRate  int
	channels    int
	application Application

	// CELT encoders always operate at 48 kHz internally. Separate instances
	// retain the mode-specific transform geometry for 2.5/5/10/20 ms frames.
	celtEncoder  *celt.Encoder
	celtEncoders [4]*celt.Encoder

	// SILK encoder for the speech/low-bitrate path. It operates at the packet's
	// SILK internal rate (8/12/16 kHz); 24/48 kHz voice input is downsampled to
	// 16 kHz before encoding.
	silkEncoder    *silk.Encoder
	silkSampleRate int

	// Resampler for non-48kHz input rates
	inputResampler *resampler.Resampler // inRate -> 48kHz
	silkResampler  *resampler.Resampler // input sampleRate -> silkSampleRate

	// Configuration
	bitrateSetting      int // requested bitrate or BitrateAuto/BitrateMax
	bitrate             int // effective numeric bitrate for the current frame size
	complexity          int
	rateMode            celt.RateMode // CBR/VBR/CVBR
	frameSize           int           // frame size in samples at sampleRate
	expertFrameDuration ExpertFrameDuration
	padBytes            int  // code-3 padding-data bytes to append (0 = none)
	dtx                 bool // discontinuous transmission: minimal silence packets

	// Inband FEC (SILK LBRR). useInbandFEC requests redundant coding; the SILK
	// encoder only emits LBRR when this is on and packetLossPerc > 0.
	useInbandFEC   bool
	packetLossPerc int

	// Bandwidth control (CELT-only path). maxBandwidth caps the automatic
	// selection; forcedBandwidth pins an exact bandwidth (BandwidthAuto means
	// automatic). Both use the public Bandwidth* constants.
	maxBandwidth    int
	forcedBandwidth int

	// lastDetectedBW is the framing bandwidth chosen by the signal-driven detector
	// on the previous auto-selection packet (negative = no history). It seeds the
	// detector's hysteresis so the per-packet decision does not flap near a tier
	// boundary. Only updated while bandwidth is automatic (forcedBandwidth==Auto).
	lastDetectedBW int

	// Active internal CELT frame size at 48 kHz (120/240/480/960).
	internalFrameSize int

	// prevMode is the coding mode (framing.Mode*) of the previously emitted
	// packet, or -1 before the first packet. It detects both directions of a
	// SILK/hybrid <-> CELT-only transition so a 5 ms redundant CELT frame can
	// smooth the handoff, mirroring libopus opus_encode_native.
	prevMode int

	// redundancyCelt encodes the standalone 5 ms (240 @ 48 kHz) CELT
	// frame used to smooth mode transitions. It is created lazily and reset
	// before each use so the redundant frame carries no overlap history.
	redundancyCelt *celt.Encoder

	// CTL-style observable state from the most recently encoded packet.
	lastFinalRange uint32
	inDTX          bool

	forceChannels          int
	lsbDepth               int
	predictionDisabled     bool
	phaseInversionDisabled bool
	surroundEnergyMask     []float64
	forcedMono             *Encoder
}

// isValidOpusRate returns true if the sample rate is one of the five valid Opus rates.
func isValidOpusRate(rate int) bool {
	switch rate {
	case 8000, 12000, 16000, 24000, 48000:
		return true
	}
	return false
}

func isValidApplication(application Application) bool {
	switch application {
	case ApplicationVOIP, ApplicationAudio, ApplicationRestrictedLowDelay:
		return true
	}
	return false
}

// NewEncoder creates a stateful Opus encoder using the legacy compatibility
// defaults: 64 kbit/s, complexity 5, and CBR.
//
// sampleRate must be 8000, 12000, 16000, 24000, or 48000 Hz; channels must be
// one or two. Invalid arguments return an error wrapping ErrBadArg.
func NewEncoder(sampleRate, channels int, application Application) (*Encoder, error) {
	// Validate parameters
	if !isValidOpusRate(sampleRate) {
		return nil, fmt.Errorf("%w: %w: %d", ErrBadArg, ErrUnsupportedSampleRate, sampleRate)
	}

	if channels != 1 && channels != 2 {
		return nil, fmt.Errorf("%w: %w: %d", ErrBadArg, ErrUnsupportedChannels, channels)
	}
	if !isValidApplication(application) {
		return nil, fmt.Errorf("%w: unsupported application %d", ErrBadArg, application)
	}

	// Frame size at the caller's sample rate (20ms)
	frameSize := (sampleRate * 20) / 1000

	// Start with the 20 ms CELT geometry; shorter modes are created lazily.
	internalFrameSize := 960

	// Create CELT encoder at 48kHz
	celtEnc, err := celt.NewEncoder(celt.FrameSize20ms, 48000, channels, celt.DefaultEncoderConfig())
	if err != nil {
		return nil, fmt.Errorf("failed to create CELT encoder: %w", err)
	}

	enc := &Encoder{
		sampleRate:          sampleRate,
		channels:            channels,
		application:         application,
		celtEncoder:         celtEnc,
		bitrateSetting:      64000,
		bitrate:             64000,            // Default bitrate
		complexity:          5,                // Default complexity
		rateMode:            celt.RateModeCBR, // Default CBR (backward compatible)
		frameSize:           frameSize,
		expertFrameDuration: ExpertFrameDurationArgument,
		maxBandwidth:        BandwidthFullband,
		forcedBandwidth:     BandwidthAuto,
		lastDetectedBW:      -1, // no detection history yet
		internalFrameSize:   internalFrameSize,
		prevMode:            -1, // no previous packet yet
		forceChannels:       ChannelsAuto,
		lsbDepth:            LSBDepthDefault,
	}
	enc.celtEncoders[3] = celtEnc

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
	enc.celtEncoder.SetSignalType(signalTypeForApplication(application))

	if silkRate, ok := silkEncodeSampleRate(sampleRate); ok {
		silkEnc, err := silk.NewEncoder(silkRate, channels)
		if err != nil {
			return nil, fmt.Errorf("failed to create SILK encoder: %w", err)
		}
		_ = silkEnc.SetComplexity(enc.complexity)
		silkEnc.SetRateMode(silk.RateModeCBR)
		enc.silkEncoder = silkEnc
		enc.silkSampleRate = silkRate
		if silkRate != sampleRate {
			r, err := resampler.NewResampler(sampleRate, silkRate, channels, resampler.QualityDefault)
			if err != nil {
				return nil, fmt.Errorf("failed to create SILK input resampler: %w", err)
			}
			enc.silkResampler = r
		}
	}

	return enc, nil
}

// NewEncoderWithProfile creates an encoder with an explicit defaults profile.
// NewEncoder remains equivalent to EncoderProfileLegacy for compatibility.
// The sample-rate and channel constraints are the same as NewEncoder.
func NewEncoderWithProfile(sampleRate, channels int, application Application, profile EncoderProfile) (*Encoder, error) {
	if profile != EncoderProfileLegacy && profile != EncoderProfileLibopus {
		return nil, fmt.Errorf("%w: unsupported encoder profile %d", ErrBadArg, profile)
	}
	enc, err := NewEncoder(sampleRate, channels, application)
	if err != nil {
		return nil, err
	}
	if profile == EncoderProfileLibopus {
		if err := enc.SetBitrate(BitrateAuto); err != nil {
			return nil, err
		}
		if err := enc.SetComplexity(ComplexityDefault); err != nil {
			return nil, err
		}
		enc.SetVBR(true)
		enc.SetVBRConstraint(true)
	}
	return enc, nil
}

// signalTypeForApplication maps an Opus application to the CELT content hint used
// by application-driven encoder heuristics (e.g. the patch-transient
// sensitivity). VOIP is treated as voice; general audio and restricted-low-delay
// are treated as music/general.
func signalTypeForApplication(application Application) celt.SignalType {
	switch application {
	case ApplicationVOIP:
		return celt.SignalVoice
	default: // ApplicationAudio, ApplicationRestrictedLowDelay
		return celt.SignalMusic
	}
}

// Encode encodes PCM audio samples
//
// pcm contains interleaved 16-bit PCM samples (left, right, left, right, ...)
// frameSize is the number of samples per channel (at the encoder's sample rate).
// With a fixed expert frame duration, frameSize is the available sample count.
// Returns compressed Opus packet
func (e *Encoder) Encode(pcm []int16, frameSize int) ([]byte, error) {
	selectedFrameSize, err := e.selectEncodeFrameSize(frameSize)
	if err != nil {
		return nil, err
	}
	expectedSize := frameSize * e.channels
	if len(pcm) < expectedSize {
		return nil, fmt.Errorf("%w: insufficient PCM data: got %d, need %d", ErrBadArg, len(pcm), expectedSize)
	}

	// Convert int16 to float64
	selectedSize := selectedFrameSize * e.channels
	floatPCM := make([]float64, selectedSize)
	for i := 0; i < selectedSize; i++ {
		floatPCM[i] = float64(pcm[i]) / 32768.0
	}

	return e.encodeFloat(floatPCM, selectedFrameSize)
}

// Encode24 encodes interleaved signed 24-bit PCM stored in int32 values.
// The nominal input range is [-8388608, 8388607]. frameSize is samples per
// channel, and pcm must contain at least frameSize*Channels() values unless a
// fixed expert duration selects a smaller prefix.
func (e *Encoder) Encode24(pcm []int32, frameSize int) ([]byte, error) {
	selectedFrameSize, err := e.selectEncodeFrameSize(frameSize)
	if err != nil {
		return nil, err
	}
	expectedSize := frameSize * e.channels
	if len(pcm) < expectedSize {
		return nil, fmt.Errorf("%w: insufficient PCM data: got %d, need %d", ErrBadArg, len(pcm), expectedSize)
	}
	selectedSize := selectedFrameSize * e.channels
	floatPCM := make([]float64, selectedSize)
	for i := range floatPCM {
		floatPCM[i] = float64(pcm[i]) / 8388608.0
	}
	return e.encodeFloat(floatPCM, selectedFrameSize)
}

// EncodeFloat encodes floating-point PCM samples
//
// pcm contains interleaved float64 samples in range [-1.0, 1.0]
// frameSize is the number of samples per channel (at the encoder's sample rate).
// With a fixed expert frame duration, frameSize is the available sample count.
func (e *Encoder) EncodeFloat(pcm []float64, frameSize int) ([]byte, error) {
	selectedFrameSize, err := e.selectEncodeFrameSize(frameSize)
	if err != nil {
		return nil, err
	}
	expectedSize := frameSize * e.channels
	if len(pcm) < expectedSize {
		return nil, fmt.Errorf("%w: insufficient PCM data: got %d, need %d", ErrBadArg, len(pcm), expectedSize)
	}

	selectedSize := selectedFrameSize * e.channels
	return e.encodeFloat(pcm[:selectedSize], selectedFrameSize)
}

// EncodeFloat32 encodes interleaved float32 PCM samples in range [-1.0, 1.0].
// frameSize is the number of samples per channel at the encoder sample rate.
// With a fixed expert frame duration, frameSize is the available sample count.
func (e *Encoder) EncodeFloat32(pcm []float32, frameSize int) ([]byte, error) {
	selectedFrameSize, err := e.selectEncodeFrameSize(frameSize)
	if err != nil {
		return nil, err
	}
	expectedSize := frameSize * e.channels
	if len(pcm) < expectedSize {
		return nil, fmt.Errorf("%w: insufficient PCM data: got %d, need %d", ErrBadArg, len(pcm), expectedSize)
	}
	selectedSize := selectedFrameSize * e.channels
	floatPCM := make([]float64, selectedSize)
	for i := range floatPCM {
		floatPCM[i] = float64(pcm[i])
	}
	return e.encodeFloat(floatPCM, selectedFrameSize)
}

// encodeFloat is the internal encoding path shared by Encode and EncodeFloat.
//
// Short 2.5/5/10 ms requests use their corresponding CELT geometry. Requests
// that are exact multiples (1..6) of the 20 ms base are split into consecutive
// 20 ms frames and packed as one Opus packet (RFC 6716 §3.2).
func (e *Encoder) encodeFloat(pcm []float64, frameSize int) ([]byte, error) {
	if e.forceChannels == ChannelsMono && e.channels == ChannelsStereo {
		mono := make([]float64, frameSize)
		for i := 0; i < frameSize; i++ {
			mono[i] = 0.5 * (pcm[2*i] + pcm[2*i+1])
		}
		child, err := e.monoEncoder()
		if err != nil {
			return nil, err
		}
		packet, err := child.encodeFloat(mono, frameSize)
		if err == nil {
			e.lastFinalRange = child.lastFinalRange
			e.inDTX = child.inDTX
		}
		return packet, err
	}
	e.inDTX = e.dtx && isSilentPCM(pcm)
	if err := e.selectCELTEncoder(frameSize); err != nil {
		return nil, err
	}
	if err := e.applyBitrateSetting(frameSize); err != nil {
		return nil, err
	}
	if frameSize < e.frameSize {
		return e.encodeShortCELTPacket(pcm)
	}
	nFrames := frameSize / e.frameSize
	if e.shouldEncodeSILKOnly() {
		celtToSilk := e.prevMode == framing.ModeCELTOnly
		out, err := e.encodeSILKOnlyPacket(pcm, nFrames, celtToSilk)
		if err == nil {
			e.prevMode = framing.ModeSILKOnly
		}
		return out, err
	}
	bw := -1
	hybrid := false
	if e.shouldEncodeHybrid(nFrames) {
		bw = e.narrowAutoHybridBandwidth(pcm, e.selectHybridBandwidth())
		hybrid = bw == framing.BandwidthSuperwideband || bw == framing.BandwidthFullband
	}

	// libopus-faithful hybrid->CELT transition: when the previous packet was
	// hybrid and this one would switch to CELT-only, defer the switch by one
	// packet. This packet stays hybrid and carries a trailing 5 ms redundant CELT
	// frame whose state seeds the next (genuinely CELT-only) packet, smoothing the
	// handoff (opus_encode_native: prev_mode!=CELT && mode==CELT -> redundancy).
	redundancy := false
	celtToSilk := false
	if !hybrid && e.prevMode == framing.ModeHybrid && e.canDeferToHybrid(nFrames) {
		bw = e.deferredHybridBandwidth(pcm)
		hybrid = true
		redundancy = true
	} else if hybrid && e.prevMode == framing.ModeCELTOnly {
		redundancy = true
		celtToSilk = true
	}

	if hybrid {
		out, redundancyEmitted, err := e.encodeHybridPacket(pcm, nFrames, bw, redundancy, celtToSilk)
		if err == nil {
			// to_celt: after the deferred frame the real switch happens, so the
			// next packet's predecessor is CELT-only (opus_encode_native).
			if redundancyEmitted && !celtToSilk {
				e.prevMode = framing.ModeCELTOnly
			} else {
				e.prevMode = framing.ModeHybrid
			}
		}
		return out, err
	}

	// Select the coded bandwidth (NB/WB/SWB/FB) and limit the CELT encoder's
	// coded bands to match, then generate the base TOC byte for that bandwidth.
	// The per-frame duration is always 20 ms; multi-frame packets express longer
	// durations via the count code rather than a different config. The config-driven
	// ceiling (sample rate, bitrate, explicit settings) is the widest bandwidth
	// allowed; when selection is automatic it is further narrowed by analysing the
	// actual signal, so a source with no high-frequency energy is coded in a
	// narrower band rather than wasting bits. The detection runs once over the whole
	// input PCM, so every frame in a packet still shares the same bandwidth/config.
	if bw < 0 {
		bw = e.narrowAutoBandwidth(pcm, e.selectCeltBandwidth())
	}
	e.celtEncoder.SetEndBand(celtEndBandForFramingBW(bw))
	toc, err := framing.GenerateTOCExt(framing.ModeCELTOnly, bw, e.channels, framing.FrameSize20ms)
	if err != nil {
		return nil, fmt.Errorf("failed to generate TOC: %w", err)
	}

	// base = 20 ms in samples at the caller's sample rate.
	base := e.frameSize

	// Single-frame, no padding: compact code-0 packet (TOC + payload).
	if nFrames == 1 && e.padBytes <= 0 {
		compressed, err := e.encodeOneCELTFrame(pcm)
		if err != nil {
			return nil, err
		}
		e.prevMode = framing.ModeCELTOnly
		e.lastFinalRange = e.celtEncoder.FinalRange()
		return append([]byte{toc}, compressed...), nil
	}

	// Encode each 20 ms chunk continuously (the resampler and CELT encoder keep
	// their inter-frame state across chunks) and pack the frames. A single frame
	// reaches here only when padding was requested, in which case it is wrapped in
	// a code-3 packet (the only count code that carries padding).
	chunkLen := base * e.channels
	frames := make([][]byte, 0, nFrames)
	var rangeFinal uint32
	for k := 0; k < nFrames; k++ {
		chunk := pcm[k*chunkLen : (k+1)*chunkLen]
		f, err := e.encodeOneCELTFrame(chunk)
		if err != nil {
			return nil, err
		}
		frames = append(frames, f)
		rangeFinal ^= e.celtEncoder.FinalRange()
	}

	// CBR packs frames of equal size with the most compact code; VBR/CVBR
	// frames vary in size and need explicit length prefixes. Padding (when
	// requested) forces a code-3 packet with the padding flag. DTX may turn an
	// otherwise-CBR run of frames into mixed sizes (silent frames shrink), so it
	// also needs the variable-length packing path.
	vbr := e.rateMode != celt.RateModeCBR || e.dtx
	payload, code, err := packOpusFramesPadded(frames, vbr, e.padBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to pack %d frames: %w", nFrames, err)
	}
	e.prevMode = framing.ModeCELTOnly
	e.lastFinalRange = rangeFinal
	return append([]byte{toc | byte(code)}, payload...), nil
}

func (e *Encoder) encodeShortCELTPacket(pcm []float64) ([]byte, error) {
	bw := e.narrowAutoBandwidth(pcm, e.selectCeltBandwidth())
	e.celtEncoder.SetEndBand(celtEndBandForFramingBW(bw))
	toc, err := framing.GenerateTOCExt(framing.ModeCELTOnly, bw, e.channels, e.internalFrameSize)
	if err != nil {
		return nil, fmt.Errorf("failed to generate short-frame TOC: %w", err)
	}
	compressed, err := e.encodeOneCELTFrame(pcm)
	if err != nil {
		return nil, err
	}
	e.prevMode = framing.ModeCELTOnly
	e.lastFinalRange = e.celtEncoder.FinalRange()
	if e.padBytes <= 0 {
		return append([]byte{toc}, compressed...), nil
	}
	payload, code, err := packOpusFramesPadded([][]byte{compressed}, e.rateMode != celt.RateModeCBR || e.dtx, e.padBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to pad short CELT frame: %w", err)
	}
	return append([]byte{toc | byte(code)}, payload...), nil
}

// canDeferToHybrid reports whether the encoder can keep this packet in hybrid
// mode for one more frame to carry a transition-smoothing redundant CELT frame.
// The hybrid SILK layer is always the 16 kHz wideband encoder, so the SILK
// encoder must exist at that internal rate; low-delay never uses hybrid.
func (e *Encoder) canDeferToHybrid(nFrames int) bool {
	if e.predictionDisabled {
		return false
	}
	if e.silkEncoder == nil || e.silkSampleRate != 16000 {
		return false
	}
	if e.application == ApplicationRestrictedLowDelay {
		return false
	}
	return nFrames >= 1 && nFrames <= 6
}

// deferredHybridBandwidth picks the coded bandwidth for a deferred (transition)
// hybrid packet. Hybrid requires SWB or FB; when the signal-driven selection has
// narrowed below SWB (which is why the encoder wanted to switch to CELT), the
// single transitional frame is clamped up to SWB so it remains a valid hybrid
// packet, mirroring libopus reverting mode to the previous (hybrid) mode.
func (e *Encoder) deferredHybridBandwidth(pcm []float64) int {
	bw := e.narrowAutoHybridBandwidth(pcm, e.selectHybridBandwidth())
	if bw != framing.BandwidthSuperwideband && bw != framing.BandwidthFullband {
		bw = framing.BandwidthSuperwideband
	}
	return bw
}

// computeRedundancyBytes mirrors libopus compute_redundancy_bytes: it sizes the
// trailing 5 ms redundant CELT frame from the target bitrate and the per-frame
// byte budget, returning 0 when too few bits are available for redundancy to be
// worthwhile (the decoder then relies on PLC).
func computeRedundancyBytes(maxDataBytes, bitrate, frameRate, channels int) int {
	if frameRate <= 0 || channels <= 0 {
		return 0
	}
	baseBits := 40*channels + 20
	// Equivalent rate for 5 ms frames, then a 3/2 VBR boost (libopus).
	redundancyRate := bitrate + baseBits*(200-frameRate)
	redundancyRate = 3 * redundancyRate / 2
	redundancyBytes := redundancyRate / 1600

	availableBits := maxDataBytes*8 - 2*baseBits
	cap := (availableBits*240/(240+48000/frameRate) + baseBits) / 8
	if redundancyBytes > cap {
		redundancyBytes = cap
	}
	if redundancyBytes > 4+8*channels {
		if redundancyBytes > 257 {
			redundancyBytes = 257
		}
	} else {
		redundancyBytes = 0
	}
	return redundancyBytes
}

// redundancyEncoder lazily builds the dedicated 5 ms (240 @ 48 kHz)
// CELT encoder used for transition redundancy.
func (e *Encoder) redundancyEncoder() (*celt.Encoder, error) {
	if e.redundancyCelt == nil {
		c, err := celt.NewEncoder(celt.FrameSize5ms, 48000, e.channels, celt.DefaultEncoderConfig())
		if err != nil {
			return nil, fmt.Errorf("failed to create redundancy CELT encoder: %w", err)
		}
		c.SetPhaseInversionDisabled(e.phaseInversionDisabled)
		e.redundancyCelt = c
	}
	return e.redundancyCelt, nil
}

// encodeRedundantFrame encodes either the leading or trailing 5 ms of a 20 ms
// 48 kHz CELT input as a standalone CELT packet of exactly nbytes.
//
// seed controls the starting state of the dedicated 5 ms encoder, mirroring how
// libopus's single celt_enc carries (or resets) state across the transition:
//   - seed == nil: the encoder is reset, so the frame carries no overlap history
//     (used for SILK->CELT trailing redundancy, where the decoder also resets).
//   - seed != nil: the encoder continues from seed's state (used for CELT->SILK
//     leading redundancy, where the decoder decodes the frame from its previous
//     CELT state). Pass the encoder that holds the previous CELT-only state.
func (e *Encoder) encodeRedundantFrame(celtInput []float64, nbytes int, leading bool, endBand int, seed *celt.Encoder) ([]byte, error) {
	red, err := e.redundancyEncoder()
	if err != nil {
		return nil, err
	}
	const redSamples = celt.FrameSize5ms // 240 samples @ 48 kHz
	tailLen := redSamples * e.channels
	part := celtInput
	if len(celtInput) >= tailLen {
		if leading {
			part = celtInput[:tailLen]
		} else {
			part = celtInput[len(celtInput)-tailLen:]
		}
	} else {
		part = padOrTrim(celtInput, tailLen)
	}
	if seed != nil {
		red.CopyStateFrom(seed)
	} else {
		red.Reset()
	}
	red.SetEndBand(endBand)
	red.SetBitrate(e.bitrate)
	red.SetPhaseInversionDisabled(e.phaseInversionDisabled)
	return red.EncodeRedundant(part, nbytes)
}

func (e *Encoder) shouldEncodeSILKOnly() bool {
	if e.predictionDisabled {
		return false
	}
	if e.silkEncoder == nil {
		return false
	}
	if e.application == ApplicationRestrictedLowDelay {
		return false
	}

	// SILK encode support is intentionally narrow: only speech intent may enter
	// it. ApplicationVOIP derives voice intent by default, SignalVoice can opt
	// other applications in, and SignalMusic explicitly keeps the packet on CELT.
	if !e.hasVoiceIntent() {
		return false
	}
	if e.bitrate > e.silkOnlyBitrateLimit() {
		return false
	}
	nativeBW, ok := nativeSilkFramingBandwidth(e.silkSampleRate)
	if !ok {
		return false
	}
	nativePublic := silkFramingBWToPublic(nativeBW)
	if e.forcedBandwidth != BandwidthAuto {
		forced := celtFramingBWToPublic(nyquistClampFramingBW(publicToCeltFramingBW(e.forcedBandwidth), e.sampleRate))
		if forced != nativePublic {
			return false
		}
	}
	if e.forcedBandwidth == BandwidthAuto && bandwidthRank(e.maxBandwidth) < bandwidthRank(nativePublic) {
		return false
	}
	return true
}

func (e *Encoder) shouldEncodeHybrid(nFrames int) bool {
	if e.predictionDisabled {
		return false
	}
	if e.silkEncoder == nil || e.silkSampleRate != 16000 {
		return false
	}
	if nFrames < 1 || nFrames > 6 {
		return false
	}
	if e.application == ApplicationRestrictedLowDelay {
		return false
	}
	if !e.hasVoiceIntent() || e.bitrate <= e.silkOnlyBitrateLimit() ||
		e.bitrate > e.hybridBitrateLimit() {
		return false
	}
	bw := e.selectHybridBandwidth()
	return bw == framing.BandwidthSuperwideband || bw == framing.BandwidthFullband
}

// silkOnlyBitrateLimit is the upper mode boundary for the current channel/loss
// configuration. Stereo receives a larger aggregate budget; active in-band FEC
// extends the predictive-mode region because CELT-only cannot carry LBRR.
func (e *Encoder) silkOnlyBitrateLimit() int {
	limit := 40000
	if e.channels == 2 {
		limit = 48000
	}
	if e.useInbandFEC && e.packetLossPerc > 0 {
		limit += 8000 * e.channels
	}
	return limit
}

// hybridBitrateLimit is the upper useful hybrid boundary. Above it the full
// bandwidth CELT path gets the entire packet budget instead of retaining a
// fixed 16 kHz SILK low band. FEC extends the boundary because hybrid can carry
// LBRR while CELT-only cannot.
func (e *Encoder) hybridBitrateLimit() int {
	limit := 112000
	if e.channels == 2 {
		limit = 192000
	}
	if e.useInbandFEC && e.packetLossPerc > 0 {
		limit += 16000 * e.channels
	}
	return limit
}

func (e *Encoder) hasVoiceIntent() bool {
	signal := e.celtEncoder.SignalTypeHint()
	return signal == SignalVoice || (signal == SignalAuto && e.application == ApplicationVOIP)
}

func (e *Encoder) selectHybridBandwidth() int {
	bw := e.selectCeltBandwidth()
	if bw < framing.BandwidthSuperwideband {
		return bw
	}
	if e.sampleRate == 24000 && bw > framing.BandwidthSuperwideband {
		return framing.BandwidthSuperwideband
	}
	return bw
}

func (e *Encoder) narrowAutoBandwidth(pcm []float64, bw int) int {
	if e.forcedBandwidth != BandwidthAuto {
		return bw
	}
	det := detectSignalBandwidth(pcm, e.channels, e.sampleRate, e.lastDetectedBW)
	e.lastDetectedBW = det
	if det < bw {
		return det
	}
	return bw
}

func (e *Encoder) narrowAutoHybridBandwidth(pcm []float64, bw int) int {
	if e.forcedBandwidth != BandwidthAuto {
		return bw
	}
	det, sparse := detectSignalBandwidthAndSparsity(pcm, e.channels, e.sampleRate, e.lastDetectedBW)
	if det < framing.BandwidthSuperwideband && sparse {
		e.lastDetectedBW = bw
		return bw
	}
	e.lastDetectedBW = det
	if det < bw {
		return det
	}
	return bw
}

func (e *Encoder) encodeHybridPacket(pcm []float64, nFrames, bw int, redundancy, celtToSilk bool) ([]byte, bool, error) {
	toc, err := framing.GenerateTOCExt(framing.ModeHybrid, bw, e.channels, framing.FrameSize20ms)
	if err != nil {
		return nil, false, fmt.Errorf("failed to generate hybrid TOC: %w", err)
	}

	inputChunkLen := e.frameSize * e.channels
	silkFrameSize := e.silkSampleRate * 20 / 1000
	silkChunkLen := silkFrameSize * e.channels
	celtEnd := celtEndBandForFramingBW(bw)
	nominalBytes := e.hybridFrameTargetBytes()
	// CBR keeps every hybrid frame at the full per-frame ceiling. In VBR/CVBR,
	// CELT selects the final shared payload size immediately before allocation.
	cbr := e.rateMode == celt.RateModeCBR

	frames := make([][]byte, 0, nFrames)
	var rangeFinal uint32
	redundancyEmitted := false
	for k := 0; k < nFrames; k++ {
		chunk := pcm[k*inputChunkLen : (k+1)*inputChunkLen]
		silkPCM := chunk
		if e.silkResampler != nil {
			silkPCM = e.silkResampler.Process(chunk)
			silkPCM = padOrTrim(silkPCM, silkChunkLen)
		}
		celtInput := e.celtInputFrame(chunk)

		// CELT->SILK redundancy is carried by the first frame; SILK->CELT
		// redundancy is carried by the last frame (libopus frame_redundancy).
		frameRedundancy := redundancy &&
			((celtToSilk && k == 0) || (!celtToSilk && k == nFrames-1))

		maxBytes := nominalBytes
		if !cbr && !frameRedundancy {
			// VBR's available packet capacity is distinct from its nominal rate
			// target. CELT will shrink this RFC frame ceiling to the size selected
			// from the SILK tell state and its own high-band target.
			maxBytes = MaxFrameBytes
		}
		enc := entcode.NewEncoder(maxBytes)
		e.silkEncoder.SetHybridMode(true)
		err := e.silkEncoder.EncodeMultiWithEncoder(enc, silkPCM, 1)
		e.silkEncoder.SetHybridMode(false)
		if err != nil {
			return nil, false, fmt.Errorf("SILK hybrid encoding failed: %w", err)
		}

		// Redundancy flag (logp 12) is written between SILK and CELT, then
		// celt_to_silk (0 for SILK->CELT) and the redundant frame length. The gate
		// mirrors libopus: 17 bits of redundancy overhead + 20 bits for the hybrid
		// flag/size must fit. When redundancy is not selected (or does not fit), a
		// false flag is written and the frame stays plain hybrid.
		redundancyBytes := 0
		if frameRedundancy && enc.ECTell()+17+20 <= maxBytes*8 {
			redundancyBytes = computeRedundancyBytes(maxBytes, e.bitrate, e.sampleRate/e.frameSize, e.channels)
			// Reserve 8 bits for the length plus a few for CELT (libopus
			// max_redundancy for the hybrid branch).
			maxRedundancy := (maxBytes - 1) - ((enc.ECTell() + 8 + 3 + 7) >> 3)
			if redundancyBytes > maxRedundancy {
				redundancyBytes = maxRedundancy
			}
			if redundancyBytes > 257 {
				redundancyBytes = 257
			}
		}
		if redundancyBytes < 2 {
			redundancyBytes = 0
			frameRedundancy = false
		}
		if enc.ECTell()+37 <= maxBytes*8 {
			enc.EncodeBitLogp(frameRedundancy, 12)
			if frameRedundancy {
				enc.EncodeBitLogp(celtToSilk, 1)
				enc.EncodeUint(uint32(redundancyBytes-2), 256)
			}
		} else {
			frameRedundancy = false
			redundancyBytes = 0
		}

		// With redundancy the main shared stream remains fixed at maxBytes minus
		// the trailing redundant frame. Non-redundant VBR starts with maxBytes and
		// lets CELT shrink the entropy coder after its VBR analysis and before bit
		// allocation, matching celt_encode_with_ec.
		frameBytes := maxBytes
		targetBytes := maxBytes
		if frameRedundancy {
			targetBytes = maxBytes - redundancyBytes
			enc.Shrink(targetBytes)
		}
		// CELT->SILK leading redundant frame must be encoded from the previous
		// CELT-only state, which celtEncoder still holds at this point (k==0, before
		// the hybrid high band reuses it). libopus encodes the leading redundant
		// frame first, then resets celt_enc for the hybrid high band. We compute it
		// here (state-faithful) and append it to the frame tail below.
		var leadingRedFrame []byte
		if frameRedundancy && celtToSilk {
			rf, rerr := e.encodeRedundantFrame(celtInput, redundancyBytes, true, celtEnd, e.celtEncoder)
			if rerr != nil {
				return nil, false, fmt.Errorf("CELT leading redundant frame encoding failed: %w", rerr)
			}
			leadingRedFrame = rf
			// The new hybrid high-band stream starts from a fresh CELT state.
			e.celtEncoder.Reset()
		}
		// libopus gives hybrid CELT the total bitrate minus the SILK target and
		// disables CELT's own VBR constraint. hybridCELTBitrate adapts that split
		// to this encoder's natural-size SILK VBR output.
		if frameRedundancy {
			e.celtEncoder.SetRateMode(celt.RateModeCBR)
		} else if !cbr {
			e.celtEncoder.SetRateMode(celt.RateModeVBR)
			e.celtEncoder.SetBitrate(e.hybridCELTBitrate(enc.ECTell()))
		}
		chosenBytes, celtErr := e.celtEncoder.EncodeHybrid(
			celtInput, enc, targetBytes, 17, celtEnd, isSilentPCM(chunk),
		)
		e.celtEncoder.SetRateMode(e.rateMode)
		e.celtEncoder.SetBitrate(e.bitrate)
		if celtErr != nil {
			return nil, false, fmt.Errorf("CELT hybrid encoding failed: %w", celtErr)
		}
		targetBytes = chosenBytes
		if !frameRedundancy {
			frameBytes = targetBytes
		}
		rangeFinal ^= enc.GetRng()
		enc.Flush()
		frame := enc.Bytes()
		if len(frame) > targetBytes {
			return nil, false, fmt.Errorf("hybrid frame %d exceeds target: %d > %d bytes", k, len(frame), targetBytes)
		}
		if len(frame) < targetBytes {
			padded := make([]byte, targetBytes)
			copy(padded, frame)
			frame = padded
		}

		// Append the 5 ms redundant CELT frame so the decoder recovers it from
		// stream[len-redundancyBytes:] (opus.go decodeHybridPacket). The CELT->SILK
		// leading frame was already computed above (from the previous CELT-only
		// state); the SILK->CELT trailing frame is computed here from a fresh state.
		if frameRedundancy {
			var redFrame []byte
			if celtToSilk {
				redFrame = leadingRedFrame
			} else {
				rf, rerr := e.encodeRedundantFrame(celtInput, redundancyBytes, false, celt.NumBands48000, nil)
				if rerr != nil {
					return nil, false, fmt.Errorf("CELT redundant frame encoding failed: %w", rerr)
				}
				redFrame = rf
				// SILK->CELT: the next (genuinely CELT-only) packet continues from the
				// trailing redundant frame's state, mirroring the decoder which adopts
				// the redundant decoder's state (celtDec.CopyStateFrom(redDec)).
				e.celtEncoder.CopyStateFrom(e.redundancyCelt)
			}
			redPadded := make([]byte, redundancyBytes)
			copy(redPadded, redFrame)
			frame = append(frame, redPadded...)
			redundancyEmitted = true
		}
		if len(frame) != frameBytes && frameRedundancy {
			// Defensive: redundant frame must total exactly maxBytes.
			return nil, false, fmt.Errorf("hybrid redundant frame %d size %d != %d", k, len(frame), frameBytes)
		}
		frames = append(frames, frame)
	}

	// Each frame is padded to its own targetBytes. In CBR those are equal, so
	// pass vbr=false (compact code 1 for 2 frames); in VBR they vary, so the
	// variable-length packing path (length prefixes / code 2/3) is required.
	payload, code, err := packOpusFramesPadded(frames, !cbr, e.padBytes)
	if err != nil {
		return nil, false, fmt.Errorf("failed to pack hybrid stream: %w", err)
	}
	e.lastFinalRange = rangeFinal
	return append([]byte{toc | byte(code)}, payload...), redundancyEmitted, nil
}

func (e *Encoder) hybridFrameTargetBytes() int {
	tb := int(float64(e.bitrate) * 0.020 / 8.0)
	if tb < 2 {
		tb = 2
	}
	if tb > 1275 {
		tb = 1275
	}
	return tb
}

func (e *Encoder) hybridCELTBitrate(silkBits int) int {
	// The current SILK VBR encoder can use substantially less than its configured
	// target on easy speech. Base the split on the bits actually present in the
	// shared stream so CELT's VBR target preserves the requested total rate.
	frameRate := e.sampleRate / e.frameSize
	silkBitrate := silkBits * frameRate
	celtBitrate := e.bitrate - silkBitrate
	if celtBitrate < 1 {
		celtBitrate = 1
	}
	return celtBitrate
}

func (e *Encoder) encodeSILKOnlyPacket(pcm []float64, nFrames int, celtToSilk bool) ([]byte, error) {
	bw, ok := nativeSilkFramingBandwidth(e.silkSampleRate)
	if !ok {
		return nil, fmt.Errorf("SILK-only encoding not available for %d Hz", e.sampleRate)
	}

	groupSize, groups, err := silkPacketGroupsForChannels(nFrames, e.channels)
	if err != nil {
		return nil, err
	}
	tocFrameSize := groupSize * framing.FrameSize20ms
	toc, err := framing.GenerateTOCExt(framing.ModeSILKOnly, bw, e.channels, tocFrameSize)
	if err != nil {
		return nil, fmt.Errorf("failed to generate SILK TOC: %w", err)
	}

	inputChunkLen := e.frameSize * e.channels
	silkFrameSize := e.silkSampleRate * 20 / 1000
	silkChunkLen := silkFrameSize * e.channels
	streams := make([][]byte, 0, len(groups))
	var rangeFinal uint32
	pos := 0
	for gi, group := range groups {
		inputSamples := group * inputChunkLen
		silkPCM := pcm[pos : pos+inputSamples]
		if e.silkResampler != nil {
			silkPCM = e.silkResampler.Process(silkPCM)
			silkPCM = padOrTrim(silkPCM, group*silkChunkLen)
		}
		silent := isSilentPCM(silkPCM)
		encodeSILK := !silent
		if silent {
			switch {
			case e.silkEncoder.HasPendingLBRR(group):
				// A silent current frame must still carry redundancy generated by
				// the previous active packet. Running the normal SILK path emits
				// that LBRR before advancing the inactive-frame state.
				encodeSILK = true
			case e.silkEncoder.HasAnyPendingLBRR():
				// A duration change can make the old LBRR syntax incompatible.
				// Expire it now so it cannot be mislabelled by a later packet.
				e.silkEncoder.DiscardPendingLBRR()
			}
		}
		stream := []byte{0x00}
		frameRedundancy := celtToSilk && gi == 0 && !silent
		if encodeSILK {
			e.inDTX = false
			conservativeNSQ := e.shouldUseConservativeSILKNSQ(group)
			prevTrellis := e.silkEncoder.TrellisNSQ()
			var sharedEnc *entcode.Encoder
			if conservativeNSQ {
				e.silkEncoder.SetTrellisNSQ(false)
			}
			if frameRedundancy {
				// SILK-only redundancy has no explicit flag or byte count: after
				// SILK, celt_to_silk is coded and every remaining byte is the
				// redundant CELT frame.
				nominalBytes := e.silkStreamTargetBytes(group)
				redBytes := computeRedundancyBytes(nominalBytes, e.bitrate, e.sampleRate/e.frameSize, e.channels)
				// The current simplified SILK rate controller can exceed its
				// nominal target substantially. Encode into the Opus frame ceiling,
				// then place redundancy immediately after the bytes SILK actually
				// needs. This preserves the normative SILK-only layout without
				// truncating the entropy stream.
				sharedEnc = entcode.NewEncoder(1275)
				err = e.silkEncoder.EncodeMultiWithEncoder(sharedEnc, silkPCM, group)
				mainBytes := (sharedEnc.ECTell() + 1 + 7) >> 3
				if err == nil && redBytes >= 2 && sharedEnc.ECTell()+17 <= (mainBytes+redBytes)*8 {
					maxRedundancy := 1275 - mainBytes
					if redBytes > maxRedundancy {
						redBytes = maxRedundancy
					}
					if redBytes >= 2 {
						sharedEnc.EncodeBitLogp(true, 1)
						mainBytes = (sharedEnc.ECTell() + 7) >> 3
						sharedEnc.Shrink(mainBytes)
						sharedEnc.Flush()
						stream = sharedEnc.Bytes()
						celtInput := e.celtInputFrame(pcm[pos : pos+inputChunkLen])
						// CELT->SILK leading redundancy: seed the redundant frame from
						// the previous CELT-only state (celtEncoder is untouched on the
						// SILK-only path), matching the decoder which decodes it with its
						// previous CELT state (decodeLeadingRedundancy copies lastCeltDec).
						redFrame, rerr := e.encodeRedundantFrame(celtInput, redBytes, true, 17, e.celtEncoder)
						if rerr != nil {
							err = rerr
						} else {
							stream = append(stream, redFrame...)
						}
					} else {
						frameRedundancy = false
					}
				} else if err == nil {
					frameRedundancy = false
				}
			}
			if !frameRedundancy && err == nil {
				if sharedEnc != nil {
					// The attempted redundancy encode already advanced SILK state.
					// Finish that same entropy stream as plain SILK.
					sharedEnc.Flush()
					stream = sharedEnc.Bytes()
				} else {
					stream, err = e.silkEncoder.EncodeMulti(silkPCM, group)
				}
			}
			if conservativeNSQ {
				e.silkEncoder.SetTrellisNSQ(prevTrellis)
			}
			if err != nil {
				return nil, fmt.Errorf("SILK encoding failed: %w", err)
			}
			if silent {
				// The active packet's pending LBRR has now been written into this
				// stream. Do not retain redundancy generated while advancing the
				// silent carrier: later groups and packets must not advertise that
				// inactive audio as a new recovery frame.
				e.silkEncoder.DiscardPendingLBRR()
			}
		}
		if !frameRedundancy && e.shouldPadSILKStream(silkPCM) {
			targetBytes := e.silkStreamTargetBytes(group)
			if len(stream) < targetBytes {
				padded := make([]byte, targetBytes)
				copy(padded, stream)
				stream = padded
			}
		}
		streams = append(streams, stream)
		if encodeSILK {
			rangeFinal ^= e.silkEncoder.LastFinalRange()
		}
		pos += inputSamples
	}
	payload, code, err := packOpusFramesPadded(streams, true, e.padBytes)
	if err == nil && e.shouldPadSILKPacket(streams, celtToSilk) {
		targetBytes := 1 + e.bitrate*(20*nFrames)/1000/8
		payload, code, err = packOpusFramesToPacketSize(streams, true, targetBytes)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to pack SILK stream: %w", err)
	}
	e.lastFinalRange = rangeFinal
	return append([]byte{toc | byte(code)}, payload...), nil
}

func (e *Encoder) shouldUseConservativeSILKNSQ(groupFrames int) bool {
	return e.sampleRate == 48000 &&
		e.silkSampleRate == 16000 &&
		e.channels == 1 &&
		groupFrames == 1
}

func (e *Encoder) shouldPadSILKStream(pcm []float64) bool {
	if e.rateMode != celt.RateModeCBR {
		return false
	}
	if isSilentPCM(pcm) {
		return false
	}
	if e.channels == 2 && e.silkEncoder != nil && e.silkEncoder.TrellisNSQ() {
		// A flushed SILK range stream cannot be padded by appending payload
		// zeros: libopus then consumes different tail symbols. Stereo trellis
		// uses its natural budget-controlled stream size.
		return false
	}
	return true
}

func (e *Encoder) shouldPadSILKPacket(streams [][]byte, celtToSilk bool) bool {
	if e.rateMode != celt.RateModeCBR || e.padBytes > 0 || celtToSilk {
		return false
	}
	if len(streams) <= 1 {
		return false
	}
	if e.channels != 2 || e.silkEncoder == nil || !e.silkEncoder.TrellisNSQ() {
		return false
	}
	for _, stream := range streams {
		if len(stream) <= 1 {
			return false
		}
	}
	return true
}

func (e *Encoder) silkStreamTargetBytes(nFrames int) int {
	tb := int(float64(e.bitrate) * (0.020 * float64(nFrames)) / 8.0)
	if tb < 2 {
		tb = 2
	}
	if tb > 1275 {
		tb = 1275
	}
	return tb
}

func isSilentPCM(pcm []float64) bool {
	for _, v := range pcm {
		if math.Abs(v) > 1.0/32768.0 {
			return false
		}
	}
	return true
}

func silkPacketGroups(nFrames int) (groupSize int, groups []int, err error) {
	switch nFrames {
	case 1:
		return 1, []int{1}, nil
	case 2:
		return 2, []int{2}, nil
	case 3:
		return 3, []int{3}, nil
	case 4:
		return 2, []int{2, 2}, nil
	case 5:
		return 1, []int{1, 1, 1, 1, 1}, nil
	case 6:
		return 3, []int{3, 3}, nil
	default:
		return 0, nil, fmt.Errorf("invalid SILK frame count %d", nFrames)
	}
}

func silkPacketGroupsForChannels(nFrames, channels int) (groupSize int, groups []int, err error) {
	if channels == 2 {
		switch nFrames {
		case 3, 4, 5, 6:
			groups := make([]int, nFrames)
			for i := range groups {
				groups[i] = 1
			}
			return 1, groups, nil
		}
	}
	return silkPacketGroups(nFrames)
}

func nativeSilkFramingBandwidth(sampleRate int) (int, bool) {
	switch sampleRate {
	case 8000:
		return framing.BandwidthNarrowband, true
	case 12000:
		return framing.BandwidthMediumband, true
	case 16000:
		return framing.BandwidthWideband, true
	default:
		return 0, false
	}
}

func silkEncodeSampleRate(sampleRate int) (int, bool) {
	switch sampleRate {
	case 8000, 12000, 16000:
		return sampleRate, true
	case 24000, 48000:
		return 16000, true
	default:
		return 0, false
	}
}

func silkFramingBWToPublic(bw int) int {
	switch bw {
	case framing.BandwidthNarrowband:
		return BandwidthNarrowband
	case framing.BandwidthMediumband:
		return BandwidthMediumband
	default:
		return BandwidthWideband
	}
}

func bandwidthRank(bw int) int {
	switch bw {
	case BandwidthNarrowband:
		return 0
	case BandwidthMediumband:
		return 1
	case BandwidthWideband:
		return 2
	case BandwidthSuperWideband:
		return 3
	case BandwidthFullband:
		return 4
	default:
		return 4
	}
}

func frameSizeForExpertDuration(duration ExpertFrameDuration, sampleRate, argumentFrameSize int) (int, bool) {
	switch duration {
	case ExpertFrameDurationArgument:
		return argumentFrameSize, true
	case ExpertFrameDuration2_5ms:
		return sampleRate / 400, true
	case ExpertFrameDuration5ms:
		return sampleRate / 200, true
	case ExpertFrameDuration10ms:
		return sampleRate / 100, true
	case ExpertFrameDuration20ms:
		return sampleRate / 50, true
	case ExpertFrameDuration40ms:
		return sampleRate / 25, true
	case ExpertFrameDuration60ms:
		return sampleRate * 3 / 50, true
	case ExpertFrameDuration80ms:
		return sampleRate * 2 / 25, true
	case ExpertFrameDuration100ms:
		return sampleRate / 10, true
	case ExpertFrameDuration120ms:
		return sampleRate * 3 / 25, true
	default:
		return 0, false
	}
}

// selectEncodeFrameSize resolves the duration without changing encoder state.
// In fixed-duration mode, frameSize describes available input rather than the
// duration to encode.
func (e *Encoder) selectEncodeFrameSize(frameSize int) (int, error) {
	selected, ok := frameSizeForExpertDuration(e.expertFrameDuration, e.sampleRate, frameSize)
	if !ok {
		return 0, fmt.Errorf("%w: invalid expert frame duration %d", ErrInvalidState, e.expertFrameDuration)
	}
	if e.expertFrameDuration != ExpertFrameDurationArgument && frameSize < selected {
		return 0, fmt.Errorf("%w: available frameSize %d is shorter than selected frame size %d at %d Hz", ErrBadArg, frameSize, selected, e.sampleRate)
	}
	if _, err := e.validateFrameSize(selected); err != nil {
		return 0, err
	}
	return selected, nil
}

func (e *Encoder) validateFrameSize(frameSize int) (int, error) {
	base := e.frameSize
	if base <= 0 || frameSize <= 0 {
		return 0, fmt.Errorf("%w: frameSize %d at %d Hz", ErrUnsupportedFrameSize, frameSize, e.sampleRate)
	}
	for _, divisor := range []int{8, 4, 2} {
		if frameSize*divisor == base {
			return 1, nil
		}
	}
	if frameSize%base != 0 {
		return 0, fmt.Errorf("%w: frameSize %d is not a valid Opus duration at %d Hz", ErrUnsupportedFrameSize, frameSize, e.sampleRate)
	}
	nFrames := frameSize / base
	if nFrames < 1 || nFrames > 6 {
		return 0, fmt.Errorf("%w: packet duration %d ms exceeds Opus maximum 120 ms", ErrUnsupportedFrameSize, nFrames*20)
	}
	return nFrames, nil
}

func (e *Encoder) selectCELTEncoder(frameSize int) error {
	internalSize := celt.FrameSize20ms
	if frameSize < e.frameSize {
		internalSize = frameSize * SampleRate48kHz / e.sampleRate
	}
	idx := celtEncoderIndex(internalSize)
	if idx < 0 {
		return fmt.Errorf("%w: CELT frameSize %d", ErrUnsupportedFrameSize, frameSize)
	}
	next := e.celtEncoders[idx]
	if next == nil {
		var err error
		next, err = celt.NewEncoder(internalSize, SampleRate48kHz, e.channels, celt.DefaultEncoderConfig())
		if err != nil {
			return fmt.Errorf("failed to create %d-sample CELT encoder: %w", internalSize, err)
		}
		e.celtEncoders[idx] = next
	}
	next.SetBitrate(e.bitrate)
	next.SetComplexity(e.complexity)
	next.SetRateMode(e.rateMode)
	next.SetDTX(e.dtx)
	next.SetPhaseInversionDisabled(e.phaseInversionDisabled)
	next.SetSignalType(e.celtEncoder.SignalTypeHint())
	next.SetEnergyMask(e.surroundEnergyMask)
	if next != e.celtEncoder {
		next.CopyStateFrom(e.celtEncoder)
		e.celtEncoder = next
	}
	e.internalFrameSize = internalSize
	return nil
}

func (e *Encoder) setSurroundEnergyMask(mask []float64) {
	e.surroundEnergyMask = append(e.surroundEnergyMask[:0], mask...)
	for _, enc := range e.celtEncoders {
		if enc != nil {
			enc.SetEnergyMask(e.surroundEnergyMask)
		}
	}
}

func celtEncoderIndex(frameSize int) int {
	switch frameSize {
	case celt.FrameSize2_5ms:
		return 0
	case celt.FrameSize5ms:
		return 1
	case celt.FrameSize10ms:
		return 2
	case celt.FrameSize20ms:
		return 3
	default:
		return -1
	}
}

// encodeOneCELTFrame resamples one 20 ms PCM chunk (if needed) and encodes it
// into a single CELT frame payload (no TOC byte).
func (e *Encoder) encodeOneCELTFrame(pcm []float64) ([]byte, error) {
	celtInput := e.celtInputFrame(pcm)

	compressed, err := e.celtEncoder.Encode(celtInput)
	if err != nil {
		return nil, fmt.Errorf("CELT encoding failed: %w", err)
	}
	return compressed, nil
}

func (e *Encoder) celtInputFrame(pcm []float64) []float64 {
	var celtInput []float64
	if e.inputResampler != nil {
		// Resample from sampleRate to 48kHz.
		resampled := e.inputResampler.Process(pcm)
		// The resampled output should be approximately internalFrameSize *
		// channels samples. Pad or trim to exact size for CELT.
		targetLen := e.internalFrameSize * e.channels
		celtInput = padOrTrim(resampled, targetLen)
	} else {
		celtInput = pcm
	}
	return celtInput
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

// Bitrate returns the configured target bitrate. It returns BitrateAuto or
// BitrateMax when that policy is configured.
func (e *Encoder) Bitrate() int { return e.bitrateSetting }

// EffectiveBitrate returns the numeric bitrate currently applied internally.
// For BitrateAuto and BitrateMax this is updated for each encoded frame size.
func (e *Encoder) EffectiveBitrate() int { return e.bitrate }

// Complexity returns the current complexity setting (0–10).
func (e *Encoder) Complexity() int { return e.complexity }

// VBR reports whether variable bitrate is enabled.
func (e *Encoder) VBR() bool { return e.rateMode != celt.RateModeCBR }

// VBRConstraint reports whether constrained VBR is enabled.
func (e *Encoder) VBRConstraint() bool { return e.rateMode == celt.RateModeCVBR }

// SampleRate returns the encoder input sample rate in Hz.
func (e *Encoder) SampleRate() int { return e.sampleRate }

// Channels returns the encoder input channel count.
func (e *Encoder) Channels() int { return e.channels }

// Lookahead returns the codec lookahead in samples at the encoder input rate.
func (e *Encoder) Lookahead() int {
	// CELT uses a 120-sample overlap at 48 kHz. The public encoder's current
	// paths do not add a separate analysis delay beyond that overlap.
	return e.sampleRate / 400
}

// FinalRange returns the XOR of the entropy coder final ranges for the most
// recently encoded packet's constituent Opus frames.
func (e *Encoder) FinalRange() uint32 { return e.lastFinalRange }

// InDTX reports whether the most recently encoded packet used the encoder's
// DTX silence path.
func (e *Encoder) InDTX() bool { return e.inDTX }

// Application returns the current application mode.
func (e *Encoder) Application() Application { return e.application }

// SetExpertFrameDuration selects a fixed packet duration. Argument restores
// the default behavior where each Encode call's frameSize selects the duration.
func (e *Encoder) SetExpertFrameDuration(duration ExpertFrameDuration) error {
	if _, ok := frameSizeForExpertDuration(duration, e.sampleRate, e.frameSize); !ok {
		return fmt.Errorf("%w: invalid expert frame duration %d", ErrBadArg, duration)
	}
	e.expertFrameDuration = duration
	if e.forcedMono != nil {
		e.forcedMono.expertFrameDuration = duration
	}
	return nil
}

// ExpertFrameDuration returns the configured packet-duration selection.
func (e *Encoder) ExpertFrameDuration() ExpertFrameDuration {
	return e.expertFrameDuration
}

// SetForceChannels controls the channel count written to the Opus stream.
// ChannelsAuto uses the constructor channel count. A stereo encoder may be
// forced to mono; forcing stereo from a mono input is invalid.
func (e *Encoder) SetForceChannels(channels int) error {
	if channels != ChannelsAuto && channels != ChannelsMono && channels != ChannelsStereo {
		return fmt.Errorf("%w: invalid forced channel count %d", ErrBadArg, channels)
	}
	if channels == ChannelsStereo && e.channels != ChannelsStereo {
		return fmt.Errorf("%w: cannot force stereo from mono input", ErrBadArg)
	}
	e.forceChannels = channels
	return nil
}

// ForceChannels returns the configured forced stream channel count.
func (e *Encoder) ForceChannels() int { return e.forceChannels }

// SetLSBDepth sets the retained input precision hint in bits per sample. The
// current encoder exposes this for CTL parity but does not use it in codec
// decisions.
func (e *Encoder) SetLSBDepth(depth int) error {
	if depth < LSBDepthMin || depth > LSBDepthMax {
		return fmt.Errorf("%w: invalid LSB depth %d", ErrBadArg, depth)
	}
	e.lsbDepth = depth
	return nil
}

// LSBDepth returns the configured input precision hint.
func (e *Encoder) LSBDepth() int { return e.lsbDepth }

// SetPredictionDisabled disables predictive SILK/hybrid mode routing. CELT
// remains available for all supported frame durations.
func (e *Encoder) SetPredictionDisabled(disabled bool) {
	e.predictionDisabled = disabled
}

// PredictionDisabled reports whether predictive mode routing is disabled.
func (e *Encoder) PredictionDisabled() bool { return e.predictionDisabled }

// SetPhaseInversionDisabled disables CELT intensity-stereo phase inversion.
// This is intended for compatibility with downmixing pipelines; disabling it
// is not compliant with the Opus specification.
func (e *Encoder) SetPhaseInversionDisabled(disabled bool) {
	e.phaseInversionDisabled = disabled
	for _, enc := range e.celtEncoders {
		if enc != nil {
			enc.SetPhaseInversionDisabled(disabled)
		}
	}
	if e.redundancyCelt != nil {
		e.redundancyCelt.SetPhaseInversionDisabled(disabled)
	}
}

// PhaseInversionDisabled reports the encoder phase-inversion setting.
func (e *Encoder) PhaseInversionDisabled() bool {
	return e.phaseInversionDisabled
}

func (e *Encoder) monoEncoder() (*Encoder, error) {
	if e.forcedMono == nil {
		enc, err := NewEncoder(e.sampleRate, ChannelsMono, e.application)
		if err != nil {
			return nil, err
		}
		e.forcedMono = enc
	}
	m := e.forcedMono
	if err := m.SetBitrate(e.bitrateSetting); err != nil {
		return nil, err
	}
	if err := m.SetComplexity(e.complexity); err != nil {
		return nil, err
	}
	if e.rateMode == celt.RateModeCBR {
		m.SetVBR(false)
	} else {
		m.SetVBR(true)
		m.SetVBRConstraint(e.rateMode == celt.RateModeCVBR)
	}
	if err := m.SetApplication(e.application); err != nil {
		return nil, err
	}
	m.SetSignalType(e.SignalType())
	if err := m.SetMaxBandwidth(e.maxBandwidth); err != nil {
		return nil, err
	}
	if err := m.SetBandwidth(e.forcedBandwidth); err != nil {
		return nil, err
	}
	if err := m.SetExpertFrameDuration(e.expertFrameDuration); err != nil {
		return nil, err
	}
	m.SetDTX(e.dtx)
	m.SetInbandFEC(e.useInbandFEC)
	m.SetPacketLossPerc(e.packetLossPerc)
	m.SetPacketPadding(e.padBytes)
	_ = m.SetLSBDepth(e.lsbDepth)
	m.SetPredictionDisabled(e.predictionDisabled)
	m.SetPhaseInversionDisabled(e.phaseInversionDisabled)
	return m, nil
}

// SetBitrate sets the target bitrate in bits per second. It accepts numeric
// rates from 6000 through 510000, BitrateAuto, or BitrateMax.
func (e *Encoder) SetBitrate(bitrate int) error {
	if bitrate != BitrateAuto && bitrate != BitrateMax && (bitrate < 6000 || bitrate > 510000) {
		return fmt.Errorf("%w: invalid bitrate %d (must be between 6000 and 510000)", ErrBadArg, bitrate)
	}
	e.bitrateSetting = bitrate
	return e.applyBitrateSetting(e.frameSize)
}

func (e *Encoder) applyBitrateSetting(frameSize int) error {
	bitrate := e.bitrateSetting
	switch bitrate {
	case BitrateAuto:
		// libopus user_bitrate_to_bitrate(): framing overhead plus one bit per
		// input sample and channel.
		bitrate = 60*e.sampleRate/frameSize + e.sampleRate*e.channels
	case BitrateMax:
		bitrate = MaxFrameBytes * 8 * e.sampleRate / frameSize
		if bitrate > 1500000 {
			bitrate = 1500000
		}
	}
	e.bitrate = bitrate
	e.celtEncoder.SetBitrate(e.bitrate)
	if e.silkEncoder != nil {
		silkBitrate := e.bitrate
		if silkBitrate > 40000 {
			silkBitrate = 40000
		}
		if err := e.silkEncoder.SetBitrate(silkBitrate); err != nil {
			return err
		}
	}
	return nil
}

// SetComplexity sets the computational complexity (0-10)
// Higher values use more CPU but may provide better quality
func (e *Encoder) SetComplexity(complexity int) error {
	if complexity < 0 || complexity > 10 {
		return fmt.Errorf("%w: invalid complexity %d (must be 0-10)", ErrBadArg, complexity)
	}
	e.complexity = complexity
	e.celtEncoder.SetComplexity(complexity)
	if e.silkEncoder != nil {
		if err := e.silkEncoder.SetComplexity(complexity); err != nil {
			return err
		}
	}
	return nil
}

// SetVBR enables or disables variable bitrate mode.
// When enabled, this sets constrained VBR (CVBR), which is the libopus default:
// the encoder produces variable-size packets but keeps the average bitrate
// close to the target. Use SetVBRConstraint(false) for unconstrained VBR.
func (e *Encoder) SetVBR(vbr bool) {
	if vbr {
		e.rateMode = celt.RateModeCVBR
	} else {
		e.rateMode = celt.RateModeCBR
	}
	e.celtEncoder.SetRateMode(e.rateMode)
	e.syncSILKRateMode()
}

// SetVBRConstraint controls the VBR constraint. When true (default), CVBR is
// used; when false, unconstrained VBR is used. Has no effect if VBR is disabled.
func (e *Encoder) SetVBRConstraint(constrained bool) {
	if e.rateMode == celt.RateModeCBR {
		return // VBR not enabled, nothing to do
	}
	if constrained {
		e.rateMode = celt.RateModeCVBR
	} else {
		e.rateMode = celt.RateModeVBR
	}
	e.celtEncoder.SetRateMode(e.rateMode)
	e.syncSILKRateMode()
}

func (e *Encoder) syncSILKRateMode() {
	if e.silkEncoder == nil {
		return
	}
	switch e.rateMode {
	case celt.RateModeVBR:
		e.silkEncoder.SetRateMode(silk.RateModeVBR)
	case celt.RateModeCVBR:
		e.silkEncoder.SetRateMode(silk.RateModeCVBR)
	default:
		e.silkEncoder.SetRateMode(silk.RateModeCBR)
	}
}

// SetPacketPadding sets the number of code-3 padding-data bytes appended to each
// emitted packet (RFC 6716 §3.2.5). When n > 0, every packet is encoded as a
// code-3 packet with the padding flag set and n zero bytes appended at the end;
// the padding does not affect the decoded audio (the decoder strips it). This is
// useful for increasing or obscuring the payload length; because the encoded
// audio size may vary, a fixed n does not guarantee a fixed total packet size.
// Use PacketPad when an already encoded packet must reach an exact total size.
// n <= 0 disables padding (the default), restoring compact framing selection.
func (e *Encoder) SetPacketPadding(n int) {
	if n < 0 {
		n = 0
	}
	e.padBytes = n
}

// SetDTX enables or disables discontinuous transmission. When enabled, frames
// the encoder detects as silent are emitted as minimal packets (a few bytes)
// instead of being padded to the target size. This reduces bitrate during
// silence. The decoder reconstructs such frames as digital silence. DTX is off
// by default. The reduction is effective in any rate mode; in CBR it overrides
// the fixed-size padding for silent CELT frames, while SILK digital-silence
// frames are kept compact even without DTX.
func (e *Encoder) SetDTX(enabled bool) {
	e.dtx = enabled
	e.celtEncoder.SetDTX(enabled)
}

// DTX reports whether discontinuous transmission is enabled.
func (e *Encoder) DTX() bool { return e.dtx }

// SetInbandFEC enables or disables SILK inband forward error correction (LBRR).
// When enabled together with a non-zero packet-loss percentage, SILK-only
// packets carry a low-bitrate redundant copy of the previous packet's frame(s),
// which a decoder can recover via its decode_fec path after a lost packet.
// FEC applies to SILK-only and hybrid speech paths; it is off by default and has
// no effect on CELT-only packets.
func (e *Encoder) SetInbandFEC(enabled bool) {
	e.useInbandFEC = enabled
	e.syncSILKFEC()
}

// InbandFEC reports whether inband FEC is enabled.
func (e *Encoder) InbandFEC() bool { return e.useInbandFEC }

// SetPacketLossPerc sets the expected packet-loss percentage used to tune FEC
// redundancy (higher loss → smaller, more frequent LBRR frames). Values are
// clamped to 0 through 100. With FEC enabled, zero disables LBRR emission.
func (e *Encoder) SetPacketLossPerc(perc int) {
	if perc < 0 {
		perc = 0
	}
	if perc > 100 {
		perc = 100
	}
	e.packetLossPerc = perc
	e.syncSILKFEC()
}

// PacketLossPerc reports the configured expected packet-loss percentage.
func (e *Encoder) PacketLossPerc() int { return e.packetLossPerc }

// syncSILKFEC pushes the FEC configuration to the SILK encoder. LBRR is gated on
// both the request flag and a non-zero loss estimate, mirroring libopus'
// decide_fec (which never codes redundancy at 0% loss).
func (e *Encoder) syncSILKFEC() {
	if e.silkEncoder == nil {
		return
	}
	e.silkEncoder.SetPacketLossPerc(e.packetLossPerc)
	e.silkEncoder.SetInbandFEC(e.useInbandFEC && e.packetLossPerc > 0)
}

// SetApplication changes the application mode. This re-derives the CELT content
// hint (voice for VOIP, music otherwise), which influences bandwidth selection
// and transient sensitivity; it does not affect already-emitted packets.
// Invalid application values return ErrBadArg and leave the encoder unchanged.
func (e *Encoder) SetApplication(application Application) error {
	if !isValidApplication(application) {
		return fmt.Errorf("%w: unsupported application %d", ErrBadArg, application)
	}
	e.application = application
	e.celtEncoder.SetSignalType(signalTypeForApplication(application))
	return nil
}

// SetSignalType overrides the content hint used by encoder heuristics.
// SignalAuto (the default) re-derives the hint from the current Application
// setting (VOIP → voice, otherwise music). Calling this with SignalVoice or
// SignalMusic pins the hint regardless of the Application value; a subsequent
// SetApplication call will overwrite it again.
func (e *Encoder) SetSignalType(s SignalType) {
	e.celtEncoder.SetSignalType(s)
}

// SignalType reports the current content hint.
func (e *Encoder) SignalType() SignalType {
	return e.celtEncoder.SignalTypeHint()
}

// SetMaxBandwidth caps the automatically selected coded bandwidth. bw must be one
// of the public Bandwidth* constants (Narrowband..Fullband). The encoder never
// exceeds this cap, nor the input sample rate's Nyquist limit. The default is
// BandwidthFullband (no extra cap). Has no effect when an explicit bandwidth is
// forced via SetBandwidth.
func (e *Encoder) SetMaxBandwidth(bw int) error {
	if !isValidBandwidth(bw) {
		return fmt.Errorf("%w: %w: %d", ErrBadArg, ErrUnsupportedBandwidth, bw)
	}
	e.maxBandwidth = bw
	return nil
}

// MaxBandwidth returns the configured automatic bandwidth cap.
func (e *Encoder) MaxBandwidth() int { return e.maxBandwidth }

// SetBandwidth forces a specific coded bandwidth, overriding the automatic
// selection (it is still clamped to the input sample rate's Nyquist limit). Pass
// BandwidthAuto to return to automatic selection (the default). bw must be
// BandwidthAuto or one of the public Bandwidth* constants. CELT has no
// medium-band mode, so BandwidthMediumband is rounded up to BandwidthWideband.
func (e *Encoder) SetBandwidth(bw int) error {
	if bw != BandwidthAuto && !isValidBandwidth(bw) {
		return fmt.Errorf("%w: %w: %d", ErrBadArg, ErrUnsupportedBandwidth, bw)
	}
	e.forcedBandwidth = bw
	return nil
}

// Bandwidth reports the policy-selected bandwidth before PCM content analysis,
// as a public Bandwidth* constant. Automatic encoding may narrow an individual
// packet further; use PacketGetBandwidth on the emitted packet to observe it.
func (e *Encoder) Bandwidth() int {
	if e.shouldEncodeSILKOnly() {
		bw, _ := nativeSilkFramingBandwidth(e.silkSampleRate)
		return silkFramingBWToPublic(bw)
	}
	if e.shouldEncodeHybrid(1) {
		return celtFramingBWToPublic(e.selectHybridBandwidth())
	}
	return celtFramingBWToPublic(e.selectCeltBandwidth())
}

// GetBandwidth is a CTL-style alias for Bandwidth.
func (e *Encoder) GetBandwidth() int { return e.Bandwidth() }

// isValidBandwidth reports whether bw is one of the public Bandwidth* constants.
func isValidBandwidth(bw int) bool {
	switch bw {
	case BandwidthNarrowband, BandwidthMediumband, BandwidthWideband,
		BandwidthSuperWideband, BandwidthFullband:
		return true
	}
	return false
}

// selectCeltBandwidth chooses the coded bandwidth (an internal framing.Bandwidth*
// value: NB/WB/SWB/FB) for the CELT-only path. It starts from the input sample
// rate's Nyquist limit, then applies either the forced bandwidth (clamped to
// Nyquist) or, for automatic selection, the max-bandwidth cap and a coarse
// bitrate-based reduction. The bitrate reduction is application-aware: the VOIP
// (voice) application requires a higher bitrate before widening, since speech
// concentrates its energy at lower frequencies (see bitrateCeltBandwidth).
// Narrower bandwidths avoid spending bits on frequency bands the source rate or
// bitrate cannot meaningfully support.
func (e *Encoder) selectCeltBandwidth() int {
	nyq := nyquistCeltBandwidth(e.sampleRate)
	if e.forcedBandwidth != BandwidthAuto {
		bw := publicToCeltFramingBW(e.forcedBandwidth)
		if bw > nyq {
			bw = nyq
		}
		return bw
	}
	bw := nyq
	if cap := publicToCeltFramingBW(e.maxBandwidth); cap < bw {
		bw = cap
	}
	if br := bitrateCeltBandwidth(e.bitrate, e.application, e.celtEncoder.SignalTypeHint()); br < bw {
		bw = br
	}
	return bw
}

// nyquistCeltBandwidth returns the widest CELT bandwidth supported by an input
// sample rate's Nyquist limit. CELT has no medium-band mode, so 12 kHz input
// (6 kHz Nyquist) maps to wideband rather than dropping the 4–6 kHz range.
func nyquistCeltBandwidth(sampleRate int) int {
	switch sampleRate {
	case 8000:
		return framing.BandwidthNarrowband
	case 12000, 16000:
		return framing.BandwidthWideband
	case 24000:
		return framing.BandwidthSuperwideband
	default: // 48000
		return framing.BandwidthFullband
	}
}

func nyquistClampFramingBW(bw, sampleRate int) int {
	nyq := nyquistCeltBandwidth(sampleRate)
	if bw > nyq {
		return nyq
	}
	return bw
}

// bitrateCeltBandwidth returns a coarse bandwidth ceiling for a target bitrate so
// that low bitrates do not waste bits on high-frequency bands. The thresholds are
// heuristic and conservative (the default 64 kbps stays fullband for every
// application); the decoder reconstructs whatever bandwidth is signalled, so these
// only shape quality.
//
// The thresholds are application-aware, mirroring libopus' separate voice and
// music bandwidth thresholds: VOIP (voice) content stays in a narrower band until
// a higher bitrate is available, because speech energy is concentrated at low
// frequencies and the extra bits are better spent on the speech range. The audio
// and restricted-low-delay applications use the (wider) music thresholds.
func bitrateCeltBandwidth(bitrate, application int, signal SignalType) int {
	voice := signal == SignalVoice || (signal == SignalAuto && application == ApplicationVOIP)
	if voice {
		switch {
		case bitrate < 20000:
			return framing.BandwidthNarrowband
		case bitrate < 36000:
			return framing.BandwidthWideband
		case bitrate < 52000:
			return framing.BandwidthSuperwideband
		default:
			return framing.BandwidthFullband
		}
	}
	switch {
	case bitrate < 16000:
		return framing.BandwidthNarrowband
	case bitrate < 28000:
		return framing.BandwidthWideband
	case bitrate < 44000:
		return framing.BandwidthSuperwideband
	default:
		return framing.BandwidthFullband
	}
}

// publicToCeltFramingBW maps a public Bandwidth* constant to the internal framing
// bandwidth used for CELT. Medium-band is rounded up to wideband (CELT has no MB).
func publicToCeltFramingBW(pub int) int {
	switch pub {
	case BandwidthNarrowband:
		return framing.BandwidthNarrowband
	case BandwidthMediumband, BandwidthWideband:
		return framing.BandwidthWideband
	case BandwidthSuperWideband:
		return framing.BandwidthSuperwideband
	default: // BandwidthFullband
		return framing.BandwidthFullband
	}
}

// celtFramingBWToPublic is the inverse of publicToCeltFramingBW for the framing
// bandwidths CELT uses (NB/WB/SWB/FB).
func celtFramingBWToPublic(bw int) int {
	switch bw {
	case framing.BandwidthNarrowband:
		return BandwidthNarrowband
	case framing.BandwidthWideband:
		return BandwidthWideband
	case framing.BandwidthSuperwideband:
		return BandwidthSuperWideband
	default:
		return BandwidthFullband
	}
}

// celtEndBandForFramingBW maps an internal framing bandwidth to the CELT "end"
// band count the encoder and decoder must agree on for that bandwidth.
func celtEndBandForFramingBW(bw int) int {
	switch bw {
	case framing.BandwidthNarrowband:
		return 13
	case framing.BandwidthWideband:
		return 17
	case framing.BandwidthSuperwideband:
		return 19
	default: // fullband
		return 21
	}
}

// Reset clears codec history and last-packet observations while retaining
// encoder configuration such as bitrate, application, and controls.
func (e *Encoder) Reset() error {
	// Preserve the configured content hint. The active encoder may be a
	// short-frame instance carrying a SetSignalType/SetApplication update that the
	// 20 ms encoder never saw; reapply it to every encoder below so switching back
	// to celtEncoders[3] does not revert SignalType() to a stale hint.
	signalHint := e.celtEncoder.SignalTypeHint()
	for _, enc := range e.celtEncoders {
		if enc != nil {
			enc.Reset()
		}
	}
	if e.silkEncoder != nil {
		e.silkEncoder.Reset()
	}
	if e.inputResampler != nil {
		e.inputResampler.Reset()
	}
	if e.silkResampler != nil {
		e.silkResampler.Reset()
	}
	e.lastDetectedBW = -1
	e.prevMode = -1
	e.lastFinalRange = 0
	e.inDTX = false
	if e.forcedMono != nil {
		if err := e.forcedMono.Reset(); err != nil {
			return err
		}
	}
	if e.redundancyCelt != nil {
		e.redundancyCelt.Reset()
	}
	e.celtEncoder = e.celtEncoders[3]
	e.internalFrameSize = celt.FrameSize20ms
	for _, enc := range e.celtEncoders {
		if enc != nil {
			enc.SetBitrate(e.bitrate)
			enc.SetComplexity(e.complexity)
			enc.SetRateMode(e.rateMode)
			enc.SetDTX(e.dtx)
			enc.SetPhaseInversionDisabled(e.phaseInversionDisabled)
			enc.SetSignalType(signalHint)
		}
	}
	return nil
}

// silkRateInfo holds a SILK decoder and its associated resampler for one SILK sample rate.
type silkRateInfo struct {
	dec       *silk.Decoder
	resampler *resampler.Resampler // silkRate -> outputRate (nil if same rate); stereo fallback

	// silkResampler is the bit-exact libopus SILK resampler used for the mono
	// path. sMid carries the 1-sample delay libopus applies via sStereo.sMid.
	silkResampler *silk.Resampler
	sMid          int16

	// silkResamplerL/R are bit-exact libopus SILK resamplers for the stereo
	// path. libopus resamples each L/R channel separately at the internal rate
	// after silk_stereo_MS_to_LR, with no sMid-style 1-sample delay (the MS->LR
	// alignment already accounts for it). Used only for the stereo (ci=1) info.
	silkResamplerL *silk.Resampler
	silkResamplerR *silk.Resampler
}

// Decoder represents the state of one Opus stream decoder.
//
// A Decoder is stateful, must not be copied after first use, and is not safe
// for concurrent use. Packets for a logical stream must be supplied in decode
// order, and calls to Decode, DecodePLC, DecodeFEC, getters, SetGain, and Reset
// on the same instance must be serialized by the caller. Separate Decoder
// instances may be used concurrently.
//
// Decode methods borrow packet and destination slices only for the duration of
// the call. Slices returned by DecodeFloat and DecodeFloat32 are owned by the
// caller.
type Decoder struct {
	sampleRate int
	channels   int

	// CELT decoders indexed by [bwIdx 0-3][lmIdx 0-3][chIdx 0-1].
	// bwIdx: 0=NB(13bands), 1=WB(17bands), 2=SWB(19bands), 3=FB(21bands)
	// lmIdx: 0=2.5ms, 1=5ms, 2=10ms, 3=20ms
	// chIdx: 0=mono, 1=stereo
	// CELT always runs at 48kHz internally; bandwidth only limits numBands.
	celtDecoders [4][4][2]*celt.Decoder
	lastCeltDec  *celt.Decoder
	prevMode     int
	// prevRedundancy records that the previous hybrid frame ended with a
	// trailing CELT redundancy frame. libopus keeps this separately from
	// prev_mode so the next lost frame uses CELT-only PLC while normal packet
	// transition handling still sees the original hybrid framing mode.
	prevRedundancy bool

	// SILK decoders indexed by [rateIdx 0-2][chIdx 0=mono,1=stereo].
	// rateIdx: 0=8kHz, 1=12kHz, 2=16kHz
	// libopus keeps ONE SILK decoder per channel whose synthesis state carries
	// across packets regardless of Opus frame duration; the per-packet frame
	// geometry is switched with (*silk.Decoder).SetFrameMs. Keeping separate
	// 10ms/20ms instances would discontinue the synthesis state at every
	// frame-size switch (e.g. NB config 1 -> config 0), causing voiced bursts to
	// diverge for a few frames after each switch.
	silkDecoders [3][2]*silkRateInfo

	// Bit-exact SILK resampler state at the physical-channel level, mirroring
	// libopus channel_state[n].resampler_state. These persist across packets and
	// across the mono/stereo internal split (unlike silkRateInfo, which is keyed
	// per packet-channel-count). A channel's resampler is reset when its SILK
	// internal rate changes (libopus silk_decoder_set_fs). silkSMid is the
	// sStereo.sMid 1-sample carry used by the mono internal path.
	silkRS             [2]*silk.Resampler
	silkRSInKHz        [2]int // current internal rate (kHz) per channel; 0 = uninitialized
	silkSMid           int16
	prevSilkInternalCh int // previous packet's SILK internal channel count (0 = none yet)

	// Resampler for non-48kHz CELT output rates
	celtResampler *resampler.Resampler // 48kHz -> outRate

	frameSize              int // frame size in samples at sampleRate
	internalFrameSize      int // always 960 (20ms at 48kHz)
	lastPacketDuration     int // samples per channel decoded by the last packet
	lastPacketConfig       int // TOC config of the last successfully decoded packet
	lastPacketChannels     int // coded channels of the last successfully decoded packet
	lastFinalRange         uint32
	lastPitch              int
	gainQ8                 int
	phaseInversionDisabled bool
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

// NewDecoder creates a stateful Opus decoder.
//
// sampleRate is the requested output rate and must be 8000, 12000, 16000,
// 24000, or 48000 Hz. channels must be one or two. Invalid arguments return an
// error wrapping ErrBadArg.
func NewDecoder(sampleRate, channels int) (*Decoder, error) {
	// Validate parameters
	if !isValidOpusRate(sampleRate) {
		return nil, fmt.Errorf("%w: %w: %d", ErrBadArg, ErrUnsupportedSampleRate, sampleRate)
	}

	if channels != 1 && channels != 2 {
		return nil, fmt.Errorf("%w: %w: %d", ErrBadArg, ErrUnsupportedChannels, channels)
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
		prevMode:          -1,
		lastPacketConfig:  -1,
	}

	// Create CELT decoders for all 4 bandwidths × 4 frame sizes × 2 channel counts.
	// CELT always runs at 48kHz internally; bandwidth only controls numBands.
	for bw := 0; bw < 4; bw++ {
		numBands := celtBWNumBands[bw]
		for lm := 0; lm < 4; lm++ {
			fs := celtLMFrameSize[lm]
			for ch := 1; ch <= 2; ch++ {
				d, err := celt.NewDecoderEx(fs, 48000, numBands, ch)
				if err != nil {
					return nil, fmt.Errorf("failed to create CELT decoder (bw=%d lm=%d ch=%d): %w", bw, lm, ch, err)
				}
				dec.celtDecoders[bw][lm][ch-1] = d
			}
		}
	}

	// Pre-create ONE SILK decoder per rate × channel config. The Opus frame
	// duration (10ms/20ms) is switched per packet via SetFrameMs so that the
	// SILK synthesis state stays continuous across frame-size changes, matching
	// libopus (which uses a single decoder per channel).
	silkRates := []int{8000, 12000, 16000}
	for ri, silkRate := range silkRates {
		for ch := 1; ch <= 2; ch++ {
			sd, err := silk.NewDecoderWithFrameMs(silkRate, ch, 20)
			if err != nil {
				return nil, fmt.Errorf("failed to create SILK decoder (rate=%d ch=%d): %w", silkRate, ch, err)
			}
			var rs *resampler.Resampler
			if silkRate != sampleRate {
				rs, err = resampler.NewResampler(silkRate, sampleRate, ch, resampler.QualityDefault)
				if err != nil {
					return nil, fmt.Errorf("failed to create SILK resampler (%d->%d, ch=%d): %w", silkRate, sampleRate, ch, err)
				}
			}
			// Bit-exact libopus SILK resampler (used for the mono path).
			silkRs, err := silk.NewResampler(silkRate, sampleRate)
			if err != nil {
				return nil, fmt.Errorf("failed to create bit-exact SILK resampler (%d->%d): %w", silkRate, sampleRate, err)
			}
			info := &silkRateInfo{dec: sd, resampler: rs, silkResampler: silkRs}
			if ch == 2 {
				// Per-channel bit-exact resamplers for the stereo L/R path.
				lRs, lErr := silk.NewResampler(silkRate, sampleRate)
				if lErr != nil {
					return nil, fmt.Errorf("failed to create stereo bit-exact SILK resampler L (%d->%d): %w", silkRate, sampleRate, lErr)
				}
				rRs, rErr := silk.NewResampler(silkRate, sampleRate)
				if rErr != nil {
					return nil, fmt.Errorf("failed to create stereo bit-exact SILK resampler R (%d->%d): %w", silkRate, sampleRate, rErr)
				}
				info.silkResamplerL = lRs
				info.silkResamplerR = rRs
			}
			dec.silkDecoders[ri][ch-1] = info
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

// Decode decodes an Opus packet into interleaved int16 PCM and returns the
// number of samples per channel. pcm must have room for the packet duration
// times Channels() values; a short buffer returns ErrBufferTooSmall.
func (d *Decoder) Decode(data []byte, pcm []int16) (int, error) {
	required, err := d.packetOutputSamples(data)
	if err != nil {
		return 0, err
	}
	if len(pcm) < required {
		return 0, fmt.Errorf("%w: got %d samples, need %d", ErrBufferTooSmall, len(pcm), required)
	}

	floatPCM, err := d.DecodeFloat(data)
	if err != nil {
		return 0, err
	}

	n := len(floatPCM)

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

// Decode24 decodes an Opus packet to interleaved signed 24-bit PCM stored in
// int32 values. Output is saturated to [-8388608, 8388607]. pcm must have room
// for the packet duration times Channels() values; the return value is samples
// per channel, and a short buffer returns ErrBufferTooSmall.
func (d *Decoder) Decode24(data []byte, pcm []int32) (int, error) {
	required, err := d.packetOutputSamples(data)
	if err != nil {
		return 0, err
	}
	if len(pcm) < required {
		return 0, fmt.Errorf("%w: got %d samples, need %d", ErrBufferTooSmall, len(pcm), required)
	}

	floatPCM, err := d.DecodeFloat(data)
	if err != nil {
		return 0, err
	}
	for i, sample := range floatPCM {
		scaled := sample * 8388608.0
		if scaled > 8388607.0 {
			scaled = 8388607.0
		} else if scaled < -8388608.0 {
			scaled = -8388608.0
		}
		pcm[i] = int32(math.Round(scaled))
	}
	return len(floatPCM) / d.channels, nil
}

// packetOutputSamples returns the interleaved output sample count without
// mutating decoder state.
func (d *Decoder) packetOutputSamples(data []byte) (int, error) {
	duration, err := PacketGetNumSamples(data, d.sampleRate)
	if err != nil {
		return 0, err
	}
	return duration * d.channels, nil
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

// encodeOpusFrameLength encodes a single frame length using the RFC 6716 §3.2.1
// 1-or-2-byte scheme. It is the exact inverse of parseOpusFrameLength.
//
// Values < 252 use one byte; values in [252, 1275] use two bytes b0,b1 where
// length = b1*4 + b0 and b0 ∈ [252, 255].
func encodeOpusFrameLength(n int) ([]byte, error) {
	const maxLen = 255*4 + 255 // 1275
	if n < 0 || n > maxLen {
		return nil, fmt.Errorf("frame length %d out of range [0,%d]", n, maxLen)
	}
	if n < 252 {
		return []byte{byte(n)}, nil
	}
	b0 := 252 + ((n - 252) & 3)
	b1 := (n - b0) / 4
	return []byte{byte(b0), byte(b1)}, nil
}

// packOpusFrames builds the frame portion of an Opus packet (everything after
// the TOC byte) from the given per-frame payloads, choosing the most compact
// RFC 6716 §3.2 count code. It returns the payload bytes and the count code to
// OR into the TOC. It is the inverse of splitOpusFrames.
//
// vbr selects whether variable-size codes are allowed: when false (CBR) and all
// frames are equal length, the compact equal-size codes (1 / 3-CBR) are used;
// otherwise explicit length prefixes (codes 2 / 3-VBR) are emitted. Frames of
// unequal length always force a VBR code regardless of the hint.
func packOpusFrames(frames [][]byte, vbr bool) ([]byte, int, error) {
	n := len(frames)
	if n == 0 {
		return nil, 0, fmt.Errorf("no frames to pack")
	}
	if n > 48 {
		return nil, 0, fmt.Errorf("too many frames: %d (max 48)", n)
	}

	if n == 1 {
		return frames[0], 0, nil
	}

	allEqual := true
	for i := 1; i < n; i++ {
		if len(frames[i]) != len(frames[0]) {
			allEqual = false
			break
		}
	}

	if n == 2 {
		if !vbr && allEqual {
			// Code 1: two equal-size frames, no length prefix.
			out := make([]byte, 0, len(frames[0])+len(frames[1]))
			out = append(out, frames[0]...)
			out = append(out, frames[1]...)
			return out, 1, nil
		}
		// Code 2: explicit length of the first frame; second is the remainder.
		lp, err := encodeOpusFrameLength(len(frames[0]))
		if err != nil {
			return nil, 0, err
		}
		out := make([]byte, 0, len(lp)+len(frames[0])+len(frames[1]))
		out = append(out, lp...)
		out = append(out, frames[0]...)
		out = append(out, frames[1]...)
		return out, 2, nil
	}

	// n >= 3: code 3.
	if !vbr && allEqual {
		// Code 3 CBR: frame-count byte (vbr=0, padding=0) then equal frames.
		out := make([]byte, 0, 1+n*len(frames[0]))
		out = append(out, byte(n)) // lower 6 bits = count
		for _, f := range frames {
			out = append(out, f...)
		}
		return out, 3, nil
	}

	// Code 3 VBR: frame-count byte with VBR flag, then the first n-1 frame
	// lengths, then all frame payloads.
	out := make([]byte, 0, 1+2*(n-1))
	out = append(out, 0x80|byte(n)) // VBR flag | count
	for i := 0; i < n-1; i++ {
		lp, err := encodeOpusFrameLength(len(frames[i]))
		if err != nil {
			return nil, 0, err
		}
		out = append(out, lp...)
	}
	for _, f := range frames {
		out = append(out, f...)
	}
	return out, 3, nil
}

// encodePaddingCount encodes a padding-data-byte count as the run of count bytes
// that prefixes a code-3 padding payload (RFC 6716 §3.2.5). It is the inverse of
// the run the decoder consumes in splitOpusFrames: each 0xFF byte contributes 254
// and continues; the first byte < 255 contributes its value (0..254) and ends the
// run. padBytes is the number of trailing padding-data bytes (it does NOT count
// the run bytes themselves). padBytes must be >= 0.
func encodePaddingCount(padBytes int) []byte {
	var run []byte
	for padBytes > 254 {
		run = append(run, 255)
		padBytes -= 254
	}
	run = append(run, byte(padBytes))
	return run
}

// packOpusFramesPadded is packOpusFrames with optional code-3 padding. When
// padBytes <= 0 it is exactly packOpusFrames. When padBytes > 0 it forces a
// code-3 packet (the only count code with a padding mechanism), sets the padding
// flag (0x40) in the frame-count byte, writes the padding-count run, the frame
// lengths (VBR) or nothing (CBR), all frame payloads, then padBytes zero bytes at
// the very end. The result round-trips through splitOpusFrames, which strips the
// padding-data bytes from the end. A single frame is legal under code 3
// (frameCount=1), so padding is available for 1..48 frames.
func packOpusFramesPadded(frames [][]byte, vbr bool, padBytes int) ([]byte, int, error) {
	if padBytes <= 0 {
		return packOpusFrames(frames, vbr)
	}
	out, err := packOpusFramesCode3(frames, vbr, true, padBytes)
	return out, 3, err
}

// packOpusFramesToPacketSize keeps the entropy-coded frame bytes unchanged and
// uses only RFC 6716 code-3 framing/padding to reach an exact total packet size.
// targetBytes includes the TOC byte, which the caller prepends separately.
func packOpusFramesToPacketSize(frames [][]byte, vbr bool, targetBytes int) ([]byte, int, error) {
	compact, code, err := packOpusFrames(frames, vbr)
	if err != nil {
		return nil, 0, err
	}
	if 1+len(compact) == targetBytes {
		return compact, code, nil
	}
	if 1+len(compact) > targetBytes {
		return compact, code, nil
	}

	code3, err := packOpusFramesCode3(frames, vbr, false, 0)
	if err != nil {
		return nil, 0, err
	}
	if 1+len(code3) == targetBytes {
		return code3, 3, nil
	}

	// A padded code-3 packet adds padBytes data bytes plus the padding-count
	// run. Each continuation byte represents 254 padding bytes, so the run
	// length is max(1, ceil(padBytes/254)). Invert that relationship in O(1)
	// and validate the candidate because sizes immediately after each
	// continuation boundary are not representable exactly.
	requiredPadding := targetBytes - 1 - len(code3)
	if requiredPadding >= 1 {
		padBytes := requiredPadding - (requiredPadding+254)/255
		if padBytes >= 0 && padBytes+len(encodePaddingCount(padBytes)) == requiredPadding {
			padded, err := packOpusFramesCode3(frames, vbr, true, padBytes)
			if err != nil {
				return nil, 0, err
			}
			if 1+len(padded) == targetBytes {
				return padded, 3, nil
			}
		}
	}
	return compact, code, nil
}

func packOpusFramesCode3(frames [][]byte, vbr, padding bool, padBytes int) ([]byte, error) {
	n := len(frames)
	if n == 0 {
		return nil, fmt.Errorf("no frames to pack")
	}
	if n > 48 {
		return nil, fmt.Errorf("too many frames: %d (max 48)", n)
	}
	if padBytes < 0 {
		return nil, fmt.Errorf("negative padding size: %d", padBytes)
	}

	allEqual := true
	for i := 1; i < n; i++ {
		if len(frames[i]) != len(frames[0]) {
			allEqual = false
			break
		}
	}
	// CBR layout is only possible when every frame is the same size; otherwise
	// explicit per-frame lengths (VBR layout) are required.
	useVBR := vbr || !allEqual

	capacity := 1 + 2*n + padBytes
	if padding {
		capacity += len(encodePaddingCount(padBytes))
	}
	out := make([]byte, 0, capacity)
	flags := byte(n)
	if useVBR {
		flags |= 0x80
	}
	if padding {
		flags |= 0x40
	}
	out = append(out, flags)
	if padding {
		out = append(out, encodePaddingCount(padBytes)...)
	}
	if useVBR {
		// First n-1 frame lengths, then all payloads.
		for i := 0; i < n-1; i++ {
			lp, err := encodeOpusFrameLength(len(frames[i]))
			if err != nil {
				return nil, err
			}
			out = append(out, lp...)
		}
	}
	for _, f := range frames {
		out = append(out, f...)
	}
	// Trailing padding-data bytes (zeros), stripped from the end on decode.
	out = append(out, make([]byte, padBytes)...)
	return out, nil
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

		// Skip padding (RFC 6716 §3.2.5): the padding length is a run of count
		// bytes at the front. Each byte of value 255 contributes 254 data bytes
		// and continues; the first byte < 255 contributes its value and ends the
		// run. The padding data bytes themselves are stripped from the END.
		if padding {
			padLen := 0
			for {
				if len(payload) < 1 {
					return nil, fmt.Errorf("code 3: missing padding count")
				}
				p := int(payload[0])
				payload = payload[1:]
				if p == 255 {
					padLen += 254
					continue
				}
				padLen += p
				break
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

func opusFrameCount(payload []byte, countCode int) (int, error) {
	switch countCode {
	case 0:
		return 1, nil
	case 1, 2:
		return 2, nil
	case 3:
		if len(payload) < 1 {
			return 0, fmt.Errorf("code 3: empty payload")
		}
		frameCount := int(payload[0] & 0x3F)
		if frameCount == 0 || frameCount > 48 {
			return 0, fmt.Errorf("code 3: invalid frame count %d", frameCount)
		}
		return frameCount, nil
	default:
		return 0, fmt.Errorf("unknown count code %d", countCode)
	}
}

func packetDurationSamples(config, frameCount, sampleRate int) (int, error) {
	if frameCount < 1 {
		return 0, fmt.Errorf("%w: invalid frame count %d", ErrInvalidPacket, frameCount)
	}
	var perFrame int
	if config < 16 {
		perFrame = sampleRate * silkConfigFrameMs(config) / 1000
	} else {
		perFrame = celtFrameSamples(config, sampleRate)
	}
	total := perFrame * frameCount
	maxDuration := sampleRate * 120 / 1000
	if total > maxDuration {
		return 0, fmt.Errorf("%w: packet duration %d samples exceeds Opus maximum %d", ErrInvalidPacket, total, maxDuration)
	}
	return total, nil
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

// DecodeFloat decodes an Opus packet to caller-owned interleaved float64 PCM.
//
// Samples use the conventional normalized scale where -1 and +1 correspond to
// full-scale input. Positive decoder gain can produce values outside that
// nominal range.
func (d *Decoder) DecodeFloat(data []byte) ([]float64, error) {
	info, err := inspectPacket(data, d.sampleRate)
	if err != nil {
		return nil, err
	}

	toc := data[0]
	config, stereo, countCode := framing.ParseTOC(toc)

	payload := data[1:]
	duration := info.totalSamples

	if config < 16 {
		pktChannels := 1
		if stereo {
			pktChannels = 2
		}
		if config >= 12 {
			// Hybrid mode: SILK low band + CELT high band sharing one stream.
			out, trailingRedundancy, err := d.decodeHybridPacket(payload, countCode, config, pktChannels)
			if err == nil {
				d.lastPacketDuration = duration
				d.lastPacketConfig = config
				d.lastPacketChannels = pktChannels
				d.prevMode = framing.ModeHybrid
				d.prevRedundancy = trailingRedundancy
				d.applyGain(out)
			}
			return out, err
		}
		// SILK-only mode.
		out, err := d.decodeSILKPacket(payload, countCode, config, pktChannels)
		if err == nil {
			d.lastPacketDuration = duration
			d.lastPacketConfig = config
			d.lastPacketChannels = pktChannels
			d.prevMode = framing.ModeSILKOnly
			d.prevRedundancy = false
			d.applyGain(out)
		}
		return out, err
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
	var rangeFinal uint32
	for _, frame := range frames {
		if d.lastCeltDec != nil && d.lastCeltDec != activeCeltDec {
			activeCeltDec.CopyStateFrom(d.lastCeltDec)
		}
		pcm, err := activeCeltDec.Decode(frame)
		if err != nil {
			return nil, fmt.Errorf("CELT decoding failed: %w", err)
		}
		d.lastCeltDec = activeCeltDec
		rangeFinal ^= activeCeltDec.LastFinalRange()
		d.lastPitch = activeCeltDec.Pitch() * d.sampleRate / 48000

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

	d.lastPacketDuration = duration
	d.lastPacketConfig = config
	d.lastPacketChannels = pktChannels
	d.lastFinalRange = rangeFinal
	d.prevMode = framing.ModeCELTOnly
	d.prevRedundancy = false
	d.applyGain(allPCM)
	return allPCM, nil
}

func (d *Decoder) applyGain(pcm []float64) {
	if d.gainQ8 == 0 {
		return
	}
	scale := math.Pow(10, float64(d.gainQ8)/(20*256))
	for i := range pcm {
		pcm[i] *= scale
	}
}

// DecodeFloat32 decodes an Opus packet to caller-owned interleaved float32 PCM.
// Positive decoder gain can produce values outside the nominal [-1, 1] scale.
func (d *Decoder) DecodeFloat32(data []byte) ([]float32, error) {
	pcm, err := d.DecodeFloat(data)
	if err != nil {
		return nil, err
	}
	out := make([]float32, len(pcm))
	for i := range pcm {
		out[i] = float32(pcm[i])
	}
	return out, nil
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

// decodeSILKPacket decodes a SILK or Hybrid-mode packet.
//
// For SILK, each Opus frame payload is one range-coded stream. A 40 ms or
// 60 ms SILK TOC config stores multiple 20 ms SILK subframes inside that one
// stream; Opus count codes store multiple such Opus frame streams.
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

	if d.silkDecoders[ri][ci] == nil {
		return nil, fmt.Errorf("SILK decoder not initialized for rate=%dkHz ch=%d", rateKHz, pktChannels)
	}
	info := d.silkDecoders[ri][ci]
	// Switch the per-packet frame geometry (10ms/20ms) without resetting the
	// synthesis state, so it stays continuous across frame-size changes.
	info.dec.SetFrameMs(frameDurationMs)
	monoPeer := d.silkDecoders[ri][0]
	stereoPeer := d.silkDecoders[ri][1]
	if pktChannels == 2 && monoPeer != nil && monoPeer.dec != nil {
		info.dec.CopyPrimaryStateFrom(monoPeer.dec)
	}

	silkStreams, err := splitOpusFrames(payload, countCode)
	if err != nil {
		return nil, err
	}
	nSilkFramesPerStream := subframesPerOpusFrame

	// Compute expected samples per stream at output rate
	samplesPerStream := (d.sampleRate * frameDurationMs / 1000) * d.channels
	if samplesPerStream < 1 {
		samplesPerStream = (d.sampleRate * frameDurationMs / 1000) * d.channels
	}

	// --- Bit-exact SILK resampler setup (mirrors libopus dec_API.c) ---
	// Ensure each active physical channel has a resampler for the current
	// internal rate; a channel's resampler is reset when its rate changes.
	d.ensureSilkResampler(0, rateKHz)
	if pktChannels == 2 {
		d.ensureSilkResampler(1, rateKHz)
	}
	// Mono->stereo internal transition: seed channel 1's resampler from
	// channel 0 (dec_API.c:217). This runs after ensure (which models the
	// init/set_fs that libopus overwrites with this memcpy).
	if pktChannels == 2 && d.prevSilkInternalCh == 1 && d.silkRS[0] != nil {
		seeded := *d.silkRS[0]
		d.silkRS[1] = &seeded
		d.silkRSInKHz[1] = rateKHz
	}
	// stereo_to_mono: previous packet stereo, now mono, internal rate unchanged.
	// The right output channel of the first SILK frame is resampled through the
	// still-warm channel-1 resampler from the same mono signal (dec_API.c:418-426).
	stereoToMono := pktChannels == 1 && d.prevSilkInternalCh == 2 &&
		d.silkRS[1] != nil && d.silkRSInKHz[1] == rateKHz

	var allPCM []float64
	var rangeFinal uint32
	for si, stream := range silkStreams {
		if len(stream) < 2 {
			pcm, err := info.dec.DecodeMulti(stream, nSilkFramesPerStream)
			if err != nil {
				allPCM = append(allPCM, make([]float64, samplesPerStream)...)
				continue
			}
			pcm = d.resampleSILK(pcm, nSilkFramesPerStream, pktChannels, stereoToMono && si == 0)
			pcm = padOrTrim(pcm, samplesPerStream)
			allPCM = append(allPCM, pcm...)
			rangeFinal ^= info.dec.LastFinalRange()
			continue
		}
		dec := entcode.NewDecoder(stream)
		if dec.Error() != nil {
			allPCM = append(allPCM, make([]float64, samplesPerStream)...)
			continue
		}

		// Decode SILK sub-frames from this stream, retaining the range decoder so
		// a CELT->SILK redundancy marker can be read immediately afterwards.
		pcm, err := info.dec.DecodeMultiWithDecoder(dec, nSilkFramesPerStream)
		if err != nil {
			allPCM = append(allPCM, make([]float64, samplesPerStream)...)
			continue
		}

		redundancyBytes := 0
		celtToSilk := false
		if dec.ECTell()+17 <= len(stream)*8 {
			celtToSilk = dec.DecodeBitLogp(1)
			if celtToSilk {
				redundancyBytes = len(stream) - ((dec.ECTell() + 7) >> 3)
				if redundancyBytes < 2 || len(stream)-redundancyBytes < 0 {
					redundancyBytes = 0
					celtToSilk = false
				} else {
					dec.ShrinkStorage(redundancyBytes)
				}
			}
		}

		// Resample from internal rate to the output rate using the persistent
		// per-channel bit-exact resamplers, producing d.channels-interleaved PCM.
		// stereoToMono applies only to the very first SILK frame after the
		// transition (si == 0; the method gates further on f == 0).
		pcm = d.resampleSILK(pcm, nSilkFramesPerStream, pktChannels, stereoToMono && si == 0)

		// Pad or trim to exact expected length
		pcm = padOrTrim(pcm, samplesPerStream)
		if si == 0 && celtToSilk && redundancyBytes >= 2 {
			if redPCM := d.decodeLeadingRedundancy(stream[len(stream)-redundancyBytes:], pktChannels, 17); redPCM != nil &&
				d.prevMode != framing.ModeSILKOnly {
				d.crossfadeLeadingRedundancy(pcm, redPCM)
			}
		}
		allPCM = append(allPCM, pcm...)
		rangeFinal ^= dec.GetRng()
	}
	d.lastFinalRange = rangeFinal
	d.lastPitch = info.dec.Pitch() * d.sampleRate / (rateKHz * 1000)
	d.prevSilkInternalCh = pktChannels

	if pktChannels == 1 && stereoPeer != nil && stereoPeer.dec != nil {
		stereoPeer.dec.CopyPrimaryStateFrom(info.dec)
	} else if pktChannels == 2 && monoPeer != nil && monoPeer.dec != nil {
		monoPeer.dec.CopyPrimaryStateFrom(info.dec)
	}

	return allPCM, nil
}

// decodeHybridPacket decodes a hybrid-mode packet (config 12-15): a SILK
// wideband (16 kHz internal) low-frequency layer and a CELT high-frequency
// layer (start band 17) that share a single range-coded stream per Opus frame.
// SILK is decoded from the front of the stream; the CELT decoder continues from
// the same decoder. The resampled SILK signal and the CELT high band are summed
// in the time domain (mirrors libopus opus_decode_frame's hybrid path).
// crossfadeRedundancy blends the last 2.5 ms (F2_5 at the output rate) of the
// hybrid output `out` with the second half of the 5 ms redundant CELT frame
// `red`, using window² weights (libopus smooth_fade). Both buffers are
// interleaved at d.channels; `red` holds 2*F2_5 samples per channel.
func (d *Decoder) crossfadeRedundancy(out, red []float64, samplesPerFrame int) {
	ch := d.channels
	f25 := d.sampleRate / 400 // F2_5 at output rate (120 @ 48k)
	frameSamplesPerCh := samplesPerFrame / ch
	if frameSamplesPerCh < f25 || len(red) < 2*f25*ch {
		return
	}
	win := celt.OverlapWindow48()
	inc := 48000 / d.sampleRate
	if inc < 1 {
		inc = 1
	}
	base := (frameSamplesPerCh - f25) * ch // first interleaved sample of the tail
	for i := 0; i < f25; i++ {
		wi := win[i*inc]
		w := wi * wi
		for c := 0; c < ch; c++ {
			o := base + i*ch + c
			r := (f25+i)*ch + c
			out[o] = w*red[r] + (1.0-w)*out[o]
		}
	}
}

// crossfadeLeadingRedundancy replaces the first 2.5 ms with the first half of
// the redundant CELT frame, then fades its second half into the new SILK/hybrid
// output over the next 2.5 ms (libopus smooth_fade, window squared).
func (d *Decoder) crossfadeLeadingRedundancy(out, red []float64) {
	ch := d.channels
	f25 := d.sampleRate / 400
	if len(out) < 2*f25*ch || len(red) < 2*f25*ch {
		return
	}
	copy(out[:f25*ch], red[:f25*ch])
	win := celt.OverlapWindow48()
	inc := 48000 / d.sampleRate
	if inc < 1 {
		inc = 1
	}
	for i := 0; i < f25; i++ {
		w := win[i*inc]
		w *= w
		for c := 0; c < ch; c++ {
			o := (f25+i)*ch + c
			out[o] = (1.0-w)*red[o] + w*out[o]
		}
	}
}

func (d *Decoder) decodeLeadingRedundancy(frame []byte, pktChannels, endBand int) []float64 {
	if len(frame) < 2 {
		return nil
	}
	actualCh := pktChannels
	redDec, err := celt.NewDecoderEx(celt.FrameSize5ms, 48000, endBand, actualCh)
	if err != nil {
		return nil
	}
	redDec.SetPhaseInversionDisabled(d.phaseInversionDisabled)
	if d.lastCeltDec != nil {
		redDec.CopyStateFrom(d.lastCeltDec)
	}
	redPCM, err := redDec.Decode(frame)
	if err != nil {
		return nil
	}
	if d.celtResampler != nil {
		redPCM = d.celtResampler.Process(redPCM)
	}
	redPCM = adjustChannels(redPCM, actualCh, d.channels)
	redPCM = padOrTrim(redPCM, (d.sampleRate/200)*d.channels)
	d.lastCeltDec = redDec
	return redPCM
}

func (d *Decoder) decodeHybridPacket(payload []byte, countCode, config, pktChannels int) ([]float64, bool, error) {
	const rateKHz = 16 // hybrid SILK layer is always wideband
	ri := silkRateIdx(rateKHz)
	ci := pktChannels - 1
	frameDurationMs := silkConfigFrameMs(config) // 10 (even config) or 20 (odd)

	// CELT band range: hybrid SWB (config 12,13) -> end 19; FB (14,15) -> end 21.
	const celtStart = 17
	celtEnd := 21
	if config < 14 {
		celtEnd = 19
	}
	// CELT high-band decoder: fullband mode (21 bands), LM by frame duration.
	celtLMIdx := 3 // 20ms -> 960 samples
	if frameDurationMs == 10 {
		celtLMIdx = 2 // 10ms -> 480 samples
	}
	const celtBWIdx = 3 // fullband (21 bands)
	celtActualCh := pktChannels
	celtDec := d.celtDecoders[celtBWIdx][celtLMIdx][ci]
	if celtDec == nil {
		celtDec = d.celtDecoders[celtBWIdx][celtLMIdx][0]
		celtActualCh = 1
	}

	if d.silkDecoders[ri][ci] == nil {
		return nil, false, fmt.Errorf("SILK decoder not initialized for hybrid rate=%dkHz ch=%d", rateKHz, pktChannels)
	}
	info := d.silkDecoders[ri][ci]
	info.dec.SetFrameMs(frameDurationMs)
	monoPeer := d.silkDecoders[ri][0]
	stereoPeer := d.silkDecoders[ri][1]
	if pktChannels == 2 && monoPeer != nil && monoPeer.dec != nil {
		info.dec.CopyPrimaryStateFrom(monoPeer.dec)
	}

	// One SILK frame per Opus frame in hybrid mode.
	nSilkFrames := silkSubframesPerOpusFrame(config)
	silkStreams, err := splitOpusFrames(payload, countCode)
	if err != nil {
		silkStreams = [][]byte{payload}
	}

	// Bit-exact SILK resampler setup, identical to the SILK-only path so that
	// per-channel resampler state stays continuous across SILK<->hybrid packets.
	d.ensureSilkResampler(0, rateKHz)
	if pktChannels == 2 {
		d.ensureSilkResampler(1, rateKHz)
	}
	if pktChannels == 2 && d.prevSilkInternalCh == 1 && d.silkRS[0] != nil {
		seeded := *d.silkRS[0]
		d.silkRS[1] = &seeded
		d.silkRSInKHz[1] = rateKHz
	}
	stereoToMono := pktChannels == 1 && d.prevSilkInternalCh == 2 &&
		d.silkRS[1] != nil && d.silkRSInKHz[1] == rateKHz

	samplesPerFrame := (d.sampleRate * frameDurationMs / 1000) * d.channels

	var allPCM []float64
	var rangeFinal uint32
	trailingRedundancy := false
	for si, stream := range silkStreams {
		// libopus replaces prev_redundancy for every constituent frame. An
		// unreadable final frame must therefore clear an earlier frame's value.
		trailingRedundancy = false
		// One shared range decoder per Opus frame: SILK reads first, CELT after.
		dec := entcode.NewDecoder(stream)
		if dec.Error() != nil {
			allPCM = append(allPCM, make([]float64, samplesPerFrame)...)
			continue
		}

		// SILK low-band layer (front of the stream), at the 16 kHz internal rate.
		silkPCM, serr := info.dec.DecodeMultiWithDecoder(dec, nSilkFrames)
		if serr != nil {
			silkPCM = make([]float64, info.dec.FrameSize()*pktChannels*nSilkFrames)
		}
		silkOut := d.resampleSILK(silkPCM, nSilkFrames, pktChannels, stereoToMono && si == 0)
		silkOut = padOrTrim(silkOut, samplesPerFrame)

		// Hybrid redundancy flag (logp 12), decoded between SILK and CELT in
		// libopus opus_decode_frame. When set, the packet carries a trailing 5 ms
		// redundant CELT frame (full band) used to smooth SILK<->CELT transitions.
		// We support the SILK->CELT case (celt_to_silk=0): the main CELT layer
		// decodes from a length reduced by redundancy_bytes, then a reset CELT
		// decoder decodes the redundant frame from the packet tail, whose state
		// seeds the following CELT-only packet's coarse-energy prediction.
		redundancy := false
		celtToSilk := false
		redundancyBytes := 0
		celtLen := len(stream)
		if dec.ECTell()+37 <= len(stream)*8 {
			redundancy = dec.DecodeBitLogp(12)
		}
		if redundancy {
			celtToSilk = dec.DecodeBitLogp(1)
			redundancyBytes = int(dec.DecodeUint(256)) + 2
			celtLen = len(stream) - redundancyBytes
			if celtLen < 0 || celtLen*8 < dec.ECTell() {
				celtLen = len(stream)
				redundancyBytes = 0
				redundancy = false
			} else {
				dec.ShrinkStorage(redundancyBytes)
			}
		}
		// Each constituent Opus frame replaces libopus' prev_redundancy state;
		// after a packed packet only the final frame therefore remains visible.
		trailingRedundancy = redundancy && !celtToSilk

		var leadingRedundancy []float64
		if redundancy && celtToSilk && redundancyBytes >= 2 && celtLen+redundancyBytes <= len(stream) {
			leadingRedundancy = d.decodeLeadingRedundancy(stream[celtLen:celtLen+redundancyBytes], pktChannels, celtEnd)
		}

		// CELT high-band layer continues from the same range decoder.
		if celtToSilk && d.prevMode == framing.ModeCELTOnly {
			celtDec.Reset()
		} else if d.lastCeltDec != nil && d.lastCeltDec != celtDec {
			celtDec.CopyStateFrom(d.lastCeltDec)
		}
		celtPCM, cerr := celtDec.DecodeHybrid(dec, celtLen, celtStart, celtEnd)
		if cerr == nil {
			d.lastCeltDec = celtDec
			if d.celtResampler != nil {
				celtPCM = d.celtResampler.Process(celtPCM)
			}
			celtPCM = adjustChannels(celtPCM, celtActualCh, d.channels)
			celtPCM = padOrTrim(celtPCM, samplesPerFrame)
			// Time-domain sum of the two layers, clipped to [-1, 1].
			for i := 0; i < samplesPerFrame; i++ {
				v := silkOut[i] + celtPCM[i]
				if v > 1.0 {
					v = 1.0
				} else if v < -1.0 {
					v = -1.0
				}
				silkOut[i] = v
			}
			if len(leadingRedundancy) >= (d.sampleRate/200)*d.channels && d.prevMode != framing.ModeSILKOnly {
				d.crossfadeLeadingRedundancy(silkOut, leadingRedundancy)
			}

			// SILK->CELT redundancy: decode the trailing 5 ms (F5=240 @ 48k) CELT
			// frame on a freshly reset decoder, crossfade the last 2.5 ms of this
			// frame, and adopt its state so the next CELT-only packet predicts
			// coarse energy from the right baseline (matches libopus).
			if redundancy && !celtToSilk && redundancyBytes >= 2 && celtLen+redundancyBytes <= len(stream) {
				if redDec, rerr := celt.NewDecoderEx(240, 48000, 21, celtActualCh); rerr == nil {
					redDec.SetPhaseInversionDisabled(d.phaseInversionDisabled)
					if redPCM, derr := redDec.Decode(stream[celtLen : celtLen+redundancyBytes]); derr == nil {
						if d.celtResampler != nil {
							redPCM = d.celtResampler.Process(redPCM)
						}
						redPCM = adjustChannels(redPCM, celtActualCh, d.channels)
						d.crossfadeRedundancy(silkOut, redPCM, samplesPerFrame)
						// Adopt the redundant frame's CELT state for continuity.
						celtDec.CopyStateFrom(redDec)
					}
				}
			}
		}
		allPCM = append(allPCM, silkOut...)
		rangeFinal ^= dec.GetRng()
	}
	d.lastFinalRange = rangeFinal
	d.lastPitch = info.dec.Pitch() * d.sampleRate / (rateKHz * 1000)
	d.prevSilkInternalCh = pktChannels

	if pktChannels == 1 && stereoPeer != nil && stereoPeer.dec != nil {
		stereoPeer.dec.CopyPrimaryStateFrom(info.dec)
	} else if pktChannels == 2 && monoPeer != nil && monoPeer.dec != nil {
		monoPeer.dec.CopyPrimaryStateFrom(info.dec)
	}

	return allPCM, trailingRedundancy, nil
}

func (d *Decoder) previousLossMode() int {
	if d.prevRedundancy {
		return framing.ModeCELTOnly
	}
	return d.prevMode
}

// previousPacketMode is the framing mode of the most recently received data
// packet. Unlike previousLossMode it is not changed by PLC; libopus uses this
// state (st->mode) when deciding whether a following packet may carry FEC.
func (d *Decoder) previousPacketMode() int {
	if d.lastPacketConfig < 0 {
		return -1
	}
	mode, _, _ := framing.ParseTOCConfig(d.lastPacketConfig)
	return mode
}

// ensureSilkResampler makes sure physical channel ch has a bit-exact SILK
// resampler configured for the given internal rate (kHz). It (re)creates the
// resampler when the rate changes, mirroring libopus silk_decoder_set_fs which
// re-inits resampler_state only on a rate change. A reset clears the filter
// state, which is the intended behaviour on a rate switch.
func (d *Decoder) ensureSilkResampler(ch, rateKHz int) {
	if d.silkRS[ch] != nil && d.silkRSInKHz[ch] == rateKHz {
		return
	}
	rs, err := silk.NewResampler(rateKHz*1000, d.sampleRate)
	if err != nil {
		// Unsupported rate combination: leave the channel without a bit-exact
		// resampler; resampleSILK falls back to channel duplication.
		d.silkRS[ch] = nil
		d.silkRSInKHz[ch] = 0
		return
	}
	d.silkRS[ch] = rs
	d.silkRSInKHz[ch] = rateKHz
}

// resampleSILK resamples one decoded SILK stream from the internal rate to the
// output rate using the persistent per-channel bit-exact resamplers, returning
// interleaved PCM with d.channels channels. It mirrors libopus dec_API.c's
// resampling stage: the resampler is invoked once per SILK frame; the mono
// internal path carries the sStereo.sMid 1-sample delay; and on a stereo->mono
// transition the first SILK frame's right channel is resampled through the
// still-warm channel-1 resampler from the same mono signal.
func (d *Decoder) resampleSILK(pcm []float64, nFrames, pktChannels int, stereoToMono bool) []float64 {
	if nFrames < 1 {
		nFrames = 1
	}
	rs0 := d.silkRS[0]
	if rs0 == nil {
		// No bit-exact resampler available: fall back to generic behaviour.
		return adjustChannels(pcm, pktChannels, d.channels)
	}
	rs1 := d.silkRS[1]

	if pktChannels == 2 {
		// Internal stereo: L through channel 0, R through channel 1, per frame.
		perChanFrameLen := (len(pcm) / 2) / nFrames
		if perChanFrameLen < 1 {
			return adjustChannels(pcm, pktChannels, d.channels)
		}
		lin := make([]int16, perChanFrameLen)
		rin := make([]int16, perChanFrameLen)
		estCap := (len(pcm) / 2) * 6
		outL := make([]int16, 0, estCap)
		outR := make([]int16, 0, estCap)
		for f := 0; f < nFrames; f++ {
			base := f * perChanFrameLen * 2
			for i := 0; i < perChanFrameLen; i++ {
				lin[i] = f64ToI16(pcm[base+i*2])
				rin[i] = f64ToI16(pcm[base+i*2+1])
			}
			outL = append(outL, rs0.Process(lin)...)
			if rs1 != nil {
				outR = append(outR, rs1.Process(rin)...)
			}
		}
		return interleaveSILKOut(outL, outR, d.channels)
	}

	// Internal mono: channel 0 carries the sMid 1-sample delay.
	frameLen := len(pcm) / nFrames
	if frameLen < 1 {
		return adjustChannels(pcm, pktChannels, d.channels)
	}
	rin := make([]int16, frameLen)
	estCap := len(pcm) * 6
	outL := make([]int16, 0, estCap)
	var outR []int16
	if d.channels == 2 {
		outR = make([]int16, 0, estCap)
	}
	for f := 0; f < nFrames; f++ {
		chunk := pcm[f*frameLen : (f+1)*frameLen]
		rin[0] = d.silkSMid
		for i := 0; i < frameLen-1; i++ {
			rin[i+1] = f64ToI16(chunk[i])
		}
		d.silkSMid = f64ToI16(chunk[frameLen-1])
		lout := rs0.Process(rin)
		outL = append(outL, lout...)
		if d.channels == 2 {
			// Only the very first SILK frame after a stereo->mono transition
			// resamples the right channel separately; otherwise duplicate L.
			if stereoToMono && f == 0 && rs1 != nil {
				outR = append(outR, rs1.Process(rin)...)
			} else {
				outR = append(outR, lout...)
			}
		}
	}
	return interleaveSILKOut(outL, outR, d.channels)
}

// f64ToI16 converts a normalized float sample to int16, matching the existing
// (non-saturating) SILK resampler input conversion.
func f64ToI16(x float64) int16 {
	return int16(math.Round(x * 32768.0))
}

// interleaveSILKOut builds normalized float64 output from int16 channel data.
// For mono output it returns L only; for stereo it interleaves L/R, duplicating
// L when R is missing or length-mismatched.
func interleaveSILKOut(l, r []int16, channels int) []float64 {
	if channels < 2 {
		out := make([]float64, len(l))
		for i, v := range l {
			out[i] = float64(v) / 32768.0
		}
		return out
	}
	if len(r) != len(l) {
		r = l
	}
	out := make([]float64, len(l)*2)
	for i := range l {
		out[i*2] = float64(l[i]) / 32768.0
		out[i*2+1] = float64(r[i]) / 32768.0
	}
	return out
}

// DecodePLC performs packet-loss concealment for frameSize samples per channel.
// frameSize may be any positive multiple of 2.5 ms through 120 ms. Before the
// first successful packet (and after Reset), concealment returns zero samples.
func (d *Decoder) DecodePLC(pcm []int16, frameSize int) (int, error) {
	if !isValidLossFrameSize(frameSize, d.sampleRate) {
		return 0, fmt.Errorf("%w: frameSize %d at %d Hz", ErrUnsupportedFrameSize, frameSize, d.sampleRate)
	}
	required := frameSize * d.channels
	if len(pcm) < required {
		return 0, fmt.Errorf("%w: got %d samples, need %d", ErrBufferTooSmall, len(pcm), required)
	}
	floatPCM, err := d.DecodePLCFloat(frameSize)
	if err != nil {
		return 0, err
	}
	for i, sample := range floatPCM {
		sample *= 32768.0
		if sample > 32767.0 {
			sample = 32767.0
		}
		if sample < -32768.0 {
			sample = -32768.0
		}
		pcm[i] = int16(sample)
	}
	return frameSize, nil
}

// DecodePLC24 performs packet-loss concealment to interleaved signed 24-bit
// PCM stored in int32 values. frameSize follows DecodePLC semantics.
func (d *Decoder) DecodePLC24(pcm []int32, frameSize int) (int, error) {
	if !isValidLossFrameSize(frameSize, d.sampleRate) {
		return 0, fmt.Errorf("%w: frameSize %d at %d Hz", ErrUnsupportedFrameSize, frameSize, d.sampleRate)
	}
	required := frameSize * d.channels
	if len(pcm) < required {
		return 0, fmt.Errorf("%w: got %d samples, need %d", ErrBufferTooSmall, len(pcm), required)
	}
	floatPCM, err := d.DecodePLCFloat(frameSize)
	if err != nil {
		return 0, err
	}
	floatToInt24(pcm[:required], floatPCM)
	return frameSize, nil
}

// DecodePLCFloat performs packet-loss concealment and returns interleaved
// float64 PCM. frameSize follows DecodePLC semantics.
func (d *Decoder) DecodePLCFloat(frameSize int) ([]float64, error) {
	if err := d.validatePLCState(frameSize); err != nil {
		return nil, err
	}
	pcm, err := d.decodePLCFloat(frameSize)
	if err != nil {
		return nil, err
	}
	d.applyGain(pcm)
	d.lastPacketDuration = frameSize
	d.lastFinalRange = 0
	return pcm, nil
}

// DecodePLCFloat32 performs packet-loss concealment and returns interleaved
// float32 PCM. frameSize follows DecodePLC semantics.
func (d *Decoder) DecodePLCFloat32(frameSize int) ([]float32, error) {
	pcm, err := d.DecodePLCFloat(frameSize)
	if err != nil {
		return nil, err
	}
	out := make([]float32, len(pcm))
	for i := range out {
		out[i] = float32(pcm[i])
	}
	return out, nil
}

// validatePLCState checks every deterministic PLC failure condition without
// advancing entropy, synthesis, or resampler state.
func (d *Decoder) validatePLCState(frameSize int) error {
	if !isValidLossFrameSize(frameSize, d.sampleRate) {
		return fmt.Errorf("%w: frameSize %d at %d Hz", ErrUnsupportedFrameSize, frameSize, d.sampleRate)
	}
	lossMode := d.previousLossMode()
	if lossMode == -1 {
		return nil
	}
	if lossMode == framing.ModeCELTOnly {
		if d.lastCeltDec == nil {
			return fmt.Errorf("%w: missing CELT decoder history", ErrInvalidState)
		}
		return nil
	}
	if lossMode != framing.ModeSILKOnly && lossMode != framing.ModeHybrid {
		return fmt.Errorf("%w: unsupported previous decoder mode %d", ErrInvalidState, lossMode)
	}
	if d.lastPacketConfig < 0 || d.lastPacketChannels < 1 {
		return fmt.Errorf("%w: missing SILK decoder history", ErrInvalidState)
	}
	rateKHz := silkConfigRateKHz(d.lastPacketConfig)
	info := d.silkDecoders[silkRateIdx(rateKHz)][d.lastPacketChannels-1]
	if info == nil || info.dec == nil {
		return fmt.Errorf("%w: SILK decoder for %d kHz and %d channels", ErrInvalidState, rateKHz, d.lastPacketChannels)
	}
	if lossMode == framing.ModeHybrid {
		if d.lastCeltDec == nil {
			return fmt.Errorf("%w: missing CELT history for hybrid PLC", ErrInvalidState)
		}
	}
	return nil
}

func (d *Decoder) decodePLCFloat(frameSize int) ([]float64, error) {
	lossMode := d.previousLossMode()
	if lossMode == -1 {
		return make([]float64, frameSize*d.channels), nil
	}

	if lossMode == framing.ModeCELTOnly {
		out := make([]float64, 0, frameSize*d.channels)
		for remaining := frameSize; remaining > 0; {
			chunk := lossDecodeChunk(remaining, d.sampleRate, lossMode)
			frame, err := d.decodeCELTPLCFrame(chunk)
			if err != nil {
				return nil, err
			}
			out = append(out, frame...)
			remaining -= chunk
		}
		d.prevMode = framing.ModeCELTOnly
		d.prevRedundancy = false
		return out, nil
	}

	if lossMode != framing.ModeSILKOnly && lossMode != framing.ModeHybrid {
		return nil, fmt.Errorf("%w: unsupported previous decoder mode %d", ErrInvalidState, lossMode)
	}
	if d.lastPacketConfig < 0 || d.lastPacketChannels < 1 {
		return nil, fmt.Errorf("%w: missing SILK decoder history", ErrInvalidState)
	}

	rateKHz := silkConfigRateKHz(d.lastPacketConfig)
	ri := silkRateIdx(rateKHz)
	ci := d.lastPacketChannels - 1
	info := d.silkDecoders[ri][ci]
	if info == nil || info.dec == nil {
		return nil, fmt.Errorf("%w: SILK decoder for %d kHz and %d channels", ErrInvalidState, rateKHz, d.lastPacketChannels)
	}
	out := make([]float64, 0, frameSize*d.channels)
	for remaining := frameSize; remaining > 0; {
		chunk := lossDecodeChunk(remaining, d.sampleRate, lossMode)
		decodeSamples := chunk
		minSILKSamples := d.sampleRate / 100 // SILK PLC produces at least 10 ms.
		if decodeSamples < minSILKSamples {
			decodeSamples = minSILKSamples
		}
		frameMs := decodeSamples * 1000 / d.sampleRate
		info.dec.SetFrameMs(frameMs)
		silkPCM, err := info.dec.DecodePLC(1)
		if err != nil {
			return nil, fmt.Errorf("SILK PLC decoding failed: %w", err)
		}
		d.ensureSilkResampler(0, rateKHz)
		if d.lastPacketChannels == 2 {
			d.ensureSilkResampler(1, rateKHz)
		}
		frame := d.resampleSILK(silkPCM, 1, d.lastPacketChannels, false)
		frame = padOrTrim(frame, decodeSamples*d.channels)
		frame = frame[:chunk*d.channels]
		if lossMode == framing.ModeHybrid {
			celtPCM, err := d.decodeCELTPLCFrame(chunk)
			if err != nil {
				return nil, err
			}
			for i := range celtPCM {
				v := frame[i] + celtPCM[i]
				if v > 1 {
					v = 1
				} else if v < -1 {
					v = -1
				}
				frame[i] = v
			}
		}
		out = append(out, frame...)
		remaining -= chunk
	}
	d.lastPitch = info.dec.Pitch() * d.sampleRate / (rateKHz * 1000)
	d.prevSilkInternalCh = d.lastPacketChannels
	if peer := d.silkDecoders[ri][1-ci]; peer != nil && peer.dec != nil {
		peer.dec.CopyPrimaryStateFrom(info.dec)
	}
	return out, nil
}

func (d *Decoder) decodeCELTPLCFrame(frameSize int) ([]float64, error) {
	if d.lastCeltDec == nil || d.lastPacketConfig < 0 || d.lastPacketChannels < 1 {
		return nil, fmt.Errorf("%w: missing CELT decoder history", ErrInvalidState)
	}
	frameSize48 := frameSize * SampleRate48kHz / d.sampleRate
	lm := -1
	for i, size := range celtLMFrameSize {
		if size == frameSize48 {
			lm = i
			break
		}
	}
	if lm < 0 {
		return nil, fmt.Errorf("%w: CELT PLC frameSize %d at %d Hz", ErrUnsupportedFrameSize, frameSize, d.sampleRate)
	}
	_, bandwidth, _ := framing.ParseTOCConfig(d.lastPacketConfig)
	bw := 0
	switch bandwidth {
	case framing.BandwidthNarrowband:
		bw = 0
	case framing.BandwidthWideband:
		bw = 1
	case framing.BandwidthSuperwideband:
		bw = 2
	case framing.BandwidthFullband:
		bw = 3
	default:
		return nil, fmt.Errorf("%w: invalid CELT bandwidth %d", ErrInvalidState, bandwidth)
	}
	if bw < 0 || bw >= len(d.celtDecoders) {
		return nil, fmt.Errorf("%w: invalid CELT bandwidth %d", ErrInvalidState, bandwidth)
	}
	active := d.celtDecoders[bw][lm][d.lastPacketChannels-1]
	if active == nil {
		return nil, fmt.Errorf("%w: missing CELT PLC decoder", ErrInvalidState)
	}
	if active != d.lastCeltDec {
		active.CopyStateFrom(d.lastCeltDec)
	}
	frame, err := active.DecodePLC()
	if err != nil {
		return nil, fmt.Errorf("CELT PLC decoding failed: %w", err)
	}
	d.lastCeltDec = active
	if d.celtResampler != nil {
		frame = d.celtResampler.Process(frame)
	}
	frame = adjustChannels(frame, active.Channels(), d.channels)
	return padOrTrim(frame, frameSize*d.channels), nil
}

func isValidPacketFrameSize(frameSize, sampleRate int) bool {
	for _, numerator := range []int{1, 2, 4, 8, 16, 24, 32, 40, 48} {
		if frameSize*400 == sampleRate*numerator {
			return true
		}
	}
	return false
}

func isValidLossFrameSize(frameSize, sampleRate int) bool {
	quantum := sampleRate / 400
	return quantum > 0 && frameSize > 0 && frameSize <= sampleRate*120/1000 && frameSize%quantum == 0
}

func floatToInt24(dst []int32, src []float64) {
	for i, sample := range src {
		scaled := sample * 8388608.0
		if scaled > 8388607.0 {
			scaled = 8388607.0
		} else if scaled < -8388608.0 {
			scaled = -8388608.0
		}
		dst[i] = int32(math.Round(scaled))
	}
}

func floatToInt16(dst []int16, src []float64) {
	for i, sample := range src {
		scaled := sample * 32768.0
		if scaled > 32767.0 {
			scaled = 32767.0
		} else if scaled < -32768.0 {
			scaled = -32768.0
		}
		dst[i] = int16(scaled)
	}
}

func lossDecodeChunk(remaining, sampleRate, mode int) int {
	f20 := sampleRate / 50
	f10 := sampleRate / 100
	f5 := sampleRate / 200
	chunk := remaining
	if chunk > f20 {
		chunk = f20
	} else if chunk < f20 {
		if chunk > f10 {
			chunk = f10
		} else if mode != framing.ModeSILKOnly && chunk > f5 && chunk < f10 {
			chunk = f5
		}
	}
	return chunk
}

// DecodeFEC decodes in-band forward-error-correction data from the packet
// following a loss. For v1 compatibility, the lost duration is inferred from
// the packet's total duration. Use DecodeFECWithDuration when it differs.
func (d *Decoder) DecodeFEC(data []byte, pcm []int16) (int, error) {
	info, err := inspectPacket(data, d.sampleRate)
	if err != nil {
		return 0, err
	}
	if info.mode == ModeCELTOnly {
		return 0, fmt.Errorf("%w: CELT-only packets do not carry SILK LBRR", ErrUnimplemented)
	}
	return d.DecodeFECWithDuration(data, pcm, info.totalSamples)
}

// DecodeFECWithDuration decodes recovery data for exactly frameSize lost
// samples per channel. frameSize may be any positive multiple of 2.5 ms through
// 120 ms. If the packet cannot carry LBRR, concealment is used as in libopus.
func (d *Decoder) DecodeFECWithDuration(data []byte, pcm []int16, frameSize int) (int, error) {
	if !isValidLossFrameSize(frameSize, d.sampleRate) {
		return 0, fmt.Errorf("%w: frameSize %d at %d Hz", ErrUnsupportedFrameSize, frameSize, d.sampleRate)
	}
	required := frameSize * d.channels
	if len(pcm) < required {
		return 0, fmt.Errorf("%w: got %d samples, need %d", ErrBufferTooSmall, len(pcm), required)
	}
	floatPCM, err := d.DecodeFECFloat(data, frameSize)
	if err != nil {
		return 0, err
	}
	for i, sample := range floatPCM {
		scaled := sample * 32768.0
		if scaled > 32767.0 {
			scaled = 32767.0
		} else if scaled < -32768.0 {
			scaled = -32768.0
		}
		pcm[i] = int16(scaled)
	}
	return frameSize, nil
}

// DecodeFEC24 decodes recovery data to interleaved signed 24-bit PCM stored in
// int32 values. frameSize has the same explicit lost-duration semantics as
// DecodeFECWithDuration.
func (d *Decoder) DecodeFEC24(data []byte, pcm []int32, frameSize int) (int, error) {
	if !isValidLossFrameSize(frameSize, d.sampleRate) {
		return 0, fmt.Errorf("%w: frameSize %d at %d Hz", ErrUnsupportedFrameSize, frameSize, d.sampleRate)
	}
	required := frameSize * d.channels
	if len(pcm) < required {
		return 0, fmt.Errorf("%w: got %d samples, need %d", ErrBufferTooSmall, len(pcm), required)
	}
	floatPCM, err := d.DecodeFECFloat(data, frameSize)
	if err != nil {
		return 0, err
	}
	floatToInt24(pcm[:required], floatPCM)
	return frameSize, nil
}

// DecodeFECFloat decodes recovery data for exactly frameSize lost samples per
// channel and returns interleaved float64 PCM.
func (d *Decoder) DecodeFECFloat(data []byte, frameSize int) ([]float64, error) {
	if err := d.validateFECState(data, frameSize); err != nil {
		return nil, err
	}
	staged, err := d.cloneState()
	if err != nil {
		return nil, err
	}
	pcm, finalRange, err := staged.decodeFECFloat(data, frameSize)
	if err != nil {
		return nil, err
	}
	staged.applyGain(pcm)
	staged.lastPacketDuration = frameSize
	staged.lastFinalRange = finalRange
	*d = *staged
	return pcm, nil
}

// DecodeFECFloat32 is DecodeFECFloat with float32 output.
func (d *Decoder) DecodeFECFloat32(data []byte, frameSize int) ([]float32, error) {
	pcm, err := d.DecodeFECFloat(data, frameSize)
	if err != nil {
		return nil, err
	}
	out := make([]float32, len(pcm))
	for i := range out {
		out[i] = float32(pcm[i])
	}
	return out, nil
}

func (d *Decoder) decodeFECFloat(data []byte, frameSize int) ([]float64, uint32, error) {
	info, err := inspectPacket(data, d.sampleRate)
	if err != nil {
		return nil, 0, err
	}
	config, _, countCode := framing.ParseTOC(data[0])
	frames, err := splitOpusFrames(data[1:], countCode)
	if err != nil {
		return nil, 0, fmt.Errorf("%w: %v", ErrInvalidPacket, err)
	}

	// libopus can recover at most the duration of the first Opus frame. Any
	// earlier part of a longer loss is concealed first. A CELT packet, a CELT
	// predecessor, or a loss shorter than one packet frame is all-PLC.
	packetFrameSize := info.samplesPerFrame
	if frameSize < packetFrameSize || info.mode == ModeCELTOnly || d.previousPacketMode() == framing.ModeCELTOnly {
		pcm, err := d.decodePLCFloat(frameSize)
		return pcm, 0, err
	}
	out := make([]float64, 0, frameSize*d.channels)
	if prefix := frameSize - packetFrameSize; prefix > 0 {
		plc, err := d.decodePLCFloat(prefix)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, plc...)
	}

	rateKHz := silkConfigRateKHz(config)
	if rateKHz != 8 && rateKHz != 12 && rateKHz != 16 {
		return nil, 0, fmt.Errorf("%w: invalid SILK rate %d kHz", ErrInvalidPacket, rateKHz)
	}
	if info.channels < 1 || info.channels > 2 {
		return nil, 0, fmt.Errorf("%w: invalid channel count %d", ErrInvalidPacket, info.channels)
	}
	ri := silkRateIdx(rateKHz)
	ci := info.channels - 1
	infoDec := d.silkDecoders[ri][ci]
	if infoDec == nil || infoDec.dec == nil {
		return nil, 0, fmt.Errorf("%w: SILK decoder for %d kHz", ErrInvalidState, rateKHz)
	}
	frameMs := 20
	if is10msConfig(config) {
		frameMs = 10
	}
	infoDec.dec.SetFrameMs(frameMs)
	nSilkFrames := silkSubframesPerOpusFrame(config)

	silkPCM, err := infoDec.dec.DecodeFEC(frames[0], nSilkFrames)
	if err != nil {
		return nil, 0, fmt.Errorf("%w: SILK LBRR decode: %v", ErrInvalidPacket, err)
	}
	d.ensureSilkResampler(0, rateKHz)
	if info.channels == 2 {
		d.ensureSilkResampler(1, rateKHz)
	}
	silkPCM = d.resampleSILK(silkPCM, nSilkFrames, info.channels, false)
	silkPCM = padOrTrim(silkPCM, packetFrameSize*d.channels)
	finalRange := infoDec.dec.LastFinalRange()
	if info.mode == ModeHybrid {
		// CELT carries no in-band FEC. Its high band advances through PLC and,
		// as in libopus, supplies the combined final range.
		if d.lastCeltDec != nil {
			celtPCM, err := d.decodeCELTPLCFrame(packetFrameSize)
			if err != nil {
				return nil, 0, err
			}
			for i := range silkPCM {
				v := silkPCM[i] + celtPCM[i]
				if v > 1 {
					v = 1
				} else if v < -1 {
					v = -1
				}
				silkPCM[i] = v
			}
			finalRange = d.lastCeltDec.LastFinalRange()
		} else {
			finalRange = 0
		}
	}
	out = append(out, silkPCM...)

	d.prevSilkInternalCh = info.channels
	peerCI := 1 - ci
	if peer := d.silkDecoders[ri][peerCI]; peer != nil && peer.dec != nil {
		peer.dec.CopyPrimaryStateFrom(infoDec.dec)
	}
	d.lastPacketConfig = config
	d.lastPacketChannels = info.channels
	// inspectPacket reports the public Mode* constants; prevMode is compared
	// against the internal framing.Mode* constants (e.g. by DecodePLC).
	if info.mode == ModeHybrid {
		d.prevMode = framing.ModeHybrid
	} else {
		d.prevMode = framing.ModeSILKOnly
	}
	d.prevRedundancy = false
	return out, finalRange, nil
}

func (d *Decoder) validateFECState(data []byte, frameSize int) error {
	if !isValidLossFrameSize(frameSize, d.sampleRate) {
		return fmt.Errorf("%w: frameSize %d at %d Hz", ErrUnsupportedFrameSize, frameSize, d.sampleRate)
	}
	info, err := inspectPacket(data, d.sampleRate)
	if err != nil {
		return err
	}
	if frameSize < info.samplesPerFrame || info.mode == ModeCELTOnly || d.previousPacketMode() == framing.ModeCELTOnly {
		return d.validatePLCState(frameSize)
	}
	config, _, _ := framing.ParseTOC(data[0])
	rateKHz := silkConfigRateKHz(config)
	if rateKHz != 8 && rateKHz != 12 && rateKHz != 16 {
		return fmt.Errorf("%w: invalid SILK rate %d kHz", ErrInvalidPacket, rateKHz)
	}
	if info.channels < 1 || info.channels > 2 {
		return fmt.Errorf("%w: invalid channel count %d", ErrInvalidPacket, info.channels)
	}
	infoDec := d.silkDecoders[silkRateIdx(rateKHz)][info.channels-1]
	if infoDec == nil || infoDec.dec == nil {
		return fmt.Errorf("%w: SILK decoder for %d kHz", ErrInvalidState, rateKHz)
	}
	return nil
}

func (d *Decoder) cloneState() (*Decoder, error) {
	clone, err := NewDecoder(d.sampleRate, d.channels)
	if err != nil {
		return nil, err
	}
	clone.frameSize = d.frameSize
	clone.internalFrameSize = d.internalFrameSize
	clone.prevMode = d.prevMode
	clone.prevRedundancy = d.prevRedundancy
	clone.lastPacketDuration = d.lastPacketDuration
	clone.lastPacketConfig = d.lastPacketConfig
	clone.lastPacketChannels = d.lastPacketChannels
	clone.lastFinalRange = d.lastFinalRange
	clone.lastPitch = d.lastPitch
	clone.gainQ8 = d.gainQ8
	clone.phaseInversionDisabled = d.phaseInversionDisabled
	clone.prevSilkInternalCh = d.prevSilkInternalCh
	clone.silkRSInKHz = d.silkRSInKHz
	clone.silkSMid = d.silkSMid

	for bw := range d.celtDecoders {
		for lm := range d.celtDecoders[bw] {
			for ch := range d.celtDecoders[bw][lm] {
				src := d.celtDecoders[bw][lm][ch]
				dst := clone.celtDecoders[bw][lm][ch]
				if src != nil && dst != nil {
					dst.CopyAllStateFrom(src)
				}
				if src == d.lastCeltDec {
					clone.lastCeltDec = dst
				}
			}
		}
	}
	for ri := range d.silkDecoders {
		for ci := range d.silkDecoders[ri] {
			src := d.silkDecoders[ri][ci]
			dst := clone.silkDecoders[ri][ci]
			if src == nil || dst == nil {
				continue
			}
			dst.dec.CopyAllStateFrom(src.dec)
			dst.sMid = src.sMid
			if src.resampler != nil && dst.resampler != nil {
				dst.resampler.CopyStateFrom(src.resampler)
			} else if src.resampler == nil {
				dst.resampler = nil
			}
			copySilkResampler := func(srcRS *silk.Resampler) *silk.Resampler {
				if srcRS == nil {
					return nil
				}
				dstRS := new(silk.Resampler)
				dstRS.CopyStateFrom(srcRS)
				return dstRS
			}
			dst.silkResampler = copySilkResampler(src.silkResampler)
			dst.silkResamplerL = copySilkResampler(src.silkResamplerL)
			dst.silkResamplerR = copySilkResampler(src.silkResamplerR)
		}
	}
	for ch, src := range d.silkRS {
		if src != nil {
			clone.silkRS[ch] = new(silk.Resampler)
			clone.silkRS[ch].CopyStateFrom(src)
		} else {
			clone.silkRS[ch] = nil
		}
	}
	if d.celtResampler != nil && clone.celtResampler != nil {
		clone.celtResampler.CopyStateFrom(d.celtResampler)
	}
	return clone, nil
}

// Reset clears codec history and last-packet range, pitch, and bandwidth while
// retaining decoder configuration such as output gain and phase inversion.
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
		for ci := range d.silkDecoders[ri] {
			info := d.silkDecoders[ri][ci]
			if info != nil {
				info.dec.Reset()
				if info.resampler != nil {
					info.resampler.Reset()
				}
				if info.silkResampler != nil {
					info.silkResampler.Reset()
				}
				if info.silkResamplerL != nil {
					info.silkResamplerL.Reset()
				}
				if info.silkResamplerR != nil {
					info.silkResamplerR.Reset()
				}
				info.sMid = 0
			}
		}
	}
	d.silkRS[0] = nil
	d.silkRS[1] = nil
	d.silkRSInKHz[0] = 0
	d.silkRSInKHz[1] = 0
	d.silkSMid = 0
	d.prevSilkInternalCh = 0
	d.lastPacketDuration = d.frameSize
	if d.celtResampler != nil {
		d.celtResampler.Reset()
	}
	d.lastCeltDec = nil
	d.prevMode = -1
	d.prevRedundancy = false
	d.lastPacketConfig = -1
	d.lastPacketChannels = 0
	d.lastFinalRange = 0
	d.lastPitch = 0
	return nil
}

// GetLastPacketDuration returns the most recent decode, PLC, or FEC duration in
// samples per channel at the decoder output rate. Before decoding and after
// Reset, it reports the constructor's 20 ms default.
func (d *Decoder) GetLastPacketDuration() int {
	if d.lastPacketDuration > 0 {
		return d.lastPacketDuration
	}
	return d.frameSize
}

// Bandwidth returns the bandwidth of the most recently decoded packet as a
// public Bandwidth* constant. It returns BandwidthAuto before the first
// successful decode or after Reset.
func (d *Decoder) Bandwidth() int {
	if d.lastPacketConfig < 0 {
		return BandwidthAuto
	}
	_, bandwidth, _ := framing.ParseTOCConfig(d.lastPacketConfig)
	return publicPacketBandwidth(bandwidth)
}

// GetBandwidth is a CTL-style alias for Bandwidth.
func (d *Decoder) GetBandwidth() int { return d.Bandwidth() }

// SampleRate returns the decoder output sample rate in Hz.
func (d *Decoder) SampleRate() int { return d.sampleRate }

// Channels returns the decoder output channel count.
func (d *Decoder) Channels() int { return d.channels }

// FinalRange returns the XOR of the entropy decoder final ranges for the most
// recently decoded packet's constituent Opus frames.
func (d *Decoder) FinalRange() uint32 { return d.lastFinalRange }

// Pitch returns the most recently reported decoder pitch period in samples at
// the decoder output rate. Zero means no pitch period is currently available.
func (d *Decoder) Pitch() int { return d.lastPitch }

// SetGain sets the decoder output gain in Q8 dB.
func (d *Decoder) SetGain(gainQ8 int) error {
	if gainQ8 < GainQ8Min || gainQ8 > GainQ8Max {
		return fmt.Errorf("%w: invalid decoder gain %d", ErrBadArg, gainQ8)
	}
	d.gainQ8 = gainQ8
	return nil
}

// Gain returns the configured decoder output gain in Q8 dB.
func (d *Decoder) Gain() int { return d.gainQ8 }

// SetPhaseInversionDisabled disables intensity-stereo phase inversion while
// decoding CELT. This is intended for compatibility with downmixing pipelines;
// disabling it is not compliant with the Opus specification.
func (d *Decoder) SetPhaseInversionDisabled(disabled bool) {
	d.phaseInversionDisabled = disabled
	for bw := range d.celtDecoders {
		for lm := range d.celtDecoders[bw] {
			for ch := range d.celtDecoders[bw][lm] {
				if dec := d.celtDecoders[bw][lm][ch]; dec != nil {
					dec.SetPhaseInversionDisabled(disabled)
				}
			}
		}
	}
}

// PhaseInversionDisabled reports the decoder phase-inversion setting.
func (d *Decoder) PhaseInversionDisabled() bool {
	return d.phaseInversionDisabled
}
