package silk

import "math"

// process_gains_flp32.go ports silk_process_gains_FLP, silk_gains_quant and
// silk_residual_energy_FLP (libopus 1.6.1) with silk_float operation order.
// In VBR the encode_frame_FLP quantiser loop runs once (useCBR == 0, first
// pass within maxBits), so these gains are the coded gains.

const (
	gainQuantOffset      = (minQGainDBInt*128)/6 + 16*128                                               // OFFSET
	gainQuantScaleQ16    = (65536 * (NLevelsQGain - 1)) / (((maxQGainDBInt - minQGainDBInt) * 128) / 6) // SCALE_Q16
	gainQuantInvScaleQ16 = (65536 * (((maxQGainDBInt - minQGainDBInt) * 128) / 6)) / (NLevelsQGain - 1) // INV_SCALE_Q16
	minQGainDBInt        = 2
	maxQGainDBInt        = 88
)

// silkGainsQuant ports silk_gains_quant: gainQ16 is quantised in place, ind
// receives the coded symbols (absolute index for k == 0 without conditional
// coding, delta symbols otherwise), prevInd is LastGainIndex, and absInd the
// absolute index after each subframe.
func silkGainsQuant(gainQ16 []int32, prevInd *int, conditional bool, nbSubfr int) (ind, absInd []int) {
	ind = make([]int, nbSubfr)
	absInd = make([]int, nbSubfr)
	for k := 0; k < nbSubfr; k++ {
		// Convert to log scale, scale, floor(); the C code stores into an
		// opus_int8, which the value range never exceeds here.
		v := int(int8(silkSMULWB(gainQuantScaleQ16, int16(silkLin2Log(gainQ16[k])-gainQuantOffset))))
		if v < *prevInd {
			v++
		}
		v = clampInt(v, 0, NLevelsQGain-1)
		if k == 0 && !conditional {
			v = clampInt(v, *prevInd+MinDeltaGainQuant, NLevelsQGain-1)
			*prevInd = v
		} else {
			v -= *prevInd
			dbl := 2*MaxDeltaGainQuant - NLevelsQGain + *prevInd
			if v > dbl {
				v = dbl + (v-dbl+1)>>1
			}
			v = clampInt(v, MinDeltaGainQuant, MaxDeltaGainQuant)
			if v > dbl {
				*prevInd += v<<1 - dbl
				if *prevInd > NLevelsQGain-1 {
					*prevInd = NLevelsQGain - 1
				}
			} else {
				*prevInd += v
			}
			v -= MinDeltaGainQuant
		}
		ind[k] = v
		absInd[k] = *prevInd
		q := silkSMULWB(gainQuantInvScaleQ16, int16(*prevInd)) + gainQuantOffset
		if q > 3967 {
			q = 3967
		}
		gainQ16[k] = silkLog2Lin(q)
	}
	return ind, absInd
}

// silkResidualEnergyFLP32 ports silk_residual_energy_FLP: x is LPC_in_pre
// (each subframe: order warm-up samples then subfr_length samples, in int16
// scale float32), a holds the two float32 LPC sets (Q12 / 4096), gains the
// float32 noise-shape gains.
func silkResidualEnergyFLP32(x []float64, a [2][]float64, gains []float64, subfrLength, nbSubfr, order int) [silkMaxNBSubframes]float64 {
	var nrgs [silkMaxNBSubframes]float64
	shift := order + subfrLength
	res := make([]float64, 2*shift)
	for half := 0; half < 2; half++ {
		if half == 1 && nbSubfr != silkMaxNBSubframes {
			break
		}
		start := half * 2 * shift
		silkLPCAnalysisFilterFLP32(res, a[half], x[start:start+2*shift], 2*shift, order)
		for j := 0; j < 2; j++ {
			k := half*2 + j
			energy := silkEnergyFLP32(res[j*shift+order : j*shift+order+subfrLength])
			nrgs[k] = f32(f32(gains[k]*gains[k]) * energy)
		}
	}
	return nrgs
}

// silkProcessGainsResult carries silk_process_gains_FLP's outputs.
type silkProcessGainsResult struct {
	symbols      []int     // GainsIndices as coded (delta symbols for k > 0)
	absIndices   []int     // absolute index per subframe
	gainsQ16     []int32   // quantised gains (Q16)
	gainsUnqQ16  []int32   // GainsUnq_Q16
	gains        []float64 // quantised gains as float32 values (Q0)
	lastGainPrev int
	quantOffset  int
	lambda       float64
}

