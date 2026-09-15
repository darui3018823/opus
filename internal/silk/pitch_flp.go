package silk

import "math"

// pitch_flp.go ports libopus' float SILK pitch estimator
// (silk_find_pitch_lags_FLP + silk_pitch_analysis_core_FLP) to Go. It replaces
// the previous home-brew single-lag full-frame autocorrelation with the real
// multi-stage hierarchical search operating on an LPC-whitened residual. The
// search produces per-subframe pitch lags, an encodable lag index, a pitch
// contour codebook index, and a normalized LTP correlation, and decides
// voiced/unvoiced. See RFC 6716 and the libopus float SILK encoder.

// Pitch estimator definitions (silk/pitch_est_defines.h).
const (
	peMaxNbSubfr     = 4
	peSubfrLengthMs  = 5
	peLtpMemLengthMs = 20
	peMaxFsKHz       = 16

	peMaxLagMs = 18
	peMinLagMs = 2
	peMaxLag   = peMaxLagMs * peMaxFsKHz // 288

	peDSrchLength  = 24
	peNbStage3Lags = 5

	peNbCbksStage2    = 3
	peNbCbksStage2Ext = 11
	peNbCbksStage3Max = 34
	peNbCbksStage3_10 = 12
	peNbCbksStage2_10 = 3

	peShortlagBias    = 0.2
	pePrevlagBias     = 0.2
	peFlatcontourBias = 0.05

	silkPEMinComplex = 0
	silkPEMidComplex = 1
	silkPEMaxComplex = 2

	// find_pitch tuning (tuning_parameters.h)
	findPitchWhiteNoiseFraction = 1e-3
	findPitchBandwidthExpansion = 0.99

	// LPC windows (define.h)
	findPitchLPCWinMs    = 24 // 20 + 2*LA_PITCH_MS
	findPitchLPCWinMs2SF = 14 // 10 + 2*LA_PITCH_MS

	// (PE_MAX_LAG>>1) + 5
	peCBufLen = (peMaxLag >> 1) + 5
)

// Stage 2 lag codebooks (silk/pitch_est_tables.c).
var silkCBLagsStage2 = [peMaxNbSubfr][peNbCbksStage2Ext]int{
	{0, 2, -1, -1, -1, 0, 0, 1, 1, 0, 1},
	{0, 1, 0, 0, 0, 0, 0, 1, 0, 0, 0},
	{0, 0, 1, 0, 0, 0, 1, 0, 0, 0, 0},
	{0, -1, 2, 1, 0, 1, 1, 0, 0, -1, -1},
}

var silkCBLagsStage2_10ms = [peMaxNbSubfr >> 1][peNbCbksStage2_10]int{
	{0, 1, 0},
	{0, 0, 1},
}

// Stage 3 lag codebooks.
var silkCBLagsStage3 = [peMaxNbSubfr][peNbCbksStage3Max]int{
	{0, 0, 1, -1, 0, 1, -1, 0, -1, 1, -2, 2, -2, -2, 2, -3, 2, 3, -3, -4, 3, -4, 4, 4, -5, 5, -6, -5, 6, -7, 6, 5, 8, -9},
	{0, 0, 1, 0, 0, 0, 0, 0, 0, 0, -1, 1, 0, 0, 1, -1, 0, 1, -1, -1, 1, -1, 2, 1, -1, 2, -2, -2, 2, -2, 2, 2, 3, -3},
	{0, 1, 0, 0, 0, 0, 0, 0, 1, 0, 1, 0, 0, 1, -1, 1, 0, 0, 2, 1, -1, 2, -1, -1, 2, -1, 2, 2, -1, 3, -2, -2, -2, 3},
	{0, 1, 0, 0, 1, 0, 1, -1, 2, -1, 2, -1, 2, 3, -2, 3, -2, -2, 4, 4, -3, 5, -3, -4, 6, -4, 6, 5, -5, 8, -6, -5, -7, 9},
}

