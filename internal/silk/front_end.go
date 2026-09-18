package silk

// SILK encoder front end (silk/enc_API.c, silk/resampler.c,
// silk/float/encode_frame_FLP.c): libopus converts the Opus-layer float input
// to int16 (RES2INT16 = FLOAT2INT16), runs it through silk_resampler — whose
// encoder direction delays the signal by delay_matrix_enc[in][out] input
// samples even when the rates are equal and it only copies — into inputBuf,
// and codes the frame from inputBuf + 1, one sample behind the resampled
// input (inputBuf[0..1] hold the stereo-prediction history sMid). The coded
// x_frame is that int16 frame as float, with eight ±1e-6 anti-denormal
// offsets.
//
// The Go encoder keeps its [-1,1] float64 convention, but every sample it
// stores is an int16 (or int16 ± 1e-6f) divided by 32768, so the ×32768
// conversions downstream recover the libopus float32 values exactly.

// silkEncResamplerDelay returns delay_matrix_enc[fs][fs] (samples): the
// input delay of the equal-rate encoder-direction resampler.
func silkEncResamplerDelay(fsKHz int) int {
	switch fsKHz {
	case 8:
		return 6
	case 12:
		return 7
	case 16:
		return 10
	}
	return 0
}

// SetAPISampleRate tells the encoder the Opus-layer input rate. When it
// equals the SILK rate the libopus resampler is a pure delay that the front
// end reproduces; otherwise the Opus layer resamples with its own filter and
// only the one-sample inputBuf offset is applied (the libopus down-sampling
// FIR path is not ported yet).
func (e *Encoder) SetAPISampleRate(hz int) {
	e.apiSampleRate = hz
	if e.side != nil {
		e.side.SetAPISampleRate(hz)
	}
}

// frontEndDelay returns the number of samples the front end delays the
// input by before the coded frame: the resampler delay plus, for mono, the
// inputBuf + 1 offset (stereo applies that offset inside lrToMS).
func (e *Encoder) frontEndDelay(mono bool) int {
	d := 0
	if e.apiSampleRate == 0 || e.apiSampleRate == e.sampleRate {
		d = silkEncResamplerDelay(e.sampleRate / 1000)
	}
	if mono {
		d++
	}
	return d
}

// Float2Int16Sample is FLOAT2INT16 (RES2INT16 in the float build): scale by
// 32768 in float32, saturate, and round to nearest even like lrintf.
func Float2Int16Sample(v float64) int16 {
	x := float64(float32(v) * 32768)
	if x > 32767 {
		x = 32767
	} else if x < -32768 {
		x = -32768
	}
	return int16(silkFloat2Int(x))
}

// frontEndFrame quantises one input frame to the int16 grid and delays it
// through the front-end delay line, returning the frame the VAD sees and the
// coder pushes into x_buf. Values stay in [-1,1] (int16 / 32768).
func (e *Encoder) frontEndFrame(input []float64, mono bool) []float64 {
	n := len(input)
	q := make([]float64, n)
	for i, v := range input {
		q[i] = float64(Float2Int16Sample(v)) / 32768
	}
	d := e.frontEndDelay(mono)
	if d == 0 {
		return q
	}
	if len(e.encInputDelay) != d {
		e.encInputDelay = make([]float64, d)
	}
	if d >= n {
		joined := append(append([]float64(nil), e.encInputDelay...), q...)
		copy(e.encInputDelay, joined[n:])
		return joined[:n]
	}
	out := make([]float64, n)
	copy(out, e.encInputDelay)
	copy(out[d:], q[:n-d])
	copy(e.encInputDelay, q[n-d:])
	return out
}

// SetStreamChannels sets nChannelsInternal for a stereo encoder (1 or 2).
// With 1 the caller hands EncodeMulti the L/R downmix (silk_Encode's
// RES2INT16(L + R) halved with rounding) as a mono frame per SILK frame and
// the packet is a mono SILK stream. toMono marks the last stereo frame before
// a stereo->mono transition (silk_stereo_LR_to_MS collapses the width and
// predictors). Mono encoders ignore both.
func (e *Encoder) SetStreamChannels(n int, toMono bool) {
	if e.channels != 2 {
		return
	}
	if n < 1 || n > 2 {
		n = 2
	}
	e.streamChannels = n
	e.toMono = toMono
}

// StreamChannels returns nChannelsInternal (1 or 2).
func (e *Encoder) StreamChannels() int {
	if e.channels != 2 || e.streamChannels == 0 {
		return e.channels
	}
	return e.streamChannels
}

// DownmixToMono is silk_Encode's nChannelsAPI == 2 / nChannelsInternal == 1
// input combination: L + R summed in float, converted with RES2INT16 and
// halved with rounding, returned in the [-1,1] int16 grid.
func DownmixToMono(lr []float64) []float64 {
	n := len(lr) / 2
	mono := make([]float64, n)
	for i := 0; i < n; i++ {
		sum := Float2Int16Sample(float64(float32(lr[2*i]) + float32(lr[2*i+1])))
		mono[i] = float64(silkRShiftRound(int64(sum), 1)) / 32768
	}
	return mono
}

