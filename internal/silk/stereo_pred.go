package silk

// Encoder-side stereo prediction: a fixed-point port of silk_stereo_LR_to_MS
// (silk/stereo_LR_to_MS.c), silk_stereo_find_predictor and
// silk_stereo_quant_pred (libopus 1.6.1).

const (
	stereoInterpLenMs        = 8    // STEREO_INTERP_LEN_MS
	stereoRatioSmoothCoefQ16 = 655  // SILK_FIX_CONST(STEREO_RATIO_SMOOTH_COEF, 16), 0.01
	stereoRatioSmoothHalfQ16 = 328  // SILK_FIX_CONST(STEREO_RATIO_SMOOTH_COEF / 2, 16)
	stereoQuantSubStepQ16    = 6554 // SILK_FIX_CONST(0.5 / STEREO_QUANT_SUB_STEPS, 16)
)

// stereoEncState is stereo_enc_state.
type stereoEncState struct {
	predPrevQ13   [2]int16
	sMid          [2]int16
	sSide         [2]int16
	midSideAmpQ0  [4]int32
	smthWidthQ14  int16
	widthPrevQ14  int16
	silentSideLen int16
}

// reset restores silk_InitEncoder's stereo state.
func (s *stereoEncState) reset() {
	*s = stereoEncState{
		midSideAmpQ0: [4]int32{0, 1, 0, 1},
		smthWidthQ14: 1 << 14,
	}
}

// stereoLRToMSResult carries silk_stereo_LR_to_MS's outputs: the mid and side
// frames as the encoder codes them (inputBuf + 1, frameLength samples), the
// predictor indices, the mid-only flag and the mid/side bitrates.
type stereoLRToMSResult struct {
	mid, side    []int16
	ix           [2][3]int8
	midOnly      bool
	midSideRates [2]int32
}

