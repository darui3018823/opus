package silk

import (
	"fmt"
	"os"

	"github.com/darui3018823/opus/internal/entcode"
)

var lbrrDebug = os.Getenv("OPUS_LBRR_DEBUG") != ""

// leakFingerprint summarises the encoder state that influences the regular
// encode of subsequent frames, for debugging LBRR-generation state isolation.
func (e *Encoder) leakFingerprint() string {
	sum := func(xs []int32) int64 {
		var s int64
		for _, v := range xs {
			s += int64(v)
		}
		return s
	}
	return fmt.Sprintf("pPitch=%d pLagIx=%d pGainQ16=%d pGainIdx=%d pSig=%d pLagPitch=%d ffar=%v ltpSum=%.4f harm=%.4f tilt=%.4f lpc=%d ltp=%d xq=%d sLTP=%d nsqGain=%d ltpCorr=%.5f lagPrev=%d ltpBuf=%d shpBuf=%d lfar=%d diff=%d sLPC=%d sAR2=%d rewhite=%v curLag=%d curCont=%d nsqSeed=%d pGains=%.4f",
		e.prevPitchLag, e.prevLagIndex, e.prevGainQ16, e.prevGainIdx, e.prevSignalType, e.prevLagForPitch,
		e.firstFrameAfterReset, e.ltpSumLogGainQ7, e.shapeHarmSmooth, e.shapeTiltSmooth,
		sum(e.lpcState), sum(e.ltpState), sum32to64(e.nsq.xq), sum(e.nsq.sLTPShpQ14), e.nsq.prevGainQ16, e.ltpCorrState,
		e.nsq.lagPrev, e.nsq.sLTPBufIdx, e.nsq.sLTPShpBufIdx, e.nsq.sLFARShpQ14, e.nsq.sDiffShpQ14,
		sum(e.nsq.sLPCQ14[:]), sum(e.nsq.sAR2Q14[:]), e.nsq.rewhiteFlag,
		e.curPitchLagIndex, e.curPitchContourIndex, e.nsqSeed, sumFloats(e.prevGains))
}

func sum32to64(xs []int16) int64 {
	var s int64
	for _, v := range xs {
		s += int64(v)
	}
	return s
}

func sumFloats(xs []float64) float64 {
	var s float64
	for _, v := range xs {
		s += v
	}
	return s
}

// Inband Low Bitrate Redundancy (LBRR / in-band FEC) for the SILK encoder.
//
// LBRR carries a coarse, independently-decodable copy of an earlier frame so a
// decoder can reconstruct it via its decode_fec path after the original packet
// is lost. This mirrors libopus' SILK design (silk_LBRR_encode_FLP +
// enc_API.c): each frame, while it is regularly encoded, is *also* re-quantized
// at a lower bitrate (gains bumped up) and buffered. The buffered redundancy is
// then written at the front of the *next* packet — the one-packet FEC delay.
//
// This file contains the encoder-side buffering and replay path. Decoder-side
// LBRR extraction is implemented in decoder.go.

// lbrrSpeechActivityThres mirrors LBRR_SPEECH_ACTIVITY_THRES (tuning_parameters.h):
// LBRR is only coded for frames with sufficient speech activity.
const lbrrSpeechActivityThres = 0.3

// lbrrFrameData is the buffered side information + excitation of one LBRR frame,
// captured from the regular encode and replayed into the next packet.
type lbrrFrameData struct {
	present         bool // an LBRR copy exists for this frame slot
	condIndependent bool // coded independently (run start) vs conditionally
	signalType      int
	quantOffset     int

	// Gains: gainSym0 is the first-subframe symbol (an absolute index when
	// condIndependent, otherwise a raw delta); gainDeltas are the remaining
	// subframes' raw deltas. They reproduce the bumped LBRR gains exactly.
	gainSym0   int
	gainDeltas []int

	nlsf nlsfAnalysis

	// Voiced-only pitch/LTP indices (replayed verbatim).
	lagHigh    int
	lagLow     int
	contour    int
	ltpPerIdx  int
	ltpGainIdx []int

	seed   int32
	pulses []int16
}

// SetInbandFEC enables or disables SILK inband FEC (LBRR). When enabled and the
// per-frame speech activity is high enough, each packet carries a redundant
// copy of the previous packet's frame(s). Default: disabled.
func (e *Encoder) SetInbandFEC(enabled bool) {
	e.lbrrEnabled = enabled
	if !enabled {
		e.pendingLBRR = nil
		e.curLBRR = nil
		e.pendingLBRRFrames = 0
		e.pendingLBRRStereoPred = nil
		e.lbrrInPrevPacket = false
		e.lbrrRunPrevGainIdx = 0
	}
	if e.side != nil {
		e.side.SetInbandFEC(enabled)
	}
}

