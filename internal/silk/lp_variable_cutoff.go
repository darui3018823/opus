package silk

// Variable low-pass filter and internal sample rate switching of the SILK
// encoder (silk/LP_variable_cutoff.c, silk/control_audio_bandwidth.c,
// silk/biquad_alt.c). A bandwidth change is smoothed by an elliptic
// low-pass whose cutoff moves over TRANSITION_FRAMES frames, interpolating
// between five fixed filters; the state machine decides when the internal
// rate itself may switch (switchReady, acted on by the Opus layer).

const (
	silkTransitionTimeMs  = 5120
	silkTransitionNB      = 3
	silkTransitionNA      = 2
	silkTransitionIntNum  = 5
	silkTransitionFrames  = silkTransitionTimeMs / 20
	silkTransitionIntStep = silkTransitionFrames / (silkTransitionIntNum - 1)
)

var silkTransitionLPBQ28 = [silkTransitionIntNum][silkTransitionNB]int32{
	{250767114, 501534038, 250767114},
	{209867381, 419732057, 209867381},
	{170987846, 341967853, 170987846},
	{131531482, 263046905, 131531482},
	{89306658, 178584282, 89306658},
}

var silkTransitionLPAQ28 = [silkTransitionIntNum][silkTransitionNA]int32{
	{506393414, 239854379},
	{411067935, 169683996},
	{306733530, 116694253},
	{185807084, 77959395},
	{35497197, 57401098},
}

// LPState is silk_LP_state: the transition filter memory, the frame counter
// of the running transition and the direction (mode: 0 idle, 1 opening
// after a switch up, -2 closing towards a switch down). A re-init that
// keeps the transition (prefill 2) carries it to the new encoder; the
// previous rate (saved_fs_kHz) is the old encoder's, which runs the switch
// decision before the re-init.
type LPState struct {
	InLPState         [2]int32
	TransitionFrameNo int
	Mode              int
}

// LPState returns the encoder's transition filter state.
func (e *Encoder) LPState() LPState { return e.lp }

// SetLPState restores a transition filter state saved from another
// encoder instance (silk_Encode prefill 2 re-initialises the encoder but
// keeps sLP, with the old rate in saved_fs_kHz).
func (e *Encoder) SetLPState(st LPState) {
	e.lp = st
	if e.side != nil {
		e.side.lp = st
	}
}

// lpInterpolateFilterTaps is silk_LP_interpolate_filter_taps.
func lpInterpolateFilterTaps(ind int, facQ16 int32) (b [silkTransitionNB]int32, a [silkTransitionNA]int32) {
	if ind < silkTransitionIntNum-1 {
		if facQ16 > 0 {
			if facQ16 < 32768 {
				// Piece-wise linear interpolation of B and A.
				for nb := 0; nb < silkTransitionNB; nb++ {
					b[nb] = silkSMLAWB(silkTransitionLPBQ28[ind][nb], silkTransitionLPBQ28[ind+1][nb]-silkTransitionLPBQ28[ind][nb], int16(facQ16))
				}
				for na := 0; na < silkTransitionNA; na++ {
					a[na] = silkSMLAWB(silkTransitionLPAQ28[ind][na], silkTransitionLPAQ28[ind+1][na]-silkTransitionLPAQ28[ind][na], int16(facQ16))
				}
			} else {
				// fac_Q16 - (1 << 16) is in range of a 16-bit int.
				for nb := 0; nb < silkTransitionNB; nb++ {
					b[nb] = silkSMLAWB(silkTransitionLPBQ28[ind+1][nb], silkTransitionLPBQ28[ind+1][nb]-silkTransitionLPBQ28[ind][nb], int16(facQ16-(1<<16)))
				}
				for na := 0; na < silkTransitionNA; na++ {
					a[na] = silkSMLAWB(silkTransitionLPAQ28[ind+1][na], silkTransitionLPAQ28[ind+1][na]-silkTransitionLPAQ28[ind][na], int16(facQ16-(1<<16)))
				}
			}
		} else {
			b = silkTransitionLPBQ28[ind]
			a = silkTransitionLPAQ28[ind]
		}
	} else {
		b = silkTransitionLPBQ28[silkTransitionIntNum-1]
		a = silkTransitionLPAQ28[silkTransitionIntNum-1]
	}
	return b, a
}

