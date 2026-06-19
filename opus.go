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

// Application specifies the encoding mode (use constants from package)
type Application = int

// SignalType is a content hint that lets the encoder tune heuristics for the
// dominant signal type without changing the bitstream format.
type SignalType = celt.SignalType

const (
	// SignalAuto lets the encoder derive a hint from the Application setting
	// (VOIP → voice, Audio/RestrictedLowDelay → music). This is the default.
	SignalAuto SignalType = celt.SignalUnknown
	// SignalVoice marks speech-leaning content. The encoder uses narrower
	// bandwidth tiers (matching ApplicationVOIP) and switches to short blocks
	// more eagerly on plosive onsets.
	SignalVoice SignalType = celt.SignalVoice
	// SignalMusic marks music or general audio content, applying wider
	// bandwidth tiers and standard transient sensitivity.
	SignalMusic SignalType = celt.SignalMusic
)

// Encoder represents an Opus encoder instance
type Encoder struct {
	sampleRate  int
	channels    int
	application Application

	// CELT encoder (always operates at 48kHz internally)
	celtEncoder *celt.Encoder

	// SILK encoder for the speech/low-bitrate path. It operates at the packet's
	// SILK internal rate (8/12/16 kHz); 24/48 kHz voice input is downsampled to
	// 16 kHz before encoding.
	silkEncoder    *silk.Encoder
	silkSampleRate int

	// Resampler for non-48kHz input rates
	inputResampler *resampler.Resampler // inRate -> 48kHz
	silkResampler  *resampler.Resampler // input sampleRate -> silkSampleRate

	// Configuration
	bitrate    int
	complexity int
	rateMode   celt.RateMode // CBR/VBR/CVBR
	frameSize  int           // frame size in samples at sampleRate
	padBytes   int           // code-3 padding-data bytes to append (0 = none)
	dtx        bool          // discontinuous transmission: minimal silence packets

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

	// Internal 48kHz frame size (always 960 for 20ms)
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
		bitrate:           64000,            // Default bitrate
		complexity:        5,                // Default complexity
		rateMode:          celt.RateModeCBR, // Default CBR (backward compatible)
		frameSize:         frameSize,
		maxBandwidth:      BandwidthFullband,
		forcedBandwidth:   BandwidthAuto,
		lastDetectedBW:    -1, // no detection history yet
		internalFrameSize: internalFrameSize,
		prevMode:          -1, // no previous packet yet
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
// frameSize is the number of samples per channel (at the encoder's sample rate)
// Returns compressed Opus packet
func (e *Encoder) Encode(pcm []int16, frameSize int) ([]byte, error) {
	if _, err := e.validateFrameSize(frameSize); err != nil {
		return nil, err
	}
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
	if _, err := e.validateFrameSize(frameSize); err != nil {
		return nil, err
	}
	expectedSize := frameSize * e.channels
	if len(pcm) < expectedSize {
		return nil, fmt.Errorf("insufficient PCM data: got %d, need %d", len(pcm), expectedSize)
	}

	return e.encodeFloat(pcm[:expectedSize], frameSize)
}

// encodeFloat is the internal encoding path shared by Encode and EncodeFloat.
//
// The encoder always emits 20 ms CELT-only fullband frames internally. When the
// requested frameSize is an exact multiple (2..6) of the 20 ms base, the input
// is split into that many consecutive 20 ms chunks, each is encoded into its own
// CELT frame, and the frames are packed into a single multi-frame Opus packet
// (RFC 6716 §3.2, count codes 1/2/3). Otherwise a single-frame (code 0) packet
// is produced.
func (e *Encoder) encodeFloat(pcm []float64, frameSize int) ([]byte, error) {
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
		return append([]byte{toc}, compressed...), nil
	}

	// Encode each 20 ms chunk continuously (the resampler and CELT encoder keep
	// their inter-frame state across chunks) and pack the frames. A single frame
	// reaches here only when padding was requested, in which case it is wrapped in
	// a code-3 packet (the only count code that carries padding).
	chunkLen := base * e.channels
	frames := make([][]byte, 0, nFrames)
	for k := 0; k < nFrames; k++ {
		chunk := pcm[k*chunkLen : (k+1)*chunkLen]
		f, err := e.encodeOneCELTFrame(chunk)
		if err != nil {
			return nil, err
		}
		frames = append(frames, f)
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
	return append([]byte{toc | byte(code)}, payload...), nil
}

// canDeferToHybrid reports whether the encoder can keep this packet in hybrid
// mode for one more frame to carry a transition-smoothing redundant CELT frame.
// The hybrid SILK layer is always the 16 kHz wideband encoder, so the SILK
// encoder must exist at that internal rate; low-delay never uses hybrid.
func (e *Encoder) canDeferToHybrid(nFrames int) bool {
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
	return red.EncodeRedundant(part, nbytes)
}

func (e *Encoder) shouldEncodeSILKOnly() bool {
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
	if e.bitrate > 40000 {
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
	if e.silkEncoder == nil || e.silkSampleRate != 16000 {
		return false
	}
	if nFrames < 1 || nFrames > 6 {
		return false
	}
	if e.application == ApplicationRestrictedLowDelay {
		return false
	}
	if !e.hasVoiceIntent() || e.bitrate <= 40000 {
		return false
	}
	bw := e.selectHybridBandwidth()
	return bw == framing.BandwidthSuperwideband || bw == framing.BandwidthFullband
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
	maxBytes := e.hybridFrameTargetBytes()
	// CBR keeps every hybrid frame at the full per-frame ceiling; VBR/CVBR lets
	// the per-frame target shrink for easy content (see hybridAdaptiveTargetBytes).
	cbr := e.rateMode == celt.RateModeCBR

	frames := make([][]byte, 0, nFrames)
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

		// Choose the per-frame coded size. The SILK low band has already been
		// written; CELT fills the remainder of the high band up to targetBytes,
		// and the frame is padded to exactly targetBytes so the decoder (which
		// derives the CELT budget from the final packet length) stays
		// bit-symmetric with the allocation the encoder used. In VBR the target
		// shrinks toward the SILK size for frames with little high-band energy
		// (silence, low tones), so easy hybrid frames are no longer padded to the
		// full CBR ceiling. With redundancy the CELT high band is given the
		// remaining budget (maxBytes - redundancyBytes); the redundant 5 ms frame
		// is appended after it so the whole frame is still maxBytes.
		frameBytes := maxBytes
		targetBytes := maxBytes
		if frameRedundancy {
			targetBytes = maxBytes - redundancyBytes
			enc.Shrink(targetBytes)
		} else if !cbr {
			targetBytes = e.hybridAdaptiveTargetBytes(enc.ECTell(), celtInput, maxBytes)
			frameBytes = targetBytes
			// Shrink the shared range encoder to the chosen size before CELT writes
			// so the raw-tail bits land at targetBytes (libopus ec_enc_shrink). The
			// decoder derives the CELT budget from the final packet length, so the
			// allocation CELT computes against targetBytes stays bit-symmetric.
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
		if err := e.celtEncoder.EncodeHybrid(celtInput, enc, targetBytes, 17, celtEnd); err != nil {
			return nil, false, fmt.Errorf("CELT hybrid encoding failed: %w", err)
		}
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

// hybridAdaptiveTargetBytes picks a per-frame hybrid packet size for VBR. The
// SILK low band has already consumed silkBits; the CELT high band is given a
// budget scaled by how much energy sits above the SILK cutoff, so frames with
// little high-frequency content shrink below the CBR ceiling instead of padding
// to it. The result is anchored above the SILK size (plus a small floor for the
// mandatory high-band coarse energy) and capped at maxBytes.
func (e *Encoder) hybridAdaptiveTargetBytes(silkBits int, celtInput []float64, maxBytes int) int {
	silkBytes := (silkBits + 7) / 8
	// Minimum room for the CELT high-band coarse energy + header symbols.
	const minHighBandBytes = 10
	maxHighBand := maxBytes - silkBytes
	if maxHighBand <= minHighBandBytes {
		// SILK is already near the ceiling; let CELT use whatever remains.
		return maxBytes
	}
	frac := hybridHighBandActivity(celtInput, e.channels)
	highBand := minHighBandBytes + int(frac*float64(maxHighBand-minHighBandBytes)+0.5)
	target := silkBytes + highBand
	if target > maxBytes {
		target = maxBytes
	}
	return target
}

// hybridHighBandActivity estimates, in [0,1], how much of the signal's energy
// sits at high frequencies, which is what the CELT high band of a hybrid packet
// codes. It uses a first-difference (high-pass) energy ratio on the mono downmix:
// the ratio is ~0 for low tones, small for band-limited speech, and ~2 for white
// noise, so it is mapped through sqrt(ratio/2) to span the budget range.
func hybridHighBandActivity(pcm []float64, channels int) float64 {
	if channels <= 0 {
		return 0
	}
	n := len(pcm) / channels
	if n < 2 {
		return 0
	}
	sample := func(i int) float64 {
		if channels == 1 {
			return pcm[i]
		}
		return 0.5 * (pcm[i*channels] + pcm[i*channels+1])
	}
	var energy, hpEnergy float64
	prev := sample(0)
	for i := 1; i < n; i++ {
		s := sample(i)
		d := s - prev
		prev = s
		hpEnergy += d * d
		energy += s * s
	}
	if energy < 1e-9 {
		return 0
	}
	frac := math.Sqrt(hpEnergy / energy / 2.0)
	if frac > 1 {
		frac = 1
	}
	return frac
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
	pos := 0
	for gi, group := range groups {
		inputSamples := group * inputChunkLen
		silkPCM := pcm[pos : pos+inputSamples]
		if e.silkResampler != nil {
			silkPCM = e.silkResampler.Process(silkPCM)
			silkPCM = padOrTrim(silkPCM, group*silkChunkLen)
		}
		stream := []byte{0x00}
		frameRedundancy := celtToSilk && gi == 0 && !isSilentPCM(silkPCM)
		if !isSilentPCM(silkPCM) {
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

func (e *Encoder) validateFrameSize(frameSize int) (int, error) {
	base := e.frameSize
	if base <= 0 || frameSize <= 0 || frameSize%base != 0 {
		return 0, fmt.Errorf("%w: frameSize %d is not a 20 ms multiple at %d Hz", ErrUnsupportedFrameSize, frameSize, e.sampleRate)
	}
	nFrames := frameSize / base
	if nFrames < 1 || nFrames > 6 {
		return 0, fmt.Errorf("%w: packet duration %d ms exceeds Opus maximum 120 ms", ErrUnsupportedFrameSize, nFrames*20)
	}
	return nFrames, nil
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

// Bitrate returns the current target bitrate in bits per second.
func (e *Encoder) Bitrate() int { return e.bitrate }

// Complexity returns the current complexity setting (0–10).
func (e *Encoder) Complexity() int { return e.complexity }

// VBR reports whether variable bitrate is enabled.
func (e *Encoder) VBR() bool { return e.rateMode != celt.RateModeCBR }

// Application returns the current application mode.
func (e *Encoder) Application() Application { return e.application }

// SetBitrate sets the target bitrate in bits per second
func (e *Encoder) SetBitrate(bitrate int) error {
	if bitrate < 6000 || bitrate > 510000 {
		return fmt.Errorf("invalid bitrate: %d (must be between 6000 and 510000)", bitrate)
	}
	e.bitrate = bitrate
	e.celtEncoder.SetBitrate(bitrate)
	if e.silkEncoder != nil {
		silkBitrate := bitrate
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
		return fmt.Errorf("invalid complexity: %d (must be 0-10)", complexity)
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
// useful for keeping a constant on-the-wire packet size or for obscuring the true
// payload length. n <= 0 disables padding (the default), restoring the compact
// code-0/1/2/3 selection.
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

// SetApplication changes the application mode. This re-derives the CELT content
// hint (voice for VOIP, music otherwise), which influences bandwidth selection
// and transient sensitivity; it does not affect already-emitted packets.
func (e *Encoder) SetApplication(application Application) {
	e.application = application
	e.celtEncoder.SetSignalType(signalTypeForApplication(application))
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
		return fmt.Errorf("invalid bandwidth: %d", bw)
	}
	e.maxBandwidth = bw
	return nil
}

// SetBandwidth forces a specific coded bandwidth, overriding the automatic
// selection (it is still clamped to the input sample rate's Nyquist limit). Pass
// BandwidthAuto to return to automatic selection (the default). bw must be
// BandwidthAuto or one of the public Bandwidth* constants. CELT has no
// medium-band mode, so BandwidthMediumband is rounded up to BandwidthWideband.
func (e *Encoder) SetBandwidth(bw int) error {
	if bw != BandwidthAuto && !isValidBandwidth(bw) {
		return fmt.Errorf("invalid bandwidth: %d", bw)
	}
	e.forcedBandwidth = bw
	return nil
}

// Bandwidth reports the coded bandwidth the encoder would currently use, as a
// public Bandwidth* constant.
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

// Reset resets the encoder state
func (e *Encoder) Reset() error {
	e.celtEncoder.Reset()
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
	if e.redundancyCelt != nil {
		e.redundancyCelt.Reset()
	}
	e.celtEncoder.SetBitrate(e.bitrate)
	e.celtEncoder.SetComplexity(e.complexity)
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
	lastCeltDec  *celt.Decoder
	prevMode     int

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

	frameSize          int // frame size in samples at sampleRate
	internalFrameSize  int // always 960 (20ms at 48kHz)
	lastPacketDuration int // samples per channel decoded by the last packet
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
		prevMode:          -1,
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

	n := len(floatPCM)
	if n > len(pcm) {
		return 0, fmt.Errorf("%w: got %d samples, need %d", ErrBufferTooSmall, len(pcm), n)
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
	frameCount, err := opusFrameCount(payload, countCode)
	if err != nil {
		return nil, err
	}
	duration, err := packetDurationSamples(config, frameCount, d.sampleRate)
	if err != nil {
		return nil, err
	}

	if config < 16 {
		pktChannels := 1
		if stereo {
			pktChannels = 2
		}
		if config >= 12 {
			// Hybrid mode: SILK low band + CELT high band sharing one stream.
			out, err := d.decodeHybridPacket(payload, countCode, config, pktChannels)
			if err == nil {
				d.lastPacketDuration = duration
				d.prevMode = framing.ModeHybrid
			}
			return out, err
		}
		// SILK-only mode.
		out, err := d.decodeSILKPacket(payload, countCode, config, pktChannels)
		if err == nil {
			d.lastPacketDuration = duration
			d.prevMode = framing.ModeSILKOnly
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
	for _, frame := range frames {
		if d.lastCeltDec != nil && d.lastCeltDec != activeCeltDec {
			activeCeltDec.CopyStateFrom(d.lastCeltDec)
		}
		pcm, err := activeCeltDec.Decode(frame)
		if err != nil {
			return nil, fmt.Errorf("CELT decoding failed: %w", err)
		}
		d.lastCeltDec = activeCeltDec

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
	d.prevMode = framing.ModeCELTOnly
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
				d.prevMode == framing.ModeCELTOnly {
				d.crossfadeLeadingRedundancy(pcm, redPCM)
			}
		}
		allPCM = append(allPCM, pcm...)
	}
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

func (d *Decoder) decodeHybridPacket(payload []byte, countCode, config, pktChannels int) ([]float64, error) {
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
		return nil, fmt.Errorf("SILK decoder not initialized for hybrid rate=%dkHz ch=%d", rateKHz, pktChannels)
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
	for si, stream := range silkStreams {
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
			if len(leadingRedundancy) >= (d.sampleRate/200)*d.channels && d.prevMode == framing.ModeCELTOnly {
				d.crossfadeLeadingRedundancy(silkOut, leadingRedundancy)
			}

			// SILK->CELT redundancy: decode the trailing 5 ms (F5=240 @ 48k) CELT
			// frame on a freshly reset decoder, crossfade the last 2.5 ms of this
			// frame, and adopt its state so the next CELT-only packet predicts
			// coarse energy from the right baseline (matches libopus).
			if redundancy && !celtToSilk && redundancyBytes >= 2 && celtLen+redundancyBytes <= len(stream) {
				if redDec, rerr := celt.NewDecoderEx(240, 48000, 21, celtActualCh); rerr == nil {
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
	}
	d.prevSilkInternalCh = pktChannels

	if pktChannels == 1 && stereoPeer != nil && stereoPeer.dec != nil {
		stereoPeer.dec.CopyPrimaryStateFrom(info.dec)
	} else if pktChannels == 2 && monoPeer != nil && monoPeer.dec != nil {
		monoPeer.dec.CopyPrimaryStateFrom(info.dec)
	}

	return allPCM, nil
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
	return nil
}

// GetLastPacketDuration returns the duration of the last decoded packet in samples
func (d *Decoder) GetLastPacketDuration() int {
	if d.lastPacketDuration > 0 {
		return d.lastPacketDuration
	}
	return d.frameSize
}
