package silk

import "math"

// find_lpc.go — Linear Prediction Coefficients analysis and NLSF interpolation
// Ported from libopus silk/float/find_LPC_FLP.c, silk/interpolate.c, and
// silk/float/LPC_analysis_filter_FLP.c.

// silkInterpolate ports libopus silk_interpolate: computes
// xi[i] = x0[i] + ((x1[i] - x0[i]) * ifactQ2) >> 2.
func silkInterpolate(xi, x0, x1 []int16, ifactQ2, d int) {
	for i := 0; i < d; i++ {
		xi[i] = x0[i] + int16((int32(x1[i]-x0[i])*int32(ifactQ2))>>2)
	}
}

// silkNLSF2AFLP ports libopus silk_NLSF2A_FLP: converts NLSF Q15 to float LPC
// coefficients in monic whitening form (a[0..order-1]).
func silkNLSF2AFLP(pAR []float64, nlsfQ15 []int16, order int) {
	aQ12 := nlsfToLPCLibopus(nlsfQ15, order)
	for i := 0; i < order; i++ {
		pAR[i] = float64(aQ12[i]) / 4096.0
	}
}

// silkFloatToQ16 converts float64 LPC coefficients to Q16 int32 for silkA2NLSF.
func silkFloatToQ16(a []float64) []int32 {
	q16 := make([]int32, len(a))
	for i, v := range a {
		q16[i] = silkFloat2Int(v * 65536.0)
	}
	return q16
}

// silkFindLPCFLP ports libopus silk_find_LPC_FLP. It computes Burg LPC analysis
// over x (the LPC_in_pre buffer of nbSubfr subframes, each of length subfrLength =
// subfr_len + order), searches over NLSF interpolation indices (3..0) using the
// first-half residual energy, and returns the target NLSFs in Q15 and the
// chosen interpolation factor (0..4, where 4 = no interpolation).
func silkFindLPCFLP(
	x []float64,
	minInvGain float64,
	subfrLength, nbSubfr, order int,
	useInterpolatedNLSFs, firstFrameAfterReset bool,
	prevNLSFQ15 []int16,
) ([]int16, int) {
	interpFactor := 4
	if order <= 0 || subfrLength <= order || nbSubfr <= 0 || len(x) < subfrLength*nbSubfr {
		return make([]int16, order), 4
	}
	// Burg AR analysis for the full frame
	a, resNrg := silkBurgModifiedFLP(x[:subfrLength*nbSubfr], minInvGain, subfrLength, nbSubfr, order)
	targetNLSFQ15 := make([]int16, order)

	if useInterpolatedNLSFs && !firstFrameAfterReset && nbSubfr == 4 && len(prevNLSFQ15) == order {
		// Optimal solution for last 10 ms (subframes 2 and 3); subtract residual energy here
		halfSubfr := 2
		secondStart := halfSubfr * subfrLength
		aTmp, secondNrg := silkBurgModifiedFLP(x[secondStart:subfrLength*nbSubfr], minInvGain, subfrLength, halfSubfr, order)
		resNrg = float64(float32(resNrg - secondNrg))

		// Convert last-half AR to NLSFs
		silkA2NLSF(targetNLSFQ15, silkFloatToQ16(aTmp), order)

		// Search over interpolation indices to find the one with lowest residual energy
		resNrg2nd := float64(math.MaxFloat32)
		nlsf0Q15 := make([]int16, order)
		aInterp := make([]float64, order)
		lpcRes := make([]float64, 2*subfrLength)

		for k := 3; k >= 0; k-- {
			// Interpolate NLSFs for first half
			silkInterpolate(nlsf0Q15, prevNLSFQ15, targetNLSFQ15, k, order)

			// Convert to LPC for residual energy evaluation
			silkNLSF2AFLP(aInterp, nlsf0Q15, order)

			// Calculate residual energy with LSF interpolation
			silkLPCAnalysisFilterFLP32(lpcRes, aInterp, x, 2*subfrLength, order)
			resNrgInterp := float64(float32(silkEnergyFLP32(lpcRes[order:subfrLength]) +
				silkEnergyFLP32(lpcRes[subfrLength+order:2*subfrLength])))

			// Determine whether current interpolated NLSFs are best so far
			if resNrgInterp < resNrg {
				resNrg = resNrgInterp
				interpFactor = k
			} else if resNrgInterp > resNrg2nd {
				// No reason to continue iterating - residual energies will continue to climb
				break
			}
			resNrg2nd = resNrgInterp
		}
	}

	if interpFactor == 4 {
		// NLSF interpolation is currently inactive, calculate NLSFs from full frame AR coefficients
		silkA2NLSF(targetNLSFQ15, silkFloatToQ16(a), order)
	}

	return targetNLSFQ15, interpFactor
}

func silkLPCAnalysisFilterFLP32(r, predCoef, s []float64, length, order int) {
	for ix := 0; ix < order && ix < length; ix++ {
		r[ix] = 0
	}
	for ix := order; ix < length; ix++ {
		pred := float32(0)
		for k := 0; k < order; k++ {
			pred += float32(s[ix-1-k]) * float32(predCoef[k])
		}
		r[ix] = float64(float32(s[ix]) - pred)
	}
}