// silkBiquadAltStride1 is silk_biquad_alt_stride1: a direct form II
// transposed biquad with a two-element state, in place on int16 samples.
func silkBiquadAltStride1(x []int16, b [silkTransitionNB]int32, a [silkTransitionNA]int32, s *[2]int32) {
	// Negate A_Q28 values and split in two parts.
	a0L := (-a[0]) & 0x00003FFF
	a0U := (-a[0]) >> 14
	a1L := (-a[1]) & 0x00003FFF
	a1U := (-a[1]) >> 14
	for k := range x {
		inval := x[k]
		out32Q14 := silkSMLAWB(s[0], b[0], inval) << 2
		s[0] = s[1] + silkRSHIFTRound(silkSMULWB(out32Q14, int16(a0L)), 14)
		s[0] = silkSMLAWB(s[0], out32Q14, int16(a0U))
		s[0] = silkSMLAWB(s[0], b[1], inval)
		s[1] = silkRSHIFTRound(silkSMULWB(out32Q14, int16(a1L)), 14)
		s[1] = silkSMLAWB(s[1], out32Q14, int16(a1U))
		s[1] = silkSMLAWB(s[1], b[2], inval)
		// Scale back to Q0 and saturate.
		x[k] = silkSAT16((out32Q14 + (1 << 14) - 1) >> 14)
	}
}

// silkRSHIFTRound is silk_RSHIFT_ROUND for shift > 1.
func silkRSHIFTRound(a int32, shift uint) int32 {
	return ((a >> (shift - 1)) + 1) >> 1
}

// lpVariableCutoff is silk_LP_variable_cutoff on one channel's frame
// (inputBuf + 1 in silk_encode_frame_FLP): while a transition runs it
// filters the frame with the interpolated low-pass and advances the
// transition counter by the mode.
func (e *Encoder) lpVariableCutoff(frame []int16) {
	if e.lp.Mode == 0 {
		return
	}
	// Calculate index and interpolation factor for interpolation.
	facQ16 := int32(silkTransitionFrames-e.lp.TransitionFrameNo) << (16 - 6)
	ind := int(facQ16 >> 16)
	facQ16 -= int32(ind) << 16
	b, a := lpInterpolateFilterTaps(ind, facQ16)
	// Update transition frame number for next frame.
	e.lp.TransitionFrameNo += e.lp.Mode
	if e.lp.TransitionFrameNo < 0 {
		e.lp.TransitionFrameNo = 0
	} else if e.lp.TransitionFrameNo > silkTransitionFrames {
		e.lp.TransitionFrameNo = silkTransitionFrames
	}
	silkBiquadAltStride1(frame, b, a, &e.lp.InLPState)
}

// lpFilterFrame applies lpVariableCutoff to a frame held as int16/32768
// floats (the front-end output), in place.
func (e *Encoder) lpFilterFrame(frame []float64) {
	if e.lp.Mode == 0 {
		return
	}
	x := floatFrameToInt16(frame)
	e.lpVariableCutoff(x)
	for i, v := range x {
		frame[i] = float64(v) / 32768
	}
}

