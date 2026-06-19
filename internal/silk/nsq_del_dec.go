package silk

import "math"

type silkNSQState struct {
	xq            []int16
	sLTPShpQ14    []int32
	sLPCQ14       [silkDecisionDelay + silkMaxLPCOrder]int32
	sAR2Q14       [silkMaxShapeLPCOrder]int32
	sLFARShpQ14   int32
	sDiffShpQ14   int32
	lagPrev       int
	sLTPBufIdx    int
	sLTPShpBufIdx int
	prevGainQ16   int32
	rewhiteFlag   bool
}

type nsqDelayedDecision struct {
	sLPCQ14   [120 + silkMaxLPCOrder]int32
	randState [silkDecisionDelay]int32
	qQ10      [silkDecisionDelay]int32
	xqQ14     [silkDecisionDelay]int32
	predQ15   [silkDecisionDelay]int32
	shapeQ14  [silkDecisionDelay]int32
	sAR2Q14   [silkMaxShapeLPCOrder]int32
	lfARQ14   int32
	diffQ14   int32
	seed      int32
	seedInit  int32
	rdQ10     int32
}

type nsqSampleState struct {
	qQ10       int32
	rdQ10      int32
	xqQ14      int32
	lfARQ14    int32
	diffQ14    int32
	sLTPShpQ14 int32
	lpcExcQ14  int32
}

func newSilkNSQState(frameSize, ltpMemLen int) silkNSQState {
	return silkNSQState{
		xq:          make([]int16, ltpMemLen+frameSize),
		sLTPShpQ14:  make([]int32, ltpMemLen+frameSize),
		lagPrev:     100,
		prevGainQ16: 65536,
	}
}

func (s silkNSQState) clone() silkNSQState {
	c := s
	c.xq = append([]int16(nil), s.xq...)
	c.sLTPShpQ14 = append([]int32(nil), s.sLTPShpQ14...)
	return c
}

func silkRAND(seed int32) int32 {
	return int32(uint32(907633515) + uint32(seed)*uint32(196314165))
}

func silkADD32Ovflw(a, b int32) int32 { return int32(uint32(a) + uint32(b)) }
func silkSUB32Ovflw(a, b int32) int32 { return int32(uint32(a) - uint32(b)) }
func silkADD32(a, b int32) int32      { return a + b }
func silkSUB32(a, b int32) int32      { return a - b }
func silkLSHIFT32(a int32, shift int) int32 {
	return int32(uint32(a) << shift)
}
func silkRSHIFT32(a int32, shift int) int32 { return a >> shift }
func silkSUBLSHIFT32(a, b int32, shift int) int32 {
	return a - silkLSHIFT32(b, shift)
}
func silkLIMIT32(a, lo, hi int32) int32 {
	if lo > hi {
		lo, hi = hi, lo
	}
	if a < lo {
		return lo
	}
	if a > hi {
		return hi
	}
	return a
}
func silkADDSAT32(a, b int32) int32 { return silkAddSat32(a, b) }
func silkSUBSAT32(a, b int32) int32 {
	diff := int64(a) - int64(b)
	if diff > math.MaxInt32 {
		return math.MaxInt32
	}
	if diff < math.MinInt32 {
		return math.MinInt32
	}
	return int32(diff)
}
func silkSMLAWT(a, b, c int32) int32 {
	return a + int32((int64(b)*int64(c>>16))>>16)
}

func silkNoiseShapeQuantizerShortPrediction(buf []int32, ptr int, aQ12 []int16, order int) int32 {
	out := int32(order >> 1)
	for i := 0; i < order; i++ {
		idx := ptr - i
		if idx >= 0 && idx < len(buf) {
			out = silkSMLAWB(out, buf[idx], aQ12[i])
		}
	}
	return out
}