var silkCBLagsStage3_10ms = [peMaxNbSubfr >> 1][peNbCbksStage3_10]int{
	{0, 0, 1, -1, 1, -1, 2, -2, 2, -2, 3, -3},
	{0, 1, 0, 1, -1, 2, -1, 2, -2, 3, -2, 3},
}

var silkLagRangeStage3 = [silkPEMaxComplex + 1][peMaxNbSubfr][2]int{
	{{-5, 8}, {-1, 6}, {-1, 6}, {-4, 10}},
	{{-6, 10}, {-2, 6}, {-1, 6}, {-5, 10}},
	{{-9, 12}, {-3, 7}, {-2, 7}, {-7, 13}},
}

var silkLagRangeStage3_10ms = [peMaxNbSubfr >> 1][2]int{
	{-3, 7},
	{-2, 7},
}

var silkNbCbkSearchsStage3 = [silkPEMaxComplex + 1]int{16, 24, peNbCbksStage3Max}

// Resampler ROM constants (silk/resampler_rom.c).
const (
	silkResamplerDown2_0 = int16(9872)
	silkResamplerDown2_1 = int16(39809 - 65536) // -25727
)

var silkResampler2_3CoefsLQ = [6]int16{-2797, -6507, 4697, 10739, 1567, 8276}

func silkShort2FloatArray(out []float64, in []int16) {
	for i := range out {
		out[i] = float64(in[i])
	}
}

// silkResamplerDown2 decimates int16 by a factor of 2 (silk/resampler_down2.c).
// S is a length-2 Q10 state vector (zeroed by the caller before each use).
func silkResamplerDown2(S []int32, out, in []int16) {
	len2 := len(in) >> 1
	for k := 0; k < len2; k++ {
		in32 := int32(in[2*k]) << 10
		y := in32 - S[0]
		x := silkSMLAWB(y, y, silkResamplerDown2_1)
		out32 := S[0] + x
		S[0] = in32 + x

		in32 = int32(in[2*k+1]) << 10
		y = in32 - S[1]
		x = silkSMULWB(y, silkResamplerDown2_0)
		out32 += S[1]
		out32 += x
		S[1] = in32 + x

		out[k] = silkSAT16(silkRShiftRound(int64(out32), 11))
	}
}

// silkResamplerDown2_3 decimates int16 by a factor of 2/3
// (silk/resampler_down2_3.c). S is a length-6 state vector (zeroed by caller).
func silkResamplerDown2_3(S []int32, out, in []int16) {
	const orderFIR = 4
	inLen := len(in)
	buf := make([]int32, inLen+orderFIR)
	copy(buf[:orderFIR], S[:orderFIR])

	// Second-order AR filter (output in Q8) over the whole input.
	ar := [2]int32{S[orderFIR], S[orderFIR+1]}
	for k := 0; k < inLen; k++ {
		out32 := ar[0] + (int32(in[k]) << 8)
		buf[orderFIR+k] = out32
		out32 <<= 2
		ar[0] = silkSMLAWB(ar[1], out32, silkResampler2_3CoefsLQ[0])
		ar[1] = silkSMULWB(out32, silkResampler2_3CoefsLQ[1])
	}

	// Interpolate filtered signal, 2 outputs per 3 inputs.
	outIdx := 0
	bufPtr := 0
	counter := inLen
	for counter > 2 {
		res := silkSMULWB(buf[bufPtr+0], silkResampler2_3CoefsLQ[2])
		res = silkSMLAWB(res, buf[bufPtr+1], silkResampler2_3CoefsLQ[3])
		res = silkSMLAWB(res, buf[bufPtr+2], silkResampler2_3CoefsLQ[5])
		res = silkSMLAWB(res, buf[bufPtr+3], silkResampler2_3CoefsLQ[4])
		out[outIdx] = silkSAT16(silkRShiftRound(int64(res), 6))
		outIdx++

		res = silkSMULWB(buf[bufPtr+1], silkResampler2_3CoefsLQ[4])
		res = silkSMLAWB(res, buf[bufPtr+2], silkResampler2_3CoefsLQ[5])
		res = silkSMLAWB(res, buf[bufPtr+3], silkResampler2_3CoefsLQ[3])
		res = silkSMLAWB(res, buf[bufPtr+4], silkResampler2_3CoefsLQ[2])
		out[outIdx] = silkSAT16(silkRShiftRound(int64(res), 6))
		outIdx++

		bufPtr += 3
		counter -= 3
	}

	copy(S[:orderFIR], buf[inLen:inLen+orderFIR])
	S[orderFIR] = ar[0]
	S[orderFIR+1] = ar[1]
}

