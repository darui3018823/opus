package silk

import "math"

const stereoInterpLenMs = 8

type stereoPredState struct {
	predPrevQ13  [2]int32
	mid          [2]int16
	side         [2]int16
	midSideAmpQ0 [4]int32
}

func (s *stereoPredState) reset() {
	*s = stereoPredState{}
}

func (s *stereoPredState) lrToMS(pcm []float64, fsKHz, frameLength int) ([]float64, []float64, [2][3]int8) {
	mid := make([]int16, frameLength+2)
	side := make([]int16, frameLength+2)
	copy(mid[:2], s.mid[:])
	copy(side[:2], s.side[:])
	for i := 0; i < frameLength; i++ {
		l := floatToInt16Sample(pcm[2*i])
		r := floatToInt16Sample(pcm[2*i+1])
		sum := int32(l) + int32(r)
		diff := int32(l) - int32(r)
		mid[i+2] = clamp16(silkRShiftRound(int64(sum), 1))
		side[i+2] = clamp16(silkRShiftRound(int64(diff), 1))
	}
	s.mid[0], s.mid[1] = mid[frameLength], mid[frameLength+1]
	s.side[0], s.side[1] = side[frameLength], side[frameLength+1]

	is10msFrame := frameLength == 10*fsKHz
	midAnalysis := make([]float64, frameLength+2)
	sideAnalysis := make([]float64, frameLength+2)
	for i := range midAnalysis {
		midAnalysis[i] = float64(mid[i])
		sideAnalysis[i] = float64(side[i])
	}
	foundQ13, _ := silkStereoFindPredictorFLP(midAnalysis, sideAnalysis, float64(stereoSmoothCoefQ16(is10msFrame))/(1<<16))
	// silkStereoFindPredictorFLP returns the decoder-form predictors
	// [LP-HP, HP]. silkStereoQuantPred takes the two codebook-domain values
	// [LP, HP], so undo that final transform before obtaining the indices.
	predQ13 := [2]int32{
		int32(foundQ13[0]) + int32(foundQ13[1]),
		int32(foundQ13[1]),
	}
	ix := silkStereoQuantPred(&predQ13)

	sideResidual := make([]int16, frameLength)
	pred0Q13 := -s.predPrevQ13[0]
	pred1Q13 := -s.predPrevQ13[1]
	interpLen := stereoInterpLenMs * fsKHz
	if interpLen > frameLength {
		interpLen = frameLength
	}
	denomQ16 := int32((1 << 16) / (stereoInterpLenMs * fsKHz))
	delta0Q13 := -silkRShiftRound(int64(predQ13[0]-s.predPrevQ13[0])*int64(denomQ16), 16)
	delta1Q13 := -silkRShiftRound(int64(predQ13[1]-s.predPrevQ13[1])*int64(denomQ16), 16)
	for i := 0; i < frameLength; i++ {
		if i < interpLen {
			pred0Q13 += delta0Q13
			pred1Q13 += delta1Q13
		} else {
			pred0Q13 = -predQ13[0]
			pred1Q13 = -predQ13[1]
		}
		sumQ11 := int64(int32(mid[i])+int32(mid[i+2])+(int32(mid[i+1])<<1)) << 9
		sumQ8 := int64(int32(side[i+1])) << 8
		sumQ8 += (sumQ11 * int64(pred0Q13)) >> 16
		sumQ8 += ((int64(int32(mid[i+1])) << 11) * int64(pred1Q13)) >> 16
		sideResidual[i] = clamp16(silkRShiftRound(sumQ8, 8))
	}
	s.predPrevQ13 = predQ13

	midFrame := make([]float64, frameLength)
	sideFrame := make([]float64, frameLength)
	for i := 0; i < frameLength; i++ {
		midFrame[i] = float64(mid[i+1]) / 32768.0
		sideFrame[i] = float64(sideResidual[i]) / 32768.0
	}
	return midFrame, sideFrame, ix
}

func stereoLPHP(x []int16, frameLength int) ([]int16, []int16) {
	lp := make([]int16, frameLength)
	hp := make([]int16, frameLength)
	for i := 0; i < frameLength; i++ {
		sum := int32(silkRShiftRound(int64(int32(x[i])+int32(x[i+2])+(int32(x[i+1])<<1)), 2))
		lp[i] = clamp16(sum)
		hp[i] = clamp16(int32(x[i+1]) - sum)
	}
	return lp, hp
}

func stereoSmoothCoefQ16(is10ms bool) int32 {
	coef := int32(math.Round(0.01 * 65536.0))
	if is10ms {
		coef >>= 1
	}
	return coef
}

