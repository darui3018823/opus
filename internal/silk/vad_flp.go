package silk

import "math"

// vad_flp.go ports libopus silk/VAD.c (silk_VAD_Init, silk_VAD_GetSA_Q8_c,
// silk_VAD_GetNoiseLevels), silk/ana_filt_bank_1.c, and silk/sigm_Q15.c in
// their original fixed-point arithmetic. The libopus float encoder build uses
// this fixed-point VAD unchanged, so the speech activity, tilt, and band
// quality it produces are integers (Q8/Q15) that the float stages consume.

const (
	silkVADNBands                  = 4 // VAD_N_BANDS
	silkVADInternalSubframesLog2   = 2 // VAD_INTERNAL_SUBFRAMES_LOG2
	silkVADInternalSubframes       = 1 << silkVADInternalSubframesLog2
	silkVADNoiseLevelSmoothCoefQ16 = 1024  // VAD_NOISE_LEVEL_SMOOTH_COEF_Q16
	silkVADNoiseLevelsBias         = 50    // VAD_NOISE_LEVELS_BIAS
	silkVADNegativeOffsetQ5        = 128   // VAD_NEGATIVE_OFFSET_Q5
	silkVADSNRFactorQ16            = 45000 // VAD_SNR_FACTOR_Q16
	silkVADSNRSmoothCoefQ18        = 4096  // VAD_SNR_SMOOTH_COEF_Q18
	silkVADAnaFiltBankA20          = int16(5394 << 1)
	silkVADAnaFiltBankA21          = int16(-24290)
)

var silkVADTiltWeights = [silkVADNBands]int32{30000, 6000, -12000, -12000}

// silkVADState mirrors silk_VAD_state.
type silkVADState struct {
	anaState       [2]int32
	anaState1      [2]int32
	anaState2      [2]int32
	xnrgSubfr      [silkVADNBands]int32
	nrgRatioSmthQ8 [silkVADNBands]int32
	hpState        int16
	nl             [silkVADNBands]int32
	invNL          [silkVADNBands]int32
	noiseLevelBias [silkVADNBands]int32
	counter        int32
}

// silkVADResult carries the VAD outputs both as the libopus integers and as
// the normalized floats the Go float stages currently consume.
type silkVADResult struct {
	speechActivityQ8    int
	inputTiltQ15        int
	inputQualityBandQ15 [silkVADNBands]int
	speechActivity      float64
	inputTilt           float64
	inputQuality        float64
	inputQualityBand    [silkVADNBands]float64
}

func newSilkVADState() silkVADState {
	var st silkVADState
	st.reset()
	return st
}

// reset ports silk_VAD_Init.
func (st *silkVADState) reset() {
	*st = silkVADState{}
	for b := 0; b < silkVADNBands; b++ {
		st.noiseLevelBias[b] = silkMax32(silkVADNoiseLevelsBias/int32(b+1), 1)
	}
	for b := 0; b < silkVADNBands; b++ {
		st.nl[b] = 100 * st.noiseLevelBias[b]
		st.invNL[b] = math.MaxInt32 / st.nl[b]
	}
	st.counter = 15
	for b := 0; b < silkVADNBands; b++ {
		st.nrgRatioSmthQ8[b] = 100 * 256
	}
}

// silkVADGetSAQ8 runs the SILK VAD on one frame of normalized float input.
// The samples are converted to int16 with nearest-even rounding like the
// libopus float API's FLOAT2INT16.
func (e *Encoder) silkVADGetSAQ8(signal []float64) silkVADResult {
	result := silkVADResult{speechActivityQ8: 255, inputQuality: 1.0}
	for b := range result.inputQualityBandQ15 {
		result.inputQualityBandQ15[b] = 32767
	}
	if len(signal) == 0 || e.frameSize < 8 {
		return result.withFloats()
	}
	x16 := make([]int16, e.frameSize)
	for i := 0; i < len(x16) && i < len(signal); i++ {
		x16[i] = silkSAT16(silkFloat2Int(signal[i] * 32768.0))
	}
	saQ8, tiltQ15, qualityQ15 := e.silkVAD.getSAQ8(x16, e.frameSize, e.sampleRate/1000)
	result.speechActivityQ8 = saQ8
	result.inputTiltQ15 = tiltQ15
	result.inputQualityBandQ15 = qualityQ15
	return result.withFloats()
}

func (r silkVADResult) withFloats() silkVADResult {
	r.speechActivity = float64(r.speechActivityQ8) / 256.0
	r.inputTilt = float64(r.inputTiltQ15) / 32768.0
	for b := range r.inputQualityBand {
		r.inputQualityBand[b] = float64(r.inputQualityBandQ15[b]) / 32768.0
	}
	// silk_noise_shape_analysis_FLP: 0.5 * (band 0 + band 1) quality.
	r.inputQuality = 0.5 * (r.inputQualityBand[0] + r.inputQualityBand[1])
	return r
}