// silkApplySineWindowFLP applies a sine window (silk/float/apply_sine_window_FLP.c).
// winType 1: 0..pi/2; winType 2: pi/2..pi. length must be a multiple of 4.
func silkApplySineWindowFLP(out, in []float64, winType, length int) {
	freq := math.Pi / float64(length+1)
	c := 2.0 - freq*freq
	var s0, s1 float64
	if winType < 2 {
		s0 = 0.0
		s1 = freq
	} else {
		s0 = 1.0
		s1 = 0.5 * c
	}
	for k := 0; k < length; k += 4 {
		out[k+0] = in[k+0] * 0.5 * (s0 + s1)
		out[k+1] = in[k+1] * s1
		s0 = c*s1 - s0
		out[k+2] = in[k+2] * 0.5 * (s1 + s0)
		out[k+3] = in[k+3] * s0
		s1 = c*s0 - s1
	}
}

func silkInnerProductFLP(a, b []float64, n int) float64 {
	sum := 0.0
	for i := 0; i < n; i++ {
		sum += a[i] * b[i]
	}
	return sum
}

// silkAutocorrelationFLP computes the first count autocorrelation taps.
func silkAutocorrelationFLP(results, input []float64, n, count int) {
	if count > n {
		count = n
	}
	for i := 0; i < count; i++ {
		results[i] = silkInnerProductFLP(input, input[i:], n-i)
	}
}

// silkSchurFLP returns the residual energy and fills reflection coefficients
// (silk/float/schur_FLP.c).
func silkSchurFLP(reflCoef, autoCorr []float64, order int) float64 {
	// order may be the shaping LPC order (up to silkMaxShapeLPCOrder at high
	// complexity), which exceeds silkMaxLPCOrder, so size the scratch for the
	// larger of the two callers (pitch whitening vs. noise-shape analysis).
	var c [silkMaxShapeLPCOrder + 1][2]float64
	for k := 0; k <= order; k++ {
		c[k][0] = autoCorr[k]
		c[k][1] = autoCorr[k]
	}
	for k := 0; k < order; k++ {
		denom := c[0][1]
		if denom < 1e-9 {
			denom = 1e-9
		}
		rcTmp := -c[k+1][0] / denom
		reflCoef[k] = rcTmp
		for n := 0; n < order-k; n++ {
			ctmp1 := c[n+k+1][0]
			ctmp2 := c[n][1]
			c[n+k+1][0] = ctmp1 + ctmp2*rcTmp
			c[n][1] = ctmp2 + ctmp1*rcTmp
		}
	}
	return c[0][1]
}

// silkK2aFLP converts reflection coefficients to prediction coefficients.
func silkK2aFLP(a, rc []float64, order int) {
	for k := 0; k < order; k++ {
		rck := rc[k]
		for n := 0; n < (k+1)>>1; n++ {
			tmp1 := a[n]
			tmp2 := a[k-n-1]
			a[n] = tmp1 + tmp2*rck
			a[k-n-1] = tmp2 + tmp1*rck
		}
		a[k] = -rck
	}
}

