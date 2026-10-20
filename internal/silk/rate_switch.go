package silk

// Mid-stream internal sample rate switch (silk/control_codec.c): when the
// Opus layer lets SILK switch (opusCanSwitch) but no prefill runs — the
// redundant frame did not fit, so silk_bw_switch was cleared — the main
// silk_Encode call's silk_control_encoder moves the rate itself.
// silk_setup_resamplers carries the input history across the switch by
// resampling x_buf up to the API rate and back down through the new input
// resampler, and silk_setup_fs resets only the shaping, NSQ, NLSF, LP
// filter and pitch state; everything else (VAD, rate control, LBRR, the
// stereo state, the transition state) carries on.

// CarryRateSwitchState makes e, a fresh encoder at the new rate configured
// like old, continue old's stream after silk_control_encoder's switch. It
// returns the new input resamplers (mid, then side for a stereo stream),
// already run over the resampled history; they are nil when the API rate
// equals the new SILK rate (the front end's delay line holds the history
// then).
func (e *Encoder) CarryRateSwitchState(old *Encoder) ([]*Resampler, error) {
	var out []*Resampler
	r, err := e.carryChannelRateSwitch(old, old.channels == 1 || old.streamChannels == 1)
	if err != nil {
		return nil, err
	}
	out = append(out, r)
	e.stereoState = old.stereoState
	e.prevOnlyMiddle = old.prevOnlyMiddle
	e.streamChannels = old.streamChannels
	e.prevStreamChannels = old.prevStreamChannels
	e.toMono = old.toMono
	e.nBitsExceeded = old.nBitsExceeded
	e.nBitsUsedLBRR = old.nBitsUsedLBRR
	e.timeSinceSwitchAllowedMs = old.timeSinceSwitchAllowedMs
	e.allowBandwidthSwitch = old.allowBandwidthSwitch
	e.SetDTX(old.useDTX)
	e.SetOpusActivity(VADActivity)
	if old.opusNoActivity {
		e.SetOpusActivity(VADNoActivity)
	}
	if e.side != nil && old.side != nil && old.streamChannels == 2 {
		r, err := e.side.carryChannelRateSwitch(old.side, false)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}

// carryChannelRateSwitch is silk_setup_resamplers + silk_setup_fs on one
// channel state. mono marks a channel coded from inputBuf + 1 by the front
// end (a mono stream): inputBuf[1] still holds the last old-rate sample
// (sStereo.sMid[1]), which leads the first new-rate frame.
func (e *Encoder) carryChannelRateSwitch(old *Encoder, mono bool) (*Resampler, error) {
	// State silk_setup_fs keeps.
	e.silkVAD = old.silkVAD
	e.speechActivity, e.speechActivityQ8 = old.speechActivity, old.speechActivityQ8
	e.inputTilt, e.inputTiltQ15 = old.inputTilt, old.inputTiltQ15
	e.inputQuality, e.inputQualityB, e.inputQualityBandQ15 = old.inputQuality, old.inputQualityB, old.inputQualityBandQ15
	e.variableHPSmth1Q15 = old.variableHPSmth1Q15
	e.noSpeechCounter, e.inDTX, e.lastPacketDTX = old.noSpeechCounter, old.inDTX, old.lastPacketDTX
	e.lastQuantOffsetType = old.lastQuantOffsetType
	e.prevLagIndex = old.prevLagIndex
	e.frameCounter = old.frameCounter
	e.ltpCorrState = old.ltpCorrState
	e.ltpSumLogGainQ7 = old.ltpSumLogGainQ7
	e.lbrrEnabledPrev = old.lbrrEnabledPrev
	e.lbrrGainIncreases = old.lbrrGainIncreases
	e.lbrrPrevLastGainIndex = old.lbrrPrevLastGainIndex
	e.lbrrFlag = old.lbrrFlag
	e.pendingLBRR = old.pendingLBRR
	e.pendingLBRRFrames = old.pendingLBRRFrames
	e.pendingLBRRStereoPred = old.pendingLBRRStereoPred
	e.pendingLBRRStereoMidOnly = old.pendingLBRRStereoMidOnly
	e.inputBuf = old.inputBuf
	// The transition state carries on; only its filter state is reset.
	e.lp = old.lp
	e.lp.InLPState = [2]int32{}

	// silk_setup_resamplers: x_buf (buf_length_ms of it) to int16, up to the
	// API rate through a temporary decoder-direction resampler, then down
	// through the freshly initialised encoder resampler, whose state is the
	// new channel's input resampler.
	api := old.apiSampleRate
	if api == 0 {
		api = old.sampleRate
	}
	bufMs := 2*old.nSubframes*5 + silkLAShapeMs
	oldN := bufMs * old.sampleRate / 1000
	newN := bufMs * e.sampleRate / 1000
	xFIX := make([]int16, oldN)
	for i := range xFIX {
		var v float64
		if i < len(old.xBuf) {
			v = old.xBuf[i] * 32768
		}
		xFIX[i] = silkFloat2Int16Even(v)
	}
	// Even at equal rates the decoder-direction resampler delays its
	// input (delay_matrix_dec).
	tmp, err := NewResampler(old.sampleRate, api)
	if err != nil {
		return nil, err
	}
	xAPI := tmp.Process(xFIX)
	var res *Resampler
	var xNew []int16
	var line []float64
	if mono {
		var last float64
		if len(old.encInputDelay) > 0 {
			last = old.encInputDelay[0]
		}
		line = append(line, last)
	}
	if api == e.sampleRate {
		// The front end's delay line is the equal-rate encoder resampler.
		d := silkEncResamplerDelay(e.sampleRate / 1000)
		xNew = make([]int16, len(xAPI))
		copy(xNew[min(d, len(xNew)):], xAPI)
		for i := 0; i < d; i++ {
			var v int16
			if k := len(xAPI) - d + i; k >= 0 {
				v = xAPI[k]
			}
			line = append(line, float64(v)/32768)
		}
	} else {
		r, err := NewEncoderResampler(api, e.sampleRate)
		if err != nil {
			return nil, err
		}
		xNew = r.Process(xAPI)
		res = r
	}
	e.encInputDelay = line
	for i := 0; i < newN && i < len(e.xBuf); i++ {
		var v int16
		if i < len(xNew) {
			v = xNew[i]
		}
		e.xBuf[i] = float64(v) / 32768
	}
	e.pitchHist = e.xBuf[:e.ltpMemLength()]
	return res, nil
}