// silkProcessGainsFLP32 ports silk_process_gains_FLP. shapeGains are the
// noise-shape gains (float32), ltpPredCodGain psEncCtrl->LTPredCodGain,
// resNrg the residual energies, prevInd the shape state LastGainIndex.
func silkProcessGainsFLP32(signalType, quantOffset int, shapeGains []float64, ltpPredCodGain float64, snrDBQ7, inputTiltQ15, subfrLength, nbSubfr int,
	resNrg [silkMaxNBSubframes]float64, prevInd *int, conditional bool) silkProcessGainsResult {
	gains := make([]float64, nbSubfr)
	copy(gains, shapeGains)
	if signalType == SignalTypeVoiced {
		s := f32(1.0 - f32(0.5*silkSigmoidFLP32(f32(c32(0.25)*f32(ltpPredCodGain-12.0)))))
		for k := range gains {
			gains[k] = f32(gains[k] * s)
		}
	}
	invMaxSqrVal := f32(math.Pow(2.0, f32(c32(0.33)*f32(21.0-f32(float64(snrDBQ7)*(1/128.0))))) / float64(subfrLength))
	for k := 0; k < nbSubfr; k++ {
		g := gains[k]
		g = f32(math.Sqrt(f32(f32(g*g) + f32(resNrg[k]*invMaxSqrVal))))
		if g > 32767 {
			g = 32767
		}
		gains[k] = g
	}
	res := silkProcessGainsResult{quantOffset: quantOffset, lastGainPrev: *prevInd}
	res.gainsQ16 = make([]int32, nbSubfr)
	for k := 0; k < nbSubfr; k++ {
		res.gainsQ16[k] = int32(f32(gains[k] * 65536)) // (opus_int32)(Gains * 65536.0f) truncates
	}
	res.gainsUnqQ16 = append([]int32(nil), res.gainsQ16...)
	res.symbols, res.absIndices = silkGainsQuant(res.gainsQ16, prevInd, conditional, nbSubfr)
	res.gains = make([]float64, nbSubfr)
	for k := 0; k < nbSubfr; k++ {
		res.gains[k] = f32(float64(res.gainsQ16[k]) / 65536)
	}
	if signalType == SignalTypeVoiced {
		if f32(ltpPredCodGain+f32(float64(inputTiltQ15)*(1.0/32768.0))) > 1.0 {
			res.quantOffset = 0
		} else {
			res.quantOffset = 1
		}
	}
	return res
}

// exactProcessGains runs the libopus residual-energy and process_gains
// stages for the frame: LPC_in_pre from the domain config (already scaled by
// 1/Gains in float32), the quantised LPC sets, the noise-shape gains, the
// LTP coding gain, and the frame's SNR_dB_Q7.
func (e *Encoder) exactProcessGains(signal []float64, signalType, quantOffset int, nlsf nlsfAnalysis, cfg lpcInPreConfig, conditional bool) *silkProcessGainsResult {
	subfrLength := e.frameSize / e.nSubframes
	lpcInPre := buildLPCInPre(cfg.input, cfg.subframeLengths, cfg.invGains, cfg.ltpCoefs, cfg.pitchLags, e.lpcOrder, cfg.voiced)
	// LPC_in_pre is in [-1,1]; the residual energies scale by 2^30 exactly.
	scaled := make([]float64, len(lpcInPre))
	e.traceLPCInPre = make([]float32, len(lpcInPre))
	for i, v := range lpcInPre {
		scaled[i] = v * 32768
		e.traceLPCInPre[i] = float32(scaled[i])
	}
	e.traceInvGains = make([]float32, len(cfg.invGains))
	for i, v := range cfg.invGains {
		e.traceInvGains[i] = float32(v)
	}
	var a [2][]float64
	a[1] = make([]float64, e.lpcOrder)
	for i := 0; i < e.lpcOrder; i++ {
		a[1][i] = float64(nlsf.lpcQ12[i]) / 4096
	}
	a[0] = a[1]
	if nlsf.lpcQ12Interp != nil {
		a[0] = make([]float64, e.lpcOrder)
		for i := 0; i < e.lpcOrder; i++ {
			a[0][i] = float64(nlsf.lpcQ12Interp[i]) / 4096
		}
	}
	shapeGains := e.pendingShape32.gains[:e.nSubframes]
	resNrg := silkResidualEnergyFLP32(scaled, a, shapeGains, subfrLength, e.nSubframes, e.lpcOrder)
	snrDBQ7 := e.frameSNRdBQ7()
	prev := e.prevGainIdx
	res := silkProcessGainsFLP32(signalType, quantOffset, shapeGains, cfg.ltpPredCodGain, snrDBQ7, e.inputTiltQ15,
		subfrLength, e.nSubframes, resNrg, &prev, conditional)
	res.lambda = 0
	return &res
}
