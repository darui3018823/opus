package silk

import "math"

// noise_shape_flp32.go ports silk_noise_shape_analysis_FLP (libopus 1.6.1,
// silk/float/noise_shape_analysis_FLP.c) with silk_float (float32) operation
// order: every float op is rounded through f32, the warped autocorrelation and
// Schur recursion accumulate in double like the C code, and sqrt/pow/exp go
// through double and are cast back to float32 like the (silk_float) casts.

// silkNoiseShapeInputs are the encoder-state fields the analysis reads.
type silkNoiseShapeInputs struct {
	fsKHz, nbSubfr, subfrLength int
	shapingLPCOrder, laShape    int
	shapeWinLength              int
	warpingQ16                  int32
	useCBR                      bool
	signalType                  int
	snrDBQ7                     int
	speechActivityQ8            int
	inputQualityBandQ15         [silkVADNBands]int
	ltpCorr                     float64 // psEnc->LTPCorr (float32 value)
	predGain                    float64 // psEncCtrl->predGain from find_pitch_lags (float32 value)
	pitchL                      []int
}

// silkNoiseShapeOutputs are psEncCtrl fields written by the analysis (float32
// values held in float64) plus the updated smoothers.
type silkNoiseShapeOutputs struct {
	inputQuality, codingQuality float64
	quantOffsetType             int
	ar                          [silkMaxNBSubframes][silkMaxShapeLPCOrder]float64
	gains                       [silkMaxNBSubframes]float64
	lfMAShp, lfARShp            [silkMaxNBSubframes]float64
	tilt, harmShapeGain         [silkMaxNBSubframes]float64
}

// c32 returns the float32 literal value of a C float constant (e.g. 0.94f).
func c32(x float64) float64 { return float64(float32(x)) }

// silkLog2FLP32 is silk_log2: (silk_float)(3.32192809488736 * log10(x)).
func silkLog2FLP32(x float64) float64 { return f32(3.32192809488736 * math.Log10(x)) }

// silkSigmoidFLP32 is silk_sigmoid: 1.0f / (1.0f + (silk_float)exp(-x)).
func silkSigmoidFLP32(x float64) float64 {
	return f32(1.0 / f32(1.0+f32(math.Exp(-x))))
}

// silkWarpedAutocorrelationFLP32 ports silk_warped_autocorrelation_FLP: double
// state and accumulators, float32 outputs.
func silkWarpedAutocorrelationFLP32(corr, input []float64, warping float64, length, order int) {
	var state [silkMaxShapeLPCOrder + 1]float64
	var c [silkMaxShapeLPCOrder + 1]float64
	for n := 0; n < length; n++ {
		tmp1 := input[n]
		for i := 0; i < order; i += 2 {
			tmp2 := state[i] + warping*(state[i+1]-tmp1)
			state[i] = tmp1
			c[i] += state[0] * tmp1
			tmp1 = state[i+1] + warping*(state[i+2]-tmp2)
			state[i+1] = tmp2
			c[i+1] += state[0] * tmp2
		}
		state[order] = tmp1
		c[order] += state[0] * tmp1
	}
	for i := 0; i <= order; i++ {
		corr[i] = f32(c[i])
	}
}

// warpedGainFLP32 ports warped_gain.
func warpedGainFLP32(coefs []float64, lambda float64, order int) float64 {
	lambda = -lambda
	gain := coefs[order-1]
	for i := order - 2; i >= 0; i-- {
		gain = f32(f32(lambda*gain) + coefs[i])
	}
	return f32(1.0 / f32(1.0-f32(lambda*gain)))
}

// warpedTrue2MonicCoefsFLP32 ports warped_true2monic_coefs.
func warpedTrue2MonicCoefsFLP32(coefs []float64, lambda, limit float64, order int) {
	toMonic := func() float64 {
		for i := order - 1; i > 0; i-- {
			coefs[i-1] = f32(coefs[i-1] - f32(lambda*coefs[i]))
		}
		gain := f32(f32(1.0-f32(lambda*lambda)) / f32(1.0+f32(lambda*coefs[0])))
		for i := 0; i < order; i++ {
			coefs[i] = f32(coefs[i] * gain)
		}
		return gain
	}
	gain := toMonic()
	ind := 0
	for iter := 0; iter < 10; iter++ {
		maxabs := -1.0
		for i := 0; i < order; i++ {
			tmp := math.Abs(coefs[i])
			if tmp > maxabs {
				maxabs = tmp
				ind = i
			}
		}
		if maxabs <= limit {
			return
		}
		// Convert back to true warped coefficients
		for i := 1; i < order; i++ {
			coefs[i-1] = f32(coefs[i-1] + f32(lambda*coefs[i]))
		}
		gain = f32(1.0 / gain)
		for i := 0; i < order; i++ {
			coefs[i] = f32(coefs[i] * gain)
		}
		// Apply bandwidth expansion
		chirp := f32(c32(0.99) - f32(f32(f32(c32(0.8)+f32(c32(0.1)*float64(iter)))*f32(maxabs-limit))/f32(maxabs*float64(ind+1))))
		silkBwexpanderFLP32(coefs, order, chirp)
		gain = toMonic()
	}
}