// lrToMS ports silk_stereo_LR_to_MS. left and right are this frame's
// samples as they sit at inputBuf + 2 (after the resampler); totalRateBps is
// the packet's TargetRate_bps, prevSpeechActQ8 the mid channel's previous
// speech activity, toMono the last-frame-before-mono flag.
func (s *stereoEncState) lrToMS(left, right []int16, fsKHz, frameLength int, totalRateBps int32, prevSpeechActQ8 int, toMono bool) stereoLRToMSResult {
	mid := make([]int16, frameLength+2)
	side := make([]int16, frameLength+2)
	// Convert to basic mid/side signals (x1[n-2], x2[n-2] for n >= 2; the
	// two leading samples are replaced by the buffered state below).
	for n := 2; n < frameLength+2; n++ {
		sum := int32(left[n-2]) + int32(right[n-2])
		diff := int32(left[n-2]) - int32(right[n-2])
		mid[n] = int16(silkRShiftRound(int64(sum), 1))
		side[n] = silkSAT16(silkRShiftRound(int64(diff), 1))
	}
	// Buffering
	mid[0], mid[1] = s.sMid[0], s.sMid[1]
	side[0], side[1] = s.sSide[0], s.sSide[1]
	s.sMid[0], s.sMid[1] = mid[frameLength], mid[frameLength+1]
	s.sSide[0], s.sSide[1] = side[frameLength], side[frameLength+1]

	// LP and HP filter mid and side signals
	lpMid := make([]int16, frameLength)
	hpMid := make([]int16, frameLength)
	lpSide := make([]int16, frameLength)
	hpSide := make([]int16, frameLength)
	for n := 0; n < frameLength; n++ {
		sum := silkRShiftRound(int64(int32(mid[n])+int32(mid[n+2])+int32(mid[n+1])<<1), 2)
		lpMid[n] = int16(sum)
		hpMid[n] = int16(int32(mid[n+1]) - sum)
		sum = silkRShiftRound(int64(int32(side[n])+int32(side[n+2])+int32(side[n+1])<<1), 2)
		lpSide[n] = int16(sum)
		hpSide[n] = int16(int32(side[n+1]) - sum)
	}

	// Find energies and predictors
	is10ms := frameLength == 10*fsKHz
	smoothCoefQ16 := int32(stereoRatioSmoothCoefQ16)
	if is10ms {
		smoothCoefQ16 = stereoRatioSmoothHalfQ16
	}
	smoothCoefQ16 = silkSMULWB(silkSMULBB(int32(prevSpeechActQ8), int32(prevSpeechActQ8)), int16(smoothCoefQ16))

	var predQ13 [2]int32
	var lpRatioQ14, hpRatioQ14 int32
	predQ13[0] = silkStereoFindPredictor(&lpRatioQ14, lpMid, lpSide, s.midSideAmpQ0[0:2], frameLength, smoothCoefQ16)
	predQ13[1] = silkStereoFindPredictor(&hpRatioQ14, hpMid, hpSide, s.midSideAmpQ0[2:4], frameLength, smoothCoefQ16)
	// Ratio of the norms of residual and mid signals
	fracQ16 := silkSMLABB(hpRatioQ14, lpRatioQ14, 3)
	if fracQ16 > 1<<16 {
		fracQ16 = 1 << 16
	}

	// Determine bitrate distribution between mid and side, and possibly
	// reduce stereo width
	if is10ms {
		totalRateBps -= 1200
	} else {
		totalRateBps -= 600
	}
	if totalRateBps < 1 {
		totalRateBps = 1
	}
	minMidRateBps := silkSMLABB(2000, int32(fsKHz), 600)
	// Default bitrate distribution: 8 parts for Mid and (5+3*frac) parts for
	// Side, so mid_rate = (8 / (13 + 3 * frac)) * total_rate.
	frac3Q16 := 3 * fracQ16
	var rates [2]int32
	var widthQ14 int32
	rates[0] = silkDIV32VarQ(totalRateBps, (13<<16)+frac3Q16, 16+3)
	if rates[0] < minMidRateBps {
		// Mid bitrate below minimum: reduce stereo width
		rates[0] = minMidRateBps
		rates[1] = totalRateBps - rates[0]
		// width = 4 * (2 * side_rate - min_rate) / ((1 + 3 * frac) * min_rate)
		widthQ14 = silkDIV32VarQ(rates[1]<<1-minMidRateBps,
			silkSMULWB((1<<16)+frac3Q16, int16(minMidRateBps)), 14+2)
		widthQ14 = clampInt32(widthQ14, 0, 1<<14)
	} else {
		rates[1] = totalRateBps - rates[0]
		widthQ14 = 1 << 14
	}

	// Smoother
	s.smthWidthQ14 = int16(silkSMLAWB(int32(s.smthWidthQ14), widthQ14-int32(s.smthWidthQ14), int16(smoothCoefQ16)))

	// At very low bitrates or for inputs that are nearly amplitude panned,
	// switch to panned-mono coding
	res := stereoLRToMSResult{midSideRates: rates}
	scaleDown := func() {
		predQ13[0] = silkSMULBB(int32(s.smthWidthQ14), predQ13[0]) >> 14
		predQ13[1] = silkSMULBB(int32(s.smthWidthQ14), predQ13[1]) >> 14
	}
	switch {
	case toMono:
		// Last frame before stereo->mono transition; collapse stereo width
		widthQ14 = 0
		predQ13 = [2]int32{}
		res.ix = silkStereoQuantPred(&predQ13)
	case s.widthPrevQ14 == 0 &&
		(8*totalRateBps < 13*minMidRateBps || silkSMULWB(fracQ16, s.smthWidthQ14) < 819): // SILK_FIX_CONST(0.05, 14)
		// Code as panned-mono; previous frame already had zero width
		scaleDown()
		res.ix = silkStereoQuantPred(&predQ13)
		widthQ14 = 0
		predQ13 = [2]int32{}
		res.midSideRates = [2]int32{totalRateBps, 0}
		res.midOnly = true
	case s.widthPrevQ14 != 0 &&
		(8*totalRateBps < 11*minMidRateBps || silkSMULWB(fracQ16, s.smthWidthQ14) < 328): // SILK_FIX_CONST(0.02, 14)
		// Transition to zero-width stereo
		scaleDown()
		res.ix = silkStereoQuantPred(&predQ13)
		widthQ14 = 0
		predQ13 = [2]int32{}
	case s.smthWidthQ14 > 15565: // SILK_FIX_CONST(0.95, 14)
		// Full-width stereo coding
		res.ix = silkStereoQuantPred(&predQ13)
		widthQ14 = 1 << 14
	default:
		// Reduced-width stereo coding; scale down and quantize predictors
		scaleDown()
		res.ix = silkStereoQuantPred(&predQ13)
		widthQ14 = int32(s.smthWidthQ14)
	}

	// Make sure to keep on encoding until the tapered output has been
	// transmitted
	if res.midOnly {
		s.silentSideLen += int16(frameLength - stereoInterpLenMs*fsKHz)
		if int(s.silentSideLen) < silkLAShapeMs*fsKHz {
			res.midOnly = false
		} else {
			s.silentSideLen = 10000 // limit to avoid wrapping around
		}
	} else {
		s.silentSideLen = 0
	}

	if !res.midOnly && res.midSideRates[1] < 1 {
		res.midSideRates[1] = 1
		res.midSideRates[0] = totalRateBps - res.midSideRates[1]
		if res.midSideRates[0] < 1 {
			res.midSideRates[0] = 1
		}
	}

	// Interpolate predictors and subtract prediction from side channel
	out := make([]int16, frameLength)
	pred0Q13 := -int32(s.predPrevQ13[0])
	pred1Q13 := -int32(s.predPrevQ13[1])
	wQ24 := int32(s.widthPrevQ14) << 10
	denomQ16 := int32((1 << 16) / (stereoInterpLenMs * fsKHz))
	delta0Q13 := -silkRShiftRound(int64(silkSMULBB(predQ13[0]-int32(s.predPrevQ13[0]), denomQ16)), 16)
	delta1Q13 := -silkRShiftRound(int64(silkSMULBB(predQ13[1]-int32(s.predPrevQ13[1]), denomQ16)), 16)
	deltawQ24 := silkSMULWB(widthQ14-int32(s.widthPrevQ14), int16(denomQ16)) << 10
	interpLen := stereoInterpLenMs * fsKHz
	for n := 0; n < interpLen; n++ {
		pred0Q13 += delta0Q13
		pred1Q13 += delta1Q13
		wQ24 += deltawQ24
		sum := (int32(mid[n]) + int32(mid[n+2]) + int32(mid[n+1])<<1) << 9  // Q11
		sum = silkSMLAWB(silkSMULWB(wQ24, side[n+1]), sum, int16(pred0Q13)) // Q8
		sum = silkSMLAWB(sum, int32(mid[n+1])<<11, int16(pred1Q13))         // Q8
		out[n] = silkSAT16(silkRShiftRound(int64(sum), 8))
	}
	pred0Q13 = -predQ13[0]
	pred1Q13 = -predQ13[1]
	wQ24 = widthQ14 << 10
	for n := interpLen; n < frameLength; n++ {
		sum := (int32(mid[n]) + int32(mid[n+2]) + int32(mid[n+1])<<1) << 9  // Q11
		sum = silkSMLAWB(silkSMULWB(wQ24, side[n+1]), sum, int16(pred0Q13)) // Q8
		sum = silkSMLAWB(sum, int32(mid[n+1])<<11, int16(pred1Q13))         // Q8
		out[n] = silkSAT16(silkRShiftRound(int64(sum), 8))
	}
	s.predPrevQ13[0] = int16(predQ13[0])
	s.predPrevQ13[1] = int16(predQ13[1])
	s.widthPrevQ14 = int16(widthQ14)

	// The coded frames start at inputBuf + 1: mid[1 : frameLength+1] and the
	// side residual (x2[n-1] for n in [0, frameLength)).
	res.mid = mid[1 : frameLength+1]
	res.side = out
	return res
}

