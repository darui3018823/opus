package silk

import "math"

// LTP gain quantisation (silk_find_LTP_FLP + silk_quant_LTP_gains / VQ_WMat_EC).
//
// The earlier homebrew selectLTPGain matched only the centre tap to the pitch
// gain, leaving a large long-term residual on voiced frames. That residual is
// what pins steady-voiced frames to the full bitrate budget (the natural pulse
// output already exceeds budget, so rate control throttles to fill it). Porting
// the real weighted-VQ search shrinks the residual, so the natural coding drops
// below budget and the byte count falls toward libopus's.

const (
	ltpOrder        = 5
	ltpCorrInvMax   = 0.03 // LTP_CORR_INV_MAX
	ltpGainSafetyQ7 = 51.0 // SILK_FIX_CONST(0.4, 7)
	// SILK_FIX_CONST(MAX_SUM_LOG_GAIN_DB/6, 7), MAX_SUM_LOG_GAIN_DB=250.
	maxSumLogGainConst = 5333.0
)

// ltpGainCodebook returns the per-vector taps for one of the three LTP gain
// codebooks (mirrors silk_LTP_vq_ptrs_Q7).
func ltpGainCodebook(perIdx int) [][5]int8 {
	switch perIdx {
	case 0:
		return silkLTPGainVQ0[:]
	case 1:
		return silkLTPGainVQ1[:]
	default:
		return silkLTPGainVQ2[:]
	}
}

// energyRange returns sum_{i=0}^{n-1} res[off+i]^2, treating out-of-range
// indices as zero so callers need not reserve look-ahead past the frame.
func energyRange(res []float64, off, n int) float64 {
	sum := 0.0
	for i := 0; i < n; i++ {
		idx := off + i
		if idx < 0 || idx >= len(res) {
			continue
		}
		sum += res[idx] * res[idx]
	}
	return sum
}

// innerProdRange returns sum_{i=0}^{n-1} res[a+i]*res[b+i] with out-of-range
// indices treated as zero.
func innerProdRange(res []float64, a, b, n int) float64 {
	sum := 0.0
	for i := 0; i < n; i++ {
		ai, bi := a+i, b+i
		if ai < 0 || ai >= len(res) || bi < 0 || bi >= len(res) {
			continue
		}
		sum += res[ai] * res[bi]
	}
	return sum
}

// corrMatrixLTP builds the LTP_ORDER×LTP_ORDER correlation matrix X'X for the
// lagged residual starting at base (silk_corrMatrix_FLP, Order=ltpOrder).
func corrMatrixLTP(res []float64, base, length int) [ltpOrder * ltpOrder]float64 {
	var XX [ltpOrder * ltpOrder]float64
	p1 := base + ltpOrder - 1
	energy := energyRange(res, p1, length)
	XX[0] = energy
	for j := 1; j < ltpOrder; j++ {
		energy += sq(res, p1-j) - sq(res, p1+length-j)
		XX[j*ltpOrder+j] = energy
	}
	p2 := base + ltpOrder - 2
	for lag := 1; lag < ltpOrder; lag++ {
		energy = innerProdRange(res, p1, p2, length)
		XX[lag*ltpOrder+0] = energy
		XX[0*ltpOrder+lag] = energy
		for j := 1; j < ltpOrder-lag; j++ {
			energy += prod(res, p1-j, p2-j) - prod(res, p1+length-j, p2+length-j)
			XX[(lag+j)*ltpOrder+j] = energy
			XX[j*ltpOrder+(lag+j)] = energy
		}
		p2--
	}
	return XX
}

// corrVectorLTP builds the X'*target correlation vector (silk_corrVector_FLP).
func corrVectorLTP(res []float64, base, target, length int) [ltpOrder]float64 {
	var xX [ltpOrder]float64
	p1 := base + ltpOrder - 1
	for lag := 0; lag < ltpOrder; lag++ {
		xX[lag] = innerProdRange(res, p1, target, length)
		p1--
	}
	return xX
}

func sq(res []float64, i int) float64 {
	if i < 0 || i >= len(res) {
		return 0
	}
	return res[i] * res[i]
}

func prod(res []float64, a, b int) float64 {
	if a < 0 || a >= len(res) || b < 0 || b >= len(res) {
		return 0
	}
	return res[a] * res[b]
}

