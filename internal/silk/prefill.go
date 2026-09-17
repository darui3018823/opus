package silk

// Prefill is silk_Encode with prefillFlag set: 10 ms of input at the
// encoder's sample rate (interleaved L/R for a stereo stream, mono
// otherwise) primes a freshly initialised encoder without coding anything —
// opus_encode_native runs it on a CELT-only -> SILK/hybrid switch so the
// first coded frame has an input history. Like libopus it advances the
// front-end delay line, runs the VAD (and the mid/side analysis of a stereo
// stream), places the samples at the end of the x_buf history and counts
// the frame for the NSQ seed; the rate control, LBRR and coding state are
// untouched. bitrate is the frame's SILK bitrate (the stereo analysis
// reads it as the prefill's target rate).
func (e *Encoder) Prefill(pcm []float64) {
	n := e.frameSize / 2
	if n <= 0 {
		return
	}
	stereo := e.channels == 2 && e.streamChannels == 2 && e.side != nil
	if stereo {
		if len(pcm) < 2*n {
			return
		}
		fsKHz := e.sampleRate / 1000
		if e.prevStreamChannels == 1 {
			e.enterStereoStream()
		}
		l := make([]float64, n)
		r := make([]float64, n)
		for i := 0; i < n; i++ {
			l[i] = pcm[2*i]
			r[i] = pcm[2*i+1]
		}
		left := floatFrameToInt16(e.frontEndFrame(l, false))
		right := floatFrameToInt16(e.side.frontEndFrame(r, false))
		// TargetRate_bps of a 10 ms prefill is the bitrate itself.
		ms := e.stereoState.lrToMS(left, right, fsKHz, n, int32(e.bitrate), e.speechActivityQ8, e.toMono)
		mid := int16FrameToFloat(ms.mid)
		side := int16FrameToFloat(ms.side)
		if !ms.midOnly {
			if e.prevOnlyMiddle {
				e.side.resetForSideReactivation()
			}
			e.side.prefillFrame(side)
		}
		e.prefillFrame(mid)
		e.prevOnlyMiddle = ms.midOnly
		e.prevStreamChannels = 2
		return
	}
	if len(pcm) < n {
		return
	}
	framePCM := e.frontEndFrame(pcm[:n], true)
	if e.channels == 2 {
		e.trackMonoStreamHistory(framePCM)
	}
	e.prefillFrame(framePCM)
	if e.channels == 2 {
		e.prevStreamChannels = 1
	}
}

// prefillFrame runs the VAD on one channel's 10 ms prefill frame and writes
// it to the end of the x_buf history: silk_encode_frame_FLP copies the frame
// behind the look-ahead (with the anti-denormal offsets) and shifts the
// buffer by the frame length before returning without coding, so the 10 ms
// end up right before the look-ahead of the next frame.
func (e *Encoder) prefillFrame(frame []float64) {
	n := len(frame)
	res := e.silkVADGetSAQ8N(frame, n)
	e.installVAD(res)
	// The VAD's DTX bookkeeping (silk_encode_do_VAD).
	if res.speechActivityQ8 < 13 {
		e.noSpeechCounter++
		if e.noSpeechCounter > silkMaxConsecutiveDTX+silkNBSpeechFramesBeforeDTX {
			e.noSpeechCounter = silkNBSpeechFramesBeforeDTX
		}
	} else {
		e.noSpeechCounter = 0
	}
	ltpMem := e.ltpMemLength()
	la := e.laShapeLength()
	want := ltpMem + la + e.frameSize
	if len(e.xBuf) != want {
		e.xBuf = make([]float64, want)
	}
	e.pitchHist = e.xBuf[:ltpMem]
	end := ltpMem + la
	copy(e.xBuf[end-n:end], frame)
	step := n >> 3
	for i := 0; i < 8; i++ {
		idx := end - n + i*step
		v := float32(e.xBuf[idx] * 32768)
		v += float32(1-(i&2)) * 1e-6
		e.xBuf[idx] = float64(v) / 32768
	}
	e.frameCounter++
}

// installVAD makes a VAD result the encoder's current speech activity,
// input tilt and quality (the fields silk_VAD_GetSA_Q8 writes to the state).
func (e *Encoder) installVAD(res silkVADResult) {
	e.speechActivity = res.speechActivity
	e.inputTilt = res.inputTilt
	e.speechActivityQ8 = res.speechActivityQ8
	e.inputTiltQ15 = res.inputTiltQ15
	e.inputQuality = res.inputQuality
	e.inputQualityB = res.inputQualityBand
	e.inputQualityBandQ15 = res.inputQualityBandQ15
}