// limitCoefsFLP32 ports limit_coefs.
func limitCoefsFLP32(coefs []float64, limit float64, order int) {
	ind := 0
	for iter := 0; iter < 10; iter++ {
		maxabs := -1.0
		for i := 0; i < order; i++ {
			tmp := math.Abs(coefs[i])
			if tmp > maxabs {
				maxabs = tmp
				ind = i
			}
		}
		if maxabs <= limit {
			return
		}
		chirp := f32(c32(0.99) - f32(f32(f32(c32(0.8)+f32(c32(0.1)*float64(iter)))*f32(maxabs-limit))/f32(maxabs*float64(ind+1))))
		silkBwexpanderFLP32(coefs, order, chirp)
	}
}

// silkNoiseShapeAnalysisFLP32 ports silk_noise_shape_analysis_FLP. x is
// [la_shape past | frame | la_shape look-ahead] in silk_float int16 scale;
// pitchRes is res_pitch for the frame (2 ms segments) when unvoiced. harmSmth
// and tiltSmth are the sShape smoothers (updated in place).
func silkNoiseShapeAnalysisFLP32(in silkNoiseShapeInputs, x, pitchRes []float64, harmSmth, tiltSmth *float64) silkNoiseShapeOutputs {
	var out silkNoiseShapeOutputs

	// GAIN CONTROL
	snrAdjDB := f32(float64(in.snrDBQ7) * (1 / 128.0))
	out.inputQuality = f32(f32(0.5*float64(in.inputQualityBandQ15[0]+in.inputQualityBandQ15[1])) * (1.0 / 32768.0))
	out.codingQuality = silkSigmoidFLP32(f32(0.25 * f32(snrAdjDB-20.0)))
	if !in.useCBR {
		b := f32(1.0 - f32(float64(in.speechActivityQ8)*(1.0/256.0)))
		snrAdjDB = f32(snrAdjDB - f32(f32(f32(f32(c32(bgSNRDecrDB)*out.codingQuality)*f32(0.5+f32(0.5*out.inputQuality)))*b)*b))
	}
	if in.signalType == SignalTypeVoiced {
		snrAdjDB = f32(snrAdjDB + f32(c32(harmSNRIncrDB)*in.ltpCorr))
	} else {
		snrAdjDB = f32(snrAdjDB + f32(f32(f32(f32(c32(-0.4)*float64(in.snrDBQ7))*(1/128.0))+6.0)*f32(1.0-out.inputQuality)))
	}

	// SPARSENESS PROCESSING
	if in.signalType == SignalTypeVoiced {
		out.quantOffsetType = 0
	} else {
		nSamples := 2 * in.fsKHz
		energyVariation := 0.0
		logEnergyPrev := 0.0
		nSegs := silkSubframeLengthMS * in.nbSubfr / 2
		for k := 0; k < nSegs; k++ {
			nrg := f32(float64(nSamples) + f32(silkEnergyFLP32(pitchRes[k*nSamples:(k+1)*nSamples])))
			logEnergy := silkLog2FLP32(nrg)
			if k > 0 {
				energyVariation = f32(energyVariation + math.Abs(f32(logEnergy-logEnergyPrev)))
			}
			logEnergyPrev = logEnergy
		}
		if energyVariation > f32(c32(energyVariationQntOffset)*float64(nSegs-1)) {
			out.quantOffsetType = 0
		} else {
			out.quantOffsetType = 1
		}
	}

	// Control bandwidth expansion
	strength := f32(c32(findPitchWhiteNoiseFrac) * in.predGain)
	bwExp := f32(c32(shapeBandwidthExpansion) / f32(1.0+f32(strength*strength)))
	warping := f32(f32(float64(in.warpingQ16)/65536.0) + f32(c32(0.01)*out.codingQuality))

	// Compute noise shaping AR coefs and gains
	var xWindowed [silkMaxShapeWinLength]float64
	var autoCorr [silkMaxShapeLPCOrder + 1]float64
	var rc [silkMaxShapeLPCOrder + 1]float64
	flatPart := in.fsKHz * 3
	slopePart := (in.shapeWinLength - flatPart) / 2
	xPtr := 0 // x already starts la_shape before the frame
	for k := 0; k < in.nbSubfr; k++ {
		seg := x[xPtr : xPtr+in.shapeWinLength]
		silkApplySineWindowFLP32(xWindowed[:slopePart], seg, 1, slopePart)
		copy(xWindowed[slopePart:slopePart+flatPart], seg[slopePart:slopePart+flatPart])
		silkApplySineWindowFLP32(xWindowed[slopePart+flatPart:in.shapeWinLength], seg[slopePart+flatPart:], 2, slopePart)
		xPtr += in.subfrLength

		if in.warpingQ16 > 0 {
			silkWarpedAutocorrelationFLP32(autoCorr[:], xWindowed[:in.shapeWinLength], warping, in.shapeWinLength, in.shapingLPCOrder)
		} else {
			silkAutocorrelationFLP32(autoCorr[:], xWindowed[:in.shapeWinLength], in.shapeWinLength, in.shapingLPCOrder+1)
		}
		autoCorr[0] = f32(autoCorr[0] + f32(f32(autoCorr[0]*c32(shapeWhiteNoiseFraction))+1.0))

		nrg := silkSchurFLP32(rc[:], autoCorr[:], in.shapingLPCOrder)
		ar := out.ar[k][:in.shapingLPCOrder]
		silkK2aFLP32(ar, rc[:], in.shapingLPCOrder)
		out.gains[k] = f32(math.Sqrt(nrg))
		if in.warpingQ16 > 0 {
			out.gains[k] = f32(out.gains[k] * warpedGainFLP32(ar, warping, in.shapingLPCOrder))
		}
		silkBwexpanderFLP32(ar, in.shapingLPCOrder, bwExp)
		if in.warpingQ16 > 0 {
			warpedTrue2MonicCoefsFLP32(ar, warping, f32(3.999), in.shapingLPCOrder)
		} else {
			limitCoefsFLP32(ar, f32(3.999), in.shapingLPCOrder)
		}
	}

	// Gain tweaking
	gainMult := f32(math.Pow(2.0, f32(c32(-0.16)*snrAdjDB)))
	gainAdd := f32(math.Pow(2.0, f32(c32(0.16)*c32(minQGainDB))))
	for k := 0; k < in.nbSubfr; k++ {
		out.gains[k] = f32(out.gains[k] * gainMult)
		out.gains[k] = f32(out.gains[k] + gainAdd)
	}

	// Control low-frequency shaping and noise tilt
	strength = f32(c32(lowFreqShaping) * f32(1.0+f32(c32(lowQualityLFShapingDecr)*f32(f32(float64(in.inputQualityBandQ15[0])*(1.0/32768.0))-1.0))))
	strength = f32(strength * f32(float64(in.speechActivityQ8)*(1.0/256.0)))
	var tilt float64
	if in.signalType == SignalTypeVoiced {
		for k := 0; k < in.nbSubfr; k++ {
			b := f32(f32(c32(0.2)/float64(in.fsKHz)) + f32(3.0/float64(in.pitchL[k])))
			out.lfMAShp[k] = f32(-1.0 + b)
			out.lfARShp[k] = f32(f32(1.0-b) - f32(b*strength))
		}
		tilt = f32(-c32(hpNoiseCoef) - f32(f32(f32(f32(1-c32(hpNoiseCoef))*c32(harmHPNoiseCoef))*float64(in.speechActivityQ8))*(1.0/256.0)))
	} else {
		b := f32(c32(1.3) / float64(in.fsKHz))
		out.lfMAShp[0] = f32(-1.0 + b)
		out.lfARShp[0] = f32(f32(1.0-b) - f32(f32(b*strength)*c32(0.6)))
		for k := 1; k < in.nbSubfr; k++ {
			out.lfMAShp[k] = out.lfMAShp[0]
			out.lfARShp[k] = out.lfARShp[0]
		}
		tilt = -c32(hpNoiseCoef)
	}

	// HARMONIC SHAPING CONTROL
	harmShapeGain := 0.0
	if in.signalType == SignalTypeVoiced {
		harmShapeGain = c32(harmonicShaping)
		harmShapeGain = f32(harmShapeGain + f32(c32(highRateHarmonicShaping)*f32(1.0-f32(f32(1.0-out.codingQuality)*out.inputQuality))))
		harmShapeGain = f32(harmShapeGain * f32(math.Sqrt(in.ltpCorr)))
	}

	// Smooth over subframes
	for k := 0; k < in.nbSubfr; k++ {
		*harmSmth = f32(*harmSmth + f32(c32(subframeSmoothCoef)*f32(harmShapeGain-*harmSmth)))
		out.harmShapeGain[k] = *harmSmth
		*tiltSmth = f32(*tiltSmth + f32(c32(subframeSmoothCoef)*f32(tilt-*tiltSmth)))
		out.tilt[k] = *tiltSmth
	}
	return out
}