func (e *Encoder) silkNSQDelDec(
	x16 []int16,
	lpcQ12 []int16,
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
	if e.nsq.prevGainQ16 == 0 {
		e.nsq.prevGainQ16 = 65536
	}
	lag := e.nsq.lagPrev
	offsetQ10 := int32(silkQuantizationOffsetsQ10[signalType>>1][quantOffsetType])
	cfg := e.silkComplexityConfig()
	nStates := cfg.nStatesDelayedDecision
	if nStates < 1 {
		nStates = 1
	}
	if nStates > 4 {
		nStates = 4
	}

	delDec := make([]nsqDelayedDecision, nStates)
	for k := 0; k < nStates; k++ {
		dd := &delDec[k]
		dd.seed = int32((k + int(seed)) & 3)
		dd.seedInit = dd.seed
		dd.lfARQ14 = e.nsq.sLFARShpQ14
		dd.diffQ14 = e.nsq.sDiffShpQ14
		if len(e.nsq.sLTPShpQ14) > 0 {
			idx := silkLTPMemLengthMs*(e.sampleRate/1000) - 1
			if idx >= 0 && idx < len(e.nsq.sLTPShpQ14) {
				dd.shapeQ14[0] = e.nsq.sLTPShpQ14[idx]
			}
		}
		copy(dd.sLPCQ14[:silkMaxLPCOrder], e.nsq.sLPCQ14[:])
		copy(dd.sAR2Q14[:], e.nsq.sAR2Q14[:])
	}

	decisionDelay := silkDecisionDelay
	subframeLen := e.frameSize / e.nSubframes
	if decisionDelay > subframeLen {
		decisionDelay = subframeLen
	}
	if signalType == SignalTypeVoiced {
		for k := 0; k < e.nSubframes; k++ {
			limit := pitchL[k] - 5/2 - 1
			if limit < decisionDelay {
				decisionDelay = limit
			}
		}
	} else if lag > 0 {
		limit := lag - 5/2 - 1
		if limit < decisionDelay {
			decisionDelay = limit
		}
	}
	if decisionDelay < 1 {
		decisionDelay = 1
	}

	ltpMemLen := silkLTPMemLengthMs * (e.sampleRate / 1000)
	if len(e.nsq.xq) != ltpMemLen+e.frameSize {
		e.nsq = newSilkNSQState(e.frameSize, ltpMemLen)
	}
	sLTPQ15 := make([]int32, ltpMemLen+e.frameSize)
	sLTP := make([]int16, ltpMemLen+e.frameSize)
	xScQ10 := make([]int32, subframeLen)
	delayedGainQ10 := make([]int32, silkDecisionDelay)

	e.nsq.sLTPShpBufIdx = ltpMemLen
	e.nsq.sLTPBufIdx = ltpMemLen
	smplBufIdx := 0
	subfrCount := 0
	for sf := 0; sf < e.nSubframes; sf++ {
		aQ12 := lpcQ12
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
			if sf == 0 {
				startIdx := ltpMemLen - lag - e.lpcOrder - 5/2
				if startIdx < 0 {
					startIdx = 0
				}
				filterLen := ltpMemLen - startIdx
				if filterLen > 0 {
					silkLPCAnalysisFilter(sLTP[startIdx:startIdx+filterLen], int16SliceToInt32(e.nsq.xq[startIdx:startIdx+filterLen]), aQ12, filterLen, e.lpcOrder)
				}
				e.nsq.sLTPBufIdx = ltpMemLen
				e.nsq.rewhiteFlag = true
			}
		}

		frameOffset := sf * subframeLen
		e.silkNSQDelDecScaleStates(delDec, x16[frameOffset:frameOffset+subframeLen], xScQ10, sLTP, sLTPQ15,
			sf, nStates, int32(ltpScaleQ14), gainsQ16, pitchL, signalType, decisionDelay)

		e.silkNoiseShapeQuantizerDelDec(delDec, signalType, xScQ10, pulses, frameOffset,
			e.nsq.xq, ltpMemLen+frameOffset, sLTPQ15, delayedGainQ10, aQ12, bQ14[:],
			shape.AR_Q13[sf][:], lag, harmShapeFIRPackedQ14, shape.Tilt_Q14[sf],
			shape.LF_shp_Q14[sf], gainsQ16[sf], lambdaQ10, offsetQ10, subframeLen,
			subfrCount, shape.ShapingLPCOrder, e.lpcOrder, int(shape.Warping_Q16),
			nStates, &smplBufIdx, decisionDelay)
		subfrCount++
	}

	winner := 0
	rdMin := delDec[0].rdQ10
	for k := 1; k < nStates; k++ {
		if delDec[k].rdQ10 < rdMin {
			rdMin = delDec[k].rdQ10
			winner = k
		}
	}
	// libopus writes the winning state's initial seed back to the bitstream
	// (psIndices->Seed = psDelDec[Winner_ind].SeedInit) so the decoder replays
	// the same pseudo-random sign sequence the winner used. The caller encodes
	// this seed instead of the base seed.
	e.nsqSeed = delDec[winner].seedInit & 3
	dd := &delDec[winner]
	lastIdx := smplBufIdx + decisionDelay
	gainQ10 := silkRSHIFT32(gainsQ16[e.nSubframes-1], 6)
	pxqOffset := ltpMemLen + e.frameSize
	for i := 0; i < decisionDelay; i++ {
		lastIdx = (lastIdx - 1) % silkDecisionDelay
		if lastIdx < 0 {
			lastIdx += silkDecisionDelay
		}
		outPos := e.frameSize - decisionDelay + i
		if outPos >= 0 && outPos < len(pulses) {
			pulses[outPos] = int16(silkRShiftRound(int64(dd.qQ10[lastIdx]), 10))
			e.nsq.xq[pxqOffset-decisionDelay+i] = clamp16(silkRShiftRound(int64(silkSMULWW(dd.xqQ14[lastIdx], gainQ10)), 8))
			e.nsq.sLTPShpQ14[pxqOffset-decisionDelay+i] = dd.shapeQ14[lastIdx]
		}
	}
	copy(e.nsq.sLPCQ14[:], dd.sLPCQ14[subframeLen:subframeLen+silkMaxLPCOrder])
	copy(e.nsq.sAR2Q14[:], dd.sAR2Q14[:])
	e.nsq.sLFARShpQ14 = dd.lfARQ14
	e.nsq.sDiffShpQ14 = dd.diffQ14
	e.nsq.lagPrev = pitchL[e.nSubframes-1]
	copy(e.nsq.xq, e.nsq.xq[e.frameSize:])
	copy(e.nsq.sLTPShpQ14, e.nsq.sLTPShpQ14[e.frameSize:])
	e.prevGainQ16 = e.nsq.prevGainQ16
	e.syncLegacyNSQState()
	return pulses
}

