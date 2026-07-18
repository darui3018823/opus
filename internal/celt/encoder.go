package celt

import (
	"errors"
	"fmt"
	"math"
	"os"

	"github.com/darui3018823/opus/internal/dsp"
	"github.com/darui3018823/opus/internal/entcode"
)

var encDebug = os.Getenv("OPUS_ENC_DEBUG") != ""
var encTrace = os.Getenv("OPUS_ENC_TRACE") != ""

func etr(enc *entcode.Encoder, label string) {
	if encTrace {
		fmt.Fprintf(os.Stderr, "[ENC %-12s] tell=%d tellf=%d rng=%08x\n",
			label, enc.ECTell(), enc.TellFrac(), enc.GetRng())
	}
}

// RateMode selects the bitrate control strategy for the CELT encoder.
type RateMode int

const (
	// RateModeCBR produces fixed-size packets equal to targetBytes.
	RateModeCBR RateMode = iota
	// RateModeVBR produces variable-size packets — the encoder shrinks the
	// packet to the actual bytes used after encoding (unconstrained VBR).
	RateModeVBR
	// RateModeCVBR is constrained VBR: like VBR but the output size is
	// clamped so the average bitrate stays close to the target. A per-frame
	// reservoir (vbrOffset) accumulates surplus/deficit.
	RateModeCVBR
)

// SignalType hints the dominant content type, letting application-driven
// heuristics tune their decisions. SignalUnknown applies the music/general
// defaults. The top-level encoder derives it from the Opus application
// (VOIP -> voice, audio/low-delay -> music).
type SignalType int

const (
	// SignalUnknown means no content hint is available; use general defaults.
	SignalUnknown SignalType = iota
	// SignalVoice marks speech-leaning content.
	SignalVoice
	// SignalMusic marks music/general-audio content.
	SignalMusic
)

// patchTransientVoiceThreshold is the energy-rise threshold (log2-amplitude, the
// bandLogE domain) used by patch_transient_decision for voice-leaning content. It
// is lower than the libopus default of 1.0 so that speech plosives and onsets
// switch to short blocks more eagerly (mirroring the spirit of libopus enabling
// allow_weak_transients on the voice path).
const patchTransientVoiceThreshold = 0.5

// Encoder is a CELT encoder instance. Its output is decoded by the CELT decoder
// in this package: the encode path is the structural mirror of decodeCELTRange,
// emitting the same range-coder symbol sequence in the same order so that the
// (RFC-conformant) decoder reconstructs the signal.
type Encoder struct {
	mode          *Mode
	celtMode      *dsp.CELTMode // forward (analysis) MDCT, N-point long block
	shortCeltMode *dsp.CELTMode // forward MDCT for transient short blocks (NBase-point)

	// Per-channel streaming state.
	overlap    [][]float64 // MDCT analysis overlap memory (Overlap samples each)
	preemphMem []float64   // pre-emphasis filter memory (×32768 domain: 0.85*prev)

	// Encoder configuration.
	bitrate    int
	complexity int
	rateMode   RateMode
	dtx        bool       // discontinuous transmission: minimal packets for silence
	signalType SignalType // content hint (voice/music) for application-driven heuristics
	disableInv bool       // disable intensity-stereo phase inversion
	// endBand is the number of coded bands (the CELT "end" band). 0 means full
	// band (mode.Bands.NumBands). A smaller value limits the coded bandwidth
	// (NB=13, WB=17, SWB=19, FB=21), matching the decoder's per-config band count.
	// It is a configuration field, not reset by Reset (libopus keeps end out of
	// OPUS_RESET_STATE).
	endBand int

	// Inter-frame coarse-energy predictor state (oldBandE), channel-major
	// mean-subtracted log2-amplitude. Mirrors the decoder's prevEnergies.
	prevBandEnergies []float64
	// foldSeed mirrors the decoder's lastFinalRange (the range value used to seed
	// PVQ noise folding). Because the range register evolves identically in the
	// encoder and decoder for the same symbols, storing enc.GetRng() before flush
	// reproduces the decoder's lastFinalRange for the next frame.
	foldSeed   uint32
	finalRange uint32
	frameCount int

	// Spreading-decision state (libopus st->tonal_average / st->spread_decision).
	// tonal_average is the recursively-averaged tonality measure; lastSpread is
	// the previous frame's decision, used for hysteresis.
	tonalAverage int
	lastSpread   int

	// consecTransient counts consecutive transient frames (libopus
	// st->consec_transient). It gates the anti-collapse decision: anti-collapse
	// is enabled while consecTransient < 2.
	consecTransient int

	// intensity is the previous frame's intensity-stereo starting band (libopus
	// st->intensity), kept across frames so the hysteresis decision is stable.
	// Zeroed by Reset, matching libopus OPUS_RESET_STATE.
	intensity int
	// energyMask is a per-frame, channel-major surround SMR supplied by the
	// multistream surround analyzer. The first bounded consumer is allocation
	// trim; later decisions deliberately remain independent.
	energyMask     []float64
	lastCodedBands int

	// CVBR reservoir: accumulated bit surplus/deficit in Q8 bits. Positive means
	// the encoder has used fewer bits than the target and can afford to spend
	// more; negative means it has overspent. Clamped to [-maxReservoir, maxReservoir].
	vbrOffset    int
	vbrCount     int // number of VBR frames encoded (for average computation)
	vbrDriftComp int // drift compensation accumulator
}

// FinalRange returns the range coder rng after the most recent Encode (before
// flush). For a correctly paired packet it equals the decoder's LastFinalRange.
func (e *Encoder) FinalRange() uint32 { return e.finalRange }