// getSAQ8 ports silk_VAD_GetSA_Q8_c. It returns speech_activity_Q8,
// input_tilt_Q15, and input_quality_bands_Q15.
func (st *silkVADState) getSAQ8(pIn []int16, frameLength, fsKHz int) (saQ8, tiltQ15 int, qualityQ15 [silkVADNBands]int) {
	decimatedFramelength1 := frameLength >> 1
	decimatedFramelength2 := frameLength >> 2
	decimatedFramelength := frameLength >> 3

	var xOffset [silkVADNBands]int
	xOffset[0] = 0
	xOffset[1] = decimatedFramelength + decimatedFramelength2
	xOffset[2] = xOffset[1] + decimatedFramelength
	xOffset[3] = xOffset[2] + decimatedFramelength2
	x := make([]int16, xOffset[3]+decimatedFramelength1)

	// 0-8 kHz to 0-4 kHz and 4-8 kHz
	silkAnaFiltBank1(pIn[:frameLength], &st.anaState, x, x[xOffset[3]:])
	// 0-4 kHz to 0-2 kHz and 2-4 kHz
	silkAnaFiltBank1(x[:decimatedFramelength1], &st.anaState1, x, x[xOffset[2]:])
	// 0-2 kHz to 0-1 kHz and 1-2 kHz
	silkAnaFiltBank1(x[:decimatedFramelength2], &st.anaState2, x, x[xOffset[1]:])

	// HP filter on lowest band (differentiator).
	x[decimatedFramelength-1] >>= 1
	hpStateTmp := x[decimatedFramelength-1]
	for i := decimatedFramelength - 1; i > 0; i-- {
		x[i-1] >>= 1
		x[i] -= x[i-1]
	}
	x[0] -= st.hpState
	st.hpState = hpStateTmp

	// Calculate the energy in each band.
	var xnrg [silkVADNBands]int32
	for b := 0; b < silkVADNBands; b++ {
		decLen := frameLength >> uint(silkMinInt(silkVADNBands-b, silkVADNBands-1))
		decSubframeLength := decLen >> silkVADInternalSubframesLog2
		decSubframeOffset := 0
		xnrg[b] = st.xnrgSubfr[b]
		var sumSquared int32
		for s := 0; s < silkVADInternalSubframes; s++ {
			sumSquared = 0
			for i := 0; i < decSubframeLength; i++ {
				xTmp := int32(x[xOffset[b]+i+decSubframeOffset]) >> 3
				sumSquared = silkSMLABB(sumSquared, xTmp, xTmp)
			}
			if s < silkVADInternalSubframes-1 {
				xnrg[b] = silkAddPosSat32(xnrg[b], sumSquared)
			} else {
				// Look-ahead subframe.
				xnrg[b] = silkAddPosSat32(xnrg[b], sumSquared>>1)
			}
			decSubframeOffset += decSubframeLength
		}
		st.xnrgSubfr[b] = sumSquared
	}

	st.getNoiseLevels(&xnrg)

	// Signal-plus-noise to noise ratio estimation.
	sumSquared := int32(0)
	inputTilt := int32(0)
	var nrgToNoiseRatioQ8 [silkVADNBands]int32
	for b := 0; b < silkVADNBands; b++ {
		speechNrg := xnrg[b] - st.nl[b]
		if speechNrg > 0 {
			if uint32(xnrg[b])&0xFF800000 == 0 {
				nrgToNoiseRatioQ8[b] = (xnrg[b] << 8) / (st.nl[b] + 1)
			} else {
				nrgToNoiseRatioQ8[b] = xnrg[b] / ((st.nl[b] >> 8) + 1)
			}
			snrQ7 := silkLin2Log(nrgToNoiseRatioQ8[b]) - 8*128
			sumSquared = silkSMLABB(sumSquared, snrQ7, snrQ7)
			if speechNrg < 1<<20 {
				// Scale down SNR value for small subband speech energies.
				snrQ7 = silkSMULWB(silkSqrtApprox(speechNrg)<<6, int16(snrQ7))
			}
			inputTilt = silkSMLAWB(inputTilt, silkVADTiltWeights[b], int16(snrQ7))
		} else {
			nrgToNoiseRatioQ8[b] = 256
		}
	}

	// Mean-of-squares, root-mean-square approximation, scale to dBs.
	sumSquared /= silkVADNBands
	pSNRdBQ7 := int32(int16(3 * silkSqrtApprox(sumSquared)))

	// Speech probability estimation.
	saQ15 := silkSigmQ15(silkSMULWB(silkVADSNRFactorQ16, int16(pSNRdBQ7)) - silkVADNegativeOffsetQ5)

	// Frequency tilt measure.
	tiltQ15 = int((silkSigmQ15(inputTilt) - 16384) << 1)

	// Scale the sigmoid output based on power levels.
	speechNrg := int32(0)
	for b := 0; b < silkVADNBands; b++ {
		speechNrg += int32(b+1) * ((xnrg[b] - st.nl[b]) >> 4)
	}
	if frameLength == 20*fsKHz {
		speechNrg >>= 1
	}
	if speechNrg <= 0 {
		saQ15 >>= 1
	} else if speechNrg < 16384 {
		speechNrg <<= 16
		speechNrg = silkSqrtApprox(speechNrg)
		saQ15 = silkSMULWB(32768+speechNrg, int16(saQ15))
	}
	saQ8 = silkMinInt(int(saQ15>>7), 255)

	// Energy level and SNR estimation.
	smoothCoefQ16 := silkSMULWB(silkVADSNRSmoothCoefQ18, int16(silkSMULWB(saQ15, int16(saQ15))))
	if frameLength == 10*fsKHz {
		smoothCoefQ16 >>= 1
	}
	for b := 0; b < silkVADNBands; b++ {
		st.nrgRatioSmthQ8[b] = silkSMLAWB(st.nrgRatioSmthQ8[b], nrgToNoiseRatioQ8[b]-st.nrgRatioSmthQ8[b], int16(smoothCoefQ16))
		snrQ7 := 3 * (silkLin2Log(st.nrgRatioSmthQ8[b]) - 8*128)
		qualityQ15[b] = int(silkSigmQ15((snrQ7 - 16*128) >> 4))
	}
	return saQ8, tiltQ15, qualityQ15
}

