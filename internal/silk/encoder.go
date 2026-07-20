package silk

import (
	"fmt"
	"math"
	"os"

	"github.com/darui3018823/opus/internal/entcode"
)

// RateMode describes the packet-size contract supplied by the top-level Opus
// encoder. Both VBR modes may use the natural SNR-target size; CBR must stay on
// the budget-fitting path so packetization can pad every active stream equally.
type RateMode int

const (
	RateModeCBR RateMode = iota
	RateModeVBR
	RateModeCVBR
)

// Encoder represents a SILK encoder instance
type Encoder struct {
	sampleRate     int  // Sample rate (8000, 12000, 16000, 24000)
	frameSize      int  // Frame size in samples
	frameMs        int  // Frame duration in milliseconds (10 or 20)
	nSubframes     int  // Number of SILK subframes in one frame
	packetFrames   int  // Number of SILK frames in the packet currently being encoded
	channels       int  // Number of channels (1 or 2)
	lpcOrder       int  // LPC order based on bandwidth
	complexity     int  // Complexity (0-10)
	bitrate        int  // Target bitrate in bps
	vad            *VAD // Voice activity detector
	silkVAD        silkVADState
	speechActivity float64
	inputTilt      float64
	inputQuality   float64
	inputQualityB  [silkVADNBands]float64
	prevEnergy     float64   // Previous frame energy for smoothing
	prevLPC        []float64 // Previous LPC coefficients
	prevNLSF       []float64 // Previous NLSF
	prevNLSFQ15    []int16   // Previous quantized NLSF in Q15 (matches decoder prevNLSFQ15; used for interpolation search)
	prevPitchLag   int       // Previous pitch lag
	prevLagIndex   int       // Previous entropy-coded pitch lag index
	prevSignalType int       // Previous SILK signal type
	prevGains      []float64 // Previous subframe gains
	prevGainIdx    int       // Previous absolute gain index, matching decoder state
	prevGainQ16    int32     // Previous synthesis gain, matching decoder state
	lpcState       []int32   // Encoder-side LPC synthesis state, Q14
	ltpState       []int32   // Encoder-side LTP output history, Q0
	nsq            silkNSQState
	nsqDelDec      [4]nsqDelayedDecision
	nsqSeed        int32 // winning del-dec seed (silk_NSQ_del_dec writes this back to the bitstream)
	lastFinalRange uint32
	// useTrellisNSQ enables the FLP noise-shape analysis + delayed-decision
	// trellis NSQ (Q3+Q4) for active frames. Voiced frames use the perceptual
	// shaping path; unvoiced/stereo-component frames keep neutral shaping while
	// retaining the delayed-decision rate/distortion search.
	useTrellisNSQ bool
	// snrTargetEnabled records the OPUS_SILK_RC_SNR A/B selection.
	// useSNRTargetVBR is its rate-mode-gated effective value.
	rateMode         RateMode
	snrTargetEnabled bool
	useSNRTargetVBR  bool
	lastSNRVBRFrame  bool
	lastSNRVBRStream bool
	// stereoComponent marks the mid/side encoders of a stereo packet. The stereo
	// predictor path converts L/R to decoder-symmetric adaptive M/S before these
	// component encoders run.
	stereoComponent bool
	// hybridMode marks frames encoded as the SILK low band of a hybrid packet.
	// It gates off the Step 4 voiced trellis: the hybrid SILK+CELT energy balance
	// is a separate WIP and the trellis-coded low band is not yet conformant
	// there. Set per-call by the hybrid encoder.
	hybridMode      bool
	shapeHarmSmooth float64
	shapeTiltSmooth float64
	noiseShapeBuf   []float64
	side            *Encoder // side-channel encoder for stereo packets
	stereoState     stereoPredState
	prevOnlyMiddle  bool // previous stereo frame omitted the side channel

	// Pitch analysis state (silk_find_pitch_lags_FLP).
	pitchHist            []float64 // Past ltp_mem_length input samples, [-1,1]
	prevLagForPitch      int       // Previous frame pitch lag (0 if unvoiced)
	ltpCorrState         float64   // Normalized LTP correlation from prev frame
	firstFrameAfterReset bool      // True until the first frame after reset is encoded
	curPitchLagIndex     int       // Lag index selected for the current frame
	curPitchContourIndex int       // Pitch contour index for the current frame

	// ltpSumLogGainQ7 is the cumulative log prediction gain across subframes
	// (silk sum_log_gain_Q7), limiting the total LTP gain for stability.
	ltpSumLogGainQ7 float64

	// ── Inband Low Bitrate Redundancy (LBRR / in-band FEC) ──────────────────
	// lbrrEnabled is the SILK LBRR_coded gate (set by the top-level FEC
	// decision); packetLossPerc feeds the LBRR gain-increase schedule.
	lbrrEnabled    bool
	packetLossPerc int
	// lbrrInPrevPacket records whether the previous packet generated LBRR data,
	// selecting the LBRR_GainIncreases schedule (silk_setup_LBRR).
	lbrrInPrevPacket bool
	// pendingLBRR holds the LBRR frames generated while encoding the previous
	// packet; they are emitted at the front of the current packet (the cross-
	// packet one-packet FEC delay). curLBRR accumulates the current packet's
	// LBRR frames. lbrrPlanned is set when curLBRR has at least one frame.
	pendingLBRR        []lbrrFrameData
	curLBRR            []lbrrFrameData
	pendingLBRRFrames  int // frame count the pending LBRR data was generated for
	lbrrRunPrevGainIdx int // running gain index within the current LBRR run
	// pendingLBRRStereoPred carries the M/S predictor indices for the frames in
	// pendingLBRR. Stereo LBRR syntax writes these controls frame-by-frame
	// before the corresponding mid/side redundant bodies.
	pendingLBRRStereoPred [][2][3]int8
	// lbrrBitsPerFrame is the current packet's emitted LBRR cost divided across
	// its regular frames. silkFrameTargetBits subtracts it so CBR/CVBR do not
	// simply add redundancy on top of the configured bitrate.
	lbrrBitsPerFrame int
	// Capture of the current voiced frame's coded pitch/LTP indices, populated
	// by encodePitchAndLTP so the LBRR generator can replay them.
	capLagHigh    int
	capLagLow     int
	capContour    int
	capLTPPerIdx  int
	capLTPGainIdx []int
}

type nlsfAnalysis struct {
	cb1Idx       int
	rawIdx       []int
	nlsfQ15      []int16
	lpcQ12       []int16
	lpcQ12Interp []int16 // LPC from interpolated NLSF for subframes 0,1 (nil when interpFactor==4)
	interpFactor int
}

type lpcBurgDomain struct {
	signal      []float64
	subfrLength int
	nbSubfr     int
	minInvGain  float64
}

type encoderFrameState struct {
	prevPitchLag    int
	prevLagIndex    int
	prevGainQ16     int32
	lpcState        []int32
	ltpState        []int32
	nsq             silkNSQState
	shapeHarmSmooth float64
	shapeTiltSmooth float64
	ltpSumLogGainQ7 float64
}

type rateControlPlan struct {
	gainTargets []int
	gainIndices []int
	rateScale   float64
	snrVBR      bool
}

type silkShapeSubframe struct {
	feedback float64
	tilt     float64
	lf       float64
	hf       float64
	harmonic float64
	lambda   float64
}

type silkShapeAnalysis struct {
	subframes []silkShapeSubframe
}

// NewEncoder creates a new 20 ms SILK encoder.
func NewEncoder(sampleRate, channels int) (*Encoder, error) {
	return NewEncoderWithFrameMs(sampleRate, channels, 20)
}

// NewEncoderWithFrameMs creates a new SILK encoder for 10 ms or 20 ms frames.
func NewEncoderWithFrameMs(sampleRate, channels, frameMs int) (*Encoder, error) {
	if sampleRate != 8000 && sampleRate != 12000 && sampleRate != 16000 && sampleRate != 24000 {
		return nil, fmt.Errorf("invalid sample rate: %d (must be 8000, 12000, 16000, or 24000)", sampleRate)
	}
	if channels != 1 && channels != 2 {
		return nil, fmt.Errorf("invalid channels: %d (must be 1 or 2)", channels)
	}
	if frameMs != 10 && frameMs != 20 {
		return nil, fmt.Errorf("invalid frame duration: %d ms (must be 10 or 20)", frameMs)
	}

	lpcOrder := 10
	if sampleRate >= 16000 {
		lpcOrder = 16
	}

	frameSize := sampleRate * frameMs / 1000
	nSubframes := 4
	if frameMs == 10 {
		nSubframes = 2
	}

	prevNLSF := make([]float64, lpcOrder)
	prevNLSFQ15 := make([]int16, lpcOrder)
	for i := range prevNLSF {
		prevNLSF[i] = math.Pi * float64(i+1) / float64(lpcOrder+1)
		prevNLSFQ15[i] = int16((float64(i+1) / float64(lpcOrder+1)) * 32768.0)
	}

	enc := &Encoder{
		sampleRate:     sampleRate,
		frameSize:      frameSize,
		frameMs:        frameMs,
		nSubframes:     nSubframes,
		channels:       channels,
		lpcOrder:       lpcOrder,
		complexity:     5,
		bitrate:        sampleRate * channels * 16 / 8,
		vad:            NewVAD(),
		silkVAD:        newSilkVADState(),
		speechActivity: 1.0,
		inputQuality:   1.0,
		prevEnergy:     1.0,
		prevLPC:        make([]float64, lpcOrder),
		prevNLSF:       prevNLSF,
		prevPitchLag:   100,
		prevLagIndex:   0,
		prevSignalType: SignalTypeUnvoiced,
		prevGains:      []float64{1.0, 1.0, 1.0, 1.0},
		prevGainIdx:    10,
		prevGainQ16:    65536,
		lpcState:       make([]int32, silkMaxLPCOrder),
		ltpState:       make([]int32, silkLTPMemLengthMs*(sampleRate/1000)),
		nsq:            newSilkNSQState(frameSize, silkLTPMemLengthMs*(sampleRate/1000)),

		pitchHist:            make([]float64, peLtpMemLengthMs*(sampleRate/1000)),
		prevLagForPitch:      0,
		ltpCorrState:         0,
		firstFrameAfterReset: true,
		// Step 4: voiced frames use the delayed-decision trellis NSQ with gains
		// co-designed by silk_process_gains_FLP. OPUS_SILK_TRELLIS=0 forces the
		// legacy homebrew quantizer everywhere for A/B comparison.
		useTrellisNSQ:    os.Getenv("OPUS_SILK_TRELLIS") != "0",
		rateMode:         RateModeVBR,
		snrTargetEnabled: os.Getenv("OPUS_SILK_RC_SNR") != "0",
	}
	enc.useSNRTargetVBR = enc.snrTargetEnabled
	for i := range enc.inputQualityB {
		enc.inputQualityB[i] = 1.0
	}
	if channels == 2 {
		side, err := NewEncoderWithFrameMs(sampleRate, 1, frameMs)
		if err != nil {
			return nil, err
		}
		side.complexity = enc.complexity
		side.bitrate = enc.bitrate
		enc.stereoComponent = true
		side.stereoComponent = true
		enc.side = side
	}
	// Do not let the history smoother suppress a live onset. This applies to
	// both mono and stereo components; the VAD flags are written before the
	// per-frame symbols, so changing an accurately predicted flag does not alter
	// the encoder/decoder entropy ordering.
	enc.vad.immediateAttack = true
	if enc.side != nil {
		enc.side.vad.immediateAttack = true
	}

	return enc, nil
}

// SetComplexity sets the computational complexity (0-10)
func (e *Encoder) SetComplexity(complexity int) error {
	if complexity < 0 || complexity > 10 {
		return fmt.Errorf("complexity must be between 0 and 10, got %d", complexity)
	}
	e.complexity = complexity
	if e.side != nil {
		return e.side.SetComplexity(complexity)
	}
	return nil
}

// SetBitrate sets the target bitrate in bps
func (e *Encoder) SetBitrate(bitrate int) error {
	if bitrate < 6000 || bitrate > 40000 {
		return fmt.Errorf("bitrate must be between 6000 and 40000 bps, got %d", bitrate)
	}
	e.bitrate = bitrate
	if e.side != nil {
		return e.side.SetBitrate(bitrate)
	}
	return nil
}

// SetRateMode supplies the top-level Opus packet-size contract. The
// SNR-target natural-size path is available only in VBR/CVBR and remains
// independently disableable with OPUS_SILK_RC_SNR=0.
func (e *Encoder) SetRateMode(mode RateMode) {
	e.rateMode = mode
	e.useSNRTargetVBR = e.snrTargetEnabled && mode != RateModeCBR
	if e.side != nil {
		e.side.SetRateMode(mode)
	}
}

// Encode encodes a frame of audio samples using the range encoder.
func (e *Encoder) Encode(pcm []float64) ([]byte, error) {
	if len(pcm) != e.frameSize*e.channels {
		return nil, fmt.Errorf("invalid PCM length: got %d, expected %d", len(pcm), e.frameSize*e.channels)
	}

	return e.EncodeMulti(pcm, 1)
}

// EncodeMulti encodes n consecutive SILK frames into one shared range-coded
// stream. This mirrors the SILK packet structure used by Opus: VAD flags for
// all frames, LBRR flags, then frame payloads in order. Stereo input is encoded
// as adaptive mid/side, with predictor indices written before each frame.
func (e *Encoder) EncodeMulti(pcm []float64, nFrames int) ([]byte, error) {
	enc := entcode.NewEncoder(64)
	if err := e.EncodeMultiWithEncoder(enc, pcm, nFrames); err != nil {
		return nil, err
	}
	e.lastFinalRange = enc.GetRng()
	enc.Flush()
	return enc.Bytes(), nil
}

// EncodeMultiWithEncoder writes n consecutive SILK frames into an existing
// range encoder. It is used by hybrid Opus packets, where SILK and CELT share
// one entropy stream.
func (e *Encoder) EncodeMultiWithEncoder(enc *entcode.Encoder, pcm []float64, nFrames int) error {
	if enc == nil {
		return fmt.Errorf("nil range encoder")
	}
	if nFrames < 1 {
		return fmt.Errorf("invalid frame count: %d", nFrames)
	}
	expected := e.frameSize * e.channels * nFrames
	if len(pcm) != expected {
		return fmt.Errorf("invalid PCM length: got %d, expected %d", len(pcm), expected)
	}
	if e.channels == 2 {
		return e.encodeMultiStereoWithEncoder(enc, pcm, nFrames)
	}
	prevPacketFrames := e.packetFrames
	e.packetFrames = nFrames
	defer func() {
		e.packetFrames = prevPacketFrames
	}()

	frames := make([][]float64, nFrames)
	vadFlags := make([]bool, nFrames)
	for frame := 0; frame < nFrames; frame++ {
		start := frame * e.frameSize * e.channels
		framePCM := pcm[start : start+e.frameSize*e.channels]
		frames[frame] = framePCM
		vadFlags[frame] = e.vad.Detect(framePCM)
	}

	for _, active := range vadFlags {
		enc.EncodeBitLogp(active, 1)
	}
	// LBRR flag + redundant frames carried over from the previous packet (the
	// one-packet FEC delay). When FEC is disabled this writes a single 0 bit,
	// identical to the previous hardcoded behaviour.
	lbrrStartBits := enc.ECTell()
	e.emitPendingLBRR(enc, nFrames)
	lbrrBits := enc.ECTell() - lbrrStartBits
	e.lbrrBitsPerFrame = 0
	if lbrrBits > 1 {
		e.lbrrBitsPerFrame = (lbrrBits + nFrames - 1) / nFrames
	}
	if lbrrDebug {
		fmt.Fprintf(os.Stderr, "[LBRR budget] bits=%d perFrame=%d base=%d adjusted=%d\n",
			lbrrBits, e.lbrrBitsPerFrame, e.bitrate*e.frameMs/1000, e.silkFrameTargetBits())
	}

	e.beginLBRRPacket()
	e.lastSNRVBRStream = false
	for i, signal := range frames {
		e.encodeRangeFrame(enc, signal, vadFlags[i], i > 0)
		e.lastSNRVBRStream = e.lastSNRVBRStream || e.lastSNRVBRFrame
	}
	e.finishLBRRPacket(nFrames)
	return nil
}

func (e *Encoder) encodeMultiStereo(pcm []float64, nFrames int) ([]byte, error) {
	enc := entcode.NewEncoder(64)
	if err := e.encodeMultiStereoWithEncoder(enc, pcm, nFrames); err != nil {
		return nil, err
	}
	e.lastFinalRange = enc.GetRng()
	enc.Flush()
	return enc.Bytes(), nil
}

