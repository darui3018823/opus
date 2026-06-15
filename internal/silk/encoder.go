package silk

import (
	"fmt"
	"math"

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
	side           *Encoder  // side-channel encoder for stereo packets
}

type nlsfAnalysis struct {
	cb1Idx       int
	rawIdx       []int
	nlsfQ15      []int16
	lpcQ12       []int16
	interpFactor int
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
	}
	if channels == 2 {
		side, err := NewEncoderWithFrameMs(sampleRate, 1, frameMs)
		if err != nil {
			return nil, err
		}
		side.complexity = enc.complexity
		side.bitrate = enc.bitrate
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
	signalType := SignalTypeInactive
	pitchLag := e.prevPitchLag
	pitchGain := 0.0
	if vadActive {
		signalType = SignalTypeUnvoiced
		pitchLag, pitchGain = e.analyzePitch(signal)
		if pitchGain >= 0.55 {
			signalType = SignalTypeVoiced
		}
	}
	quantOffset := 0

	e.encodeTypeOffset(enc, vadActive, signalType, quantOffset)

	gainIdx := e.analysisGainIndex(signal)
	gainIndices := e.encodeGains(enc, signalType, gainIdx, conditionalGain)

	cb := getNLSFCB(e.lpcOrder)
	nlsf := e.analyzeNLSF(signal, cb, signalType)
	e.encodeNLSF(enc, cb, signalType, nlsf)

	if e.nSubframes == 4 {
		enc.EncodeIcdf(nlsf.interpFactor, silkNLSFInterpFactorICDF[:], 8)
	}

	ltpCoeffsQ14 := make([][5]int16, e.nSubframes)
	ltpScaleQ14 := silkLTPScalesTable[0]
	if signalType == SignalTypeVoiced {
		ltpCoeffsQ14, ltpScaleQ14 = e.encodePitchAndLTP(enc, pitchLag, pitchGain, conditionalGain)
	}

	seed := int32(0)
	enc.EncodeIcdf(int(seed), silkUniform4ICDF[:], 8)
	pitchLags := make([]int, e.nSubframes)
	for sf := range pitchLags {
		pitchLags[sf] = pitchLag
	}
	pulses := e.closedLoopNSQ(signal, nlsf.lpcQ12, gainIndices,
		signalType, quantOffset, seed, pitchLags, ltpCoeffsQ14, ltpScaleQ14)
	e.encodePulses(enc, pulses, signalType, quantOffset)

	e.prevNLSF = nlsfQ15ToRadians(nlsf.nlsfQ15)
	if len(gainIndices) > 0 {
		e.prevGainIdx = gainIndices[len(gainIndices)-1]
	}
	e.prevEnergy = computeEnergy(signal)
	e.prevSignalType = signalType
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
		corr, lagEnergy := 0.0, 0.0
		for i := lag; i < len(signal); i++ {
			corr += signal[i] * signal[i-lag]
			lagEnergy += signal[i-lag] * signal[i-lag]
		}
		if lagEnergy <= 1e-12 {
			continue
		}
		norm := corr / math.Sqrt(energy*lagEnergy)
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

func (e *Encoder) encodePitchAndLTP(enc *entcode.Encoder, pitchLag int, pitchGain float64, conditionalGain bool) ([][5]int16, int16) {
	fsKHz := e.sampleRate / 1000
	minLag := PitchEstMinLagMs * fsKHz
	maxLag := PitchEstMaxLagMs * fsKHz
	if pitchLag < minLag {
		pitchLag = minLag
	}
	if pitchLag > maxLag {
		pitchLag = maxLag
	}

	lagOffset := pitchLag - minLag
	step := fsKHz >> 1
	if step < 1 {
		step = 1
	}
	lagIndex := lagOffset / step
	lagLowBits := lagOffset % step
	if lagIndex < 0 {
		lagIndex = 0
	}
	if lagIndex >= len(silkPitchLagICDF) {
		lagIndex = len(silkPitchLagICDF) - 1
		lagLowBits = step - 1
	}

	if conditionalGain && e.prevSignalType == SignalTypeVoiced {
		enc.EncodeIcdf(0, silkPitchDeltaICDF[:], 8) // Force absolute lag coding.
	}
	enc.EncodeIcdf(lagIndex, silkPitchLagICDF[:], 8)
	encodePitchLagLowBits(enc, fsKHz, lagLowBits)
	encodeFlatPitchContour(enc, fsKHz, e.nSubframes)

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

	e.prevPitchLag = pitchLag
	e.prevLagIndex = lagIndex
	ltpCoeffsQ14 := make([][5]int16, e.nSubframes)
	for sf := 0; sf < e.nSubframes; sf++ {
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
	return ltpCoeffsQ14, silkLTPScalesTable[0]
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

func (e *Encoder) encodeGains(enc *entcode.Encoder, signalType, targetIdx int, conditional bool) []int {
	absIndices := make([]int, e.nSubframes)
	prevIdx := e.prevGainIdx
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
		targetIdx = e.encodeGainDelta(enc, targetIdx, targetIdx)
		absIndices[sf] = targetIdx
	}
	return absIndices
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
		cb1Idx = bestNLSFStage1(signal, cb)
		rawIdx = refineNLSFResidual(signal, cb, cb1Idx)
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
	rawIdx := make([]int, cb.order)
	bestCost := lpcResidualEnergy(signal, nlsfToLPCLibopus(reconstructNLSFQ15(cb, cb1Idx, rawIdx), cb.order))

	for pass := 0; pass < 2; pass++ {
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
	pulses := make([]int16, e.frameSize)
	if signalType == SignalTypeInactive || len(signal) == 0 {
		e.updateSilentSynthesisState()
		return pulses
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
	shapeErr := 0.0
	shape := 0.30
	pulseRatePenalty := 96.0 * float64(int64(1)<<20)
	if signalType == SignalTypeVoiced {
		shape = 0.50
		pulseRatePenalty = 28.0 * float64(int64(1)<<20)
	}

	for sf := 0; sf < e.nSubframes; sf++ {
		start := sf * subframeLen
		gainIdx := gainIndices[clampInt(sf, 0, len(gainIndices)-1)]
		gainQ16 := silkGainDequantQ16(gainIdx)
		gainQ10 := gainQ16 >> 6
		if gainQ10 <= 0 {
			gainQ10 = 1
		}
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

			target := signal[start+i] + shape*shapeErr
			if target > 1.5 {
				target = 1.5
			} else if target < -1.5 {
				target = -1.5
			}
			desiredQ14 := int32(math.Round(target * (float64(int64(1)<<39) / float64(gainQ10))))
			desiredExcQ14 := desiredQ14 - predQ14 - ltpPredQ14

			seed = 196314165*seed + 907633515
			pulse := chooseNSQPulseShaped(desiredExcQ14, offsetQ14, seed < 0, pulseRatePenalty)
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
			shapeErr = signal[start+i] - recon
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
	for p := base - 4; p <= base+4; p++ {
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