// silenceEnergyThreshold is the SIG-domain (×32768) summed band-energy below
// which a frame is treated as digital silence. Pre-emphasised real audio yields
// band energies many orders of magnitude above this (even at very low levels,
// because of the ×32768 scaling), while a truly silent frame sums only the
// per-band 1e-27 floors. The wide gap makes a fixed threshold safe against
// false positives on quiet-but-real content.
const silenceEnergyThreshold = 1e-2

// EncoderConfig holds encoder configuration
type EncoderConfig struct {
	Bitrate    int      // Target bitrate in bps
	Complexity int      // Complexity level (0-10)
	RateMode   RateMode // Rate control mode (CBR/VBR/CVBR)
}

// DefaultEncoderConfig returns default encoder configuration
func DefaultEncoderConfig() *EncoderConfig {
	return &EncoderConfig{
		Bitrate:    64000,       // 64 kbps
		Complexity: 5,           // Medium complexity
		RateMode:   RateModeCBR, // CBR (backward compatible)
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
	overlap := mode.Overlap
	win := celtWindow(overlap)
	celtMode := dsp.NewCELTMode(frameSize, overlap, win)
	// Short-block analysis MDCT (NBase samples), the encode counterpart of the
	// decoder's shortCeltMode, used to limit pre-echo on transient frames.
	shortCeltMode := dsp.NewCELTMode(overlap, overlap, win)

	e := &Encoder{
		mode:          mode,
		celtMode:      celtMode,
		shortCeltMode: shortCeltMode,
		overlap:       make([][]float64, channels),
		preemphMem:    make([]float64, channels),
		bitrate:       config.Bitrate,
		complexity:    config.Complexity,
		rateMode:      config.RateMode,
		// libopus opus_custom_encoder_init defaults.
		tonalAverage: 256,
		lastSpread:   spreadNormal,
	}
	for c := 0; c < channels; c++ {
		e.overlap[c] = make([]float64, overlap)
	}

	// oldBandE history is zeroed by libopus OPUS_RESET_STATE.
	e.prevBandEnergies = make([]float64, channels*mode.Bands.NumBands)

	return e, nil
}

// targetBytes returns the fixed packet size (bytes) for this frame. The decoder
// uses len(packet)*8 as its bit-allocation budget, so the encoder commits to this
// size up front, runs the whole allocation against it, and pads the output to it.
func (e *Encoder) targetBytes() int {
	frameDuration := float64(e.mode.FrameSize) / float64(e.mode.SampleRate)
	tb := int(float64(e.bitrate) * frameDuration / 8.0)
	if tb < 2 {
		tb = 2
	}
	if tb > 1275 {
		tb = 1275
	}
	return tb
}

// Encode encodes one CELT frame (interleaved float64 PCM, FrameSize*Channels).
func (e *Encoder) Encode(samples []float64) ([]byte, error) {
	_, out, err := e.encodeRange(samples, nil, e.targetBytes(), 0, -1, false)
	return out, err
}

// SetEnergyMask sets the transient per-frame surround SMR. It is internal to
// the parent surround encoder and is copied so callers may reuse their buffer.
func (e *Encoder) SetEnergyMask(mask []float64) {
	e.energyMask = append(e.energyMask[:0], mask...)
}

// EncodeRedundant encodes a standalone fullband CELT frame of exactly nbytes,
// used for the 5 ms redundant frame that smooths a hybrid->CELT transition. The
// caller resets the encoder first so the frame carries no overlap history, which
// matches the decoder's freshly reset redundant-frame decoder. The output is a
// fixed-size (CBR) packet padded to nbytes regardless of the encoder's rate mode.
func (e *Encoder) EncodeRedundant(samples []float64, nbytes int) ([]byte, error) {
	if nbytes < 2 {
		return nil, errors.New("celt: invalid redundancy size")
	}
	saved := e.rateMode
	e.rateMode = RateModeCBR
	_, out, err := e.encodeRange(samples, nil, nbytes, 0, -1, false)
	e.rateMode = saved
	return out, err
}

// EncodeHybrid writes the CELT high-band layer of a hybrid frame into an
// already-started range encoder. The SILK layer must already have written its
// symbols. maxBytes is the maximum shared Opus frame payload size. In VBR mode,
// the returned size is the final payload size selected before CELT allocation.
func (e *Encoder) EncodeHybrid(samples []float64, enc *entcode.Encoder, maxBytes, start, end int, sourceSilence bool) (int, error) {
	if enc == nil {
		return 0, errors.New("celt: nil range encoder")
	}
	if maxBytes < 2 {
		return 0, errors.New("celt: invalid hybrid payload size")
	}
	targetBytes, _, err := e.encodeRange(samples, enc, maxBytes, start, end, sourceSilence)
	return targetBytes, err
}

func (e *Encoder) encodeRange(samples []float64, sharedEnc *entcode.Encoder, maxTargetBytes, startBand, endBand int, sourceSilence bool) (int, []byte, error) {
	if len(samples) != e.mode.FrameSize*e.mode.Channels {
		return 0, nil, errors.New("celt: invalid input size")
	}

	ch := e.mode.Channels
	numBands := e.mode.Bands.NumBands
	nbEBands := NumBands48000
	lm := e.mode.LM
	frameSize := e.mode.FrameSize
	ov := e.mode.Overlap
	shared := sharedEnc != nil
	tell0Frac := 1
	if shared {
		tell0Frac = sharedEnc.TellFrac()
	}
	start, end := startBand, numBands
	if start < 0 {
		start = 0
	}
	if start > numBands {
		start = numBands
	}
	if endBand > 0 {
		end = endBand
	} else if e.endBand > 0 && e.endBand < numBands {
		end = e.endBand
	}
	if end > numBands {
		end = numBands
	}
	if end < start {
		end = start
	}
	M := 1 << uint(lm)
	frameLen := M * int(EBands48000[numBands])
	if maxTargetBytes < 2 {
		maxTargetBytes = 2
	}
	inputSilence := sourceSilence
	if !inputSilence {
		inputSilence = true
		for _, sample := range samples {
			if sample != 0 {
				inputSilence = false
				break
			}
		}
	}

	// --- Analysis: pre-emphasis, forward MDCT, band energy, normalisation ---
	X := make([]float64, ch*frameLen)
	// bandE holds per-band amplitude in libopus channel-major [c*nbEBands+i]
	// layout (used by stereo intensity/split inside quant_all_bands).
	bandE := make([]float64, 2*nbEBands)
	logE := make([]float64, ch*numBands)

	// Pass 1: pre-emphasis and build the per-channel analysis buffers
	// [overlap(prev) ‖ preemph(frame)], length frameSize+overlap. This is the
	// time-domain signal both the transient detector and the forward MDCT read.
	bufs := make([][]float64, ch)
	for c := 0; c < ch; c++ {
		pe := make([]float64, frameSize)
		mem := e.preemphMem[c]
		for i := 0; i < frameSize; i++ {
			var s float64
			if ch == 1 {
				s = samples[i]
			} else {
				s = samples[i*ch+c]
			}
			xin := s * 32768.0
			pe[i] = xin - mem
			mem = 0.85 * xin
		}
		e.preemphMem[c] = mem

		buf := make([]float64, frameSize+ov)
		copy(buf[:ov], e.overlap[c])
		copy(buf[ov:], pe)
		bufs[c] = buf
	}

	// Transient detection (short MDCT blocks reduce pre-echo on attacks). The
	// isTransient flag is only coded for LM>0; the bit's budget guard below is
	// satisfied for every non-degenerate packet (the symbols before it cost only
	// a couple of bits), so committing to short blocks here cannot desync.
	isTransient := false
	tfChan := 0
	tfEstimate := 0.0
	if lm > 0 && e.complexity >= 1 {
		isTransient, tfChan, tfEstimate = transientAnalysis(bufs, frameSize+ov, ch)
	}
	// Pass 2: forward MDCT (M interleaved short blocks on transients, else one
	// long block), band energy, and per-band normalisation.
	//
	// logE2 (bandLogE2) is a second set of band log energies fed to the dynalloc
	// masking follower. On transient frames at complexity>=8 (secondMdct) it comes
	// from a full-length (long-block) MDCT of the same analysis buffer, giving the
	// follower the long block's superior frequency resolution while the actual
	// per-band energy (logE/bandE, used for normalisation and coarse energy) stays
	// short-block. Off transients (or below complexity 8) the long block IS the
	// actual MDCT, so logE2==logE — matching libopus' OPUS_COPY fallback.
	logE2 := make([]float64, ch*numBands)
	nBase := e.mode.NBase

	// computeSpectrum runs the forward MDCT (M interleaved short blocks when
	// transient, else one long block), band energy, normalised spectrum X and
	// logE for the given block type. The overlap-state copy reads the unchanged
	// analysis buffer, so calling this twice (after patch_transient_decision
	// promotes the frame to transient) advances the overlap exactly once.
	computeSpectrum := func(transient bool) {
		for c := 0; c < ch; c++ {
			buf := bufs[c]
			var coeffs []float64
			if transient {
				// M short MDCTs over overlapping NBase windows, interleaved into the
				// layout the decoder's transient synthesis expects (coeff k+i*M).
				coeffs = make([]float64, frameSize)
				for b := 0; b < M; b++ {
					sub := buf[b*nBase : b*nBase+nBase+ov]
					sc := e.shortCeltMode.CLTMDCTForward(sub)
					for i := 0; i < nBase; i++ {
						coeffs[b+i*M] = sc[i]
					}
				}
			} else {
				coeffs = e.celtMode.CLTMDCTForward(buf)
			}
			copy(e.overlap[c], buf[frameSize:frameSize+ov])

			base := c * frameLen
			for i := 0; i < numBands; i++ {
				lo := M * int(EBands48000[i])
				hi := M * int(EBands48000[i+1])
				sumsq := 1e-27
				for j := lo; j < hi; j++ {
					sumsq += coeffs[j] * coeffs[j]
				}
				amp := math.Sqrt(sumsq)
				bandE[c*nbEBands+i] = amp
				logE[c*numBands+i] = math.Log2(amp) - EMean(i)
				inv := 1.0 / amp
				for j := lo; j < hi; j++ {
					X[base+j] = coeffs[j] * inv
				}
			}
		}
	}

	// computeLogE2Long fills logE2 from a full-length (long-block) MDCT of the
	// analysis buffer plus the +corr short-vs-long amplitude-scale compensation.
	// CLTMDCTForward is a pure function of its input, so this never disturbs the
	// overlap state already advanced by computeSpectrum.
	computeLogE2Long := func(corr float64) {
		for c := 0; c < ch; c++ {
			longCoeffs := e.celtMode.CLTMDCTForward(bufs[c])
			for i := 0; i < numBands; i++ {
				lo := M * int(EBands48000[i])
				hi := M * int(EBands48000[i+1])
				sumsq := 1e-27
				for j := lo; j < hi; j++ {
					sumsq += longCoeffs[j] * longCoeffs[j]
				}
				logE2[c*numBands+i] = math.Log2(math.Sqrt(sumsq)) - EMean(i) + corr
			}
		}
	}

	computeSpectrum(isTransient)
	// logE2 (bandLogE2) feeds the dynalloc masking follower. On transient frames at
	// complexity>=8 (secondMdct) it comes from a long-block MDCT (superior frequency
	// resolution) while the actual per-band energy stays short-block; otherwise the
	// long block IS the actual MDCT, so logE2==logE (libopus' OPUS_COPY fallback).
	secondMdct := isTransient && e.complexity >= 8
	if secondMdct {
		computeLogE2Long(0.5 * float64(lm))
	} else {
		copy(logE2, logE)
	}

	// --- Silence detection ---
	// Sum the SIG-domain band energy (bandE holds sqrt(1e-27+Σcoeff²) per band).
	// The analysis above has already advanced the overlap and pre-emphasis memory,
	// so state continuity is preserved whether or not the frame is silent.
	var frameEnergy float64
	for c := 0; c < ch; c++ {
		for i := start; i < end; i++ {
			amp := bandE[c*nbEBands+i]
			frameEnergy += amp * amp
		}
	}
	isSilence := frameEnergy < silenceEnergyThreshold
	if isSilence && !shared {
		out, err := e.encodeSilenceFrame(maxTargetBytes)
		return maxTargetBytes, out, err
	}

	// --- patch_transient_decision (energy-rise fallback transient detector) ---
	// When the time-domain transientAnalysis did not flag the frame but the band
	// energies jumped over the previous frame (an onset the envelope analysis
	// missed), promote the frame to transient and re-run the MDCT with short blocks
	// to limit pre-echo. libopus runs this at complexity>=5 on non-LFE frames.
	// Skipped on the first frame (no previous energies to compare) and on silent
	// frames (returned above). Voice-leaning content uses a lower threshold so
	// plosives switch eagerly.
	if lm > 0 && !isTransient && e.complexity >= 5 && e.frameCount > 0 {
		threshold := 1.0 // libopus QCONST16(1, DB_SHIFT)
		if e.signalType == SignalVoice {
			threshold = patchTransientVoiceThreshold
		}
		if patchTransientDecision(logE, e.prevBandEnergies, numBands, start, end, ch, threshold) {
			isTransient = true
			tfEstimate = 0.2 // libopus QCONST16(.2f, 14)
			// The long-block logE just computed becomes the bandLogE2 estimate
			// (good frequency resolution); add the +LM/2 scale correction. Then
			// recompute the actual spectrum with short blocks.
			corr := 0.5 * float64(lm)
			for idx := range logE2 {
				logE2[idx] = logE[idx] + corr
			}
			computeSpectrum(true)
		}
	}

	// --- VBR target computation ---
	// In VBR/CVBR mode, adjust the allocation budget based on signal activity.
	// This mirrors libopus celt_encode_with_ec VBR logic (simplified):
	//   - Compute total band energy as a proxy for signal activity.
	//   - Scale targetBytes between a floor (minBytes) and the full CBR target.
	//   - Silent or near-silent frames get a smaller budget; complex frames
	//     get the full budget.
	targetBytes := maxTargetBytes

	if e.rateMode != RateModeCBR && !shared {
		// Sum log-domain band energies to estimate activity.
		// EMean-subtracted logE is ~0 for a typical signal; very negative = quiet.
		var activity float64
		for c := 0; c < ch; c++ {
			for i := start; i < end; i++ {
				v := logE[c*numBands+i]
				if v > 0 {
					activity += v
				} else if v > -10.0 {
					// Only lightly penalize moderately quiet bands
					activity += v * 0.05
				}
			}
		}
		// Normalise to [0, 1] range. A fully active signal with numBands
		// bands at logE~+3 each gives activity ~63; we saturate at that.
		maxActivity := float64(ch*numBands) * 3.0
		if maxActivity < 1 {
			maxActivity = 1
		}
		frac := activity / maxActivity
		if frac > 1.0 {
			frac = 1.0
		}
		if frac < 0.0 {
			frac = 0.0
		}

		// Minimum packet size: enough for header symbols + some coarse energy.
		minBytes := maxTargetBytes / 4
		if minBytes < 2 {
			minBytes = 2
		}

		// Scale target: frac=1 → full budget; frac=0 → minBytes.
		// Use a sqrt curve so that moderate signals still get most of the budget.
		scaledFrac := math.Sqrt(frac)
		targetBytes = minBytes + int(scaledFrac*float64(maxTargetBytes-minBytes)+0.5)
		if targetBytes > maxTargetBytes {
			targetBytes = maxTargetBytes
		}
		if targetBytes < minBytes {
			targetBytes = minBytes
		}

		// CVBR reservoir adjustment: use accumulated surplus to boost budget
		// when the signal needs it.
		if e.rateMode == RateModeCVBR && e.vbrOffset > 0 {
			// Allow spending up to half the surplus on this frame.
			boostBytes := (e.vbrOffset >> (3 + 3)) / 2 // Q8 bits → bytes, halved
			targetBytes += boostBytes
			if targetBytes > maxTargetBytes {
				targetBytes = maxTargetBytes
			}
		}
	}
	if e.rateMode == RateModeCVBR && !shared && targetBytes < maxTargetBytes {
		// Match libopus compute_vbr's constrained-VBR damping:
		// base_target + 0.67*(target-base_target). The activity curve can
		// otherwise start a quiet tonal stream at one quarter of its nominal
		// target and take several damaged frames to refill the reservoir.
		targetBytes = maxTargetBytes - (2*(maxTargetBytes-targetBytes)+1)/3
	}

	totalBits := targetBytes * 8

	// Allocate the entropy coder at the full (CBR) budget, then shrink it to the
	// chosen VBR target before any symbols are written (libopus ec_enc_shrink).
	//
	// Shrinking BEFORE the header/coarse-energy symbols — rather than after, as
	// libopus celt_encode_with_ec does — is deliberate. The coarse-energy path
	// selection (QuantizeCoarseEnergy) and clt_compute_allocation both read the
	// budget as packet_length*8, and the decoder derives that from the FINAL
	// (shrunk) packet length. Encoding against the shrunk budget here keeps those
	// decisions bit-symmetric with the decoder. libopus can defer the shrink to
	// after coarse energy because its budget guards never bind that early at its
	// bitrates; doing the same unconditionally here would risk a low-bitrate
	// stereo desync (the bitsLeft<30 qi-clamp in QuantizeCoarseEnergy can trip on
	// the smaller decoder-side budget but not on the larger pre-shrink budget).
	enc := sharedEnc
	if enc == nil {
		enc = entcode.NewEncoder(maxTargetBytes)
		if targetBytes < maxTargetBytes {
			enc.Shrink(targetBytes)
		}
	}

	// === Header symbols, in decoder order (decodeCELTRange) ===

	// Silence flag (logp 15) — read only when ec_tell is still at the initial
	// one-bit offset. Hybrid frames skip this after SILK has consumed bits.
	if enc.ECTell() == 1 && enc.ECTell()+1 <= totalBits {
		enc.EncodeBitLogp(false, 15)
	}
	etr(enc, "silence")

	// Post-filter (CELT-only, start==0): signalled disabled.
	if start == 0 && enc.ECTell()+16 <= totalBits {
		enc.EncodeBitLogp(false, 1)
	}

	// isTransient (logp 3, LM>0). The decision was made in pass 1 and already
	// drove the MDCT block size; here we just code it.
	if lm > 0 && enc.ECTell()+3 <= totalBits {
		enc.EncodeBitLogp(isTransient, 3)
	} else {
		if isTransient {
			isTransient = false
			computeSpectrum(false)
			copy(logE2, logE)
		}
	}

	// intra/inter flag for coarse energy.
	intra := e.frameCount == 0
	if enc.ECTell()+3 <= totalBits {
		enc.EncodeBitLogp(intra, 3)
	}

	etr(enc, "intra")

	// Coarse band energies.
	quantLogE := QuantizeCoarseEnergy(enc, logE, e.prevBandEnergies, nil,
		intra, numBands, start, end, lm, ch, totalBits)
	etr(enc, "coarse")

	// Dynamic-allocation analysis (masking follower → per-band boosts and the
	// per-band importance weights consumed by tf_analysis). Only the DECISIONS are
	// computed here; the boost symbols are written later (dynallocEncode), after
	// spread, to keep the bitstream in decoder order.
	vbrOn := e.rateMode != RateModeCBR
	constrainedVBR := e.rateMode == RateModeCVBR
	offsets, importance := dynallocAnalysis(logE, logE2, numBands, end, ch, lm, isTransient, vbrOn, constrainedVBR)

	// Time-frequency resolution. tf_analysis runs a Viterbi search over per-band
	// L1 sparsity to pick tfRes[]/tf_select; libopus disables it (tf_res =
	// isTransient) at very low bitrate or below complexity 2.
	tfRes := make([]int, numBands)
	tfSelect := 0
	if targetBytes >= 15*ch && e.complexity >= 2 {
		lambda := 20480/targetBytes + 2
		if lambda < 80 {
			lambda = 80
		}
		tfSelect = tfAnalysis(end, isTransient, tfRes, lambda, X, frameLen, lm, tfChan, tfEstimate, importance)
	} else if isTransient {
		for i := start; i < end; i++ {
			tfRes[i] = 1
		}
	}
	tfEncode(enc, start, end, isTransient, tfRes, lm, tfSelect, totalBits)
	etr(enc, "tf_encode")

	// Spread decision (tonality-based). Complexity 0 forces SPREAD_NONE; otherwise
	// spreading_decision measures per-band tonality from the normalised spectrum.
	spread := spreadNormal
	if e.complexity == 0 {
		spread = spreadNone
	} else {
		spread = spreadingDecision(X, frameLen, end, ch, M, &e.tonalAverage, e.lastSpread)
	}
	if enc.ECTell()+4 <= totalBits {
		enc.EncodeIcdf(spread, spreadIcdf[:], 5)
	}
	e.lastSpread = spread
	etr(enc, "spread")

	// Dynamic allocation boosts (decided above; written here in decoder order).
	dynallocEncode(enc, offsets, numBands, start, end, lm, ch, totalBits)
	etr(enc, "dynalloc")

	// Allocation trim (spectral tilt + stereo correlation).
	surroundTrim := 0.0
	if len(e.energyMask) >= ch*numBands && start == 0 {
		maskEnd := max(2, e.lastCodedBands)
		if maskEnd > end {
			maskEnd = end
		}
		surroundTrim = surroundMaskTrim(e.energyMask, ch, numBands, maskEnd)
	}
	allocTrim := allocTrimAnalysis(X, logE, numBands, end, lm, ch, frameLen, end, tfEstimate, surroundTrim, e.bitrate)
	if enc.ECTell()+6 <= totalBits {
		enc.EncodeIcdf(allocTrim, TrimICDF[:], 7)
	}
	etr(enc, "alloc_trim")

	// libopus selects the hybrid VBR size only after coarse energy, TF,
	// spreading, dynamic allocation, and allocation trim have been coded. The
	// shrink must precede computeAllocationEncode so the encoder and decoder use
	// the same final packet length as their allocation budget.
	if shared && e.rateMode != RateModeCBR {
		tell := enc.TellFrac()
		totalBoost := 0
		for i := start; i < end; i++ {
			totalBoost += offsets[i]
		}

		// e.bitrate is the CELT share of the hybrid bitrate. vbrRate and target
		// use the libopus Q3-bit domain.
		vbrRate := e.bitrate * frameSize / e.mode.SampleRate << 3
		baseTarget := vbrRate - ((9*ch + 4) << 3)
		if baseTarget < 0 {
			baseTarget = 0
		}
		target := baseTarget + int(math.Round((tfEstimate-0.25)*float64(50<<3)))
		if tfEstimate > 0.7 && target < 50<<3 {
			target = 50 << 3
		}
		target += tell

		minAllowed := (tell+totalBoost+(1<<6)-1)/(1<<6) + 2
		hybridMin := (tell0Frac + (37 << 3) + totalBoost + (1 << 6) - 1) / (1 << 6)
		if minAllowed < hybridMin {
			minAllowed = hybridMin
		}
		targetBytes = (target + (1 << 5)) >> 6
		// Preserve the existing hybrid high-band activity calibration while
		// moving it to the libopus VBR decision point. The SILK prefix plus a
		// small mandatory CELT floor is the low-activity target; active high-band
		// content interpolates toward base_target.
		silkBytes := (tell0Frac + (1 << 6) - 1) >> 6
		activityFloor := silkBytes + 10
		if activityFloor < targetBytes {
			activity := hybridHighBandActivity(samples, ch)
			targetBytes = activityFloor + int(activity*float64(targetBytes-activityFloor)+0.5)
		}
		if targetBytes < minAllowed {
			targetBytes = minAllowed
		}
		if inputSilence {
			targetBytes = minAllowed
		}
		if targetBytes > maxTargetBytes {
			targetBytes = maxTargetBytes
		}
		enc.Shrink(targetBytes)
		totalBits = targetBytes * 8
	}

	// === Bit allocation, fine energy, PVQ, anti-collapse, final fine ===
	bitsQ3 := totalBits<<3 - enc.TellFrac() - 1
	antiCollapseRsv := 0
	if isTransient && lm >= 2 && bitsQ3 >= (lm+2)<<3 {
		antiCollapseRsv = 1 << 3
	}
	bitsQ3 -= antiCollapseRsv
	if bitsQ3 < 0 {
		bitsQ3 = 0
	}

	// Stereo coding decisions (C==2 only). Both are written into the stream by
	// computeAllocationEncode and read back by the decoder, so any in-range choice
	// round-trips; these heuristics only shape stereo quality.
	encIntensity := end
	encDualStereo := false
	if ch == 2 {
		// Dual stereo (independent L/R) vs joint mid/side, from the L/R-vs-M/S
		// entropy proxy. LM>0 always here (20 ms frames), matching libopus' LM!=0
		// guard for running this analysis.
		encDualStereo = stereoAnalysis(X, frameLen, lm)

		// Intensity-stereo starting band from the equivalent bitrate (libopus
		// hysteresis_decision over equiv_rate in kbps). For our fixed-LM frames the
		// (40*C+20)*((400>>LM)-50) correction term is zero (400>>LM == 50 at LM=3).
		equivRate := targetBytes * 8 * 50
		if shift := 3 - lm; shift > 0 {
			equivRate >>= uint(shift)
		} else if shift < 0 {
			equivRate <<= uint(-shift)
		}
		if e.bitrate > 0 {
			corr := (40*ch + 20) * ((400 >> uint(lm)) - 50)
			if r := e.bitrate - corr; r < equivRate {
				equivRate = r
			}
		}
		e.intensity = hysteresisDecision(equivRate/1000, intensityThresholds[:],
			intensityHysteresis[:], len(intensityThresholds), e.intensity)
		encIntensity = e.intensity
		if encIntensity < start {
			encIntensity = start
		}
		if encIntensity > end {
			encIntensity = end
		}
	}
	pulses, eBits, finePriority, balance, intensity, codedBands, dualStereo :=
		computeAllocationEncode(enc, encIntensity, encDualStereo,
			numBands, start, end, lm, ch, allocTrim, bitsQ3, offsets)
	e.lastCodedBands = codedBands

	if encDebug && e.frameCount == 10 {
		fmt.Fprintf(os.Stderr, "[ENC] bitsQ3=%d codedBands=%d\n", bitsQ3, codedBands)
		for i := start; i < end; i++ {
			N := M * int(EBands48000[i+1]-EBands48000[i])
			q := celtBits2PulsesQ3(i, lm, pulses[i])
			fmt.Fprintf(os.Stderr, "[ENC] band %d N=%d bandE=%.4f pulsesQ3=%d K=%d eBits=%d\n",
				i, N, bandE[i], pulses[i], getPulses(q), eBits[i])
		}
	}

	// Fine energy — raw bits to packet end, FORWARD band order. Track the
	// residual error so the final-fine pass and the next-frame predictor match
	// what the decoder reconstructs.
	fineError := make([]float64, ch*numBands)
	for idx := range fineError {
		fineError[idx] = logE[idx] - quantLogE[idx]
	}
	for i := start; i < end; i++ {
		fb := eBits[i]
		if fb <= 0 {
			continue
		}
		frac := 1 << uint(fb)
		for c := 0; c < ch; c++ {
			idx := c*numBands + i
			q2 := int(math.Floor((fineError[idx] + 0.5) * float64(frac)))
			if q2 > frac-1 {
				q2 = frac - 1
			}
			if q2 < 0 {
				q2 = 0
			}
			enc.EncodeBits(uint32(q2), uint(fb))
			offset := (float64(q2)+0.5)/float64(frac) - 0.5
			quantLogE[idx] += offset
			fineError[idx] -= offset
		}
	}

	// PVQ for all bands.
	var Y []float64
	if ch == 2 {
		Y = X[frameLen:]
	}
	collapse := make([]byte, numBands*ch)
	totalBitsQ3 := totalBits<<3 - antiCollapseRsv
	e.foldSeed = QuantAllBandsEncode(enc, bandE, start, end, X[:frameLen], Y, collapse,
		pulses, isTransient, spread, dualStereo, intensity, tfRes,
		totalBitsQ3, balance, lm, codedBands, e.foldSeed, e.disableInv)

	// Anti-collapse bit (raw, reserved only on transients with LM>=2). libopus
	// enables anti-collapse while the run of consecutive transients is short
	// (consec_transient < 2); the decoder applies it using the PVQ collapse masks.
	// consecTransient is read here (pre-update) and advanced at end of frame.
	if antiCollapseRsv > 0 {
		antiCollapseOn := uint32(0)
		if e.consecTransient < 2 {
			antiCollapseOn = 1
		}
		enc.EncodeBits(antiCollapseOn, 1)
	}

	// Final fine energy — one extra bit per (band,channel) by priority.
	bitsLeft := totalBits - enc.ECTell()
	for prio := 0; prio < 2; prio++ {
		for i := start; i < end && bitsLeft >= ch; i++ {
			if eBits[i] >= MaxFineEnergy || finePriority[i] != prio {
				continue
			}
			for c := 0; c < ch; c++ {
				idx := c*numBands + i
				q2 := 0
				if fineError[idx] >= 0 {
					q2 = 1
				}
				enc.EncodeBits(uint32(q2), 1)
				offset := (float64(q2) - 0.5) * math.Exp2(float64(-eBits[i]-1))
				quantLogE[idx] += offset
				fineError[idx] -= offset
				bitsLeft--
			}
		}
	}

	e.finalRange = enc.GetRng()

	// Update the inter-frame predictor with the fine-corrected energies.
	for idx := range quantLogE {
		v := quantLogE[idx]
		if v < -28.0 {
			v = -28.0
		}
		e.prevBandEnergies[idx] = v
	}
	e.frameCount++
	// Advance the consecutive-transient run (libopus updates consec_transient at
	// end of frame; the anti-collapse decision above used the pre-update value).
	if isTransient {
		e.consecTransient++
	} else {
		e.consecTransient = 0
	}

	etr(enc, "final")
	if shared {
		return targetBytes, nil, nil
	}
	enc.Flush()

	// --- Rate mode: determine final packet size ---
	switch e.rateMode {
	case RateModeVBR:
		// VBR packet size is exactly the chosen targetBytes.
		// The variance comes from targetBytes being adjusted based on
		// signal activity before allocation.

	case RateModeCVBR:
		// Update CVBR reservoir.
		// targetBytes is the actual chosen size for this frame.
		// maxTargetBytes is the nominal CBR target.
		targetBitsQ8 := maxTargetBytes << (3 + 3)
		usedBitsQ8 := targetBytes << (3 + 3)

		// maxReservoir limits how much the average can drift from the target.
		maxReservoir := 3 * targetBitsQ8
		if maxReservoir < 16<<3 {
			maxReservoir = 16 << 3
		}

		delta := targetBitsQ8 - usedBitsQ8 // positive = underspend (surplus)

		newOffset := e.vbrOffset + delta
		if newOffset > maxReservoir {
			newOffset = maxReservoir
		}
		if newOffset < -maxReservoir {
			newOffset = -maxReservoir
		}
		e.vbrOffset = newOffset

	default:
		// CBR
	}

	out := enc.Bytes()
	// Bytes() already returns max(capacity, range_front+raw_tail), so a genuine
	// over-budget frame is reflected in len(out); the merge byte is shared between
	// the range front and the raw tail, so the physical size is NOT (ec_tell+7)/8
	// (that would double-count the shared byte). Just pad up to the committed
	// target — for VBR this is the shrunk size, for CBR the full budget.
	if len(out) < targetBytes {
		padded := make([]byte, targetBytes)
		copy(padded, out)
		out = padded
	}
	return targetBytes, out, nil
}

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
	activity := math.Sqrt(hpEnergy / energy / 2.0)
	if activity > 1 {
		activity = 1
	}
	return activity
}

