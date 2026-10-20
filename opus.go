package opus

import (
	"errors"
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
	// 64 kbit/s, complexity 5, and CBR. Like NewEncoder it uses
	// ModePolicyLibopus since v1.5.0.
	EncoderProfileLegacy EncoderProfile = iota
	// EncoderProfileLibopus uses libopus-style defaults: automatic bitrate,
	// complexity 9, constrained VBR, and ModePolicyLibopus for the automatic
	// mode, channel and bandwidth decisions.
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
	// silkInputResamplers are the bit-exact libopus SILK resamplers
	// (encoder direction, one per channel like silk_encoder_state.resampler_state)
	// from the input rate to silkSampleRate; nil when the rates are equal.
	silkInputResamplers []*silk.Resampler

	// delayBuffer holds the last delayCompensation() input samples
	// (interleaved). CELT codes the input delayed by that amount while SILK
	// codes the current frame, mirroring opus_encoder.c's delay_buffer /
	// pcm_buf split (delay_compensation = Fs/250 outside restricted low delay).
	delayBuffer []float64

	// Input high-pass state (opus_encoder.c hp_mem / variable_HP_smth2_Q15):
	// the VOIP hp_cutoff biquad or the dc_reject blocker runs on every
	// packet before SILK and the delay buffer see the samples.
	hpMem              [4]float32
	variableHPSmth2Q15 int32
	// lastConditionedInput is the conditioned input of the most recent
	// packet, the signal the codec actually coded; tests measure quality
	// against it because the high-pass shifts phase relative to the caller's
	// samples. Read-only after the call, and reused by the next call.
	lastConditionedInput []float64
	// conditionBuf and delayJoined back conditionInput's and
	// delayedCELTInput's per-packet outputs.
	conditionBuf []float64
	delayJoined  []float64
	// hybridFadeBuf holds a hybrid frame's faded CELT input.
	hybridFadeBuf []float64
	// silkInOut and silkDownmix back silkInput's result and its L/R
	// downmix; the SILK encoder consumes them within the call that asked.
	silkInOut, silkDownmix []float64
	// celtFadeBuf holds the faded CELT input of a SILK-only (redundancy) or
	// CELT-only packet; it is used within the packet only.
	celtFadeBuf []float64

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
	useInbandFEC bool
	// fecConfig is st->fec_config (OPUS_SET_INBAND_FEC: 0, 1 or 2).
	fecConfig int
	// vbrUnconstrained is !st->vbr_constraint: kept independently of the
	// VBR setting, as OPUS_SET_VBR and OPUS_SET_VBR_CONSTRAINT are.
	vbrUnconstrained bool
	packetLossPerc   int
	// lbrrCoded is the previous packet's LBRR_coded decision (decide_fec
	// hysteresis).
	lbrrCoded bool
	// streamChannels / prevStreamChannels mirror st->stream_channels and
	// st->prev_channels (the rate-dependent mono/stereo decision for a stereo
	// input); toMono is silk_mode.toMono.
	streamChannels     int
	prevStreamChannels int
	// silkPrevChannels is SILK's nPrevChannelsInternal: the stream channel
	// count of the last SILK-coded packet (0 after a SILK re-init), which
	// drives the resampler hand-over on mono<->stereo transitions.
	silkPrevChannels int
	// silkStaleSideBuf is the side channel's inputBuf during a stereo ->
	// mono prefill at a new SILK rate (see silkInternalRateControl).
	silkStaleSideBuf []int16
	// silkMinInternalRate / silkMaxInternalRate are the packet's
	// silk_mode.minInternalSampleRate / maxInternalSampleRate.
	silkMinInternalRate, silkMaxInternalRate int
	// silkOpusCanSwitch is st->silk_mode.opusCanSwitch: switchReady &&
	// !nonfinal_frame of the last SILK call, read by the next one.
	silkOpusCanSwitch bool
	// silkBwSwitch is st->silk_bw_switch: SILK signalled switchReady, the
	// next packet switches its internal rate (leading redundancy, prefill 2).
	silkBwSwitch bool
	// nonfinalFrame is st->nonfinal_frame: this 20 ms frame is not the last
	// of a multi-frame packet, so SILK may not switch its rate after it.
	nonfinalFrame bool
	toMono        bool
	// peakSignalEnergy / nbNoActivityMsQ1 are st->peak_signal_energy and
	// st->nb_no_activity_ms_Q1 (libopus DTX); silkUseDTX is silk_mode.useDTX
	// for the packet. frameIsSilence / frameAnalysis / packetLSBDepth carry
	// the packet's (or, in a multi-frame packet, the frame's) silence test and
	// analysis to opus_encode_frame_native.
	peakSignalEnergy float32
	nbNoActivityMsQ1 int
	silkUseDTX       bool
	frameIsSilence   bool
	frameAnalysis    celt.AnalysisInfo
	packetLSBDepth   int
	// frameSILKDTX marks a frame SILK DTX dropped (no delay-buffer tail).
	frameSILKDTX bool
	// silkFrameMs is the SILK / hybrid frame duration of the packet being
	// coded under the libopus policy: 10 for a 10 ms packet, 20 otherwise.
	silkFrameMs int
	// packetEquivRate is the libopus policy's equiv_rate for the packet
	// being coded, valid while packetEquivRateSet. It can be zero or
	// negative (2.5 ms frames at low rates).
	packetEquivRate    int
	packetEquivRateSet bool
	// analysisReadPos / analysisReadSubframe are analysis_read_pos_bak and
	// analysis_read_subframe_bak: the analysis read position before this
	// packet's run_analysis (-1 when the analysis did not run).
	analysisReadPos, analysisReadSubframe int
	// outDataBytes is the multistream encoder's per-stream packet budget
	// (opus_encode_native's out_data_bytes; 0 outside it), and
	// libopusForcedMode st->user_forced_mode (-1 for automatic) under the
	// libopus policy.
	outDataBytes int
	// maxPacketSize is SetMaxPacketSize: the caller's output buffer for a
	// single-stream packet (0 = defaultOutDataBytes).
	maxPacketSize int
	// callerPCM / callerFrameSize are the input the tonality analysis reads
	// (analysis_pcm / analysis_frame_size) when it is not the coded frame:
	// the caller's whole input when an expert frame duration codes only a
	// prefix, or a projection encoder's unmixed channels.
	callerPCM []float64
	// inputBuf holds Encode/Encode24's float64 conversion of the caller's
	// samples; it is only read during that call.
	inputBuf        []float64
	callerFrameSize int
	// reportedBitrate is EffectiveBitrate: the setting bounded by 1275
	// bytes per coded frame. Under the libopus policy the packet bitrate
	// (e.bitrate) is st->bitrate_bps instead, bounded by max_data_bytes.
	reportedBitrate   int
	libopusForcedMode int
	// lfe is st->lfe (OPUS_SET_LFE), set by the surround encoder on its
	// low-frequency effects stream under the libopus policy.
	lfe bool
	// streamStarted is set by the first encode after construction or Reset:
	// SetModePolicy resets the stream state only once encoding has begun.
	streamStarted bool
	// autoBandwidth is st->auto_bandwidth (the hysteresis memory of the
	// automatic bandwidth decision); silkFramesCoded marks that the SILK
	// encoder has coded frames since (re)initialisation, after which the
	// internal rate is not changed.
	autoBandwidth   int
	silkFramesCoded bool

	// Bandwidth control (CELT-only path). maxBandwidth caps the automatic
	// selection; forcedBandwidth pins an exact bandwidth (BandwidthAuto means
	// automatic). Both use the public Bandwidth* constants.
	maxBandwidth    int
	forcedBandwidth int
	// signalSetting is the public OPUS_SET_SIGNAL-style request. SignalAuto
	// remains observable as Auto while the internal CELT hint is derived from
	// application.
	signalSetting SignalType

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

	forceChannels int
	lsbDepth      int
	// inputLSBDepth is the precision of the API call (16 for Encode, 24 for
	// Encode24 / EncodeFloat); libopus uses IMIN(call depth, OPUS_SET_LSB_DEPTH).
	inputLSBDepth int
	// inputIntegerAPI marks Encode / Encode24 input (downmix_int*) for the
	// analysis.
	inputIntegerAPI bool
	// analysis is the tonality analysis state (libopus st->analysis), run at
	// complexity >= 7 on the raw input of every frame.
	analysis *celt.TonalityAnalysis
	// hybridStereoWidthQ14 is libopus st->hybrid_stereo_width_Q14: the
	// stereo width the previous CELT frame was faded to (16384 = full).
	hybridStereoWidthQ14 int
	// prevHBGain is libopus st->prev_HB_gain: the hybrid high-band gain the
	// previous frame faded to.
	prevHBGain float32
	// stereoWidth is libopus st->width_mem; libopusBandwidth is st->bandwidth,
	// the bandwidth kept across SILK/hybrid packets (-1 before the first).
	stereoWidth      stereoWidthState
	libopusBandwidth int
	// libopusMode is st->mode (the last decided mode; hybrid initially);
	// packetStereoWidth is the packet's compute_stereo_width result.
	libopusMode       int
	packetStereoWidth float32
	// libopusModePolicy selects opus_encode_native's automatic mode /
	// bandwidth decision (decideLibopusMode) instead of the Go encoder's
	// bitrate-boundary rules; see SetModePolicy.
	libopusModePolicy bool
	// pendingMode is the mode decided for the packet being encoded under the
	// libopus policy (-1 otherwise), read by the high-pass cutoff smoother.
	pendingMode     int
	celtFadeScratch []float64
	// silkInScratch and silkResScratch hold silkInput's per-channel int16
	// resampler input and output; both are consumed before it returns.
	silkInScratch, silkResScratch [2][]int16
	// celtPrefillTail is the last 2.5 ms of the previous packet's CELT input
	// (libopus tmp_prefill from the delay buffer), the prefill of the CELT
	// encoder after a mode transition.
	celtPrefillTail        []float64
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
	case ApplicationVOIP, ApplicationAudio, ApplicationRestrictedLowDelay,
		ApplicationRestrictedSILK, ApplicationRestrictedCELT:
		return true
	}
	return false
}

// isRestrictedCodecApplication reports the libopus 1.6 applications that
// fix the codec at creation (RESTRICTED_SILK and RESTRICTED_CELT). They
// have no Go-policy behaviour: their encoders always follow the libopus
// policy.
func isRestrictedCodecApplication(application Application) bool {
	return application == ApplicationRestrictedSILK || application == ApplicationRestrictedCELT
}

// celtOnlyApplication reports the applications that only code CELT-only
// packets without the Opus-layer delay compensation (RESTRICTED_LOWDELAY
// and RESTRICTED_CELT).
func (e *Encoder) celtOnlyApplication() bool {
	return e.application == ApplicationRestrictedLowDelay || e.application == ApplicationRestrictedCELT
}

// NewEncoder creates a stateful Opus encoder using the legacy compatibility
// defaults: 64 kbit/s, complexity 5, and CBR. Its automatic mode, channel
// and bandwidth decisions follow ModePolicyLibopus (since v1.5.0); call
// SetModePolicy(ModePolicyLegacy) for the earlier decisions.
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
		sampleRate:           sampleRate,
		channels:             channels,
		application:          application,
		celtEncoder:          celtEnc,
		bitrateSetting:       64000,
		bitrate:              64000,            // Default bitrate
		complexity:           5,                // Default complexity
		rateMode:             celt.RateModeCBR, // Default CBR (backward compatible)
		frameSize:            frameSize,
		expertFrameDuration:  ExpertFrameDurationArgument,
		maxBandwidth:         BandwidthFullband,
		forcedBandwidth:      BandwidthAuto,
		lastDetectedBW:       -1, // no detection history yet
		internalFrameSize:    internalFrameSize,
		prevMode:             -1, // no previous packet yet
		forceChannels:        ChannelsAuto,
		lsbDepth:             LSBDepthDefault,
		hybridStereoWidthQ14: 1 << 14,
		prevHBGain:           1,
		libopusBandwidth:     -1,
		libopusMode:          framing.ModeHybrid,
		libopusForcedMode:    -1,
		variableHPSmth2Q15:   variableHPSmth2Initial(),
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
		if err := enc.rebuildSILKEncoder(silkRate); err != nil {
			return nil, err
		}
		enc.silkEncoder.SetRateMode(silk.RateModeCBR)
	}
	// The libopus mode policy is the default since v1.5.0.
	enc.libopusModePolicy = true

	return enc, nil
}