// SetPacketLossPerc sets the expected packet-loss percentage (0..100). It feeds
// the LBRR gain-increase schedule (more loss → smaller, cheaper LBRR frames).
func (e *Encoder) SetPacketLossPerc(perc int) {
	if perc < 0 {
		perc = 0
	}
	if perc > 100 {
		perc = 100
	}
	e.packetLossPerc = perc
	if e.side != nil {
		e.side.packetLossPerc = perc
	}
}

// lbrrGainIncreasesValue mirrors silk_setup_LBRR: 7 when the previous packet had
// no LBRR (it was coded at a higher bitrate), otherwise a packet-loss-scaled
// value floored at 3.
func (e *Encoder) lbrrGainIncreasesValue() int {
	if !e.lbrrInPrevPacket {
		return 7
	}
	// silk_SMULWB(PacketLoss_perc, SILK_FIX_CONST(0.2, 16)); 0.2*2^16 ≈ 13107.
	g := 7 - ((e.packetLossPerc * 13107) >> 16)
	if g < 3 {
		g = 3
	}
	return g
}

// beginLBRRPacket resets the per-packet LBRR accumulation state.
func (e *Encoder) beginLBRRPacket() {
	e.curLBRR = e.curLBRR[:0]
	e.lbrrRunPrevGainIdx = 0
}

// finishLBRRPacket promotes the LBRR frames generated for the just-encoded
// packet to "pending", so they are emitted at the front of the next packet.
func (e *Encoder) finishLBRRPacket(nFrames int) {
	any := false
	for i := range e.curLBRR {
		if e.curLBRR[i].present {
			any = true
			break
		}
	}
	if any {
		e.pendingLBRR = append([]lbrrFrameData(nil), e.curLBRR...)
		e.pendingLBRRFrames = nFrames
		e.lbrrInPrevPacket = true
	} else {
		e.pendingLBRR = nil
		e.pendingLBRRFrames = 0
		e.lbrrInPrevPacket = false
	}
}

// computeLBRRGains derives the LBRR gain symbols and the absolute gain indices
// the decoder will reconstruct, from the regular frame's absolute gain indices.
// At an LBRR run start (condIndependent) the first index is bumped up by
// gainIncreases to lower the redundant bitrate (silk_LBRR_encode_FLP). The
// returned gAbs feeds the redundant NSQ so the encoder's gains match the decoder.
func computeLBRRGains(gainAbs []int, condIndependent bool, gainIncreases, runPrevGainIdx int) (sym0 int, deltas []int, gAbs []int) {
	n := len(gainAbs)
	if n == 0 {
		return 0, nil, nil
	}
	gAbs = make([]int, n)
	if condIndependent {
		sym0 = clampInt(gainAbs[0]+gainIncreases, 0, NLevelsQGain-1)
		gAbs[0] = sym0
	} else {
		d := clampDeltaGain(gainAbs[0] - runPrevGainIdx)
		sym0 = d
		gAbs[0] = applyQuantizedGainDelta(runPrevGainIdx, d)
	}
	for sf := 1; sf < n; sf++ {
		d := clampDeltaGain(gainAbs[sf] - gainAbs[sf-1])
		deltas = append(deltas, d)
		gAbs[sf] = applyQuantizedGainDelta(gAbs[sf-1], d)
	}
	return sym0, deltas, gAbs
}

func clampDeltaGain(d int) int {
	if d < MinDeltaGainQuant {
		return MinDeltaGainQuant
	}
	if d > MaxDeltaGainQuant {
		return MaxDeltaGainQuant
	}
	return d
}