// encodeSilenceFrame emits a CELT frame whose only bitstream content is the
// silence flag (logp 15, set true). The decoder (decodeCELTRange) reads the flag,
// advances its tell to the packet end so every later symbol's budget guard fails,
// and forces all band energies to the -28 dB floor — reconstructing digital
// silence. We mirror that state here: the inter-frame coarse-energy predictor
// goes to the -28 floor and the fold seed/final range take the range value right
// after the silence bit, matching the decoder's post-frame state exactly.
//
// Packet sizing: VBR/CVBR (and DTX) keep the minimal flushed packet; plain CBR
// with DTX off pads to the full target so the constant-bitrate contract holds.
func (e *Encoder) encodeSilenceFrame(maxTargetBytes int) ([]byte, error) {
	enc := entcode.NewEncoder(maxTargetBytes)
	enc.EncodeBitLogp(true, 15)

	e.foldSeed = enc.GetRng()
	e.finalRange = enc.GetRng()
	for idx := range e.prevBandEnergies {
		e.prevBandEnergies[idx] = -28.0
	}
	e.frameCount++
	// A silent frame is non-transient: reset the run.
	e.consecTransient = 0

	etr(enc, "silence(true)")
	enc.Flush()
	out := enc.Bytes()

	padTo := 0
	if e.rateMode == RateModeCBR && !e.dtx {
		padTo = maxTargetBytes
	}
	if len(out) < padTo {
		padded := make([]byte, padTo)
		copy(padded, out)
		out = padded
	}
	return out, nil
}

