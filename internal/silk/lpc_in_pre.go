package silk

import "math"

const (
	maxPredictionPowerGain           = 1e4
	maxPredictionPowerGainAfterReset = 1e2
)

type lpcInPreConfig struct {
	input           []float64
	subframeLengths []int
	invGains        []float64
	ltpCoefs        [][]float64
	pitchLags       []int
	ltpPredCodGain  float64
	codingQuality   float64
	voiced          bool
}

func lpcMinInvGain(ltpPredCodGain, codingQuality float64, firstFrameAfterReset bool) float64 {
	if firstFrameAfterReset {
		return 1.0 / maxPredictionPowerGainAfterReset
	}
	denom := 0.25 + 0.75*codingQuality
	if denom <= 0 {
		denom = 0.25
	}
	return math.Pow(2.0, ltpPredCodGain/3.0) / maxPredictionPowerGain / denom
}

// buildLPCInPre builds the input domain used by libopus find_LPC_FLP. The input
// x is preceding history followed by the current frame; each output subframe is
// order warm-up samples plus the subframe, scaled by the matching inverse gain.
func buildLPCInPre(x []float64, subframeLengths []int, invGains []float64, ltpCoefs [][]float64, pitchLags []int, order int, voiced bool) []float64 {
	if order < 0 {
		order = 0
	}
	frameLen := 0
	for _, n := range subframeLengths {
		if n > 0 {
			frameLen += n
		}
	}
	if frameLen == 0 {
		return nil
	}
	frameStart := len(x) - frameLen
	if frameStart < 0 {
		frameStart = 0
	}
	outLen := frameLen + order*len(subframeLengths)
	out := make([]float64, outLen)
	dst := 0
	cum := 0
	for sf, subLen := range subframeLengths {
		if subLen < 0 {
			subLen = 0
		}
		invGain := 1.0
		if sf < len(invGains) && invGains[sf] != 0 {
			invGain = invGains[sf]
		}
		xPtr := frameStart + cum - order
		if !voiced {
			for i := 0; i < subLen+order; i++ {
				out[dst+i] = sampleAt(x, xPtr+i) * invGain
			}
		} else {
			lag := 0
			if sf < len(pitchLags) {
				lag = pitchLags[sf]
			}
			coefs := []float64(nil)
			if sf < len(ltpCoefs) {
				coefs = ltpCoefs[sf]
			}
			for i := 0; i < subLen+order; i++ {
				v := sampleAt(x, xPtr+i)
				if lag > 0 {
					lagPtr := xPtr - lag + i
					for j := 0; j < ltpOrder; j++ {
						b := 0.0
						if j < len(coefs) {
							b = coefs[j]
						}
						v -= b * sampleAt(x, lagPtr+ltpOrder/2-j)
					}
				}
				out[dst+i] = v * invGain
			}
		}
		dst += subLen + order
		cum += subLen
	}
	return out
}

func sampleAt(x []float64, idx int) float64 {
	if idx < 0 || idx >= len(x) {
		return 0
	}
	return x[idx]
}

func equalSubframeLengths(frameSize, nSubframes int) []int {
	if nSubframes <= 0 {
		return nil
	}
	lengths := make([]int, nSubframes)
	base := frameSize / nSubframes
	for i := range lengths {
		lengths[i] = base
	}
	return lengths
}

func invGainsFromIndices(gainIndices []int) []float64 {
	inv := make([]float64, len(gainIndices))
	for i, idx := range gainIndices {
		gain := float64(silkGainDequantQ16(idx)) / 65536.0
		if gain <= 0 {
			inv[i] = 1
		} else {
			inv[i] = 1.0 / gain
		}
	}
	return inv
}

func ltpQ14ToFloat(ltpCoeffsQ14 [][5]int16) [][]float64 {
	out := make([][]float64, len(ltpCoeffsQ14))
	for sf, coeffs := range ltpCoeffsQ14 {
		out[sf] = make([]float64, ltpOrder)
		for k := 0; k < ltpOrder; k++ {
			out[sf][k] = float64(coeffs[k]) / 16384.0
		}
	}
	return out
}

// lastHalfBurgNLSF runs Burg LPC analysis over the last half of a stacked
// LPC_in_pre buffer. Each stacked subframe is subfrLength samples long,
// including the order-sample warm-up prefix used by find_LPC_FLP.
func lastHalfBurgNLSF(preSignal []float64, subfrLength, order, nbSubfr int, minInvGain float64) ([]int16, []float64) {
	if order <= 0 || subfrLength <= order || nbSubfr < 2 {
		return nil, nil
	}
	halfSubfr := nbSubfr / 2
	if halfSubfr <= 0 {
		return nil, nil
	}
	start := (nbSubfr - halfSubfr) * subfrLength
	need := start + halfSubfr*subfrLength
	if start < 0 || need > len(preSignal) {
		return nil, nil
	}
	if minInvGain <= 0 {
		minInvGain = lpcMinInvGain(0, 1, false)
	}
	a, _ := silkBurgModifiedFLP(preSignal[start:need], minInvGain, subfrLength, halfSubfr, order)
	nlsf := silkA2NLSFFLP(a, order)
	return nlsf, a
}

func firstHalfStackedLPCResidual(preSignal []float64, lpcQ12 []int16, order, subfrLength, nbSubfr int) float64 {
	if order <= 0 || subfrLength <= order || nbSubfr < 2 || len(lpcQ12) < order {
		return 0
	}
	halfSubfr := nbSubfr / 2
	energy := 0.0
	for sf := 0; sf < halfSubfr; sf++ {
		base := sf * subfrLength
		if base+subfrLength > len(preSignal) {
			break
		}
		for i := order; i < subfrLength; i++ {
			pred := 0.0
			for j := 0; j < order; j++ {
				pred += float64(lpcQ12[j]) / 4096.0 * preSignal[base+i-j-1]
			}
			err := preSignal[base+i] - pred
			energy += err * err
		}
	}
	return energy
}

func (e *Encoder) lpcInPreInput(signal []float64) []float64 {
	histLen := len(e.pitchHist)
	if histLen < e.lpcOrder {
		histLen = e.lpcOrder
	}
	buf := make([]float64, histLen+len(signal))
	if len(e.pitchHist) >= histLen {
		copy(buf[:histLen], e.pitchHist[len(e.pitchHist)-histLen:])
	} else {
		copy(buf[histLen-len(e.pitchHist):histLen], e.pitchHist)
	}
	copy(buf[histLen:], signal)
	return buf
}