func (e *Encoder) encodeMultiStereoWithEncoder(enc *entcode.Encoder, pcm []float64, nFrames int) error {
	if e.side == nil {
		return fmt.Errorf("missing SILK side-channel encoder")
	}

	// A single-frame stereo packet can report a live onset immediately, which
	// ensures the frame reaches pitch analysis. Multi-frame stereo streams keep
	// the smoothed flags: their conditional-gain context is shared across all
	// frames, and changing the precomputed VAD pattern breaks libopus parity.
	e.vad.immediateAttack = nFrames == 1
	e.side.vad.immediateAttack = nFrames == 1

	midFrames := make([][]float64, nFrames)
	sideFrames := make([][]float64, nFrames)
	stereoPredIx := make([][2][3]int8, nFrames)
	vadFlags := [2][]bool{
		make([]bool, nFrames),
		make([]bool, nFrames),
	}
	for frame := 0; frame < nFrames; frame++ {
		base := frame * e.frameSize * 2
		mid, side, predIx := e.stereoState.lrToMS(pcm[base:base+e.frameSize*2], e.sampleRate/1000, e.frameSize)
		midFrames[frame] = mid
		sideFrames[frame] = side
		stereoPredIx[frame] = predIx
		vadFlags[0][frame] = e.vad.Detect(mid)
		vadFlags[1][frame] = e.side.vad.Detect(side)
	}

	for ch := 0; ch < 2; ch++ {
		for _, active := range vadFlags[ch] {
			enc.EncodeBitLogp(active, 1)
		}
		symbol := e.pendingLBRRSymbol(nFrames)
		if ch == 1 {
			symbol = e.side.pendingLBRRSymbol(nFrames)
		}
		enc.EncodeBitLogp(symbol != 0, 1)
	}
	lbrrSymbols := [2]int{
		e.pendingLBRRSymbol(nFrames),
		e.side.pendingLBRRSymbol(nFrames),
	}
	for ch, symbol := range lbrrSymbols {
		component := e
		if ch == 1 {
			component = e.side
		}
		component.writePendingLBRRMask(enc, nFrames, symbol)
	}
	// libopus writes stereo LBRR frame-major. A mid-channel redundant frame is
	// preceded by its stereo predictor and, when side LBRR is absent, the
	// mid-only flag. The channel bodies then follow mid before side.
	for frame := 0; frame < nFrames; frame++ {
		midPresent := lbrrSymbols[0]&(1<<uint(frame)) != 0
		sidePresent := lbrrSymbols[1]&(1<<uint(frame)) != 0
		if midPresent {
			var pred [2][3]int8
			if frame < len(e.pendingLBRRStereoPred) {
				pred = e.pendingLBRRStereoPred[frame]
			}
			encodeStereoPred(enc, pred)
			if !sidePresent {
				enc.EncodeIcdf(1, silkStereoOnlyCodeMidICDF[:], 8)
			}
			e.writeLBRRFrame(enc, e.pendingLBRR[frame], previousLBRRSignalType(e.pendingLBRR, lbrrSymbols[0], frame))
		}
		if sidePresent {
			e.side.writeLBRRFrame(enc, e.side.pendingLBRR[frame], previousLBRRSignalType(e.side.pendingLBRR, lbrrSymbols[1], frame))
		}
	}

	e.beginLBRRPacket()
	e.side.beginLBRRPacket()
	e.lastSNRVBRStream = false
	for i := 0; i < nFrames; i++ {
		encodeStereoPred(enc, stereoPredIx[i])
		onlyMiddle := false
		if !vadFlags[1][i] {
			onlyMiddle = true
			enc.EncodeIcdf(1, silkStereoOnlyCodeMidICDF[:], 8)
		}
		e.encodeRangeFrame(enc, midFrames[i], vadFlags[0][i], i > 0)
		e.lastSNRVBRStream = e.lastSNRVBRStream || e.lastSNRVBRFrame
		if !onlyMiddle {
			if e.prevOnlyMiddle {
				e.side.Reset()
			}
			e.side.encodeRangeFrame(enc, sideFrames[i], vadFlags[1][i], i > 0 && !e.prevOnlyMiddle)
			e.lastSNRVBRStream = e.lastSNRVBRStream || e.side.lastSNRVBRFrame
		} else {
			e.side.appendMissingLBRRFrame()
		}
		e.prevOnlyMiddle = onlyMiddle
	}
	e.finishLBRRPacket(nFrames)
	e.side.finishLBRRPacket(nFrames)
	e.pendingLBRRStereoPred = append(e.pendingLBRRStereoPred[:0], stereoPredIx...)
	return nil
}

func previousLBRRSignalType(frames []lbrrFrameData, symbol, frame int) int {
	for i := frame - 1; i >= 0; i-- {
		if symbol&(1<<uint(i)) != 0 {
			return frames[i].signalType
		}
	}
	return -1
}

func encodeStereoPred(enc *entcode.Encoder, ix [2][3]int8) {
	n := 5*int(ix[0][2]) + int(ix[1][2])
	enc.EncodeIcdf(n, silkStereoPredJointICDF[:], 8)
	for i := 0; i < 2; i++ {
		enc.EncodeIcdf(int(ix[i][0]), silkUniform3ICDF[:], 8)
		enc.EncodeIcdf(int(ix[i][1]), silkUniform5ICDF[:], 8)
	}
}

// encodeRangeFrame writes one structurally valid SILK frame. Slice 3 adds the
// first voiced path: simple pitch/LTP decisions are encoded before the seed,
// and pulse coding is driven from a short-term residual instead of raw samples.
func (e *Encoder) encodeRangeFrame(enc *entcode.Encoder, signal []float64, vadActive, conditionalGain bool) {
	initialState := e.snapshotFrameState()
	vadSA := e.silkVADGetSAQ8(signal)
	e.speechActivity = vadSA.speechActivity
	e.inputTilt = vadSA.inputTilt
	e.inputQuality = vadSA.inputQuality
	e.inputQualityB = vadSA.inputQualityBand

	signalType := SignalTypeInactive
	pitchLag := e.prevPitchLag
	pitchGain := 0.0
	e.curPitchLagIndex = 0
	e.curPitchContourIndex = 0
	if vadActive {
		signalType = SignalTypeUnvoiced
		voiced, lagIndex, contourIndex, ltpCorr := e.silkFindPitchLags(signal, e.speechActivity)
		if voiced {
			signalType = SignalTypeVoiced
			e.curPitchLagIndex = lagIndex
			e.curPitchContourIndex = contourIndex
			pitchGain = ltpCorr
		}
	}
	quantOffset := 0

	cb := getNLSFCB(e.lpcOrder)
	var domainGainTargets []int
	var domainConfig []lpcInPreConfig
	if !e.stereoComponent && !e.hybridMode && signalType != SignalTypeInactive {
		bootstrap := e.analyzeNLSF(signal, cb, signalType)
		quantOffset = e.estimateQuantOffsetType(signal, bootstrap.lpcQ12, signalType, pitchLag, pitchGain)
		pitchLags := make([]int, e.nSubframes)
		for sf := range pitchLags {
			pitchLags[sf] = pitchLag
		}
		var ltpCoeffsQ14 [][5]int16
		ltpPredCodGain := 0.0
		if signalType == SignalTypeVoiced {
			pitchLags = e.reconstructCurrentPitchLags()
			ltpSum := e.ltpSumLogGainQ7
			_, _, ltpCoeffsQ14, ltpPredCodGain = e.selectLTPGainsVQWithGain(signal, bootstrap.lpcQ12, pitchLags)
			e.ltpSumLogGainQ7 = ltpSum
		}
		gainTargets, shape := e.shapeGainAnalysis(signal, bootstrap.lpcQ12, nil, signalType, quantOffset, pitchLags, ltpCoeffsQ14, pitchGain)
		domainGainTargets = gainTargets
		gainIndices := e.resolveGainIndices(gainTargets, conditionalGain)
		invGains := invGainsFromIndices(gainIndices)
		domainConfig = []lpcInPreConfig{{
			input:           e.lpcInPreInput(signal),
			subframeLengths: equalSubframeLengths(e.frameSize, e.nSubframes),
			invGains:        invGains,
			ltpCoefs:        ltpQ14ToFloat(ltpCoeffsQ14),
			pitchLags:       pitchLags,
			ltpPredCodGain:  ltpPredCodGain,
			codingQuality:   shape.CodingQuality,
			voiced:          signalType == SignalTypeVoiced,
		}}
	}
	nlsf := e.analyzeNLSF(signal, cb, signalType, domainConfig...)
	if len(domainConfig) == 0 {
		quantOffset = e.estimateQuantOffsetType(signal, nlsf.lpcQ12, signalType, pitchLag, pitchGain)
	}

	plan := e.selectRateControlPlan(initialState, signal, vadActive, signalType, quantOffset, conditionalGain, nlsf, pitchLag, pitchGain, domainGainTargets)
	e.lastSNRVBRFrame = plan.snrVBR

	e.restoreFrameState(initialState)
	e.encodeTypeOffset(enc, vadActive, signalType, quantOffset)
	gainIndices := e.encodeGains(enc, signalType, plan.gainTargets, conditionalGain)
	e.encodeNLSF(enc, cb, signalType, nlsf)

	if e.nSubframes == 4 {
		enc.EncodeIcdf(nlsf.interpFactor, silkNLSFInterpFactorICDF[:], 8)
	}

	ltpCoeffsQ14 := make([][5]int16, e.nSubframes)
	ltpScaleQ14 := silkLTPScalesTable[0]
	pitchLags := make([]int, e.nSubframes)
	for sf := range pitchLags {
		pitchLags[sf] = pitchLag
	}
	if signalType == SignalTypeVoiced {
		ltpCoeffsQ14, ltpScaleQ14, pitchLags = e.encodePitchAndLTP(enc, signal, nlsf.lpcQ12, pitchGain, conditionalGain)
	}

	if len(plan.gainIndices) == len(gainIndices) {
		gainIndices = plan.gainIndices
	}
	// Run the delayed-decision NSQ before encoding the seed: the trellis selects
	// the winning state and its initial seed (e.nsqSeed), which libopus writes to
	// the bitstream so the decoder reproduces the same sign sequence.
	e.nsqSeed = 0
	pulses := e.closedLoopNSQWithRateScale(signal, nlsf.lpcQ12, nlsf.lpcQ12Interp, gainIndices,
		signalType, quantOffset, 0, pitchLags, ltpCoeffsQ14, ltpScaleQ14, plan.rateScale)
	enc.EncodeIcdf(int(e.nsqSeed), silkUniform4ICDF[:], 8)
	e.encodePulses(enc, pulses, signalType, quantOffset)

	// Low-Bitrate Redundancy: generate (but do not yet emit) a coarse redundant
	// copy of this frame. It is buffered and written at the front of the next
	// packet (the one-packet FEC delay). Mirrors silk_LBRR_encode_FLP: reuse the
	// regular side information, bump the gains, and re-run the quantizer.
	var leakBefore string
	if lbrrDebug {
		leakBefore = e.leakFingerprint()
	}
	e.generateLBRRFrame(signal, signalType, quantOffset, gainIndices, nlsf,
		pitchLags, ltpCoeffsQ14, ltpScaleQ14, plan.rateScale, initialState)
	if lbrrDebug {
		if after := e.leakFingerprint(); after != leakBefore {
			fmt.Fprintf(os.Stderr, "[LBRR LEAK]\n  before=%s\n  after =%s\n", leakBefore, after)
		}
	}

	e.prevNLSF = nlsfQ15ToRadians(nlsf.nlsfQ15)
	if len(e.prevNLSFQ15) == len(nlsf.nlsfQ15) {
		copy(e.prevNLSFQ15, nlsf.nlsfQ15)
	} else {
		e.prevNLSFQ15 = append([]int16(nil), nlsf.nlsfQ15...)
	}
	if len(gainIndices) > 0 {
		e.prevGainIdx = gainIndices[len(gainIndices)-1]
	}
	e.prevEnergy = computeEnergy(signal)
	e.prevSignalType = signalType
	if signalType == SignalTypeVoiced && len(pitchLags) > 0 {
		e.prevLagForPitch = pitchLags[len(pitchLags)-1]
	} else {
		e.prevLagForPitch = 0
	}
	e.updatePitchHist(signal)
	e.firstFrameAfterReset = false
}

func (e *Encoder) snapshotFrameState() encoderFrameState {
	return encoderFrameState{
		prevPitchLag:    e.prevPitchLag,
		prevLagIndex:    e.prevLagIndex,
		prevGainQ16:     e.prevGainQ16,
		lpcState:        append([]int32(nil), e.lpcState...),
		ltpState:        append([]int32(nil), e.ltpState...),
		nsq:             e.nsq.clone(),
		shapeHarmSmooth: e.shapeHarmSmooth,
		shapeTiltSmooth: e.shapeTiltSmooth,
		ltpSumLogGainQ7: e.ltpSumLogGainQ7,
	}
}

func (e *Encoder) restoreFrameState(st encoderFrameState) {
	e.prevPitchLag = st.prevPitchLag
	e.prevLagIndex = st.prevLagIndex
	e.prevGainQ16 = st.prevGainQ16
	if len(st.lpcState) == len(e.lpcState) {
		copy(e.lpcState, st.lpcState)
	}
	if len(st.ltpState) == len(e.ltpState) {
		copy(e.ltpState, st.ltpState)
	}
	e.nsq.copyFrom(st.nsq)
	e.shapeHarmSmooth = st.shapeHarmSmooth
	e.shapeTiltSmooth = st.shapeTiltSmooth
	e.ltpSumLogGainQ7 = st.ltpSumLogGainQ7
}

func (e *Encoder) selectRateControlPlan(
	initial encoderFrameState,
	signal []float64,
	vadActive bool,
	signalType, quantOffset int,
	conditionalGain bool,
	nlsf nlsfAnalysis,
	pitchLag int,
	pitchGain float64,
	domainGainTargets []int,
) rateControlPlan {
	baseTargets := e.analysisGainIndices(signal)
	if len(domainGainTargets) == e.nSubframes {
		baseTargets = append([]int(nil), domainGainTargets...)
	}
	baseIndices := e.resolveGainIndices(baseTargets, conditionalGain)
	if signalType == SignalTypeInactive {
		return rateControlPlan{gainTargets: baseTargets, gainIndices: baseIndices, rateScale: 1}
	}
	// When trellis is explicitly disabled, voiced SILK-only frames keep the
	// proven no-rate-control heuristic-gain path. Hybrid frames still run the
	// budget search so their homebrew NSQ cannot consume the shared SILK+CELT
	// packet budget.
	if signalType == SignalTypeVoiced && !e.voicedUsesTrellis() && !e.hybridMode {
		return rateControlPlan{gainTargets: baseTargets, gainIndices: baseIndices, rateScale: 1}
	}

	// Seed the rate-control search from excitation-normalized gains rather than
	// the mis-scaled dB heuristic. Unvoiced frames have no LTP prediction so the
	// gain normalizes the signal directly (Q5d); voiced frames take the
	// noise-shape + process_gains pipeline so the gain matches the spectral
	// envelope and bounds the residual, instead of the heuristic that flooded the
	// shell coder with pulses (Step 4).
	if len(domainGainTargets) == e.nSubframes {
		// Mono SILK-only find_LPC_FLP-domain migration: these gains were already
		// computed before NLSF analysis so Burg could run in the gain-scaled
		// domain. Keep the same targets for the coded gain plan.
	} else if signalType == SignalTypeVoiced {
		pitchLags := e.reconstructCurrentPitchLags()
		_, _, ltpCoeffsQ14 := e.selectLTPGainsVQ(signal, nlsf.lpcQ12, pitchLags)
		baseTargets = e.shapeGainIndices(signal, nlsf.lpcQ12, nlsf.lpcQ12Interp, signalType, quantOffset, pitchLags, ltpCoeffsQ14, pitchGain)
	} else {
		// Unvoiced keeps the Q5d excitation-normalised gains whether it runs the
		// homebrew or the trellis NSQ. The trellis is used with *neutral* shaping
		// for unvoiced (see closedLoopNSQWithRateScale), so its objective is
		// broadband error like homebrew's; the excitation-RMS gain (RMS ≈ 2
		// pulse-units) is the operating point tuned for that — it sets the right
		// pulse density to spend the rate budget on broadband SNR. The
		// spectral-envelope shape gains would normalise the excitation sparser and
		// leave the budget search under-spending the noise frame.
		baseTargets = e.excitationGainIndicesResidual(signal, nlsf.lpcQ12)
	}
	baseIndices = e.resolveGainIndices(baseTargets, conditionalGain)

	targetBits := e.silkFrameTargetBits()
	if targetBits <= 0 {
		return rateControlPlan{gainTargets: baseTargets, gainIndices: baseIndices, rateScale: 1}
	}

	if signalType == SignalTypeVoiced && e.useSNRTargetVBR && !e.stereoComponent {
		e.restoreFrameState(initial)
		snrRateScale := 1.0
		totalBits, gainIndices := e.estimateFrameCandidateBits(
			signal, vadActive, signalType, quantOffset, conditionalGain,
			baseTargets, nlsf, pitchLag, pitchGain, snrRateScale)
		if totalBits <= targetBits {
			e.restoreFrameState(initial)
			return rateControlPlan{gainTargets: baseTargets, gainIndices: gainIndices, rateScale: snrRateScale, snrVBR: true}
		}
	}

	return e.selectBudgetRateControlPlan(initial, signal, vadActive, signalType, quantOffset, conditionalGain, nlsf, pitchLag, pitchGain, baseTargets, baseIndices, targetBits)
}

