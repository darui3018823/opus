package silk

import (
	"fmt"
	"math"
	"os"

	"github.com/darui3018823/opus/internal/entcode"
)

// Encoder represents a SILK encoder instance
type Encoder struct {
	sampleRate     int       // Sample rate (8000, 12000, 16000, 24000)
	frameSize      int       // Frame size in samples
	frameMs        int       // Frame duration in milliseconds (10 or 20)
	nSubframes     int       // Number of SILK subframes in one frame
	channels       int       // Number of channels (1 or 2)
	lpcOrder       int       // LPC order based on bandwidth
	complexity     int       // Complexity (0-10)
	bitrate        int       // Target bitrate in bps
	vad            *VAD      // Voice activity detector
	prevEnergy     float64   // Previous frame energy for smoothing
	prevLPC        []float64 // Previous LPC coefficients
	prevNLSF       []float64 // Previous NLSF
	prevPitchLag   int       // Previous pitch lag
	prevLagIndex   int       // Previous entropy-coded pitch lag index
	prevSignalType int       // Previous SILK signal type
	prevGains      []float64 // Previous subframe gains
	prevGainIdx    int       // Previous absolute gain index, matching decoder state
	prevGainQ16    int32     // Previous synthesis gain, matching decoder state
	lpcState       []int32   // Encoder-side LPC synthesis state, Q14
	ltpState       []int32   // Encoder-side LTP output history, Q0
	nsq            silkNSQState
	nsqSeed        int32 // winning del-dec seed (silk_NSQ_del_dec writes this back to the bitstream)
	// useTrellisNSQ enables the FLP noise-shape analysis + delayed-decision
	// trellis NSQ (Q3+Q4) for voiced frames, where the gains co-designed by
	// silk_process_gains_FLP (Step 4) make it a clear win over the legacy
	// single-state homebrew quantizer. On by default; unvoiced/inactive frames
	// always use the homebrew path (the trellis shaping hurts broadband noise).
	useTrellisNSQ bool
	// stereoComponent marks the mid/side encoders of a stereo packet. The Step 4
	// voiced trellis + process_gains path is gated to mono for now: it produces a
	// stereo multi-frame bitstream that our decoder and libopus reconstruct
	// differently (a conformance gap to chase down with the stereo predictor in
	// Step 5). Stereo components keep the proven heuristic-gain + homebrew path.
	stereoComponent bool
	// hybridMode marks frames encoded as the SILK low band of a hybrid packet.
	// Like stereoComponent it gates off the Step 4 voiced trellis: the hybrid
	// SILK+CELT energy balance is a separate WIP and the trellis-coded low band
	// is not yet conformant there. Set per-call by the hybrid encoder.
	hybridMode      bool
	shapeHarmSmooth float64
	shapeTiltSmooth float64
	side            *Encoder // side-channel encoder for stereo packets

	// Pitch analysis state (silk_find_pitch_lags_FLP).
	pitchHist            []float64 // Past ltp_mem_length input samples, [-1,1]
	prevLagForPitch      int       // Previous frame pitch lag (0 if unvoiced)
	ltpCorrState         float64   // Normalized LTP correlation from prev frame
	firstFrameAfterReset bool      // Skip pitch search on the first frame
	curPitchLagIndex     int       // Lag index selected for the current frame
	curPitchContourIndex int       // Pitch contour index for the current frame
}

type nlsfAnalysis struct {
	cb1Idx       int
	rawIdx       []int
	nlsfQ15      []int16
	lpcQ12       []int16
	interpFactor int
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
}

