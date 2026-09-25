package silk

// nsq_plain.go ports silk_NSQ (silk/NSQ.c), the noise shaping quantizer
// without delayed decision that libopus uses when
// nStatesDelayedDecision == 1 and warping_Q16 == 0 (complexity 0 and 1).
// Its arithmetic differs from the single-state delayed-decision quantizer
// in Q-format and rounding, so the pulses are not interchangeable.

// silkNSQPlain runs silk_NSQ over one frame and updates e.nsq like the
// delayed-decision port does; it returns the pulses and leaves the seed
// (silk_NSQ does not change psIndices->Seed) in e.nsqSeed.
func (e *Encoder) silkNSQPlain(
	x16 []int16,
	lpcQ12 []int16,
	lpcQ12Interp []int16,
	ltpCoefQ14 [][5]int16,
	shape silkNoiseShapeAnalysis,
	gainsQ16 [silkMaxNBSubframes]int32,
	pitchL [silkMaxNBSubframes]int,
	lambdaQ10 int32,
	ltpScaleQ14 int16,
	signalType, quantOffsetType int,
	seed int32,
) []int16 {
	pulses := make([]int16, e.frameSize)
	interpActive := len(lpcQ12Interp) >= e.lpcOrder
	lpcForSubframe := func(sf int) []int16 {
		if interpActive && sf < 2 {
			return lpcQ12Interp
		}
		return lpcQ12
	}
	if e.nsq.prevGainQ16 == 0 {
		e.nsq.prevGainQ16 = 65536
	}
	randSeed := seed
	lag := e.nsq.lagPrev
	offsetQ10 := int32(silkQuantizationOffsetsQ10[signalType>>1][quantOffsetType])
	subframeLen := e.frameSize / e.nSubframes
	ltpMemLen := silkLTPMemLengthMs * (e.sampleRate / 1000)
	if len(e.nsq.xq) != ltpMemLen+e.frameSize {
		e.nsq = newSilkNSQState(e.frameSize, ltpMemLen)
	}
	sLTPQ15 := make([]int32, ltpMemLen+e.frameSize)
	sLTP := make([]int16, ltpMemLen+e.frameSize)
	xScQ10 := make([]int32, subframeLen)
	// sLPC_Q14[MAX_SUB_FRAME_LENGTH + NSQ_LPC_BUF_LENGTH]: the state occupies
	// the first NSQ_LPC_BUF_LENGTH (= MAX_LPC_ORDER) entries.
	sLPCQ14 := make([]int32, subframeLen+silkMaxLPCOrder)
	copy(sLPCQ14, e.nsq.sLPCQ14[:silkMaxLPCOrder])

	e.nsq.sLTPShpBufIdx = ltpMemLen
	e.nsq.sLTPBufIdx = ltpMemLen
	for sf := 0; sf < e.nSubframes; sf++ {
		aQ12 := lpcForSubframe(sf)
		if len(aQ12) < e.lpcOrder {
			tmp := make([]int16, e.lpcOrder)
			copy(tmp, aQ12)
			aQ12 = tmp
		}
		var bQ14 [5]int16
		if sf < len(ltpCoefQ14) {
			bQ14 = ltpCoefQ14[sf]
		}
		harmShapeFIRPackedQ14 := silkRSHIFT32(shape.HarmShapeGain_Q14[sf], 2)
		harmShapeFIRPackedQ14 |= silkLSHIFT32(silkRSHIFT32(shape.HarmShapeGain_Q14[sf], 1), 16)

		e.nsq.rewhiteFlag = false
		if signalType == SignalTypeVoiced {
			lag = pitchL[sf]
			// Rewhiten with new A coefs: k & (3 - (LSF_interpolation_flag << 1)) == 0.
			if sf == 0 || (interpActive && sf == 2) {
				startIdx := ltpMemLen - lag - e.lpcOrder - 5/2
				if startIdx < 0 {
					startIdx = 0
				}
				filterLen := ltpMemLen - startIdx
				if filterLen > 0 {
					xqOff := sf * subframeLen
					silkLPCAnalysisFilter(sLTP[startIdx:startIdx+filterLen], int16SliceToInt32(e.nsq.xq[startIdx+xqOff:startIdx+xqOff+filterLen]), aQ12, filterLen, e.lpcOrder)
				}
				e.nsq.rewhiteFlag = true
				e.nsq.sLTPBufIdx = ltpMemLen
			}
		}

		frameOffset := sf * subframeLen
		e.silkNSQScaleStates(x16[frameOffset:frameOffset+subframeLen], xScQ10, sLTP, sLTPQ15, sLPCQ14,
			sf, int32(ltpScaleQ14), gainsQ16, pitchL, signalType)

		randSeed = e.silkNoiseShapeQuantizer(&randSeed, signalType, xScQ10, pulses[frameOffset:frameOffset+subframeLen],
			e.nsq.xq[ltpMemLen+frameOffset:ltpMemLen+frameOffset+subframeLen], sLTPQ15, sLPCQ14, aQ12, bQ14[:],
			shape.AR_Q13[sf][:], lag, harmShapeFIRPackedQ14, shape.Tilt_Q14[sf], shape.LF_shp_Q14[sf],
			gainsQ16[sf], lambdaQ10, offsetQ10, subframeLen, shape.ShapingLPCOrder, e.lpcOrder)
	}

	// Update lagPrev for next frame; save quantized speech and noise shaping
	// signals.
	e.nsq.lagPrev = pitchL[e.nSubframes-1]
	copy(e.nsq.sLPCQ14[:silkMaxLPCOrder], sLPCQ14[:silkMaxLPCOrder])
	copy(e.nsq.xq, e.nsq.xq[e.frameSize:])
	copy(e.nsq.sLTPShpQ14, e.nsq.sLTPShpQ14[e.frameSize:])
	e.prevGainQ16 = e.nsq.prevGainQ16
	e.nsqSeed = seed
	e.syncLegacyNSQState()
	return pulses
}