func (e *Encoder) selectBudgetRateControlPlan(
	initial encoderFrameState,
	signal []float64,
	vadActive bool,
	signalType, quantOffset int,
	conditionalGain bool,
	nlsf nlsfAnalysis,
	pitchLag int,
	pitchGain float64,
	baseTargets []int,
	baseIndices []int,
	targetBits int,
) rateControlPlan {

	gainBoosts := []int{0, 2, 4, 6, 8, 10, 12}
	rateScales := []float64{1, 2, 4, 8, 16, 32, 64, 128, 512}
	if e.complexity < 4 {
		gainBoosts = []int{0, 4, 8, 12}
		rateScales = []float64{1, 4, 16, 64, 512}
	}
	if e.hybridMode {
		// The low band shares a hard packet ceiling with CELT. Some sustained
		// voiced frames need a much stronger pulse penalty than SILK-only quality
		// control permits, so keep searching until a compact fallback is found.
		gainBoosts = []int{0, 4, 8, 12, 16, 20, 24, 32, 40, 48}
		rateScales = []float64{1, 4, 16, 64, 256, 1024, 4096}
	} else if e.lbrrBitsPerFrame > 0 {
		// LBRR is part of the configured SILK packet budget, not additive
		// overhead. Search beyond the ordinary quality-control range when the
		// redundant copy leaves a small regular-frame budget.
		gainBoosts = []int{0, 2, 4, 6, 8, 10, 12, 16, 20, 24}
		rateScales = []float64{1, 2, 4, 8, 16, 32, 64, 128, 512, 1024, 4096}
	}

	best := rateControlPlan{gainTargets: baseTargets, gainIndices: baseIndices, rateScale: 1}
	maxInt := int(^uint(0) >> 1)
	bestOver := maxInt
	bestTotal := maxInt
	inputRMS := math.Sqrt(computeEnergy(signal))
	minOutputRMS := inputRMS / 20
	if minOutputRMS < 0.006 {
		minOutputRMS = 0.006
	}

	for _, boost := range gainBoosts {
		targets := boostedGainTargets(baseTargets, boost)
		e.restoreFrameState(initial)
		headerBits, gainIndices, pitchLags, ltpCoeffsQ14, ltpScaleQ14 := e.estimateFrameHeaderBits(
			signal, vadActive, signalType, quantOffset, conditionalGain, targets, nlsf, pitchLag, pitchGain)
		for _, scale := range rateScales {
			e.restoreFrameState(initial)
			pulses := e.closedLoopNSQWithRateScale(signal, nlsf.lpcQ12, nlsf.lpcQ12Interp, gainIndices,
				signalType, quantOffset, 0, pitchLags, ltpCoeffsQ14, ltpScaleQ14, scale)
			// Voiced hybrid frames may need to sacrifice SILK-layer activity to
			// leave room for CELT in the shared packet budget. Unvoiced hybrid
			// frames cannot: CELT only carries the upper band, so collapsing the
			// SILK excitation destroys most of a 24 kHz noise-like signal.
			preserveLowBand := !e.hybridMode || signalType != SignalTypeVoiced
			if preserveLowBand && !pulsesMeetActivityFloor(pulses, e.frameSize) {
				continue
			}
			if preserveLowBand && e.currentFrameOutputRMS() < minOutputRMS {
				continue
			}
			pulseBits := e.estimatePulseBits(pulses, signalType, quantOffset)
			totalBits := headerBits + pulseBits + 8
			over := totalBits - targetBits
			if over <= 0 {
				e.restoreFrameState(initial)
				return rateControlPlan{gainTargets: targets, gainIndices: gainIndices, rateScale: scale}
			}
			if over < bestOver || (over == bestOver && totalBits < bestTotal) {
				bestOver = over
				bestTotal = totalBits
				best = rateControlPlan{gainTargets: targets, gainIndices: gainIndices, rateScale: scale}
			}
		}
	}

	e.restoreFrameState(initial)
	return best
}

func (e *Encoder) estimateFrameCandidateBits(
	signal []float64,
	vadActive bool,
	signalType, quantOffset int,
	conditionalGain bool,
	gainTargets []int,
	nlsf nlsfAnalysis,
	pitchLag int,
	pitchGain float64,
	rateScale float64,
) (bits int, gainIndices []int) {
	enc := entcode.NewEncoder((e.silkFrameTargetBits() + 7) / 8)
	e.encodeTypeOffset(enc, vadActive, signalType, quantOffset)
	gainIndices = e.encodeGains(enc, signalType, gainTargets, conditionalGain)
	cb := getNLSFCB(e.lpcOrder)
	e.encodeNLSF(enc, cb, signalType, nlsf)
	if e.nSubframes == 4 {
		enc.EncodeIcdf(nlsf.interpFactor, silkNLSFInterpFactorICDF[:], 8)
	}

	ltpCoeffsQ14 := make([][5]int16, e.nSubframes)
	ltpScaleQ14 := silkLTPScalesTable[0]
	pitchLags := make([]int, e.nSubframes)
	for sf := range pitchLags {
		pitchLags[sf] = pitchLag
	}
	if signalType == SignalTypeVoiced {
		ltpCoeffsQ14, ltpScaleQ14, pitchLags = e.encodePitchAndLTP(enc, signal, nlsf.lpcQ12, pitchGain, conditionalGain)
	}

	e.nsqSeed = 0
	pulses := e.closedLoopNSQWithRateScale(signal, nlsf.lpcQ12, nlsf.lpcQ12Interp, gainIndices,
		signalType, quantOffset, 0, pitchLags, ltpCoeffsQ14, ltpScaleQ14, rateScale)
	enc.EncodeIcdf(int(e.nsqSeed), silkUniform4ICDF[:], 8)
	e.encodePulses(enc, pulses, signalType, quantOffset)
	enc.Flush()
	return len(enc.Bytes()) * 8, gainIndices
}

func pulsesMeetActivityFloor(pulses []int16, frameSize int) bool {
	nonZero := 0
	absSum := 0
	for _, p := range pulses {
		if p == 0 {
			continue
		}
		nonZero++
		if p < 0 {
			absSum += int(-p)
		} else {
			absSum += int(p)
		}
	}
	minNonZero := frameSize / 8
	if minNonZero < 16 {
		minNonZero = 16
	}
	minAbsSum := frameSize
	if minAbsSum < 64 {
		minAbsSum = 64
	}
	return nonZero >= minNonZero && absSum >= minAbsSum
}

func (e *Encoder) currentFrameOutputRMS() float64 {
	if e.frameSize <= 0 || len(e.ltpState) < e.frameSize {
		return 0
	}
	start := len(e.ltpState) - e.frameSize
	sum := 0.0
	for _, s := range e.ltpState[start:] {
		v := float64(s) / 32768.0
		sum += v * v
	}
	return math.Sqrt(sum / float64(e.frameSize))
}

func (e *Encoder) silkFrameTargetBits() int {
	bits := e.bitrate * e.frameMs / 1000
	if e.channels == 2 || e.stereoComponent {
		bits /= 2
	}
	bits -= e.lbrrBitsPerFrame
	if bits < 16 {
		bits = 16
	}
	return bits
}

func isShortLagVoiced(fsKHz, pitchLag int) bool {
	return pitchLag > 0 && pitchLag < 5*fsKHz
}

func boostedGainTargets(base []int, boost int) []int {
	out := make([]int, len(base))
	for i, v := range base {
		out[i] = clampInt(v+boost, 0, NLevelsQGain-1)
	}
	return out
}

func (e *Encoder) estimateFrameHeaderBits(
	signal []float64,
	vadActive bool,
	signalType, quantOffset int,
	conditionalGain bool,
	gainTargets []int,
	nlsf nlsfAnalysis,
	pitchLag int,
	pitchGain float64,
) (bits int, gainIndices []int, pitchLags []int, ltpCoeffsQ14 [][5]int16, ltpScaleQ14 int16) {
	enc := entcode.NewEncoder((e.silkFrameTargetBits() + 7) / 8)
	e.encodeTypeOffset(enc, vadActive, signalType, quantOffset)
	gainIndices = e.encodeGains(enc, signalType, gainTargets, conditionalGain)
	cb := getNLSFCB(e.lpcOrder)
	e.encodeNLSF(enc, cb, signalType, nlsf)
	if e.nSubframes == 4 {
		enc.EncodeIcdf(nlsf.interpFactor, silkNLSFInterpFactorICDF[:], 8)
	}
	ltpCoeffsQ14 = make([][5]int16, e.nSubframes)
	ltpScaleQ14 = silkLTPScalesTable[0]
	pitchLags = make([]int, e.nSubframes)
	for sf := range pitchLags {
		pitchLags[sf] = pitchLag
	}
	if signalType == SignalTypeVoiced {
		ltpCoeffsQ14, ltpScaleQ14, pitchLags = e.encodePitchAndLTP(enc, signal, nlsf.lpcQ12, pitchGain, conditionalGain)
	}
	enc.EncodeIcdf(0, silkUniform4ICDF[:], 8)
	enc.Flush()
	return len(enc.Bytes()) * 8, gainIndices, pitchLags, ltpCoeffsQ14, ltpScaleQ14
}

func (e *Encoder) estimatePulseBits(pulses []int16, signalType, quantOffset int) int {
	enc := entcode.NewEncoder((e.silkFrameTargetBits() + 7) / 8)
	e.encodePulses(enc, pulses, signalType, quantOffset)
	enc.Flush()
	return len(enc.Bytes()) * 8
}

func (e *Encoder) analyzePitch(signal []float64) (int, float64) {
	fsKHz := e.sampleRate / 1000
	minLag := PitchEstMinLagMs * fsKHz
	maxLag := PitchEstMaxLagMs * fsKHz
	if len(signal) <= minLag {
		return e.prevPitchLag, 0
	}
	if maxLag >= len(signal) {
		maxLag = len(signal) - 1
	}

	energy := 0.0
	for _, v := range signal {
		energy += v * v
	}
	if energy <= 1e-12 {
		return e.prevPitchLag, 0
	}

	bestLag := minLag
	bestCorr := 0.0
	for lag := minLag; lag <= maxLag; lag++ {
		corr, currentEnergy, lagEnergy := 0.0, 0.0, 0.0
		windowLen := len(signal) - lag
		for i := 0; i < windowLen; i++ {
			current := signal[i+lag]
			delayed := signal[i]
			corr += current * delayed
			currentEnergy += current * current
			lagEnergy += delayed * delayed
		}
		if currentEnergy <= 1e-12 || lagEnergy <= 1e-12 {
			continue
		}
		norm := corr / math.Sqrt(currentEnergy*lagEnergy)
		if norm > bestCorr {
			bestCorr = norm
			bestLag = lag
		}
	}
	if bestCorr < 0 {
		bestCorr = 0
	}
	if bestCorr > 1 {
		bestCorr = 1
	}
	return bestLag, bestCorr
}

// encodePitchAndLTP encodes the absolute lag index and pitch contour index
// selected by silk_find_pitch_lags_FLP (e.curPitchLagIndex /
// e.curPitchContourIndex), then the LTP gains. It reconstructs the per-subframe
// pitch lags exactly as the decoder does (from the encoded indices) so the NSQ
// uses the same lags the decoder will, and returns them alongside the LTP taps.
func (e *Encoder) encodePitchAndLTP(enc *entcode.Encoder, signal []float64, lpcQ12 []int16, pitchGain float64, conditionalGain bool) ([][5]int16, int16, []int) {
	fsKHz := e.sampleRate / 1000

	step := fsKHz >> 1
	if step < 1 {
		step = 1
	}
	coreLagIndex := e.curPitchLagIndex
	if coreLagIndex < 0 {
		coreLagIndex = 0
	}
	lagIndex := coreLagIndex / step
	lagLowBits := coreLagIndex % step
	if lagIndex >= len(silkPitchLagICDF) {
		lagIndex = len(silkPitchLagICDF) - 1
		lagLowBits = step - 1
	}

	contourIndex := e.curPitchContourIndex
	contourMax := contourCBSize(fsKHz, e.nSubframes)
	if contourIndex < 0 || contourIndex >= contourMax {
		contourIndex = 0
	}

	if conditionalGain && e.prevSignalType == SignalTypeVoiced {
		enc.EncodeIcdf(0, silkPitchDeltaICDF[:], 8) // Force absolute lag coding.
	}
	enc.EncodeIcdf(lagIndex, silkPitchLagICDF[:], 8)
	encodePitchLagLowBits(enc, fsKHz, lagLowBits)
	encodePitchContour(enc, fsKHz, e.nSubframes, contourIndex)

	// Reconstruct the per-subframe lags the way the decoder will, from the
	// encoded indices (so encoder and decoder stay bit-for-bit in sync).
	recLag := lagIndex*step + lagLowBits
	pitchLags := e.reconstructCurrentPitchLags()

	ltpPerIdx, ltpGainIndices, ltpCoeffsQ14 := e.selectLTPGainsVQ(signal, lpcQ12, pitchLags)
	enc.EncodeIcdf(ltpPerIdx, silkLTPPerIndexICDF[:], 8)
	for sf := 0; sf < e.nSubframes; sf++ {
		ltpGainIdx := ltpGainIndices[sf]
		switch ltpPerIdx {
		case 0:
			enc.EncodeIcdf(ltpGainIdx, silkLTPGainICDF0[:], 8)
		case 1:
			enc.EncodeIcdf(ltpGainIdx, silkLTPGainICDF1[:], 8)
		default:
			enc.EncodeIcdf(ltpGainIdx, silkLTPGainICDF2[:], 8)
		}
	}
	if !conditionalGain {
		enc.EncodeIcdf(0, silkLTPScaleICDF[:], 8)
	}

	e.prevPitchLag = pitchLags[e.nSubframes-1]
	e.prevLagIndex = recLag

	// Capture the coded pitch/LTP indices so the LBRR generator can replay this
	// frame's side information into the next packet without recomputation.
	e.capLagHigh = lagIndex
	e.capLagLow = lagLowBits
	e.capContour = contourIndex
	e.capLTPPerIdx = ltpPerIdx
	e.capLTPGainIdx = append([]int(nil), ltpGainIndices...)

	return ltpCoeffsQ14, silkLTPScalesTable[0], pitchLags
}

// reconstructCurrentPitchLags rebuilds the per-subframe pitch lags from the
// current frame's quantized lag/contour indices the same way the decoder does
// (mirrors the lag math in encodePitchAndLTP). Gain analysis uses it so the LTP
// residual it measures lines up with the lags actually written to the bitstream.
func (e *Encoder) reconstructCurrentPitchLags() []int {
	fsKHz := e.sampleRate / 1000
	minLag := PitchEstMinLagMs * fsKHz
	maxLag := PitchEstMaxLagMs * fsKHz
	step := fsKHz >> 1
	if step < 1 {
		step = 1
	}
	coreLagIndex := e.curPitchLagIndex
	if coreLagIndex < 0 {
		coreLagIndex = 0
	}
	lagIndex := coreLagIndex / step
	lagLowBits := coreLagIndex % step
	if lagIndex >= len(silkPitchLagICDF) {
		lagIndex = len(silkPitchLagICDF) - 1
		lagLowBits = step - 1
	}
	contourIndex := e.curPitchContourIndex
	contourMax := contourCBSize(fsKHz, e.nSubframes)
	if contourIndex < 0 || contourIndex >= contourMax {
		contourIndex = 0
	}
	baseLag := minLag + lagIndex*step + lagLowBits
	contourOffsets := silkPitchContourOffsets(contourIndex, e.nSubframes, fsKHz)
	pitchLags := make([]int, e.nSubframes)
	for sf := 0; sf < e.nSubframes; sf++ {
		pitchLags[sf] = clampInt(baseLag+contourOffsets[sf], minLag, maxLag)
	}
	return pitchLags
}

// ltpCoeffsForIndices builds the per-subframe Q14 LTP coefficient set from the
// quantized periodicity/gain indices (the codebook entries the decoder reads).
func ltpCoeffsForIndices(ltpPerIdx, ltpGainIdx, nSubframes int) [][5]int16 {
	ltpCoeffsQ14 := make([][5]int16, nSubframes)
	for sf := 0; sf < nSubframes; sf++ {
		for k := 0; k < 5; k++ {
			switch ltpPerIdx {
			case 0:
				ltpCoeffsQ14[sf][k] = int16(silkLTPGainVQ0[ltpGainIdx][k]) << 7
			case 1:
				ltpCoeffsQ14[sf][k] = int16(silkLTPGainVQ1[ltpGainIdx][k]) << 7
			default:
				ltpCoeffsQ14[sf][k] = int16(silkLTPGainVQ2[ltpGainIdx][k]) << 7
			}
		}
	}
	return ltpCoeffsQ14
}

// contourCBSize returns the number of pitch-contour codebook entries for the
// given sample rate and subframe count (matching the decoder ICDF tables).
func contourCBSize(fsKHz, nSubframes int) int {
	switch {
	case fsKHz == 8 && nSubframes == 4:
		return 11
	case fsKHz == 8:
		return 3
	case nSubframes == 4:
		return 34
	default:
		return 12
	}
}

// encodePitchContour encodes the pitch contour index using the ICDF matching the
// decoder's silkPitchContourOffsets selection.
func encodePitchContour(enc *entcode.Encoder, fsKHz, nSubframes, contourIndex int) {
	switch {
	case fsKHz == 8 && nSubframes == 4:
		enc.EncodeIcdf(contourIndex, silkPitchContourNBICDF[:], 8)
	case fsKHz == 8:
		enc.EncodeIcdf(contourIndex, silkPitchContour10msNBICDF[:], 8)
	case nSubframes == 4:
		enc.EncodeIcdf(contourIndex, silkPitchContourICDF[:], 8)
	default:
		enc.EncodeIcdf(contourIndex, silkPitchContour10msICDF[:], 8)
	}
}

