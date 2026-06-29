package silk

import "math"

// silkResidualEnergyFLP mirrors silk_residual_energy_FLP for the encoder gain
// pipeline. lpcInPre is the stacked subframe buffer built by buildLPCInPre:
// each subframe has an order-sample warm-up prefix followed by subfrLength
// current samples. The first LPC set is used for the first frame half, and the
// second set for the last half.
func silkResidualEnergyFLP(lpcInPre []float64, lpc0Q12, lpc1Q12 []int16, gains [silkMaxNBSubframes]float64, subfrLength, nbSubfr, order int) [silkMaxNBSubframes]float64 {
	var nrgs [silkMaxNBSubframes]float64
	if subfrLength <= 0 || nbSubfr < 2 || order <= 0 {
		return nrgs
	}
	shift := order + subfrLength
	if len(lpcInPre) < nbSubfr*shift {
		return nrgs
	}

	halfResidualEnergy := func(half int, coefQ12 []int16) {
		if len(coefQ12) < order {
			return
		}
		startSF := half * 2
		if startSF >= nbSubfr {
			return
		}
		nSF := 2
		if startSF+nSF > nbSubfr {
			nSF = nbSubfr - startSF
		}
		inStart := startSF * shift
		length := nSF * shift
		if inStart+length > len(lpcInPre) {
			return
		}
		res := make([]float64, length)
		coef := make([]float64, order)
		for i := 0; i < order; i++ {
			coef[i] = float64(coefQ12[i]) / 4096.0
		}
		silkLPCAnalysisFilterFLP(res, coef, lpcInPre[inStart:inStart+length], length, order)
		for localSF := 0; localSF < nSF; localSF++ {
			sf := startSF + localSF
			base := order + localSF*shift
			energy := silkEnergyFLP(res[base : base+subfrLength])
			gain := gains[sf]
			nrgs[sf] = gain * gain * energy * (32768.0 * 32768.0)
		}
	}

	halfResidualEnergy(0, lpc0Q12)
	if nbSubfr == silkMaxNBSubframes {
		halfResidualEnergy(1, lpc1Q12)
	}
	return nrgs
}

func (e *Encoder) processGainsResidualEnergy(signal []float64, lpcQ12 []int16, lpcInterpQ12 []int16, signalType int, pitchLags []int, ltpCoeffsQ14 [][5]int16, gains [silkMaxNBSubframes]float64) [silkMaxNBSubframes]float64 {
	if e.nSubframes < 2 || e.frameSize <= 0 || e.lpcOrder <= 0 || len(lpcQ12) < e.lpcOrder {
		return e.ltpResidualEnergyPerSubframe(signal, lpcQ12, signalType, pitchLags, ltpCoeffsQ14)
	}
	subfrLengths := equalSubframeLengths(e.frameSize, e.nSubframes)
	invGains := make([]float64, e.nSubframes)
	for sf := range invGains {
		gain := gains[sf]
		if gain <= 0 || math.IsNaN(gain) || math.IsInf(gain, 0) {
			invGains[sf] = 1
		} else {
			invGains[sf] = 1.0 / gain
		}
	}
	lpcInPre := buildLPCInPre(e.lpcInPreInput(signal), subfrLengths, invGains,
		ltpQ14ToFloat(ltpCoeffsQ14), pitchLags, e.lpcOrder, signalType == SignalTypeVoiced)
	if len(lpcInPre) == 0 {
		return e.ltpResidualEnergyPerSubframe(signal, lpcQ12, signalType, pitchLags, ltpCoeffsQ14)
	}
	lpc0 := lpcQ12
	if len(lpcInterpQ12) >= e.lpcOrder {
		lpc0 = lpcInterpQ12
	}
	return silkResidualEnergyFLP(lpcInPre, lpc0, lpcQ12, gains, e.frameSize/e.nSubframes, e.nSubframes, e.lpcOrder)
}
