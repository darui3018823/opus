package silk

import "math"

const (
	silkVADNBands                  = 4
	silkVADInternalSubframes       = 4
	silkVADNoiseLevelSmoothCoefQ16 = 1024.0
	silkVADNoiseLevelsBias         = 50.0
	silkVADNegativeOffsetQ5        = 128.0
	silkVADSNRFactorQ16            = 45000.0
	silkVADSNRSmoothCoef           = 4096.0 / 262144.0
	silkVADInt32Max                = 2147483647.0
	silkVADAnaFiltBankA20          = 5394 << 1
	silkVADAnaFiltBankA21          = -24290
)

var silkVADTiltWeights = [silkVADNBands]float64{30000, 6000, -12000, -12000}

type silkVADState struct {
	anaState       [2]int32
	anaState1      [2]int32
	anaState2      [2]int32
	hpState        int16
	xNrgSubfr      [silkVADNBands]float64
	nrgRatioSmthQ8 [silkVADNBands]float64
	nl             [silkVADNBands]float64
	invNL          [silkVADNBands]float64
	noiseLevelBias [silkVADNBands]float64
	counter        int
}

type silkVADResult struct {
	speechActivity   float64
	inputTilt        float64
	inputQuality     float64
	inputQualityBand [silkVADNBands]float64
}

func newSilkVADState() silkVADState {
	var st silkVADState
	st.reset()
	return st
}

func (st *silkVADState) reset() {
	*st = silkVADState{}
	for b := 0; b < silkVADNBands; b++ {
		bias := silkVADNoiseLevelsBias / float64(b+1)
		if bias < 1 {
			bias = 1
		}
		st.noiseLevelBias[b] = bias
		st.nl[b] = 100 * bias
		st.invNL[b] = silkVADInt32Max / st.nl[b]
		st.nrgRatioSmthQ8[b] = 100 * 256
	}
	st.counter = 15
}

func (e *Encoder) silkVADGetSAQ8(signal []float64) silkVADResult {
	result := silkVADResult{
		speechActivity: 1.0,
		inputTilt:      0,
		inputQuality:   1.0,
	}
	for b := range result.inputQualityBand {
		result.inputQualityBand[b] = 1.0
	}
	if len(signal) == 0 {
		return result
	}

	x16 := make([]int16, e.frameSize)
	for i := 0; i < len(x16) && i < len(signal); i++ {
		x16[i] = clamp16(int32(math.Round(signal[i] * 32768.0)))
	}

	low0, high0 := silkAnaFiltBank1(x16, &e.silkVAD.anaState)
	low1, high1 := silkAnaFiltBank1(low0, &e.silkVAD.anaState1)
	low2, high2 := silkAnaFiltBank1(low1, &e.silkVAD.anaState2)
	silkVADHighPassLowestBand(low2, &e.silkVAD.hpState)

	bands := [silkVADNBands][]int16{low2, high2, high1, high0}
	var xNrg [silkVADNBands]float64
	for b := 0; b < silkVADNBands; b++ {
		xNrg[b] = e.silkVAD.xNrgSubfr[b]
		decSubframeLen := len(bands[b]) / silkVADInternalSubframes
		if decSubframeLen <= 0 {
			continue
		}
		lastSum := 0.0
		for s := 0; s < silkVADInternalSubframes; s++ {
			start := s * decSubframeLen
			sum := 0.0
			for i := 0; i < decSubframeLen && start+i < len(bands[b]); i++ {
				v := float64(int32(bands[b][start+i]) >> 3)
				sum += v * v
			}
			if s < silkVADInternalSubframes-1 {
				xNrg[b] += sum
			} else {
				xNrg[b] += 0.5 * sum
			}
			lastSum = sum
		}
		e.silkVAD.xNrgSubfr[b] = lastSum
	}

	e.silkVAD.getNoiseLevels(xNrg)

	sumSquared := 0.0
	inputTiltQ5 := 0.0
	var nrgToNoiseRatioQ8 [silkVADNBands]float64
	for b := 0; b < silkVADNBands; b++ {
		speechNrg := xNrg[b] - e.silkVAD.nl[b]
		if speechNrg > 0 {
			nrgToNoiseRatioQ8[b] = xNrg[b] * 256.0 / (e.silkVAD.nl[b] + 1.0)
			snrQ7 := 128.0*math.Log2(math.Max(nrgToNoiseRatioQ8[b], 1.0)) - 8.0*128.0
			sumSquared += snrQ7 * snrQ7
			tiltSNRQ7 := snrQ7
			if speechNrg < 1<<20 {
				tiltSNRQ7 *= math.Sqrt(speechNrg) / 1024.0
			}
			inputTiltQ5 += silkVADTiltWeights[b] * tiltSNRQ7 / 65536.0
		} else {
			nrgToNoiseRatioQ8[b] = 256
		}
	}

	meanSquared := sumSquared / silkVADNBands
	pSNRDBQ7 := 3.0 * math.Sqrt(meanSquared)
	activity := silkSigmoid((silkVADSNRFactorQ16*pSNRDBQ7/65536.0 - silkVADNegativeOffsetQ5) / 32.0)

	inputTilt := 2.0 * (silkSigmoid(inputTiltQ5/32.0) - 0.5)

	speechNrg := 0.0
	for b := 0; b < silkVADNBands; b++ {
		speechNrg += float64(b+1) * (xNrg[b] - e.silkVAD.nl[b]) / 16.0
	}
	if e.frameSize == 20*(e.sampleRate/1000) {
		speechNrg *= 0.5
	}
	switch {
	case speechNrg <= 0:
		activity *= 0.5
	case speechNrg < 16384:
		activity *= (32768.0 + math.Sqrt(speechNrg*65536.0)) / 65536.0
	}
	activity = clampFloat(activity, 0, 255.0/256.0)

	smoothCoef := silkVADSNRSmoothCoef * activity * activity
	if e.frameSize == 10*(e.sampleRate/1000) {
		smoothCoef *= 0.5
	}

	for b := 0; b < silkVADNBands; b++ {
		e.silkVAD.nrgRatioSmthQ8[b] += smoothCoef * (nrgToNoiseRatioQ8[b] - e.silkVAD.nrgRatioSmthQ8[b])
		snrQ7 := 3.0 * (128.0*math.Log2(math.Max(e.silkVAD.nrgRatioSmthQ8[b], 1.0)) - 8.0*128.0)
		result.inputQualityBand[b] = clampFloat(silkSigmoid((snrQ7-16.0*128.0)/128.0), 0, 1)
	}
	result.speechActivity = activity
	result.inputTilt = inputTilt
	result.inputQuality = 0.5 * (result.inputQualityBand[0] + result.inputQualityBand[1])
	return result
}

