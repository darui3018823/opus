package silk

import (
	"math"
	"math/bits"
	"sort"
)

const (
	nlsfQuantMaxAmplitude     = 4
	nlsfQuantMaxAmplitudeExt  = 10
	nlsfQuantLevelAdjQ10      = 102
	nlsfQuantDelDecStates     = 4
	nlsfQuantDelDecStatesLog2 = 2
	nlsfWeightQ               = 2
	defaultNLSFQuantSurvivors = 6
)

func (e *Encoder) nlsfMuQ20() int32 {
	speechQ8 := int32(math.Round(e.speechActivity * 256.0))
	if speechQ8 < 0 {
		speechQ8 = 0
	}
	if speechQ8 > 256 {
		speechQ8 = 256
	}
	mu := int32(3146) + int32((int64(-268435)*int64(speechQ8))>>16)
	if e.nSubframes == 2 {
		mu += mu >> 1
	}
	if mu < 1 {
		return 1
	}
	if mu > 5243 {
		return 5243
	}
	return mu
}

func (e *Encoder) nlsfQuantSurvivors() int {
	switch {
	case e.complexity < 1:
		return 2
	case e.complexity < 2:
		return 3
	case e.complexity < 3:
		return 2
	case e.complexity < 4:
		return 4
	case e.complexity < 6:
		return 6
	case e.complexity < 8:
		return 8
	default:
		return 16
	}
}

func (e *Encoder) faithfulNLSFEncode(targetQ15 []int16, cb *nlsfCBParams, signalType int) (int, []int, []int16) {
	weights := silkNLSFWeightsLaroia(targetQ15)
	cb1Idx, rawIdx := silkNLSFEncode(targetQ15, cb, weights, e.nlsfMuQ20(), e.nlsfQuantSurvivors(), signalType)
	nlsfQ15 := reconstructNLSFQ15(cb, cb1Idx, rawIdx)
	return cb1Idx, rawIdx, nlsfQ15
}

func silkNLSFWeightsLaroia(nlsfQ15 []int16) []int16 {
	order := len(nlsfQ15)
	out := make([]int16, order)
	if order == 0 {
		return out
	}
	scale := int32(1) << (15 + nlsfWeightQ)
	tmp1 := int32(nlsfQ15[0])
	if tmp1 < 1 {
		tmp1 = 1
	}
	tmp1 = scale / tmp1
	tmp2 := int32(1)
	if order > 1 {
		tmp2 = int32(nlsfQ15[1]) - int32(nlsfQ15[0])
		if tmp2 < 1 {
			tmp2 = 1
		}
	}
	tmp2 = scale / tmp2
	out[0] = clampInt16(tmp1 + tmp2)

	for k := 1; k < order-1; k += 2 {
		tmp1 = int32(nlsfQ15[k+1]) - int32(nlsfQ15[k])
		if tmp1 < 1 {
			tmp1 = 1
		}
		tmp1 = scale / tmp1
		out[k] = clampInt16(tmp1 + tmp2)

		tmp2 = int32(nlsfQ15[k+2]) - int32(nlsfQ15[k+1])
		if tmp2 < 1 {
			tmp2 = 1
		}
		tmp2 = scale / tmp2
		out[k+1] = clampInt16(tmp1 + tmp2)
	}

	tmp1 = int32(1<<15) - int32(nlsfQ15[order-1])
	if tmp1 < 1 {
		tmp1 = 1
	}
	tmp1 = scale / tmp1
	out[order-1] = clampInt16(tmp1 + tmp2)
	return out
}

