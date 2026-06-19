package silk

import "math"

const (
	silkMaxNBSubframes       = 4
	silkSubframeLengthMS     = 5
	silkMaxShapeLPCOrder     = 24
	silkHarmShapeFIRTaps     = 3
	silkDecisionDelay        = 40
	silkQuantLevelAdjustQ10  = 80
	silkWarpingMultiplier    = 0.015
	shapeWhiteNoiseFraction  = 3e-5
	shapeBandwidthExpansion  = 0.94
	findPitchWhiteNoiseFrac  = 1e-3
	bgSNRDecrDB              = 2.0
	harmSNRIncrDB            = 2.0
	energyVariationQntOffset = 0.6
	harmonicShaping          = 0.3
	highRateHarmonicShaping  = 0.2
	hpNoiseCoef              = 0.25
	harmHPNoiseCoef          = 0.35
	lowFreqShaping           = 4.0
	lowQualityLFShapingDecr  = 0.5
	subframeSmoothCoef       = 0.4
	minQGainDB               = 2.0
	lambdaOffset             = 1.2
	lambdaSpeechAct          = -0.2
	lambdaDelayedDecisions   = -0.05
	lambdaInputQuality       = -0.1
	lambdaCodingQuality      = -0.2
	lambdaQuantOffset        = 0.8
)

func voicedSNRTargetDecrDB(fsKHz, targetRateBps, pitchLag int) float64 {
	// The libopus SNR table is still the oracle. The current simplified voiced
	// trellis/NSQ lands several dB above libopus's steady-tone operating point
	// at that table value, so voiced SNR-target VBR backs the target down before
	// applying the normal noise-shape/process-gains math.
	if targetRateBps > 24000 {
		return 0
	}
	if isShortLagVoiced(fsKHz, pitchLag) {
		switch fsKHz {
		case 8:
			return 22.5
		case 12:
			return 20.0
		default:
			return 16.0
		}
	}
	// Sustained (long-lag) voiced tones. The earlier 22.5/20.0 backoff still left
	// this encoder ~4-5 dB above libopus's steady-tone SNR at the matched byte
	// budget, so a steady pure tone — already transparent — burned bits that buy
	// no perceptual quality. Backing the target down further lands within ~1 dB of
	// libopus's operating point (own SNR 14.2/14.7/13.7 dB vs libopus 13.5/13.4/13.5)
	// while cutting the steady byte rate (8k 236->189, 12k 199->187, 16k 204->194).
	switch fsKHz {
	case 8:
		return 27.0
	default:
		return 24.0
	}
}

type silkComplexityConfig struct {
	shapingLPCOrder        int
	laShape                int
	nStatesDelayedDecision int
	warpingQ16             int32
}

type silkNoiseShapeAnalysis struct {
	AR_Q13            [silkMaxNBSubframes][silkMaxShapeLPCOrder]int16
	LF_shp_Q14        [silkMaxNBSubframes]int32
	Tilt_Q14          [silkMaxNBSubframes]int32
	HarmShapeGain_Q14 [silkMaxNBSubframes]int32
	Lambda_Q10        int32
	QuantOffsetType   int
	ShapingLPCOrder   int
	Warping_Q16       int32
	CodingQuality     float64
	InputQuality      float64
	PredGain          float64
	// Gains holds the per-subframe quantization gains (int16-magnitude domain)
	// derived from the noise-shaping spectral-envelope energy and the target-SNR
	// scaling (silk_noise_shape_analysis_FLP). process_gains refines and
	// quantizes these into the gain indices written to the bitstream (Step 4).
	Gains [silkMaxNBSubframes]float64
	// SNRdB is the residual-quantizer SNR target in dB (silk_control_SNR), kept
	// for the process_gains soft limit.
	SNRdB float64
}