// silkStereoFindPredictorFLP estimates the two predictors used by SILK stereo
// coding. The side signal is modelled from the same two mid-channel bases used
// by the decoder:
//
//	side[n] ~= pred[0] * LP(mid)[n] + pred[1] * mid[n]
//
// The 2x2 normal equations are solved through an LDLᵀ factorization. The
// returned predictors are quantized to the SILK stereo codebook and expressed
// in decoder form [LP-HP, HP], with scale == 1<<13.
func silkStereoFindPredictorFLP(mid, side []float64, smthCoef float64) (predQ13 [2]int16, scale int) {
	const q13Scale = 1 << 13
	scale = q13Scale

	n := len(mid)
	if len(side) < n {
		n = len(side)
	}
	if n == 0 {
		return predQ13, scale
	}

	var xx00, xx01, xx11, xy0, xy1 float64
	for i := 0; i < n; i++ {
		prev := mid[i]
		if i > 0 {
			prev = mid[i-1]
		}
		next := mid[i]
		if i+1 < n {
			next = mid[i+1]
		}
		lp := 0.25 * (prev + 2*mid[i] + next)
		center := mid[i]
		y := side[i]
		xx00 += lp * lp
		xx01 += lp * center
		xx11 += center * center
		xy0 += lp * y
		xy1 += center * y
	}

	// Keep the matrix positive definite for silence and nearly rank-one tonal
	// input. smthCoef is the frame smoothing coefficient used by SILK; using it
	// in the diagonal floor makes the estimate less jumpy when the available
	// correlation energy is weak without biasing normal active frames.
	if smthCoef < 0 {
		smthCoef = 0
	} else if smthCoef > 1 {
		smthCoef = 1
	}
	trace := xx00 + xx11
	reg := math.Max(1e-9, trace*(1e-8+1e-4*smthCoef))

	// LDLᵀ factorization of [[xx00, xx01], [xx01, xx11]] and solve.
	d0 := xx00 + reg
	l10 := xx01 / d0
	d1 := xx11 + reg - l10*xx01
	var w0, w1 float64
	if d1 > 1e-12*d0 {
		z0 := xy0 / d0
		z1 := (xy1 - l10*xy0) / d1
		w1 = z1
		w0 = z0 - l10*z1
	}

	// Convert from the decoder bases [LP, center] to the codebook-domain
	// [LP, HP], where HP=center-LP. Quantization performs the final
	// [LP-HP, HP] transform expected by stereo_MS_to_LR.
	codebookPred := [2]int32{
		int32(math.Round((w0 + w1) * q13Scale)),
		int32(math.Round(w1 * q13Scale)),
	}
	silkStereoQuantPred(&codebookPred)
	predQ13[0] = int16(codebookPred[0])
	predQ13[1] = int16(codebookPred[1])
	return predQ13, scale
}

// silkStereoFindPredictor estimates the least-squares predictor y ~= pred*x.
// The 64-bit accumulators are the quality-oriented equivalent of libopus'
// scaled fixed-point energy/correlation helpers; bit-exact fixed-point
// intermediate values are not required by this encoder.
func silkStereoFindPredictor(midSideAmpQ0 *[4]int32, ampOffset int, x, y []int16, length int, smoothCoefQ16 int32) int32 {
	var nrgX, nrgY, corr int64
	for i := 0; i < length; i++ {
		xi := int64(x[i])
		yi := int64(y[i])
		nrgX += xi * xi
		nrgY += yi * yi
		corr += xi * yi
	}
	if nrgX < 1 {
		nrgX = 1
	}

	predQ13 := int32(0)
	if corr >= 0 {
		predQ13 = int32(((corr << 13) + nrgX/2) / nrgX)
	} else {
		predQ13 = -int32((((-corr) << 13) + nrgX/2) / nrgX)
	}
	if predQ13 < -(1 << 14) {
		predQ13 = -(1 << 14)
	} else if predQ13 > 1<<14 {
		predQ13 = 1 << 14
	}
	pred2Q10 := int32((int64(predQ13) * int64(predQ13)) >> 16)
	if abs32(pred2Q10) > smoothCoefQ16 {
		smoothCoefQ16 = abs32(pred2Q10)
	}
	if smoothCoefQ16 > 32767 {
		smoothCoefQ16 = 32767
	}

	resNrg := nrgY - ((2 * int64(predQ13) * corr) >> 13) + ((int64(predQ13) * int64(predQ13) * nrgX) >> 26)
	if resNrg < 0 {
		resNrg = 0
	}
	midAmp := int32(math.Round(math.Sqrt(float64(nrgX))))
	resAmp := int32(math.Round(math.Sqrt(float64(resNrg))))
	midSideAmpQ0[ampOffset] += int32((int64(midAmp-midSideAmpQ0[ampOffset]) * int64(smoothCoefQ16)) >> 16)
	midSideAmpQ0[ampOffset+1] += int32((int64(resAmp-midSideAmpQ0[ampOffset+1]) * int64(smoothCoefQ16)) >> 16)
	return predQ13
}

func silkStereoQuantPred(predQ13 *[2]int32) [2][3]int8 {
	var ix [2][3]int8
	for n := 0; n < 2; n++ {
		errMin := int32(math.MaxInt32)
		quantPred := int32(0)
	search:
		for i := 0; i < len(silkStereoPredQuantQ13)-1; i++ {
			lowQ13 := int32(silkStereoPredQuantQ13[i])
			stepQ13 := int32((int64(int32(silkStereoPredQuantQ13[i+1])-lowQ13) * 6554) >> 16)
			for j := 0; j < 5; j++ {
				lvlQ13 := lowQ13 + stepQ13*int32(2*j+1)
				errQ13 := abs32(predQ13[n] - lvlQ13)
				if errQ13 < errMin {
					errMin = errQ13
					quantPred = lvlQ13
					ix[n][0] = int8(i)
					ix[n][1] = int8(j)
				} else {
					break search
				}
			}
		}
		ix[n][2] = ix[n][0] / 3
		ix[n][0] -= ix[n][2] * 3
		predQ13[n] = quantPred
	}
	predQ13[0] -= predQ13[1]
	return ix
}

func abs32(v int32) int32 {
	if v < 0 {
		return -v
	}
	return v
}