func silkNLSFEncode(targetQ15 []int16, cb *nlsfCBParams, weightsQ2 []int16, muQ20 int32, nSurvivors, signalType int) (int, []int) {
	order := cb.order
	if len(targetQ15) < order {
		return 0, make([]int, order)
	}
	target := append([]int16(nil), targetQ15[:order]...)
	silkNLSFStabilize(target, cb.deltaMinQ15, order)
	if nSurvivors <= 0 {
		nSurvivors = defaultNLSFQuantSurvivors
	}
	if nSurvivors > cb.nEntries {
		nSurvivors = cb.nEntries
	}

	errQ24 := silkNLSFVQErrors(target, cb)
	survivors := make([]int, cb.nEntries)
	for i := range survivors {
		survivors[i] = i
	}
	sort.SliceStable(survivors, func(i, j int) bool {
		return errQ24[survivors[i]] < errQ24[survivors[j]]
	})
	survivors = survivors[:nSurvivors]

	bestCB1 := survivors[0]
	bestRaw := make([]int, order)
	bestRD := int64(math.MaxInt64)
	for _, cb1 := range survivors {
		resQ10 := make([]int16, order)
		wAdjQ5 := make([]int16, order)
		for i := 0; i < order; i++ {
			cb1ValQ15 := int32(cb.cb1Q8[cb1*order+i]) << 7
			wQ9 := int32(cb.cb1WghtQ9[cb1*order+i])
			resQ10[i] = int16((int64(int32(target[i])-cb1ValQ15) * int64(wQ9)) >> 14)
			den := int64(wQ9) * int64(wQ9)
			if den <= 0 {
				wAdjQ5[i] = 1
			} else {
				w := (int64(weightsQ2[i]) << 21) / den
				if w < 1 {
					w = 1
				}
				if w > math.MaxInt16 {
					w = math.MaxInt16
				}
				wAdjQ5[i] = int16(w)
			}
		}

		ecIx, predQ8 := nlsfUnpack(cb, cb1)
		raw, rd := silkNLSFDelDecQuant(resQ10, wAdjQ5, predQ8, ecIx, cb.cb2RatesQ5, cb.quantStepSizeQ16, cb.invQuantStepSizeQ6, muQ20)

		icdf := cb.cb1ICDF[(signalType>>1)*cb.nEntries:]
		probQ8 := int32(0)
		if cb1 == 0 {
			probQ8 = 256 - int32(icdf[0])
		} else {
			probQ8 = int32(icdf[cb1-1]) - int32(icdf[cb1])
		}
		bitsQ7 := int64((8 << 7) - silkLin2Log(probQ8))
		rd += bitsQ7 * int64(muQ20>>2)
		if rd < bestRD {
			bestRD = rd
			bestCB1 = cb1
			bestRaw = raw
		}
	}
	return bestCB1, bestRaw
}

func silkNLSFVQErrors(target []int16, cb *nlsfCBParams) []int64 {
	errs := make([]int64, cb.nEntries)
	for idx := 0; idx < cb.nEntries; idx++ {
		sum := int64(0)
		predQ24 := int64(0)
		base := idx * cb.order
		for m := cb.order - 2; m >= 0; m -= 2 {
			diffQ15 := int64(int32(target[m+1]) - (int32(cb.cb1Q8[base+m+1]) << 7))
			diffwQ24 := diffQ15 * int64(cb.cb1WghtQ9[base+m+1])
			sum += abs64(diffwQ24 - (predQ24 >> 1))
			predQ24 = diffwQ24

			diffQ15 = int64(int32(target[m]) - (int32(cb.cb1Q8[base+m]) << 7))
			diffwQ24 = diffQ15 * int64(cb.cb1WghtQ9[base+m])
			sum += abs64(diffwQ24 - (predQ24 >> 1))
			predQ24 = diffwQ24
		}
		errs[idx] = sum
	}
	return errs
}