func encodePitchLagLowBits(enc *entcode.Encoder, fsKHz, lagLowBits int) {
	switch fsKHz {
	case 8:
		enc.EncodeIcdf(clampInt(lagLowBits, 0, 3), silkUniform4ICDF[:], 8)
	case 12:
		enc.EncodeIcdf(clampInt(lagLowBits, 0, 5), silkUniform6ICDF[:], 8)
	default:
		enc.EncodeIcdf(clampInt(lagLowBits, 0, 7), silkUniform8ICDF[:], 8)
	}
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func encodeFlatPitchContour(enc *entcode.Encoder, fsKHz, nSubframes int) {
	switch {
	case fsKHz == 8 && nSubframes == 4:
		enc.EncodeIcdf(0, silkPitchContourNBICDF[:], 8)
	case fsKHz == 8:
		enc.EncodeIcdf(0, silkPitchContour10msNBICDF[:], 8)
	case nSubframes == 4:
		enc.EncodeIcdf(0, silkPitchContourICDF[:], 8)
	default:
		enc.EncodeIcdf(0, silkPitchContour10msICDF[:], 8)
	}
}

func selectLTPGain(pitchGain float64) (int, int) {
	target := pitchGain * 128.0
	bestPer, bestIdx := 0, 0
	bestErr := math.Inf(1)
	consider := func(perIdx, gainIdx int, taps [5]int8) {
		err := 0.0
		for k, tap := range taps {
			want := 0.0
			if k == 2 {
				want = target
			}
			diff := float64(tap) - want
			err += diff * diff
		}
		if err < bestErr {
			bestErr = err
			bestPer = perIdx
			bestIdx = gainIdx
		}
	}
	for i, taps := range silkLTPGainVQ0 {
		consider(0, i, taps)
	}
	for i, taps := range silkLTPGainVQ1 {
		consider(1, i, taps)
	}
	for i, taps := range silkLTPGainVQ2 {
		consider(2, i, taps)
	}
	return bestPer, bestIdx
}

func (e *Encoder) analysisExcitation(signal []float64, lpcQ12 []int16, signalType, pitchLag int, pitchGain float64) []float64 {
	if signalType == SignalTypeInactive {
		return make([]float64, len(signal))
	}

	residual := make([]float64, len(signal))
	for i := range signal {
		pred := 0.0
		for j := 0; j < e.lpcOrder && j < i; j++ {
			pred += float64(lpcQ12[j]) / 4096.0 * signal[i-j-1]
		}
		residual[i] = signal[i] - pred
	}
	if signalType != SignalTypeVoiced || pitchGain <= 0 || pitchLag <= 0 {
		return residual
	}

	excitation := make([]float64, len(residual))
	copy(excitation, residual)
	ltpGain := pitchGain
	if ltpGain > 0.8 {
		ltpGain = 0.8
	}
	for i := pitchLag; i < len(excitation); i++ {
		excitation[i] -= ltpGain * residual[i-pitchLag]
	}
	return excitation
}

func (e *Encoder) analyzeNoiseShape(signal []float64, signalType int, pitchGain float64) silkShapeAnalysis {
	out := silkShapeAnalysis{subframes: make([]silkShapeSubframe, e.nSubframes)}
	if e.nSubframes == 0 {
		return out
	}
	subframeLen := e.frameSize / e.nSubframes
	frameEnergy := computeEnergy(signal)
	frameRMS := math.Sqrt(frameEnergy + 1e-18)
	for sf := 0; sf < e.nSubframes; sf++ {
		start := sf * subframeLen
		end := start + subframeLen
		if end > len(signal) {
			end = len(signal)
		}
		if start >= end {
			out.subframes[sf] = defaultShapeSubframe(signalType, pitchGain)
			continue
		}

		energy, lag1, diffEnergy := 0.0, 0.0, 0.0
		prev := signal[start]
		for i := start; i < end; i++ {
			x := signal[i]
			energy += x * x
			if i > start {
				lag1 += x * prev
				d := x - prev
				diffEnergy += d * d
			}
			prev = x
		}
		n := float64(end - start)
		rms := math.Sqrt(energy/n + 1e-18)
		tilt := 0.0
		if energy > 1e-12 {
			tilt = lag1 / energy
		}
		tilt = clampFloat(tilt, -0.75, 0.75)
		hfRatio := 0.0
		if energy > 1e-12 {
			hfRatio = diffEnergy / (4.0 * energy)
		}
		hfRatio = clampFloat(hfRatio, 0, 1)

		activity := clampFloat(rms/(frameRMS+1e-9), 0.35, 2.0)
		shape := defaultShapeSubframe(signalType, pitchGain)
		shape.tilt = 0.18 * tilt
		shape.lf = clampFloat(-0.11*tilt, -0.10, 0.10)
		shape.hf = clampFloat(0.14*(hfRatio-0.22), -0.05, 0.13)
		shape.lambda *= clampFloat(1.10-0.12*activity+0.10*hfRatio, 0.82, 1.24)
		if signalType == SignalTypeVoiced {
			shape.feedback = clampFloat(0.42+0.10*pitchGain+0.04*math.Max(tilt, 0), 0.40, 0.58)
			shape.harmonic = clampFloat(0.10+0.42*pitchGain, 0, 0.55)
			shape.lambda *= clampFloat(0.96-0.08*pitchGain, 0.86, 1.0)
		} else {
			shape.feedback = clampFloat(0.25+0.10*math.Max(tilt, 0)+0.09*hfRatio, 0.22, 0.42)
			shape.harmonic = 0
			shape.lambda *= clampFloat(1.0+0.12*hfRatio, 1.0, 1.12)
		}
		out.subframes[sf] = shape
	}
	return out
}

func defaultShapeSubframe(signalType int, pitchGain float64) silkShapeSubframe {
	shape := silkShapeSubframe{
		feedback: 0.30,
		lambda:   1.0,
	}
	if signalType == SignalTypeVoiced {
		shape.feedback = 0.50
		shape.harmonic = clampFloat(0.10+0.42*pitchGain, 0, 0.55)
		shape.lambda = 0.92
	}
	return shape
}

func clampFloat(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func (e *Encoder) encodeTypeOffset(enc *entcode.Encoder, vadActive bool, signalType, quantOffset int) {
	typeIx := signalType*2 + quantOffset
	if vadActive {
		symbol := typeIx - 2
		if symbol < 0 {
			symbol = 0
		}
		if symbol > 3 {
			symbol = 3
		}
		enc.EncodeIcdf(symbol, silkTypeOffsetVADICDF[:], 8)
		return
	}
	symbol := typeIx
	if symbol < 0 {
		symbol = 0
	}
	if symbol > 1 {
		symbol = 1
	}
	enc.EncodeIcdf(symbol, silkTypeOffsetNoVADICDF[:], 8)
}

func (e *Encoder) analysisGainIndex(signal []float64) int {
	energy := computeEnergy(signal)
	return analysisGainIndexFromEnergy(energy)
}

func (e *Encoder) analysisGainIndices(signal []float64) []int {
	return e.gainIndicesFromEnergy(signal, analysisGainIndexFromEnergy)
}

// excitationGainIndicesResidual normalises the unvoiced excitation gain to the
// short-term LPC *residual* energy rather than the raw signal energy. The gain
// targets the quantized excitation level (RMS ≈ 2 pulse-units; the legacy dB
// heuristic in analysisGainIndices is ~1000× too low and floods the shell coder).
// For noise-like input the LPC predictor is nearly flat, so the residual matches
// the signal energy and unvoiced-noise quality is preserved. For a harmonic frame
// that the pitch analyser misclassifies as
// unvoiced (e.g. the first frame after reset, where the zero-history pitch search
// is unreliable), the LPC residual is far smaller than the signal: gating the
// gain on the signal energy would set it for the full tonal amplitude while the
// sharp LPC synthesis resonance amplifies further, producing a runaway (clipping)
// output that then poisons every subsequent voiced frame's LTP state. Using the
// residual energy keeps the synthesised level bounded.
func (e *Encoder) excitationGainIndicesResidual(signal []float64, lpcQ12 []int16) []int {
	targets := make([]int, e.nSubframes)
	if e.nSubframes == 0 {
		return targets
	}
	subLen := e.frameSize / e.nSubframes
	resNrg := e.ltpResidualEnergyPerSubframe(signal, lpcQ12, SignalTypeUnvoiced, nil, nil)
	const int16Scale = 32768.0 * 32768.0
	for sf := 0; sf < e.nSubframes; sf++ {
		energy := 0.0
		if subLen > 0 {
			energy = resNrg[sf] / (int16Scale * float64(subLen))
		}
		targets[sf] = excitationGainIndexFromEnergy(energy)
	}
	return targets
}

// shapeGainIndices derives per-subframe target gain indices from the libopus
// gain pipeline (silk_noise_shape_analysis_FLP gains + silk_process_gains_FLP
// soft limit), with the voiced SNR-target VBR backoff applied before gain_mult.
// The shape gains come from the noise-shaping spectral envelope (never
// near-zero, so steady voiced frames stay stable); the soft limit then floors
// the gain using the LPC+LTP residual energy so the quantized signal is bounded.
// Used for voiced frames where the dB heuristic mis-scaled the gain and flooded
// the shell coder with pulses (Step 4). The shape-smoothing state is saved and
// restored so this acts as a pure analysis pass.
func (e *Encoder) shapeGainAnalysis(signal []float64, lpcQ12 []int16, lpcInterpQ12 []int16, signalType, quantOffset int, pitchLags []int, ltpCoeffsQ14 [][5]int16, pitchGain float64) ([]int, silkNoiseShapeAnalysis) {
	harmSmooth, tiltSmooth := e.shapeHarmSmooth, e.shapeTiltSmooth
	shape := e.analyzeNoiseShapeFLP(signal, lpcQ12, signalType, quantOffset, pitchLags, pitchGain, e.speechActivity)
	e.shapeHarmSmooth, e.shapeTiltSmooth = harmSmooth, tiltSmooth

	resNrg := e.processGainsResidualEnergy(signal, lpcQ12, lpcInterpQ12, signalType, pitchLags, ltpCoeffsQ14, shape.Gains)
	subLen := e.frameSize / e.nSubframes
	invMaxSqr := 0.0
	if subLen > 0 {
		invMaxSqr = math.Pow(2.0, 0.33*(21.0-shape.SNRdB)) / float64(subLen)
	}
	silkTraceSNR("process_gains fs=%dkHz signal=%d snr=%.3fdB sub_len=%d inv_max_sqr=%.9f",
		e.sampleRate/1000, signalType, shape.SNRdB, subLen, invMaxSqr)

	// Gain reduction when the LTP coding gain is high (silk_process_gains_FLP):
	// a strong long-term predictor leaves a small residual, so the synthesis
	// gain can be scaled down, sparing pulses on steady voiced frames. The soft
	// limit below still floors the gain by the residual energy, so the reduction
	// only takes hold where the prediction is genuinely good (ratio_bytes ~2x→~1x).
	gainScale := 1.0
	if signalType == SignalTypeVoiced {
		ltpCodGainDB := e.ltpPredCodGainDB(signal, lpcQ12, e.ltpResidualEnergyPerSubframe(signal, lpcQ12, signalType, pitchLags, ltpCoeffsQ14), pitchLags, ltpCoeffsQ14)
		gainScale = 1.0 - 0.5*silkSigmoid(0.25*(ltpCodGainDB-12.0))
		silkTraceSNR("process_gains voiced ltp_cod_gain=%.3fdB gain_scale=%.6f", ltpCodGainDB, gainScale)
	}

	targets := make([]int, e.nSubframes)
	for sf := 0; sf < e.nSubframes; sf++ {
		gain := shape.Gains[sf] * gainScale
		// Soft limit on the ratio of residual energy to squared gain
		// (silk_process_gains_FLP): raises the gain when the prediction residual
		// is large, capping the number of pulses the NSQ has to spend.
		gain = math.Sqrt(gain*gain + resNrg[sf]*invMaxSqr)
		if gain > 32767 {
			gain = 32767
		}
		silkTraceSNR("process_gains sf=%d shape_gain=%.3f res_nrg=%.3f final_gain=%.3f gain_index=%d",
			sf, shape.Gains[sf], resNrg[sf], gain, silkQuantizeGainIndex(gain*65536.0))
		targets[sf] = silkQuantizeGainIndex(gain * 65536.0)
	}
	return targets, shape
}

func (e *Encoder) shapeGainIndices(signal []float64, lpcQ12 []int16, lpcInterpQ12 []int16, signalType, quantOffset int, pitchLags []int, ltpCoeffsQ14 [][5]int16, pitchGain float64) []int {
	targets, _ := e.shapeGainAnalysis(signal, lpcQ12, lpcInterpQ12, signalType, quantOffset, pitchLags, ltpCoeffsQ14, pitchGain)
	return targets
}

// ltpResidualEnergyPerSubframe returns the per-subframe LPC (+LTP for voiced)
// open-loop prediction residual energy in the int16-magnitude domain (sum of
// squares), used by the process_gains soft limit. Mirrors the residual libopus
// measures in silk_residual_energy_FLP.
func (e *Encoder) ltpResidualEnergyPerSubframe(signal []float64, lpcQ12 []int16, signalType int, pitchLags []int, ltpCoeffsQ14 [][5]int16) [silkMaxNBSubframes]float64 {
	var nrgs [silkMaxNBSubframes]float64
	if e.nSubframes == 0 {
		return nrgs
	}
	subLen := e.frameSize / e.nSubframes

	hist := e.pitchHist
	buf := make([]float64, len(hist)+len(signal))
	copy(buf, hist)
	copy(buf[len(hist):], signal)
	res := make([]float64, len(buf))
	for i := range buf {
		pred := 0.0
		for j := 0; j < e.lpcOrder && j <= i-1; j++ {
			pred += float64(lpcQ12[j]) / 4096.0 * buf[i-j-1]
		}
		res[i] = buf[i] - pred
	}

	frameStart := len(hist)
	const int16Scale = 32768.0 * 32768.0
	for sf := 0; sf < e.nSubframes; sf++ {
		lag := 0
		var b [5]float64
		if signalType == SignalTypeVoiced {
			lag = pitchLags[clampInt(sf, 0, len(pitchLags)-1)]
			if sf < len(ltpCoeffsQ14) {
				for k := 0; k < 5; k++ {
					b[k] = float64(ltpCoeffsQ14[sf][k]) / 16384.0
				}
			}
		}
		sum := 0.0
		for i := 0; i < subLen; i++ {
			idx := frameStart + sf*subLen + i
			if idx >= len(res) {
				break
			}
			v := res[idx]
			if lag > 0 {
				for k := 0; k < 5; k++ {
					src := idx - lag + 2 - k
					if src >= 0 && src < len(res) {
						v -= b[k] * res[src]
					}
				}
			}
			sum += v * v
		}
		nrgs[sf] = sum * int16Scale
	}
	return nrgs
}

// ltpPredCodGainDB returns the LTP prediction coding gain in dB, the quantity
// silk_quant_LTP_gains reports as pred_gain_dB_Q7 / 128. libopus computes it as
// -3*log2(res_nrg) where res_nrg is the LTP residual energy normalised by the
// LPC residual energy (averaged over subframes). We reuse the open-loop LPC+LTP
// residual (passed in as ltpNrg) and an LPC-only residual pass for the
// denominator. A perfectly periodic (steady voiced) frame drives this high, so
// the process_gains reduction shrinks its gains the most.
func (e *Encoder) ltpPredCodGainDB(signal []float64, lpcQ12 []int16, ltpNrg [silkMaxNBSubframes]float64, pitchLags []int, ltpCoeffsQ14 [][5]int16) float64 {
	if e.nSubframes == 0 {
		return 0
	}
	lpcNrg := e.ltpResidualEnergyPerSubframe(signal, lpcQ12, SignalTypeUnvoiced, pitchLags, ltpCoeffsQ14)
	ratioSum := 0.0
	for sf := 0; sf < e.nSubframes; sf++ {
		denom := lpcNrg[sf]
		if denom < 1e-9 {
			denom = 1e-9
		}
		r := ltpNrg[sf] / denom
		// The optimal predictor never increases the residual; clamp so a crude
		// per-subframe LTP gain cannot push the coding gain negative.
		if r > 1.0 {
			r = 1.0
		}
		if r < 1e-9 {
			r = 1e-9
		}
		ratioSum += r
	}
	ratio := ratioSum / float64(e.nSubframes)
	if ratio < 1e-9 {
		ratio = 1e-9
	}
	return -3.0 * silkLog2(ratio)
}

func (e *Encoder) gainIndicesFromEnergy(signal []float64, fromEnergy func(float64) int) []int {
	targets := make([]int, e.nSubframes)
	if len(signal) == 0 {
		for i := range targets {
			targets[i] = 10
		}
		return targets
	}

	frameTarget := fromEnergy(computeEnergy(signal))
	subframeLen := e.frameSize / e.nSubframes
	for sf := range targets {
		start := sf * subframeLen
		end := start + subframeLen
		if start >= len(signal) {
			targets[sf] = frameTarget
			continue
		}
		if end > len(signal) {
			end = len(signal)
		}
		targets[sf] = fromEnergy(computeEnergy(signal[start:end]))
		if targets[sf] > frameTarget {
			targets[sf] = frameTarget
		}
	}
	return targets
}

func analysisGainIndexFromEnergy(energy float64) int {
	if energy <= 1e-12 {
		return 10
	}
	gainDB := LinearToDB(math.Sqrt(energy) + 1e-12)
	idx := int(math.Round((gainDB + 30.0) / 1.5))
	if idx < 10 {
		idx = 10
	}
	if idx > 40 {
		idx = 40
	}
	return idx
}

func excitationGainIndexFromEnergy(energy float64) int {
	if energy <= 1e-12 {
		return 10
	}
	// gainQ16 ≈ rms · 2^30 keeps the excitation RMS near 2 pulse-units, matching
	// libopus's operating point: small pulses the shell coder represents cheaply,
	// landing the per-frame size near the 24 kbps budget so rate control does not
	// have to throttle (legacy idx≈10 saturated the ±1024 clamp and blew budget).
	targetQ16 := math.Sqrt(energy) * float64(int64(1)<<30)
	return silkQuantizeGainIndex(targetQ16)
}

// silkQuantizeGainIndex returns the gain index whose dequantized Q16 gain is
// closest (in the log domain) to targetQ16, over the full SILK gain range.
func silkQuantizeGainIndex(targetQ16 float64) int {
	if targetQ16 < 1 {
		targetQ16 = 1
	}
	logTarget := math.Log(targetQ16)
	best := 0
	bestErr := math.Inf(1)
	for idx := 0; idx < NLevelsQGain; idx++ {
		g := float64(silkGainDequantQ16(idx))
		if g < 1 {
			g = 1
		}
		errAbs := math.Abs(math.Log(g) - logTarget)
		if errAbs < bestErr {
			bestErr = errAbs
			best = idx
		}
	}
	return best
}

func (e *Encoder) encodeGains(enc *entcode.Encoder, signalType int, targetIndices []int, conditional bool) []int {
	absIndices := make([]int, e.nSubframes)
	prevIdx := e.prevGainIdx
	targetIdx := gainTargetAt(targetIndices, 0)
	if conditional {
		targetIdx = e.encodeGainDelta(enc, prevIdx, targetIdx)
	} else {
		if targetIdx < prevIdx-16 {
			targetIdx = prevIdx - 16
		}
		gainMSB := targetIdx >> 3
		gainLSB := targetIdx & 7
		if gainMSB > 7 {
			gainMSB = 7
			gainLSB = 7
			targetIdx = NLevelsQGain - 1
		}
		enc.EncodeIcdf(gainMSB, silkGainICDF[signalType][:], 8)
		enc.EncodeIcdf(gainLSB, silkUniform8ICDF[:], 8)
	}
	absIndices[0] = targetIdx

	for sf := 1; sf < e.nSubframes; sf++ {
		targetIdx = e.encodeGainDelta(enc, targetIdx, gainTargetAt(targetIndices, sf))
		absIndices[sf] = targetIdx
	}
	return absIndices
}

func (e *Encoder) resolveGainIndices(targetIndices []int, conditional bool) []int {
	absIndices := make([]int, e.nSubframes)
	prevIdx := e.prevGainIdx
	targetIdx := gainTargetAt(targetIndices, 0)
	if conditional {
		targetIdx = quantizedGainDelta(prevIdx, targetIdx)
	} else {
		if targetIdx < prevIdx-16 {
			targetIdx = prevIdx - 16
		}
		if targetIdx >= NLevelsQGain {
			targetIdx = NLevelsQGain - 1
		}
		if targetIdx < 0 {
			targetIdx = 0
		}
	}
	absIndices[0] = targetIdx

	for sf := 1; sf < e.nSubframes; sf++ {
		targetIdx = quantizedGainDelta(targetIdx, gainTargetAt(targetIndices, sf))
		absIndices[sf] = targetIdx
	}
	return absIndices
}

func gainTargetAt(targetIndices []int, sf int) int {
	if len(targetIndices) == 0 {
		return 10
	}
	if sf < 0 {
		sf = 0
	}
	if sf >= len(targetIndices) {
		sf = len(targetIndices) - 1
	}
	return clampInt(targetIndices[sf], 0, NLevelsQGain-1)
}

func (e *Encoder) encodeGainDelta(enc *entcode.Encoder, prevIdx, targetIdx int) int {
	delta := targetIdx - prevIdx
	if delta < MinDeltaGainQuant {
		delta = MinDeltaGainQuant
	}
	if delta > MaxDeltaGainQuant {
		delta = MaxDeltaGainQuant
	}
	enc.EncodeIcdf(delta-MinDeltaGainQuant, silkDeltaGainICDF[:], 8)
	return applyQuantizedGainDelta(prevIdx, delta)
}

func quantizedGainDelta(prevIdx, targetIdx int) int {
	delta := targetIdx - prevIdx
	if delta < MinDeltaGainQuant {
		delta = MinDeltaGainQuant
	}
	if delta > MaxDeltaGainQuant {
		delta = MaxDeltaGainQuant
	}
	return applyQuantizedGainDelta(prevIdx, delta)
}

func applyQuantizedGainDelta(prevIdx, delta int) int {
	dblStepThresh := 2*MaxDeltaGainQuant - NLevelsQGain + prevIdx
	if delta > dblStepThresh {
		prevIdx += 2*delta - dblStepThresh
	} else {
		prevIdx += delta
	}
	if prevIdx < 0 {
		prevIdx = 0
	}
	if prevIdx >= NLevelsQGain {
		prevIdx = NLevelsQGain - 1
	}
	return prevIdx
}

func (e *Encoder) defaultNLSFIndex(signalType int, cb *nlsfCBParams) int {
	if cb.order == 16 {
		return 9
	}
	if signalType == SignalTypeInactive {
		return 0
	}
	return 10
}

func (e *Encoder) analyzeNLSF(signal []float64, cb *nlsfCBParams, signalType int, preConfig ...lpcInPreConfig) nlsfAnalysis {
	rawIdx := make([]int, cb.order)
	cb1Idx := e.defaultNLSFIndex(signalType, cb)
	var burgDomain *lpcBurgDomain
	if len(preConfig) > 0 && signalType != SignalTypeInactive {
		cfg := preConfig[0]
		input := signal
		if len(cfg.input) > 0 {
			input = cfg.input
		}
		lpcInPre := buildLPCInPre(input, cfg.subframeLengths, cfg.invGains, cfg.ltpCoefs, cfg.pitchLags, cb.order, cfg.voiced)
		if len(cfg.subframeLengths) > 0 && len(lpcInPre) > 0 {
			subfrLength := cfg.subframeLengths[0] + cb.order
			uniform := subfrLength > cb.order
			for _, n := range cfg.subframeLengths {
				if n+cb.order != subfrLength {
					uniform = false
					break
				}
			}
			if uniform && len(lpcInPre) >= subfrLength*len(cfg.subframeLengths) {
				burgDomain = &lpcBurgDomain{
					signal:      lpcInPre,
					subfrLength: subfrLength,
					nbSubfr:     len(cfg.subframeLengths),
					minInvGain:  lpcMinInvGain(cfg.ltpPredCodGain, cfg.codingQuality, e.firstFrameAfterReset),
				}
			}
		}
	}
	if signalType != SignalTypeInactive {
		targetQ15, ok := e.lpcNLSFTargetQ15(signal, cb, burgDomain)
		if ok {
			acceptFaithful := burgDomain != nil && signalType == SignalTypeVoiced && !e.stereoComponent && !e.hybridMode && !e.lbrrEnabled && e.packetFrames == 1
			if acceptFaithful {
				if analysis, done := e.guardedFaithfulBurgNLSFAnalysis(signal, cb, targetQ15, signalType, burgDomain); done {
					return analysis
				}
			}
			cb1Idx, rawIdx = e.guardedFaithfulNLSFAnalysis(signal, cb, targetQ15, signalType, acceptFaithful)
		} else {
			cb1Idx, rawIdx = bestNLSFAnalysis(signal, cb, targetQ15, ok)
		}
	}

	nlsfQ15 := reconstructNLSFQ15(cb, cb1Idx, rawIdx)
	lpcQ12 := nlsfToLPCLibopus(nlsfQ15, cb.order)

	interpFactor, lpcQ12Interp := e.selectNLSFInterpolation(signal, cb, signalType, nlsfQ15, lpcQ12)
	if burgDomain != nil {
		interpFactor, lpcQ12Interp = 4, nil
	}

	return nlsfAnalysis{
		cb1Idx:       cb1Idx,
		rawIdx:       rawIdx,
		nlsfQ15:      nlsfQ15,
		lpcQ12:       lpcQ12,
		lpcQ12Interp: lpcQ12Interp,
		interpFactor: interpFactor,
	}
}

func (e *Encoder) guardedFaithfulBurgNLSFAnalysis(signal []float64, cb *nlsfCBParams, fullTargetQ15 []int16, signalType int, domain *lpcBurgDomain) (nlsfAnalysis, bool) {
	if domain == nil || len(fullTargetQ15) != cb.order {
		return nlsfAnalysis{}, false
	}

	lastHalfQ15, _ := lastHalfBurgNLSF(domain.signal, domain.subfrLength, cb.order, domain.nbSubfr, domain.minInvGain)
	if len(lastHalfQ15) != cb.order {
		return nlsfAnalysis{}, false
	}
	silkNLSFStabilize(lastHalfQ15, cb.deltaMinQ15, cb.order)

	interpFactor := e.selectFaithfulBurgInterpolationLPCInPre(cb, fullTargetQ15, lastHalfQ15, domain)
	transmitTarget := transparentBurgTransmitTarget(fullTargetQ15, lastHalfQ15, interpFactor)
	faithfulCB1, faithfulRaw, faithfulQ15 := e.faithfulNLSFEncode(transmitTarget, cb, signalType)
	faithfulLPC := nlsfToLPCLibopus(faithfulQ15, cb.order)
	faithfulPeak := lpcSpectralPeakGain(faithfulLPC)

	if os.Getenv("OPUS_SILK_TRANSPARENT_NLSF") != "1" {
		legacyCB1 := bestNLSFStage1(signal, cb)
		legacyRaw := refineNLSFResidual(signal, cb, legacyCB1)
		legacyQ15 := reconstructNLSFQ15(cb, legacyCB1, legacyRaw)
		legacyLPC := nlsfToLPCLibopus(legacyQ15, cb.order)
		legacyPeak := lpcSpectralPeakGain(legacyLPC)

		targetLPC := nlsfToLPCLibopus(transmitTarget, cb.order)
		loudnessDiff := lpcEnvelopeLoudnessDB(faithfulLPC) - lpcEnvelopeLoudnessDB(targetLPC)
		peakOK := faithfulPeak <= math.Max(18.0, legacyPeak*1.35) && faithfulPeak <= 96.0
		if !peakOK || math.Abs(loudnessDiff) > 1.5 {
			return nlsfAnalysis{
				cb1Idx:       legacyCB1,
				rawIdx:       legacyRaw,
				nlsfQ15:      legacyQ15,
				lpcQ12:       legacyLPC,
				lpcQ12Interp: nil,
				interpFactor: 4,
			}, true
		}
	}

	lpcQ12Interp := interpolatedLPCForTransmittedNLSF(e.prevNLSFQ15, faithfulQ15, interpFactor, cb)
	return nlsfAnalysis{
		cb1Idx:       faithfulCB1,
		rawIdx:       faithfulRaw,
		nlsfQ15:      faithfulQ15,
		lpcQ12:       faithfulLPC,
		lpcQ12Interp: lpcQ12Interp,
		interpFactor: interpFactor,
	}, true
}

func (e *Encoder) guardedFaithfulNLSFAnalysis(signal []float64, cb *nlsfCBParams, targetQ15 []int16, signalType int, acceptFaithful bool) (int, []int) {
	faithfulCB1, faithfulRaw, faithfulQ15 := e.faithfulNLSFEncode(targetQ15, cb, signalType)
	faithfulLPC := nlsfToLPCLibopus(faithfulQ15, cb.order)
	faithfulPeak := lpcSpectralPeakGain(faithfulLPC)

	legacyCB1 := bestNLSFStage1(signal, cb)
	legacyRaw := refineNLSFResidual(signal, cb, legacyCB1)
	legacyQ15 := reconstructNLSFQ15(cb, legacyCB1, legacyRaw)
	legacyLPC := nlsfToLPCLibopus(legacyQ15, cb.order)
	legacyPeak := lpcSpectralPeakGain(legacyLPC)
	peakOK := faithfulPeak <= math.Max(18.0, legacyPeak*1.35)

	if acceptFaithful {
		targetLPC := nlsfToLPCLibopus(targetQ15, cb.order)
		loudnessDiff := lpcEnvelopeLoudnessDB(faithfulLPC) - lpcEnvelopeLoudnessDB(targetLPC)
		if peakOK && math.Abs(loudnessDiff) <= 1.5 {
			return faithfulCB1, faithfulRaw
		}
		return legacyCB1, legacyRaw
	}

	faithfulResidual := lpcResidualEnergy(signal, faithfulLPC)
	legacyResidual := lpcResidualEnergy(signal, legacyLPC)
	if faithfulResidual <= legacyResidual*1.05+1e-12 && peakOK {
		return faithfulCB1, faithfulRaw
	}
	return legacyCB1, legacyRaw
}

// selectNLSFInterpolation mirrors the interpolation-index search of libopus
// silk_find_LPC_FLP. It chooses NLSFInterpCoef_Q2 (0..4) by testing whether
// interpolating the previous quantized NLSF toward the current quantized NLSF
// lowers the LPC residual energy over the first half of the frame (subframes
// 0 and 1). interpFactor==4 means no interpolation.
//
// Unlike libopus — which computes the analysis on the LTP-residual / gain-scaled
// signal and derives the transmitted NLSF from a last-half Burg — this works
// directly on the time-domain signal and on our codebook-quantized NLSF (the
// transmitted current frame value), evaluating only the interpolation decision.
// The returned LPC set (subframes 0,1) is built from the same quantized NLSF
// interpolation the decoder applies, so encoder analysis-by-synthesis stays
// aligned with decoder reconstruction.
//
// Gating: only 4-subframe frames, not the first frame after reset, never
// inactive frames, and voiced frames only when the trellis NSQ honours the
// per-subframe LPC sets (the homebrew voiced path cannot re-whiten mid-frame).
func (e *Encoder) selectNLSFInterpolation(signal []float64, cb *nlsfCBParams, signalType int, nlsfQ15, lpcQ12 []int16) (int, []int16) {
	if e.nSubframes != 4 || e.firstFrameAfterReset {
		return 4, nil
	}
	if signalType == SignalTypeInactive {
		return 4, nil
	}
	// First cut: restrict interpolation to mono SILK-only frames, matching the
	// staging discipline of the earlier SILK quality steps. Stereo and hybrid
	// share tighter packet budgets / separate conformance constraints and are
	// expanded only after dedicated libopus-decode validation.
	if e.stereoComponent || e.hybridMode {
		return 4, nil
	}
	if signalType == SignalTypeVoiced && !e.voicedUsesTrellis() {
		return 4, nil
	}
	if len(e.prevNLSFQ15) != cb.order {
		return 4, nil
	}

	half := e.frameSize / 2
	if half <= cb.order {
		return 4, nil
	}

	// Baseline: residual of the first half using the current (non-interpolated) LPC.
	// libopus picks the interpolation index with strictly lower first-half residual
	// (silk_find_LPC_FLP: `if res_nrg_interp < res_nrg`). We mirror that comparison.
	//
	// Caveat (documented WIP): libopus runs this decision on the gain-scaled /
	// LTP-residual signal and transmits a last-half Burg NLSF, so subframes 2,3 stay
	// optimal and only 0,1 interpolate from a consistent basis. We decide on the
	// time-domain signal against the codebook-quantized full-frame NLSF, so on
	// synthetic sustained tones (where codebook jitter makes prevNLSF != currNLSF)
	// the open-loop residual can favour interpolation that the closed-loop NSQ
	// reconstructs slightly worse. The full benefit needs the find_LPC_FLP-domain
	// port; the 2-set NSQ + interpolation wiring here is validated against libopus
	// decode (opusref) and is the foundation for that follow-up.
	bestNrg := firstHalfLPCResidual(signal, lpcQ12, cb.order, half)
	bestFactor := 4
	var bestLPC []int16

	// Search interpolation indices 3..0 (matching libopus iteration order).
	for k := 3; k >= 0; k-- {
		interpNLSF := interpolateNLSFQ15(e.prevNLSFQ15, nlsfQ15, k, cb)
		interpLPC := nlsfToLPCLibopus(interpNLSF, cb.order)
		nrg := firstHalfLPCResidual(signal, interpLPC, cb.order, half)
		if nrg < bestNrg {
			bestNrg = nrg
			bestFactor = k
			bestLPC = interpLPC
		}
	}
	return bestFactor, bestLPC
}

func (e *Encoder) selectFaithfulBurgInterpolationLPCInPre(cb *nlsfCBParams, fullTargetQ15, lastHalfQ15 []int16, domain *lpcBurgDomain) int {
	if e.nSubframes != 4 || e.firstFrameAfterReset {
		return 4
	}
	if domain == nil || domain.nbSubfr != 4 || domain.subfrLength <= cb.order {
		return 4
	}
	if len(domain.signal) < domain.subfrLength*domain.nbSubfr ||
		len(fullTargetQ15) != cb.order || len(lastHalfQ15) != cb.order ||
		len(e.prevNLSFQ15) != cb.order {
		return 4
	}

	bestNrg := faithfulBurgFirstHalfBaseline(domain.signal, domain.subfrLength, domain.nbSubfr, cb.order, domain.minInvGain)
	if bestNrg < 0 || math.IsNaN(bestNrg) || math.IsInf(bestNrg, 0) {
		fullLPC := nlsfToLPCLibopus(fullTargetQ15, cb.order)
		bestNrg = firstHalfStackedLPCResidual(domain.signal, fullLPC, cb.order, domain.subfrLength, domain.nbSubfr)
	}

	bestFactor := 4
	resNrg2nd := math.Inf(1)
	for k := 3; k >= 0; k-- {
		interpNLSF := interpolateNLSFQ15(e.prevNLSFQ15, lastHalfQ15, k, cb)
		interpLPC := nlsfToLPCLibopus(interpNLSF, cb.order)
		nrg := firstHalfStackedLPCResidual(domain.signal, interpLPC, cb.order, domain.subfrLength, domain.nbSubfr)
		if nrg < bestNrg {
			bestNrg = nrg
			bestFactor = k
		} else if nrg > resNrg2nd {
			break
		}
		resNrg2nd = nrg
	}
	return bestFactor
}

func faithfulBurgFirstHalfBaseline(preSignal []float64, subfrLength, nbSubfr, order int, minInvGain float64) float64 {
	if order <= 0 || subfrLength <= order || nbSubfr != 4 || len(preSignal) < subfrLength*nbSubfr {
		return math.Inf(1)
	}
	if minInvGain <= 0 {
		minInvGain = lpcMinInvGain(0, 1, false)
	}
	_, fullNrg := silkBurgModifiedFLP(preSignal[:subfrLength*nbSubfr], minInvGain, subfrLength, nbSubfr, order)
	secondStart := (nbSubfr / 2) * subfrLength
	_, secondNrg := silkBurgModifiedFLP(preSignal[secondStart:subfrLength*nbSubfr], minInvGain, subfrLength, nbSubfr/2, order)
	return fullNrg - secondNrg
}

func transparentBurgTransmitTarget(transparentFull, transparentLastHalf []int16, interpFactor int) []int16 {
	if interpFactor < 4 {
		return append([]int16(nil), transparentLastHalf...)
	}
	return append([]int16(nil), transparentFull...)
}

func interpolatedLPCForTransmittedNLSF(prevQ15, transmittedQ15 []int16, interpFactor int, cb *nlsfCBParams) []int16 {
	if interpFactor >= 4 || len(prevQ15) != cb.order || len(transmittedQ15) != cb.order {
		return nil
	}
	interpNLSF := interpolateNLSFQ15(prevQ15, transmittedQ15, interpFactor, cb)
	return nlsfToLPCLibopus(interpNLSF, cb.order)
}

// interpolateNLSFQ15 reproduces the decoder's quantized-NLSF interpolation
// (decoder.go: prev + ((factor*(curr-prev))>>2)) followed by stabilization, so
// the encoder's subframe-0/1 LPC matches what the decoder reconstructs for the
// chosen interpolation factor.
func interpolateNLSFQ15(prevQ15, currQ15 []int16, factor int, cb *nlsfCBParams) []int16 {
	out := make([]int16, cb.order)
	for i := 0; i < cb.order; i++ {
		prev := int32(prevQ15[i])
		curr := int32(currQ15[i])
		out[i] = int16(prev + ((int32(factor) * (curr - prev)) >> 2))
	}
	silkNLSFStabilize(out, cb.deltaMinQ15, cb.order)
	return out
}

// firstHalfLPCResidual returns the mean LPC residual energy over [order, half)
// of the signal, skipping the order-sample warm-up so all interpolation
// candidates are compared on the same fully-predicted window.
func firstHalfLPCResidual(signal []float64, lpcQ12 []int16, order, half int) float64 {
	if half > len(signal) {
		half = len(signal)
	}
	if half <= order {
		return 0
	}
	energy := 0.0
	for i := order; i < half; i++ {
		pred := 0.0
		for j := 0; j < order && j < len(lpcQ12); j++ {
			pred += float64(lpcQ12[j]) / 4096.0 * signal[i-j-1]
		}
		err := signal[i] - pred
		energy += err * err
	}
	return energy / float64(half-order)
}

func (e *Encoder) lpcNLSFTargetQ15(signal []float64, cb *nlsfCBParams, domain ...*lpcBurgDomain) ([]int16, bool) {
	burgSignal := signal
	subfrLength := len(signal)
	nbSubfr := 1
	minInvGain := lpcMinInvGain(0, 1, e.firstFrameAfterReset)
	if len(domain) > 0 && domain[0] != nil {
		burgSignal = domain[0].signal
		subfrLength = domain[0].subfrLength
		nbSubfr = domain[0].nbSubfr
		minInvGain = domain[0].minInvGain
	}
	if len(burgSignal) <= cb.order || subfrLength <= cb.order || nbSubfr <= 0 || len(burgSignal) < subfrLength*nbSubfr {
		return nil, false
	}
	// Burg-method LPC over the whole frame (single subframe), then accurate
	// A2NLSF root finding — the libopus silk_find_LPC_FLP path. minInvGain
	// bounds the prediction gain with the libopus LTP/coding-quality formula.
	a, _ := silkBurgModifiedFLP(burgSignal, minInvGain, subfrLength, nbSubfr, cb.order)
	target := silkA2NLSFFLP(a, cb.order)
	if len(target) != cb.order {
		return nil, false
	}
	silkNLSFStabilize(target, cb.deltaMinQ15, cb.order)
	return target, true
}

func bestNLSFAnalysis(signal []float64, cb *nlsfCBParams, targetQ15 []int16, hasTarget bool) (int, []int) {
	type candidate struct {
		cb1Idx int
		rawIdx []int
	}

	candidates := []candidate{}
	legacyCB1 := bestNLSFStage1(signal, cb)
	candidates = append(candidates, candidate{
		cb1Idx: legacyCB1,
		rawIdx: refineNLSFResidual(signal, cb, legacyCB1),
	})

	if hasTarget {
		for _, cb1 := range topNLSFCB1ByTarget(cb, targetQ15, 6) {
			seed := rawNLSFResidualForTarget(cb, cb1, targetQ15)
			candidates = append(candidates, candidate{
				cb1Idx: cb1,
				rawIdx: refineNLSFResidualFrom(signal, cb, cb1, seed),
			})
		}
	}

	best := candidates[0]
	legacyLPC := nlsfToLPCLibopus(reconstructNLSFQ15(cb, best.cb1Idx, best.rawIdx), cb.order)
	legacyGain := lpcSpectralPeakGain(legacyLPC)
	bestCost := lpcResidualEnergy(signal, legacyLPC)
	for _, cand := range candidates {
		nlsfQ15 := reconstructNLSFQ15(cb, cand.cb1Idx, cand.rawIdx)
		lpcQ12 := nlsfToLPCLibopus(nlsfQ15, cb.order)
		peakGain := lpcSpectralPeakGain(lpcQ12)
		if peakGain > math.Max(18.0, legacyGain*1.35) {
			continue
		}
		cost := lpcResidualEnergy(signal, lpcQ12)
		if hasTarget {
			cost += 1e-8 * nlsfTargetDistortion(cb, cand.cb1Idx, nlsfQ15, targetQ15)
		}
		if cost < bestCost {
			bestCost = cost
			best = cand
		}
	}
	return best.cb1Idx, best.rawIdx
}

func lpcSpectralPeakGain(lpcQ12 []int16) float64 {
	const grid = 128
	peak := 1.0
	for g := 0; g < grid; g++ {
		w := math.Pi * (float64(g) + 0.5) / grid
		realPart := 1.0
		imagPart := 0.0
		for i, c := range lpcQ12 {
			a := float64(c) / 4096.0
			phase := -w * float64(i+1)
			realPart -= a * math.Cos(phase)
			imagPart -= a * math.Sin(phase)
		}
		den := realPart*realPart + imagPart*imagPart
		if den <= 1e-12 {
			return math.Inf(1)
		}
		gain := 1.0 / math.Sqrt(den)
		if gain > peak {
			peak = gain
		}
	}
	return peak
}

func lpcEnvelopeLoudnessDB(lpcQ12 []int16) float64 {
	const grid = 128
	sumPower := 0.0
	for g := 0; g < grid; g++ {
		w := math.Pi * (float64(g) + 0.5) / grid
		realPart := 1.0
		imagPart := 0.0
		for i, c := range lpcQ12 {
			a := float64(c) / 4096.0
			phase := -w * float64(i+1)
			realPart -= a * math.Cos(phase)
			imagPart -= a * math.Sin(phase)
		}
		den := realPart*realPart + imagPart*imagPart
		if den <= 1e-12 {
			return math.Inf(1)
		}
		sumPower += 1.0 / den
	}
	if sumPower <= 0 {
		return math.Inf(-1)
	}
	return 10.0 * math.Log10(sumPower/float64(grid))
}

func bestNLSFStage1(signal []float64, cb *nlsfCBParams) int {
	bestIdx := 0
	bestCost := math.Inf(1)
	rawIdx := make([]int, cb.order)
	for idx := 0; idx < cb.nEntries; idx++ {
		nlsfQ15 := reconstructNLSFQ15(cb, idx, rawIdx)
		lpcQ12 := nlsfToLPCLibopus(nlsfQ15, cb.order)
		cost := lpcResidualEnergy(signal, lpcQ12)
		if cost < bestCost {
			bestCost = cost
			bestIdx = idx
		}
	}
	return bestIdx
}

func refineNLSFResidual(signal []float64, cb *nlsfCBParams, cb1Idx int) []int {
	return refineNLSFResidualFrom(signal, cb, cb1Idx, nil)
}

func refineNLSFResidualFrom(signal []float64, cb *nlsfCBParams, cb1Idx int, seed []int) []int {
	rawIdx := make([]int, cb.order)
	copy(rawIdx, seed)
	for i := range rawIdx {
		rawIdx[i] = clampInt(rawIdx[i], -3, 3)
	}
	var trialBuf [silkMaxLPCOrder]int
	var nlsfBuf, lpcBuf [silkMaxLPCOrder]int16
	trial := trialBuf[:cb.order]
	bestCost := nlsfResidualCostWithScratch(signal, cb, cb1Idx, rawIdx, nlsfBuf[:], lpcBuf[:])

	for pass := 0; pass < 3; pass++ {
		improved := false
		for i := 0; i < cb.order; i++ {
			bestVal := rawIdx[i]
			for _, candidate := range []int{-3, -2, -1, 0, 1, 2, 3} {
				if candidate == rawIdx[i] {
					continue
				}
				copy(trial, rawIdx)
				trial[i] = candidate
				cost := nlsfResidualCostWithScratch(signal, cb, cb1Idx, trial, nlsfBuf[:], lpcBuf[:])
				if cost < bestCost {
					bestCost = cost
					bestVal = candidate
				}
			}
			if bestVal != rawIdx[i] {
				rawIdx[i] = bestVal
				improved = true
			}
		}
		if !improved {
			break
		}
	}
	return rawIdx
}

func nlsfResidualCostWithScratch(signal []float64, cb *nlsfCBParams, cb1Idx int, rawIdx []int, nlsfBuf, lpcBuf []int16) float64 {
	nlsfQ15 := reconstructNLSFQ15Into(nlsfBuf, cb, cb1Idx, rawIdx)
	lpcQ12 := nlsfToLPCLibopusInto(lpcBuf, nlsfQ15, cb.order)
	return lpcResidualEnergy(signal, lpcQ12)
}

func topNLSFCB1ByTarget(cb *nlsfCBParams, targetQ15 []int16, n int) []int {
	if n > cb.nEntries {
		n = cb.nEntries
	}
	bestIdx := make([]int, 0, n)
	bestCost := make([]float64, 0, n)
	rawZero := make([]int, cb.order)
	for idx := 0; idx < cb.nEntries; idx++ {
		nlsfQ15 := reconstructNLSFQ15(cb, idx, rawZero)
		cost := nlsfTargetDistortion(cb, idx, nlsfQ15, targetQ15)
		insert := len(bestCost)
		for insert > 0 && cost < bestCost[insert-1] {
			insert--
		}
		if insert >= n {
			continue
		}
		bestIdx = append(bestIdx, 0)
		bestCost = append(bestCost, 0)
		copy(bestIdx[insert+1:], bestIdx[insert:])
		copy(bestCost[insert+1:], bestCost[insert:])
		bestIdx[insert] = idx
		bestCost[insert] = cost
		if len(bestIdx) > n {
			bestIdx = bestIdx[:n]
			bestCost = bestCost[:n]
		}
	}
	return bestIdx
}

func rawNLSFResidualForTarget(cb *nlsfCBParams, cb1Idx int, targetQ15 []int16) []int {
	rawIdx := make([]int, cb.order)
	predQ8 := nlsfPredQ8ForCB1(cb, cb1Idx)
	desiredResQ10 := make([]int32, cb.order)
	for i := 0; i < cb.order; i++ {
		cb1Val := int32(cb.cb1Q8[cb1Idx*cb.order+i]) << 7
		wghtQ9 := int32(cb.cb1WghtQ9[cb1Idx*cb.order+i])
		desiredResQ10[i] = int32(int64(int32(targetQ15[i])-cb1Val) * int64(wghtQ9) >> 14)
	}

	const nlsfQuantLevelAdjQ10 = int32(102)
	nextOutQ10 := int32(0)
	for i := cb.order - 1; i >= 0; i-- {
		predQ10 := (nextOutQ10 * int32(predQ8[i])) >> 8
		bestRaw := 0
		bestErr := int64(1<<63 - 1)
		bestOut := int32(0)
		for raw := -3; raw <= 3; raw++ {
			out := int32(raw) << 10
			if out > 0 {
				out -= nlsfQuantLevelAdjQ10
			} else if out < 0 {
				out += nlsfQuantLevelAdjQ10
			}
			out = predQ10 + int32((int64(out)*int64(cb.quantStepSizeQ16))>>16)
			err := int64(out - desiredResQ10[i])
			if err < 0 {
				err = -err
			}
			if err < bestErr {
				bestErr = err
				bestRaw = raw
				bestOut = out
			}
		}
		rawIdx[i] = bestRaw
		nextOutQ10 = bestOut
	}
	return rawIdx
}

func nlsfPredQ8ForCB1(cb *nlsfCBParams, cb1Idx int) []uint8 {
	predQ8 := make([]uint8, cb.order)
	ecSelBase := cb1Idx * (cb.order / 2)
	for i := 0; i < cb.order; i += 2 {
		entry := cb.cb2Select[ecSelBase+i/2]
		predQ8[i] = cb.predQ8[i+int((entry&1))*int(cb.order-1)]
		predQ8[i+1] = cb.predQ8[i+int((entry>>4)&1)*int(cb.order-1)+1]
	}
	return predQ8
}

func nlsfTargetDistortion(cb *nlsfCBParams, cb1Idx int, nlsfQ15, targetQ15 []int16) float64 {
	if len(targetQ15) != cb.order || len(nlsfQ15) != cb.order {
		return math.Inf(1)
	}
	cost := 0.0
	for i := 0; i < cb.order; i++ {
		diff := float64(int(nlsfQ15[i]) - int(targetQ15[i]))
		w := float64(cb.cb1WghtQ9[cb1Idx*cb.order+i]) / 512.0
		cost += w * diff * diff
	}
	return cost
}

func reconstructNLSFQ15(cb *nlsfCBParams, cb1Idx int, rawIdx []int) []int16 {
	nlsfQ15 := make([]int16, cb.order)
	return reconstructNLSFQ15Into(nlsfQ15, cb, cb1Idx, rawIdx)
}

func reconstructNLSFQ15Into(nlsfQ15 []int16, cb *nlsfCBParams, cb1Idx int, rawIdx []int) []int16 {
	nlsfQ15 = nlsfQ15[:cb.order]
	if cb1Idx < 0 {
		cb1Idx = 0
	}
	if cb1Idx >= cb.nEntries {
		cb1Idx = cb.nEntries - 1
	}

	const nlsfQuantLevelAdjQ10 = int32(102)
	var predQ8 [silkMaxLPCOrder]uint8
	ecSelBase := cb1Idx * (cb.order / 2)
	for i := 0; i < cb.order; i += 2 {
		entry := cb.cb2Select[ecSelBase+i/2]
		predQ8[i] = cb.predQ8[i+int((entry&1))*int(cb.order-1)]
		predQ8[i+1] = cb.predQ8[i+int((entry>>4)&1)*int(cb.order-1)+1]
	}

	var resQ10 [silkMaxLPCOrder]int32
	outQ10 := int32(0)
	for i := cb.order - 1; i >= 0; i-- {
		idx := 0
		if i < len(rawIdx) {
			idx = clampInt(rawIdx[i], -nlsfQuantMaxAmplitudeExt, nlsfQuantMaxAmplitudeExt)
		}
		predQ10 := (outQ10 * int32(predQ8[i])) >> 8
		outQ10 = int32(idx) << 10
		if outQ10 > 0 {
			outQ10 -= nlsfQuantLevelAdjQ10
		} else if outQ10 < 0 {
			outQ10 += nlsfQuantLevelAdjQ10
		}
		outQ10 = predQ10 + int32((int64(outQ10)*int64(cb.quantStepSizeQ16))>>16)
		resQ10[i] = outQ10
	}

	for i := 0; i < cb.order; i++ {
		cb1Val := int32(cb.cb1Q8[cb1Idx*cb.order+i])
		wghtQ9 := int32(cb.cb1WghtQ9[cb1Idx*cb.order+i])
		div := int32(0)
		if wghtQ9 != 0 {
			div = (resQ10[i] << 14) / wghtQ9
		}
		tmp := div + (cb1Val << 7)
		if tmp < 0 {
			tmp = 0
		}
		if tmp > 32767 {
			tmp = 32767
		}
		nlsfQ15[i] = int16(tmp)
	}
	silkNLSFStabilize(nlsfQ15, cb.deltaMinQ15, cb.order)
	return nlsfQ15
}

func lpcResidualEnergy(signal []float64, lpcQ12 []int16) float64 {
	if len(signal) == 0 {
		return 0
	}
	energy := 0.0
	for i := range signal {
		pred := 0.0
		for j := 0; j < len(lpcQ12) && j < i; j++ {
			pred += float64(lpcQ12[j]) / 4096.0 * signal[i-j-1]
		}
		err := signal[i] - pred
		energy += err * err
	}
	return energy / float64(len(signal))
}

func nlsfQ15ToRadians(nlsfQ15 []int16) []float64 {
	out := make([]float64, len(nlsfQ15))
	for i, v := range nlsfQ15 {
		out[i] = float64(v) / 32768.0 * math.Pi
	}
	return out
}

func (e *Encoder) encodeNLSF(enc *entcode.Encoder, cb *nlsfCBParams, signalType int, analysis nlsfAnalysis) {
	cb1Idx := clampInt(analysis.cb1Idx, 0, cb.nEntries-1)
	offset := (signalType >> 1) * cb.nEntries
	enc.EncodeIcdf(cb1Idx, cb.cb1ICDF[offset:offset+cb.nEntries], 8)

	ecSelBase := cb1Idx * (cb.order / 2)
	for i := 0; i < cb.order; i += 2 {
		entry := cb.cb2Select[ecSelBase+i/2]
		ecIx0 := ((int(entry) >> 1) & 7) * 9
		ecIx1 := ((int(entry) >> 5) & 7) * 9
		encodeNLSFResidualIndex(enc, nlsfRawIndex(analysis.rawIdx, i), cb.cb2ICDF[ecIx0:ecIx0+9])
		encodeNLSFResidualIndex(enc, nlsfRawIndex(analysis.rawIdx, i+1), cb.cb2ICDF[ecIx1:ecIx1+9])
	}
}

func nlsfRawIndex(rawIdx []int, i int) int {
	if i >= len(rawIdx) {
		return 0
	}
	return clampInt(rawIdx[i], -nlsfQuantMaxAmplitudeExt, nlsfQuantMaxAmplitudeExt)
}

func encodeNLSFResidualIndex(enc *entcode.Encoder, idx int, icdf []uint8) {
	switch {
	case idx >= nlsfQuantMaxAmplitude:
		enc.EncodeIcdf(2*nlsfQuantMaxAmplitude, icdf, 8)
		enc.EncodeIcdf(clampInt(idx-nlsfQuantMaxAmplitude, 0, len(silkNLSFExtICDF)-1), silkNLSFExtICDF[:], 8)
	case idx <= -nlsfQuantMaxAmplitude:
		enc.EncodeIcdf(0, icdf, 8)
		enc.EncodeIcdf(clampInt(-idx-nlsfQuantMaxAmplitude, 0, len(silkNLSFExtICDF)-1), silkNLSFExtICDF[:], 8)
	default:
		enc.EncodeIcdf(idx+nlsfQuantMaxAmplitude, icdf, 8)
	}
}

type pulseBlock struct {
	pulses   [shellCodecFrameLength]int16
	shellAbs [shellCodecFrameLength]int
	sum      int
	nLShifts int
}

func (e *Encoder) encodePulses(enc *entcode.Encoder, pulses []int16, signalType, quantOffset int) {
	blocks := makePulseBlocks(pulses, e.frameSize)

	row := signalType >> 1
	if row > 1 {
		row = 1
	}
	rateLevelIdx := selectPulseRateLevel(row, blocks)
	enc.EncodeIcdf(rateLevelIdx, silkRateLevelsICDF[row][:], 8)

	for i := range blocks {
		encodePulseBlockSum(enc, rateLevelIdx, blocks[i])
	}
	for i := range blocks {
		if blocks[i].sum > 0 {
			encodeShellBlock(enc, blocks[i].shellAbs)
		}
	}
	for i := range blocks {
		encodePulseLSBs(enc, blocks[i])
	}
	encodePulseSigns(enc, blocks, signalType, quantOffset)
}

func (e *Encoder) simpleNSQ(excitation []float64, gainIndices []int, signalType, quantOffset int, seed int32) []int16 {
	pulses := make([]int16, e.frameSize)
	if signalType == SignalTypeInactive || len(excitation) == 0 {
		return pulses
	}

	uvIdx := 0
	if signalType == SignalTypeVoiced {
		uvIdx = 1
	}
	offsetQ14 := int32(silkQuantizationOffsetsQ10[uvIdx][quantOffset]) << 4
	subframeLen := e.frameSize / e.nSubframes
	shape := 0.35
	if signalType == SignalTypeVoiced {
		shape = 0.45
	}

	err := 0.0
	for i := 0; i < e.frameSize && i < len(excitation); i++ {
		sf := i / subframeLen
		if sf >= len(gainIndices) {
			sf = len(gainIndices) - 1
		}
		gainQ10 := silkGainDequantQ16(gainIndices[sf]) >> 6
		if gainQ10 <= 0 {
			continue
		}

		target := excitation[i] + shape*err
		if target > 2.0 {
			target = 2.0
		} else if target < -2.0 {
			target = -2.0
		}

		desiredQ14 := int32(math.Round(target * (float64(int64(1)<<39) / float64(gainQ10))))
		seed = 196314165*seed + 907633515
		pulse := chooseNSQPulse(desiredQ14, offsetQ14, seed < 0)
		pulses[i] = pulse

		reconQ14 := decodedExcitationQ14(int(pulse), offsetQ14, seed < 0)
		recon := float64(reconQ14) * float64(gainQ10) / float64(int64(1)<<39)
		err = target - recon
		seed += int32(pulse)
	}
	return pulses
}

func (e *Encoder) closedLoopNSQ(
	signal []float64,
	lpcQ12 []int16,
	gainIndices []int,
	signalType, quantOffset int,
	seed int32,
	pitchLags []int,
	ltpCoeffsQ14 [][5]int16,
	ltpScaleQ14 int16,
) []int16 {
	return e.closedLoopNSQWithRateScale(signal, lpcQ12, nil, gainIndices,
		signalType, quantOffset, seed, pitchLags, ltpCoeffsQ14, ltpScaleQ14, 1)
}

// voicedUsesTrellis reports whether voiced frames take the Step 4 trellis NSQ.
func (e *Encoder) voicedUsesTrellis() bool {
	return e.useTrellisNSQ
}

// unvoicedUsesTrellis reports whether unvoiced frames take the full
// delayed-decision trellis NSQ instead of the single-state homebrew quantizer.
// Like voiced, unvoiced is paired with the co-designed noise-shape envelope
// gains (shapeGainIndices); the trellis perceptual shaping only beats homebrew's
// broadband SNR when the gains are co-designed with it (Step 3/4 lesson).
//
// Phase 6 widens this beyond mono SILK-only. The trellis path already neutralises
// stereo-component and unvoiced spectral shaping before NSQ, preserving the
// broadband-SNR objective while retaining delayed-decision rate/distortion
// search. Hybrid still supplies its own CELT high band; this gate only decides
// the SILK low-band excitation quantizer.
func (e *Encoder) unvoicedUsesTrellis() bool {
	return e.useTrellisNSQ
}

// TrellisNSQ reports whether voiced SILK-only frames may use the trellis NSQ.
func (e *Encoder) TrellisNSQ() bool {
	return e.useTrellisNSQ
}

// LastFinalRange returns the pre-flush entropy range of the last standalone stream.
func (e *Encoder) LastFinalRange() uint32 {
	return e.lastFinalRange
}

// SetTrellisNSQ enables or disables the voiced SILK-only trellis NSQ.
func (e *Encoder) SetTrellisNSQ(enabled bool) {
	e.useTrellisNSQ = enabled
}

// LastStreamSNRVBR reports whether the most recently encoded SILK stream used
// the voiced SNR-target natural-size path.
func (e *Encoder) LastStreamSNRVBR() bool {
	return e.lastSNRVBRStream
}

// SetHybridMode marks subsequent frames as the SILK low band of a hybrid packet
// (see hybridMode). The hybrid encoder sets it before encoding and clears it
// after so the same SILK encoder instance can also serve SILK-only packets.
func (e *Encoder) SetHybridMode(on bool) {
	e.hybridMode = on
	if e.side != nil {
		e.side.SetHybridMode(on)
	}
}

// closedLoopNSQWithRateScale quantizes the excitation with the FLP
// noise-shaping analysis (Q3) feeding the delayed-decision trellis NSQ (Q4),
// the libopus core ported in noise_shape.go / nsq_del_dec.go. The legacy
// single-state quantizer is retained as closedLoopNSQHomebrew for reference and
// A/B debugging. The rate-control search lever (rateScale) is mapped onto the
// trellis rate weight Lambda: a larger rateScale raises Lambda, suppressing
// pulses, mirroring the homebrew pulseRatePenalty semantics the search expects.
func (e *Encoder) closedLoopNSQWithRateScale(
	signal []float64,
	lpcQ12 []int16,
	lpcQ12Interp []int16,
	gainIndices []int,
	signalType, quantOffset int,
	seed int32,
	pitchLags []int,
	ltpCoeffsQ14 [][5]int16,
	ltpScaleQ14 int16,
	rateScale float64,
) []int16 {
	// The delayed-decision trellis with the co-designed process_gains gains is a
	// clear win when its perceptual shaping is paired with the co-designed
	// noise-shape envelope gains (Step 4). Both voiced and unvoiced take it with
	// those gains; inactive frames produce no excitation (the near-silent path)
	// and stay on the homebrew zero-pulse branch. Dispatch by type.
	useTrellis := false
	switch signalType {
	case SignalTypeVoiced:
		useTrellis = e.voicedUsesTrellis()
	case SignalTypeUnvoiced:
		useTrellis = e.unvoicedUsesTrellis()
	}
	if !useTrellis {
		return e.closedLoopNSQHomebrew(signal, lpcQ12, lpcQ12Interp, gainIndices,
			signalType, quantOffset, seed, pitchLags, ltpCoeffsQ14, ltpScaleQ14, rateScale)
	}
	if len(signal) == 0 {
		e.updateSilentSynthesisState()
		e.updateSilentNSQState()
		return make([]int16, e.frameSize)
	}
	if rateScale < 1 {
		rateScale = 1
	}

	x16 := make([]int16, e.frameSize)
	for i := 0; i < e.frameSize && i < len(signal); i++ {
		x16[i] = clamp16(int32(math.Round(signal[i] * 32768.0)))
	}

	var gainsQ16 [silkMaxNBSubframes]int32
	var pitchL [silkMaxNBSubframes]int
	for sf := 0; sf < e.nSubframes; sf++ {
		gIdx := gainIndices[clampInt(sf, 0, len(gainIndices)-1)]
		gq := silkGainDequantQ16(gIdx)
		if gq < 1 {
			gq = 1
		}
		gainsQ16[sf] = gq
		lag := e.prevPitchLag
		if sf < len(pitchLags) && pitchLags[sf] > 0 {
			lag = pitchLags[sf]
		}
		if lag < 1 {
			lag = 1
		}
		pitchL[sf] = lag
	}

	pitchGain := estimatePitchGainFromLTP(ltpCoeffsQ14)
	shape := e.analyzeNoiseShapeFLP(signal, lpcQ12, signalType, quantOffset, pitchLags, pitchGain, e.speechActivity)
	if e.stereoComponent || signalType == SignalTypeUnvoiced {
		// Stereo mid/side components are later reconstructed and resampled as a
		// coupled signal, where component-domain spectral shaping concentrates
		// quantization noise near the SILK layer edge.
		//
		// Unvoiced/noise: the perceptual AR/tilt/LF shaping is what libopus uses,
		// but it deliberately spreads quantization noise to perceptually masked
		// bands, which lowers the broadband SNR this project scores against (the
		// reason unvoiced previously stayed on homebrew). Running the
		// delayed-decision trellis with *neutral* shaping keeps the lookahead
		// rate-distortion win — strictly stronger than the greedy single-state
		// homebrew quantizer — while optimising broadband error, so the trellis
		// can match or beat homebrew on noise instead of regressing it.
		//
		// In both cases keep the delayed-decision trellis and its rate term but
		// drop the spectral shaping.
		shape.AR_Q13 = [silkMaxNBSubframes][silkMaxShapeLPCOrder]int16{}
		shape.LF_shp_Q14 = [silkMaxNBSubframes]int32{}
		shape.Tilt_Q14 = [silkMaxNBSubframes]int32{}
		shape.HarmShapeGain_Q14 = [silkMaxNBSubframes]int32{}
		shape.Warping_Q16 = 0
	}

	lambdaQ10 := shape.Lambda_Q10
	if rateScale > 1 {
		lambdaQ10 = int32(float64(shape.Lambda_Q10) * (1.0 + 0.5*math.Log2(rateScale)))
	}
	if lambdaQ10 < 64 {
		lambdaQ10 = 64
	}

	return e.silkNSQDelDec(x16, lpcQ12, lpcQ12Interp, ltpCoeffsQ14, shape, gainsQ16, pitchL,
		lambdaQ10, ltpScaleQ14, signalType, quantOffset, seed)
}

func (e *Encoder) updateSilentNSQState() {
	ltpMemLen := silkLTPMemLengthMs * (e.sampleRate / 1000)
	if len(e.nsq.xq) == ltpMemLen+e.frameSize {
		copy(e.nsq.xq, e.nsq.xq[e.frameSize:])
		for i := ltpMemLen; i < len(e.nsq.xq); i++ {
			e.nsq.xq[i] = 0
		}
		copy(e.nsq.sLTPShpQ14, e.nsq.sLTPShpQ14[e.frameSize:])
		for i := ltpMemLen; i < len(e.nsq.sLTPShpQ14); i++ {
			e.nsq.sLTPShpQ14[i] = 0
		}
	}
	for i := range e.nsq.sLPCQ14 {
		e.nsq.sLPCQ14[i] = 0
	}
	for i := range e.nsq.sAR2Q14 {
		e.nsq.sAR2Q14[i] = 0
	}
	e.nsq.sLFARShpQ14 = 0
	e.nsq.sDiffShpQ14 = 0
}

func (e *Encoder) closedLoopNSQHomebrew(
	signal []float64,
	lpcQ12 []int16,
	lpcQ12Interp []int16,
	gainIndices []int,
	signalType, quantOffset int,
	seed int32,
	pitchLags []int,
	ltpCoeffsQ14 [][5]int16,
	ltpScaleQ14 int16,
	rateScale float64,
) []int16 {
	pulses := make([]int16, e.frameSize)
	if signalType == SignalTypeInactive || len(signal) == 0 {
		e.updateSilentSynthesisState()
		e.syncTrellisNSQState()
		return pulses
	}
	if rateScale < 1 {
		rateScale = 1
	}
	// NLSF interpolation: subframes 0,1 use the interpolated LPC set. This path
	// handles unvoiced frames (no mid-frame re-whitening — the voiced rewhitening
	// block below is gated on TYPE_VOICED), and voiced frames only reach here when
	// the trellis is disabled, in which case selectNLSFInterpolation already
	// forced interpFactor==4 (lpcQ12Interp==nil) so there is no LPC-set switch.
	interpActive := len(lpcQ12Interp) >= e.lpcOrder
	lpcForSubframe := func(sf int) []int16 {
		if interpActive && sf < 2 {
			return lpcQ12Interp
		}
		return lpcQ12
	}

	uvIdx := 0
	if signalType == SignalTypeVoiced {
		uvIdx = 1
	}
	offsetQ14 := int32(silkQuantizationOffsetsQ10[uvIdx][quantOffset]) << 4
	subframeLen := e.frameSize / e.nSubframes

	ltpMemLen := len(e.ltpState)
	sLTPQ15 := make([]int32, ltpMemLen+e.frameSize)
	outBufQ0 := make([]int32, ltpMemLen+e.frameSize)
	copy(outBufQ0, e.ltpState)
	sLTPBufIdx := ltpMemLen

	sLPCQ14 := make([]int32, silkMaxLPCOrder+subframeLen)
	copy(sLPCQ14[:silkMaxLPCOrder], e.lpcState)
	output := make([]int16, e.frameSize)
	shaping := e.analyzeNoiseShape(signal, signalType, estimatePitchGainFromLTP(ltpCoeffsQ14))
	errHist := make([]float64, ltpMemLen+e.frameSize)
	shapeErr := 0.0
	prevShapeErr := 0.0
	lfShapeErr := 0.0
	pulseRatePenalty := 96.0 * float64(int64(1)<<20)
	if signalType == SignalTypeVoiced {
		pulseRatePenalty = 28.0 * float64(int64(1)<<20)
	}
	pulseRatePenalty *= rateScale

	for sf := 0; sf < e.nSubframes; sf++ {
		start := sf * subframeLen
		aQ12 := lpcForSubframe(sf)
		gainIdx := gainIndices[clampInt(sf, 0, len(gainIndices)-1)]
		gainQ16 := silkGainDequantQ16(gainIdx)
		gainQ10 := gainQ16 >> 6
		if gainQ10 <= 0 {
			gainQ10 = 1
		}
		shape := shapeForSubframe(shaping, sf, signalType)
		invGainQ31 := silkInverse32VarQ(gainQ16, 47)
		gainAdjQ16 := int32(1 << 16)
		if gainQ16 != e.prevGainQ16 {
			gainAdjQ16 = silkDIV32VarQ(e.prevGainQ16, gainQ16, 16)
			for i := 0; i < silkMaxLPCOrder; i++ {
				sLPCQ14[i] = silkSMULWW(gainAdjQ16, sLPCQ14[i])
			}
		}
		e.prevGainQ16 = gainQ16

		lag := e.prevPitchLag
		if sf < len(pitchLags) && pitchLags[sf] > 0 {
			lag = pitchLags[sf]
		}
		if signalType == SignalTypeVoiced {
			if sf == 0 {
				startIdx := sLTPBufIdx - lag - e.lpcOrder - 2
				if startIdx < 0 {
					startIdx = 0
				}
				filterLen := sLTPBufIdx - startIdx
				if filterLen > 0 {
					sLTP := make([]int16, filterLen)
					silkLPCAnalysisFilter(sLTP, outBufQ0[startIdx:startIdx+filterLen], aQ12, filterLen, e.lpcOrder)
					invGainQ31 = silkSMULWB(invGainQ31, ltpScaleQ14) << 2
					for i := 0; i < lag+2 && i < filterLen; i++ {
						sLTPQ15[sLTPBufIdx-i-1] = silkSMULWB(invGainQ31, sLTP[filterLen-i-1])
					}
				}
			} else if gainAdjQ16 != 1<<16 {
				for i := 0; i < lag+2 && sLTPBufIdx-i-1 >= 0; i++ {
					idx := sLTPBufIdx - i - 1
					sLTPQ15[idx] = silkSMULWW(gainAdjQ16, sLTPQ15[idx])
				}
			}
		}

		for i := 0; i < subframeLen && start+i < e.frameSize && start+i < len(signal); i++ {
			lpcPredQ10 := int32(e.lpcOrder >> 1)
			for j := 0; j < e.lpcOrder; j++ {
				lpcPredQ10 = silkSMLAWB(lpcPredQ10, sLPCQ14[silkMaxLPCOrder+i-j-1], aQ12[j])
			}
			predQ14 := silkLShiftSat32(lpcPredQ10, 4)

			ltpPredQ14 := int32(0)
			if signalType == SignalTypeVoiced {
				ltpPredQ13 := int32(2)
				var coeffs [5]int16
				if sf < len(ltpCoeffsQ14) {
					coeffs = ltpCoeffsQ14[sf]
				}
				for k := 0; k < 5; k++ {
					ltpIdx := sLTPBufIdx - lag + 2 - k
					if ltpIdx >= 0 && ltpIdx < len(sLTPQ15) {
						ltpPredQ13 = silkSMLAWB(ltpPredQ13, sLTPQ15[ltpIdx], coeffs[k])
					}
				}
				ltpPredQ14 = ltpPredQ13 << 1
			}

			harmonicErr := 0.0
			if signalType == SignalTypeVoiced && lag > 0 {
				errIdx := ltpMemLen + start + i - lag
				if errIdx >= 0 && errIdx < len(errHist) {
					harmonicErr = errHist[errIdx]
				}
			}
			hfErr := shapeErr - prevShapeErr
			target := signal[start+i] +
				shape.feedback*shapeErr +
				shape.tilt*prevShapeErr +
				shape.lf*lfShapeErr +
				shape.hf*hfErr +
				shape.harmonic*harmonicErr
			if target > 1.5 {
				target = 1.5
			} else if target < -1.5 {
				target = -1.5
			}
			desiredQ14 := int32(math.Round(target * (float64(int64(1)<<39) / float64(gainQ10))))
			desiredExcQ14 := desiredQ14 - predQ14 - ltpPredQ14

			seed = 196314165*seed + 907633515
			pulse := chooseNSQPulseShaped(desiredExcQ14, offsetQ14, seed < 0, pulseRatePenalty*shape.lambda)
			pulses[start+i] = pulse

			excQ14 := decodedExcitationQ14(int(pulse), offsetQ14, seed < 0)
			presQ14 := excQ14
			if signalType == SignalTypeVoiced {
				presQ14 += ltpPredQ14
				sLTPQ15[sLTPBufIdx] = presQ14 << 1
				sLTPBufIdx++
			}
			v := silkAddSat32(presQ14, predQ14)
			sLPCQ14[silkMaxLPCOrder+i] = v
			pxq := silkRShiftRound(int64(silkSMULWW(v, gainQ10)), 8)
			output[start+i] = clamp16(pxq)

			recon := float64(output[start+i]) / 32768.0
			prevShapeErr = shapeErr
			shapeErr = signal[start+i] - recon
			lfShapeErr = 0.94*lfShapeErr + shapeErr
			errHist[ltpMemLen+start+i] = shapeErr
			seed += int32(pulse)
		}

		copy(sLPCQ14[:silkMaxLPCOrder], sLPCQ14[subframeLen:subframeLen+silkMaxLPCOrder])
	}

	copy(e.lpcState, sLPCQ14[:silkMaxLPCOrder])
	if e.frameSize <= ltpMemLen {
		mvLen := ltpMemLen - e.frameSize
		copy(e.ltpState[:mvLen], e.ltpState[e.frameSize:])
		for i := 0; i < e.frameSize; i++ {
			e.ltpState[mvLen+i] = int32(output[i])
		}
	}
	e.syncTrellisNSQState()
	return pulses
}

func estimatePitchGainFromLTP(ltpCoeffsQ14 [][5]int16) float64 {
	best := 0.0
	for _, coeffs := range ltpCoeffsQ14 {
		sum := 0.0
		for _, c := range coeffs {
			if c > 0 {
				sum += float64(c) / 16384.0
			}
		}
		if sum > best {
			best = sum
		}
	}
	return clampFloat(best, 0, 1)
}

func shapeForSubframe(analysis silkShapeAnalysis, sf, signalType int) silkShapeSubframe {
	if sf >= 0 && sf < len(analysis.subframes) {
		shape := analysis.subframes[sf]
		if shape.lambda <= 0 {
			shape.lambda = 1
		}
		return shape
	}
	return defaultShapeSubframe(signalType, 0)
}

func (e *Encoder) updateSilentSynthesisState() {
	for i := range e.lpcState {
		e.lpcState[i] = 0
	}
	if len(e.ltpState) == 0 || e.frameSize > len(e.ltpState) {
		return
	}
	mvLen := len(e.ltpState) - e.frameSize
	copy(e.ltpState[:mvLen], e.ltpState[e.frameSize:])
	for i := mvLen; i < len(e.ltpState); i++ {
		e.ltpState[i] = 0
	}
}

func chooseNSQPulse(desiredQ14, offsetQ14 int32, flipSign bool) int16 {
	rawTarget := desiredQ14
	if flipSign {
		rawTarget = -rawTarget
	}
	base := int(math.Round(float64(rawTarget-offsetQ14) / 16384.0))

	bestPulse := 0
	bestErr := int64(1<<63 - 1)
	for p := base - 3; p <= base+3; p++ {
		candidate := clampInt(p, -1024, 1024)
		exc := decodedExcitationQ14(candidate, offsetQ14, flipSign)
		err := int64(exc) - int64(desiredQ14)
		if err < 0 {
			err = -err
		}
		if err < bestErr {
			bestErr = err
			bestPulse = candidate
		}
	}
	return int16(bestPulse)
}

func chooseNSQPulseShaped(desiredQ14, offsetQ14 int32, flipSign bool, pulseRatePenalty float64) int16 {
	rawTarget := desiredQ14
	if flipSign {
		rawTarget = -rawTarget
	}
	base := int(math.Round(float64(rawTarget-offsetQ14) / 16384.0))

	bestPulse := 0
	bestCost := math.Inf(1)
	candidates := make([]int, 0, 16)
	for p := base - 4; p <= base+4; p++ {
		candidates = append(candidates, p)
	}
	candidates = append(candidates, 0, -1, 1, -2, 2)
	for _, p := range candidates {
		candidate := clampInt(p, -1024, 1024)
		exc := decodedExcitationQ14(candidate, offsetQ14, flipSign)
		err := float64(int64(exc) - int64(desiredQ14))
		absPulse := math.Abs(float64(candidate))
		cost := err*err + absPulse*absPulse*pulseRatePenalty
		if candidate == 0 {
			cost *= 0.98
		}
		if cost < bestCost {
			bestCost = cost
			bestPulse = candidate
		}
	}
	return int16(bestPulse)
}

func decodedExcitationQ14(pulse int, offsetQ14 int32, flipSign bool) int32 {
	exc := int32(pulse) << 14
	if exc > 0 {
		exc -= 80 << 4
	} else if exc < 0 {
		exc += 80 << 4
	}
	exc += offsetQ14
	if flipSign {
		exc = -exc
	}
	return exc
}

func makePulseBlocks(pulses []int16, frameSize int) []pulseBlock {
	iter := frameSize >> log2ShellCodecFrameLen
	if iter*shellCodecFrameLength < frameSize {
		iter++
	}

	blocks := make([]pulseBlock, iter)
	for blockIdx := range blocks {
		blockStart := blockIdx * shellCodecFrameLength
		for i := 0; i < shellCodecFrameLength; i++ {
			pos := blockStart + i
			if pos >= len(pulses) {
				continue
			}
			p := pulses[pos]
			blocks[blockIdx].pulses[i] = p
			if p < 0 {
				blocks[blockIdx].shellAbs[i] = int(-p)
			} else {
				blocks[blockIdx].shellAbs[i] = int(p)
			}
		}

		for {
			sum := 0
			for _, p := range blocks[blockIdx].shellAbs {
				sum += p
			}
			if sum <= silkMaxPulses {
				blocks[blockIdx].sum = sum
				break
			}
			blocks[blockIdx].nLShifts++
			for i := range blocks[blockIdx].shellAbs {
				blocks[blockIdx].shellAbs[i] >>= 1
			}
		}
	}
	return blocks
}

func selectPulseRateLevel(row int, blocks []pulseBlock) int {
	bestLevel := 0
	bestCost := math.Inf(1)
	for rateLevelIdx := 0; rateLevelIdx < nRateLevels-1; rateLevelIdx++ {
		cost := icdfCost(silkRateLevelsICDF[row][:], rateLevelIdx)
		for _, block := range blocks {
			if block.nLShifts == 0 {
				cost += icdfCost(silkPulsesPerBlockICDF[rateLevelIdx][:], block.sum)
				continue
			}
			cost += icdfCost(silkPulsesPerBlockICDF[rateLevelIdx][:], silkMaxPulses+1)
			for shift := 1; shift < block.nLShifts; shift++ {
				cost += icdfCost(silkPulsesPerBlockICDF[nRateLevels-1][:], silkMaxPulses+1)
			}
			offset := 0
			if block.nLShifts == 10 {
				offset = 1
			}
			cost += icdfCost(silkPulsesPerBlockICDF[nRateLevels-1][offset:], block.sum)
		}
		if cost < bestCost {
			bestCost = cost
			bestLevel = rateLevelIdx
		}
	}
	return bestLevel
}

func icdfCost(icdf []uint8, symbol int) float64 {
	if symbol < 0 || symbol >= len(icdf) {
		return math.Inf(1)
	}
	var freq int
	if symbol == 0 {
		freq = 256 - int(icdf[0])
	} else {
		freq = int(icdf[symbol-1]) - int(icdf[symbol])
	}
	if freq <= 0 {
		return math.Inf(1)
	}
	return -math.Log2(float64(freq) / 256.0)
}

func encodePulseBlockSum(enc *entcode.Encoder, rateLevelIdx int, block pulseBlock) {
	if block.nLShifts == 0 {
		enc.EncodeIcdf(block.sum, silkPulsesPerBlockICDF[rateLevelIdx][:], 8)
		return
	}

	enc.EncodeIcdf(silkMaxPulses+1, silkPulsesPerBlockICDF[rateLevelIdx][:], 8)
	for shift := 1; shift < block.nLShifts; shift++ {
		enc.EncodeIcdf(silkMaxPulses+1, silkPulsesPerBlockICDF[nRateLevels-1][:], 8)
	}
	offset := 0
	if block.nLShifts == 10 {
		offset = 1
	}
	enc.EncodeIcdf(block.sum, silkPulsesPerBlockICDF[nRateLevels-1][offset:], 8)
}

func encodeShellBlock(enc *entcode.Encoder, absPulses [shellCodecFrameLength]int) {
	splitEncode := func(first, total int, table []uint8) {
		if total <= 0 {
			return
		}
		off := int(silkShellCodeTableOffsets[total])
		enc.EncodeIcdf(first, table[off:off+total+1], 8)
	}

	var p1 [8]int
	var p2 [4]int
	var p3 [2]int
	for i := 0; i < 8; i++ {
		p1[i] = absPulses[2*i] + absPulses[2*i+1]
	}
	for i := 0; i < 4; i++ {
		p2[i] = p1[2*i] + p1[2*i+1]
	}
	for i := 0; i < 2; i++ {
		p3[i] = p2[2*i] + p2[2*i+1]
	}

	splitEncode(p3[0], p3[0]+p3[1], silkShellCodeTable3[:])
	splitEncode(p2[0], p3[0], silkShellCodeTable2[:])
	splitEncode(p1[0], p2[0], silkShellCodeTable1[:])
	splitEncode(absPulses[0], p1[0], silkShellCodeTable0[:])
	splitEncode(absPulses[2], p1[1], silkShellCodeTable0[:])
	splitEncode(p1[2], p2[1], silkShellCodeTable1[:])
	splitEncode(absPulses[4], p1[2], silkShellCodeTable0[:])
	splitEncode(absPulses[6], p1[3], silkShellCodeTable0[:])
	splitEncode(p2[2], p3[1], silkShellCodeTable2[:])
	splitEncode(p1[4], p2[2], silkShellCodeTable1[:])
	splitEncode(absPulses[8], p1[4], silkShellCodeTable0[:])
	splitEncode(absPulses[10], p1[5], silkShellCodeTable0[:])
	splitEncode(p1[6], p2[3], silkShellCodeTable1[:])
	splitEncode(absPulses[12], p1[6], silkShellCodeTable0[:])
	splitEncode(absPulses[14], p1[7], silkShellCodeTable0[:])
}

func encodePulseLSBs(enc *entcode.Encoder, block pulseBlock) {
	if block.nLShifts == 0 {
		return
	}
	for _, p := range block.pulses {
		absQ := int(p)
		if absQ < 0 {
			absQ = -absQ
		}
		for bit := block.nLShifts - 1; bit >= 0; bit-- {
			enc.EncodeIcdf((absQ>>bit)&1, silkLSBICDFDec[:], 8)
		}
	}
}

func encodePulseSigns(enc *entcode.Encoder, blocks []pulseBlock, signalType, quantOffset int) {
	signRow := signalType*2 + quantOffset
	if signRow < 0 {
		signRow = 0
	}
	if signRow > 5 {
		signRow = 5
	}

	for _, block := range blocks {
		p := block.sum & 0x1F
		if p == 0 {
			continue
		}
		col := p
		if col > 6 {
			col = 6
		}
		icdf2 := [2]uint8{silkSignICDF[signRow][col], 0}
		for _, pulse := range block.pulses {
			if pulse == 0 {
				continue
			}
			symbol := 1
			if pulse < 0 {
				symbol = 0
			}
			enc.EncodeIcdf(symbol, icdf2[:], 8)
		}
	}
}

// Reset resets the encoder state
func (e *Encoder) Reset() {
	e.vad.Reset()
	e.silkVAD.reset()
	e.speechActivity = 1.0
	e.inputTilt = 0
	e.inputQuality = 1.0
	for i := range e.inputQualityB {
		e.inputQualityB[i] = 1.0
	}
	e.prevEnergy = 1.0
	for i := range e.prevLPC {
		e.prevLPC[i] = 0
	}
	for i := range e.prevNLSF {
		e.prevNLSF[i] = math.Pi * float64(i+1) / float64(e.lpcOrder+1)
	}
	if len(e.prevNLSFQ15) != e.lpcOrder {
		e.prevNLSFQ15 = make([]int16, e.lpcOrder)
	}
	for i := range e.prevNLSFQ15 {
		e.prevNLSFQ15[i] = int16((float64(i+1) / float64(e.lpcOrder+1)) * 32768.0)
	}
	e.prevPitchLag = 100
	e.prevLagIndex = 0
	e.prevGains = []float64{1.0, 1.0, 1.0, 1.0}
	e.prevGainIdx = 10
	e.prevGainQ16 = 65536
	e.prevSignalType = SignalTypeUnvoiced
	for i := range e.lpcState {
		e.lpcState[i] = 0
	}
	for i := range e.ltpState {
		e.ltpState[i] = 0
	}
	for i := range e.pitchHist {
		e.pitchHist[i] = 0
	}
	e.prevLagForPitch = 0
	e.ltpCorrState = 0
	e.firstFrameAfterReset = true
	e.curPitchLagIndex = 0
	e.curPitchContourIndex = 0
	e.ltpSumLogGainQ7 = 0
	e.nsq = newSilkNSQState(e.frameSize, silkLTPMemLengthMs*(e.sampleRate/1000))
	e.nsqSeed = 0
	e.shapeHarmSmooth = 0
	e.shapeTiltSmooth = 0
	e.lastSNRVBRFrame = false
	e.lastSNRVBRStream = false
	e.lastFinalRange = 0
	e.pendingLBRR = nil
	e.curLBRR = nil
	e.pendingLBRRFrames = 0
	e.pendingLBRRStereoPred = nil
	e.lbrrInPrevPacket = false
	e.lbrrRunPrevGainIdx = 0
	e.lbrrBitsPerFrame = 0
	e.capLagHigh = 0
	e.capLagLow = 0
	e.capContour = 0
	e.capLTPPerIdx = 0
	e.capLTPGainIdx = nil
	e.stereoState.reset()
	e.prevOnlyMiddle = false
	if e.side != nil {
		e.side.Reset()
	}
}

// computeEnergy computes signal energy
func computeEnergy(signal []float64) float64 {
	if len(signal) == 0 {
		return 0
	}
	energy := 0.0
	for _, s := range signal {
		energy += s * s
	}
	return energy / float64(len(signal))
}

// QuantizeSubframeGains quantizes subframe gains
func QuantizeSubframeGains(gains []float64) ([]float64, []int) {
	quantized := make([]float64, len(gains))
	indices := make([]int, len(gains))

	for i, g := range gains {
		gainDB := LinearToDB(g)

		step := 3.0
		index := int(math.Round(gainDB / step))

		if index < -20 {
			index = -20
		}
		if index > 13 {
			index = 13
		}

		indices[i] = index
		quantized[i] = DBToLinear(float64(index) * step)
	}

	return quantized, indices
}
