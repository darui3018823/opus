package silk

import "math"

// Burg-method LPC analysis and accurate A→NLSF conversion.
//
// This is a direct port of libopus silk/float/burg_modified_FLP.c and
// silk/A2NLSF.c. It replaces the earlier autocorrelation+Levinson LPC and the
// quantile-based A2NLSF approximation used by the SILK encoder's NLSF target.

// FIND_LPC_COND_FAC from libopus silk/tuning_parameters.h.
const findLPCCondFac = 1e-5

// silkBurgModifiedFLP ports silk_burg_modified_FLP. It computes prediction
// coefficients A (length order, monic-implied: A(z) = 1 - sum A[k] z^-(k+1))
// from nbSubfr stacked subframes of subfrLength samples each, bounding the
// prediction gain via minInvGain. It returns A and the residual energy.
func silkBurgModifiedFLP(x []float64, minInvGain float64, subfrLength, nbSubfr, order int) ([]float64, float64) {
	D := order
	A := make([]float64, D)
	CFirstRow := make([]float64, D)
	CLastRow := make([]float64, D)
	CAf := make([]float64, D+1)
	CAb := make([]float64, D+1)
	Af := make([]float64, D)

	// Compute autocorrelations, added over subframes.
	C0 := silkEnergyFLP(x[:nbSubfr*subfrLength])
	for s := 0; s < nbSubfr; s++ {
		xp := x[s*subfrLength:]
		for n := 1; n < D+1; n++ {
			sum := 0.0
			for i := 0; i < subfrLength-n; i++ {
				sum += xp[i] * xp[i+n]
			}
			CFirstRow[n-1] += sum
		}
	}
	copy(CLastRow, CFirstRow)

	CAb[0] = C0 + findLPCCondFac*C0 + 1e-9
	CAf[0] = CAb[0]
	invGain := 1.0
	reachedMaxGain := false

	for n := 0; n < D; n++ {
		// Update correlation rows and C*Af / C*flipud(Af).
		for s := 0; s < nbSubfr; s++ {
			xp := x[s*subfrLength:]
			tmp1 := xp[n]
			tmp2 := xp[subfrLength-n-1]
			for k := 0; k < n; k++ {
				CFirstRow[k] -= xp[n] * xp[n-k-1]
				CLastRow[k] -= xp[subfrLength-n-1] * xp[subfrLength-n+k]
				atmp := Af[k]
				tmp1 += xp[n-k-1] * atmp
				tmp2 += xp[subfrLength-n+k] * atmp
			}
			for k := 0; k <= n; k++ {
				CAf[k] -= tmp1 * xp[n-k]
				CAb[k] -= tmp2 * xp[subfrLength-n+k-1]
			}
		}
		tmp1 := CFirstRow[n]
		tmp2 := CLastRow[n]
		for k := 0; k < n; k++ {
			atmp := Af[k]
			tmp1 += CLastRow[n-k-1] * atmp
			tmp2 += CFirstRow[n-k-1] * atmp
		}
		CAf[n+1] = tmp1
		CAb[n+1] = tmp2

		// Nominator and denominator for the next reflection coefficient.
		num := CAb[n+1]
		nrgB := CAb[0]
		nrgF := CAf[0]
		for k := 0; k < n; k++ {
			atmp := Af[k]
			num += CAb[n-k] * atmp
			nrgB += CAb[k+1] * atmp
			nrgF += CAf[k+1] * atmp
		}

		rc := -2.0 * num / (nrgF + nrgB)

		// Bound the inverse prediction gain.
		t := invGain * (1.0 - rc*rc)
		if t <= minInvGain {
			rc = math.Sqrt(1.0 - minInvGain/invGain)
			if num > 0 {
				rc = -rc
			}
			invGain = minInvGain
			reachedMaxGain = true
		} else {
			invGain = t
		}

		// Update the AR coefficients.
		for k := 0; k < (n+1)>>1; k++ {
			t1 := Af[k]
			t2 := Af[n-k-1]
			Af[k] = t1 + rc*t2
			Af[n-k-1] = t2 + rc*t1
		}
		Af[n] = rc

		if reachedMaxGain {
			for k := n + 1; k < D; k++ {
				Af[k] = 0.0
			}
			break
		}

		// Update C*Af and C*Ab.
		for k := 0; k <= n+1; k++ {
			t1 := CAf[k]
			CAf[k] += rc * CAb[n-k+1]
			CAb[n-k+1] += rc * t1
		}
	}

	var nrgF float64
	if reachedMaxGain {
		for k := 0; k < D; k++ {
			A[k] = -Af[k]
		}
		for s := 0; s < nbSubfr; s++ {
			C0 -= silkEnergyFLP(x[s*subfrLength : s*subfrLength+D])
		}
		nrgF = C0 * invGain
	} else {
		nrgF = CAf[0]
		tmp1 := 1.0
		for k := 0; k < D; k++ {
			atmp := Af[k]
			nrgF += CAf[k+1] * atmp
			tmp1 += atmp * atmp
			A[k] = -atmp
		}
		nrgF -= findLPCCondFac * C0 * tmp1
	}
	return A, nrgF
}

// A2NLSF fixed-point constants from libopus silk/A2NLSF.c and silk/define.h.
const (
	binDivStepsA2NLSF   = 3   // BIN_DIV_STEPS_A2NLSF_FIX
	maxIterationsA2NLSF = 16  // MAX_ITERATIONS_A2NLSF_FIX
	lsfCosTabSz         = 128 // LSF_COS_TAB_SZ_FIX
)