type rateControlPlan struct {
	gainTargets []int
	gainIndices []int
	rateScale   float64
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
	for i := range prevNLSF {
		prevNLSF[i] = math.Pi * float64(i+1) / float64(lpcOrder+1)
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
		useTrellisNSQ: os.Getenv("OPUS_SILK_TRELLIS") != "0",
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
// as a conservative mid/side pair with zero stereo predictors.
func (e *Encoder) EncodeMulti(pcm []float64, nFrames int) ([]byte, error) {
	enc := entcode.NewEncoder(64)
	if err := e.EncodeMultiWithEncoder(enc, pcm, nFrames); err != nil {
		return nil, err
	}
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
	enc.EncodeBitLogp(false, 1) // No LBRR data in this encoder slice.

	for i, signal := range frames {
		e.encodeRangeFrame(enc, signal, vadFlags[i], i > 0)
	}
	return nil
}

func (e *Encoder) encodeMultiStereo(pcm []float64, nFrames int) ([]byte, error) {
	enc := entcode.NewEncoder(64)
	if err := e.encodeMultiStereoWithEncoder(enc, pcm, nFrames); err != nil {
		return nil, err
	}
	enc.Flush()
	return enc.Bytes(), nil
}

func (e *Encoder) encodeMultiStereoWithEncoder(enc *entcode.Encoder, pcm []float64, nFrames int) error {
	if e.side == nil {
		return fmt.Errorf("missing SILK side-channel encoder")
	}

	midFrames := make([][]float64, nFrames)
	sideFrames := make([][]float64, nFrames)
	vadFlags := [2][]bool{
		make([]bool, nFrames),
		make([]bool, nFrames),
	}
	for frame := 0; frame < nFrames; frame++ {
		mid := make([]float64, e.frameSize)
		side := make([]float64, e.frameSize)
		base := frame * e.frameSize * 2
		for i := 0; i < e.frameSize; i++ {
			l := pcm[base+i*2]
			r := pcm[base+i*2+1]
			mid[i] = 0.5 * (l + r)
			side[i] = 0.5 * (l - r)
		}
		midFrames[frame] = mid
		sideFrames[frame] = side
		vadFlags[0][frame] = e.vad.Detect(mid)
		vadFlags[1][frame] = e.side.vad.Detect(side)
	}

	for ch := 0; ch < 2; ch++ {
		for _, active := range vadFlags[ch] {
			enc.EncodeBitLogp(active, 1)
		}
		enc.EncodeBitLogp(false, 1) // No LBRR data in this encoder slice.
	}

	prevOnlyMiddle := false
	for i := 0; i < nFrames; i++ {
		encodeZeroStereoPred(enc)
		onlyMiddle := false
		if !vadFlags[1][i] {
			onlyMiddle = true
			enc.EncodeIcdf(1, silkStereoOnlyCodeMidICDF[:], 8)
		}
		e.encodeRangeFrame(enc, midFrames[i], vadFlags[0][i], i > 0)
		if !onlyMiddle {
			e.side.encodeRangeFrame(enc, sideFrames[i], vadFlags[1][i], i > 0 && !prevOnlyMiddle)
		}
		prevOnlyMiddle = onlyMiddle
	}
	return nil
}

func encodeZeroStereoPred(enc *entcode.Encoder) {
	// joint=12, ix0=1, ix1=2 decodes both predictors to zero.
	enc.EncodeIcdf(12, silkStereoPredJointICDF[:], 8)
	for i := 0; i < 2; i++ {
		enc.EncodeIcdf(1, silkUniform3ICDF[:], 8)
		enc.EncodeIcdf(2, silkUniform5ICDF[:], 8)
	}
}

// encodeRangeFrame writes one structurally valid SILK frame. Slice 3 adds the
// first voiced path: simple pitch/LTP decisions are encoded before the seed,
// and pulse coding is driven from a short-term residual instead of raw samples.
func (e *Encoder) encodeRangeFrame(enc *entcode.Encoder, signal []float64, vadActive, conditionalGain bool) {
	initialState := e.snapshotFrameState()

	signalType := SignalTypeInactive
	pitchLag := e.prevPitchLag
	pitchGain := 0.0
	e.curPitchLagIndex = 0
	e.curPitchContourIndex = 0
	if vadActive {
		signalType = SignalTypeUnvoiced
		speechActivity := 1.0 // proxy until silk_VAD_GetSA_Q8 is ported
		voiced, lagIndex, contourIndex, ltpCorr := e.silkFindPitchLags(signal, speechActivity)
		if voiced {
			signalType = SignalTypeVoiced
			e.curPitchLagIndex = lagIndex
			e.curPitchContourIndex = contourIndex
			pitchGain = ltpCorr
		}
	}
	quantOffset := 0

	cb := getNLSFCB(e.lpcOrder)
	nlsf := e.analyzeNLSF(signal, cb, signalType)
	quantOffset = e.estimateQuantOffsetType(signal, nlsf.lpcQ12, signalType, pitchLag, pitchGain)

	plan := e.selectRateControlPlan(initialState, signal, vadActive, signalType, quantOffset, conditionalGain, nlsf, pitchLag, pitchGain)

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
		ltpCoeffsQ14, ltpScaleQ14, pitchLags = e.encodePitchAndLTP(enc, pitchGain, conditionalGain)
	}

	if len(plan.gainIndices) == len(gainIndices) {
		gainIndices = plan.gainIndices
	}
	// Run the delayed-decision NSQ before encoding the seed: the trellis selects
	// the winning state and its initial seed (e.nsqSeed), which libopus writes to
	// the bitstream so the decoder reproduces the same sign sequence.
	e.nsqSeed = 0
	pulses := e.closedLoopNSQWithRateScale(signal, nlsf.lpcQ12, gainIndices,
		signalType, quantOffset, 0, pitchLags, ltpCoeffsQ14, ltpScaleQ14, plan.rateScale)
	enc.EncodeIcdf(int(e.nsqSeed), silkUniform4ICDF[:], 8)
	e.encodePulses(enc, pulses, signalType, quantOffset)

	e.prevNLSF = nlsfQ15ToRadians(nlsf.nlsfQ15)
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
	e.nsq = st.nsq.clone()
	e.shapeHarmSmooth = st.shapeHarmSmooth
	e.shapeTiltSmooth = st.shapeTiltSmooth
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
) rateControlPlan {
	baseTargets := e.analysisGainIndices(signal)
	baseIndices := e.resolveGainIndices(baseTargets, conditionalGain)
	if signalType == SignalTypeInactive {
		return rateControlPlan{gainTargets: baseTargets, gainIndices: baseIndices, rateScale: 1}
	}
	// Voiced frames only get the Step 4 noise-shape + process_gains + rate-control
	// treatment on the (mono) trellis path. Stereo components keep the proven
	// no-rate-control heuristic-gain path so their bitstream stays conformant.
	if signalType == SignalTypeVoiced && !e.voicedUsesTrellis() {
		return rateControlPlan{gainTargets: baseTargets, gainIndices: baseIndices, rateScale: 1}
	}

	// Seed the rate-control search from excitation-normalized gains rather than
	// the mis-scaled dB heuristic. Unvoiced frames have no LTP prediction so the
	// gain normalizes the signal directly (Q5d); voiced frames take the
	// noise-shape + process_gains pipeline so the gain matches the spectral
	// envelope and bounds the residual, instead of the heuristic that flooded the
	// shell coder with pulses (Step 4).
	if signalType == SignalTypeVoiced {
		pitchLags := e.reconstructCurrentPitchLags()
		ltpPerIdx, ltpGainIdx := selectLTPGain(pitchGain)
		ltpCoeffsQ14 := ltpCoeffsForIndices(ltpPerIdx, ltpGainIdx, e.nSubframes)
		baseTargets = e.shapeGainIndices(signal, nlsf.lpcQ12, signalType, quantOffset, pitchLags, ltpCoeffsQ14, pitchGain)
	} else {
		baseTargets = e.excitationGainIndices(signal)
	}
	baseIndices = e.resolveGainIndices(baseTargets, conditionalGain)

	targetBits := e.silkFrameTargetBits()
	if targetBits <= 0 {
		return rateControlPlan{gainTargets: baseTargets, gainIndices: baseIndices, rateScale: 1}
	}

	gainBoosts := []int{0, 2, 4, 6, 8, 10, 12}
	rateScales := []float64{1, 2, 4, 8, 16, 32, 64, 128, 512}
	if e.complexity < 4 {
		gainBoosts = []int{0, 4, 8, 12}
		rateScales = []float64{1, 4, 16, 64, 512}
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
			vadActive, signalType, quantOffset, conditionalGain, targets, nlsf, pitchLag, pitchGain)
		for _, scale := range rateScales {
			e.restoreFrameState(initial)
			pulses := e.closedLoopNSQWithRateScale(signal, nlsf.lpcQ12, gainIndices,
				signalType, quantOffset, 0, pitchLags, ltpCoeffsQ14, ltpScaleQ14, scale)
			if !pulsesMeetActivityFloor(pulses, e.frameSize) {
				continue
			}
			if e.currentFrameOutputRMS() < minOutputRMS {
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
	if e.channels == 2 {
		bits /= 2
	}
	if bits < 16 {
		bits = 16
	}
	return bits
}

func boostedGainTargets(base []int, boost int) []int {
	out := make([]int, len(base))
	for i, v := range base {
		out[i] = clampInt(v+boost, 0, NLevelsQGain-1)
	}
	return out
}

func (e *Encoder) estimateFrameHeaderBits(
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
		ltpCoeffsQ14, ltpScaleQ14, pitchLags = e.encodePitchAndLTP(enc, pitchGain, conditionalGain)
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
func (e *Encoder) encodePitchAndLTP(enc *entcode.Encoder, pitchGain float64, conditionalGain bool) ([][5]int16, int16, []int) {
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

	ltpPerIdx, ltpGainIdx := selectLTPGain(pitchGain)
	enc.EncodeIcdf(ltpPerIdx, silkLTPPerIndexICDF[:], 8)
	for sf := 0; sf < e.nSubframes; sf++ {
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
	ltpCoeffsQ14 := ltpCoeffsForIndices(ltpPerIdx, ltpGainIdx, e.nSubframes)
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

// excitationGainIndices derives per-subframe gains so that the quantized
// excitation stays normalized (RMS ≈ 2 pulse-units). The legacy dB heuristic
// used by analysisGainIndices is ~1000× too low for unpredicted signals, which
// drives the NSQ pulses into the ±1024 clamp and blows the bit budget; rate
// control then crushes the pulses to silence, leaving only the PRNG quantization
// offset (decorrelated noise). This path is used for the unvoiced branch where
// there is no LTP prediction to carry the waveform.
func (e *Encoder) excitationGainIndices(signal []float64) []int {
	return e.gainIndicesFromEnergy(signal, excitationGainIndexFromEnergy)
}

// shapeGainIndices derives per-subframe target gain indices the libopus way
// (silk_noise_shape_analysis_FLP gains + silk_process_gains_FLP soft limit),
// rather than from the signal-energy dB heuristic. The shape gains come from the
// noise-shaping spectral envelope (never near-zero, so steady voiced frames
// stay stable) scaled by the target-SNR gain_mult; the soft limit then floors
// the gain using the LPC+LTP residual energy so the quantized signal is bounded.
// Used for voiced frames where the dB heuristic mis-scaled the gain and flooded
// the shell coder with pulses (Step 4). The shape-smoothing state is saved and
// restored so this acts as a pure analysis pass.
func (e *Encoder) shapeGainIndices(signal []float64, lpcQ12 []int16, signalType, quantOffset int, pitchLags []int, ltpCoeffsQ14 [][5]int16, pitchGain float64) []int {
	harmSmooth, tiltSmooth := e.shapeHarmSmooth, e.shapeTiltSmooth
	shape := e.analyzeNoiseShapeFLP(signal, lpcQ12, signalType, quantOffset, pitchLags, pitchGain)
	e.shapeHarmSmooth, e.shapeTiltSmooth = harmSmooth, tiltSmooth

	resNrg := e.ltpResidualEnergyPerSubframe(signal, lpcQ12, signalType, pitchLags, ltpCoeffsQ14)
	subLen := e.frameSize / e.nSubframes
	invMaxSqr := 0.0
	if subLen > 0 {
		invMaxSqr = math.Pow(2.0, 0.33*(21.0-shape.SNRdB)) / float64(subLen)
	}

	// Gain reduction when the LTP coding gain is high (silk_process_gains_FLP):
	// a strong long-term predictor leaves a small residual, so the synthesis
	// gain can be scaled down, sparing pulses on steady voiced frames. The soft
	// limit below still floors the gain by the residual energy, so the reduction
	// only takes hold where the prediction is genuinely good (ratio_bytes ~2x→~1x).
	gainScale := 1.0
	if signalType == SignalTypeVoiced {
		ltpCodGainDB := e.ltpPredCodGainDB(signal, lpcQ12, resNrg, pitchLags, ltpCoeffsQ14)
		gainScale = 1.0 - 0.5*silkSigmoid(0.25*(ltpCodGainDB-12.0))
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
		targets[sf] = silkQuantizeGainIndex(gain * 65536.0)
	}
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

func (e *Encoder) analyzeNLSF(signal []float64, cb *nlsfCBParams, signalType int) nlsfAnalysis {
	rawIdx := make([]int, cb.order)
	cb1Idx := e.defaultNLSFIndex(signalType, cb)
	if signalType != SignalTypeInactive {
		targetQ15, ok := e.lpcNLSFTargetQ15(signal, cb)
		cb1Idx, rawIdx = bestNLSFAnalysis(signal, cb, targetQ15, ok)
	}

	nlsfQ15 := reconstructNLSFQ15(cb, cb1Idx, rawIdx)
	lpcQ12 := nlsfToLPCLibopus(nlsfQ15, cb.order)
	return nlsfAnalysis{
		cb1Idx:       cb1Idx,
		rawIdx:       rawIdx,
		nlsfQ15:      nlsfQ15,
		lpcQ12:       lpcQ12,
		interpFactor: 4,
	}
}

func (e *Encoder) lpcNLSFTargetQ15(signal []float64, cb *nlsfCBParams) ([]int16, bool) {
	if len(signal) <= cb.order {
		return nil, false
	}
	lpc := NewLPCAnalysis(cb.order)
	if lpc == nil {
		return nil, false
	}
	if err := lpc.AnalyzeWindowed(signal); err != nil {
		return nil, false
	}
	target := lpc.NLSFTargetQ15()
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
	return refineNLSFResidualFrom(signal, cb, cb1Idx, make([]int, cb.order))
}

func refineNLSFResidualFrom(signal []float64, cb *nlsfCBParams, cb1Idx int, seed []int) []int {
	rawIdx := make([]int, cb.order)
	copy(rawIdx, seed)
	for i := range rawIdx {
		rawIdx[i] = clampInt(rawIdx[i], -3, 3)
	}
	bestCost := lpcResidualEnergy(signal, nlsfToLPCLibopus(reconstructNLSFQ15(cb, cb1Idx, rawIdx), cb.order))

	for pass := 0; pass < 3; pass++ {
		improved := false
		for i := 0; i < cb.order; i++ {
			bestVal := rawIdx[i]
			for _, candidate := range []int{-3, -2, -1, 0, 1, 2, 3} {
				if candidate == rawIdx[i] {
					continue
				}
				trial := append([]int(nil), rawIdx...)
				trial[i] = candidate
				nlsfQ15 := reconstructNLSFQ15(cb, cb1Idx, trial)
				cost := lpcResidualEnergy(signal, nlsfToLPCLibopus(nlsfQ15, cb.order))
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
	quantStepSizeQ16 := int32(11796)
	if cb.order == 16 {
		quantStepSizeQ16 = 9830
	}

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
			out = predQ10 + int32((int64(out)*int64(quantStepSizeQ16))>>16)
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
	if cb1Idx < 0 {
		cb1Idx = 0
	}
	if cb1Idx >= cb.nEntries {
		cb1Idx = cb.nEntries - 1
	}

	const nlsfQuantLevelAdjQ10 = int32(102)
	quantStepSizeQ16 := int32(11796)
	if cb.order == 16 {
		quantStepSizeQ16 = 9830
	}

	predQ8 := make([]uint8, cb.order)
	ecSelBase := cb1Idx * (cb.order / 2)
	for i := 0; i < cb.order; i += 2 {
		entry := cb.cb2Select[ecSelBase+i/2]
		predQ8[i] = cb.predQ8[i+int((entry&1))*int(cb.order-1)]
		predQ8[i+1] = cb.predQ8[i+int((entry>>4)&1)*int(cb.order-1)+1]
	}

	resQ10 := make([]int32, cb.order)
	outQ10 := int32(0)
	for i := cb.order - 1; i >= 0; i-- {
		idx := 0
		if i < len(rawIdx) {
			idx = clampInt(rawIdx[i], -4, 4)
		}
		predQ10 := (outQ10 * int32(predQ8[i])) >> 8
		outQ10 = int32(idx) << 10
		if outQ10 > 0 {
			outQ10 -= nlsfQuantLevelAdjQ10
		} else if outQ10 < 0 {
			outQ10 += nlsfQuantLevelAdjQ10
		}
		outQ10 = predQ10 + int32((int64(outQ10)*int64(quantStepSizeQ16))>>16)
		resQ10[i] = outQ10
	}

	nlsfQ15 := make([]int16, cb.order)
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
		enc.EncodeIcdf(nlsfSymbol(analysis.rawIdx, i), cb.cb2ICDF[ecIx0:ecIx0+9], 8)
		enc.EncodeIcdf(nlsfSymbol(analysis.rawIdx, i+1), cb.cb2ICDF[ecIx1:ecIx1+9], 8)
	}
}

func nlsfSymbol(rawIdx []int, i int) int {
	if i >= len(rawIdx) {
		return 4
	}
	return clampInt(rawIdx[i], -3, 3) + 4
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
	return e.closedLoopNSQWithRateScale(signal, lpcQ12, gainIndices,
		signalType, quantOffset, seed, pitchLags, ltpCoeffsQ14, ltpScaleQ14, 1)
}

// voicedUsesTrellis reports whether voiced frames take the Step 4 trellis NSQ +
// process_gains path. Gated to the enabled flag and to non-stereo encoders: the
// stereo multi-frame trellis bitstream is not yet conformant (Step 5).
func (e *Encoder) voicedUsesTrellis() bool {
	return e.useTrellisNSQ && !e.stereoComponent && !e.hybridMode
}

// SetHybridMode marks subsequent frames as the SILK low band of a hybrid packet
// (see hybridMode). The hybrid encoder sets it before encoding and clears it
// after so the same SILK encoder instance can also serve SILK-only packets.
func (e *Encoder) SetHybridMode(on bool) {
	e.hybridMode = on
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
	gainIndices []int,
	signalType, quantOffset int,
	seed int32,
	pitchLags []int,
	ltpCoeffsQ14 [][5]int16,
	ltpScaleQ14 int16,
	rateScale float64,
) []int16 {
	// The delayed-decision trellis with the co-designed process_gains gains is a
	// clear win for voiced frames (Step 4), but its perceptual shaping lowers
	// broadband SNR on noise, so unvoiced/inactive frames stay on the homebrew
	// quantizer with the excitation-normalized gains (Q5d). Dispatch by type.
	if signalType != SignalTypeVoiced || !e.voicedUsesTrellis() {
		return e.closedLoopNSQHomebrew(signal, lpcQ12, gainIndices,
			signalType, quantOffset, seed, pitchLags, ltpCoeffsQ14, ltpScaleQ14, rateScale)
	}
	if signalType == SignalTypeInactive || len(signal) == 0 {
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
	shape := e.analyzeNoiseShapeFLP(signal, lpcQ12, signalType, quantOffset, pitchLags, pitchGain)

	lambdaQ10 := shape.Lambda_Q10
	if rateScale > 1 {
		lambdaQ10 = int32(float64(shape.Lambda_Q10) * (1.0 + 0.5*math.Log2(rateScale)))
	}
	if lambdaQ10 < 64 {
		lambdaQ10 = 64
	}

	return e.silkNSQDelDec(x16, lpcQ12, ltpCoeffsQ14, shape, gainsQ16, pitchL,
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
		return pulses
	}
	if rateScale < 1 {
		rateScale = 1
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
					silkLPCAnalysisFilter(sLTP, outBufQ0[startIdx:startIdx+filterLen], lpcQ12, filterLen, e.lpcOrder)
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
				lpcPredQ10 = silkSMLAWB(lpcPredQ10, sLPCQ14[silkMaxLPCOrder+i-j-1], lpcQ12[j])
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

// encodeLegacyAnalysisFrame is the pre-slice encoder path kept as a reference
// for future analysis work. It is no longer used by Encode because its symbol
// order does not match the SILK decoder grammar.
func (e *Encoder) encodeLegacyAnalysisFrame(pcm []float64) ([]byte, error) {
	// For stereo, extract left channel
	signal := pcm
	if e.channels == 2 {
		signal = make([]float64, e.frameSize)
		for i := 0; i < e.frameSize; i++ {
			signal[i] = pcm[i*2]
		}
	}

	// Voice activity detection
	isSpeech := e.vad.Detect(signal)
	if !isSpeech {
		return e.encodeSilence()
	}

	// LPC analysis
	lpcCoeffs := AnalyzeLPC(signal, e.lpcOrder)
	if lpcCoeffs == nil {
		return nil, fmt.Errorf("LPC analysis failed")
	}

	// Convert to NLSF for quantization
	nlsf := LPCToLSF(lpcCoeffs)
	if nlsf == nil {
		return nil, fmt.Errorf("LPC to LSF conversion failed")
	}

	// Quantize NLSF
	quantizedNLSF, nlsfIndices := QuantizeNLSF(nlsf)
	if quantizedNLSF == nil {
		return nil, fmt.Errorf("NLSF quantization failed")
	}

	// Compute LPC residual
	residual := ComputeResidual(signal, lpcCoeffs)

	// Pitch analysis on residual
	pitchLag, pitchGain := DetectPitch(residual, MinPitchLag, MaxPitchLag)

	// Determine signal type
	signalType := SignalTypeUnvoiced
	if pitchGain > 0.3 {
		signalType = SignalTypeVoiced
	}

	// Apply pitch prediction
	pitchResidual := ApplyPitchPrediction(residual, pitchLag, pitchGain)

	// Compute subframe gains
	subframeGains := ComputeSubframeGains(pitchResidual, 4)

	// Quantize gains
	_, gainIndices := QuantizeSubframeGains(subframeGains)

	// Compute excitation pulse counts per subframe
	pulseCounts := computePulseCounts(pitchResidual, 4)

	// Pack frame using range encoder
	packet := e.encodeFrame(nlsfIndices, signalType, pitchLag, gainIndices, pulseCounts)

	// Update state
	e.prevLPC = lpcCoeffs
	e.prevNLSF = quantizedNLSF
	e.prevPitchLag = pitchLag
	e.prevGains = subframeGains
	e.prevEnergy = computeEnergy(signal)

	return packet, nil
}

// encodeFrame encodes a SILK frame using the range encoder.
func (e *Encoder) encodeFrame(nlsfIndices []int, signalType int, pitchLag int, gainIndices []int, pulseCounts []int) []byte {
	enc := entcode.NewEncoder(64)

	// 1. Encode VAD flag (1 = speech present)
	enc.EncodeIcdf(1, icdfVAD[:], 8)

	// 2. Encode LBRR flag (0 = no LBRR)
	enc.EncodeIcdf(0, icdfLBRR[:], 8)

	// 3. Encode signal type and quantization offset type
	sigQOffIdx := signalType*2 + 0
	if sigQOffIdx >= len(icdfSignalTypeQOffset) {
		sigQOffIdx = len(icdfSignalTypeQOffset) - 1
	}
	enc.EncodeIcdf(sigQOffIdx, icdfSignalTypeQOffset[:], 8)

	// 4. Encode NLSF indices
	idx0 := nlsfIndices[0]
	if idx0 < 0 {
		idx0 = 0
	}
	if idx0 >= 32 {
		idx0 = 31
	}
	enc.EncodeIcdf(idx0, icdfNLSFStage1[:], 8)

	idx1 := nlsfIndices[1]
	if idx1 < 0 {
		idx1 = 0
	}
	if idx1 >= 8 {
		idx1 = 7
	}
	enc.EncodeIcdf(idx1, icdfNLSFStage2[:], 8)

	// 5. Encode pitch lag (if voiced)
	if signalType == SignalTypeVoiced {
		pl := pitchLag - MinPitchLag
		if pl < 0 {
			pl = 0
		}
		pitchHigh := pl / 64
		pitchLow := pl % 64

		if pitchHigh >= 8 {
			pitchHigh = 7
			pitchLow = 63
		}

		enc.EncodeIcdf(pitchHigh, icdfPitchHighBits[:], 8)
		enc.EncodeBits(uint32(pitchLow), uint(6))

		// Encode LTP filter index
		enc.EncodeIcdf(1, icdfLTPFilter[:], 8) // default filter
	}

	// 6. Encode gains
	// First subframe: absolute gain index
	g0 := gainIndices[0]
	absGainIdx := g0 + 20
	if absGainIdx < 0 {
		absGainIdx = 0
	}
	if absGainIdx >= 32 {
		absGainIdx = 31
	}
	enc.EncodeIcdf(absGainIdx, icdfGainFirst[:], 8)

	// Subsequent subframes: delta gain
	for sf := 1; sf < 4; sf++ {
		var deltaIdx int
		if sf < len(gainIndices) {
			delta := gainIndices[sf] - gainIndices[sf-1]
			deltaIdx = delta + 20
		} else {
			deltaIdx = 20
		}
		if deltaIdx < 0 {
			deltaIdx = 0
		}
		if deltaIdx >= 41 {
			deltaIdx = 40
		}
		enc.EncodeIcdf(deltaIdx, icdfGainDelta[:], 8)
	}

	// 7. Encode excitation pulse counts
	for sf := 0; sf < 4; sf++ {
		pc := 0
		if sf < len(pulseCounts) {
			pc = pulseCounts[sf]
		}
		if pc < 0 {
			pc = 0
		}
		if pc >= 19 {
			pc = 18
		}
		enc.EncodeIcdf(pc, icdfExcPulseCount[:], 8)
	}

	enc.Flush()
	return enc.Bytes()
}

// encodeSilence creates a minimal packet for silence
func (e *Encoder) encodeSilence() ([]byte, error) {
	return []byte{0x00}, nil
}

// computePulseCounts estimates excitation pulse counts per subframe
func computePulseCounts(residual []float64, numSubframes int) []int {
	counts := make([]int, numSubframes)
	if len(residual) == 0 {
		return counts
	}
	subframeLen := len(residual) / numSubframes

	for sf := 0; sf < numSubframes; sf++ {
		start := sf * subframeLen
		end := start + subframeLen
		if end > len(residual) {
			end = len(residual)
		}

		energy := 0.0
		for i := start; i < end; i++ {
			energy += residual[i] * residual[i]
		}
		rms := math.Sqrt(energy / float64(end-start))

		count := int(rms * 10)
		if count > 18 {
			count = 18
		}
		counts[sf] = count
	}

	return counts
}

// Reset resets the encoder state
func (e *Encoder) Reset() {
	e.vad.Reset()
	e.prevEnergy = 1.0
	for i := range e.prevLPC {
		e.prevLPC[i] = 0
	}
	for i := range e.prevNLSF {
		e.prevNLSF[i] = math.Pi * float64(i+1) / float64(e.lpcOrder+1)
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