// findLTP computes the normalised per-subframe correlation matrix/vector pairs
// used by the gain VQ (silk_find_LTP_FLP). res is the short-term (LPC) residual
// in the int16 magnitude domain; frameStart indexes the first frame sample;
// lags are the per-subframe pitch lags.
func (e *Encoder) findLTP(res []float64, frameStart int, lags []int, subfrLen int) ([][ltpOrder * ltpOrder]float64, [][ltpOrder]float64) {
	XX := make([][ltpOrder * ltpOrder]float64, e.nSubframes)
	xX := make([][ltpOrder]float64, e.nSubframes)
	for k := 0; k < e.nSubframes; k++ {
		rPtr := frameStart + k*subfrLen
		lagBase := rPtr - (lags[k] + ltpOrder/2)
		m := corrMatrixLTP(res, lagBase, subfrLen)
		v := corrVectorLTP(res, lagBase, rPtr, subfrLen)
		xx := energyRange(res, rPtr, subfrLen+ltpOrder)
		denom := xx
		floor := ltpCorrInvMax*0.5*(m[0]+m[ltpOrder*ltpOrder-1]) + 1.0
		if floor > denom {
			denom = floor
		}
		temp := 1.0 / denom
		for i := range m {
			m[i] *= temp
		}
		for i := range v {
			v[i] *= temp
		}
		XX[k] = m
		xX[k] = v
	}
	return XX, xX
}

// vqWMatLTP runs the weighted rate-distortion VQ search over one codebook for a
// single subframe (silk_VQ_WMat_EC, float). It returns the best index, the
// normalised residual energy (+gain penalty), the rate-distortion cost, and the
// effective gain of the winner.
func vqWMatLTP(XX [ltpOrder * ltpOrder]float64, xX [ltpOrder]float64, perIdx, subfrLen int, maxGainQ7 float64) (ind int, resNrg, rateDist, gainQ7 float64) {
	cb := ltpGainCodebook(perIdx)
	bits := silkLTPGainBITSQ5Codebooks[perIdx]
	gains := silkLTPGainVQGainCodebooks[perIdx]
	rateDist = math.Inf(1)
	resNrg = math.Inf(1)
	for k := range cb {
		var b [ltpOrder]float64
		for i := 0; i < ltpOrder; i++ {
			b[i] = float64(cb[k][i]) / 128.0 // Q7 -> actual tap
		}
		// Quantization error: 1.001 - 2 * xX·b + b'·XX·b.
		sum1 := 1.001
		for i := 0; i < ltpOrder; i++ {
			sum1 -= 2.0 * b[i] * xX[i]
			for j := 0; j < ltpOrder; j++ {
				sum1 += b[i] * XX[i*ltpOrder+j] * b[j]
			}
		}
		if sum1 < 0 {
			continue
		}
		g := float64(gains[k])
		penalty := 0.0
		if g > maxGainQ7 {
			penalty = (g - maxGainQ7) / 16.0
		}
		// bits ≈ subfr_len * 128 * log2(residual fraction), plus half the code
		// length (silk's "-1" shift on cl_Q5<<(3-1)).
		bitsRes := float64(subfrLen) * 128.0 * math.Log2(sum1+penalty)
		bitsTot := bitsRes + float64(bits[k])*4.0
		if bitsTot <= rateDist {
			rateDist = bitsTot
			resNrg = sum1 + penalty
			ind = k
			gainQ7 = g
		}
	}
	return ind, resNrg, rateDist, gainQ7
}