// silkStereoFindPredictor ports silk_stereo_find_predictor: the least-squares
// prediction gain of y from x in Q13, updating the smoothed mid/residual
// norms midResAmpQ0[0:2] and returning the residual/mid ratio in Q14.
func silkStereoFindPredictor(ratioQ14 *int32, x, y []int16, midResAmpQ0 []int32, length int, smoothCoefQ16 int32) int32 {
	nrgx, scale1 := silkSumSqrShift(x[:length])
	nrgy, scale2 := silkSumSqrShift(y[:length])
	scale := scale1
	if scale2 > scale {
		scale = scale2
	}
	scale += scale & 1 // make even
	nrgy >>= uint(scale - scale2)
	nrgx >>= uint(scale - scale1)
	if nrgx < 1 {
		nrgx = 1
	}
	var corr int32
	for i := 0; i < length; i++ {
		corr += silkSMULBB(int32(x[i]), int32(y[i])) >> uint(scale)
	}
	predQ13 := silkDIV32VarQ(corr, nrgx, 13)
	predQ13 = clampInt32(predQ13, -(1 << 14), 1<<14)
	pred2Q10 := silkSMULWB(predQ13, int16(predQ13))

	// Faster update for signals with large prediction parameters
	if abs32(pred2Q10) > smoothCoefQ16 {
		smoothCoefQ16 = abs32(pred2Q10)
	}

	// Smoothed mid and residual norms
	scale >>= 1
	midResAmpQ0[0] = silkSMLAWB(midResAmpQ0[0], silkSqrtApprox(nrgx)<<uint(scale)-midResAmpQ0[0], int16(smoothCoefQ16))
	// Residual energy = nrgy - 2 * pred * corr + pred^2 * nrgx
	nrgy -= silkSMULWB(corr, int16(predQ13)) << (3 + 1)
	nrgy += silkSMULWB(nrgx, int16(pred2Q10)) << 6
	midResAmpQ0[1] = silkSMLAWB(midResAmpQ0[1], silkSqrtApprox(nrgy)<<uint(scale)-midResAmpQ0[1], int16(smoothCoefQ16))

	// Ratio of smoothed residual and mid norms
	den := midResAmpQ0[0]
	if den < 1 {
		den = 1
	}
	*ratioQ14 = clampInt32(silkDIV32VarQ(midResAmpQ0[1], den, 14), 0, 32767)
	return predQ13
}