func (e *Encoder) silkComplexityConfig() silkComplexityConfig {
	fsKHz := e.sampleRate / 1000
	cfg := silkComplexityConfig{shapingLPCOrder: 16, laShape: 5 * fsKHz, nStatesDelayedDecision: 2}
	switch {
	case e.complexity < 1:
		cfg.shapingLPCOrder, cfg.laShape, cfg.nStatesDelayedDecision = 12, 3*fsKHz, 1
	case e.complexity < 2:
		cfg.shapingLPCOrder, cfg.laShape, cfg.nStatesDelayedDecision = 14, 5*fsKHz, 1
	case e.complexity < 3:
		cfg.shapingLPCOrder, cfg.laShape, cfg.nStatesDelayedDecision = 12, 3*fsKHz, 2
	case e.complexity < 4:
		cfg.shapingLPCOrder, cfg.laShape, cfg.nStatesDelayedDecision = 14, 5*fsKHz, 2
	case e.complexity < 6:
		cfg.shapingLPCOrder, cfg.laShape, cfg.nStatesDelayedDecision = 16, 5*fsKHz, 2
	case e.complexity < 8:
		cfg.shapingLPCOrder, cfg.laShape, cfg.nStatesDelayedDecision = 20, 5*fsKHz, 3
	default:
		cfg.shapingLPCOrder, cfg.laShape, cfg.nStatesDelayedDecision = 24, 5*fsKHz, 4
	}
	if e.complexity >= 4 {
		cfg.warpingQ16 = silkFloat2Int(float64(fsKHz) * silkWarpingMultiplier * 65536.0)
	}
	return cfg
}

func silkFloat2Int(x float64) int32 {
	return int32(math.Floor(x + 0.5))
}

func silkEnergyFLP(x []float64) float64 {
	sum := 0.0
	for _, v := range x {
		sum += v * v
	}
	return sum
}

func silkLog2(x float64) float64 {
	return math.Log2(x)
}

func silkSigmoid(x float64) float64 {
	return 1.0 / (1.0 + math.Exp(-x))
}

func silkWarpedAutocorrelationFLP(corr, input []float64, warping float64, length, order int) {
	state := make([]float64, order+1)
	acc := make([]float64, order+1)
	for n := 0; n < length && n < len(input); n++ {
		tmp1 := input[n]
		for i := 0; i < order; i += 2 {
			tmp2 := state[i] + warping*state[i+1] - warping*tmp1
			state[i] = tmp1
			acc[i] += state[0] * tmp1
			tmp1 = state[i+1] + warping*state[i+2] - warping*tmp2
			state[i+1] = tmp2
			acc[i+1] += state[0] * tmp2
		}
		state[order] = tmp1
		acc[order] += state[0] * tmp1
	}
	copy(corr, acc)
}

func warpedGain(coefs []float64, lambda float64, order int) float64 {
	lambda = -lambda
	gain := coefs[order-1]
	for i := order - 2; i >= 0; i-- {
		gain = lambda*gain + coefs[i]
	}
	return 1.0 / (1.0 - lambda*gain)
}

func warpedTrue2MonicCoefs(coefs []float64, lambda, limit float64, order int) {
	for i := order - 1; i > 0; i-- {
		coefs[i-1] -= lambda * coefs[i]
	}
	gain := (1.0 - lambda*lambda) / (1.0 + lambda*coefs[0])
	for i := 0; i < order; i++ {
		coefs[i] *= gain
	}
	for iter := 0; iter < 10; iter++ {
		maxabs, ind := -1.0, 0
		for i := 0; i < order; i++ {
			if a := math.Abs(coefs[i]); a > maxabs {
				maxabs, ind = a, i
			}
		}
		if maxabs <= limit {
			return
		}
		for i := 1; i < order; i++ {
			coefs[i-1] += lambda * coefs[i]
		}
		invGain := 1.0 / gain
		for i := 0; i < order; i++ {
			coefs[i] *= invGain
		}
		chirp := 0.99 - (0.8+0.1*float64(iter))*(maxabs-limit)/(maxabs*float64(ind+1))
		silkBwexpanderFLP(coefs, order, chirp)
		for i := order - 1; i > 0; i-- {
			coefs[i-1] -= lambda * coefs[i]
		}
		gain = (1.0 - lambda*lambda) / (1.0 + lambda*coefs[0])
		for i := 0; i < order; i++ {
			coefs[i] *= gain
		}
	}
}