// Reset resets the encoder state.
func (e *Encoder) Reset() {
	for c := range e.overlap {
		for i := range e.overlap[c] {
			e.overlap[c][i] = 0
		}
		e.preemphMem[c] = 0
	}
	for i := range e.prevBandEnergies {
		e.prevBandEnergies[i] = 0
	}
	e.foldSeed = 0
	e.frameCount = 0
	e.tonalAverage = 256
	e.lastSpread = spreadNormal
	e.consecTransient = 0
	e.intensity = 0
	e.lastCodedBands = 0
	e.vbrOffset = 0
	e.vbrCount = 0
	e.vbrDriftComp = 0
}

// SetPhaseInversionDisabled controls intensity-stereo phase inversion.
func (e *Encoder) SetPhaseInversionDisabled(disabled bool) {
	e.disableInv = disabled
}

// PhaseInversionDisabled reports the intensity-stereo phase inversion setting.
func (e *Encoder) PhaseInversionDisabled() bool {
	return e.disableInv
}

// CopyStateFrom copies the inter-frame prediction state from src into e, the
// encoder-side mirror of the decoder's CopyStateFrom. libopus carries its single
// celt_enc across mode transitions; this codebase uses a dedicated 5 ms encoder
// for the redundant frame, so the state must be threaded explicitly:
//
//   - CELT->SILK leading redundancy: the redundant frame continues from the
//     previous CELT-only state (seed = celtEncoder) so it matches the decoder,
//     which decodes it with its previous CELT state.
//   - SILK->CELT trailing redundancy: the next CELT-only packet continues from
//     the trailing redundant frame's state (seed = redundancyCelt), matching the
//     decoder's celtDec.CopyStateFrom(redDec).
//
// Only streaming/prediction state is copied; the configuration (mode, bitrate,
// complexity, rate mode, end band) is left intact. The copy is channel/band
// clamped so a 5 ms encoder and a longer-frame encoder (identical 48 kHz overlap
// and band count) interoperate.
func (e *Encoder) CopyStateFrom(src *Encoder) {
	if src == nil || src == e {
		return
	}

	for c := range e.overlap {
		sc := c
		if sc >= len(src.overlap) {
			sc = len(src.overlap) - 1
		}
		if sc >= 0 {
			copy(e.overlap[c], src.overlap[sc])
		}
	}

	dstBands := e.mode.Bands.NumBands
	srcBands := src.mode.Bands.NumBands
	dstHistCh := 0
	if dstBands > 0 {
		dstHistCh = len(e.prevBandEnergies) / dstBands
	}
	srcHistCh := 0
	if srcBands > 0 {
		srcHistCh = len(src.prevBandEnergies) / srcBands
	}
	for c := 0; c < dstHistCh; c++ {
		sc := c
		if sc >= srcHistCh {
			sc = srcHistCh - 1
		}
		for i := 0; i < dstBands; i++ {
			di := c*dstBands + i
			if i >= srcBands || sc < 0 {
				e.prevBandEnergies[di] = 0
				continue
			}
			e.prevBandEnergies[di] = src.prevBandEnergies[sc*srcBands+i]
		}
	}

	for c := range e.preemphMem {
		sc := c
		if sc >= len(src.preemphMem) {
			sc = len(src.preemphMem) - 1
		}
		if sc >= 0 {
			e.preemphMem[c] = src.preemphMem[sc]
		}
	}

	e.foldSeed = src.foldSeed
	e.finalRange = src.finalRange
	e.frameCount = src.frameCount
	e.tonalAverage = src.tonalAverage
	e.lastSpread = src.lastSpread
	e.consecTransient = src.consecTransient
	e.intensity = src.intensity
	e.lastCodedBands = src.lastCodedBands
}