func (e *Encoder) silkNoiseShapeQuantizerDelDec(
	delDec []nsqDelayedDecision,
	signalType int,
	xQ10 []int32,
	pulses []int16,
	pulsesBase int,
	xq []int16,
	xqBase int,
	sLTPQ15 []int32,
	delayedGainQ10 []int32,
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
	length int,
	subfr int,
	shapingLPCOrder int,
	predictLPCOrder int,
	warpingQ16 int,
	nStatesDelayedDecision int,
	smplBufIdx *int,
	decisionDelay int,
) {
	sampleState := make([][2]nsqSampleState, nStatesDelayedDecision)
	shpLagBase := e.nsq.sLTPShpBufIdx - lag + silkHarmShapeFIRTaps/2
	predLagBase := e.nsq.sLTPBufIdx - lag + 5/2
	gainQ10 := silkRSHIFT32(gainQ16, 6)
	for i := 0; i < length; i++ {
		ltpPredQ14 := int32(0)
		if signalType == SignalTypeVoiced {
			ltpPredQ14 = 2
			for k := 0; k < 5; k++ {
				idx := predLagBase + i - k
				if idx >= 0 && idx < len(sLTPQ15) {
					ltpPredQ14 = silkSMLAWB(ltpPredQ14, sLTPQ15[idx], bQ14[k])
				}
			}
			ltpPredQ14 = silkLSHIFT32(ltpPredQ14, 1)
		}
		nLTPQ14 := int32(0)
		if lag > 0 {
			idx := shpLagBase + i
			s0, s1, s2 := int32(0), int32(0), int32(0)
			if idx >= 0 && idx < len(e.nsq.sLTPShpQ14) {
				s0 = e.nsq.sLTPShpQ14[idx]
			}
			if idx-1 >= 0 && idx-1 < len(e.nsq.sLTPShpQ14) {
				s1 = e.nsq.sLTPShpQ14[idx-1]
			}
			if idx-2 >= 0 && idx-2 < len(e.nsq.sLTPShpQ14) {
				s2 = e.nsq.sLTPShpQ14[idx-2]
			}
			nLTPQ14 = silkSMULWB(silkADDSAT32(s0, s2), int16(harmShapeFIRPackedQ14))
			nLTPQ14 = silkSMLAWT(nLTPQ14, s1, harmShapeFIRPackedQ14)
			nLTPQ14 = silkSUBLSHIFT32(ltpPredQ14, nLTPQ14, 2)
		}

		for k := 0; k < nStatesDelayedDecision; k++ {
			dd := &delDec[k]
			ss := &sampleState[k]
			dd.seed = silkRAND(dd.seed)
			lpcPredQ14 := silkNoiseShapeQuantizerShortPrediction(dd.sLPCQ14[:], silkMaxLPCOrder-1+i, aQ12, predictLPCOrder)
			lpcPredQ14 = silkLSHIFT32(lpcPredQ14, 4)

			tmp2 := silkSMLAWB(dd.diffQ14, dd.sAR2Q14[0], int16(warpingQ16))
			tmp1 := silkSMLAWB(dd.sAR2Q14[0], silkSUB32Ovflw(dd.sAR2Q14[1], tmp2), int16(warpingQ16))
			dd.sAR2Q14[0] = tmp2
			nARQ14 := int32(shapingLPCOrder >> 1)
			nARQ14 = silkSMLAWB(nARQ14, tmp2, arShpQ13[0])
			for j := 2; j < shapingLPCOrder; j += 2 {
				tmp2 = silkSMLAWB(dd.sAR2Q14[j-1], silkSUB32Ovflw(dd.sAR2Q14[j], tmp1), int16(warpingQ16))
				dd.sAR2Q14[j-1] = tmp1
				nARQ14 = silkSMLAWB(nARQ14, tmp1, arShpQ13[j-1])
				tmp1 = silkSMLAWB(dd.sAR2Q14[j], silkSUB32Ovflw(dd.sAR2Q14[j+1], tmp2), int16(warpingQ16))
				dd.sAR2Q14[j] = tmp2
				nARQ14 = silkSMLAWB(nARQ14, tmp2, arShpQ13[j])
			}
			dd.sAR2Q14[shapingLPCOrder-1] = tmp1
			nARQ14 = silkSMLAWB(nARQ14, tmp1, arShpQ13[shapingLPCOrder-1])
			nARQ14 = silkLSHIFT32(nARQ14, 1)
			nARQ14 = silkSMLAWB(nARQ14, dd.lfARQ14, int16(tiltQ14))
			nARQ14 = silkLSHIFT32(nARQ14, 2)

			nLFQ14 := silkSMULWB(dd.shapeQ14[*smplBufIdx], int16(lfShpQ14))
			nLFQ14 = silkSMLAWT(nLFQ14, dd.lfARQ14, lfShpQ14)
			nLFQ14 = silkLSHIFT32(nLFQ14, 2)

			tmp1 = silkADDSAT32(nARQ14, nLFQ14)
			tmp2 = silkADD32Ovflw(nLTPQ14, lpcPredQ14)
			tmp1 = silkSUBSAT32(tmp2, tmp1)
			tmp1 = silkRShiftRound(int64(tmp1), 4)
			rQ10 := silkSUB32(xQ10[i], tmp1)
			if dd.seed < 0 {
				rQ10 = -rQ10
			}
			rQ10 = silkLIMIT32(rQ10, -(31 << 10), 30<<10)
			q1Q10, q2Q10, rd1Q10, rd2Q10 := nsqQuantCandidates(rQ10, offsetQ10, lambdaQ10)
			if rd1Q10 < rd2Q10 {
				ss[0].rdQ10 = silkADD32(dd.rdQ10, rd1Q10)
				ss[1].rdQ10 = silkADD32(dd.rdQ10, rd2Q10)
				ss[0].qQ10, ss[1].qQ10 = q1Q10, q2Q10
			} else {
				ss[0].rdQ10 = silkADD32(dd.rdQ10, rd2Q10)
				ss[1].rdQ10 = silkADD32(dd.rdQ10, rd1Q10)
				ss[0].qQ10, ss[1].qQ10 = q2Q10, q1Q10
			}
			for c := 0; c < 2; c++ {
				excQ14 := silkLSHIFT32(ss[c].qQ10, 4)
				if dd.seed < 0 {
					excQ14 = -excQ14
				}
				lpcExcQ14 := silkADD32(excQ14, ltpPredQ14)
				xqQ14 := silkADD32Ovflw(lpcExcQ14, lpcPredQ14)
				ss[c].diffQ14 = silkSUB32Ovflw(xqQ14, silkLSHIFT32(xQ10[i], 4))
				sLFARShpQ14 := silkSUB32Ovflw(ss[c].diffQ14, nARQ14)
				ss[c].sLTPShpQ14 = silkSUBSAT32(sLFARShpQ14, nLFQ14)
				ss[c].lfARQ14 = sLFARShpQ14
				ss[c].lpcExcQ14 = lpcExcQ14
				ss[c].xqQ14 = xqQ14
			}
		}

		*smplBufIdx = (*smplBufIdx - 1) % silkDecisionDelay
		if *smplBufIdx < 0 {
			*smplBufIdx += silkDecisionDelay
		}
		lastIdx := (*smplBufIdx + decisionDelay) % silkDecisionDelay

		winner := 0
		rdMin := sampleState[0][0].rdQ10
		for k := 1; k < nStatesDelayedDecision; k++ {
			if sampleState[k][0].rdQ10 < rdMin {
				rdMin = sampleState[k][0].rdQ10
				winner = k
			}
		}
		winnerRand := delDec[winner].randState[lastIdx]
		for k := 0; k < nStatesDelayedDecision; k++ {
			if delDec[k].randState[lastIdx] != winnerRand {
				sampleState[k][0].rdQ10 = silkADD32(sampleState[k][0].rdQ10, math.MaxInt32>>4)
				sampleState[k][1].rdQ10 = silkADD32(sampleState[k][1].rdQ10, math.MaxInt32>>4)
			}
		}
		rdMax, rdMin2 := sampleState[0][0].rdQ10, sampleState[0][1].rdQ10
		rdMaxInd, rdMinInd := 0, 0
		for k := 1; k < nStatesDelayedDecision; k++ {
			if sampleState[k][0].rdQ10 > rdMax {
				rdMax, rdMaxInd = sampleState[k][0].rdQ10, k
			}
			if sampleState[k][1].rdQ10 < rdMin2 {
				rdMin2, rdMinInd = sampleState[k][1].rdQ10, k
			}
		}
		if rdMin2 < rdMax {
			copyDelayedDecisionSuffix(&delDec[rdMaxInd], &delDec[rdMinInd], i)
			sampleState[rdMaxInd][0] = sampleState[rdMinInd][1]
		}

		dd := &delDec[winner]
		if subfr > 0 || i >= decisionDelay {
			// libopus advances the pulses/pxq pointers per subframe, so the
			// delayed write pulses[i-decisionDelay] reaches back into the prior
			// subframe's region. Mirror that with absolute base offsets rather
			// than a per-subframe sub-slice (which would drop those writes).
			pIdx := pulsesBase + i - decisionDelay
			xIdx := xqBase + i - decisionDelay
			if pIdx >= 0 && pIdx < len(pulses) && xIdx >= 0 && xIdx < len(xq) {
				pulses[pIdx] = int16(silkRShiftRound(int64(dd.qQ10[lastIdx]), 10))
				xq[xIdx] = clamp16(silkRShiftRound(int64(silkSMULWW(dd.xqQ14[lastIdx], delayedGainQ10[lastIdx])), 8))
				idx := e.nsq.sLTPShpBufIdx - decisionDelay
				if idx >= 0 && idx < len(e.nsq.sLTPShpQ14) {
					e.nsq.sLTPShpQ14[idx] = dd.shapeQ14[lastIdx]
				}
				idx = e.nsq.sLTPBufIdx - decisionDelay
				if idx >= 0 && idx < len(sLTPQ15) {
					sLTPQ15[idx] = dd.predQ15[lastIdx]
				}
			}
		}
		e.nsq.sLTPShpBufIdx++
		e.nsq.sLTPBufIdx++

		for k := 0; k < nStatesDelayedDecision; k++ {
			dd := &delDec[k]
			ss := &sampleState[k][0]
			dd.lfARQ14 = ss.lfARQ14
			dd.diffQ14 = ss.diffQ14
			dd.sLPCQ14[silkMaxLPCOrder+i] = ss.xqQ14
			dd.xqQ14[*smplBufIdx] = ss.xqQ14
			dd.qQ10[*smplBufIdx] = ss.qQ10
			dd.predQ15[*smplBufIdx] = silkLSHIFT32(ss.lpcExcQ14, 1)
			dd.shapeQ14[*smplBufIdx] = ss.sLTPShpQ14
			dd.seed = silkADD32Ovflw(dd.seed, silkRShiftRound(int64(ss.qQ10), 10))
			dd.randState[*smplBufIdx] = dd.seed
			dd.rdQ10 = ss.rdQ10
		}
		delayedGainQ10[*smplBufIdx] = gainQ10
	}
	for k := 0; k < nStatesDelayedDecision; k++ {
		copy(delDec[k].sLPCQ14[:silkMaxLPCOrder], delDec[k].sLPCQ14[length:length+silkMaxLPCOrder])
	}
}