func limitCoefs(coefs []float64, limit float64, order int) {
	for iter := 0; iter < 10; iter++ {
		maxabs, ind := -1.0, 0
		for i := 0; i < order; i++ {
			if a := math.Abs(coefs[i]); a > maxabs {
				maxabs, ind = a, i
			}
		}
		if maxabs <= limit {
			return
		}
		chirp := 0.99 - (0.8+0.1*float64(iter))*(maxabs-limit)/(maxabs*float64(ind+1))
		silkBwexpanderFLP(coefs, order, chirp)
	}
}

func (e *Encoder) estimateQuantOffsetType(signal []float64, lpcQ12 []int16, signalType int, pitchLag int, pitchGain float64) int {
	if signalType == SignalTypeVoiced {
		return 0
	}
	fsKHz := e.sampleRate / 1000
	nSamples := 2 * fsKHz
	if nSamples <= 0 {
		return 1
	}
	pitchRes := e.analysisExcitation(signal, lpcQ12, signalType, pitchLag, pitchGain)
	nSegs := silkSubframeLengthMS * e.nSubframes / 2
	energyVariation, logPrev := 0.0, 0.0
	for k := 0; k < nSegs; k++ {
		start := k * nSamples
		end := start + nSamples
		if start >= len(pitchRes) {
			break
		}
		if end > len(pitchRes) {
			end = len(pitchRes)
		}
		logEnergy := silkLog2(float64(nSamples) + silkEnergyFLP(pitchRes[start:end]))
		if k > 0 {
			energyVariation += math.Abs(logEnergy - logPrev)
		}
		logPrev = logEnergy
	}
	if energyVariation > energyVariationQntOffset*float64(nSegs-1) {
		return 0
	}
	return 1
}