// generateLBRRFrame is called from encodeRangeFrame after a frame has been
// regularly encoded. It builds (but does not emit) the redundant copy of this
// frame, reusing the regular side information with bumped gains and a fresh NSQ
// pass from the pre-frame state (so the main, post-frame state is untouched).
func (e *Encoder) generateLBRRFrame(
	signal []float64,
	signalType, quantOffset int,
	gainIndices []int,
	nlsf nlsfAnalysis,
	pitchLags []int,
	ltpCoeffsQ14 [][5]int16,
	ltpScaleQ14 int16,
	rateScale float64,
	preFrame encoderFrameState,
) {
	if !e.lbrrEnabled {
		return
	}
	// LBRR is coded only for active frames with sufficient speech activity. The
	// active requirement is essential, not just an optimisation: a SILK LBRR
	// frame must have typeOffset >= 2 (silk_encode_indices asserts this), so an
	// inactive signal type would be miscoded as unvoiced by the decoder and
	// desync the gain ICDF. libopus' single VAD keeps these consistent; this
	// encoder has two (per-frame VAD vs silk speech-activity), so gate on both.
	fr := lbrrFrameData{
		present:     e.speechActivity > lbrrSpeechActivityThres && signalType != SignalTypeInactive,
		signalType:  signalType,
		quantOffset: quantOffset,
	}
	if !fr.present {
		e.curLBRR = append(e.curLBRR, fr)
		return
	}

	fr.condIndependent = len(e.curLBRR) == 0 || !e.curLBRR[len(e.curLBRR)-1].present
	gainIncreases := e.lbrrGainIncreasesValue()
	sym0, deltas, gAbs := computeLBRRGains(gainIndices, fr.condIndependent, gainIncreases, e.lbrrRunPrevGainIdx)
	fr.gainSym0 = sym0
	fr.gainDeltas = deltas

	// Re-quantize the excitation at the bumped gains from the pre-frame encoder
	// state, then restore the post-frame state the regular path produced.
	saved := e.snapshotFrameState()
	savedSeed := e.nsqSeed
	e.restoreFrameState(preFrame)
	e.nsqSeed = 0
	lbrrRateScale := rateScale
	if lbrrRateScale < 16 {
		// The redundant copy intentionally operates at a much lower rate than
		// the primary frame. The gain bump alone is not enough for this
		// encoder's trellis to reach the LBRR operating point, so raise Lambda
		// to suppress low-value pulses while retaining the same side data.
		lbrrRateScale = 16
	}
	pulses := e.closedLoopNSQWithRateScale(signal, nlsf.lpcQ12, nlsf.lpcQ12Interp, gAbs,
		signalType, quantOffset, 0, pitchLags, ltpCoeffsQ14, ltpScaleQ14, lbrrRateScale)
	fr.seed = e.nsqSeed
	e.restoreFrameState(saved)
	e.nsqSeed = savedSeed

	fr.pulses = append([]int16(nil), pulses...)
	fr.nlsf = nlsf
	if signalType == SignalTypeVoiced {
		fr.lagHigh = e.capLagHigh
		fr.lagLow = e.capLagLow
		fr.contour = e.capContour
		fr.ltpPerIdx = e.capLTPPerIdx
		fr.ltpGainIdx = append([]int(nil), e.capLTPGainIdx...)
	}

	if len(gAbs) > 0 {
		e.lbrrRunPrevGainIdx = gAbs[len(gAbs)-1]
	}
	e.curLBRR = append(e.curLBRR, fr)
}

// emitPendingLBRR writes the LBRR flag, the per-frame LBRR flags (for 2/3-frame
// packets), and the buffered LBRR frame bodies at the front of the current
// packet — immediately after the VAD flags, matching the SILK grammar. With FEC
// disabled (or no pending data) it writes a single 0 bit, identical to the prior
// hardcoded behaviour.
func (e *Encoder) emitPendingLBRR(enc *entcode.Encoder, nFrames int) {
	symbol := e.pendingLBRRSymbol(nFrames)
	lbrrFlag := symbol > 0
	enc.EncodeBitLogp(lbrrFlag, 1)
	if !lbrrFlag {
		return
	}
	if nFrames > 1 {
		if nFrames == 2 {
			enc.EncodeIcdf(symbol-1, silkLBRRFlags2ICDF[:], 8)
		} else {
			enc.EncodeIcdf(symbol-1, silkLBRRFlags3ICDF[:], 8)
		}
	}

	if lbrrDebug {
		pend := e.pendingLBRR
		fmt.Fprintf(os.Stderr, "[LBRR emit] nFrames=%d symbol=%#b flag=%v\n", nFrames, symbol, lbrrFlag)
		for i := range pend {
			fmt.Fprintf(os.Stderr, "  frame %d: present=%v condIndep=%v sig=%d qoff=%d gainSym0=%d nDeltas=%d nPulses=%d seed=%d\n",
				i, pend[i].present, pend[i].condIndependent, pend[i].signalType, pend[i].quantOffset,
				pend[i].gainSym0, len(pend[i].gainDeltas), len(pend[i].pulses), pend[i].seed)
		}
	}

	e.writePendingLBRRBodies(enc, symbol)
}

