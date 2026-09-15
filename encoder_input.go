package opus

import (
	"math"

	"github.com/darui3018823/opus/internal/silk"
)

// Input conditioning of opus_encode_native (libopus 1.6.1, float build):
// every packet's input is high-pass filtered before the SILK layer and the
// CELT delay buffer see it. VOIP uses hp_cutoff, a second-order high-pass
// whose cutoff follows the SILK encoder's pitch-driven variable_HP_smth1_Q15
// through a second smoother (variable_HP_smth2_Q15); other applications use
// the 3 Hz dc_reject. The float API then zeroes a frame whose energy is not
// finite and below 1e9.
//
// Arithmetic mirrors libopus: the biquad coefficients are derived in SILK
// fixed point and the filters run in float32, so the conditioned samples can
// be compared bit-for-bit with a scalar libopus build.

const (
	hpFcScaleQ19      = 2471      // SILK_FIX_CONST(1.5 * 3.14159 / 1000, 19)
	hpRScaleQ9        = 471       // SILK_FIX_CONST(0.92, 9)
	hpOneQ28          = 268435456 // SILK_FIX_CONST(1.0, 28)
	hpTwoQ22          = 8388608   // SILK_FIX_CONST(2.0, 22)
	variableHPSmthQ16 = 983       // SILK_FIX_CONST(VARIABLE_HP_SMTH_COEF2 = 0.015, 16)
	variableHPMinHz   = 60        // VARIABLE_HP_MIN_CUTOFF_HZ
	dcRejectCutoffHz  = 3
	hpVerySmall       = float32(1e-30) // VERY_SMALL (float build)
	floatAPIMaxEnergy = float32(1e9)
)

// variableHPSmth2Initial mirrors opus_encoder_init / OPUS_RESET_STATE:
// silk_LSHIFT(silk_lin2log(VARIABLE_HP_MIN_CUTOFF_HZ), 8).
func variableHPSmth2Initial() int32 {
	return silk.Lin2Log(variableHPMinHz) << 8
}

// silkSMULWBMacro is libopus' non-ARM silk_SMULWB: ((a >> 16) * (int16)b) +
// (((a & 0xFFFF) * (int16)b) >> 16).
func silkSMULWBMacro(a, b int32) int32 {
	c := int32(int16(b))
	return (a>>16)*c + ((a&0xFFFF)*c)>>16
}

// silkSMULWWMacro is libopus' non-ARM silk_SMULWW:
// silk_MLA(silk_SMULWB(a, b), a, silk_RSHIFT_ROUND(b, 16)).
func silkSMULWWMacro(a, b int32) int32 {
	round := (b>>15 + 1) >> 1
	return silkSMULWBMacro(a, b) + a*round
}

// silkSMLAWBMacro is silk_SMLAWB: a + silk_SMULWB(b, c).
func silkSMLAWBMacro(a, b, c int32) int32 {
	return a + silkSMULWBMacro(b, c)
}

// hpCutoffCoefficients derives B_Q28/A_Q28 exactly like hp_cutoff for the
// cutoff (Hz) at the encoder rate.
func hpCutoffCoefficients(cutoffHz int32, fs int) (b [3]int32, a [2]int32) {
	fcQ19 := (int32(int16(hpFcScaleQ19)) * int32(int16(cutoffHz))) / int32(fs/1000)
	rQ28 := int32(hpOneQ28) - int32(hpRScaleQ9)*fcQ19

	// b = r * [ 1; -2; 1 ]
	b[0] = rQ28
	b[1] = -rQ28 << 1
	b[2] = rQ28

	// a = [ 1; -2 * r * ( 1 - 0.5 * Fc^2 ); r^2 ]
	rQ22 := rQ28 >> 6
	a[0] = silkSMULWWMacro(rQ22, silkSMULWWMacro(fcQ19, fcQ19)-int32(hpTwoQ22))
	a[1] = silkSMULWWMacro(rQ22, rQ22)
	return b, a
}

