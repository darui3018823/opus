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