func nsqQuantCandidates(rQ10, offsetQ10, lambdaQ10 int32) (q1Q10, q2Q10, rd1Q10, rd2Q10 int32) {
	q1Q0 := silkRSHIFT32(silkSUB32(rQ10, offsetQ10), 10)
	if lambdaQ10 > 2048 {
		rdoOffset := lambdaQ10/2 - 512
		switch {
		case silkSUB32(rQ10, offsetQ10) > rdoOffset:
			q1Q0 = silkRSHIFT32(silkSUB32(silkSUB32(rQ10, offsetQ10), rdoOffset), 10)
		case silkSUB32(rQ10, offsetQ10) < -rdoOffset:
			q1Q0 = silkRSHIFT32(silkADD32(silkSUB32(rQ10, offsetQ10), rdoOffset), 10)
		case silkSUB32(rQ10, offsetQ10) < 0:
			q1Q0 = -1
		default:
			q1Q0 = 0
		}
	}
	switch {
	case q1Q0 > 0:
		q1Q10 = silkADD32(silkSUB32(silkLSHIFT32(q1Q0, 10), silkQuantLevelAdjustQ10), offsetQ10)
		q2Q10 = silkADD32(q1Q10, 1024)
		rd1Q10 = silkSMULBB(q1Q10, lambdaQ10)
		rd2Q10 = silkSMULBB(q2Q10, lambdaQ10)
	case q1Q0 == 0:
		q1Q10 = offsetQ10
		q2Q10 = silkADD32(q1Q10, 1024-silkQuantLevelAdjustQ10)
		rd1Q10 = silkSMULBB(q1Q10, lambdaQ10)
		rd2Q10 = silkSMULBB(q2Q10, lambdaQ10)
	case q1Q0 == -1:
		q2Q10 = offsetQ10
		q1Q10 = silkSUB32(q2Q10, 1024-silkQuantLevelAdjustQ10)
		rd1Q10 = silkSMULBB(-q1Q10, lambdaQ10)
		rd2Q10 = silkSMULBB(q2Q10, lambdaQ10)
	default:
		q1Q10 = silkADD32(silkADD32(silkLSHIFT32(q1Q0, 10), silkQuantLevelAdjustQ10), offsetQ10)
		q2Q10 = silkADD32(q1Q10, 1024)
		rd1Q10 = silkSMULBB(-q1Q10, lambdaQ10)
		rd2Q10 = silkSMULBB(-q2Q10, lambdaQ10)
	}
	rrQ10 := silkSUB32(rQ10, q1Q10)
	rd1Q10 = silkRSHIFT32(silkSMLABB(rd1Q10, rrQ10, rrQ10), 10)
	rrQ10 = silkSUB32(rQ10, q2Q10)
	rd2Q10 = silkRSHIFT32(silkSMLABB(rd2Q10, rrQ10, rrQ10), 10)
	return
}