// silkStereoQuantPred ports silk_stereo_quant_pred: predQ13 is quantised in
// place (with the second predictor subtracted from the first afterwards) and
// the codebook indices are returned.
func silkStereoQuantPred(predQ13 *[2]int32) [2][3]int8 {
	var ix [2][3]int8
	for n := 0; n < 2; n++ {
		errMin := int32(1<<31 - 1)
		quantPred := int32(0)
	search:
		for i := 0; i < len(silkStereoPredQuantQ13)-1; i++ {
			lowQ13 := int32(silkStereoPredQuantQ13[i])
			stepQ13 := silkSMULWB(int32(silkStereoPredQuantQ13[i+1])-lowQ13, stereoQuantSubStepQ16)
			for j := 0; j < 5; j++ {
				lvlQ13 := silkSMLABB(lowQ13, stepQ13, int32(2*j+1))
				errQ13 := abs32(predQ13[n] - lvlQ13)
				if errQ13 < errMin {
					errMin = errQ13
					quantPred = lvlQ13
					ix[n][0] = int8(i)
					ix[n][1] = int8(j)
				} else {
					// Error increasing, so we're past the optimum
					break search
				}
			}
		}
		ix[n][2] = ix[n][0] / 3
		ix[n][0] -= ix[n][2] * 3
		predQ13[n] = quantPred
	}
	// Subtract second from first predictor (helps when actually applying these)
	predQ13[0] -= predQ13[1]
	return ix
}

func abs32(v int32) int32 {
	if v < 0 {
		return -v
	}
	return v
}

func clampInt32(v, lo, hi int32) int32 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