func silkNLSFDelDecQuant(xQ10, wQ5 []int16, predCoefQ8 []uint8, ecIx []int, ecRatesQ5 []uint8, quantStepSizeQ16, invQuantStepSizeQ6, muQ20 int32) ([]int, int64) {
	order := len(xQ10)
	out0Table := make([]int32, 2*nlsfQuantMaxAmplitudeExt)
	out1Table := make([]int32, 2*nlsfQuantMaxAmplitudeExt)
	for i := -nlsfQuantMaxAmplitudeExt; i <= nlsfQuantMaxAmplitudeExt-1; i++ {
		out0 := int32(i << 10)
		out1 := out0 + 1024
		switch {
		case i > 0:
			out0 -= nlsfQuantLevelAdjQ10
			out1 -= nlsfQuantLevelAdjQ10
		case i == 0:
			out1 -= nlsfQuantLevelAdjQ10
		case i == -1:
			out0 += nlsfQuantLevelAdjQ10
		default:
			out0 += nlsfQuantLevelAdjQ10
			out1 += nlsfQuantLevelAdjQ10
		}
		slot := i + nlsfQuantMaxAmplitudeExt
		out0Table[slot] = int32((int64(out0) * int64(quantStepSizeQ16)) >> 16)
		out1Table[slot] = int32((int64(out1) * int64(quantStepSizeQ16)) >> 16)
	}

	var ind [nlsfQuantDelDecStates][silkMaxLPCOrder]int
	var prevOutQ10 [2 * nlsfQuantDelDecStates]int32
	var rdQ25 [2 * nlsfQuantDelDecStates]int64
	nStates := 1
	for i := order - 1; i >= 0; i-- {
		inQ10 := int32(xQ10[i])
		for j := 0; j < nStates; j++ {
			predQ10 := (int32(predCoefQ8[i]) * prevOutQ10[j]) >> 8
			resQ10 := inQ10 - predQ10
			indTmp := int((int64(invQuantStepSizeQ6) * int64(resQ10)) >> 16)
			indTmp = clampInt(indTmp, -nlsfQuantMaxAmplitudeExt, nlsfQuantMaxAmplitudeExt-1)
			ind[j][i] = indTmp

			out0Q10 := out0Table[indTmp+nlsfQuantMaxAmplitudeExt] + predQ10
			out1Q10 := out1Table[indTmp+nlsfQuantMaxAmplitudeExt] + predQ10
			prevOutQ10[j] = out0Q10
			prevOutQ10[j+nStates] = out1Q10

			rate0Q5, rate1Q5 := nlsfResidualRatesQ5(indTmp, ecIx[i], ecRatesQ5)
			rdTmp := rdQ25[j]
			diffQ10 := inQ10 - out0Q10
			rdQ25[j] = rdTmp + int64(diffQ10)*int64(diffQ10)*int64(wQ5[i]) + int64(muQ20)*int64(rate0Q5)
			diffQ10 = inQ10 - out1Q10
			rdQ25[j+nStates] = rdTmp + int64(diffQ10)*int64(diffQ10)*int64(wQ5[i]) + int64(muQ20)*int64(rate1Q5)
		}

		if nStates <= nlsfQuantDelDecStates/2 {
			for j := 0; j < nStates; j++ {
				ind[j+nStates][i] = ind[j][i] + 1
			}
			nStates <<= 1
			for j := nStates; j < nlsfQuantDelDecStates; j++ {
				ind[j] = ind[j-nStates]
			}
			continue
		}

		var indSort [nlsfQuantDelDecStates]int
		var rdMinQ25 [nlsfQuantDelDecStates]int64
		var rdMaxQ25 [nlsfQuantDelDecStates]int64
		for j := 0; j < nlsfQuantDelDecStates; j++ {
			if rdQ25[j] > rdQ25[j+nlsfQuantDelDecStates] {
				rdMaxQ25[j] = rdQ25[j]
				rdMinQ25[j] = rdQ25[j+nlsfQuantDelDecStates]
				rdQ25[j] = rdMinQ25[j]
				rdQ25[j+nlsfQuantDelDecStates] = rdMaxQ25[j]
				prevOutQ10[j], prevOutQ10[j+nlsfQuantDelDecStates] = prevOutQ10[j+nlsfQuantDelDecStates], prevOutQ10[j]
				indSort[j] = j + nlsfQuantDelDecStates
			} else {
				rdMinQ25[j] = rdQ25[j]
				rdMaxQ25[j] = rdQ25[j+nlsfQuantDelDecStates]
				indSort[j] = j
			}
		}
		for {
			minMaxQ25 := int64(math.MaxInt64)
			maxMinQ25 := int64(0)
			indMinMax, indMaxMin := 0, 0
			for j := 0; j < nlsfQuantDelDecStates; j++ {
				if minMaxQ25 > rdMaxQ25[j] {
					minMaxQ25 = rdMaxQ25[j]
					indMinMax = j
				}
				if maxMinQ25 < rdMinQ25[j] {
					maxMinQ25 = rdMinQ25[j]
					indMaxMin = j
				}
			}
			if minMaxQ25 >= maxMinQ25 {
				break
			}
			indSort[indMaxMin] = indSort[indMinMax] ^ nlsfQuantDelDecStates
			rdQ25[indMaxMin] = rdQ25[indMinMax+nlsfQuantDelDecStates]
			prevOutQ10[indMaxMin] = prevOutQ10[indMinMax+nlsfQuantDelDecStates]
			rdMinQ25[indMaxMin] = 0
			rdMaxQ25[indMinMax] = math.MaxInt64
			ind[indMaxMin] = ind[indMinMax]
		}
		for j := 0; j < nlsfQuantDelDecStates; j++ {
			ind[j][i] += indSort[j] >> nlsfQuantDelDecStatesLog2
		}
	}

	best := 0
	minQ25 := int64(math.MaxInt64)
	for j := 0; j < 2*nlsfQuantDelDecStates; j++ {
		if minQ25 > rdQ25[j] {
			minQ25 = rdQ25[j]
			best = j
		}
	}
	raw := make([]int, order)
	copy(raw, ind[best&(nlsfQuantDelDecStates-1)][:order])
	raw[0] += best >> nlsfQuantDelDecStatesLog2
	return raw, minQ25
}