// silkA2NLSFTransPoly ports silk_A2NLSF_trans_poly: transforms a polynomial
// from cos(n*f) to cos(f)^n.
func silkA2NLSFTransPoly(p []int32, dd int) {
	for k := 2; k <= dd; k++ {
		for n := dd; n > k; n-- {
			p[n-2] -= p[n]
		}
		p[k-2] -= p[k] << 1
	}
}

// silkA2NLSFEvalPoly ports silk_A2NLSF_eval_poly (returns Q16).
func silkA2NLSFEvalPoly(p []int32, x int32, dd int) int32 {
	y32 := p[dd] // Q16
	xQ16 := x << 4
	for n := dd - 1; n >= 0; n-- {
		y32 = silkSMLAWW(p[n], y32, xQ16)
	}
	return y32
}

// silkA2NLSFInit ports silk_A2NLSF_init: builds the even/odd polynomials P, Q
// in the cos(f)^n basis from the Q16 filter coefficients.
func silkA2NLSFInit(aQ16, P, Q []int32, dd int) {
	P[dd] = 1 << 16
	Q[dd] = 1 << 16
	for k := 0; k < dd; k++ {
		P[k] = -aQ16[dd-k-1] - aQ16[dd+k]
		Q[k] = -aQ16[dd-k-1] + aQ16[dd+k]
	}
	// Divide out the known roots: z = -1 in P, z = 1 in Q.
	for k := dd; k > 0; k-- {
		P[k-1] -= P[k]
		Q[k-1] += Q[k]
	}
	silkA2NLSFTransPoly(P, dd)
	silkA2NLSFTransPoly(Q, dd)
}

// silkA2NLSF ports silk_A2NLSF: computes NLSFs in Q15 from monic whitening
// filter coefficients in Q16. aQ16 is bandwidth-expanded in place until all
// roots are found. The order d must be even.
func silkA2NLSF(NLSF []int16, aQ16 []int32, d int) {
	dd := d >> 1
	P := make([]int32, dd+1)
	Q := make([]int32, dd+1)
	PQ := [2][]int32{P, Q}

	silkA2NLSFInit(aQ16, P, Q, dd)

	p := P
	xlo := silkLSFCosTabFixQ12[0] // Q12
	ylo := silkA2NLSFEvalPoly(p, xlo, dd)

	var rootIx int
	if ylo < 0 {
		NLSF[0] = 0
		p = Q
		ylo = silkA2NLSFEvalPoly(p, xlo, dd)
		rootIx = 1
	}

	k := 1
	iter := 0
	var thr int32
	for {
		xhi := silkLSFCosTabFixQ12[k]
		yhi := silkA2NLSFEvalPoly(p, xhi, dd)

		if (ylo <= 0 && yhi >= thr) || (ylo >= 0 && yhi <= -thr) {
			if yhi == 0 {
				thr = 1
			} else {
				thr = 0
			}
			// Binary division.
			ffrac := int32(-256)
			for m := 0; m < binDivStepsA2NLSF; m++ {
				xmid := silkRShiftRound(int64(xlo+xhi), 1)
				ymid := silkA2NLSFEvalPoly(p, xmid, dd)
				if (ylo <= 0 && ymid >= 0) || (ylo >= 0 && ymid <= 0) {
					xhi = xmid
					yhi = ymid
				} else {
					xlo = xmid
					ylo = ymid
					ffrac += 128 >> uint(m)
				}
			}
			// Interpolate.
			if silkAbs32(ylo) < 65536 {
				den := ylo - yhi
				nom := (ylo << (8 - binDivStepsA2NLSF)) + (den >> 1)
				if den != 0 {
					ffrac += nom / den
				}
			} else {
				ffrac += ylo / ((ylo - yhi) >> (8 - binDivStepsA2NLSF))
			}
			val := (int32(k) << 8) + ffrac
			if val > math.MaxInt16 {
				val = math.MaxInt16
			}
			NLSF[rootIx] = int16(val)

			rootIx++
			if rootIx >= d {
				return // Found all roots.
			}
			p = PQ[rootIx&1]
			xlo = silkLSFCosTabFixQ12[k-1]
			ylo = int32(1-(rootIx&2)) << 12
		} else {
			k++
			xlo = xhi
			ylo = yhi
			thr = 0

			if k > lsfCosTabSz {
				iter++
				if iter > maxIterationsA2NLSF {
					// Set NLSFs to white spectrum and exit.
					NLSF[0] = int16((1 << 15) / (d + 1))
					for k = 1; k < d; k++ {
						NLSF[k] = NLSF[k-1] + NLSF[0]
					}
					return
				}
				// Apply progressively more bandwidth expansion and retry.
				silkBWExpander32(aQ16, d, 65536-(1<<uint(iter)))
				silkA2NLSFInit(aQ16, P, Q, dd)
				p = P
				xlo = silkLSFCosTabFixQ12[0]
				ylo = silkA2NLSFEvalPoly(p, xlo, dd)
				if ylo < 0 {
					NLSF[0] = 0
					p = Q
					ylo = silkA2NLSFEvalPoly(p, xlo, dd)
					rootIx = 1
				} else {
					rootIx = 0
				}
				k = 1
			}
		}
	}
}

// silkA2NLSFFLP ports silk_A2NLSF_FLP: converts float LPC coefficients to Q15
// NLSFs by quantizing to Q16 and running the fixed-point root finder.
func silkA2NLSFFLP(pAR []float64, order int) []int16 {
	aFixQ16 := make([]int32, order)
	for i := 0; i < order; i++ {
		aFixQ16[i] = silkFloat2Int(pAR[i] * 65536.0)
	}
	NLSF := make([]int16, order)
	silkA2NLSF(NLSF, aFixQ16, order)
	return NLSF
}