// getNoiseLevels ports silk_VAD_GetNoiseLevels.
func (st *silkVADState) getNoiseLevels(pX *[silkVADNBands]int32) {
	var minCoef int32
	if st.counter < 1000 {
		minCoef = 32767 / ((st.counter >> 4) + 1)
		st.counter++
	}
	for k := 0; k < silkVADNBands; k++ {
		nl := st.nl[k]
		nrg := silkAddPosSat32(pX[k], st.noiseLevelBias[k])
		invNrg := math.MaxInt32 / nrg
		var coef int32
		if nrg > nl<<3 {
			coef = silkVADNoiseLevelSmoothCoefQ16 >> 3
		} else if nrg < nl {
			coef = silkVADNoiseLevelSmoothCoefQ16
		} else {
			coef = silkSMULWB(silkSMULWW(invNrg, nl), int16(silkVADNoiseLevelSmoothCoefQ16<<1))
		}
		coef = silkMax32(coef, minCoef)
		st.invNL[k] = silkSMLAWB(st.invNL[k], invNrg-st.invNL[k], int16(coef))
		nl = math.MaxInt32 / st.invNL[k]
		nl = silkMin32(nl, 0x00FFFFFF)
		st.nl[k] = nl
	}
}

// silkAnaFiltBank1 ports silk_ana_filt_bank_1: split N samples into low and
// high half-bands of N/2 samples each. outL may alias in.
func silkAnaFiltBank1(in []int16, s *[2]int32, outL, outH []int16) {
	n2 := len(in) >> 1
	for k := 0; k < n2; k++ {
		in32 := int32(in[2*k]) << 10
		y := in32 - s[0]
		x := silkSMLAWB(y, y, silkVADAnaFiltBankA21)
		out1 := s[0] + x
		s[0] = in32 + x

		in32 = int32(in[2*k+1]) << 10
		y = in32 - s[1]
		x = silkSMULWB(y, silkVADAnaFiltBankA20)
		out2 := s[1] + x
		s[1] = in32 + x

		outL[k] = silkSAT16(silkRShiftRound(int64(out2+out1), 11))
		outH[k] = silkSAT16(silkRShiftRound(int64(out2-out1), 11))
	}
}

var (
	silkSigmLUTSlopeQ10 = [6]int32{237, 153, 73, 30, 12, 7}
	silkSigmLUTPosQ15   = [6]int32{16384, 23955, 28861, 31213, 32178, 32548}
	silkSigmLUTNegQ15   = [6]int32{16384, 8812, 3906, 1554, 589, 219}
)

// silkSigmQ15 ports silk_sigm_Q15: approximate sigmoid of a Q5 input in Q15.
func silkSigmQ15(inQ5 int32) int32 {
	if inQ5 < 0 {
		inQ5 = -inQ5
		if inQ5 >= 6*32 {
			return 0
		}
		ind := inQ5 >> 5
		return silkSigmLUTNegQ15[ind] - silkSMULBB(silkSigmLUTSlopeQ10[ind], inQ5&0x1F)
	}
	if inQ5 >= 6*32 {
		return 32767
	}
	ind := inQ5 >> 5
	return silkSigmLUTPosQ15[ind] + silkSMULBB(silkSigmLUTSlopeQ10[ind], inQ5&0x1F)
}