func nlsfResidualRatesQ5(indTmp, ecIx int, ecRatesQ5 []uint8) (int32, int32) {
	switch {
	case indTmp+1 >= nlsfQuantMaxAmplitude:
		if indTmp+1 == nlsfQuantMaxAmplitude {
			return int32(ecRatesQ5[ecIx+indTmp+nlsfQuantMaxAmplitude]), 280
		}
		rate0 := int32(280 - 43*nlsfQuantMaxAmplitude + 43*indTmp)
		return rate0, rate0 + 43
	case indTmp <= -nlsfQuantMaxAmplitude:
		if indTmp == -nlsfQuantMaxAmplitude {
			return 280, int32(ecRatesQ5[ecIx+indTmp+1+nlsfQuantMaxAmplitude])
		}
		rate0 := int32(280 - 43*nlsfQuantMaxAmplitude - 43*indTmp)
		return rate0, rate0 - 43
	default:
		return int32(ecRatesQ5[ecIx+indTmp+nlsfQuantMaxAmplitude]), int32(ecRatesQ5[ecIx+indTmp+1+nlsfQuantMaxAmplitude])
	}
}

func nlsfUnpack(cb *nlsfCBParams, cb1Idx int) ([]int, []uint8) {
	ecIx := make([]int, cb.order)
	predQ8 := make([]uint8, cb.order)
	ecSelBase := cb1Idx * (cb.order / 2)
	for i := 0; i < cb.order; i += 2 {
		entry := cb.cb2Select[ecSelBase+i/2]
		ecIx[i] = ((int(entry) >> 1) & 7) * (2*nlsfQuantMaxAmplitude + 1)
		predQ8[i] = cb.predQ8[i+int(entry&1)*(cb.order-1)]
		ecIx[i+1] = ((int(entry) >> 5) & 7) * (2*nlsfQuantMaxAmplitude + 1)
		predQ8[i+1] = cb.predQ8[i+int((entry>>4)&1)*(cb.order-1)+1]
	}
	return ecIx, predQ8
}

func silkLin2Log(inLin int32) int32 {
	if inLin <= 0 {
		return 0
	}
	lz := bits.LeadingZeros32(uint32(inLin))
	fracQ7 := int32(bits.RotateLeft32(uint32(inLin), -(24-lz)) & 0x7f)
	return fracQ7 + int32((int64(fracQ7)*int64(128-fracQ7)*179)>>16) + int32(31-lz)<<7
}

func abs64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

func clampInt16(v int32) int16 {
	if v > math.MaxInt16 {
		return math.MaxInt16
	}
	if v < math.MinInt16 {
		return math.MinInt16
	}
	return int16(v)
}
