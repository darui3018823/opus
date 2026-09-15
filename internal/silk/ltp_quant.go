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
	ltpOrder      = 5
	ltpCorrInvMax = 0.03 // LTP_CORR_INV_MAX
	// SILK_FIX_CONST(MAX_SUM_LOG_GAIN_DB/6, 7), MAX_SUM_LOG_GAIN_DB=250.
	maxSumLogGainConst = 5333
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

// f32 rounds a value to float32 precision; the libopus float build computes
// these stages in silk_float (float) with double accumulation only inside
// silk_energy_FLP and silk_inner_product_FLP.
func f32(x float64) float64 { return float64(float32(x)) }

// corrMatrixLTP builds the LTP_ORDER×LTP_ORDER correlation matrix X'X for the
// lagged residual starting at base (silk_corrMatrix_FLP, Order=ltpOrder).
// The running updates multiply and subtract in float before joining the
// double accumulator, exactly as the C expression evaluates.
func corrMatrixLTP(res []float64, base, length int) [ltpOrder * ltpOrder]float64 {
	var XX [ltpOrder * ltpOrder]float64
	p1 := base + ltpOrder - 1
	energy := silkEnergyFLP32(res[p1 : p1+length])
	XX[0] = f32(energy)
	for j := 1; j < ltpOrder; j++ {
		energy += f32(f32(res[p1-j]*res[p1-j]) - f32(res[p1+length-j]*res[p1+length-j]))
		XX[j*ltpOrder+j] = f32(energy)
	}
	p2 := base + ltpOrder - 2
	for lag := 1; lag < ltpOrder; lag++ {
		energy = silkInnerProductFLP32(res[p1:], res[p2:], length)
		XX[lag*ltpOrder+0] = f32(energy)
		XX[0*ltpOrder+lag] = f32(energy)
		for j := 1; j < ltpOrder-lag; j++ {
			energy += f32(f32(res[p1-j]*res[p2-j]) - f32(res[p1+length-j]*res[p2+length-j]))
			XX[(lag+j)*ltpOrder+j] = f32(energy)
			XX[j*ltpOrder+(lag+j)] = f32(energy)
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
		xX[lag] = f32(silkInnerProductFLP32(res[p1:], res[target:], length))
		p1--
	}
	return xX
}

// findLTP computes the normalised per-subframe correlation matrix/vector pairs
// used by the gain VQ (silk_find_LTP_FLP). res is the short-term (LPC) residual
// in the int16 magnitude domain (float32-representable values); frameStart
// indexes the first frame sample and must leave lag+LTP_ORDER/2+LTP_ORDER-1
// history samples before it; lags are the per-subframe pitch lags. The
// residual must extend LTP_ORDER samples past the frame for the energy term.
func (e *Encoder) findLTP(res []float64, frameStart int, lags []int, subfrLen int) ([][ltpOrder * ltpOrder]float64, [][ltpOrder]float64) {
	return silkFindLTPFLP(res, frameStart, lags, subfrLen, e.nSubframes)
}

func silkFindLTPFLP(res []float64, frameStart int, lags []int, subfrLen, nbSubfr int) ([][ltpOrder * ltpOrder]float64, [][ltpOrder]float64) {
	XX := make([][ltpOrder * ltpOrder]float64, nbSubfr)
	xX := make([][ltpOrder]float64, nbSubfr)
	const ltpCorrInvMaxHalf = float64(float32(ltpCorrInvMax) * 0.5)
	for k := 0; k < nbSubfr; k++ {
		rPtr := frameStart + k*subfrLen
		lagBase := rPtr - (lags[k] + ltpOrder/2)
		m := corrMatrixLTP(res, lagBase, subfrLen)
		v := corrVectorLTP(res, lagBase, rPtr, subfrLen)
		xx := f32(silkEnergyFLP32(res[rPtr : rPtr+subfrLen+ltpOrder]))
		floor := f32(f32(ltpCorrInvMaxHalf*f32(m[0]+m[ltpOrder*ltpOrder-1])) + 1.0)
		denom := xx
		if floor > denom {
			denom = floor
		}
		temp := f32(1.0 / denom)
		for i := range m {
			m[i] = f32(m[i] * temp)
		}
		for i := range v {
			v[i] = f32(v[i] * temp)
		}
		XX[k] = m
		xX[k] = v
	}
	return XX, xX
}

// silkLin2Log ports silk_lin2log: approximate 128*log2(inLin) in Q7.
func silkLin2Log(inLin int32) int32 {
	lz, fracQ7 := silkCLZFrac(inLin)
	return silkSMLAWB(fracQ7, fracQ7*(128-fracQ7), 179) + int32(31-lz)<<7
}

// silkAddPosSat32 ports silk_ADD_POS_SAT32 for non-negative operands.
func silkAddPosSat32(a, b int32) int32 {
	sum := int32(uint32(a) + uint32(b))
	if uint32(sum)&0x80000000 != 0 {
		return math.MaxInt32
	}
	return sum
}

// silkVQWMatEC ports silk_VQ_WMat_EC_c: the entropy-constrained,
// matrix-weighted 5-tap codebook search for one subframe in Q17/Q15/Q8
// fixed point. It returns the best index, residual energy, rate-distortion
// cost, and the codebook gain of the winner.
func silkVQWMatEC(xxQ17 *[ltpOrder * ltpOrder]int32, xXQ17 *[ltpOrder]int32, perIdx, subfrLen int, maxGainQ7 int32) (ind int, resNrgQ15, rateDistQ8, gainQ7 int32) {
	cb := ltpGainCodebook(perIdx)
	cbGain := silkLTPGainVQGainCodebooks[perIdx]
	cl := silkLTPGainBITSQ5Codebooks[perIdx]

	var negxXQ24 [ltpOrder]int32
	for i := range negxXQ24 {
		negxXQ24[i] = -(xXQ17[i] << 7)
	}
	rateDistQ8 = math.MaxInt32
	resNrgQ15 = math.MaxInt32
	ind = 0
	mla := func(a, b int32, c int8) int32 { return a + b*int32(c) }
	for k := range cb {
		row := cb[k]
		gainTmpQ7 := int32(cbGain[k])
		// Quantization error: 1 - 2 * xX * cb + cb' * XX * cb.
		sum1Q15 := int32(32801) // SILK_FIX_CONST(1.001, 15)

		// Penalty for too large gain.
		penalty := silkMax32(gainTmpQ7-maxGainQ7, 0) << 11

		// First row of XX_Q17.
		sum2Q24 := mla(negxXQ24[0], xxQ17[1], row[1])
		sum2Q24 = mla(sum2Q24, xxQ17[2], row[2])
		sum2Q24 = mla(sum2Q24, xxQ17[3], row[3])
		sum2Q24 = mla(sum2Q24, xxQ17[4], row[4])
		sum2Q24 <<= 1
		sum2Q24 = mla(sum2Q24, xxQ17[0], row[0])
		sum1Q15 = silkSMLAWB(sum1Q15, sum2Q24, int16(row[0]))

		// Second row.
		sum2Q24 = mla(negxXQ24[1], xxQ17[7], row[2])
		sum2Q24 = mla(sum2Q24, xxQ17[8], row[3])
		sum2Q24 = mla(sum2Q24, xxQ17[9], row[4])
		sum2Q24 <<= 1
		sum2Q24 = mla(sum2Q24, xxQ17[6], row[1])
		sum1Q15 = silkSMLAWB(sum1Q15, sum2Q24, int16(row[1]))

		// Third row.
		sum2Q24 = mla(negxXQ24[2], xxQ17[13], row[3])
		sum2Q24 = mla(sum2Q24, xxQ17[14], row[4])
		sum2Q24 <<= 1
		sum2Q24 = mla(sum2Q24, xxQ17[12], row[2])
		sum1Q15 = silkSMLAWB(sum1Q15, sum2Q24, int16(row[2]))

		// Fourth row.
		sum2Q24 = mla(negxXQ24[3], xxQ17[19], row[4])
		sum2Q24 <<= 1
		sum2Q24 = mla(sum2Q24, xxQ17[18], row[3])
		sum1Q15 = silkSMLAWB(sum1Q15, sum2Q24, int16(row[3]))

		// Last row.
		sum2Q24 = negxXQ24[4] << 1
		sum2Q24 = mla(sum2Q24, xxQ17[24], row[4])
		sum1Q15 = silkSMLAWB(sum1Q15, sum2Q24, int16(row[4]))

		if sum1Q15 >= 0 {
			// Translate residual energy to bits using the high-rate
			// assumption (6 dB ==> 1 bit/sample).
			bitsResQ8 := silkSMULBB(int32(subfrLen), silkLin2Log(sum1Q15+penalty)-(15<<7))
			// The codelength component is halved ("-1" shift).
			bitsTotQ8 := bitsResQ8 + int32(cl[k])<<(3-1)
			if bitsTotQ8 <= rateDistQ8 {
				rateDistQ8 = bitsTotQ8
				resNrgQ15 = sum1Q15 + penalty
				ind = k
				gainQ7 = gainTmpQ7
			}
		}
	}
	return ind, resNrgQ15, rateDistQ8, gainQ7
}

// silkQuantLTPGains ports silk_quant_LTP_gains_FLP: the float correlations
// are converted to Q17 with libopus' float2int rounding, then the fixed-point
// codebook search chooses the periodicity index and per-subframe gain
// indices. It returns those, the updated cumulative log gain, and the LTP
// prediction gain in dB (Q7 value scaled by 1/128 as a float32).
func silkQuantLTPGains(XX [][ltpOrder * ltpOrder]float64, xX [][ltpOrder]float64, subfrLen, nbSubfr int, sumLogGainQ7 int32) (perIdx int, gainIndices []int, newSumLogGainQ7 int32, predGainDB float64) {
	xxQ17 := make([][ltpOrder * ltpOrder]int32, nbSubfr)
	xXQ17 := make([][ltpOrder]int32, nbSubfr)
	for j := 0; j < nbSubfr; j++ {
		for i := range xxQ17[j] {
			xxQ17[j][i] = silkFloat2Int(XX[j][i] * 131072.0)
		}
		for i := range xXQ17[j] {
			xXQ17[j][i] = silkFloat2Int(xX[j][i] * 131072.0)
		}
	}

	const gainSafety = int32(51) // SILK_FIX_CONST(0.4, 7)
	minRateDistQ7 := int32(math.MaxInt32)
	bestSumLogGainQ7 := int32(0)
	var resNrgQ15 int32
	gainIndices = make([]int, nbSubfr)
	for k := 0; k < 3; k++ {
		tempIdx := make([]int, nbSubfr)
		resNrgQ15Tmp := int32(0)
		rateDistQ7 := int32(0)
		sumLogGainTmpQ7 := sumLogGainQ7
		for j := 0; j < nbSubfr; j++ {
			maxGainQ7 := silkLog2Lin((int32(maxSumLogGainConst)-sumLogGainTmpQ7)+(7<<7)) - gainSafety
			ind, resNrgSubfr, rateDistSubfr, gainQ7 := silkVQWMatEC(&xxQ17[j], &xXQ17[j], k, subfrLen, maxGainQ7)
			tempIdx[j] = ind
			resNrgQ15Tmp = silkAddPosSat32(resNrgQ15Tmp, resNrgSubfr)
			rateDistQ7 = silkAddPosSat32(rateDistQ7, rateDistSubfr)
			sumLogGainTmpQ7 = silkMax32(0, sumLogGainTmpQ7+silkLin2Log(gainSafety+gainQ7)-(7<<7))
		}
		if rateDistQ7 <= minRateDistQ7 {
			minRateDistQ7 = rateDistQ7
			perIdx = k
			copy(gainIndices, tempIdx)
			bestSumLogGainQ7 = sumLogGainTmpQ7
		}
		// libopus reports the prediction gain from the residual energy of the
		// last codebook it evaluated, not of the selected one.
		resNrgQ15 = resNrgQ15Tmp
	}

	if nbSubfr == 2 {
		resNrgQ15 >>= 1
	} else {
		resNrgQ15 >>= 2
	}
	newSumLogGainQ7 = bestSumLogGainQ7
	predGainDBQ7 := silkSMULBB(-3, silkLin2Log(resNrgQ15)-(15<<7))
	predGainDB = f32(float64(predGainDBQ7) * (1.0 / 128.0))
	return perIdx, gainIndices, newSumLogGainQ7, predGainDB
}

// quantLTPGains chooses the periodicity codebook and per-subframe gain indices
// that minimise the total weighted rate-distortion (silk_quant_LTP_gains), and
// returns the resulting indices plus the LTP prediction coding gain in dB. The
// cumulative sum_log_gain state limits the total prediction gain across
// subframes for stability.
func (e *Encoder) quantLTPGains(XX [][ltpOrder * ltpOrder]float64, xX [][ltpOrder]float64, subfrLen int) (perIdx int, gainIndices []int, predGainDB float64) {
	perIdx, gainIndices, e.ltpSumLogGainQ7, predGainDB = silkQuantLTPGains(XX, xX, subfrLen, e.nSubframes, e.ltpSumLogGainQ7)
	return perIdx, gainIndices, predGainDB
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
		// silk_float residual: the exact LTP stages expect float32 values.
		res[i] = f32((buf[i] - pred) * 32768.0)
	}
	return res, frameStart
}