func silkAnaFiltBank1(in []int16, state *[2]int32) ([]int16, []int16) {
	n2 := len(in) / 2
	outL := make([]int16, n2)
	outH := make([]int16, n2)
	for k := 0; k < n2; k++ {
		in32 := int32(in[2*k]) << 10
		y := in32 - state[0]
		x := int32((int64(y) * int64(silkVADAnaFiltBankA21)) >> 16)
		out1 := state[0] + x
		state[0] = in32 + x

		in32 = int32(in[2*k+1]) << 10
		y = in32 - state[1]
		x = int32((int64(y) * int64(silkVADAnaFiltBankA20)) >> 16)
		out2 := state[1] + x
		state[1] = in32 + x

		outL[k] = clamp16(rshiftRound(int64(out2)+int64(out1), 11))
		outH[k] = clamp16(rshiftRound(int64(out2)-int64(out1), 11))
	}
	return outL, outH
}

func silkVADHighPassLowestBand(x []int16, hpState *int16) {
	if len(x) == 0 {
		return
	}
	x[len(x)-1] >>= 1
	tmp := x[len(x)-1]
	for i := len(x) - 1; i > 0; i-- {
		x[i-1] >>= 1
		x[i] -= x[i-1]
	}
	x[0] -= *hpState
	*hpState = tmp
}

func (st *silkVADState) getNoiseLevels(xNrg [silkVADNBands]float64) {
	minCoefQ16 := 0.0
	if st.counter < 1000 {
		minCoefQ16 = 32767.0 / float64((st.counter>>4)+1)
		st.counter++
	}
	for b := 0; b < silkVADNBands; b++ {
		nl := math.Max(st.nl[b], 1.0)
		nrg := math.Max(xNrg[b]+st.noiseLevelBias[b], 1.0)
		invNrg := silkVADInt32Max / nrg
		coefQ16 := 0.0
		switch {
		case nrg > 8.0*nl:
			coefQ16 = silkVADNoiseLevelSmoothCoefQ16 / 8.0
		case nrg < nl:
			coefQ16 = silkVADNoiseLevelSmoothCoefQ16
		default:
			coefQ16 = (invNrg * nl / silkVADInt32Max) * (silkVADNoiseLevelSmoothCoefQ16 * 2.0)
		}
		if coefQ16 < minCoefQ16 {
			coefQ16 = minCoefQ16
		}
		coef := coefQ16 / 65536.0
		st.invNL[b] += coef * (invNrg - st.invNL[b])
		if st.invNL[b] <= 0 {
			st.invNL[b] = 1
		}
		st.nl[b] = silkVADInt32Max / st.invNL[b]
		if st.nl[b] > 0x00ffffff {
			st.nl[b] = 0x00ffffff
		}
	}
}