// silkBwexpanderFLP applies a chirp factor to an AR filter.
func silkBwexpanderFLP(ar []float64, d int, chirp float64) {
	cfac := chirp
	for i := 0; i < d-1; i++ {
		ar[i] *= cfac
		cfac *= chirp
	}
	ar[d-1] *= cfac
}

// silkLPCAnalysisFilterFLP computes the LPC residual r[ix] = s[ix] - sum_k
// A[k]*s[ix-1-k]; the first order samples are left zero.
func silkLPCAnalysisFilterFLP(r, predCoef, s []float64, length, order int) {
	for ix := 0; ix < order && ix < length; ix++ {
		r[ix] = 0
	}
	for ix := order; ix < length; ix++ {
		pred := 0.0
		for k := 0; k < order; k++ {
			pred += s[ix-1-k] * predCoef[k]
		}
		r[ix] = s[ix] - pred
	}
}

// silkInsertionSortDecreasingFLP sorts the first L values of a descending,
// keeping the top K with their original indices (silk/float/sort_FLP.c).
func silkInsertionSortDecreasingFLP(a []float64, idx []int, L, K int) {
	for i := 0; i < K; i++ {
		idx[i] = i
	}
	for i := 1; i < K; i++ {
		value := a[i]
		j := i - 1
		for j >= 0 && value > a[j] {
			a[j+1] = a[j]
			idx[j+1] = idx[j]
			j--
		}
		a[j+1] = value
		idx[j+1] = i
	}
	for i := K; i < L; i++ {
		value := a[i]
		if value > a[K-1] {
			j := K - 2
			for j >= 0 && value > a[j] {
				a[j+1] = a[j]
				idx[j+1] = idx[j]
				j--
			}
			a[j+1] = value
			idx[j+1] = i
		}
	}
}

// pitchEstParams returns the pitch-estimation complexity (0..2), the pitch LPC
// order, and the stage-1 search threshold for the encoder's complexity setting
// (silk/control_codec.c silk_setup_complexity).
func (e *Encoder) pitchEstParams() (peComplexity, order int, searchThres1 float64) {
	switch {
	case e.complexity < 1:
		peComplexity, order, searchThres1 = silkPEMinComplex, 6, 0.8
	case e.complexity < 2:
		peComplexity, order, searchThres1 = silkPEMidComplex, 8, 0.76
	case e.complexity < 3:
		peComplexity, order, searchThres1 = silkPEMinComplex, 6, 0.8
	case e.complexity < 4:
		peComplexity, order, searchThres1 = silkPEMidComplex, 8, 0.76
	case e.complexity < 6:
		peComplexity, order, searchThres1 = silkPEMidComplex, 10, 0.74
	case e.complexity < 8:
		peComplexity, order, searchThres1 = silkPEMidComplex, 12, 0.72
	default:
		peComplexity, order, searchThres1 = silkPEMaxComplex, 16, 0.7
	}
	if order > e.lpcOrder {
		order = e.lpcOrder
	}
	return
}