// silkNSQScaleStates ports silk_nsq_scale_states.
func (e *Encoder) silkNSQScaleStates(
	x16 []int16,
	xScQ10 []int32,
	sLTP []int16,
	sLTPQ15 []int32,
	sLPCQ14 []int32,
	subfr int,
	ltpScaleQ14 int32,
	gainsQ16 [silkMaxNBSubframes]int32,
	pitchL [silkMaxNBSubframes]int,
	signalType int,
) {
	lag := pitchL[subfr]
	gain := gainsQ16[subfr]
	if gain < 1 {
		gain = 1
	}
	invGainQ31 := silkInverse32VarQ(gain, 47)
	// Scale input
	invGainQ26 := silkRShiftRound(int64(invGainQ31), 5)
	for i := 0; i < len(x16) && i < len(xScQ10); i++ {
		xScQ10[i] = silkSMULWW(int32(x16[i]), invGainQ26)
	}
	// After rewhitening the LTP state is un-scaled, so scale with inv_gain_Q16
	if e.nsq.rewhiteFlag {
		if subfr == 0 {
			// Do LTP downscaling
			invGainQ31 = silkLSHIFT32(silkSMULWB(invGainQ31, int16(ltpScaleQ14)), 2)
		}
		for i := e.nsq.sLTPBufIdx - lag - 5/2; i < e.nsq.sLTPBufIdx; i++ {
			if i >= 0 && i < len(sLTPQ15) && i < len(sLTP) {
				sLTPQ15[i] = silkSMULWB(invGainQ31, sLTP[i])
			}
		}
	}
	// Adjust for changing gain
	if gainsQ16[subfr] != e.nsq.prevGainQ16 {
		gainAdjQ16 := silkDIV32VarQ(e.nsq.prevGainQ16, gainsQ16[subfr], 16)
		// Scale long-term shaping state
		for i := e.nsq.sLTPShpBufIdx - silkLTPMemLengthMs*(e.sampleRate/1000); i < e.nsq.sLTPShpBufIdx; i++ {
			if i >= 0 && i < len(e.nsq.sLTPShpQ14) {
				e.nsq.sLTPShpQ14[i] = silkSMULWW(gainAdjQ16, e.nsq.sLTPShpQ14[i])
			}
		}
		// Scale long-term prediction state
		if signalType == SignalTypeVoiced && !e.nsq.rewhiteFlag {
			for i := e.nsq.sLTPBufIdx - lag - 5/2; i < e.nsq.sLTPBufIdx; i++ {
				if i >= 0 && i < len(sLTPQ15) {
					sLTPQ15[i] = silkSMULWW(gainAdjQ16, sLTPQ15[i])
				}
			}
		}
		e.nsq.sLFARShpQ14 = silkSMULWW(gainAdjQ16, e.nsq.sLFARShpQ14)
		e.nsq.sDiffShpQ14 = silkSMULWW(gainAdjQ16, e.nsq.sDiffShpQ14)
		// Scale short-term prediction and shaping states
		for i := 0; i < silkMaxLPCOrder; i++ {
			sLPCQ14[i] = silkSMULWW(gainAdjQ16, sLPCQ14[i])
		}
		for i := 0; i < silkMaxShapeLPCOrder; i++ {
			e.nsq.sAR2Q14[i] = silkSMULWW(gainAdjQ16, e.nsq.sAR2Q14[i])
		}
		// Save inverse gain
		e.nsq.prevGainQ16 = gainsQ16[subfr]
	}
}