// biquadRes ports the float silk_biquad_res (direct form II transposed) for
// one channel of interleaved audio.
func biquadRes(in []float64, bQ28 [3]int32, aQ28 [2]int32, s []float32, out []float64, n, offset, stride int) {
	const scale = float32(1.0 / (1 << 28))
	a0 := float32(aQ28[0]) * scale
	a1 := float32(aQ28[1]) * scale
	b0 := float32(bQ28[0]) * scale
	b1 := float32(bQ28[1]) * scale
	b2 := float32(bQ28[2]) * scale
	s0, s1 := s[0], s[1]
	for k := 0; k < n; k++ {
		i := offset + k*stride
		inval := float32(in[i])
		vout := float32(s0 + float32(b0*inval))
		s0 = float32(float32(s1-float32(vout*a0)) + float32(b1*inval))
		s1 = float32(float32(float32(-vout*a1)+float32(b2*inval)) + hpVerySmall)
		out[i] = float64(vout)
	}
	s[0], s[1] = s0, s1
}

// hpCutoff ports hp_cutoff: in and out hold frameSize interleaved samples.
func hpCutoff(in []float64, cutoffHz int32, out []float64, hpMem *[4]float32, frameSize, channels, fs int) {
	b, a := hpCutoffCoefficients(cutoffHz, fs)
	biquadRes(in, b, a, hpMem[0:2], out, frameSize, 0, channels)
	if channels == 2 {
		biquadRes(in, b, a, hpMem[2:4], out, frameSize, 1, channels)
	}
}

// dcReject ports the float dc_reject: a first-order DC blocker at cutoffHz.
func dcReject(in []float64, cutoffHz int32, out []float64, hpMem *[4]float32, frameSize, channels, fs int) {
	coef := float32(float32(6.3)*float32(cutoffHz)) / float32(fs)
	coef2 := float32(1 - coef)
	if channels == 2 {
		m0, m2 := hpMem[0], hpMem[2]
		for i := 0; i < frameSize; i++ {
			x0 := float32(in[2*i])
			x1 := float32(in[2*i+1])
			out[2*i] = float64(float32(x0 - m0))
			out[2*i+1] = float64(float32(x1 - m2))
			m0 = float32(float32(float32(coef*x0)+hpVerySmall) + float32(coef2*m0))
			m2 = float32(float32(float32(coef*x1)+hpVerySmall) + float32(coef2*m2))
		}
		hpMem[0], hpMem[2] = m0, m2
		return
	}
	m0 := hpMem[0]
	for i := 0; i < frameSize; i++ {
		x := float32(in[i])
		out[i] = float64(float32(x - m0))
		m0 = float32(float32(float32(coef*x)+hpVerySmall) + float32(coef2*m0))
	}
	hpMem[0] = m0
}

// hpFreqSmth1 returns the log2 cutoff the Opus-layer smoother follows for
// this packet: the SILK encoder's variable_HP_smth1_Q15 when the packet will
// carry SILK, otherwise the minimum cutoff (opus_encode_native).
func (e *Encoder) hpFreqSmth1(nFrames int) int32 {
	if e.silkEncoder != nil && nFrames > 0 && (e.shouldEncodeSILKOnly() || e.shouldEncodeHybrid(nFrames)) {
		return e.silkEncoder.VariableHPSmth1Q15()
	}
	return silk.Lin2Log(variableHPMinHz) << 8
}

// conditionInput returns the high-pass conditioned copy of pcm that both the
// SILK layer and the CELT delay buffer consume, advancing the filter state.
// nFrames is the number of 20 ms frames in the packet (0 for a short CELT
// frame).
func (e *Encoder) conditionInput(pcm []float64, frameSize, nFrames int) []float64 {
	e.variableHPSmth2Q15 = silkSMLAWBMacro(e.variableHPSmth2Q15,
		e.hpFreqSmth1(nFrames)-e.variableHPSmth2Q15, variableHPSmthQ16)
	cutoffHz := silk.Log2Lin(e.variableHPSmth2Q15 >> 8)

	out := make([]float64, len(pcm))
	if e.application == ApplicationVOIP {
		hpCutoff(pcm, cutoffHz, out, &e.hpMem, frameSize, e.channels, e.sampleRate)
	} else {
		dcReject(pcm, dcRejectCutoffHz, out, &e.hpMem, frameSize, e.channels, e.sampleRate)
	}

	// Float API guard: drop NaNs and signals large enough to produce them
	// downstream (celt_inner_prod accumulates in float32).
	var sum float32
	for _, v := range out {
		f := float32(v)
		sum = float32(sum + float32(f*f))
	}
	if !(sum < floatAPIMaxEnergy) || math.IsNaN(float64(sum)) {
		clear(out)
		e.hpMem = [4]float32{}
	}
	e.lastConditionedInput = out
	return out
}