// applyShape32 overwrites the quantizer shaping fields of shape with the
// wrappers_FLP.c conversions of the float32 analysis (silk_float2int of the
// scaled float32 values) and the process_gains_FLP Lambda.
func (e *Encoder) applyShape32(shape *silkNoiseShapeAnalysis, signalType, quantOffset int) {
	s := &e.pendingShape32
	cfg := e.silkComplexityConfig()
	for k := 0; k < e.nSubframes; k++ {
		for j := 0; j < cfg.shapingLPCOrder; j++ {
			shape.AR_Q13[k][j] = int16(silkFloat2Int(f32(s.ar[k][j] * 8192)))
		}
		shape.LF_shp_Q14[k] = silkFloat2Int(f32(s.lfARShp[k]*16384))<<16 |
			int32(uint16(silkFloat2Int(f32(s.lfMAShp[k]*16384))))
		shape.Tilt_Q14[k] = silkFloat2Int(f32(s.tilt[k] * 16384))
		shape.HarmShapeGain_Q14[k] = silkFloat2Int(f32(s.harmShapeGain[k] * 16384))
	}
	shape.ShapingLPCOrder = cfg.shapingLPCOrder
	shape.Warping_Q16 = cfg.warpingQ16
	shape.InputQuality = s.inputQuality
	shape.CodingQuality = s.codingQuality
	quantOffsetF := f32(float64(silkQuantizationOffsetsQ10[signalType>>1][quantOffset]) / 1024.0)
	lambda := c32(lambdaOffset)
	lambda = f32(lambda + f32(c32(lambdaDelayedDecisions)*float64(cfg.nStatesDelayedDecision)))
	lambda = f32(lambda + f32(f32(c32(lambdaSpeechAct)*float64(e.speechActivityQ8))*(1.0/256.0)))
	lambda = f32(lambda + f32(c32(lambdaInputQuality)*s.inputQuality))
	lambda = f32(lambda + f32(c32(lambdaCodingQuality)*s.codingQuality))
	lambda = f32(lambda + f32(c32(lambdaQuantOffset)*quantOffsetF))
	shape.Lambda_Q10 = silkFloat2Int(f32(lambda * 1024))
}