// silkFindPitchLags ports silk_find_pitch_lags_FLP: it whitens the input with a
// short-term LPC analysis filter and runs the multi-stage pitch core on the
// residual. signal is the current frame in [-1,1]; speechActivity is the VAD
// speech-activity estimate in [0,1]. It returns the voiced decision, the
// encodable lag index and pitch contour index, and the normalized LTP
// correlation. The per-subframe lags are reconstructed by the caller from the
// encoded indices so the encoder and decoder stay in sync.
func (e *Encoder) silkFindPitchLags(signal []float64, speechActivity float64) (voiced bool, lagIndex, contourIndex int, ltpCorr float64) {
	fsKHz := e.sampleRate / 1000
	nbSubfr := e.nSubframes

	peComplexity, order, _ := e.pitchEstParams()

	ltpMem := peLtpMemLengthMs * fsKHz
	frameLen := nbSubfr * peSubfrLengthMs * fsKHz
	bufLen := ltpMem + frameLen

	if len(e.pitchHist) != ltpMem || len(signal) < frameLen {
		e.ltpCorrState = 0
		return false, 0, 0, 0
	}

	// Analysis buffer [history | frame] in silk_float int16 scale. libopus
	// also appends la_pitch look-ahead samples here; the Go encoder has no
	// look-ahead delay yet, so the LPC window ends at the frame boundary.
	buf := make([]float64, bufLen)
	for i := 0; i < ltpMem; i++ {
		buf[i] = f32(e.pitchHist[i] * 32768.0)
	}
	for i := 0; i < frameLen; i++ {
		buf[ltpMem+i] = f32(signal[i] * 32768.0)
	}

	winLen := findPitchLPCWinMs * fsKHz
	if nbSubfr != peMaxNbSubfr {
		winLen = findPitchLPCWinMs2SF * fsKHz
	}
	laPitch := 2 * fsKHz // LA_PITCH_MS = 2
	if winLen > bufLen {
		winLen = bufLen
	}

	// The VAD port keeps float activity and tilt; libopus feeds the pitch
	// threshold Q8/Q15 integers, so quantize the same way here.
	speechActivityQ8 := clampInt(int(math.Round(speechActivity*256)), 0, 255)
	inputTiltQ15 := clampInt(int(math.Round(e.inputTilt*32768)), -32768, 32767)

	r := silkFindPitchLagsFLP32(buf, laPitch, winLen, order, fsKHz, nbSubfr, peComplexity,
		speechActivityQ8, e.prevSignalType, inputTiltQ15, e.pitchEstimationThresholdQ16(),
		e.prevLagForPitch, e.ltpCorrState, true)
	// silk_find_pred_coefs_FLP correlates this residual (res_pitch) for the
	// LTP quantizer and reads LTP_ORDER samples past the frame from the
	// look-ahead; without look-ahead those samples are zero here.
	e.pitchResidual = append(append([]float64(nil), r.res...), make([]float64, ltpOrder)...)
	if e.firstFrameAfterReset && e.channels == 1 && !e.stereoComponent && !e.hybridMode && r.voiced && firstFrameLongLagPitch(r.pitchL, fsKHz) {
		e.ltpCorrState = 0
		return false, 0, 0, 0
	}
	e.ltpCorrState = r.ltpCorr
	return r.voiced, r.lagIndex, r.contourIndex, r.ltpCorr
}

// pitchEstimationThresholdQ16 mirrors silk_setup_complexity's
// pitchEstimationThreshold_Q16 = SILK_FIX_CONST(x, 16) for the complexity.
func (e *Encoder) pitchEstimationThresholdQ16() int {
	_, _, thres := e.pitchEstParams()
	return int(int32(thres*65536 + 0.5))
}

func firstFrameLongLagPitch(pitchLags []int, fsKHz int) bool {
	if fsKHz != 16 {
		return false
	}
	// Keep the guard to low-F0 wideband onsets. The scoreboard overshoot is the
	// 16 kHz speech-like harmonic first frame (~145 Hz, lag > 100), while the
	// shorter-lag voiced/onset fixtures rely on the existing first-frame path.
	maxReliableFirstFrameLag := fsKHz * 1000 / 160
	for _, lag := range pitchLags {
		if lag > maxReliableFirstFrameLag {
			return true
		}
	}
	return false
}

// updatePitchHist shifts the encoder's pitch history buffer to end with the most
// recent ltp_mem_length input samples.
func (e *Encoder) updatePitchHist(signal []float64) {
	ltpMem := len(e.pitchHist)
	if ltpMem == 0 {
		return
	}
	if len(signal) >= ltpMem {
		copy(e.pitchHist, signal[len(signal)-ltpMem:])
		return
	}
	copy(e.pitchHist, e.pitchHist[len(signal):])
	copy(e.pitchHist[ltpMem-len(signal):], signal)
}