// ControlAudioBandwidth is silk_control_audio_bandwidth for the packet
// about to be coded: given the Opus layer's desired internal rate (from the
// coded bandwidth) and whether Opus lets the rate switch now
// (opusCanSwitch, the packet after switchReady), it runs the transition
// state machine and returns the internal rate the encoder must run at and
// whether the Opus layer should prepare a switch (switchReady: the SILK
// bit budget of this packet, payloadMs long, is reduced to leave room for
// redundancy). A
// returned rate other than the encoder's means a re-init at that rate,
// carrying LPState (prefill 2; libopus runs this pass on the re-initialised
// state with saved_fs_kHz, the Go caller runs it on the old encoder). The
// side channel of a stereo stream runs the machine on its own state too
// (silk_control_encoder per channel, the rate forced to the mid's), and
// each channel reporting switchReady takes the redundancy room off maxBits.
func (e *Encoder) ControlAudioBandwidth(desiredFsHz int, opusCanSwitch bool, payloadMs int) (fsKHz int, switchReady bool) {
	if payloadMs <= 0 {
		payloadMs = e.frameMs
	}
	fsKHz, switchReady = e.controlAudioBandwidthState(desiredFsHz, opusCanSwitch)
	if switchReady {
		// Make room for redundancy.
		e.maxBits -= e.maxBits * 5 / (payloadMs + 5)
	}
	if e.channels == 2 && e.streamChannels == 2 && e.side != nil {
		if _, ready := e.side.controlAudioBandwidthState(desiredFsHz, opusCanSwitch); ready {
			e.maxBits -= e.maxBits * 5 / (payloadMs + 5)
			switchReady = true
		}
	}
	return fsKHz, switchReady
}

// controlAudioBandwidthState is the per-channel state machine of
// silk_control_audio_bandwidth on this channel's transition state.
func (e *Encoder) controlAudioBandwidthState(desiredFsHz int, opusCanSwitch bool) (fsKHz int, switchReady bool) {
	origKHz := e.sampleRate / 1000
	fsKHz = origKHz
	fsHz := fsKHz * 1000
	apiFsHz := e.apiSampleRate
	if apiFsHz == 0 {
		apiFsHz = e.sampleRate
	}
	const maxInternalFsHz, minInternalFsHz = 16000, 8000
	switch {
	case fsHz > apiFsHz || fsHz > maxInternalFsHz || fsHz < minInternalFsHz:
		// Make sure internal rate is not higher than external rate or
		// maximum allowed, or lower than minimum allowed.
		fsHz = apiFsHz
		if fsHz > maxInternalFsHz {
			fsHz = maxInternalFsHz
		}
		if fsHz < minInternalFsHz {
			fsHz = minInternalFsHz
		}
		fsKHz = fsHz / 1000
	default:
		// State machine for the internal sampling rate switching.
		if e.lp.TransitionFrameNo >= silkTransitionFrames {
			// Stop transition phase.
			e.lp.Mode = 0
		}
		if e.allowBandwidthSwitch || opusCanSwitch {
			switch {
			case origKHz*1000 > desiredFsHz:
				// Switch down.
				if e.lp.Mode == 0 {
					// New transition.
					e.lp.TransitionFrameNo = silkTransitionFrames
					// Reset transition filter state.
					e.lp.InLPState = [2]int32{}
				}
				if opusCanSwitch {
					// Stop transition phase.
					e.lp.Mode = 0
					// Switch to a lower sample frequency.
					if origKHz == 16 {
						fsKHz = 12
					} else {
						fsKHz = 8
					}
				} else if e.lp.TransitionFrameNo <= 0 {
					switchReady = true
				} else {
					// Direction: down (at double speed).
					e.lp.Mode = -2
				}
			case origKHz*1000 < desiredFsHz:
				// Switch up.
				if opusCanSwitch {
					// Switch to a higher sample frequency.
					if origKHz == 8 {
						fsKHz = 12
					} else {
						fsKHz = 16
					}
					// New transition.
					e.lp.TransitionFrameNo = 0
					// Reset transition filter state.
					e.lp.InLPState = [2]int32{}
					// Direction: up.
					e.lp.Mode = 1
				} else if e.lp.Mode == 0 {
					switchReady = true
				} else {
					// Direction: up.
					e.lp.Mode = 1
				}
			default:
				if e.lp.Mode < 0 {
					e.lp.Mode = 1
				}
			}
		}
	}
	return fsKHz, switchReady
}

// InWBModeWithoutVariableLP is encControl->inWBmodeWithoutVariableLP: the
// encoder runs at 16 kHz with no transition filter active, so the Opus
// layer may move to SWB/FB.
func (e *Encoder) InWBModeWithoutVariableLP() bool {
	return e.sampleRate == 16000 && e.lp.Mode == 0
}