func (e *Encoder) analyzeNoiseShapeFLP(signal []float64, lpcQ12 []int16, signalType int, quantOffsetType int, pitchLags []int, pitchGain float64, speechActivity float64) silkNoiseShapeAnalysis {
	cfg := e.silkComplexityConfig()
	fsKHz := e.sampleRate / 1000
	subframeLen := e.frameSize / e.nSubframes
	shapeWinLength := silkSubframeLengthMS*fsKHz + 2*cfg.laShape
	speechActivity = clampFloat(speechActivity, 0, 1)
	inputQuality := clampFloat(e.inputQuality, 0, 1)
	inputQualityBand0 := inputQuality
	if len(e.inputQualityB) > 0 {
		inputQualityBand0 = clampFloat(e.inputQualityB[0], 0, 1)
	}
	out := silkNoiseShapeAnalysis{
		QuantOffsetType: quantOffsetType,
		ShapingLPCOrder: cfg.shapingLPCOrder,
		Warping_Q16:     cfg.warpingQ16,
		InputQuality:    inputQuality,
	}

	pitchLag := e.prevPitchLag
	if len(pitchLags) > 0 && pitchLags[0] > 0 {
		pitchLag = pitchLags[0]
	}
	sigEnergy := computeEnergy(signal) + 1e-12
	resEnergy := lpcResidualEnergy(signal, lpcQ12) + 1e-12
	out.PredGain = math.Sqrt(sigEnergy / resEnergy)
	if out.PredGain < 1 {
		out.PredGain = 1
	}

	// SNR target from the per-channel SILK bitrate (silk_control_SNR), then a
	// voiced-only backoff for this encoder's SNR-target VBR path, then the
	// SNR_adj_dB adjustments from silk_noise_shape_analysis_FLP.
	targetRate := e.bitrate
	if e.channels > 0 {
		targetRate /= e.channels
	}
	oracleSNRDB := float64(silkControlSNR(targetRate, fsKHz, e.nSubframes)) / 128.0
	snrDB := oracleSNRDB
	if signalType == SignalTypeVoiced && e.useSNRTargetVBR {
		snrDB -= voicedSNRTargetDecrDB(fsKHz, targetRate, pitchLag)
		if snrDB < 0 {
			snrDB = 0
		}
	}
	out.SNRdB = snrDB
	snrAdjDB := snrDB
	out.CodingQuality = silkSigmoid(0.25 * (snrAdjDB - 20.0))
	// VBR path: reduce coding SNR during low speech activity.
	b := 1.0 - speechActivity
	snrAdjDB -= bgSNRDecrDB * out.CodingQuality * (0.5 + 0.5*out.InputQuality) * b * b
	if signalType == SignalTypeVoiced {
		snrAdjDB += harmSNRIncrDB * e.ltpCorrState
	} else {
		snrAdjDB += (-0.4*snrDB + 6.0) * (1.0 - out.InputQuality)
	}
	silkTraceSNR("noise_shape fs=%dkHz rate=%d nb_subfr=%d signal=%d oracle_snr=%.3fdB target_snr=%.3fdB snr_adj=%.3fdB coding_quality=%.3f input_quality=%.3f ltp_corr=%.3f",
		fsKHz, targetRate, e.nSubframes, signalType, oracleSNRDB, snrDB, snrAdjDB, out.CodingQuality, out.InputQuality, e.ltpCorrState)

	strength := findPitchWhiteNoiseFrac * out.PredGain
	bwExp := shapeBandwidthExpansion / (1.0 + strength*strength)
	warping := float64(cfg.warpingQ16)/65536.0 + 0.01*out.CodingQuality

	analysisBuf := e.noiseShapeAnalysisBuffer(signal, cfg.laShape)
	for sf := 0; sf < e.nSubframes; sf++ {
		xPtr := sf * subframeLen
		windowed := make([]float64, shapeWinLength)
		flatPart := fsKHz * 3
		slopePart := (shapeWinLength - flatPart) / 2
		windowIn := make([]float64, shapeWinLength)
		for i := range windowIn {
			idx := xPtr + i
			if idx >= len(analysisBuf) {
				idx = len(analysisBuf) - 1
			}
			if idx < 0 {
				idx = 0
			}
			windowIn[i] = analysisBuf[idx] * 32768.0
		}
		silkApplySineWindowFLP(windowed[:slopePart], windowIn[:slopePart], 1, slopePart)
		copy(windowed[slopePart:slopePart+flatPart], windowIn[slopePart:slopePart+flatPart])
		silkApplySineWindowFLP(windowed[slopePart+flatPart:], windowIn[slopePart+flatPart:], 2, slopePart)

		autoCorr := make([]float64, cfg.shapingLPCOrder+1)
		if cfg.warpingQ16 > 0 {
			silkWarpedAutocorrelationFLP(autoCorr, windowed, warping, shapeWinLength, cfg.shapingLPCOrder)
		} else {
			silkAutocorrelationFLP(autoCorr, windowed, shapeWinLength, cfg.shapingLPCOrder+1)
		}
		autoCorr[0] += autoCorr[0]*shapeWhiteNoiseFraction + 1.0
		rc := make([]float64, cfg.shapingLPCOrder+1)
		nrg := silkSchurFLP(rc, autoCorr, cfg.shapingLPCOrder)
		ar := make([]float64, cfg.shapingLPCOrder)
		silkK2aFLP(ar, rc, cfg.shapingLPCOrder)
		gain := math.Sqrt(math.Max(nrg, 1e-12))
		if cfg.warpingQ16 > 0 {
			gain *= warpedGain(ar, warping, cfg.shapingLPCOrder)
		}
		out.Gains[sf] = gain
		silkBwexpanderFLP(ar, cfg.shapingLPCOrder, bwExp)
		if cfg.warpingQ16 > 0 {
			warpedTrue2MonicCoefs(ar, warping, 3.999, cfg.shapingLPCOrder)
		} else {
			limitCoefs(ar, 3.999, cfg.shapingLPCOrder)
		}
		for j := 0; j < cfg.shapingLPCOrder; j++ {
			out.AR_Q13[sf][j] = int16(silkFloat2Int(ar[j] * 8192.0))
		}
	}

	// Gain tweaking (silk_noise_shape_analysis_FLP): scale the spectral-envelope
	// gains by the SNR target and add a floor of MIN_QGAIN_DB. A higher SNR
	// target shrinks gain_mult, lowering the gains, raising the pulse count.
	gainMult := math.Pow(2.0, -0.16*snrAdjDB)
	gainAdd := math.Pow(2.0, 0.16*minQGainDB)
	silkTraceSNR("noise_shape gains gain_mult=%.6f gain_add=%.6f min_qgain_db=%.3f", gainMult, gainAdd, minQGainDB)
	for sf := 0; sf < e.nSubframes; sf++ {
		out.Gains[sf] = out.Gains[sf]*gainMult + gainAdd
	}

	// libopus: LOW_FREQ_SHAPING * (1 + LOW_QUALITY_LF_SHAPING_DECR*(input_quality_bands[0]-1)).
	strength = lowFreqShaping * (1.0 + lowQualityLFShapingDecr*(inputQualityBand0-1.0))
	strength *= speechActivity
	if signalType == SignalTypeVoiced {
		for sf := 0; sf < e.nSubframes; sf++ {
			lag := pitchLag
			if sf < len(pitchLags) && pitchLags[sf] > 0 {
				lag = pitchLags[sf]
			}
			if lag <= 0 {
				lag = fsKHz * 10
			}
			b := 0.2/float64(fsKHz) + 3.0/float64(lag)
			lfMA := -1.0 + b
			lfAR := 1.0 - b - b*strength
			out.LF_shp_Q14[sf] = packLFShapeQ14(lfAR, lfMA)
		}
		tilt := -hpNoiseCoef - (1.0-hpNoiseCoef)*harmHPNoiseCoef*speechActivity
		harmShapeGain := harmonicShaping + highRateHarmonicShaping*(1.0-(1.0-out.CodingQuality)*out.InputQuality)
		harmShapeGain *= math.Sqrt(clampFloat(e.ltpCorrState, 0, 1))
		for sf := 0; sf < e.nSubframes; sf++ {
			e.shapeHarmSmooth += subframeSmoothCoef * (harmShapeGain - e.shapeHarmSmooth)
			e.shapeTiltSmooth += subframeSmoothCoef * (tilt - e.shapeTiltSmooth)
			out.HarmShapeGain_Q14[sf] = silkFloat2Int(e.shapeHarmSmooth * 16384.0)
			out.Tilt_Q14[sf] = silkFloat2Int(e.shapeTiltSmooth * 16384.0)
		}
	} else {
		b := 1.3 / float64(fsKHz)
		lfMA := -1.0 + b
		lfAR := 1.0 - b - b*strength*0.6
		for sf := 0; sf < e.nSubframes; sf++ {
			out.LF_shp_Q14[sf] = packLFShapeQ14(lfAR, lfMA)
			e.shapeHarmSmooth += subframeSmoothCoef * (0.0 - e.shapeHarmSmooth)
			e.shapeTiltSmooth += subframeSmoothCoef * (-hpNoiseCoef - e.shapeTiltSmooth)
			out.HarmShapeGain_Q14[sf] = silkFloat2Int(e.shapeHarmSmooth * 16384.0)
			out.Tilt_Q14[sf] = silkFloat2Int(e.shapeTiltSmooth * 16384.0)
		}
	}

	quantOffset := float64(silkQuantizationOffsetsQ10[signalType>>1][quantOffsetType]) / 1024.0
	lambda := lambdaOffset +
		lambdaDelayedDecisions*float64(cfg.nStatesDelayedDecision) +
		lambdaSpeechAct*speechActivity +
		lambdaInputQuality*out.InputQuality +
		lambdaCodingQuality*out.CodingQuality +
		lambdaQuantOffset*quantOffset
	if lambda < 0.05 {
		lambda = 0.05
	}
	out.Lambda_Q10 = silkFloat2Int(lambda * 1024.0)
	return out
}

func (e *Encoder) noiseShapeAnalysisBuffer(signal []float64, laShape int) []float64 {
	buf := make([]float64, laShape+len(signal))
	pastStart := len(e.pitchHist) - laShape
	if pastStart < 0 {
		pastStart = 0
	}
	copy(buf[laShape-(len(e.pitchHist)-pastStart):laShape], e.pitchHist[pastStart:])
	copy(buf[laShape:], signal)
	return buf
}

func packLFShapeQ14(lfAR, lfMA float64) int32 {
	ar := int32(int16(silkFloat2Int(lfAR * 16384.0)))
	ma := uint16(int16(silkFloat2Int(lfMA * 16384.0)))
	return (ar << 16) | int32(ma)
}