// silkNSQNoiseShapeFeedbackLoop ports silk_NSQ_noise_shape_feedback_loop_c.
func silkNSQNoiseShapeFeedbackLoop(data0 int32, data1 []int32, coef []int16, order int) int32 {
	tmp2 := data0
	tmp1 := data1[0]
	data1[0] = tmp2
	out := int32(order >> 1)
	out = silkSMLAWB(out, tmp2, coef[0])
	for j := 2; j < order; j += 2 {
		tmp2 = data1[j-1]
		data1[j-1] = tmp1
		out = silkSMLAWB(out, tmp1, coef[j-1])
		tmp1 = data1[j]
		data1[j] = tmp2
		out = silkSMLAWB(out, tmp2, coef[j])
	}
	data1[order-1] = tmp1
	out = silkSMLAWB(out, tmp1, coef[order-1])
	// Q11 -> Q12
	return silkLSHIFT32(out, 1)
}

// silkNoiseShapeQuantizer ports silk_noise_shape_quantizer for one subframe;
// it returns the updated rand_seed.
func (e *Encoder) silkNoiseShapeQuantizer(
	randSeed *int32,
	signalType int,
	xScQ10 []int32,
	pulses []int16,
	xq []int16,
	sLTPQ15 []int32,
	sLPCQ14 []int32,
	aQ12 []int16,
	bQ14 []int16,
	arShpQ13 []int16,
	lag int,
	harmShapeFIRPackedQ14 int32,
	tiltQ14 int32,
	lfShpQ14 int32,
	gainQ16 int32,
	lambdaQ10 int32,
	offsetQ10 int32,
	length, shapingLPCOrder, predictLPCOrder int,
) int32 {
	shpLagPtr := e.nsq.sLTPShpBufIdx - lag + 3/2 // HARM_SHAPE_FIR_TAPS / 2
	predLagPtr := e.nsq.sLTPBufIdx - lag + 5/2   // LTP_ORDER / 2
	gainQ10 := silkRSHIFT32(gainQ16, 6)
	// Set up short term AR state
	psLPC := silkMaxLPCOrder - 1 // NSQ_LPC_BUF_LENGTH - 1
	rs := *randSeed
	for i := 0; i < length; i++ {
		// Generate dither
		rs = silkRAND(rs)
		// Short-term prediction
		lpcPredQ10 := silkNoiseShapeQuantizerShortPrediction(sLPCQ14, psLPC, aQ12, predictLPCOrder)
		// Long-term prediction
		var ltpPredQ13 int32
		if signalType == SignalTypeVoiced {
			// Avoids introducing a bias because silk_SMLAWB() always rounds to -inf
			ltpPredQ13 = 2
			ltpPredQ13 = silkSMLAWB(ltpPredQ13, sLTPQ15[predLagPtr], bQ14[0])
			ltpPredQ13 = silkSMLAWB(ltpPredQ13, sLTPQ15[predLagPtr-1], bQ14[1])
			ltpPredQ13 = silkSMLAWB(ltpPredQ13, sLTPQ15[predLagPtr-2], bQ14[2])
			ltpPredQ13 = silkSMLAWB(ltpPredQ13, sLTPQ15[predLagPtr-3], bQ14[3])
			ltpPredQ13 = silkSMLAWB(ltpPredQ13, sLTPQ15[predLagPtr-4], bQ14[4])
			predLagPtr++
		}
		// Noise shape feedback
		nARQ12 := silkNSQNoiseShapeFeedbackLoop(e.nsq.sDiffShpQ14, e.nsq.sAR2Q14[:], arShpQ13, shapingLPCOrder)
		nARQ12 = silkSMLAWB(nARQ12, e.nsq.sLFARShpQ14, int16(tiltQ14))
		nLFQ12 := silkSMULWB(e.nsq.sLTPShpQ14[e.nsq.sLTPShpBufIdx-1], int16(lfShpQ14))
		nLFQ12 = silkSMLAWT(nLFQ12, e.nsq.sLFARShpQ14, lfShpQ14)
		// Combine prediction and noise shaping signals
		tmp1 := silkSUB32Ovflw(silkLSHIFT32(lpcPredQ10, 2), nARQ12) // Q12
		tmp1 = silkSUB32Ovflw(tmp1, nLFQ12)                         // Q12
		if lag > 0 {
			// Symmetric, packed FIR coefficients
			nLTPQ13 := silkSMULWB(silkADDSAT32(e.nsq.sLTPShpQ14[shpLagPtr], e.nsq.sLTPShpQ14[shpLagPtr-2]), int16(harmShapeFIRPackedQ14))
			nLTPQ13 = silkSMLAWT(nLTPQ13, e.nsq.sLTPShpQ14[shpLagPtr-1], harmShapeFIRPackedQ14)
			nLTPQ13 = silkLSHIFT32(nLTPQ13, 1)
			shpLagPtr++
			tmp2 := silkSUB32(ltpPredQ13, nLTPQ13)             // Q13
			tmp1 = silkADD32Ovflw(tmp2, silkLSHIFT32(tmp1, 1)) // Q13
			tmp1 = silkRShiftRound(int64(tmp1), 3)             // Q10
		} else {
			tmp1 = silkRShiftRound(int64(tmp1), 2) // Q10
		}
		rQ10 := silkSUB32(xScQ10[i], tmp1) // residual error Q10
		// Flip sign depending on dither
		if rs < 0 {
			rQ10 = -rQ10
		}
		rQ10 = silkLIMIT32(rQ10, -(31 << 10), 30<<10)
		// Find two quantization level candidates and measure their rate-distortion
		q1Q10 := silkSUB32(rQ10, offsetQ10)
		q1Q0 := silkRSHIFT32(q1Q10, 10)
		if lambdaQ10 > 2048 {
			// For aggressive RDO, the bias becomes more than one pulse.
			rdoOffset := lambdaQ10/2 - 512
			switch {
			case q1Q10 > rdoOffset:
				q1Q0 = silkRSHIFT32(q1Q10-rdoOffset, 10)
			case q1Q10 < -rdoOffset:
				q1Q0 = silkRSHIFT32(q1Q10+rdoOffset, 10)
			case q1Q10 < 0:
				q1Q0 = -1
			default:
				q1Q0 = 0
			}
		}
		var q2Q10, rd1Q20, rd2Q20 int32
		switch {
		case q1Q0 > 0:
			q1Q10 = silkSUB32(silkLSHIFT32(q1Q0, 10), silkQuantLevelAdjustQ10)
			q1Q10 = silkADD32(q1Q10, offsetQ10)
			q2Q10 = silkADD32(q1Q10, 1024)
			rd1Q20 = silkSMULBB(q1Q10, lambdaQ10)
			rd2Q20 = silkSMULBB(q2Q10, lambdaQ10)
		case q1Q0 == 0:
			q1Q10 = offsetQ10
			q2Q10 = silkADD32(q1Q10, 1024-silkQuantLevelAdjustQ10)
			rd1Q20 = silkSMULBB(q1Q10, lambdaQ10)
			rd2Q20 = silkSMULBB(q2Q10, lambdaQ10)
		case q1Q0 == -1:
			q2Q10 = offsetQ10
			q1Q10 = silkSUB32(q2Q10, 1024-silkQuantLevelAdjustQ10)
			rd1Q20 = silkSMULBB(-q1Q10, lambdaQ10)
			rd2Q20 = silkSMULBB(q2Q10, lambdaQ10)
		default: // q1_Q0 < -1
			q1Q10 = silkADD32(silkLSHIFT32(q1Q0, 10), silkQuantLevelAdjustQ10)
			q1Q10 = silkADD32(q1Q10, offsetQ10)
			q2Q10 = silkADD32(q1Q10, 1024)
			rd1Q20 = silkSMULBB(-q1Q10, lambdaQ10)
			rd2Q20 = silkSMULBB(-q2Q10, lambdaQ10)
		}
		rrQ10 := silkSUB32(rQ10, q1Q10)
		rd1Q20 = silkSMLABB(rd1Q20, rrQ10, rrQ10)
		rrQ10 = silkSUB32(rQ10, q2Q10)
		rd2Q20 = silkSMLABB(rd2Q20, rrQ10, rrQ10)
		if rd2Q20 < rd1Q20 {
			q1Q10 = q2Q10
		}
		pulses[i] = int16(silkRShiftRound(int64(q1Q10), 10))
		// Excitation
		excQ14 := silkLSHIFT32(q1Q10, 4)
		if rs < 0 {
			excQ14 = -excQ14
		}
		// Add predictions
		lpcExcQ14 := silkADD32(excQ14, silkLSHIFT32(ltpPredQ13, 1))
		xqQ14 := silkADD32Ovflw(lpcExcQ14, silkLSHIFT32(lpcPredQ10, 4))
		// Scale XQ back to normal level before saving
		xq[i] = silkSAT16(silkRShiftRound(int64(silkSMULWW(xqQ14, gainQ10)), 8))
		// Update states
		psLPC++
		sLPCQ14[psLPC] = xqQ14
		e.nsq.sDiffShpQ14 = silkSUB32Ovflw(xqQ14, silkLSHIFT32(xScQ10[i], 4))
		sLFARShpQ14 := silkSUB32Ovflw(e.nsq.sDiffShpQ14, silkLSHIFT32(nARQ12, 2))
		e.nsq.sLFARShpQ14 = sLFARShpQ14
		e.nsq.sLTPShpQ14[e.nsq.sLTPShpBufIdx] = silkSUB32Ovflw(sLFARShpQ14, silkLSHIFT32(nLFQ12, 2))
		sLTPQ15[e.nsq.sLTPBufIdx] = silkLSHIFT32(lpcExcQ14, 1)
		e.nsq.sLTPShpBufIdx++
		e.nsq.sLTPBufIdx++
		// Make dither dependent on quantized signal
		rs = silkADD32Ovflw(rs, int32(pulses[i]))
	}
	// Update LPC synth buffer
	copy(sLPCQ14[:silkMaxLPCOrder], sLPCQ14[length:length+silkMaxLPCOrder])
	*randSeed = rs
	return rs
}