// SetBitrate sets the target bitrate.
func (e *Encoder) SetBitrate(bitrate int) {
	if bitrate > 0 {
		e.bitrate = bitrate
	}
}

// SetComplexity sets the encoding complexity.
func (e *Encoder) SetComplexity(complexity int) {
	if complexity >= 0 && complexity <= 10 {
		e.complexity = complexity
	}
}

// SetRateMode sets the rate control mode.
func (e *Encoder) SetRateMode(mode RateMode) {
	e.rateMode = mode
}

// SetSignalType sets the content hint used by application-driven heuristics (the
// patch-transient sensitivity). It does not change the bitstream layout, so any
// value round-trips; it only shapes encoder decisions.
func (e *Encoder) SetSignalType(t SignalType) {
	e.signalType = t
}

// SignalTypeHint reports the current content hint.
func (e *Encoder) SignalTypeHint() SignalType { return e.signalType }

// SetEndBand sets the number of coded bands (the CELT "end" band), limiting the
// coded bandwidth. Pass 0 (or a value >= the full band count) for full band. The
// value must match the band count the decoder derives from the packet's TOC
// config bandwidth (NB=13, WB=17, SWB=19, FB=21).
func (e *Encoder) SetEndBand(n int) {
	if n < 0 {
		n = 0
	}
	e.endBand = n
}

// SetDTX enables or disables discontinuous transmission. When enabled, silent
// frames are emitted as minimal packets even in CBR mode (otherwise CBR pads
// silent frames to the target size to preserve the fixed-rate contract).
func (e *Encoder) SetDTX(enabled bool) { e.dtx = enabled }

// DTX reports whether discontinuous transmission is enabled.
func (e *Encoder) DTX() bool { return e.dtx }

// RateMode returns the current rate control mode.
func (e *Encoder) GetRateMode() RateMode { return e.rateMode }
