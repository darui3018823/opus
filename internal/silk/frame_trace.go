package silk

// FrameTrace records the values the encoder handed to the noise-shaping
// quantizer for the most recently coded (non-LBRR) frame, in the same
// domains the instrumented libopus encoder dumps (`[SILK_ENC_NSQ_*]`), so the
// encoder oracle can name the first analysis stage that diverges.
type FrameTrace struct {
	SignalType, QuantOffset int
	NLSFQ15                 []int16    // quantized NLSF_Q15
	PredCoefQ12             [2][]int16 // row 0: first-half (interpolated) LPC, row 1: frame LPC
	GainsIndices            []int
	GainsQ16                []int32
	ARQ13                   [][]int16 // per subframe, shapingLPCOrder taps
	TiltQ14                 []int32
	HarmShapeGainQ14        []int32
	LFShpQ14                []int32
	LambdaQ10               int32
	LTPCoefQ14              []int16 // nb_subfr * 5
	PitchL                  []int
	LTPScaleQ14             int16
	Seed                    int32
	Pulses                  []int16
	// Shape32 is the libopus-faithful noise-shape analysis of the frame
	// (float32 values), for comparison with the oracle's *_FLP dumps.
	Shape32 silkNoiseShapeOutputs
	// find_LPC outputs: unquantised NLSF target, interpolation index, minInvGain.
	NLSFTargetQ15 []int16
	InterpFactor  int
	MinInvGain    float64
	// LPCInPre is find_pred_coefs' LPC_in_pre (int16 scale, float32 values)
	// and InvGains the 1/Gains it was scaled by.
	LPCInPre []float32
	InvGains []float32
	// Rate control inputs of the frame (silk_Encode): the packet budget after
	// the LBRR average, TargetRate_bps, nBitsExceeded and nBitsUsedLBRR as
	// used for this frame, the LBRR bits this packet spent (curr_nBitsUsedLBRR)
	// and ec_tell before the frame.
	NBits, TargetRateBps, NBitsExceeded, NBitsUsedLBRR, LBRRBits, Tell int
	// Loop records the encode_frame_FLP quantiser-loop iterations of the
	// frame (one entry per coded pass).
	Loop        []LoopIteration
	LoopRestore bool // the output state was restored from the "lower" iteration
}

// LoopIteration is one pass of the encode_frame_FLP quantiser loop.
type LoopIteration struct {
	Iter, NBits, MaxBits int
	UseCBR               bool
	GainMultQ8           int32
	GainsID              int32
	FoundLower           bool
	FoundUpper           bool
	Lambda               float32
	QuantOffset          int
	GainSymbols          []int
	Damage               bool
}

// StereoFrameTrace records one frame of the stereo packet flow (silk_Encode
// with nChannelsInternal == 2) in the values the instrumented libopus dumps
// as [SILK_ENC_STEREO] / [SILK_ENC_CH].
type StereoFrameTrace struct {
	Ix                   [2][3]int8
	MidOnly              bool
	Rates                [2]int32
	WidthPrev, SmthWidth int16 // stereo state after silk_stereo_LR_to_MS
	SilentSideLen        int16
	PredPrev             [2]int16
	PrevDecodeOnlyMiddle bool // before the frame
	TotalRate            int
	PrevSpeechActQ8      int
	// Per channel (mid, side): ec_tell before the channel's frame, its
	// speech_activity_Q8 and first_frame_after_reset at that point; SideCoded
	// is false when the side rate is zero.
	Tell          [2]int
	SpeechActQ8   [2]int
	FirstAfterRst [2]bool
	SideCoded     bool
}

// SideEncoder returns the side-channel encoder of a stereo encoder (nil for
// mono), for tests that inspect its traces.
func (e *Encoder) SideEncoder() *Encoder { return e.side }

// LastStereoTrace returns the stereo traces of the most recent stereo packet.
func (e *Encoder) LastStereoTrace() []StereoFrameTrace {
	return e.lastStereoTrace
}

// Shape32Values exposes the float32 noise-shape outputs for tests: AR rows,
// gains, LF_MA, LF_AR, tilt, harmonic gain, input/coding quality.
func (t FrameTrace) Shape32Values(nbSubfr, order int) (ar [][]float32, gains, lfMA, lfAR, tilt, harm []float32, inputQuality, codingQuality float32) {
	s := t.Shape32
	for k := 0; k < nbSubfr; k++ {
		row := make([]float32, order)
		for j := 0; j < order; j++ {
			row[j] = float32(s.ar[k][j])
		}
		ar = append(ar, row)
		gains = append(gains, float32(s.gains[k]))
		lfMA = append(lfMA, float32(s.lfMAShp[k]))
		lfAR = append(lfAR, float32(s.lfARShp[k]))
		tilt = append(tilt, float32(s.tilt[k]))
		harm = append(harm, float32(s.harmShapeGain[k]))
	}
	return ar, gains, lfMA, lfAR, tilt, harm, float32(s.inputQuality), float32(s.codingQuality)
}

// LastFrameTrace returns the trace of the most recently coded frame.
func (e *Encoder) LastFrameTrace() FrameTrace {
	return e.lastTrace
}

// recordNSQTrace captures the final NSQ inputs and outputs of a frame.
func (e *Encoder) recordNSQTrace(
	lpcQ12, lpcQ12Interp []int16, nlsfQ15 []int16,
	gainIndices []int, gainsQ16 [silkMaxNBSubframes]int32, pitchL [silkMaxNBSubframes]int,
	shape silkNoiseShapeAnalysis, lambdaQ10 int32, ltpCoefQ14 [][5]int16, ltpScaleQ14 int16,
	signalType, quantOffset int, seed int32, pulses []int16,
) {
	n := e.nSubframes
	t := FrameTrace{
		SignalType:   signalType,
		QuantOffset:  quantOffset,
		NLSFQ15:      append([]int16(nil), nlsfQ15...),
		GainsIndices: append([]int(nil), gainIndices...),
		LambdaQ10:    lambdaQ10,
		LTPScaleQ14:  ltpScaleQ14,
		Seed:         seed,
		Pulses:       append([]int16(nil), pulses...),
	}
	t.PredCoefQ12[1] = append([]int16(nil), lpcQ12...)
	if lpcQ12Interp != nil {
		t.PredCoefQ12[0] = append([]int16(nil), lpcQ12Interp...)
	} else {
		t.PredCoefQ12[0] = append([]int16(nil), lpcQ12...)
	}
	for sf := 0; sf < n; sf++ {
		t.GainsQ16 = append(t.GainsQ16, gainsQ16[sf])
		t.PitchL = append(t.PitchL, pitchL[sf])
		t.TiltQ14 = append(t.TiltQ14, shape.Tilt_Q14[sf])
		t.HarmShapeGainQ14 = append(t.HarmShapeGainQ14, shape.HarmShapeGain_Q14[sf])
		t.LFShpQ14 = append(t.LFShpQ14, shape.LF_shp_Q14[sf])
		t.ARQ13 = append(t.ARQ13, append([]int16(nil), shape.AR_Q13[sf][:shape.ShapingLPCOrder]...))
		if sf < len(ltpCoefQ14) {
			t.LTPCoefQ14 = append(t.LTPCoefQ14, ltpCoefQ14[sf][:]...)
		} else {
			t.LTPCoefQ14 = append(t.LTPCoefQ14, 0, 0, 0, 0, 0)
		}
	}
	e.pendingTrace = t
}