// quantLTPGains chooses the periodicity codebook and per-subframe gain indices
// that minimise the total weighted rate-distortion (silk_quant_LTP_gains), and
// returns the resulting Q14 taps plus the LTP prediction coding gain in dB. The
// cumulative sum_log_gain state limits the total prediction gain across
// subframes for stability.
func (e *Encoder) quantLTPGains(XX [][ltpOrder * ltpOrder]float64, xX [][ltpOrder]float64, subfrLen int) (perIdx int, gainIndices []int, predGainDB float64) {
	bestRateDist := math.Inf(1)
	bestPer := 0
	bestIndices := make([]int, e.nSubframes)
	bestResNrg := 0.0
	bestSumLogGain := 0.0

	for k := 0; k < 3; k++ {
		indices := make([]int, e.nSubframes)
		totalRateDist := 0.0
		totalResNrg := 0.0
		sumLogGain := e.ltpSumLogGainQ7
		for j := 0; j < e.nSubframes; j++ {
			maxGainQ7 := math.Exp2((maxSumLogGainConst-sumLogGain+896.0)/128.0) - ltpGainSafetyQ7
			ind, resNrg, rateDist, gainQ7 := vqWMatLTP(XX[j], xX[j], k, subfrLen, maxGainQ7)
			indices[j] = ind
			totalRateDist += rateDist
			totalResNrg += resNrg
			next := sumLogGain + 128.0*math.Log2(ltpGainSafetyQ7+gainQ7) - 896.0
			if next < 0 {
				next = 0
			}
			sumLogGain = next
		}
		if totalRateDist <= bestRateDist {
			bestRateDist = totalRateDist
			bestPer = k
			copy(bestIndices, indices)
			bestResNrg = totalResNrg
			bestSumLogGain = sumLogGain
		}
	}

	e.ltpSumLogGainQ7 = bestSumLogGain

	// Average normalised residual energy -> pred_gain_dB = -3*log2(res_nrg).
	avgResNrg := bestResNrg / float64(e.nSubframes)
	if avgResNrg < 1e-9 {
		avgResNrg = 1e-9
	}
	if avgResNrg > 1.0 {
		avgResNrg = 1.0
	}
	predGainDB = -3.0 * math.Log2(avgResNrg)

	return bestPer, bestIndices, predGainDB
}

// selectLTPGainsVQ runs the full LTP gain quantizer for one voiced frame:
// LPC residual -> find_LTP correlations -> weighted-VQ codebook search. It
// returns the chosen periodicity index, the per-subframe gain indices, and the
// resulting Q14 taps. Replaces the centre-tap-only homebrew selectLTPGain.
func (e *Encoder) selectLTPGainsVQWithGain(signal []float64, lpcQ12 []int16, pitchLags []int) (perIdx int, gainIndices []int, ltpCoeffsQ14 [][5]int16, predGainDB float64) {
	subfrLen := e.frameSize / e.nSubframes
	res, frameStart := e.lpcResidualInt16Domain(signal, lpcQ12)
	XX, xX := e.findLTP(res, frameStart, pitchLags, subfrLen)
	perIdx, gainIndices, predGainDB = e.quantLTPGains(XX, xX, subfrLen)
	ltpCoeffsQ14 = ltpCoeffsForPerSubframe(perIdx, gainIndices)
	return
}

func (e *Encoder) selectLTPGainsVQ(signal []float64, lpcQ12 []int16, pitchLags []int) (perIdx int, gainIndices []int, ltpCoeffsQ14 [][5]int16) {
	perIdx, gainIndices, ltpCoeffsQ14, _ = e.selectLTPGainsVQWithGain(signal, lpcQ12, pitchLags)
	return perIdx, gainIndices, ltpCoeffsQ14
}

// ltpCoeffsForPerSubframe builds the Q14 taps from a per-subframe set of gain
// indices within one periodicity codebook.
func ltpCoeffsForPerSubframe(perIdx int, gainIndices []int) [][5]int16 {
	cb := ltpGainCodebook(perIdx)
	out := make([][5]int16, len(gainIndices))
	for sf, idx := range gainIndices {
		if idx < 0 || idx >= len(cb) {
			idx = 0
		}
		for k := 0; k < 5; k++ {
			out[sf][k] = int16(cb[idx][k]) << 7
		}
	}
	return out
}

// lpcResidualInt16Domain returns the short-term (LPC) residual over the past
// pitch-memory samples followed by the current frame, scaled to the int16
// magnitude domain expected by the LTP correlation floor constants. frameStart
// is the index of the first current-frame residual sample.
func (e *Encoder) lpcResidualInt16Domain(signal []float64, lpcQ12 []int16) ([]float64, int) {
	hist := e.pitchHist
	frameStart := len(hist)
	buf := make([]float64, frameStart+len(signal)+ltpOrder)
	copy(buf, hist)
	copy(buf[frameStart:], signal)
	res := make([]float64, len(buf))
	for i := range buf {
		pred := 0.0
		for j := 0; j < e.lpcOrder && j <= i-1; j++ {
			pred += float64(lpcQ12[j]) / 4096.0 * buf[i-j-1]
		}
		res[i] = (buf[i] - pred) * 32768.0
	}
	return res, frameStart
}