// enterMonoStream continues a stereo encoder as a mono stream: silk_Encode
// runs the mono channel's resampler on the downmix and, for the first mono
// frame, averages it with the side channel's resampler output
// (silk_RSHIFT(a + b, 1)); at the SILK rate both resamplers are pure delay
// lines, so their pending samples are averaged. inputBuf + 1 keeps the
// buffered sMid[1] sample in front of the frame.
func (e *Encoder) enterMonoStream() {
	if e.side == nil {
		return
	}
	d := e.frontEndDelay(false)
	line := make([]float64, d+1)
	line[0] = float64(e.stereoState.sMid[1]) / 32768
	for i := 0; i < d; i++ {
		var a, b int32
		if i < len(e.encInputDelay) {
			a = int32(silkFloat2Int(e.encInputDelay[i] * 32768))
		}
		if i < len(e.side.encInputDelay) {
			b = int32(silkFloat2Int(e.side.encInputDelay[i] * 32768))
		}
		line[i+1] = float64((a+b)>>1) / 32768
	}
	e.encInputDelay = line
	e.clearTransitionLBRR()
}

// clearTransitionLBRR is silk_Encode's `transition` rule: a change of the
// internal channel count clears both channels' pending LBRR flags, so the
// transition packet carries no redundancy.
func (e *Encoder) clearTransitionLBRR() {
	e.pendingLBRR = nil
	e.pendingLBRRFrames = 0
	e.pendingLBRRStereoPred = nil
	e.pendingLBRRStereoMidOnly = nil
	if e.side != nil {
		e.side.pendingLBRR = nil
		e.side.pendingLBRRFrames = 0
	}
}

// trackMonoStreamHistory keeps stereo_enc_state.sMid in step while a stereo
// encoder codes a mono stream (silk_Encode buffers the two samples following
// the coded frame), so a later stereo frame starts from the right history.
func (e *Encoder) trackMonoStreamHistory(frame []float64) {
	if len(frame) == 0 || len(e.encInputDelay) == 0 {
		return
	}
	e.stereoState.sMid[0] = Float2Int16Sample(frame[len(frame)-1])
	e.stereoState.sMid[1] = Float2Int16Sample(e.encInputDelay[0])
}

// enterStereoStream continues a stereo encoder after mono frames:
// silk_Encode re-initialises the side channel encoder and starts its
// resampler from the mid channel's state; the mono line's leading inputBuf +
// 1 sample is already held in sMid.
func (e *Encoder) enterStereoStream() {
	if e.side == nil {
		return
	}
	e.side.Reset()
	e.side.stereoComponent = true
	if len(e.encInputDelay) > 0 {
		e.encInputDelay = append([]float64(nil), e.encInputDelay[1:]...)
	}
	e.side.encInputDelay = append([]float64(nil), e.encInputDelay...)
	// Mono -> stereo: the predictor history, side buffer, norms and width
	// restart (sMid and silent_side_len carry on).
	e.stereoState.predPrevQ13 = [2]int16{}
	e.stereoState.sSide = [2]int16{}
	e.stereoState.midSideAmpQ0 = [4]int32{0, 1, 0, 1}
	e.stereoState.widthPrevQ14 = 0
	e.stereoState.smthWidthQ14 = 1 << 14
	e.clearTransitionLBRR()
}

// addAntiDenormalOffsets applies encode_frame_FLP's eight ±1e-6f offsets to
// the new input region of xBuf (x_frame + LA_SHAPE_MS*fs_kHz), in float32
// like libopus.
func (e *Encoder) addAntiDenormalOffsets() {
	base := e.ltpMemLength() + e.laShapeLength()
	step := e.frameSize >> 3
	for i := 0; i < 8; i++ {
		idx := base + i*step
		if idx >= len(e.xBuf) {
			break
		}
		v := float32(e.xBuf[idx] * 32768)
		v += float32(1-(i&2)) * 1e-6
		e.xBuf[idx] = float64(v) / 32768
	}
}

// InputBufferFLP returns the float32 x_buf (int16 scale) as it stood when the
// most recent frame was coded, for oracle comparisons.
func (e *Encoder) InputBufferFLP() []float32 {
	return append([]float32(nil), e.lastXBuf...)
}

// LastSpeechActivityQ8 returns speech_activity_Q8 of the most recently coded
// frame, for oracle comparisons.
func (e *Encoder) LastSpeechActivityQ8() int {
	return e.speechActivityQ8
}

// PitchResidualTrace returns the pitch-analysis residual (res_pitch) of the
// last coded frame, in the int16 scale, for oracle comparisons.
func (e *Encoder) PitchResidualTrace() []float64 {
	return append([]float64(nil), e.pitchResidual...)
}