func (e *Encoder) pendingLBRRSymbol(nFrames int) int {
	if !e.lbrrEnabled || len(e.pendingLBRR) != nFrames || e.pendingLBRRFrames != nFrames {
		return 0
	}
	symbol := 0
	for i := range e.pendingLBRR {
		if e.pendingLBRR[i].present {
			symbol |= 1 << uint(i)
		}
	}
	return symbol
}

func (e *Encoder) writePendingLBRRMask(enc *entcode.Encoder, nFrames, symbol int) {
	if symbol == 0 || nFrames <= 1 {
		return
	}
	if nFrames == 2 {
		enc.EncodeIcdf(symbol-1, silkLBRRFlags2ICDF[:], 8)
	} else {
		enc.EncodeIcdf(symbol-1, silkLBRRFlags3ICDF[:], 8)
	}
}

func (e *Encoder) writePendingLBRRBodies(enc *entcode.Encoder, symbol int) {
	prevSignalType := -1
	for i := range e.pendingLBRR {
		if symbol&(1<<uint(i)) == 0 {
			continue
		}
		e.writeLBRRFrame(enc, e.pendingLBRR[i], prevSignalType)
		prevSignalType = e.pendingLBRR[i].signalType
	}
}

func (e *Encoder) appendMissingLBRRFrame() {
	if e.lbrrEnabled {
		e.curLBRR = append(e.curLBRR, lbrrFrameData{})
	}
}

// writeLBRRFrame replays one buffered LBRR frame's side information and pulses
// into the range coder, reproducing silk_encode_indices + silk_encode_pulses
// (encode_LBRR=1). prevSignalType is the previous emitted LBRR frame's signal
// type, used for the conditional pitch-lag delta-escape decision.
func (e *Encoder) writeLBRRFrame(enc *entcode.Encoder, fr lbrrFrameData, prevSignalType int) {
	cb := getNLSFCB(e.lpcOrder)

	// Signal type + quantizer offset (LBRR always uses the active/VAD table).
	e.encodeTypeOffset(enc, true, fr.signalType, fr.quantOffset)

	// Gains.
	if fr.condIndependent {
		enc.EncodeIcdf(fr.gainSym0>>3, silkGainICDF[fr.signalType][:], 8)
		enc.EncodeIcdf(fr.gainSym0&7, silkUniform8ICDF[:], 8)
	} else {
		enc.EncodeIcdf(fr.gainSym0-MinDeltaGainQuant, silkDeltaGainICDF[:], 8)
	}
	for _, d := range fr.gainDeltas {
		enc.EncodeIcdf(d-MinDeltaGainQuant, silkDeltaGainICDF[:], 8)
	}

	// NLSFs + interpolation factor.
	e.encodeNLSF(enc, cb, fr.signalType, fr.nlsf)
	if e.nSubframes == 4 {
		enc.EncodeIcdf(fr.nlsf.interpFactor, silkNLSFInterpFactorICDF[:], 8)
	}

	// Pitch lags + LTP (voiced only). This encoder always codes absolute lags.
	if fr.signalType == SignalTypeVoiced {
		fsKHz := e.sampleRate / 1000
		if !fr.condIndependent && prevSignalType == SignalTypeVoiced {
			enc.EncodeIcdf(0, silkPitchDeltaICDF[:], 8) // delta out of range → absolute
		}
		enc.EncodeIcdf(fr.lagHigh, silkPitchLagICDF[:], 8)
		encodePitchLagLowBits(enc, fsKHz, fr.lagLow)
		encodePitchContour(enc, fsKHz, e.nSubframes, fr.contour)
		enc.EncodeIcdf(fr.ltpPerIdx, silkLTPPerIndexICDF[:], 8)
		for sf := 0; sf < e.nSubframes; sf++ {
			idx := 0
			if sf < len(fr.ltpGainIdx) {
				idx = fr.ltpGainIdx[sf]
			}
			switch fr.ltpPerIdx {
			case 0:
				enc.EncodeIcdf(idx, silkLTPGainICDF0[:], 8)
			case 1:
				enc.EncodeIcdf(idx, silkLTPGainICDF1[:], 8)
			default:
				enc.EncodeIcdf(idx, silkLTPGainICDF2[:], 8)
			}
		}
		if fr.condIndependent {
			enc.EncodeIcdf(0, silkLTPScaleICDF[:], 8)
		}
	}

	// Seed + excitation.
	enc.EncodeIcdf(int(fr.seed), silkUniform4ICDF[:], 8)
	e.encodePulses(enc, fr.pulses, fr.signalType, fr.quantOffset)
}