// rebuildSILKEncoder creates the SILK encoder (and the input resamplers)
// for the given internal rate, carrying the encoder-level settings over.
func (e *Encoder) rebuildSILKEncoder(silkRate int) error {
	silkEnc, err := silk.NewEncoder(silkRate, e.channels)
	if err != nil {
		return fmt.Errorf("failed to create SILK encoder: %w", err)
	}
	_ = silkEnc.SetComplexity(e.complexity)
	silkEnc.SetAPISampleRate(e.sampleRate)
	if e.silkEncoder != nil {
		silkEnc.SetRateMode(e.silkEncoder.RateMode())
		silkEnc.SetTrellisNSQ(e.silkEncoder.TrellisNSQ())
	}
	var resamplers []*silk.Resampler
	if silkRate != e.sampleRate {
		for ch := 0; ch < e.channels; ch++ {
			r, err := silk.NewEncoderResampler(e.sampleRate, silkRate)
			if err != nil {
				return fmt.Errorf("failed to create SILK input resampler: %w", err)
			}
			resamplers = append(resamplers, r)
		}
	}
	e.silkEncoder = silkEnc
	e.silkSampleRate = silkRate
	e.silkInputResamplers = resamplers
	e.silkPrevChannels = 0
	e.syncSILKFEC()
	return nil
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
		if err := enc.SetModePolicy(ModePolicyLibopus); err != nil {
			return nil, err
		}
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

func (e *Encoder) effectiveSignalType() celt.SignalType {
	if e.signalSetting == SignalAuto {
		return signalTypeForApplication(e.application)
	}
	return e.signalSetting
}

func (e *Encoder) syncSignalType() {
	hint := e.effectiveSignalType()
	for _, enc := range e.celtEncoders {
		if enc != nil {
			enc.SetSignalType(hint)
		}
	}
	if e.redundancyCelt != nil {
		e.redundancyCelt.SetSignalType(hint)
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

	// Convert int16 to float64 in a reused buffer: the encoder copies
	// whatever it keeps (EncodeFloat callers may reuse their buffers too).
	floatPCM := e.inputScratch(expectedSize)
	for i := range floatPCM {
		floatPCM[i] = float64(pcm[i]) / 32768.0
	}

	e.inputLSBDepth, e.inputIntegerAPI = 16, true
	return e.encodeCallerFrame(floatPCM, frameSize, selectedFrameSize)
}

// inputScratch returns e.inputBuf resized to n samples. Every element is
// overwritten by the caller.
func (e *Encoder) inputScratch(n int) []float64 {
	if cap(e.inputBuf) < n {
		e.inputBuf = make([]float64, n)
	}
	return e.inputBuf[:n]
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
	floatPCM := e.inputScratch(expectedSize)
	for i := range floatPCM {
		floatPCM[i] = float64(pcm[i]) / 8388608.0
	}
	e.inputLSBDepth, e.inputIntegerAPI = 24, true
	return e.encodeCallerFrame(floatPCM, frameSize, selectedFrameSize)
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

	e.inputLSBDepth, e.inputIntegerAPI = 24, false
	return e.encodeCallerFrame(pcm[:expectedSize], frameSize, selectedFrameSize)
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
	floatPCM := make([]float64, expectedSize)
	for i := range floatPCM {
		floatPCM[i] = float64(pcm[i])
	}
	// opus_encode_float passes an LSB depth of 24.
	e.inputLSBDepth, e.inputIntegerAPI = 24, false
	return e.encodeCallerFrame(floatPCM, frameSize, selectedFrameSize)
}

// encodeCallerFrame codes the first selectedFrameSize samples of the
// caller's frameSize samples. Like opus_encode_native's analysis_pcm and
// analysis_frame_size, the tonality analysis sees all the caller's samples
// when an expert frame duration selects fewer.
func (e *Encoder) encodeCallerFrame(pcm []float64, frameSize, selectedFrameSize int) ([]byte, error) {
	if selectedFrameSize < frameSize {
		e.callerPCM, e.callerFrameSize = pcm, frameSize
		defer func() { e.callerPCM, e.callerFrameSize = nil, 0 }()
	}
	return e.encodeFloat(pcm[:selectedFrameSize*e.channels], selectedFrameSize)
}

// encodeFloat is the internal encoding path shared by Encode and EncodeFloat.
// Under the libopus policy a packet padding request (SetPacketPadding) is
// applied to the finished packet of any mode: its frames are repacked as a
// padded code-3 packet.
func (e *Encoder) encodeFloat(pcm []float64, frameSize int) ([]byte, error) {
	pkt, err := e.encodeFloatPacket(pcm, frameSize)
	if err != nil || !e.libopusModePolicy || e.padBytes <= 0 || len(pkt) == 0 {
		return pkt, err
	}
	toc, frames, _, err := PacketParse(pkt)
	if err != nil {
		return nil, fmt.Errorf("failed to pad packet: %w", err)
	}
	payload, err := packOpusFramesCode3(frames, e.rateMode != celt.RateModeCBR, true, e.padBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to pad packet: %w", err)
	}
	return append([]byte{toc | 3}, payload...), nil
}

// encodeFloatPacket codes one packet.
//
// Short 2.5/5/10 ms requests use their corresponding CELT geometry. Requests
// that are exact multiples (1..6) of the 20 ms base are split into consecutive
// 20 ms frames and packed as one Opus packet (RFC 6716 §3.2).
func (e *Encoder) encodeFloatPacket(pcm []float64, frameSize int) ([]byte, error) {
	// The Go policy codes a forced-mono stereo input with a separate mono
	// encoder. libopus instead keeps its one encoder and codes a mono stream
	// (stream_channels = force_channels, the CELT MDCT and SILK mid averaged
	// from both channels), which is also what a 40/60 ms packet coded during
	// the stereo->mono delay leaves behind: it sets force_channels = 1 for
	// good.
	if e.libopusModePolicy && e.outBufferBytes() == 1 && e.sampleRate == frameSize*10 {
		// opus_encode_native: "Cannot encode 100 ms in 1 byte" (after
		// clearing rangeFinal, before any state moves).
		e.lastFinalRange = 0
		return nil, fmt.Errorf("%w: 100 ms packet in a 1-byte buffer", ErrBufferTooSmall)
	}
	e.streamStarted = true
	if !e.libopusModePolicy && e.forceChannels == ChannelsMono && e.channels == ChannelsStereo {
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
	// opus_encode_native: lsb_depth = IMIN(call depth, OPUS_SET_LSB_DEPTH);
	// the tonality analysis runs on the raw input at complexity >= 7 for
	// 16-48 kHz input and feeds the CELT encoder (CELT_SET_ANALYSIS).
	lsbDepth := e.lsbDepth
	if e.inputLSBDepth > 0 && e.inputLSBDepth < lsbDepth {
		lsbDepth = e.inputLSBDepth
	}
	e.celtEncoder.SetLSBDepth(lsbDepth)
	var analysisInfo celt.AnalysisInfo
	e.analysisReadPos, e.analysisReadSubframe = -1, -1
	if e.complexity >= 7 && e.sampleRate >= 16000 && e.sampleRate <= 48000 && e.application != ApplicationRestrictedSILK {
		if e.analysis == nil {
			e.analysis = celt.NewTonalityAnalysis(e.sampleRate)
		}
		e.analysisReadPos, e.analysisReadSubframe = e.analysis.ReadPosition()
		analysisPCM, analysisFrameSize := pcm, frameSize
		if e.callerPCM != nil {
			analysisPCM, analysisFrameSize = e.callerPCM, e.callerFrameSize
		}
		e.analysis.SetIntegerInput(e.inputIntegerAPI)
		analysisInfo = e.analysis.Run(analysisPCM, analysisFrameSize, frameSize, e.channels, lsbDepth)
	} else if e.analysis != nil && e.analysis.Initialized() {
		e.analysis.Reset()
	}
	e.celtEncoder.SetAnalysis(analysisInfo)
	if err := e.applyBitrateSetting(frameSize); err != nil {
		return nil, err
	}
	// opus_encode_native: the input is high-pass conditioned, SILK codes the
	// conditioned current frame, CELT sees it delayed by delay_compensation,
	// and the bandwidth analysis reads the raw input.
	raw := pcm
	nFrames := 0
	if frameSize >= e.frameSize {
		nFrames = frameSize / e.frameSize
	}
	// opus_encode_native: stream channels, then the mode / bandwidth
	// decision (the toMono delay only applies to SILK and hybrid packets),
	// before the high-pass conditioning whose cutoff depends on the mode.
	maxDataBytes := MaxFrameBytes + 1
	if e.libopusModePolicy {
		// max_data_bytes = IMIN(1276*6, out_data_bytes); in CBR it becomes
		// cbr_bytes (applyOutDataBytes).
		maxDataBytes = 6 * (MaxFrameBytes + 1)
		if err := e.applyOutDataBytes(frameSize, &maxDataBytes); err != nil {
			return nil, err
		}
	} else if e.rateMode == celt.RateModeCBR {
		maxDataBytes = (bitrateToBits(e.bitrate, e.sampleRate, frameSize) + 4) / 8
		if maxDataBytes > MaxFrameBytes+1 {
			maxDataBytes = MaxFrameBytes + 1
		}
		if maxDataBytes < 1 {
			maxDataBytes = 1
		}
	}
	var decision modeDecision
	if e.libopusModePolicy {
		// is_digital_silence, the peak signal energy and silk_mode.useDTX:
		// SILK's own DTX only runs when the generalized DTX cannot.
		isSilence := isDigitalSilence(raw, lsbDepth)
		e.trackPeakSignalEnergy(raw, isSilence, analysisInfo)
		e.packetStereoWidth = 0
		if e.channels == 2 && e.forceChannels != ChannelsMono {
			e.packetStereoWidth = e.stereoWidth.compute(raw, frameSize, e.sampleRate)
		}
		if pkt, ok := e.libopusPLCFrame(frameSize, maxDataBytes); ok {
			// opus_encode_native clears rangeFinal on entry and a PLC frame
			// codes nothing.
			e.lastFinalRange = 0
			return pkt, nil
		}
		e.silkUseDTX = e.dtx && !(analysisInfo.Valid || isSilence)
		if e.silkEncoder != nil {
			e.silkEncoder.SetReducedDependency(e.predictionDisabled)
		}
		e.frameIsSilence, e.frameAnalysis, e.packetLSBDepth = isSilence, analysisInfo, lsbDepth
		defer func() { e.packetEquivRate, e.packetEquivRateSet = 0, false }()
		e.decideStreamChannelsLibopus(frameSize, maxDataBytes, analysisInfo)
		decision = e.decideLibopusMode(raw, frameSize, maxDataBytes, analysisInfo)
		if decision.mode == framing.ModeCELTOnly && e.toMono {
			e.toMono = false
			e.streamChannels = 1
		}
		e.pendingMode = decision.mode
		e.packetEquivRate, e.packetEquivRateSet = decision.equivRate, true
	} else {
		e.pendingMode = -1
	}
	e.silkFrameMs = 20
	if e.libopusModePolicy && frameSize < e.frameSize {
		// A 2.5/5/10 ms packet is one frame of its own size; below 10 ms
		// the decision is always CELT-only, at 10 ms SILK and hybrid code
		// 10 ms frames.
		nFrames = 1
		if frameSize*100 == e.sampleRate {
			e.silkFrameMs = 10
		}
	}
	if e.libopusModePolicy && nFrames >= 1 {
		if decision.silkPrefill && e.silkEncoder != nil {
			// CELT-only -> SILK/hybrid: silk_InitEncoder, once per packet; the
			// encoder starts at the rate of the decided bandwidth (16 kHz for
			// hybrid) and every frame of the packet prefills it.
			rate := 16000
			if decision.mode == framing.ModeSILKOnly {
				rate = e.silkInternalRateForBandwidth(decision.bandwidth)
			}
			e.silkFramesCoded = false
			if err := e.rebuildSILKEncoder(rate); err != nil {
				return nil, err
			}
			if err := e.applyBitrateSetting(frameSize); err != nil {
				return nil, err
			}
		}
		// opus_encode_native: packets longer than 20 ms in hybrid / CELT-only
		// mode (or longer than 60 ms) are coded as 20 ms frames with the
		// packet's decisions and repacketized; the rest is one frame.
		if (nFrames > 1 && decision.mode != framing.ModeSILKOnly) || nFrames > 3 {
			return e.encodeMultiframeLibopus(raw, frameSize, nFrames, maxDataBytes, decision)
		}
	}
	return e.encodeDecidedFrame(raw, frameSize, nFrames, maxDataBytes, decision)
}

// encodeDecidedFrame is opus_encode_frame_native: it codes one packet (a
// 20 ms frame, or a 40/60 ms SILK-only packet) whose mode, bandwidth,
// channels and transition flags were decided by the caller. Under the
// libopus policy the per-frame parts of the decision are applied here: the
// silk_bw_switch redundancy and prefill, and the redundancy size from this
// frame's byte budget.
func (e *Encoder) encodeDecidedFrame(pcm []float64, frameSize, nFrames, maxDataBytes int, decision modeDecision) ([]byte, error) {
	if !e.libopusModePolicy || nFrames < 1 {
		return e.encodeDecidedFrameCore(pcm, frameSize, nFrames, maxDataBytes, decision)
	}
	// opus_encode_frame_native codes within IMIN(max_data_bytes, 1276) and
	// pads a CBR packet to the full max_data_bytes.
	origMaxDataBytes := maxDataBytes
	maxDataBytes = min(maxDataBytes, MaxFrameBytes+1)
	// opus_encode_frame_native's voice activity, passed to SILK.
	activity := e.frameActivity(pcm, e.frameIsSilence, e.frameAnalysis, decision.mode)
	if e.silkEncoder != nil {
		e.silkEncoder.SetDTX(e.silkUseDTX)
		e.silkEncoder.SetOpusActivity(activity)
	}
	// SILK DTX returns before the CELT processing, the delay-buffer update
	// and the HB gain / stereo width / previous-mode bookkeeping.
	var saved struct {
		delay          []float64
		prevHBGain     float32
		hybridWidthQ14 int
	}
	if e.silkUseDTX {
		saved.delay = append(saved.delay, e.delayBuffer...)
		saved.prevHBGain = e.prevHBGain
		saved.hybridWidthQ14 = e.hybridStereoWidthQ14
	}
	out, err := e.encodeDecidedFrameCore(pcm, frameSize, nFrames, maxDataBytes, decision)
	var dtx silkDTXPacket
	if errors.As(err, &dtx) {
		copy(e.delayBuffer, saved.delay)
		e.prevHBGain = saved.prevHBGain
		e.hybridStereoWidthQ14 = saved.hybridWidthQ14
		e.lastFinalRange = 0
		return []byte{dtx.toc}, nil
	}
	if err != nil {
		return nil, err
	}
	if activity == silk.VADNoDecision && decision.mode != framing.ModeCELTOnly && e.silkEncoder != nil {
		activity = silk.VADNoActivity
		if e.silkEncoder.LastSignalType() != silk.SignalTypeInactive {
			activity = silk.VADActivity
		}
	}
	// DTX decision: a fully coded frame becomes a TOC-only packet.
	if e.dtx && !e.silkUseDTX {
		if e.decideDTXMode(activity, 2*1000*frameSize/e.sampleRate) {
			e.lastFinalRange = 0
			return []byte{out[0] &^ 0x03}, nil
		}
	} else {
		e.nbNoActivityMsQ1 = 0
	}
	if e.rateMode == celt.RateModeCBR && len(out) > 1 && len(out) < origMaxDataBytes {
		return PacketPad(out, origMaxDataBytes)
	}
	return out, nil
}

// encodeDecidedFrameCore codes the frame (see encodeDecidedFrame).
func (e *Encoder) encodeDecidedFrameCore(pcm []float64, frameSize, nFrames, maxDataBytes int, decision modeDecision) ([]byte, error) {
	raw := pcm
	e.frameSILKDTX = false
	if e.libopusModePolicy && nFrames >= 1 {
		// For the first frame at a new SILK bandwidth: leading redundancy
		// and a prefill without resetting the sampling rate control.
		if e.silkBwSwitch {
			decision.redundancy, decision.celtToSilk, decision.silkPrefill2 = true, true, true
			e.silkBwSwitch = false
		}
		// If we decided to go with CELT, make sure redundancy is off, no
		// matter what we decided earlier; otherwise size it from this frame.
		if decision.mode == framing.ModeCELTOnly {
			decision.redundancy = false
		}
		decision.redundancyBytes = 0
		if decision.redundancy {
			decision.redundancyBytes = computeRedundancyBytes(maxDataBytes, decision.bitrate, e.sampleRate/frameSize, e.streamChannelsOrInput())
			if decision.redundancyBytes == 0 {
				decision.redundancy = false
			}
		}
	}
	pcm = e.conditionInput(pcm, frameSize, nFrames)
	var silkPrefill []float64
	if e.libopusModePolicy && (decision.silkPrefill || decision.silkPrefill2) && frameSize*100 >= e.sampleRate {
		silkPrefill = e.silkPrefillInput()
	}
	celtPCM := e.delayedCELTInput(pcm)
	short := frameSize < e.frameSize
	if short && !e.libopusModePolicy {
		return e.encodeShortCELTPacket(raw, celtPCM)
	}
	if e.libopusModePolicy {
		// The frame's CELT input tail becomes the next packet's tmp_prefill
		// once this packet is done (a SILK prefill fades the current one);
		// a SILK DTX packet returns before that. The tail is the unfaded
		// input: celtPCM is replaced by faded copies below.
		tail := celtPCM
		defer func() {
			if !e.silkEncoderDTX() {
				e.rememberCELTPrefillTail(tail)
			}
		}()
	}
	if !e.libopusModePolicy {
		decision = e.goModeDecision(raw, frameSize, nFrames)
		if decision.mode != framing.ModeSILKOnly {
			// The Go policy codes hybrid and CELT packets with the input
			// channel count; only its SILK-only path downmixes.
			e.streamChannels = e.channels
			e.toMono = false
		}
	}
	if decision.mode == framing.ModeSILKOnly && e.silkEncoder != nil {
		celtToSilk := e.prevMode == framing.ModeCELTOnly
		if e.libopusModePolicy {
			celtToSilk = decision.redundancy && decision.celtToSilk
			// HB_gain is unity and the stereo width follows the equivalent
			// rate on a SILK-only packet; the fades still shape the CELT
			// input a redundant frame is coded from.
			if e.prevHBGain < 1 || e.channels == 2 {
				faded := append(e.celtFadeBuf[:0], celtPCM...)
				e.celtFadeBuf = faded
				if e.prevHBGain < 1 {
					applyGainFade(faded, e.prevHBGain, 1, e.channels, e.sampleRate)
				}
				e.applyStereoWidthFade(faded, celtOnlyStereoWidthQ14(decision.equivRate))
				celtPCM = faded
			}
			e.prevHBGain = 1
		} else {
			e.decideStreamChannels(frameSize, framing.ModeSILKOnly)
		}
		var out []byte
		var err error
		if e.silkOnlyLibopusPath(nFrames) {
			// opus_encode_native's single-frame SILK-only packet: rate,
			// redundancy, internal sample rate control and prefills.
			out, err = e.encodeSILKOnlyPacketLibopus(pcm, celtPCM, nFrames, decision, maxDataBytes, silkPrefill, decision.silkPrefill2)
		} else {
			if err := e.selectSILKInternalRate(frameSize, decision.bandwidth); err != nil {
				return nil, err
			}
			if e.libopusModePolicy {
				e.applyLBRRCoded(decision.lbrrCoded)
			} else if silkBW, ok := nativeSilkFramingBandwidth(e.silkSampleRate); ok {
				e.updateLBRRCoded(framing.ModeSILKOnly, silkBW, e.sampleRate/frameSize)
			}
			out, err = e.encodeSILKOnlyPacket(pcm, raw, celtPCM, nFrames, celtToSilk)
		}
		if err == nil {
			// to_celt: the deferred switch makes CELT-only the predecessor of
			// the next packet.
			e.prevMode = framing.ModeSILKOnly
			if e.libopusModePolicy && decision.toCelt {
				e.prevMode = framing.ModeCELTOnly
			}
			e.prevStreamChannels = e.streamChannels
			e.silkFramesCoded = true
		}
		return out, err
	}
	bw := decision.bandwidth
	hybrid := decision.mode == framing.ModeHybrid && e.silkEncoder != nil && nFrames >= 1 && nFrames <= 6
	if hybrid && e.silkSampleRate != 16000 {
		if err := e.rebuildSILKEncoder(16000); err != nil {
			return nil, err
		}
		if err := e.applyBitrateSetting(frameSize); err != nil {
			return nil, err
		}
	}
	if !e.libopusModePolicy && bw < 0 {
		// The Go policy narrows the automatic CELT bandwidth from the signal.
		bw = e.narrowAutoBandwidth(raw, e.selectCeltBandwidth())
	}

	// libopus-faithful hybrid->CELT transition: when the previous packet was
	// hybrid and this one would switch to CELT-only, defer the switch by one
	// packet. This packet stays hybrid and carries a trailing 5 ms redundant CELT
	// frame whose state seeds the next (genuinely CELT-only) packet, smoothing the
	// handoff (opus_encode_native: prev_mode!=CELT && mode==CELT -> redundancy).
	redundancy := false
	celtToSilk := false
	redundancyBytes := 0
	if e.libopusModePolicy {
		// The deferral (to_celt) and the redundancy were decided with the mode.
		redundancy, celtToSilk, redundancyBytes = decision.redundancy, decision.celtToSilk, decision.redundancyBytes
		if hybrid || decision.mode == framing.ModeCELTOnly {
			// CELT_SET_PREDICTION(2) at the start of every non-SILK packet,
			// 0 with reduced dependency (OPUS_SET_PREDICTION_DISABLED).
			pred := 2
			if e.predictionDisabled {
				pred = 0
			}
			e.celtEncoder.SetPrediction(pred)
		}
	} else if !hybrid && e.prevMode == framing.ModeHybrid && e.canDeferToHybrid(nFrames) {
		bw = e.deferredHybridBandwidth(raw)
		hybrid = true
		redundancy = true
	} else if hybrid && e.prevMode == framing.ModeCELTOnly {
		redundancy = true
		celtToSilk = true
	}

	if hybrid {
		if e.libopusModePolicy {
			e.applyLBRRCoded(decision.lbrrCoded)
		} else {
			e.updateLBRRCoded(framing.ModeHybrid, bw, e.sampleRate/frameSize)
		}
		out, redundancyEmitted, err := e.encodeHybridPacket(pcm, celtPCM, nFrames, bw, redundancy, celtToSilk, redundancyBytes, silkPrefill, decision.silkPrefill2, maxDataBytes, decision.bitrate)
		if err == nil {
			// to_celt: after the deferred frame the real switch happens, so the
			// next packet's predecessor is CELT-only (opus_encode_native),
			// whether or not the redundant frame fit.
			if (e.libopusModePolicy && decision.toCelt) || (!e.libopusModePolicy && redundancyEmitted && !celtToSilk) {
				e.prevMode = framing.ModeCELTOnly
			} else {
				e.prevMode = framing.ModeHybrid
			}
			e.prevStreamChannels = e.celtStreamChannels()
			e.silkFramesCoded = true
		}
		return out, err
	}
	// CELT_SET_END_BAND and CELT_SET_CHANNELS come before the mode-switch
	// reset and prefill (opus_encode_frame_native).
	e.celtEncoder.SetEndBand(celtEndBandForFramingBW(bw))
	streamChannels := e.celtStreamChannels()
	e.celtEncoder.SetStreamChannels(streamChannels)
	if e.libopusModePolicy {
		// A CELT-only packet after a SILK/hybrid one (only possible below
		// 10 ms, where the switch is not deferred) starts from a reset,
		// prefilled CELT state without prediction. A previous hybrid
		// packet's high-band attenuation fades out (gain_fade to unity).
		if e.prevHBGain < 1 {
			faded := append(e.celtFadeBuf[:0], celtPCM...)
			e.celtFadeBuf = faded
			applyGainFade(faded, e.prevHBGain, 1, e.channels, e.sampleRate)
			celtPCM = faded
		}
		e.prevHBGain = 1
		if e.prevMode >= 0 && e.prevMode != framing.ModeCELTOnly {
			if err := e.celtModeTransition(0); err != nil {
				return nil, err
			}
		}
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
	tocFrameSize := framing.FrameSize20ms
	if short {
		tocFrameSize = e.internalFrameSize
	}
	toc, err := framing.GenerateTOCExt(framing.ModeCELTOnly, bw, streamChannels, tocFrameSize)
	if err != nil {
		return nil, fmt.Errorf("failed to generate TOC: %w", err)
	}

	// base = 20 ms in samples at the caller's sample rate.
	base := e.frameSize

	// Single-frame, no padding: compact code-0 packet (TOC + payload).
	if nFrames == 1 && e.padBytes <= 0 {
		celtMax := 0
		if e.libopusModePolicy {
			celtMax = maxDataBytes - 1
		}
		compressed, err := e.encodeOneCELTFrameMax(celtPCM, celtMax)
		if err != nil {
			return nil, err
		}
		e.prevMode = framing.ModeCELTOnly
		e.prevStreamChannels = streamChannels
		e.lastFinalRange = e.celtEncoder.FinalRange()
		return append([]byte{toc}, compressed...), nil
	}

	// Encode each 20 ms chunk continuously (the resampler and CELT encoder keep
	// their inter-frame state across chunks) and pack the frames. A single frame
	// reaches here only when padding was requested, in which case it is wrapped in
	// a code-3 packet (the only count code that carries padding).
	chunkLen := base * e.channels
	if short {
		// A frame shorter than 20 ms is a single-frame packet.
		chunkLen = len(celtPCM)
	}
	frames := make([][]byte, 0, nFrames)
	var rangeFinal uint32
	for k := 0; k < nFrames; k++ {
		chunk := celtPCM[k*chunkLen : (k+1)*chunkLen]
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
	e.prevStreamChannels = streamChannels
	e.lastFinalRange = rangeFinal
	return append([]byte{toc | byte(code)}, payload...), nil
}

// celtStreamChannels is st->stream_channels for a CELT-only or hybrid
// packet: a stereo input may be coded as a mono stream at low rates (SILK
// downmixes, the CELT encoder averages the two channels' MDCTs).
func (e *Encoder) celtStreamChannels() int {
	return e.streamChannelsOrInput()
}

func (e *Encoder) encodeShortCELTPacket(pcm, celtPCM []float64) ([]byte, error) {
	bw := e.narrowAutoBandwidth(pcm, e.selectCeltBandwidth())
	e.celtEncoder.SetEndBand(celtEndBandForFramingBW(bw))
	e.celtEncoder.SetStreamChannels(e.channels)
	toc, err := framing.GenerateTOCExt(framing.ModeCELTOnly, bw, e.channels, e.internalFrameSize)
	if err != nil {
		return nil, fmt.Errorf("failed to generate short-frame TOC: %w", err)
	}
	compressed, err := e.encodeOneCELTFrame(celtPCM)
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
	if e.celtOnlyApplication() {
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
	maxRedundancy := (availableBits*240/(240+48000/frameRate) + baseBits) / 8
	if redundancyBytes > maxRedundancy {
		redundancyBytes = maxRedundancy
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
		c.SetSignalType(e.effectiveSignalType())
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
	if e.celtOnlyApplication() {
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
	if e.celtOnlyApplication() {
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

// encodeHybridPacket codes a hybrid packet. redundancy asks for the 5 ms
// redundant CELT frame of a transition (celtToSilk: leading, from the
// previous CELT-only state; otherwise trailing, before a switch to
// CELT-only); redundancyBytesHint is the size libopus decided with the mode
// (it lowers bits_target) or 0 to size it here.
func (e *Encoder) encodeHybridPacket(pcm, celtPCM []float64, nFrames, bw int, redundancy, celtToSilk bool, redundancyBytesHint int, silkPrefill []float64, prefill2 bool, maxDataBytesIn, bitrateIn int) ([]byte, bool, error) {
	streamChannels := e.celtStreamChannels()
	// unit is the frame length at the input rate: 20 ms, or 10 ms for a
	// 10 ms packet under the libopus policy.
	unit := e.silkUnitFrameSize()
	toc, err := framing.GenerateTOCExt(framing.ModeHybrid, bw, streamChannels, unit*48000/e.sampleRate)
	if err != nil {
		return nil, false, fmt.Errorf("failed to generate hybrid TOC: %w", err)
	}

	inputChunkLen := unit * e.channels
	silkFrameSize := e.silkSampleRate * e.silkFrameMsOrDefault() / 1000
	silkChunkLen := silkFrameSize * streamChannels
	celtEnd := celtEndBandForFramingBW(bw)
	e.celtEncoder.SetStreamChannels(streamChannels)
	if e.channels == 2 && streamChannels == 2 && e.silkPrevChannels == 1 && len(e.silkInputResamplers) > 1 {
		// Mono -> stereo: the side channel's resampler restarts from the
		// mono channel's state (silk_Encode).
		e.silkInputResamplers[1].CopyStateFrom(e.silkInputResamplers[0])
	}
	e.silkEncoder.SetStreamChannels(streamChannels, e.toMono)
	defer func() { e.silkPrevChannels = streamChannels }()
	// opus_encode_native sizing: max_data_bytes is the 1276-byte cap, or in
	// CBR cbr_bytes = (bitrate_to_bits + 4) / 8 (the bitrate is then rounded
	// to that size); bits_target is the packet minus the TOC; SILK gets
	// compute_silk_rate_for_hybrid's share of it and CELT the rest.
	cbr := e.rateMode == celt.RateModeCBR
	bitrateBps := e.bitrate
	maxDataBytes := MaxFrameBytes + 1
	if e.libopusModePolicy && maxDataBytesIn > 0 {
		// opus_encode_frame_native: the frame's max_data_bytes (the packet
		// bound, or its share of a multi-frame packet) and the packet's
		// (CBR-rounded) bitrate.
		maxDataBytes = maxDataBytesIn
		bitrateBps = bitrateIn
	} else if cbr {
		cbrBytes := (bitrateToBits(e.bitrate, e.sampleRate, unit) + 4) / 8
		if cbrBytes > maxDataBytes {
			cbrBytes = maxDataBytes
		}
		bitrateBps = bitsToBitrate(cbrBytes*8, e.sampleRate, unit)
		maxDataBytes = cbrBytes
		if maxDataBytes < 1 {
			maxDataBytes = 1
		}
	}
	bitsTarget := bitrateToBits(bitrateBps, e.sampleRate, unit)
	if b := 8 * (maxDataBytes - redundancyBytesHint); b < bitsTarget {
		bitsTarget = b
	}
	bitsTarget -= 8
	silkRate := 0
	silkMaxBits := 0
	if e.silkEncoder != nil {
		total := bitsToBitrate(bitsTarget, e.sampleRate, unit)
		silkRate = computeSILKRateForHybrid(total, bw, true, !cbr, e.lbrrCoded, streamChannels)
		silkRate = e.surroundSILKRate(silkRate, bw)
		if silkRate < 5000 && !e.libopusModePolicy {
			silkRate = 5000
		}
		if err := e.silkEncoder.SetBitrate(silkRate); err != nil {
			return nil, false, err
		}
		// silk_mode.maxBits: the frame's byte ceiling minus the TOC (and the
		// redundancy bytes plus the 21 flag/size bits). In CBR SILK runs as
		// VBR capped so that it can steal up to 25 % of the bits left after
		// its rate; in VBR the cap is the SILK share of the ceiling.
		silkMaxBits = (maxDataBytes - 1) * 8
		if redundancy && redundancyBytesHint >= 2 {
			silkMaxBits -= redundancyBytesHint*8 + 1 + 20
		}
		if e.rateMode == celt.RateModeCBR {
			otherBits := silkMaxBits - silkRate*unit/e.sampleRate
			if otherBits < 0 {
				otherBits = 0
			}
			silkMaxBits -= otherBits * 3 / 4
			if silkMaxBits < 0 {
				silkMaxBits = 0
			}
		} else {
			maxRate := computeSILKRateForHybrid(silkMaxBits*e.sampleRate/unit, bw, true, true, e.lbrrCoded, streamChannels)
			silkMaxBits = bitrateToBits(maxRate, e.sampleRate, unit)
		}
		e.silkEncoder.SetMaxBits(silkMaxBits)
		defer e.silkEncoder.SetMaxBits(0)
	}
	// silk_control_audio_bandwidth (desired rate 16 kHz in hybrid) with the
	// prefill of a re-initialised encoder; switchReady arms a SILK bandwidth
	// switch with a trailing redundant frame.
	if e.libopusModePolicy {
		// Hybrid: no bandwidth reduction at the lowest bitrates.
		e.silkMinInternalRate, e.silkMaxInternalRate = 16000, 16000
		switchReady, err := e.silkInternalRateControl(unit, 16000, silkPrefill, prefill2, silkRate, silkMaxBits, streamChannels)
		if err != nil {
			return nil, false, err
		}
		e.silkOpusCanSwitch = switchReady && !e.nonfinalFrame
		if switchReady && nFrames == 1 && !e.nonfinalFrame {
			// For the first frame at a new SILK bandwidth: a trailing
			// redundant frame now, the switch on the next packet.
			if bytes := computeRedundancyBytes(maxDataBytes, bitrateBps, e.sampleRate/unit, streamChannels); bytes != 0 {
				redundancy, celtToSilk, redundancyBytesHint = true, false, bytes
				e.silkBwSwitch = true
			}
		}
	}
	// nb_compr_bytes = max_data_bytes - 1 (- redundancy): CBR keeps every
	// hybrid frame at that size, in VBR CELT shrinks the shared coder to the
	// size it selects before the allocation.
	nominalBytes := maxDataBytes - 1

	frames := make([][]byte, 0, nFrames)
	var rangeFinal uint32
	redundancyEmitted := false
	for k := 0; k < nFrames; k++ {
		chunk := pcm[k*inputChunkLen : (k+1)*inputChunkLen]
		celtChunk := celtPCM[k*inputChunkLen : (k+1)*inputChunkLen]
		silkPCM := e.silkInput(chunk, silkChunkLen)
		celtInput := e.celtInputFrame(celtChunk)

		// CELT->SILK redundancy is carried by the first frame; SILK->CELT
		// redundancy is carried by the last frame (libopus frame_redundancy).
		frameRedundancy := redundancy &&
			((celtToSilk && k == 0) || (!celtToSilk && k == nFrames-1))

		maxBytes := nominalBytes
		enc := entcode.NewEncoder(maxBytes)
		e.silkEncoder.SetHybridMode(true)
		if cbr {
			// libopus switches SILK to VBR-with-cap inside a CBR hybrid packet.
			e.silkEncoder.SetRateMode(silk.RateModeCVBR)
		}
		if err := e.silkEncoder.SetFrameMs(e.silkFrameMsOrDefault()); err != nil {
			return nil, false, err
		}
		err := e.silkEncoder.EncodeMultiWithEncoder(enc, silkPCM, 1)
		if cbr {
			e.silkEncoder.SetRateMode(silk.RateModeCBR)
		}
		e.silkEncoder.SetHybridMode(false)
		if err != nil {
			return nil, false, fmt.Errorf("SILK hybrid encoding failed: %w", err)
		}
		if e.libopusModePolicy && e.silkEncoder.PacketDTX() {
			e.frameSILKDTX = true
			return nil, false, silkDTXPacket{toc: toc}
		}
		// CELT_SET_SILK_INFO: the SILK part's signal type and offset.
		e.celtEncoder.SetSILKInfo(e.silkEncoder.LastSILKInfo())
		// HB_gain: increasingly attenuate the high band when it gets allocated
		// fewer bits (gain_fade from the previous frame's gain).
		celtRate := bitsToBitrate(bitsTarget, e.sampleRate, unit) - silkRate
		hbGain := float32(1) - float32(math.Exp(0.6931471805599453094*float64(float32(-celtRate)*float32(1.0/1024))))
		if e.libopusModePolicy && len(e.surroundEnergyMask) > 0 {
			// No high-band attenuation under surround masking.
			hbGain = 1
		}
		if e.prevHBGain < 1 || hbGain < 1 {
			// Fade a copy: celtPCM stays the unfaded input.
			e.hybridFadeBuf = append(e.hybridFadeBuf[:0], celtChunk...)
			celtChunk = e.hybridFadeBuf
			applyGainFade(celtChunk, e.prevHBGain, hbGain, e.channels, e.sampleRate)
			celtInput = e.celtInputFrame(celtChunk)
		}
		e.prevHBGain = hbGain
		// Stereo width for the CELT input: SILK's smoothed width for a stereo
		// hybrid stream, the equivalent-rate ramp for a mono stream.
		if e.channels == 2 {
			widthQ14 := celtOnlyStereoWidthQ14(e.celtEquivRate(e.streamChannelsOrInput()))
			if e.streamChannelsOrInput() == 2 {
				widthQ14 = e.silkEncoder.StereoWidthQ14()
			}
			// celtChunk may already be hybridFadeBuf; copying it onto itself
			// is a no-op.
			e.hybridFadeBuf = append(e.hybridFadeBuf[:0], celtChunk...)
			celtChunk = e.hybridFadeBuf
			e.applyStereoWidthFade(celtChunk, widthQ14)
			celtInput = e.celtInputFrame(celtChunk)
		}

		// Redundancy flag (logp 12) is written between SILK and CELT, then
		// celt_to_silk (0 for SILK->CELT) and the redundant frame length. The gate
		// mirrors libopus: 17 bits of redundancy overhead + 20 bits for the hybrid
		// flag/size must fit. When redundancy is not selected (or does not fit), a
		// false flag is written and the frame stays plain hybrid.
		redundancyBytes := 0
		if frameRedundancy && enc.ECTell()+17+20 <= maxBytes*8 {
			redundancyBytes = redundancyBytesHint
			if redundancyBytes == 0 {
				redundancyBytes = computeRedundancyBytes(maxBytes, e.bitrate, e.sampleRate/unit, streamChannels)
			}
			// Reserve 8 bits for the length plus a few for CELT (libopus
			// max_redundancy for the hybrid branch); target the same bitrate
			// for redundancy as for the rest, 2 to 257 bytes.
			maxRedundancy := (maxBytes - 1) - ((enc.ECTell() + 8 + 3 + 7) >> 3)
			if redundancyBytes > maxRedundancy {
				redundancyBytes = maxRedundancy
			}
			if redundancyBytes < 2 && redundancyBytesHint > 0 {
				// libopus decided the redundancy with the mode and keeps at
				// least 2 bytes; the Go policy drops it instead.
				redundancyBytes = 2
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
		if !frameRedundancy {
			e.silkBwSwitch = false
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
		// CELT->SILK leading redundant frame: encoded from the previous
		// CELT-only state, which celtEncoder still holds at this point (k==0,
		// before the hybrid high band reuses it); libopus encodes it first,
		// then resets celt_enc for the hybrid high band. It is appended to the
		// frame tail below.
		var leadingRedFrame []byte
		var redundantRng uint32
		if frameRedundancy && celtToSilk {
			rf, rng, rerr := e.encodeCELTRedundancy(celtChunk, redundancyBytes, celtEnd, true)
			if rerr != nil {
				return nil, false, fmt.Errorf("CELT leading redundant frame encoding failed: %w", rerr)
			}
			leadingRedFrame, redundantRng = rf, rng
		}
		// libopus gives hybrid CELT the total bitrate minus the SILK target
		// and disables CELT's own VBR constraint; in CBR the CELT part fills
		// the remaining bytes (OPUS_BITRATE_MAX).
		if cbr {
			e.celtEncoder.SetRateMode(celt.RateModeCBR)
			e.celtEncoder.SetBitrateMax()
		} else {
			e.celtEncoder.SetRateMode(celt.RateModeVBR)
			// opus_encode_frame_native sets celt_enc's bitrate to
			// OPUS_BITRATE_MAX on every frame; the CELT control rejects a
			// share of 500 b/s or less (which surround masking of the SILK
			// rate can cause), leaving CELT without a VBR target.
			if celtRate := bitrateBps - silkRate; e.libopusModePolicy && celtRate <= 500 {
				e.celtEncoder.SetBitrateMax()
			} else {
				e.celtEncoder.SetBitrate(celtRate)
			}
		}
		// A hybrid packet after a SILK-only or CELT-only one starts from a
		// reset CELT state prefilled with the 2.5 ms before the frame, without
		// prediction (the analysis and SILK info set above are cleared too).
		if e.libopusModePolicy && k == 0 && e.prevMode >= 0 && e.prevMode != framing.ModeHybrid {
			// CELT_SET_END_BAND comes first in opus_encode_frame_native, so
			// the prefill codes the packet's bands.
			e.celtEncoder.SetEndBand(celtEnd)
			if err := e.celtModeTransition(17); err != nil {
				return nil, false, err
			}
		}
		// "If false, we already busted the budget and we'll end up with a
		// 'PLC frame'": CELT is not run, so its state stays as it was.
		chosenBytes := targetBytes
		var celtErr error
		if enc.ECTell() <= 8*targetBytes {
			chosenBytes, celtErr = e.celtEncoder.EncodeHybrid(
				celtInput, enc, targetBytes, 17, celtEnd, isSilentPCM(celtChunk),
			)
		}
		e.celtEncoder.SetRateMode(e.rateMode)
		e.celtEncoder.SetBitrate(e.bitrate)
		if celtErr != nil {
			return nil, false, fmt.Errorf("CELT hybrid encoding failed: %w", celtErr)
		}
		// The frame is what CELT used (the whole budget in CBR) plus the
		// redundant frame appended after it.
		targetBytes = chosenBytes
		frameBytes = targetBytes + redundancyBytes
		// opus_encode_frame_native: "in the unlikely case that the SILK
		// encoder busted its target, tell the decoder to call the PLC". SILK
		// can overrun silk_mode.maxBits when the LBRR data of the previous
		// packet already fills the budget; the frame then becomes the TOC and
		// a zero byte (plus the redundancy bytes) with a zero final range.
		busted := enc.ECTell() > (maxDataBytes-1)*8
		frameRng := enc.GetRng()
		enc.Flush()
		frame := enc.Bytes()
		if !busted {
			rangeFinal ^= frameRng
			if len(frame) > targetBytes {
				return nil, false, fmt.Errorf("hybrid frame %d exceeds target: %d > %d bytes", k, len(frame), targetBytes)
			}
			if len(frame) < targetBytes {
				padded := make([]byte, targetBytes)
				copy(padded, frame)
				frame = padded
			}
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
				// SILK->CELT: the trailing frame is coded by the main CELT
				// encoder from a reset, prefilled state, so the next (genuinely
				// CELT-only) packet continues from it, as the decoder adopts
				// the redundant decoder's state (celtDec.CopyStateFrom(redDec)).
				rf, rng, rerr := e.encodeCELTRedundancy(celtChunk, redundancyBytes, celtEnd, false)
				if rerr != nil {
					return nil, false, fmt.Errorf("CELT redundant frame encoding failed: %w", rerr)
				}
				redFrame, redundantRng = rf, rng
			}
			redundancyEmitted = true
			if !busted {
				redPadded := make([]byte, redundancyBytes)
				copy(redPadded, redFrame)
				frame = append(frame, redPadded...)
				rangeFinal ^= redundantRng
			}
		}
		if busted {
			// data[1] = 0, ret = 1: the packet keeps 1 + redundancy_bytes
			// payload bytes, of which only the first is rewritten.
			n := 1
			if frameRedundancy {
				n += redundancyBytes
			}
			plc := make([]byte, n)
			if n > 1 {
				copy(plc[1:], frame[min(1, len(frame)):])
			}
			frames = append(frames, plc)
			continue
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

// encodeSILKOnlyPacket codes the conditioned pcm; raw is the caller's input,
// which decides the digital-silence shortcut (like libopus' is_digital_silence
// on the unfiltered input) so a high-pass tail after active audio does not
// turn a silent frame into a coded one; celtPCM feeds the redundant CELT frame.
func (e *Encoder) encodeSILKOnlyPacket(pcm, raw, celtPCM []float64, nFrames int, celtToSilk bool) ([]byte, error) {
	bw, ok := nativeSilkFramingBandwidth(e.silkSampleRate)
	if !ok {
		return nil, fmt.Errorf("SILK-only encoding not available for %d Hz", e.sampleRate)
	}

	streamChannels := e.channels
	if e.channels == 2 && e.streamChannels == 1 {
		streamChannels = 1
	}
	if e.channels == 2 && streamChannels == 2 && e.silkPrevChannels == 1 && len(e.silkInputResamplers) > 1 {
		// Mono -> stereo: the side channel's resampler restarts from the
		// mono channel's state (silk_Encode).
		e.silkInputResamplers[1].CopyStateFrom(e.silkInputResamplers[0])
	}
	e.silkEncoder.SetStreamChannels(streamChannels, e.toMono)
	defer func() { e.silkPrevChannels = streamChannels }()
	groupSize, groups, err := silkPacketGroupsForChannels(nFrames, streamChannels)
	if err != nil {
		return nil, err
	}
	tocFrameSize := groupSize * framing.FrameSize20ms
	toc, err := framing.GenerateTOCExt(framing.ModeSILKOnly, bw, streamChannels, tocFrameSize)
	if err != nil {
		return nil, fmt.Errorf("failed to generate SILK TOC: %w", err)
	}

	inputChunkLen := e.frameSize * e.channels
	silkFrameSize := e.silkSampleRate * 20 / 1000
	silkChunkLen := silkFrameSize * streamChannels
	streams := make([][]byte, 0, len(groups))
	var rangeFinal uint32
	pos := 0
	cbrBytes := 0
	for gi, group := range groups {
		inputSamples := group * inputChunkLen
		silkPCM := e.silkInput(pcm[pos:pos+inputSamples], group*silkChunkLen)
		silent := isSilentPCM(raw[pos : pos+inputSamples])
		// opus_encode_native: max_data_bytes bounds the packet; in CBR it is
		// cbr_bytes = (bitrate_to_bits + 4) / 8, the SILK rate follows that
		// size (bits_to_bitrate(cbr_bytes * 8 - 8)) and the packet is padded
		// to it. silk_mode.maxBits = (max_data_bytes - 1) * 8 drives the
		// encode_frame_FLP quantiser loop.
		maxDataBytes := MaxFrameBytes
		if e.rateMode == celt.RateModeCBR {
			groupSamples := group * e.frameSize
			cbrBytes = (bitrateToBits(e.bitrate, e.sampleRate, groupSamples) + 4) / 8
			if cbrBytes > maxDataBytes {
				cbrBytes = maxDataBytes
			}
			if cbrBytes < 1 {
				cbrBytes = 1
			}
			maxDataBytes = cbrBytes
			silkRate := bitsToBitrate(cbrBytes*8-8, e.sampleRate, groupSamples)
			if silkRate < 5000 {
				silkRate = 5000
			}
			if err := e.silkEncoder.SetBitrate(silkRate); err != nil {
				return nil, err
			}
		}
		e.silkEncoder.SetMaxBits((maxDataBytes - 1) * 8)
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
						celtInput := e.celtInputFrame(celtPCM[pos : pos+inputChunkLen])
						// CELT->SILK leading redundancy: seed the redundant frame from
						// the previous CELT-only state (celtEncoder is untouched on the
						// SILK-only path), matching the decoder which decodes it with its
						// previous CELT state (the redundancy decoder copies lastCeltDec).
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
					// SILK-only without redundancy: libopus strips trailing
					// zero bytes (the decoder fills them in), keeping >= 2.
					for len(stream) > 2 && stream[len(stream)-1] == 0 {
						stream = stream[:len(stream)-1]
					}
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
		streams = append(streams, stream)
		// opus_encode_native reports the final range of the last constituent
		// frame; a one-byte frame tells the decoder to run its PLC and reports
		// zero, and opus_decode mirrors both rules.
		if encodeSILK {
			rangeFinal = e.silkEncoder.LastFinalRange()
		} else {
			rangeFinal = 0
		}
		pos += inputSamples
	}
	e.silkEncoder.SetMaxBits(0)
	payload, code, err := packOpusFramesPadded(streams, true, e.padBytes)
	if err == nil && e.shouldPadSILKPacket(streams, celtToSilk) {
		// opus_packet_pad to cbr_bytes (a single group packet); multi-group
		// packets keep the former per-frame estimate.
		targetBytes := 1 + e.bitrate*(20*nFrames)/1000/8
		if len(groups) == 1 && cbrBytes > 0 {
			targetBytes = cbrBytes
		}
		// opus_packet_pad: the repacketizer picks the layout from the frame
		// sizes alone (a single frame is CBR-laid-out code 3 with padding).
		payload, code, err = packOpusFramesToPacketSize(streams, false, targetBytes)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to pack SILK stream: %w", err)
	}
	e.lastFinalRange = rangeFinal
	return append([]byte{toc | byte(code)}, payload...), nil
}

func (e *Encoder) shouldUseConservativeSILKNSQ(groupFrames int) bool {
	// libopus runs the same delayed-decision NSQ for every configuration; the
	// former 48 kHz mono single-frame fallback to the homebrew quantizer
	// (a 2026-06 conformance workaround) predates the exact noise-shape
	// analysis and is no longer applied.
	return false
}

func (e *Encoder) shouldPadSILKPacket(streams [][]byte, celtToSilk bool) bool {
	if e.rateMode != celt.RateModeCBR || e.padBytes > 0 || celtToSilk {
		return false
	}
	// Padding bytes inside a SILK frame are not inert: libopus interprets any
	// sufficiently large tail as a 5 ms CELT redundancy frame. Keep the coded
	// stream at its natural size and use Opus code-3 packet padding instead.
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
	if e.application == ApplicationRestrictedSILK && selected*100 < e.sampleRate {
		return 0, fmt.Errorf("%w: %w: restricted SILK needs 10 ms frames or longer", ErrBadArg, ErrUnsupportedFrameSize)
	}
	return selected, nil
}

func (e *Encoder) validateFrameSize(frameSize int) (int, error) {
	base := e.frameSize
	if base <= 0 || frameSize <= 0 {
		return 0, fmt.Errorf("%w: %w: frameSize %d at %d Hz", ErrBadArg, ErrUnsupportedFrameSize, frameSize, e.sampleRate)
	}
	for _, divisor := range []int{8, 4, 2} {
		if frameSize*divisor == base {
			return 1, nil
		}
	}
	if frameSize%base != 0 {
		return 0, fmt.Errorf("%w: %w: frameSize %d is not a valid Opus duration at %d Hz", ErrBadArg, ErrUnsupportedFrameSize, frameSize, e.sampleRate)
	}
	nFrames := frameSize / base
	if nFrames < 1 || nFrames > 6 {
		return 0, fmt.Errorf("%w: %w: packet duration %d ms exceeds Opus maximum 120 ms", ErrBadArg, ErrUnsupportedFrameSize, nFrames*20)
	}
	return nFrames, nil
}

// frameSizeSelect is libopus frame_size_select: the frame size coded for a
// caller frame of frameSize samples under an expert duration, or an error
// wrapping ErrBadArg (frame_size_select returns -1).
func frameSizeSelect(application Application, frameSize int, duration ExpertFrameDuration, fs int) (int, error) {
	bad := func(why string) (int, error) {
		return 0, fmt.Errorf("%w: %w: %s (frameSize %d, duration %d, %d Hz)", ErrBadArg, ErrUnsupportedFrameSize, why, frameSize, duration, fs)
	}
	if frameSize < fs/400 {
		return bad("frame shorter than 2.5 ms")
	}
	var newSize int
	switch {
	case duration == ExpertFrameDurationArgument:
		newSize = frameSize
	case duration >= ExpertFrameDuration2_5ms && duration <= ExpertFrameDuration40ms:
		newSize = (fs / 400) << (duration - ExpertFrameDuration2_5ms)
	case duration > ExpertFrameDuration40ms && duration <= ExpertFrameDuration120ms:
		newSize = int(duration-ExpertFrameDuration2_5ms-2) * fs / 50
	default:
		return bad("invalid expert frame duration")
	}
	if newSize > frameSize {
		return bad("selected duration longer than the input")
	}
	if 400*newSize != fs && 200*newSize != fs && 100*newSize != fs &&
		50*newSize != fs && 25*newSize != fs && 50*newSize != 3*fs &&
		50*newSize != 4*fs && 50*newSize != 5*fs && 50*newSize != 6*fs {
		return bad("not a valid Opus duration")
	}
	if application == ApplicationRestrictedSILK && newSize < fs/100 {
		return bad("restricted SILK needs 10 ms frames or longer")
	}
	return newSize, nil
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
	next.SetDTX(e.celtDTX())
	next.SetPhaseInversionDisabled(e.phaseInversionDisabled)
	next.SetSignalType(e.effectiveSignalType())
	next.SetEnergyMask(e.surroundEnergyMask)
	next.SetLFE(e.lfe)
	next.SetUpsample(e.celtUpsample())
	if next != e.celtEncoder {
		next.CopyStateFrom(e.celtEncoder)
		e.celtEncoder = next
	}
	// OPUS_SET_PACKET_LOSS_PERC is forwarded to celt_enc (it biases the
	// coarse-energy intra decision and the prefilter tapset). Set after the
	// state hand-over, which carries the per-frame settings.
	next.SetLossRate(e.packetLossPerc)
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
	return e.encodeOneCELTFrameMax(pcm, 0)
}

// encodeOneCELTFrameMax is encodeOneCELTFrame with nb_compr_bytes bounded by
// maxBytes when maxBytes > 0 (opus_encode_frame_native's max_data_bytes - 1).
func (e *Encoder) encodeOneCELTFrameMax(pcm []float64, maxBytes int) ([]byte, error) {
	streamChannels := e.celtStreamChannels()
	e.celtEncoder.SetStreamChannels(streamChannels)
	if e.channels == 2 {
		if len(e.celtFadeScratch) < len(pcm) {
			e.celtFadeScratch = make([]float64, len(pcm))
		}
		faded := e.celtFadeScratch[:len(pcm)]
		copy(faded, pcm)
		e.applyStereoWidthFade(faded, celtOnlyStereoWidthQ14(e.celtEquivRate(streamChannels)))
		pcm = faded
	}
	celtInput := e.celtInputFrame(pcm)

	var compressed []byte
	var err error
	if maxBytes > 0 {
		compressed, err = e.celtEncoder.EncodeMax(celtInput, maxBytes)
	} else {
		compressed, err = e.celtEncoder.Encode(celtInput)
	}
	if err != nil {
		return nil, fmt.Errorf("CELT encoding failed: %w", err)
	}
	return compressed, nil
}

// silkInput returns the SILK-rate input for the conditioned Opus-rate pcm:
// libopus converts each channel to int16 (RES2INT16) and runs it through the
// channel's silk_resampler; at equal rates the samples pass through and the
// SILK front end applies the resampler's delay itself. The result holds
// int16/32768 values and has exactly want samples (interleaved).
func (e *Encoder) silkInput(pcm []float64, want int) []float64 {
	if e.channels == 2 && e.streamChannels == 1 {
		// nChannelsAPI == 2, nChannelsInternal == 1: combine L/R before the
		// mono channel's resampler; on the first mono frame after a stereo
		// packet both resamplers run and their outputs are averaged.
		e.silkDownmix = silk.DownmixToMonoInto(e.silkDownmix, pcm)
		mono := e.silkDownmix
		if len(e.silkInputResamplers) == 0 {
			return mono
		}
		in := e.silkInScratch[0][:0]
		for _, v := range mono {
			in = append(in, silk.Float2Int16Sample(v))
		}
		e.silkInScratch[0] = in
		out0 := e.silkInputResamplers[0].ProcessAppend(e.silkResScratch[0][:0], in)
		e.silkResScratch[0] = out0
		if e.silkPrevChannels == 2 && len(e.silkInputResamplers) > 1 {
			// Only the first SILK frame is averaged (nFramesEncoded == 0),
			// and only its input runs through the side resampler.
			firstIn := min(len(in), e.sampleRate*e.silkFrameMsOrDefault()/1000)
			out1 := e.silkInputResamplers[1].ProcessAppend(e.silkResScratch[1][:0], in[:firstIn])
			e.silkResScratch[1] = out1
			firstOut := min(len(out0), e.silkSampleRate*e.silkFrameMsOrDefault()/1000)
			for i := range out0[:firstOut] {
				switch {
				case i < len(out1):
					out0[i] = int16((int32(out0[i]) + int32(out1[i])) >> 1)
				case e.silkStaleSideBuf != nil:
					// The side channel ran at a lower rate: past its own
					// output, its inputBuf holds what earlier frames left.
					var stale int32
					if 2+i < len(e.silkStaleSideBuf) {
						stale = int32(e.silkStaleSideBuf[2+i])
					}
					out0[i] = int16((int32(out0[i]) + stale) >> 1)
				}
			}
		}
		out := growFloat64s(e.silkInOut, want)
		e.silkInOut = out
		clear(out)
		for i := range out {
			if i < len(out0) {
				out[i] = float64(out0[i]) / 32768
			}
		}
		return out
	}
	if len(e.silkInputResamplers) == 0 {
		return pcm
	}
	ch := e.channels
	n := len(pcm) / ch
	out := growFloat64s(e.silkInOut, want)[:0]
	var perChannelBuf [2][]int16
	perChannel := perChannelBuf[:0]
	if ch > len(perChannelBuf) {
		perChannel = make([][]int16, 0, ch)
	}
	for c := 0; c < ch; c++ {
		var in, res []int16
		if c < len(e.silkInScratch) {
			in, res = e.silkInScratch[c][:0], e.silkResScratch[c][:0]
		}
		for i := 0; i < n; i++ {
			in = append(in, silk.Float2Int16Sample(pcm[i*ch+c]))
		}
		res = e.silkInputResamplers[c].ProcessAppend(res, in)
		if c < len(e.silkInScratch) {
			e.silkInScratch[c], e.silkResScratch[c] = in, res
		}
		perChannel = append(perChannel, res)
	}
	m := want / ch
	for i := 0; i < m; i++ {
		for c := 0; c < ch; c++ {
			v := 0.0
			if i < len(perChannel[c]) {
				v = float64(perChannel[c][i]) / 32768
			}
			out = append(out, v)
		}
	}
	e.silkInOut = out
	return out
}

// bitsToBitrate and bitrateToBits are celt.h's integer conversions between a
// per-packet bit budget and a bitrate.
func bitsToBitrate(bits, fs, frameSize int) int {
	return bits * (6 * fs / frameSize) / 6
}

func bitrateToBits(bitrate, fs, frameSize int) int {
	return bitrate * 6 / (6 * fs / frameSize)
}

// delayCompensation returns the Opus-layer input delay in samples at the
// encoder rate: Fs/250 (4 ms), none for the restricted applications
// (opus_encode_native's delay_compensation).
func (e *Encoder) delayCompensation() int {
	if e.celtOnlyApplication() || e.application == ApplicationRestrictedSILK {
		return 0
	}
	return e.sampleRate / 250
}

// delayedCELTInput returns pcm delayed by delayCompensation() samples per
// channel and advances the delay buffer, so the returned slice is the
// pcm_buf[0:frame_size] region libopus hands to CELT (the SILK layer keeps
// coding the undelayed pcm).
func (e *Encoder) delayedCELTInput(pcm []float64) []float64 {
	d := e.delayCompensation() * e.channels
	if d == 0 {
		return pcm
	}
	if len(e.delayBuffer) != d {
		e.delayBuffer = make([]float64, d)
	}
	e.delayJoined = growFloat64s(e.delayJoined, d+len(pcm))
	joined := e.delayJoined
	copy(joined, e.delayBuffer)
	copy(joined[d:], pcm)
	copy(e.delayBuffer, joined[len(pcm):])
	return joined[:len(pcm)]
}

func (e *Encoder) celtInputFrame(pcm []float64) []float64 {
	var celtInput []float64
	if up := e.celtUpsample(); up > 1 {
		// libopus: the CELT encoder runs the 48 kHz mode on the input
		// zero-stuffed by resampling_factor(Fs) (celt_preemphasis).
		ch := e.channels
		n := len(pcm) / ch
		celtInput = make([]float64, n*up*ch)
		for t := 0; t < n; t++ {
			for c := 0; c < ch; c++ {
				celtInput[(t*up)*ch+c] = pcm[t*ch+c]
			}
		}
		return celtInput
	}
	if e.inputResampler != nil {
		// Resample from sampleRate to 48kHz.
		resampled := e.inputResampler.Process(pcm)
		// The resampled output is approximately the input's duration at
		// 48 kHz (a 20 ms frame, or a 2.5 ms prefill or 5 ms redundant
		// frame). Pad or trim to exact size for CELT.
		targetLen := len(pcm) / e.channels * (SampleRate48kHz / e.sampleRate) * e.channels
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
func (e *Encoder) EffectiveBitrate() int {
	if e.reportedBitrate > 0 {
		return e.reportedBitrate
	}
	return e.bitrate
}

// Complexity returns the current complexity setting (0–10).
func (e *Encoder) Complexity() int { return e.complexity }

// VBR reports whether variable bitrate is enabled.
func (e *Encoder) VBR() bool { return e.rateMode != celt.RateModeCBR }

// VBRConstraint reports the VBR constraint setting
// (OPUS_GET_VBR_CONSTRAINT), whether or not VBR is on.
func (e *Encoder) VBRConstraint() bool { return !e.vbrUnconstrained }

// SampleRate returns the encoder input sample rate in Hz.
func (e *Encoder) SampleRate() int { return e.sampleRate }

// Channels returns the encoder input channel count.
func (e *Encoder) Channels() int { return e.channels }

// Lookahead returns the codec lookahead in samples at the encoder input rate:
// the CELT overlap (Fs/400) plus the Opus-layer delay compensation (Fs/250,
// zero for restricted low delay), like OPUS_GET_LOOKAHEAD.
func (e *Encoder) Lookahead() int {
	// OPUS_GET_LOOKAHEAD adds st->delay_compensation except for the
	// restricted low-delay and CELT applications (RESTRICTED_SILK reports
	// it although it codes without it).
	if e.celtOnlyApplication() {
		return e.sampleRate / 400
	}
	return e.sampleRate/400 + e.sampleRate/250
}

// FinalRange returns the XOR of the entropy coder final ranges for the most
// recently encoded packet's constituent Opus frames.
func (e *Encoder) FinalRange() uint32 { return e.lastFinalRange }

// InDTX reports whether the most recently encoded packet used the encoder's
// DTX silence path.
func (e *Encoder) InDTX() bool {
	if e.libopusModePolicy {
		return e.libopusInDTX()
	}
	return e.inDTX
}

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

// SetLSBDepth sets the input precision hint in bits per sample
// (OPUS_SET_LSB_DEPTH). As in libopus, the CELT encoder (silence detection and
// dynamic allocation) and the tonality analysis use it; the int16 Encode caps
// it at 16.
func (e *Encoder) SetLSBDepth(depth int) error {
	if depth < LSBDepthMin || depth > LSBDepthMax {
		return fmt.Errorf("%w: invalid LSB depth %d", ErrBadArg, depth)
	}
	e.lsbDepth = depth
	return nil
}

// LSBDepth returns the configured input precision hint.
func (e *Encoder) LSBDepth() int { return e.lsbDepth }

// SetPredictionDisabled is OPUS_SET_PREDICTION_DISABLED. Under
// ModePolicyLibopus it reduces inter-packet dependency as libopus does: SILK
// codes every packet as the first frame after a reset and CELT runs without
// prediction (no prefilter, intra energy). Under ModePolicyLegacy it
// instead keeps the encoder away from the SILK and hybrid modes.
func (e *Encoder) SetPredictionDisabled(disabled bool) {
	e.predictionDisabled = disabled
}

// PredictionDisabled reports whether predictive mode routing is disabled.
func (e *Encoder) PredictionDisabled() bool { return e.predictionDisabled }

// SetPhaseInversionDisabled disables CELT intensity-stereo phase inversion.
// This is intended for compatibility with downmixing pipelines; disabling it
// is not compliant with the Opus specification.
//
// A RESTRICTED_SILK encoder has no CELT layer: like libopus it ignores the
// setting and PhaseInversionDisabled reports false.
func (e *Encoder) SetPhaseInversionDisabled(disabled bool) {
	if e.application == ApplicationRestrictedSILK {
		return
	}
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
		// Only the Go policy codes forced mono with this encoder, and it
		// codes the stream with the parent's policy.
		enc.libopusModePolicy = e.libopusModePolicy
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

// SetBitrate sets the target bitrate in bits per second. It accepts positive
// numeric rates, BitrateAuto, or BitrateMax. Like OPUS_SET_BITRATE, numeric
// requests are clamped to [500, 750000 x Channels()]; EffectiveBitrate is
// additionally bounded by the per-frame byte limit.
func (e *Encoder) SetBitrate(bitrate int) error {
	if bitrate != BitrateAuto && bitrate != BitrateMax {
		// OPUS_SET_BITRATE: positive rates are clamped to
		// [500, 750000 x channels].
		if bitrate <= 0 {
			return fmt.Errorf("%w: invalid bitrate %d (must be positive)", ErrBadArg, bitrate)
		}
		bitrate = max(500, min(750000*e.channels, bitrate))
	}
	oldSetting := e.bitrateSetting
	e.bitrateSetting = bitrate
	if err := e.applyBitrateSetting(e.frameSize); err != nil {
		e.bitrateSetting = oldSetting
		_ = e.applyBitrateSetting(e.frameSize)
		return err
	}
	return nil
}

func (e *Encoder) applyBitrateSetting(frameSize int) error {
	bitrate := e.bitrateSetting
	switch bitrate {
	case BitrateAuto:
		// libopus user_bitrate_to_bitrate(): framing overhead plus one bit per
		// input sample and channel.
		bitrate = 60*e.sampleRate/frameSize + e.sampleRate*e.channels
	case BitrateMax:
		bitrate = e.maxBitrateForFrame(frameSize)
	default:
		if maximum := e.maxBitrateForFrame(frameSize); bitrate > maximum {
			bitrate = maximum
		}
	}
	e.reportedBitrate = bitrate
	if e.libopusModePolicy && e.outDataBytes == 0 {
		// user_bitrate_to_bitrate: OPUS_BITRATE_MAX is 1.5 Mb/s and a
		// numeric setting is used as is; max_data_bytes bounds both when
		// the packet is coded (applyOutDataBytes).
		switch e.bitrateSetting {
		case BitrateAuto:
		case BitrateMax:
			bitrate = 1500000
		default:
			bitrate = e.bitrateSetting
		}
		// IMIN(user_bitrate, bits_to_bitrate(max_data_bytes*8)), kept on
		// every recomputation within the packet.
		limit := min(e.outBufferBytes(), 6*(MaxFrameBytes+1))
		bitrate = min(bitrate, bitsToBitrate(limit*8, e.sampleRate, frameSize))
	}
	return e.setPacketBitrate(bitrate, frameSize)
}

// setPacketBitrate sets the packet's bitrate (st->bitrate_bps) on the
// encoder and its CELT and SILK layers.
func (e *Encoder) setPacketBitrate(bitrate, frameSize int) error {
	e.bitrate = bitrate
	e.celtEncoder.SetBitrate(e.bitrate)
	if e.silkEncoder != nil {
		// opus_encode_native: SILK gets bits_to_bitrate(bitrate_to_bits(bitrate)
		// - 8), i.e. the packet rate minus the TOC byte amortised over the
		// packet (24000 bps at 20 ms -> 23600 bps), which selects SNR_dB_Q7.
		silkBitrate := bitsToBitrate(bitrateToBits(e.bitrate, e.sampleRate, frameSize)-8, e.sampleRate, frameSize)
		if silkBitrate < 5000 {
			silkBitrate = 5000
		}
		if err := e.silkEncoder.SetBitrate(silkBitrate); err != nil {
			return err
		}
	}
	return nil
}

func (e *Encoder) maxBitrateForFrame(frameSize int) int {
	codedFrameSize := frameSize
	if codedFrameSize > e.frameSize {
		// Long requests are packets of consecutive 20 ms Opus frames. The
		// 1275-byte ceiling applies to each constituent frame, not once to
		// the whole packet.
		codedFrameSize = e.frameSize
	}
	bitrate := MaxFrameBytes * 8 * e.sampleRate / codedFrameSize
	if bitrate > 1500000 {
		bitrate = 1500000
	}
	return bitrate
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

// SetVBR enables or disables variable bitrate mode (OPUS_SET_VBR). With
// VBR on, the VBR constraint setting (constrained by default, the libopus
// default) selects constrained or unconstrained VBR.
func (e *Encoder) SetVBR(vbr bool) {
	if vbr {
		e.rateMode = celt.RateModeCVBR
		if e.vbrUnconstrained {
			e.rateMode = celt.RateModeVBR
		}
	} else {
		e.rateMode = celt.RateModeCBR
	}
	e.celtEncoder.SetRateMode(e.rateMode)
	e.syncSILKRateMode()
}

// SetVBRConstraint sets the VBR constraint (OPUS_SET_VBR_CONSTRAINT): true
// (the default) for constrained VBR, false for unconstrained VBR. Like
// libopus the setting is kept while VBR is off and applies when it is
// turned on.
func (e *Encoder) SetVBRConstraint(constrained bool) {
	e.vbrUnconstrained = !constrained
	if e.rateMode == celt.RateModeCBR {
		return
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

// SetMaxPacketSize sets the size of the output buffer that libopus's
// opus_encode would receive (max_data_bytes): no packet is longer, a VBR
// packet of more than one frame shares it between its frames, and a CBR
// packet above 1276 bytes uses it as the packet size limit. It takes effect
// only under ModePolicyLibopus: ModePolicyLegacy stores the value (and
// MaxPacketSize returns it) but does not limit its packets by it. The default
// is 1500 bytes. n must be positive.
func (e *Encoder) SetMaxPacketSize(n int) error {
	if n < 1 {
		return fmt.Errorf("%w: max packet size %d", ErrBadArg, n)
	}
	e.maxPacketSize = n
	return nil
}

// MaxPacketSize returns the SetMaxPacketSize value (1500 by default).
func (e *Encoder) MaxPacketSize() int { return e.outBufferBytes() }

// outBufferBytes is out_data_bytes: the multistream encoder's per-stream
// budget while it codes a stream, otherwise SetMaxPacketSize.
func (e *Encoder) outBufferBytes() int {
	if e.outDataBytes > 0 {
		return e.outDataBytes
	}
	if e.maxPacketSize > 0 {
		return e.maxPacketSize
	}
	return defaultOutDataBytes
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
	e.celtEncoder.SetDTX(e.celtDTX())
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
	if enabled {
		_ = e.SetInbandFECMode(InbandFECOn)
	} else {
		_ = e.SetInbandFECMode(InbandFECOff)
	}
}

// InbandFEC reports whether inband FEC is enabled.
func (e *Encoder) InbandFEC() bool { return e.useInbandFEC }

// In-band FEC modes of SetInbandFECMode (the OPUS_SET_INBAND_FEC values).
const (
	// InbandFECOff disables LBRR.
	InbandFECOff = 0
	// InbandFECOn enables LBRR and may move the encoder to SILK so that
	// it can carry it.
	InbandFECOn = 1
	// InbandFECNoModeSwitch enables LBRR without moving music (a voice
	// estimate of at most 25) to SILK for it.
	InbandFECNoModeSwitch = 2
)

// SetInbandFECMode is OPUS_SET_INBAND_FEC with its three values; mode 2
// differs from 1 only in the libopus mode decision. Other values return
// ErrBadArg.
func (e *Encoder) SetInbandFECMode(mode int) error {
	if mode < InbandFECOff || mode > InbandFECNoModeSwitch {
		return fmt.Errorf("%w: in-band FEC mode %d", ErrBadArg, mode)
	}
	e.fecConfig = mode
	e.useInbandFEC = mode != InbandFECOff
	e.syncSILKFEC()
	return nil
}

// InbandFECMode returns the SetInbandFECMode value (OPUS_GET_INBAND_FEC).
func (e *Encoder) InbandFECMode() int { return e.fecConfig }

// SetPacketLossPerc sets the expected packet-loss percentage used to tune FEC
// redundancy (higher loss → smaller, more frequent LBRR frames). Like
// OPUS_SET_PACKET_LOSS_PERC, which rejects them with OPUS_BAD_ARG, values
// outside 0 through 100 are ignored and leave the setting unchanged. With
// FEC enabled, zero disables LBRR emission.
func (e *Encoder) SetPacketLossPerc(perc int) {
	if perc < 0 || perc > 100 {
		return
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
	if e.libopusModePolicy {
		// decide_fec sets LBRR_coded per packet (SetLBRRCoded); changing
		// the settings keeps the LBRR frames coded with the previous
		// packet, which silk_Encode still writes into the next one.
		return
	}
	e.silkEncoder.SetInbandFEC(e.useInbandFEC && e.packetLossPerc > 0)
}

// SetApplication changes the application mode. When SignalType is Auto, this
// re-derives the effective CELT content hint (voice for VOIP, music otherwise),
// which influences bandwidth selection and transient sensitivity; an explicit
// SignalVoice or SignalMusic request remains pinned.
// Invalid application values return ErrBadArg and leave the encoder unchanged.
func (e *Encoder) SetApplication(application Application) error {
	if !isValidApplication(application) {
		return fmt.Errorf("%w: unsupported application %d", ErrBadArg, application)
	}
	// libopus fixes the restricted SILK / CELT applications at creation:
	// OPUS_SET_APPLICATION fails on such an encoder even with its own
	// value, and cannot select them.
	if isRestrictedCodecApplication(e.application) {
		return fmt.Errorf("%w: application %d is fixed at creation", ErrBadArg, e.application)
	}
	if isRestrictedCodecApplication(application) {
		return fmt.Errorf("%w: application %d can only be set at creation", ErrBadArg, application)
	}
	e.application = application
	e.syncSignalType()
	return nil
}

// SetSignalType overrides the content hint used by encoder heuristics.
// SignalAuto (the default) re-derives the hint from the current Application
// setting (VOIP → voice, otherwise music). Calling this with SignalVoice or
// SignalMusic pins the hint regardless of subsequent Application changes.
func (e *Encoder) SetSignalType(s SignalType) {
	e.signalSetting = s
	e.syncSignalType()
}

// SignalType reports the configured content hint, including SignalAuto.
func (e *Encoder) SignalType() SignalType {
	return e.signalSetting
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
	if maxBW := publicToCeltFramingBW(e.maxBandwidth); maxBW < bw {
		bw = maxBW
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
	case framing.BandwidthMediumband, framing.BandwidthWideband:
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
	// Reapply the effective hint below so switching frame geometries cannot
	// expose stale internal policy after reset.
	signalHint := e.effectiveSignalType()
	for _, enc := range e.celtEncoders {
		if enc != nil {
			enc.Reset()
		}
	}
	if e.silkEncoder != nil {
		e.silkEncoder.Reset()
	}
	if e.analysis != nil {
		e.analysis.Reset()
	}
	e.hybridStereoWidthQ14 = 1 << 14
	e.prevHBGain = 1
	e.stereoWidth = stereoWidthState{}
	e.libopusBandwidth = -1
	e.libopusMode = framing.ModeHybrid
	if e.inputResampler != nil {
		e.inputResampler.Reset()
	}
	for _, r := range e.silkInputResamplers {
		r.Reset()
	}
	e.lastDetectedBW = -1
	e.prevMode = -1
	e.silkFramesCoded = false
	// OPUS_RESET_STATE also clears stream_channels (back to the input
	// channel count), prev_channels, silk_bw_switch and nonfinal_frame, and
	// silk_InitEncoder clears nPrevChannelsInternal; the CELT prefill tail
	// comes from the cleared delay buffer.
	e.streamChannels = e.channels
	e.prevStreamChannels = 0
	e.silkBwSwitch = false
	e.silkOpusCanSwitch = false
	e.nonfinalFrame = false
	e.silkPrevChannels = 0
	clear(e.celtPrefillTail)
	e.streamStarted = false
	e.peakSignalEnergy = 0
	e.nbNoActivityMsQ1 = 0
	e.autoBandwidth = 0
	e.lastFinalRange = 0
	e.inDTX = false
	clear(e.delayBuffer)
	e.hpMem = [4]float32{}
	e.variableHPSmth2Q15 = variableHPSmth2Initial()
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
			enc.SetDTX(e.celtDTX())
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
	// lastFrameConcealed is set while the last frame of the packet being
	// decoded was a frame of at most one byte, concealed in the previous
	// mode: opus_decode_frame then stores that mode (or nothing, before any
	// packet) as prev_mode, not the packet's TOC mode.
	lastFrameConcealed bool
	// lossConfig and lossChannels are the TOC config and coded channels of
	// the last frame decoded from data. Concealment continues the SILK and
	// CELT decoders that frame used (libopus keeps one state of each), while
	// lastPacketConfig follows every packet's TOC (st->mode, st->bandwidth).
	lossConfig   int
	lossChannels int
	// plcFrameSize is st->frame_size: the frame size of the last packet
	// (Fs/400 before any and after a reset). Each concealment call is cut to
	// it before the 20 / 10 / 5 ms rounding.
	plcFrameSize int
	// softclipMem is the soft-clip memory of the int16 decode (softclip_mem).
	softclipMem [2]float32
	// pcmScratch backs the PCM a packet decode returns internally, and
	// softclipScratch the int16 soft clip; both are reused between calls
	// (DecodeFloat copies its result out).
	pcmScratch      []float64
	softclipScratch []float32
	// SILK resampling scratch: resampleSILK returns silkResampled, valid
	// until its next call.
	silkLin, silkRin   []int16
	silkOutL, silkOutR []int16
	silkResampled      []float64

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
	// internal rate changes (libopus silk_decoder_set_fs). silkSMid is
	// sStereo.sMid, the two mid samples silk_Decode carries between frames:
	// the mono path delays its output through it and a stereo stream's
	// mid/side conversion starts from it; silkStereoDec is the stereo
	// decoder of the stream being decoded.
	silkRS        [2]*silk.Resampler
	silkRSInKHz   [2]int // current internal rate (kHz) per channel; 0 = uninitialized
	silkSMid      [2]int16
	silkStereoDec *silk.Decoder
	// silkActiveRi is the SILK internal rate index of the last decoded
	// SILK layer (-1 before any): libopus has one SILK decoder state, so a
	// rate change hands the state to the new rate's decoders.
	silkActiveRi       int
	prevSilkInternalCh int // previous packet's SILK internal channel count (0 = none yet)

	// Resampler for non-48kHz CELT output rates
	celtResampler *resampler.Resampler // 48kHz -> outRate

	frameSize          int // frame size in samples at sampleRate
	internalFrameSize  int // always 960 (20ms at 48kHz)
	lastPacketDuration int // samples per channel decoded by the last packet
	lastPacketConfig   int // TOC config of the last successfully decoded packet
	lastPacketChannels int // coded channels of the last successfully decoded packet
	lastFinalRange     uint32
	lastPitch          int // in 48 kHz samples, like OPUS_GET_PITCH
	// silkPitch48 is DecControl.prevPitchLag (48 kHz samples), which
	// OPUS_GET_PITCH reports outside CELT-only mode and which survives a
	// reset.
	silkPitch48            int
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
		lossConfig:        -1,
		plcFrameSize:      sampleRate / 400,
		silkActiveRi:      -1,
	}

	// Create CELT decoders for all 4 bandwidths × 4 frame sizes × 2 channel counts.
	// CELT always runs at 48kHz internally; bandwidth only controls numBands.
	for bw := 0; bw < 4; bw++ {
		numBands := celtBWNumBands[bw]
		for lm := 0; lm < 4; lm++ {
			fs := celtLMFrameSize[lm]
			for ch := 1; ch <= 2; ch++ {
				d, err := celt.NewDecoderEx(fs, 48000, numBands, ch)
				if err == nil {
					d.SetDownsample(48000 / sampleRate)
				}
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

	// CELT output below 48 kHz: the CELT decoders clear the spectrum above
	// the output Nyquist and decimate (libopus st->downsample); no resampler.

	// celt_decoder_init: a mono decoder disables intensity-stereo phase
	// inversion (a stereo stream is downmixed).
	if channels == 1 {
		dec.SetPhaseInversionDisabled(true)
	}

	return dec, nil
}

// Decode decodes an Opus packet into interleaved int16 PCM and returns the
// number of samples per channel. pcm must have room for the packet duration
// times Channels() values; a short buffer returns ErrBufferTooSmall.
// An empty packet conceals len(pcm)/Channels() samples, like DecodePLC.
func (d *Decoder) Decode(data []byte, pcm []int16) (int, error) {
	if len(data) == 0 {
		// Like opus_decode with no packet: conceal a buffer's worth.
		return d.DecodePLC(pcm, len(pcm)/d.channels)
	}
	required, err := d.packetOutputSamples(data)
	if err != nil {
		return 0, err
	}
	if len(pcm) < required {
		return 0, fmt.Errorf("%w: got %d samples, need %d", ErrBufferTooSmall, len(pcm), required)
	}

	clipped, err := d.decodeSoftClipped32(data)
	if err != nil {
		return 0, err
	}

	n := len(clipped)
	float32ToInt16(pcm[:n], clipped)

	samplesPerChannel := n / d.channels
	return samplesPerChannel, nil
}

// Decode24 decodes an Opus packet to interleaved signed 24-bit PCM stored in
// int32 values, converted like libopus RES2INT24 (rounded to nearest even and
// not clamped, so positive decoder gain can exceed the 24-bit range). pcm must have room
// for the packet duration times Channels() values; the return value is samples
// per channel, and a short buffer returns ErrBufferTooSmall.
// An empty packet conceals len(pcm)/Channels() samples, like DecodePLC.
func (d *Decoder) Decode24(data []byte, pcm []int32) (int, error) {
	if len(data) == 0 {
		// Like opus_decode24 with no packet: conceal a buffer's worth.
		return d.DecodePLC24(pcm, len(pcm)/d.channels)
	}
	required, err := d.packetOutputSamples(data)
	if err != nil {
		return 0, err
	}
	if len(pcm) < required {
		return 0, fmt.Errorf("%w: got %d samples, need %d", ErrBufferTooSmall, len(pcm), required)
	}

	floatPCM, err := d.decodeFloatShared(data)
	if err != nil {
		return 0, err
	}
	for i, sample := range floatPCM {
		pcm[i] = res2Int24(sample)
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

	// A padded code-3 packet: opus_repacketizer_out_range_impl spends
	// pad_amount = P bytes on the padding-count run and the padding data,
	// writing nb_255s = (P-1)/255 bytes of 255 and then P-255*nb_255s-1, so
	// every P >= 1 is representable (the run ends in 0 when P = 255k+1).
	if requiredPadding := targetBytes - 1 - len(code3); requiredPadding >= 1 {
		run, padBytes := libopusPaddingRun(requiredPadding)
		padded, err := packOpusFramesCode3Run(frames, vbr, run, padBytes)
		if err != nil {
			return nil, 0, err
		}
		if 1+len(padded) == targetBytes {
			return padded, 3, nil
		}
	}
	return compact, code, nil
}

// libopusPaddingRun returns the padding-count run and the padding data size
// opus_repacketizer_out_range_impl writes for pad_amount bytes of padding.
func libopusPaddingRun(padAmount int) (run []byte, padBytes int) {
	nb255s := (padAmount - 1) / 255
	run = make([]byte, nb255s+1)
	for i := 0; i < nb255s; i++ {
		run[i] = 255
	}
	run[nb255s] = byte(padAmount - 255*nb255s - 1)
	return run, padAmount - len(run)
}

func packOpusFramesCode3(frames [][]byte, vbr, padding bool, padBytes int) ([]byte, error) {
	var run []byte
	if padding && padBytes >= 0 {
		run = encodePaddingCount(padBytes)
	}
	return packOpusFramesCode3Run(frames, vbr, run, padBytes)
}

// packOpusFramesCode3Run is packOpusFramesCode3 with an explicit
// padding-count run (nil for no padding) covering padBytes data bytes.
func packOpusFramesCode3Run(frames [][]byte, vbr bool, run []byte, padBytes int) ([]byte, error) {
	padding := run != nil
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

	capacity := 1 + 2*n + padBytes + len(run)
	out := make([]byte, 0, capacity)
	flags := byte(n)
	if useVBR {
		flags |= 0x80
	}
	if padding {
		flags |= 0x40
	}
	out = append(out, flags)
	out = append(out, run...)
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
	return splitOpusFramesInto(nil, payload, countCode)
}

// maxPacketFrames is the RFC 6716 code 3 frame-count limit.
const maxPacketFrames = 48

// splitOpusFramesInto is splitOpusFrames writing the frame slices into
// dst[:0]; a caller passing a [maxPacketFrames][]byte array avoids the
// allocation.
func splitOpusFramesInto(dst [][]byte, payload []byte, countCode int) ([][]byte, error) {
	dst = dst[:0]
	switch countCode {
	case 0:
		return append(dst, payload), nil

	case 1:
		// Two frames of equal size
		if len(payload)%2 != 0 {
			return nil, fmt.Errorf("code 1: odd payload length %d", len(payload))
		}
		half := len(payload) / 2
		return append(dst, payload[:half], payload[half:]), nil

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
		return append(dst, payload[:n1], payload[n1:]), nil

	case 3:
		// Multiple frames: see RFC 6716 §3.2.5
		if len(payload) < 1 {
			return nil, fmt.Errorf("code 3: empty payload")
		}
		frameCount := int(payload[0] & 0x3F) // lower 6 bits = frame count
		vbr := (payload[0] & 0x80) != 0      // VBR flag
		padding := (payload[0] & 0x40) != 0  // padding flag
		payload = payload[1:]

		if frameCount == 0 || frameCount > maxPacketFrames {
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
			var sizeBuf [maxPacketFrames]int
			sizes := sizeBuf[:frameCount]
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
			offset := 0
			for i, n := range sizes {
				if offset+n > len(payload) {
					return nil, fmt.Errorf("code 3 VBR: frame %d length %d > remaining %d", i, n, len(payload)-offset)
				}
				dst = append(dst, payload[offset:offset+n])
				offset += n
			}
			return dst, nil
		}
		// CBR: all frames equal size
		if len(payload)%frameCount != 0 {
			return nil, fmt.Errorf("code 3 CBR: payload %d not divisible by frameCount %d", len(payload), frameCount)
		}
		frameSize := len(payload) / frameCount
		for i := 0; i < frameCount; i++ {
			dst = append(dst, payload[i*frameSize:(i+1)*frameSize])
		}
		return dst, nil

	default:
		return nil, fmt.Errorf("unknown count code %d", countCode)
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
	pcm, err := d.decodeFloatShared(data)
	if err != nil {
		return nil, err
	}
	return append([]float64(nil), pcm...), nil
}

// decodeFloatShared is DecodeFloat returning the decoder's reusable output
// buffer, valid until the next decode call, for callers that convert the
// samples straight away.
func (d *Decoder) decodeFloatShared(data []byte) ([]float64, error) {
	pcm, err := d.decodePacketFloat(data)
	if err != nil {
		return nil, err
	}
	return d.finishOutput(pcm, false), nil
}

// decodeSoftClipped decodes a packet for the int16 API.
func (d *Decoder) decodeSoftClipped(data []byte) ([]float64, error) {
	pcm, err := d.decodePacketFloat(data)
	if err != nil {
		return nil, err
	}
	return d.finishOutput(pcm, true), nil
}

// decodeSoftClipped32 is decodeSoftClipped returning the soft-clipped float32
// samples (decoder scratch, valid until the next call) without converting
// them back to float64.
func (d *Decoder) decodeSoftClipped32(data []byte) ([]float32, error) {
	pcm, err := d.decodePacketFloat(data)
	if err != nil {
		return nil, err
	}
	if cap(d.softclipScratch) < len(pcm) {
		d.softclipScratch = make([]float32, len(pcm))
	}
	f := d.softclipScratch[:len(pcm)]
	for i, v := range pcm {
		f[i] = float32(v)
	}
	_ = SoftClipFloat32(f, d.channels, d.softclipMem[:d.channels])
	return f, nil
}

// finishOutput ends a successful opus_decode_native call. The int16 API of a
// float build soft-clips the output (opus_pcm_soft_clip) with the decoder's
// clipping memory; the float and 24-bit APIs do not, and restart the memory.
func (d *Decoder) finishOutput(pcm []float64, softClip bool) []float64 {
	if !softClip {
		d.softclipMem = [2]float32{}
		return pcm
	}
	if cap(d.softclipScratch) < len(pcm) {
		d.softclipScratch = make([]float32, len(pcm))
	}
	f := d.softclipScratch[:len(pcm)]
	for i, v := range pcm {
		f[i] = float32(v)
	}
	_ = SoftClipFloat32(f, d.channels, d.softclipMem[:d.channels])
	for i, v := range f {
		pcm[i] = float64(v)
	}
	return pcm
}

// res2Int24 is libopus RES2INT24 for a float build: the sample scaled by
// 2^23 in float32 and rounded to nearest even (lrintf), without a clamp.
func res2Int24(v float64) int32 {
	return int32(math.RoundToEven(float64(float32(v) * 8388608)))
}

// decodePacketFloat decodes one packet (opus_decode_native without the
// output conversion).
func (d *Decoder) decodePacketFloat(data []byte) ([]float64, error) {
	info, err := inspectPacket(data, d.sampleRate)
	if err != nil {
		return nil, err
	}
	// st->frame_size is updated before the packet's frames are decoded (a
	// mode-switch concealment inside the packet is cut to it).
	d.plcFrameSize = info.samplesPerFrame

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
				if !d.lastFrameConcealed {
					d.prevMode, d.prevRedundancy = framing.ModeHybrid, trailingRedundancy
				}
				d.applyGain(out)
			}
			return out, err
		}
		// SILK-only mode.
		out, trailingRedundancy, err := d.decodeSILKPacket(payload, countCode, config, pktChannels)
		if err == nil {
			d.lastPacketDuration = duration
			d.lastPacketConfig = config
			d.lastPacketChannels = pktChannels
			if !d.lastFrameConcealed {
				d.prevMode, d.prevRedundancy = framing.ModeSILKOnly, trailingRedundancy
			}
			d.applyGain(out)
		}
		return out, err
	}

	// CELT-only mode: split payload into individual frame payloads and decode each
	var frameBuf [maxPacketFrames][]byte
	frames, err := splitOpusFramesInto(frameBuf[:0], payload, countCode)
	if err != nil {
		return nil, fmt.Errorf("failed to split frames: %w", err)
	}

	pktChannels := 1
	if stereo {
		pktChannels = 2
	}

	// Select the CELT decoder matching this packet's bandwidth and frame
	// size. Like libopus's celt_dec it has the output channels and decodes
	// the packet's coded channels (CELT_SET_CHANNELS).
	bwIdx := celtConfigBWIdx(config)
	lmIdx := celtConfigLMIdx(config)
	chIdx := d.channels - 1
	activeCeltDec := d.celtDecoders[bwIdx][lmIdx][chIdx]
	if activeCeltDec == nil {
		// Fallback: mono decoder
		activeCeltDec = d.celtDecoders[bwIdx][lmIdx][0]
	}
	if activeCeltDec == nil {
		return nil, fmt.Errorf("no CELT decoder for config=%d (bw=%d lm=%d ch=%d)", config, bwIdx, lmIdx, pktChannels)
	}

	allPCM := d.pcmScratch[:0]
	// opus_decode_native overwrites rangeFinal for each constituent frame, so
	// OPUS_GET_FINAL_RANGE reports the last frame rather than an XOR across the
	// frames of one single-stream packet.
	var rangeFinal uint32
	targetLen := celtFrameSamples(config, d.sampleRate) * d.channels
	d.lastFrameConcealed = false
	for fi, frame := range frames {
		if fi > 0 && !d.lastFrameConcealed {
			d.prevMode, d.prevRedundancy = framing.ModeCELTOnly, false
		}
		if len(frame) <= 1 {
			// opus_decode_frame: a frame of at most one byte is a loss,
			// concealed in the previous mode, with a zero final range.
			d.lastFrameConcealed = true
			pcm, err := d.decodePLCFloat(targetLen / d.channels)
			if err != nil {
				return nil, err
			}
			allPCM = append(allPCM, padOrTrim(pcm, targetLen)...)
			rangeFinal = 0
			continue
		}
		// A switch from SILK/hybrid without a trailing redundant frame
		// conceals 5 ms in the old mode and discards the CELT state.
		var trans []float64
		switchReset := d.prevMode >= 0 && d.prevMode != framing.ModeCELTOnly && !d.prevRedundancy
		if switchReset {
			trans = d.transitionPLC(targetLen / d.channels)
			activeCeltDec.Reset()
		} else if d.lastCeltDec != nil && d.lastCeltDec != activeCeltDec {
			activeCeltDec.CopyStateFrom(d.lastCeltDec)
		}
		activeCeltDec.SetStreamChannels(pktChannels)
		activeCeltDec.SetAccumulate(false)
		pcm, err := activeCeltDec.Decode(frame)
		overrun := errors.Is(err, celt.ErrRangeOverrun)
		if err != nil && !overrun {
			return nil, fmt.Errorf("CELT decoding failed: %w", err)
		}
		if overrun {
			// libopus keeps the frame's decoder state, range and mode, but
			// fails the whole call (OPUS_INTERNAL_ERROR).
			d.lastCeltDec = activeCeltDec
			d.lossConfig, d.lossChannels = config, pktChannels
			d.lastFinalRange = activeCeltDec.LastFinalRange()
			d.lastFrameConcealed = false
			d.lastPitch = activeCeltDec.Pitch()
			d.prevMode, d.prevRedundancy = framing.ModeCELTOnly, false
			return nil, fmt.Errorf("%w: %w", ErrInternalError, err)
		}
		d.lastCeltDec = activeCeltDec
		d.lastFrameConcealed = false
		d.lossConfig, d.lossChannels = config, pktChannels
		rangeFinal = activeCeltDec.LastFinalRange()
		d.lastPitch = activeCeltDec.Pitch()

		// Resample from 48kHz to output sample rate if needed
		if d.celtResampler != nil {
			pcm = d.celtResampler.Process(pcm)
		}
		pcm = padOrTrim(pcm, targetLen)
		if trans != nil {
			d.applyTransition(pcm, trans)
		}
		allPCM = append(allPCM, pcm...)
	}

	d.lastPacketDuration = duration
	d.lastPacketConfig = config
	d.lastPacketChannels = pktChannels
	d.lastFinalRange = rangeFinal
	if !d.lastFrameConcealed {
		d.prevMode, d.prevRedundancy = framing.ModeCELTOnly, false
	}
	d.applyGain(allPCM)
	d.pcmScratch = allPCM
	return allPCM, nil
}

func (d *Decoder) applyGain(pcm []float64) {
	if d.gainQ8 == 0 {
		return
	}
	// opus_decode_frame (float build):
	// gain = celt_exp2(6.48814081e-4f * decode_gain), with celt_exp2(x) =
	// (float)exp(0.6931471805599453094*x), applied in float32.
	g := float32(6.48814081e-4) * float32(d.gainQ8)
	scale := float32(math.Exp(0.6931471805599453094 * float64(g)))
	for i := range pcm {
		pcm[i] = float64(float32(pcm[i]) * scale)
	}
}

// DecodeFloat32 decodes an Opus packet to caller-owned interleaved float32 PCM.
// Positive decoder gain can produce values outside the nominal [-1, 1] scale.
func (d *Decoder) DecodeFloat32(data []byte) ([]float32, error) {
	pcm, err := d.decodeFloatShared(data)
	if err != nil {
		return nil, err
	}
	out := make([]float32, len(pcm))
	for i := range pcm {
		out[i] = float32(pcm[i])
	}
	return out, nil
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
func (d *Decoder) decodeSILKPacket(payload []byte, countCode, config, pktChannels int) ([]float64, bool, error) {
	if out, ok, err := d.concealOnlyPacket(payload, countCode, d.sampleRate*silkConfigFrameMs(config)/1000); ok || err != nil {
		return out, false, err
	}
	rateKHz := silkConfigRateKHz(config)
	ri := silkRateIdx(rateKHz)
	ci := pktChannels - 1
	d.resetSILKAfterCELT()
	d.switchSILKRate(ri)

	// Determine Opus frame duration and SILK sub-frames per Opus frame
	frameDurationMs := silkConfigFrameMs(config)
	// subframesPerOpusFrame: how many 20ms (or 10ms) SILK frames are in one Opus frame
	subframesPerOpusFrame := silkSubframesPerOpusFrame(config)

	if d.silkDecoders[ri][ci] == nil {
		return nil, false, fmt.Errorf("SILK decoder not initialized for rate=%dkHz ch=%d", rateKHz, pktChannels)
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

	var frameBuf [maxPacketFrames][]byte
	silkStreams, err := splitOpusFramesInto(frameBuf[:0], payload, countCode)
	if err != nil {
		return nil, false, err
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

	allPCM := d.pcmScratch[:0]
	// A single-stream packet reports its last constituent frame range. XOR is
	// reserved for combining elementary streams in the multistream API.
	var rangeFinal uint32
	trailingRedundancy := false
	redundancyEndBand := celtEndBandForFramingBW(config / 4)
	// prevModeRedundancy is (prev_mode != MODE_SILK_ONLY || prev_redundancy)
	// for the frame being decoded: a leading CELT->SILK redundant frame is
	// only cross-faded in after a CELT/hybrid frame or a trailing redundant
	// frame (the SILK bandwidth switch).
	prevModeRedundancy := d.prevMode != framing.ModeSILKOnly || d.prevRedundancy
	frameMode, frameRedundancy := d.prevMode, d.prevRedundancy
	d.lastFrameConcealed = false
	decodedAny := false
	for si, stream := range silkStreams {
		if si > 0 {
			frameMode, frameRedundancy = framing.ModeSILKOnly, trailingRedundancy
			if d.lastFrameConcealed {
				// The concealment's mode (prev_mode after the lost frame).
				frameMode, frameRedundancy = d.prevMode, d.prevRedundancy
			}
			prevModeRedundancy = frameMode != framing.ModeSILKOnly || frameRedundancy
		}
		// opus_decode_native replaces prev_redundancy for every constituent
		// frame, so only a trailing redundancy frame on the final stream remains.
		trailingRedundancy = false
		if len(stream) < 2 {
			// opus_decode_frame: a frame of at most one byte is a loss,
			// concealed in the previous mode, with a zero final range.
			d.prevMode, d.prevRedundancy = frameMode, frameRedundancy
			d.lastFrameConcealed = true
			pcm, err := d.decodePLCFloat(samplesPerStream / d.channels)
			if err != nil {
				return nil, false, err
			}
			allPCM = append(allPCM, padOrTrim(pcm, samplesPerStream)...)
			rangeFinal = 0
			continue
		}
		dec := entcode.NewDecoder(stream)
		if dec.Error() != nil {
			allPCM = append(allPCM, make([]float64, samplesPerStream)...)
			continue
		}

		// Decode SILK sub-frames from this stream, retaining the range decoder so
		// the mandatory SILK-only redundancy direction bit can be read when the
		// packet has enough trailing data for a 5 ms CELT redundancy frame.
		d.prepareSILKStream(info.dec, pktChannels)
		pcm, err := info.dec.DecodeMultiWithDecoderReuse(dec, nSilkFramesPerStream)
		if err != nil {
			allPCM = append(allPCM, make([]float64, samplesPerStream)...)
			continue
		}

		redundancy := false
		redundancyBytes := 0
		celtToSilk := false
		sanityFailed := false
		if dec.ECTell()+17 <= len(stream)*8 {
			redundancy = true
			celtToSilk = dec.DecodeBitLogp(1)
			redundancyBytes = len(stream) - ((dec.ECTell() + 7) >> 3)
			mainBytes := len(stream) - redundancyBytes
			if redundancyBytes < 2 || mainBytes < 0 || mainBytes*8 < dec.ECTell() {
				// libopus's sanity check sets len = 0, so the frame's final
				// range becomes 0.
				redundancy = false
				redundancyBytes = 0
				celtToSilk = false
				sanityFailed = true
			} else {
				dec.ShrinkStorage(redundancyBytes)
			}
		}
		trailingRedundancy = redundancy && !celtToSilk

		// Resample from internal rate to the output rate using the persistent
		// per-channel bit-exact resamplers, producing d.channels-interleaved PCM.
		// stereoToMono applies only to the very first SILK frame after the
		// transition (si == 0; the method gates further on f == 0).
		pcm = d.resampleSILK(pcm, nSilkFramesPerStream, pktChannels, stereoToMono && si == 0)

		// Pad or trim to exact expected length
		pcm = padOrTrim(pcm, samplesPerStream)
		// A switch from CELT without redundancy conceals 5 ms in the old mode.
		if frameMode == framing.ModeCELTOnly && !redundancy {
			if trans := d.transitionPLC(samplesPerStream / d.channels); trans != nil {
				d.applyTransition(pcm, trans)
			}
		}
		// opus_decode_frame decodes a CELT->SILK redundant frame first, from
		// (and advancing) the persistent CELT state, before the hybrid ->
		// SILK silence fade-out decodes from that state.
		var redundantRange uint32
		var leadingRedundancy []float64
		if redundancy && celtToSilk && redundancyBytes >= 2 {
			redPCM, redRange, redDec := d.decodeRedundancy(
				stream[len(stream)-redundancyBytes:], pktChannels, redundancyEndBand, true,
			)
			if redDec != nil {
				redundantRange = redRange
				d.lastCeltDec = redDec
				leadingRedundancy = redPCM
			}
		}
		// For hybrid -> SILK transitions the CELT MDCT fades out through a
		// decoded silence frame.
		if frameMode == framing.ModeHybrid && !(redundancy && celtToSilk && frameRedundancy) {
			d.celtSilenceFade(pcm, pktChannels, redundancyEndBand)
		}
		if leadingRedundancy != nil && prevModeRedundancy {
			d.crossfadeLeadingRedundancy(pcm, leadingRedundancy)
		}
		if redundancy && !celtToSilk && redundancyBytes >= 2 {
			redPCM, redRange, redDec := d.decodeRedundancy(
				stream[len(stream)-redundancyBytes:], pktChannels, redundancyEndBand, false,
			)
			if redDec != nil {
				redundantRange = redRange
				d.lastCeltDec = redDec
				d.crossfadeRedundancy(pcm, redPCM, samplesPerStream)
			}
		}
		d.lastFrameConcealed = false
		d.lossConfig, d.lossChannels = config, pktChannels
		decodedAny = true
		allPCM = append(allPCM, pcm...)
		rangeFinal = dec.GetRng() ^ redundantRange
		if sanityFailed {
			rangeFinal = 0
		}
	}
	d.lastFinalRange = rangeFinal
	if !decodedAny {
		// Only concealed frames: the SILK configuration is unchanged.
		d.pcmScratch = allPCM
		return allPCM, trailingRedundancy, nil
	}
	d.lastPitch = info.dec.Pitch() * 48 / rateKHz
	d.silkPitch48 = d.lastPitch
	d.prevSilkInternalCh = pktChannels

	if pktChannels == 1 && stereoPeer != nil && stereoPeer.dec != nil {
		stereoPeer.dec.CopyPrimaryStateFrom(info.dec)
	} else if pktChannels == 2 && monoPeer != nil && monoPeer.dec != nil {
		monoPeer.dec.CopyPrimaryStateFrom(info.dec)
	}

	d.pcmScratch = allPCM
	return allPCM, trailingRedundancy, nil
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
		w := smoothFadeWeight(win[i*inc])
		for c := 0; c < ch; c++ {
			o := base + i*ch + c
			r := (f25+i)*ch + c
			out[o] = smoothFadeSample(w, out[o], red[r])
		}
	}
}

// smoothFadeWeight is smooth_fade's squared window, in float32.
func smoothFadeWeight(window float64) float32 {
	w := float32(window)
	// The conversion keeps the square rounded when the caller's 1 - w is
	// inlined next to it (no fused multiply-subtract).
	return float32(w * w)
}

// smoothFadeSample is one smooth_fade output in float32:
// w*in2 + (1-w)*in1, each product rounded (no fused multiply-add).
func smoothFadeSample(w float32, in1, in2 float64) float64 {
	return float64(float32(w*float32(in2)) + float32((1-w)*float32(in1)))
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
		w := smoothFadeWeight(win[i*inc])
		for c := 0; c < ch; c++ {
			o := (f25+i)*ch + c
			out[o] = smoothFadeSample(w, red[o], out[o])
		}
	}
}

// transitionPLC is opus_decode_frame's concealment on a switch between CELT
// and SILK/hybrid without a redundant frame: min(F5, frameSamples) samples
// decoded as a loss in the previous mode, faded in by applyTransition.
func (d *Decoder) transitionPLC(frameSamples int) []float64 {
	pcm, err := d.decodePLCFloat(min(d.sampleRate/200, frameSamples))
	if err != nil {
		return nil
	}
	return pcm
}

// applyTransition is opus_decode_frame's transition fade: the first 2.5 ms
// of a frame of at least 5 ms are the concealed audio, whose second half is
// faded into the frame over the next 2.5 ms; a shorter frame is faded from
// the start (libopus smooth_fade, window squared).
func (d *Decoder) applyTransition(out, trans []float64) {
	ch := d.channels
	f25 := d.sampleRate / 400
	if len(trans) < f25*ch || len(out) < f25*ch {
		return
	}
	in1, off := trans, 0
	if len(out) >= 2*f25*ch && len(trans) >= 2*f25*ch {
		copy(out[:f25*ch], trans[:f25*ch])
		in1, off = trans[f25*ch:], f25*ch
	}
	win := celt.OverlapWindow48()
	inc := max(48000/d.sampleRate, 1)
	for i := 0; i < f25; i++ {
		w := smoothFadeWeight(win[i*inc])
		for c := 0; c < ch; c++ {
			o := i*ch + c
			out[off+o] = smoothFadeSample(w, in1[o], out[off+o])
		}
	}
}

func (d *Decoder) decodeRedundancy(frame []byte, pktChannels, endBand int, carryState bool) ([]float64, uint32, *celt.Decoder) {
	if len(frame) < 2 {
		return nil, 0, nil
	}
	actualCh := d.channels
	redDec, err := celt.NewDecoderEx(celt.FrameSize5ms, 48000, endBand, actualCh)
	if err == nil {
		redDec.SetDownsample(48000 / d.sampleRate)
		redDec.SetStreamChannels(pktChannels)
	}
	if err != nil {
		return nil, 0, nil
	}
	redDec.SetPhaseInversionDisabled(d.phaseInversionDisabled)
	if carryState && d.lastCeltDec != nil {
		redDec.CopyStateFrom(d.lastCeltDec)
	}
	redPCM, err := redDec.Decode(frame)
	if err != nil {
		return nil, 0, nil
	}
	redundantRange := redDec.LastFinalRange()
	if d.celtResampler != nil {
		redPCM = d.celtResampler.Process(redPCM)
	}
	redPCM = adjustChannels(redPCM, actualCh, d.channels)
	redPCM = padOrTrim(redPCM, (d.sampleRate/200)*d.channels)
	return redPCM, redundantRange, redDec
}

func (d *Decoder) decodeHybridPacket(payload []byte, countCode, config, pktChannels int) ([]float64, bool, error) {
	if out, ok, err := d.concealOnlyPacket(payload, countCode, d.sampleRate*silkConfigFrameMs(config)/1000); ok || err != nil {
		return out, false, err
	}
	const rateKHz = 16 // hybrid SILK layer is always wideband
	ri := silkRateIdx(rateKHz)
	ci := pktChannels - 1
	d.resetSILKAfterCELT()
	d.switchSILKRate(ri)
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
	celtActualCh := d.channels
	celtDec := d.celtDecoders[celtBWIdx][celtLMIdx][d.channels-1]
	celtDec.SetStreamChannels(pktChannels)

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
	var frameBuf [maxPacketFrames][]byte
	silkStreams, err := splitOpusFramesInto(frameBuf[:0], payload, countCode)
	if err != nil {
		silkStreams = append(frameBuf[:0], payload)
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

	allPCM := d.pcmScratch[:0]
	// Match opus_decode_native: each packet frame replaces rangeFinal rather
	// than being XORed with preceding frames from the same stream.
	var rangeFinal uint32
	trailingRedundancy := false
	// prevMode / prevRedundancy of the frame being decoded (libopus updates
	// them per constituent frame).
	frameMode, frameRedundancy := d.prevMode, d.prevRedundancy
	d.lastFrameConcealed = false
	decodedAny := false
	for si, stream := range silkStreams {
		if si > 0 {
			frameMode, frameRedundancy = framing.ModeHybrid, trailingRedundancy
			if d.lastFrameConcealed {
				// The concealment's mode (prev_mode after the lost frame).
				frameMode, frameRedundancy = d.prevMode, d.prevRedundancy
			}
		}
		// libopus replaces prev_redundancy for every constituent frame. An
		// unreadable final frame must therefore clear an earlier frame's value.
		trailingRedundancy = false
		if len(stream) <= 1 {
			// opus_decode_frame: a frame of at most one byte is a loss,
			// concealed in the previous mode, with a zero final range.
			d.prevMode, d.prevRedundancy = frameMode, frameRedundancy
			d.lastFrameConcealed = true
			pcm, err := d.decodePLCFloat(samplesPerFrame / d.channels)
			if err != nil {
				return nil, false, err
			}
			allPCM = append(allPCM, padOrTrim(pcm, samplesPerFrame)...)
			rangeFinal = 0
			continue
		}
		// One shared range decoder per Opus frame: SILK reads first, CELT after.
		dec := entcode.NewDecoder(stream)
		if dec.Error() != nil {
			allPCM = append(allPCM, make([]float64, samplesPerFrame)...)
			continue
		}

		// SILK low-band layer (front of the stream), at the 16 kHz internal rate.
		d.prepareSILKStream(info.dec, pktChannels)
		silkPCM, serr := info.dec.DecodeMultiWithDecoderReuse(dec, nSilkFrames)
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
		// celtLost is libopus's sanity-check failure (len = 0): the CELT
		// layer is concealed and the frame's final range is 0.
		celtLost := false
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
				celtLost = true
			} else {
				dec.ShrinkStorage(redundancyBytes)
			}
		}
		// Each constituent Opus frame replaces libopus' prev_redundancy state;
		// after a packed packet only the final frame therefore remains visible.
		trailingRedundancy = redundancy && !celtToSilk

		var leadingRedundancy []float64
		var redundantRange uint32
		if redundancy && celtToSilk && redundancyBytes >= 2 && celtLen+redundancyBytes <= len(stream) {
			// The CELT->SILK redundant frame is decoded first, from (and
			// advancing) the persistent CELT state.
			var redDec *celt.Decoder
			leadingRedundancy, redundantRange, redDec = d.decodeRedundancy(
				stream[celtLen:celtLen+redundancyBytes], pktChannels, celtEnd, true,
			)
			if redDec != nil {
				d.lastCeltDec = redDec
			}
		}

		// A switch from CELT without redundancy conceals 5 ms in the old mode.
		var trans []float64
		if frameMode == framing.ModeCELTOnly && !redundancy {
			trans = d.transitionPLC(samplesPerFrame / d.channels)
		}

		// CELT high-band layer continues from the same range decoder; a mode
		// change discards the previous CELT state unless the previous frame
		// ended with a redundant frame.
		if frameMode >= 0 && frameMode != framing.ModeHybrid && !frameRedundancy {
			celtDec.Reset()
		} else if d.lastCeltDec != nil && d.lastCeltDec != celtDec {
			celtDec.CopyStateFrom(d.lastCeltDec)
		}
		var celtPCM []float64
		var cerr error
		celtDec.SetAccumulate(true)
		if celtLost {
			celtPCM = celtDec.DecodeHybridLoss(celtStart, celtEnd)
		} else {
			celtPCM, cerr = celtDec.DecodeHybrid(dec, celtLen, celtStart, celtEnd)
		}
		overrun := errors.Is(cerr, celt.ErrRangeOverrun)
		if overrun {
			cerr = nil
		}
		if cerr == nil {
			d.lastCeltDec = celtDec
			if d.celtResampler != nil {
				celtPCM = d.celtResampler.Process(celtPCM)
			}
			celtPCM = adjustChannels(celtPCM, celtActualCh, d.channels)
			celtPCM = padOrTrim(celtPCM, samplesPerFrame)
			// Time-domain sum of the two layers. libopus accumulates the CELT
			// layer into the SILK output in float32 (celt deemphasis with
			// accum) without clipping; the decoder gain comes later.
			for i := 0; i < samplesPerFrame; i++ {
				silkOut[i] = float64(float32(silkOut[i]) + float32(celtPCM[i]))
			}
			if len(leadingRedundancy) >= (d.sampleRate/200)*d.channels && (frameMode != framing.ModeSILKOnly || frameRedundancy) {
				d.crossfadeLeadingRedundancy(silkOut, leadingRedundancy)
			}
			if trans != nil {
				d.applyTransition(silkOut, trans)
			}

			// SILK->CELT redundancy: decode the trailing 5 ms (F5=240 @ 48k) CELT
			// frame on a freshly reset decoder, crossfade the last 2.5 ms of this
			// frame, and adopt its state so the next CELT-only packet predicts
			// coarse energy from the right baseline (matches libopus).
			if redundancy && !celtToSilk && redundancyBytes >= 2 && celtLen+redundancyBytes <= len(stream) {
				redPCM, redRange, redDec := d.decodeRedundancy(
					stream[celtLen:celtLen+redundancyBytes], pktChannels, celtEnd, false,
				)
				if redDec != nil {
					redundantRange = redRange
					d.crossfadeRedundancy(silkOut, redPCM, samplesPerFrame)
					// Adopt the redundant frame's CELT state for continuity.
					celtDec.CopyStateFrom(redDec)
				}
			}
		}
		d.lastFrameConcealed = false
		d.lossConfig, d.lossChannels = config, pktChannels
		decodedAny = true
		allPCM = append(allPCM, silkOut...)
		rangeFinal = dec.GetRng() ^ redundantRange
		if celtLost {
			rangeFinal = 0
		}
		if overrun {
			// The frame completes, then libopus fails the whole call.
			d.lastFinalRange = rangeFinal
			d.prevMode, d.prevRedundancy = framing.ModeHybrid, redundancy && !celtToSilk
			d.lastPitch = info.dec.Pitch() * 48 / rateKHz
			d.silkPitch48 = d.lastPitch
			d.prevSilkInternalCh = pktChannels
			return nil, false, fmt.Errorf("%w: %w", ErrInternalError, celt.ErrRangeOverrun)
		}
	}
	d.lastFinalRange = rangeFinal
	if !decodedAny {
		// Only concealed frames: the SILK configuration is unchanged.
		d.pcmScratch = allPCM
		return allPCM, trailingRedundancy, nil
	}
	d.lastPitch = info.dec.Pitch() * 48 / rateKHz
	d.silkPitch48 = d.lastPitch
	d.prevSilkInternalCh = pktChannels

	if pktChannels == 1 && stereoPeer != nil && stereoPeer.dec != nil {
		stereoPeer.dec.CopyPrimaryStateFrom(info.dec)
	} else if pktChannels == 2 && monoPeer != nil && monoPeer.dec != nil {
		monoPeer.dec.CopyPrimaryStateFrom(info.dec)
	}

	d.pcmScratch = allPCM
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

	if pktChannels == 2 && d.channels == 1 {
		// A stereo stream decoded to mono is its mid channel, which goes
		// through the mono path's sMid delay.
		pktChannels = 1
	} else if pktChannels == 2 && d.silkStereoDec != nil {
		// The stereo conversion advanced sMid.
		d.silkSMid = d.silkStereoDec.StereoMid()
	}

	if pktChannels == 2 {
		// Internal stereo: L through channel 0, R through channel 1, per frame.
		perChanFrameLen := (len(pcm) / 2) / nFrames
		if perChanFrameLen < 1 {
			return adjustChannels(pcm, pktChannels, d.channels)
		}
		d.silkLin = growInt16(d.silkLin, perChanFrameLen)
		d.silkRin = growInt16(d.silkRin, perChanFrameLen)
		lin, rin := d.silkLin, d.silkRin
		outL, outR := d.silkOutL[:0], d.silkOutR[:0]
		for f := 0; f < nFrames; f++ {
			base := f * perChanFrameLen * 2
			for i := 0; i < perChanFrameLen; i++ {
				lin[i] = f64ToI16(pcm[base+i*2])
				rin[i] = f64ToI16(pcm[base+i*2+1])
			}
			outL = rs0.ProcessAppend(outL, lin)
			if rs1 != nil {
				outR = rs1.ProcessAppend(outR, rin)
			}
		}
		d.silkOutL, d.silkOutR = outL, outR
		d.silkResampled = interleaveSILKOutInto(d.silkResampled, outL, outR, d.channels)
		return d.silkResampled
	}

	// Internal mono: channel 0 carries the sMid 1-sample delay.
	frameLen := len(pcm) / nFrames
	if frameLen < 1 {
		return adjustChannels(pcm, pktChannels, d.channels)
	}
	d.silkRin = growInt16(d.silkRin, frameLen)
	rin := d.silkRin
	outL, outR := d.silkOutL[:0], d.silkOutR[:0]
	for f := 0; f < nFrames; f++ {
		chunk := pcm[f*frameLen : (f+1)*frameLen]
		rin[0] = d.silkSMid[1]
		for i := 0; i < frameLen-1; i++ {
			rin[i+1] = f64ToI16(chunk[i])
		}
		if frameLen >= 2 {
			d.silkSMid[0] = f64ToI16(chunk[frameLen-2])
		}
		d.silkSMid[1] = f64ToI16(chunk[frameLen-1])
		n0 := len(outL)
		outL = rs0.ProcessAppend(outL, rin)
		lout := outL[n0:]
		if d.channels == 2 {
			// Only the very first SILK frame after a stereo->mono transition
			// resamples the right channel separately; otherwise duplicate L.
			if stereoToMono && f == 0 && rs1 != nil {
				outR = rs1.ProcessAppend(outR, rin)
			} else {
				outR = append(outR, lout...)
			}
		}
	}
	d.silkOutL, d.silkOutR = outL, outR
	d.silkResampled = interleaveSILKOutInto(d.silkResampled, outL, outR, d.channels)
	return d.silkResampled
}

// celtSilenceFade is opus_decode_frame's hybrid -> SILK fade-out: a 2.5 ms
// CELT frame decoded from the silence payload {0xFF, 0xFF} on the
// persistent CELT state (start band 0, the packet's end band) is added to
// the start of the SILK output.
func (d *Decoder) celtSilenceFade(pcm []float64, pktChannels, endBand int) {
	if d.lastCeltDec == nil {
		return
	}
	bw := 3
	for i, n := range celtBWNumBands {
		if n == endBand {
			bw = i
		}
	}
	dec := d.celtDecoders[bw][0][d.channels-1]
	if dec == nil {
		return
	}
	if dec != d.lastCeltDec {
		dec.CopyStateFrom(d.lastCeltDec)
	}
	dec.SetStreamChannels(pktChannels)
	dec.SetAccumulate(true)
	out, err := dec.Decode([]byte{0xFF, 0xFF})
	if err != nil {
		return
	}
	d.lastCeltDec = dec
	for i := 0; i < len(out) && i < len(pcm); i++ {
		pcm[i] = float64(float32(pcm[i]) + float32(out[i]))
	}
}

// concealOnlyPacket decodes a SILK or hybrid packet whose frames all have
// at most one byte: opus_decode_frame conceals each in the previous mode and
// leaves the SILK configuration (internal rate, channels) as it was. It
// returns ok == false, without decoding, when the packet has a frame to
// decode or cannot be split.
func (d *Decoder) concealOnlyPacket(payload []byte, countCode, frameSamples int) ([]float64, bool, error) {
	var frameBuf [maxPacketFrames][]byte
	frames, err := splitOpusFramesInto(frameBuf[:0], payload, countCode)
	if err != nil {
		return nil, false, nil
	}
	for _, f := range frames {
		if len(f) > 1 {
			return nil, false, nil
		}
	}
	out := make([]float64, 0, len(frames)*frameSamples*d.channels)
	for range frames {
		d.lastFrameConcealed = true
		pcm, err := d.decodePLCFloat(frameSamples)
		if err != nil {
			return nil, false, err
		}
		out = append(out, padOrTrim(pcm, frameSamples*d.channels)...)
	}
	d.lastFinalRange = 0
	return out, true, nil
}

// resetSILKAfterCELT is silk_ResetDecoder on a SILK or hybrid frame after a
// CELT-only frame: every SILK decoder, the stereo state and sMid are
// cleared, and the resamplers are reinitialised by the following set_fs.
func (d *Decoder) resetSILKAfterCELT() {
	if d.prevMode != framing.ModeCELTOnly {
		return
	}
	for ri := range d.silkDecoders {
		for _, info := range d.silkDecoders[ri] {
			if info != nil && info.dec != nil {
				info.dec.ResetAfterCELT()
			}
		}
	}
	d.silkRS = [2]*silk.Resampler{}
	d.silkRSInKHz = [2]int{}
	d.silkSMid = [2]int16{}
	d.silkActiveRi = -1
}

// switchSILKRate hands the SILK decoder state to the decoders of internal
// rate index ri when the rate changes (silk_decoder_set_fs).
func (d *Decoder) switchSILKRate(ri int) {
	if d.silkActiveRi >= 0 && d.silkActiveRi != ri {
		ci := max(d.prevSilkInternalCh, 1) - 1
		prev := d.silkDecoders[d.silkActiveRi][ci]
		if prev == nil || prev.dec == nil {
			prev = d.silkDecoders[d.silkActiveRi][0]
		}
		if prev != nil && prev.dec != nil {
			for c := 0; c < 2; c++ {
				if n := d.silkDecoders[ri][c]; n != nil && n.dec != nil {
					n.dec.AdoptAfterRateSwitch(prev.dec)
				}
			}
		}
	}
	d.silkActiveRi = ri
}

// prepareSILKStream hands silk_Decode's shared stereo state to the SILK
// decoder about to decode a stream: sMid, the output channel count, and
// the reset of the stereo prediction on a switch from a mono stream to a
// stereo stream with stereo output.
func (d *Decoder) prepareSILKStream(dec *silk.Decoder, pktChannels int) {
	if pktChannels != 2 {
		d.silkStereoDec = nil
		return
	}
	d.silkStereoDec = dec
	dec.SetStereoMid(d.silkSMid)
	dec.SetAPIMono(d.channels == 1)
	if d.prevSilkInternalCh == 1 {
		dec.InitSideChannel()
		if d.channels == 2 {
			dec.ResetStereoPrediction()
		}
		// Once per switch (psDec->nChannelsInternal is now 2).
		d.prevSilkInternalCh = 2
	}
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
	return interleaveSILKOutInto(nil, l, r, channels)
}

// interleaveSILKOutInto is interleaveSILKOut reusing dst's capacity.
func interleaveSILKOutInto(dst []float64, l, r []int16, channels int) []float64 {
	if channels < 2 {
		out := growFloat64s(dst, len(l))
		for i, v := range l {
			out[i] = float64(v) / 32768.0
		}
		return out
	}
	if len(r) != len(l) {
		r = l
	}
	out := growFloat64s(dst, len(l)*2)
	for i := range l {
		out[i*2] = float64(l[i]) / 32768.0
		out[i*2+1] = float64(r[i]) / 32768.0
	}
	return out
}

// DecodePLC performs packet-loss concealment for frameSize samples per channel.
// frameSize may be any positive multiple of 2.5 ms; like opus_decode_native,
// a length beyond the last packet's frame size is concealed in steps of at
// most that size. Before the first successful packet (and after Reset),
// concealment returns zero samples.
func (d *Decoder) DecodePLC(pcm []int16, frameSize int) (int, error) {
	if !isValidLossFrameSize(frameSize, d.sampleRate) {
		return 0, fmt.Errorf("%w: %w: frameSize %d at %d Hz", ErrBadArg, ErrUnsupportedFrameSize, frameSize, d.sampleRate)
	}
	required := frameSize * d.channels
	if len(pcm) < required {
		return 0, fmt.Errorf("%w: got %d samples, need %d", ErrBufferTooSmall, len(pcm), required)
	}
	floatPCM, err := d.DecodePLCFloat(frameSize)
	if err != nil {
		return 0, err
	}
	floatToInt16(pcm[:required], floatPCM)
	return frameSize, nil
}

// DecodePLC24 performs packet-loss concealment to interleaved signed 24-bit
// PCM stored in int32 values. frameSize follows DecodePLC semantics.
func (d *Decoder) DecodePLC24(pcm []int32, frameSize int) (int, error) {
	if !isValidLossFrameSize(frameSize, d.sampleRate) {
		return 0, fmt.Errorf("%w: %w: frameSize %d at %d Hz", ErrBadArg, ErrUnsupportedFrameSize, frameSize, d.sampleRate)
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
//
// Like opus_decode_native, concealment is never soft-clipped and leaves the
// soft-clip memory of the int16 API untouched.
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
		return fmt.Errorf("%w: %w: frameSize %d at %d Hz", ErrBadArg, ErrUnsupportedFrameSize, frameSize, d.sampleRate)
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
	if d.lossConfig < 0 || d.lossChannels < 1 {
		return fmt.Errorf("%w: missing SILK decoder history", ErrInvalidState)
	}
	rateKHz := silkConfigRateKHz(d.lossConfig)
	info := d.silkDecoders[silkRateIdx(rateKHz)][d.lossChannels-1]
	if info == nil || info.dec == nil {
		return fmt.Errorf("%w: SILK decoder for %d kHz and %d channels", ErrInvalidState, rateKHz, d.lossChannels)
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
			chunk := lossDecodeChunk(min(remaining, d.plcFrameSize), d.sampleRate, lossMode)
			frame, err := d.decodeCELTPLCFrame(chunk, false)
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
	if d.lossConfig < 0 || d.lossChannels < 1 {
		return nil, fmt.Errorf("%w: missing SILK decoder history", ErrInvalidState)
	}

	rateKHz := silkConfigRateKHz(d.lossConfig)
	ri := silkRateIdx(rateKHz)
	ci := d.lossChannels - 1
	info := d.silkDecoders[ri][ci]
	if info == nil || info.dec == nil {
		return nil, fmt.Errorf("%w: SILK decoder for %d kHz and %d channels", ErrInvalidState, rateKHz, d.lossChannels)
	}
	out := make([]float64, 0, frameSize*d.channels)
	for remaining := frameSize; remaining > 0; {
		chunk := lossDecodeChunk(min(remaining, d.plcFrameSize), d.sampleRate, lossMode)
		decodeSamples := chunk
		minSILKSamples := d.sampleRate / 100 // SILK PLC produces at least 10 ms.
		if decodeSamples < minSILKSamples {
			decodeSamples = minSILKSamples
		}
		frameMs := decodeSamples * 1000 / d.sampleRate
		info.dec.SetFrameMs(frameMs)
		d.prepareSILKStream(info.dec, d.lossChannels)
		silkPCM, err := info.dec.DecodePLC(1)
		if err != nil {
			return nil, fmt.Errorf("SILK PLC decoding failed: %w", err)
		}
		d.ensureSilkResampler(0, rateKHz)
		if d.lossChannels == 2 {
			d.ensureSilkResampler(1, rateKHz)
		}
		frame := d.resampleSILK(silkPCM, 1, d.lossChannels, false)
		frame = padOrTrim(frame, decodeSamples*d.channels)
		frame = frame[:chunk*d.channels]
		if lossMode == framing.ModeHybrid {
			celtPCM, err := d.decodeCELTPLCFrame(chunk, true)
			if err != nil {
				return nil, err
			}
			// Accumulated onto the SILK output in float32, unclipped.
			for i := range celtPCM {
				frame[i] = float64(float32(frame[i]) + float32(celtPCM[i]))
			}
		}
		out = append(out, frame...)
		remaining -= chunk
	}
	d.lastPitch = info.dec.Pitch() * 48 / rateKHz
	d.silkPitch48 = d.lastPitch
	d.prevSilkInternalCh = d.lossChannels
	if peer := d.silkDecoders[ri][1-ci]; peer != nil && peer.dec != nil {
		peer.dec.CopyPrimaryStateFrom(info.dec)
	}
	return out, nil
}

// accum is set when the concealed CELT layer is added onto SILK output
// (libopus celt_accum, mode != MODE_CELT_ONLY).
func (d *Decoder) decodeCELTPLCFrame(frameSize int, accum bool) ([]float64, error) {
	if d.lastCeltDec == nil || d.lossConfig < 0 || d.lossChannels < 1 {
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
	_, bandwidth, _ := framing.ParseTOCConfig(d.lossConfig)
	bw := 0
	switch bandwidth {
	case framing.BandwidthNarrowband:
		bw = 0
	case framing.BandwidthMediumband, framing.BandwidthWideband:
		bw = 1 // libopus uses CELT end band 17 for both
	case framing.BandwidthSuperwideband:
		bw = 2
	case framing.BandwidthFullband:
		bw = 3
	default:
		return nil, fmt.Errorf("%w: invalid CELT bandwidth %d", ErrInvalidState, bandwidth)
	}
	active := d.celtDecoders[bw][lm][d.channels-1]
	if active == nil {
		return nil, fmt.Errorf("%w: missing CELT PLC decoder", ErrInvalidState)
	}
	if active != d.lastCeltDec {
		active.CopyStateFrom(d.lastCeltDec)
	}
	active.SetAccumulate(accum)
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

func isValidLossFrameSize(frameSize, sampleRate int) bool {
	quantum := sampleRate / 400
	// opus_decode_native conceals any multiple of 2.5 ms, looping over
	// opus_decode_frame calls of at most st->frame_size.
	return quantum > 0 && frameSize > 0 && frameSize%quantum == 0
}

func floatToInt24(dst []int32, src []float64) {
	for i, sample := range src {
		dst[i] = res2Int24(sample)
	}
}

// float32ToInt16 is floatToInt16 for float32 samples; float32(float64(v))
// is exact, so both give the same result.
func float32ToInt16(dst []int16, src []float32) {
	dst = dst[:len(src)]
	for i, v := range src {
		dst[i] = libopusFloat32ToInt16(v)
	}
}

// libopusFloat32ToInt16 is libopusFloatToInt16 for a float32 sample.
func libopusFloat32ToInt16(sample float32) int16 {
	scaled := sample * float32(32768)
	if !(scaled > -32768) {
		scaled = -32768
	}
	if !(scaled < 32767) {
		scaled = 32767
	}
	return int16(math.RoundToEven(float64(scaled)))
}

func floatToInt16(dst []int16, src []float64) {
	for i, sample := range src {
		dst[i] = libopusFloatToInt16(sample)
	}
}

// libopusFloatToInt16 mirrors FLOAT2INT16 in libopus/celt/float_cast.h for
// the normal floating-point build: perform the scaling in float precision,
// saturate, then use the platform's default nearest-even rounding semantics.
func libopusFloatToInt16(sample float64) int16 {
	scaled := float32(sample) * float32(32768)
	// FLOAT2INT16: MAX32(x, -32768) then MIN32(x, 32767), written as the
	// (a > b ? a : b) macros so NaN becomes -32768 as in libopus.
	if !(scaled > -32768) {
		scaled = -32768
	}
	if !(scaled < 32767) {
		scaled = 32767
	}
	return int16(math.RoundToEven(float64(scaled)))
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
//
// An empty packet conceals len(pcm)/Channels() samples, like opus_decode
// with no packet and decode_fec set.
func (d *Decoder) DecodeFEC(data []byte, pcm []int16) (int, error) {
	if len(data) == 0 {
		return d.DecodePLC(pcm, len(pcm)/d.channels)
	}
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
// samples per channel. frameSize may be any positive multiple of 2.5 ms. If the
// packet cannot carry LBRR, concealment is used as in libopus.
func (d *Decoder) DecodeFECWithDuration(data []byte, pcm []int16, frameSize int) (int, error) {
	if !isValidLossFrameSize(frameSize, d.sampleRate) {
		return 0, fmt.Errorf("%w: %w: frameSize %d at %d Hz", ErrBadArg, ErrUnsupportedFrameSize, frameSize, d.sampleRate)
	}
	required := frameSize * d.channels
	if len(pcm) < required {
		return 0, fmt.Errorf("%w: got %d samples, need %d", ErrBufferTooSmall, len(pcm), required)
	}
	floatPCM, err := d.DecodeFECFloat(data, frameSize)
	if err != nil {
		return 0, err
	}
	floatToInt16(pcm[:required], floatPCM)
	return frameSize, nil
}

// DecodeFEC24 decodes recovery data to interleaved signed 24-bit PCM stored in
// int32 values. frameSize has the same explicit lost-duration semantics as
// DecodeFECWithDuration.
func (d *Decoder) DecodeFEC24(data []byte, pcm []int32, frameSize int) (int, error) {
	if !isValidLossFrameSize(frameSize, d.sampleRate) {
		return 0, fmt.Errorf("%w: %w: frameSize %d at %d Hz", ErrBadArg, ErrUnsupportedFrameSize, frameSize, d.sampleRate)
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
// Like opus_decode_native, FEC output is never soft-clipped and leaves the
// soft-clip memory of the int16 API untouched. An empty packet conceals
// frameSize samples (opus_decode_native runs the PLC for len == 0 whatever
// decode_fec says).
func (d *Decoder) DecodeFECFloat(data []byte, frameSize int) ([]float64, error) {
	if len(data) == 0 {
		return d.DecodePLCFloat(frameSize)
	}
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
	// st->frame_size follows the FEC packet only after the concealed prefix.
	d.plcFrameSize = packetFrameSize

	if len(frames[0]) <= 1 {
		// opus_decode_frame treats a first frame of at most one byte as a
		// loss: it is concealed in the previous mode with a zero final
		// range, while st->mode already follows the FEC packet.
		plc, err := d.decodePLCFloat(packetFrameSize)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, plc...)
		d.lastPacketConfig = config
		d.lastPacketChannels = info.channels
		return out, 0, nil
	}

	rateKHz := silkConfigRateKHz(config)
	if rateKHz != 8 && rateKHz != 12 && rateKHz != 16 {
		return nil, 0, fmt.Errorf("%w: invalid SILK rate %d kHz", ErrInvalidPacket, rateKHz)
	}
	if info.channels < 1 || info.channels > 2 {
		return nil, 0, fmt.Errorf("%w: invalid channel count %d", ErrInvalidPacket, info.channels)
	}
	// A SILK frame after a CELT one (the concealment of a trailing
	// redundant frame) starts from a reset SILK decoder.
	d.resetSILKAfterCELT()
	ri := silkRateIdx(rateKHz)
	ci := info.channels - 1
	d.switchSILKRate(ri)
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

	// silk_Decode's channel-count switch, as for a normal packet: a mono to
	// stereo switch seeds the right resampler from the left one, a stereo
	// to mono switch resamples the right output of the first frame through
	// the still-warm right resampler.
	d.ensureSilkResampler(0, rateKHz)
	if info.channels == 2 {
		d.ensureSilkResampler(1, rateKHz)
	}
	if info.channels == 2 && d.prevSilkInternalCh == 1 && d.silkRS[0] != nil {
		seeded := *d.silkRS[0]
		d.silkRS[1] = &seeded
		d.silkRSInKHz[1] = rateKHz
	}
	stereoToMono := info.channels == 1 && d.prevSilkInternalCh == 2 &&
		d.silkRS[1] != nil && d.silkRSInKHz[1] == rateKHz

	d.prepareSILKStream(infoDec.dec, info.channels)
	silkPCM, err := infoDec.dec.DecodeFEC(frames[0], nSilkFrames)
	if err != nil {
		return nil, 0, fmt.Errorf("%w: SILK LBRR decode: %v", ErrInvalidPacket, err)
	}
	silkPCM = d.resampleSILK(silkPCM, nSilkFrames, info.channels, stereoToMono)
	silkPCM = padOrTrim(silkPCM, packetFrameSize*d.channels)
	finalRange := infoDec.dec.LastFinalRange()

	// A switch from CELT conceals 5 ms in the old mode.
	var trans []float64
	if d.prevMode == framing.ModeCELTOnly {
		trans = d.transitionPLC(packetFrameSize)
	}
	if info.mode == ModeHybrid {
		// CELT carries no in-band FEC: its high band (start band 17, the
		// FEC packet's end band) is concealed, from a reset decoder after
		// a mode change, and supplies the final range.
		celtEnd := 21
		if config < 14 {
			celtEnd = 19
		}
		celtLMIdx := 3
		if frameMs == 10 {
			celtLMIdx = 2
		}
		celtDec := d.celtDecoders[3][celtLMIdx][d.channels-1]
		if celtDec == nil {
			return nil, 0, fmt.Errorf("%w: missing CELT decoder", ErrInvalidState)
		}
		celtDec.SetStreamChannels(info.channels)
		if d.prevMode >= 0 && d.prevMode != framing.ModeHybrid && !d.prevRedundancy {
			celtDec.Reset()
		} else if d.lastCeltDec != nil && d.lastCeltDec != celtDec {
			celtDec.CopyStateFrom(d.lastCeltDec)
		}
		celtDec.SetAccumulate(true)
		celtPCM := celtDec.DecodeHybridLoss(17, celtEnd)
		d.lastCeltDec = celtDec
		if d.celtResampler != nil {
			celtPCM = d.celtResampler.Process(celtPCM)
		}
		celtPCM = padOrTrim(adjustChannels(celtPCM, celtDec.Channels(), d.channels), packetFrameSize*d.channels)
		// Accumulated onto the SILK output in float32, unclipped.
		for i := range silkPCM {
			silkPCM[i] = float64(float32(silkPCM[i]) + float32(celtPCM[i]))
		}
		finalRange = celtDec.LastFinalRange()
	} else if d.prevMode == framing.ModeHybrid {
		// For hybrid -> SILK the CELT MDCT fades out through a decoded
		// silence frame.
		d.celtSilenceFade(silkPCM, info.channels, celtEndBandForFramingBW(config/4))
	}
	if trans != nil {
		d.applyTransition(silkPCM, trans)
	}
	if len(frames[0]) <= 1 {
		// opus_decode_frame: a payload of 0 or 1 byte runs the PLC and
		// leaves rangeFinal at 0.
		finalRange = 0
	}
	out = append(out, silkPCM...)

	d.prevSilkInternalCh = info.channels
	peerCI := 1 - ci
	if peer := d.silkDecoders[ri][peerCI]; peer != nil && peer.dec != nil {
		peer.dec.CopyPrimaryStateFrom(infoDec.dec)
	}
	d.lastPacketConfig = config
	d.lastPacketChannels = info.channels
	d.lossConfig, d.lossChannels = config, info.channels
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
		return fmt.Errorf("%w: %w: frameSize %d at %d Hz", ErrBadArg, ErrUnsupportedFrameSize, frameSize, d.sampleRate)
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
	clone.lossConfig = d.lossConfig
	clone.plcFrameSize = d.plcFrameSize
	clone.softclipMem = d.softclipMem
	clone.lossChannels = d.lossChannels
	clone.lastFinalRange = d.lastFinalRange
	clone.lastPitch = d.lastPitch
	clone.silkPitch48 = d.silkPitch48
	clone.gainQ8 = d.gainQ8
	clone.phaseInversionDisabled = d.phaseInversionDisabled
	clone.prevSilkInternalCh = d.prevSilkInternalCh
	clone.silkRSInKHz = d.silkRSInKHz
	clone.silkSMid = d.silkSMid
	clone.silkActiveRi = d.silkActiveRi
	clone.silkStereoDec = nil

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
	d.silkSMid = [2]int16{}
	d.silkActiveRi = -1
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
	d.lossConfig = -1
	d.plcFrameSize = d.sampleRate / 400
	d.softclipMem = [2]float32{}
	d.lossChannels = 0
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

// FinalRange returns the entropy decoder final range of the most recently
// decoded packet (OPUS_GET_FINAL_RANGE): that of its last Opus frame, or 0
// after concealment.
func (d *Decoder) FinalRange() uint32 { return d.lastFinalRange }

// Pitch returns the most recently reported decoder pitch period in samples at
// the decoder output rate. Zero means no pitch period is currently available.
// Unlike Pitch48kHz it does not keep the last SILK lag over a Reset.
func (d *Decoder) Pitch() int { return d.lastPitch * d.sampleRate / SampleRate48kHz }

// Pitch48kHz is OPUS_GET_PITCH: the pitch period in 48 kHz samples whatever
// the decoder output rate. After a CELT-only frame it is the CELT post-filter
// period, otherwise the last SILK lag (0 when unvoiced), which a reset keeps.
func (d *Decoder) Pitch48kHz() int {
	if d.prevMode == framing.ModeCELTOnly {
		return d.lastPitch
	}
	return d.silkPitch48
}

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

// growFloat64s returns buf resized to n elements, reallocating only when its
// capacity is too small. The contents are not cleared.
func growFloat64s(buf []float64, n int) []float64 {
	if cap(buf) < n {
		return make([]float64, n)
	}
	return buf[:n]
}

// growInt16 is growFloat64s for int16 buffers.
func growInt16(buf []int16, n int) []int16 {
	if cap(buf) < n {
		return make([]int16, n)
	}
	return buf[:n]
}