func copyDelayedDecisionSuffix(dst, src *nsqDelayedDecision, sample int) {
	copy(dst.sLPCQ14[sample:], src.sLPCQ14[sample:])
	dst.randState = src.randState
	dst.qQ10 = src.qQ10
	dst.xqQ14 = src.xqQ14
	dst.predQ15 = src.predQ15
	dst.shapeQ14 = src.shapeQ14
	dst.sAR2Q14 = src.sAR2Q14
	dst.lfARQ14 = src.lfARQ14
	dst.diffQ14 = src.diffQ14
	dst.seed = src.seed
	dst.seedInit = src.seedInit
	dst.rdQ10 = src.rdQ10
}

func (e *Encoder) silkNSQDelDecScaleStates(
	delDec []nsqDelayedDecision,
	x16 []int16,
	xScQ10 []int32,
	sLTP []int16,
	sLTPQ15 []int32,
	subfr int,
	nStatesDelayedDecision int,
	ltpScaleQ14 int32,
	gainsQ16 [silkMaxNBSubframes]int32,
	pitchL [silkMaxNBSubframes]int,
	signalType int,
	decisionDelay int,
) {
	lag := pitchL[subfr]
	gain := gainsQ16[subfr]
	if gain < 1 {
		gain = 1
	}
	invGainQ31 := silkInverse32VarQ(gain, 47)
	invGainQ26 := silkRShiftRound(int64(invGainQ31), 5)
	for i := 0; i < len(x16) && i < len(xScQ10); i++ {
		xScQ10[i] = silkSMULWW(int32(x16[i]), invGainQ26)
	}
	if e.nsq.rewhiteFlag {
		if subfr == 0 {
			invGainQ31 = silkLSHIFT32(silkSMULWB(invGainQ31, int16(ltpScaleQ14)), 2)
		}
		for i := e.nsq.sLTPBufIdx - lag - 5/2; i < e.nsq.sLTPBufIdx; i++ {
			if i >= 0 && i < len(sLTPQ15) && i < len(sLTP) {
				sLTPQ15[i] = silkSMULWB(invGainQ31, sLTP[i])
			}
		}
	}
	if gainsQ16[subfr] != e.nsq.prevGainQ16 {
		gainAdjQ16 := silkDIV32VarQ(e.nsq.prevGainQ16, gainsQ16[subfr], 16)
		for i := e.nsq.sLTPShpBufIdx - silkLTPMemLengthMs*(e.sampleRate/1000); i < e.nsq.sLTPShpBufIdx; i++ {
			if i >= 0 && i < len(e.nsq.sLTPShpQ14) {
				e.nsq.sLTPShpQ14[i] = silkSMULWW(gainAdjQ16, e.nsq.sLTPShpQ14[i])
			}
		}
		if signalType == SignalTypeVoiced && !e.nsq.rewhiteFlag {
			for i := e.nsq.sLTPBufIdx - lag - 5/2; i < e.nsq.sLTPBufIdx-decisionDelay; i++ {
				if i >= 0 && i < len(sLTPQ15) {
					sLTPQ15[i] = silkSMULWW(gainAdjQ16, sLTPQ15[i])
				}
			}
		}
		for k := 0; k < nStatesDelayedDecision; k++ {
			dd := &delDec[k]
			dd.lfARQ14 = silkSMULWW(gainAdjQ16, dd.lfARQ14)
			dd.diffQ14 = silkSMULWW(gainAdjQ16, dd.diffQ14)
			for i := 0; i < silkMaxLPCOrder; i++ {
				dd.sLPCQ14[i] = silkSMULWW(gainAdjQ16, dd.sLPCQ14[i])
			}
			for i := 0; i < silkMaxShapeLPCOrder; i++ {
				dd.sAR2Q14[i] = silkSMULWW(gainAdjQ16, dd.sAR2Q14[i])
			}
			for i := 0; i < silkDecisionDelay; i++ {
				dd.predQ15[i] = silkSMULWW(gainAdjQ16, dd.predQ15[i])
				dd.shapeQ14[i] = silkSMULWW(gainAdjQ16, dd.shapeQ14[i])
			}
		}
		e.nsq.prevGainQ16 = gainsQ16[subfr]
	}
}

