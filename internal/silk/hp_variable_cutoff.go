package silk

// Variable high-pass cutoff adaptation (silk/HP_variable_cutoff.c). The SILK
// encoder tracks the low end of the pitch frequency range in
// variable_HP_smth1_Q15; the Opus layer smooths it again
// (variable_HP_smth2_Q15) and drives hp_cutoff with the result.

const (
	variableHPSmthCoef1Q16   = 6554 // SILK_FIX_CONST(VARIABLE_HP_SMTH_COEF1 = 0.1, 16)
	variableHPMaxDeltaFreqQ7 = 51   // SILK_FIX_CONST(VARIABLE_HP_MAX_DELTA_FREQ = 0.4, 7)
	variableHPMinCutoffHz    = 60   // VARIABLE_HP_MIN_CUTOFF_HZ
	variableHPMaxCutoffHz    = 100  // VARIABLE_HP_MAX_CUTOFF_HZ
)

// Lin2Log ports silk_lin2log (128*log2(x) in Q7) for the Opus layer.
func Lin2Log(inLin int32) int32 { return silkLin2Log(inLin) }

// Log2Lin ports silk_log2lin (2^(x/128)) for the Opus layer.
func Log2Lin(inLogQ7 int32) int32 { return silkLog2Lin(inLogQ7) }

// variableHPSmth1Initial mirrors silk_init_encoder:
// silk_LSHIFT(silk_lin2log(SILK_FIX_CONST(60, 16)) - (16 << 7), 8).
func variableHPSmth1Initial() int32 {
	return (silkLin2Log(variableHPMinCutoffHz<<16) - (16 << 7)) << 8
}

// VariableHPSmth1Q15 returns the encoder's smoothed log2 high-pass cutoff
// (silk_encoder_state.variable_HP_smth1_Q15), read by the Opus layer before
// each packet like opus_encode_native.
func (e *Encoder) VariableHPSmth1Q15() int32 {
	return e.variableHPSmth1Q15
}

// hpVariableCutoff ports silk_HP_variable_cutoff. It runs once per frame
// before the frame's VAD result is installed, so prevSignalType, prevLag,
// speech_activity_Q8, and input_quality_bands_Q15[0] all describe the
// previous frame, as in silk_Encode.
func (e *Encoder) hpVariableCutoff() {
	if e.prevSignalType != SignalTypeVoiced || e.prevLagForPitch <= 0 {
		return
	}
	fsKHz := int32(e.sampleRate / 1000)
	pitchFreqHzQ16 := (fsKHz * 1000 << 16) / int32(e.prevLagForPitch)
	pitchFreqLogQ7 := silkLin2Log(pitchFreqHzQ16) - (16 << 7)

	// adjustment based on quality
	qualityQ15 := int32(e.inputQualityBandQ15[0])
	minLogQ7 := silkLin2Log(variableHPMinCutoffHz<<16) - (16 << 7)
	pitchFreqLogQ7 = silkSMLAWB(pitchFreqLogQ7, silkSMULWB(-qualityQ15<<2, int16(qualityQ15)),
		int16(pitchFreqLogQ7-minLogQ7))

	// delta_freq = pitch_freq_log - variable_HP_smth1
	deltaFreqQ7 := pitchFreqLogQ7 - e.variableHPSmth1Q15>>8
	if deltaFreqQ7 < 0 {
		// less smoothing for decreasing pitch frequency, to track something
		// close to the minimum
		deltaFreqQ7 *= 3
	}
	if deltaFreqQ7 < -variableHPMaxDeltaFreqQ7 {
		deltaFreqQ7 = -variableHPMaxDeltaFreqQ7
	} else if deltaFreqQ7 > variableHPMaxDeltaFreqQ7 {
		deltaFreqQ7 = variableHPMaxDeltaFreqQ7
	}

	// update smoother
	e.variableHPSmth1Q15 = silkSMLAWB(e.variableHPSmth1Q15,
		silkSMULBB(int32(e.speechActivityQ8), deltaFreqQ7), variableHPSmthCoef1Q16)

	// limit frequency range
	lo := silkLin2Log(variableHPMinCutoffHz) << 8
	hi := silkLin2Log(variableHPMaxCutoffHz) << 8
	if e.variableHPSmth1Q15 < lo {
		e.variableHPSmth1Q15 = lo
	} else if e.variableHPSmth1Q15 > hi {
		e.variableHPSmth1Q15 = hi
	}
}
