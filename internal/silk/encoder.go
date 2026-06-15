package silk

import (
	"fmt"
	"math"

	"github.com/darui3018823/opus/internal/entcode"
)

// Encoder represents a SILK encoder instance
type Encoder struct {
	sampleRate   int       // Sample rate (8000, 12000, 16000, 24000)
	frameSize    int       // Frame size in samples
	frameMs      int       // Frame duration in milliseconds (10 or 20)
	nSubframes   int       // Number of SILK subframes in one frame
	channels     int       // Number of channels (1 or 2)
	lpcOrder     int       // LPC order based on bandwidth
	complexity   int       // Complexity (0-10)
	bitrate      int       // Target bitrate in bps
	vad          *VAD      // Voice activity detector
	prevEnergy   float64   // Previous frame energy for smoothing
	prevLPC      []float64 // Previous LPC coefficients
	prevNLSF     []float64 // Previous NLSF
	prevPitchLag int       // Previous pitch lag
	prevGains    []float64 // Previous subframe gains
	prevGainIdx  int       // Previous absolute gain index, matching decoder state
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

	return &Encoder{
		sampleRate:   sampleRate,
		frameSize:    frameSize,
		frameMs:      frameMs,
		nSubframes:   nSubframes,
		channels:     channels,
		lpcOrder:     lpcOrder,
		complexity:   5,
		bitrate:      sampleRate * channels * 16 / 8,
		vad:          NewVAD(),
		prevEnergy:   1.0,
		prevLPC:      make([]float64, lpcOrder),
		prevNLSF:     prevNLSF,
		prevPitchLag: 100,
		prevGains:    []float64{1.0, 1.0, 1.0, 1.0},
		prevGainIdx:  10,
	}, nil
}

// SetComplexity sets the computational complexity (0-10)
func (e *Encoder) SetComplexity(complexity int) error {
	if complexity < 0 || complexity > 10 {
		return fmt.Errorf("complexity must be between 0 and 10, got %d", complexity)
	}
	e.complexity = complexity
	return nil
}