func int16SliceToInt32(in []int16) []int32 {
	out := make([]int32, len(in))
	for i, v := range in {
		out[i] = int32(v)
	}
	return out
}

// syncLegacyNSQState mirrors the trellis NSQ output history into the legacy
// lpcState / ltpState / prevGainQ16 fields. Downstream analysis that predates
// the delayed-decision NSQ (gain & pitch estimation, currentFrameOutputRMS,
// frame-state snapshot/restore) still reads those fields, so they must reflect
// what the trellis actually produced to keep enc/dec state continuity.
func (e *Encoder) syncLegacyNSQState() {
	ltpMemLen := len(e.ltpState)
	if ltpMemLen > 0 && len(e.nsq.xq) >= ltpMemLen {
		// After silkNSQDelDec shifts e.nsq.xq left by frameSize, the first
		// ltpMemLen int16 samples hold the most recent reconstructed output.
		for i := 0; i < ltpMemLen; i++ {
			e.ltpState[i] = int32(e.nsq.xq[i])
		}
	}
	if len(e.lpcState) == silkMaxLPCOrder {
		copy(e.lpcState, e.nsq.sLPCQ14[:silkMaxLPCOrder])
	}
	e.prevGainQ16 = e.nsq.prevGainQ16
}

// syncTrellisNSQState mirrors the legacy/homebrew NSQ reconstruction into the
// delayed-decision state. Unvoiced and inactive frames use the homebrew NSQ,
// while voiced frames use the trellis; without this reverse handoff an
// unvoiced-to-voiced transition re-whitens zero/stale trellis history even
// though the decoder uses the actual previous reconstruction.
func (e *Encoder) syncTrellisNSQState() {
	ltpMemLen := len(e.ltpState)
	if len(e.nsq.xq) != ltpMemLen+e.frameSize {
		e.nsq = newSilkNSQState(e.frameSize, ltpMemLen)
	}
	for i := 0; i < ltpMemLen; i++ {
		e.nsq.xq[i] = clamp16(e.ltpState[i])
	}
	for i := ltpMemLen; i < len(e.nsq.xq); i++ {
		e.nsq.xq[i] = 0
	}
	for i := range e.nsq.sLPCQ14 {
		e.nsq.sLPCQ14[i] = 0
	}
	copy(e.nsq.sLPCQ14[:silkMaxLPCOrder], e.lpcState)
	for i := range e.nsq.sLTPShpQ14 {
		e.nsq.sLTPShpQ14[i] = 0
	}
	for i := range e.nsq.sAR2Q14 {
		e.nsq.sAR2Q14[i] = 0
	}
	e.nsq.sLFARShpQ14 = 0
	e.nsq.sDiffShpQ14 = 0
	e.nsq.lagPrev = e.prevPitchLag
	e.nsq.sLTPBufIdx = ltpMemLen
	e.nsq.sLTPShpBufIdx = ltpMemLen
	e.nsq.prevGainQ16 = e.prevGainQ16
	e.nsq.rewhiteFlag = false
}
