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