// SetBitrate sets the target bitrate in bps
func (e *Encoder) SetBitrate(bitrate int) error {
	if bitrate < 6000 || bitrate > 40000 {
		return fmt.Errorf("bitrate must be between 6000 and 40000 bps, got %d", bitrate)
	}
	e.bitrate = bitrate
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
// all frames, one LBRR flag, then frame payloads in order. Stereo analysis is
// not implemented yet; for stereo input this slice encodes the left channel as
// a mono-compatible foundation for the later stereo slice.
func (e *Encoder) EncodeMulti(pcm []float64, nFrames int) ([]byte, error) {
	if nFrames < 1 {
		return nil, fmt.Errorf("invalid frame count: %d", nFrames)
	}
	expected := e.frameSize * e.channels * nFrames
	if len(pcm) != expected {
		return nil, fmt.Errorf("invalid PCM length: got %d, expected %d", len(pcm), expected)
	}

	frames := make([][]float64, nFrames)
	vadFlags := make([]bool, nFrames)
	for frame := 0; frame < nFrames; frame++ {
		start := frame * e.frameSize * e.channels
		framePCM := pcm[start : start+e.frameSize*e.channels]
		signal := framePCM
		if e.channels == 2 {
			signal = make([]float64, e.frameSize)
			for i := 0; i < e.frameSize; i++ {
				signal[i] = framePCM[i*2]
			}
		}
		frames[frame] = signal
		vadFlags[frame] = e.vad.Detect(signal)
	}

	enc := entcode.NewEncoder(64)
	for _, active := range vadFlags {
		enc.EncodeBitLogp(active, 1)
	}
	enc.EncodeBitLogp(false, 1) // No LBRR data in this encoder slice.

	for i, signal := range frames {
		e.encodeRangeFrame(enc, signal, vadFlags[i], i > 0)
	}
	enc.Flush()
	return enc.Bytes(), nil
}

// encodeRangeFrame writes one structurally valid SILK frame. It deliberately
// uses fixed NLSF residuals and zero excitation pulses; later encoder slices can
// replace those decisions without changing the surrounding stream grammar.
func (e *Encoder) encodeRangeFrame(enc *entcode.Encoder, signal []float64, vadActive, conditionalGain bool) {
	signalType := SignalTypeInactive
	if vadActive {
		signalType = SignalTypeUnvoiced
	}
	quantOffset := 0

	e.encodeTypeOffset(enc, vadActive, signalType, quantOffset)

	gainIdx := e.analysisGainIndex(signal)
	e.encodeGains(enc, signalType, gainIdx, conditionalGain)

	cb := getNLSFCB(e.lpcOrder)
	cb1Idx := e.defaultNLSFIndex(signalType, cb)
	e.encodeNLSF(enc, cb, signalType, cb1Idx)

	if e.nSubframes == 4 {
		enc.EncodeIcdf(4, silkNLSFInterpFactorICDF[:], 8) // No interpolation.
	}

	enc.EncodeIcdf(0, silkUniform4ICDF[:], 8) // Seed.
	e.encodeZeroPulses(enc, signalType, quantOffset)

	e.prevGainIdx = gainIdx
	e.prevEnergy = computeEnergy(signal)
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

func (e *Encoder) encodeGains(enc *entcode.Encoder, signalType, targetIdx int, conditional bool) {
	prevIdx := e.prevGainIdx
	if conditional {
		e.encodeGainDelta(enc, prevIdx, targetIdx)
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

	for sf := 1; sf < e.nSubframes; sf++ {
		e.encodeGainDelta(enc, targetIdx, targetIdx)
	}
}

func (e *Encoder) encodeGainDelta(enc *entcode.Encoder, prevIdx, targetIdx int) {
	delta := targetIdx - prevIdx
	if delta < MinDeltaGainQuant {
		delta = MinDeltaGainQuant
	}
	if delta > MaxDeltaGainQuant {
		delta = MaxDeltaGainQuant
	}
	enc.EncodeIcdf(delta-MinDeltaGainQuant, silkDeltaGainICDF[:], 8)
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

func (e *Encoder) encodeNLSF(enc *entcode.Encoder, cb *nlsfCBParams, signalType, cb1Idx int) {
	if cb1Idx < 0 {
		cb1Idx = 0
	}
	if cb1Idx >= cb.nEntries {
		cb1Idx = cb.nEntries - 1
	}
	offset := (signalType >> 1) * cb.nEntries
	enc.EncodeIcdf(cb1Idx, cb.cb1ICDF[offset:offset+cb.nEntries], 8)

	ecSelBase := cb1Idx * (cb.order / 2)
	for i := 0; i < cb.order; i += 2 {
		entry := cb.cb2Select[ecSelBase+i/2]
		ecIx0 := ((int(entry) >> 1) & 7) * 9
		ecIx1 := ((int(entry) >> 5) & 7) * 9
		enc.EncodeIcdf(4, cb.cb2ICDF[ecIx0:ecIx0+9], 8)
		enc.EncodeIcdf(4, cb.cb2ICDF[ecIx1:ecIx1+9], 8)
	}
}

func (e *Encoder) encodeZeroPulses(enc *entcode.Encoder, signalType, quantOffset int) {
	row := signalType >> 1
	if row > 1 {
		row = 1
	}
	enc.EncodeIcdf(0, silkRateLevelsICDF[row][:], 8)

	iter := e.frameSize >> log2ShellCodecFrameLen
	if iter*shellCodecFrameLength < e.frameSize {
		iter++
	}
	for i := 0; i < iter; i++ {
		enc.EncodeIcdf(0, silkPulsesPerBlockICDF[0][:], 8)
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
	e.prevGains = []float64{1.0, 1.0, 1.0, 1.0}
	e.prevGainIdx = 10
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