// noiseShapeFLP32Trace runs the faithful analysis on the frame with the
// encoder's current state (once per frame, advancing the float32 smoothers).
func (e *Encoder) noiseShapeFLP32Trace(signal []float64, signalType int, pitchLags []int) silkNoiseShapeOutputs {
	cfg := e.silkComplexityConfig()
	fsKHz := e.sampleRate / 1000
	buf := e.noiseShapeAnalysisBuffer(signal, cfg.laShape)
	x := make([]float64, len(buf))
	for i, v := range buf {
		x[i] = f32(v * 32768)
	}
	ltpMem := e.ltpMemLength()
	pitchRes := make([]float64, e.frameSize)
	if len(e.pitchResidual) >= ltpMem+e.frameSize {
		copy(pitchRes, e.pitchResidual[ltpMem:ltpMem+e.frameSize])
	}
	pitchL := make([]int, e.nSubframes)
	for k := range pitchL {
		if k < len(pitchLags) && pitchLags[k] > 0 {
			pitchL[k] = pitchLags[k]
		} else {
			pitchL[k] = 1
		}
	}
	targetRate := e.targetRateBps
	if targetRate == 0 {
		targetRate = e.bitrate
	}
	if e.channels > 0 {
		targetRate /= e.channels
	}
	in := silkNoiseShapeInputs{
		fsKHz:               fsKHz,
		nbSubfr:             e.nSubframes,
		subfrLength:         e.frameSize / e.nSubframes,
		shapingLPCOrder:     cfg.shapingLPCOrder,
		laShape:             cfg.laShape,
		shapeWinLength:      silkSubframeLengthMS*fsKHz + 2*cfg.laShape,
		warpingQ16:          cfg.warpingQ16,
		useCBR:              e.rateMode == RateModeCBR,
		signalType:          signalType,
		snrDBQ7:             silkControlSNR(targetRate, fsKHz, e.nSubframes),
		speechActivityQ8:    e.speechActivityQ8,
		inputQualityBandQ15: e.inputQualityBandQ15,
		ltpCorr:             e.ltpCorrState,
		predGain:            e.pitchPredGain,
		pitchL:              pitchL,
	}
	return silkNoiseShapeAnalysisFLP32(in, x, pitchRes, &e.shapeHarmSmooth32, &e.shapeTiltSmooth32)
}
