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

// silkFloat2Short rounds and saturates a float64 sample to int16 range.
func silkFloat2Short(x float64) int16 {
	v := math.Round(x)
	if v > 32767 {
		return 32767
	}
	if v < -32768 {
		return -32768
	}
	return int16(v)
}

func silkFloat2ShortArray(out []int16, in []float64) {
	for i := range out {
		out[i] = silkFloat2Short(in[i])
	}
}

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

// energyAt returns the energy of frame[off:off+n].
func energyAt(frame []float64, off, n int) float64 {
	sum := 0.0
	for i := 0; i < n; i++ {
		v := frame[off+i]
		sum += v * v
	}
	return sum
}

// innerProdAt returns sum frame[aOff+i]*frame[bOff+i] for i in [0,n).
func innerProdAt(frame []float64, aOff, bOff, n int) float64 {
	sum := 0.0
	for i := 0; i < n; i++ {
		sum += frame[aOff+i] * frame[bOff+i]
	}
	return sum
}

// silkPitchAnalysisCoreFLP is the float pitch analyser. It returns whether the
// frame is voiced, the encodable lag index and pitch contour index, the
// per-subframe lags, and updates ltpCorr in place. frame holds the LPC residual
// of length (PE_LTP_MEM_LENGTH_MS + nbSubfr*PE_SUBFR_LENGTH_MS)*fsKHz in int16
// scale. complexity is the pitch-estimation complexity (0..2).
func silkPitchAnalysisCoreFLP(
	frame []float64,
	ltpCorr *float64,
	prevLag int,
	searchThres1, searchThres2 float64,
	fsKHz, complexity, nbSubfr int,
) (pitchOut []int, lagIndex, contourIndex int, voiced bool) {
	pitchOut = make([]int, nbSubfr)

	frameLength := (peLtpMemLengthMs + nbSubfr*peSubfrLengthMs) * fsKHz
	frameLength4kHz := (peLtpMemLengthMs + nbSubfr*peSubfrLengthMs) * 4
	frameLength8kHz := (peLtpMemLengthMs + nbSubfr*peSubfrLengthMs) * 8
	sfLength := peSubfrLengthMs * fsKHz
	sfLength4kHz := peSubfrLengthMs * 4
	sfLength8kHz := peSubfrLengthMs * 8
	minLag := peMinLagMs * fsKHz
	minLag4kHz := peMinLagMs * 4
	minLag8kHz := peMinLagMs * 8
	maxLag := peMaxLagMs*fsKHz - 1
	maxLag4kHz := peMaxLagMs * 4
	maxLag8kHz := peMaxLagMs*8 - 1

	// Resample residual to 8 kHz, then 4 kHz.
	frame8kHz := make([]float64, frameLength8kHz)
	frame4kHz := make([]float64, frameLength4kHz)
	frame8FIX := make([]int16, frameLength8kHz)
	frame4FIX := make([]int16, frameLength4kHz)
	filtState := make([]int32, 6)

	switch fsKHz {
	case 16:
		frame16FIX := make([]int16, frameLength)
		silkFloat2ShortArray(frame16FIX, frame[:frameLength])
		for i := range filtState {
			filtState[i] = 0
		}
		silkResamplerDown2(filtState[:2], frame8FIX, frame16FIX)
		silkShort2FloatArray(frame8kHz, frame8FIX)
	case 12:
		frame12FIX := make([]int16, frameLength)
		silkFloat2ShortArray(frame12FIX, frame[:frameLength])
		for i := range filtState {
			filtState[i] = 0
		}
		silkResamplerDown2_3(filtState[:6], frame8FIX, frame12FIX)
		silkShort2FloatArray(frame8kHz, frame8FIX)
	default: // 8 kHz
		silkFloat2ShortArray(frame8FIX, frame[:frameLength8kHz])
	}

	// Decimate 8 kHz -> 4 kHz.
	for i := range filtState {
		filtState[i] = 0
	}
	silkResamplerDown2(filtState[:2], frame4FIX, frame8FIX)
	silkShort2FloatArray(frame4kHz, frame4FIX)

	// Low-pass filter (integer saturating add on int16-valued floats).
	for i := frameLength4kHz - 1; i > 0; i-- {
		frame4kHz[i] = float64(silkSAT16(int32(frame4kHz[i]) + int32(frame4kHz[i-1])))
	}

	// ---- FIRST STAGE: 4 kHz ----
	c0 := make([]float64, peCBufLen)
	target := sfLength4kHz << 2
	for k := 0; k < nbSubfr>>1; k++ {
		for d := minLag4kHz; d <= maxLag4kHz; d++ {
			cross := innerProdAt(frame4kHz, target, target-d, sfLength8kHz)
			normalizer := energyAt(frame4kHz, target, sfLength8kHz) +
				energyAt(frame4kHz, target-d, sfLength8kHz) +
				float64(sfLength8kHz)*4000.0
			c0[d] += 2 * cross / normalizer
		}
		target += sfLength8kHz
	}

	// Short-lag bias.
	for i := maxLag4kHz; i >= minLag4kHz; i-- {
		c0[i] -= c0[i] * float64(i) / 4096.0
	}

	lengthDSrch := 4 + 2*complexity
	dSrch := make([]int, peDSrchLength)
	seg := c0[minLag4kHz : maxLag4kHz+1]
	silkInsertionSortDecreasingFLP(seg, dSrch, maxLag4kHz-minLag4kHz+1, lengthDSrch)

	cmax := c0[minLag4kHz]
	if cmax < 0.2 {
		*ltpCorr = 0.0
		return pitchOut, 0, 0, false
	}

	threshold := searchThres1 * cmax
	for i := 0; i < lengthDSrch; i++ {
		if c0[minLag4kHz+i] > threshold {
			dSrch[i] = (dSrch[i] + minLag4kHz) << 1
		} else {
			lengthDSrch = i
			break
		}
	}

	dComp := make([]int, peCBufLen)
	for i := 0; i < lengthDSrch; i++ {
		dComp[dSrch[i]] = 1
	}
	for i := maxLag8kHz + 3; i >= minLag8kHz; i-- {
		dComp[i] += dComp[i-1] + dComp[i-2]
	}
	lengthDSrch = 0
	for i := minLag8kHz; i < maxLag8kHz+1; i++ {
		if dComp[i+1] > 0 {
			dSrch[lengthDSrch] = i
			lengthDSrch++
		}
	}
	for i := maxLag8kHz + 3; i >= minLag8kHz; i-- {
		dComp[i] += dComp[i-1] + dComp[i-2] + dComp[i-3]
	}
	lengthDComp := 0
	dCompOut := make([]int, peCBufLen)
	for i := minLag8kHz; i < maxLag8kHz+4; i++ {
		if dComp[i] > 0 {
			dCompOut[lengthDComp] = i - 2
			lengthDComp++
		}
	}

	// ---- SECOND STAGE: 8 kHz ----
	cc := make([][]float64, peMaxNbSubfr)
	for i := range cc {
		cc[i] = make([]float64, peCBufLen)
	}
	var stage2 []float64
	stage2Off := peLtpMemLengthMs * 8
	if fsKHz == 8 {
		stage2 = frame
	} else {
		stage2 = frame8kHz
	}
	for k := 0; k < nbSubfr; k++ {
		base := stage2Off + k*sfLength8kHz
		energyTmp := energyAt(stage2, base, sfLength8kHz) + 1.0
		for j := 0; j < lengthDComp; j++ {
			d := dCompOut[j]
			cross := innerProdAt(stage2, base, base-d, sfLength8kHz)
			if cross > 0.0 {
				energy := energyAt(stage2, base-d, sfLength8kHz)
				cc[k][d] = 2 * cross / (energy + energyTmp)
			} else {
				cc[k][d] = 0.0
			}
		}
	}

	ccmax := 0.0
	ccmaxB := -1000.0
	cbimax := 0
	lag := -1

	prevLagLog2 := 0.0
	if prevLag > 0 {
		switch fsKHz {
		case 12:
			prevLag = (prevLag << 1) / 3
		case 16:
			prevLag = prevLag >> 1
		}
		prevLagLog2 = math.Log2(float64(prevLag))
	}

	var cbkSize, nbCbkSearch int
	cbStage2 := func(i, j int) int { return 0 }
	if nbSubfr == peMaxNbSubfr {
		cbkSize = peNbCbksStage2Ext
		cbStage2 = func(i, j int) int { return silkCBLagsStage2[i][j] }
		if fsKHz == 8 && complexity > silkPEMinComplex {
			nbCbkSearch = peNbCbksStage2Ext
		} else {
			nbCbkSearch = peNbCbksStage2
		}
	} else {
		cbkSize = peNbCbksStage2_10
		cbStage2 = func(i, j int) int { return silkCBLagsStage2_10ms[i][j] }
		nbCbkSearch = peNbCbksStage2_10
	}
	_ = cbkSize

	ccArr := make([]float64, peNbCbksStage2Ext)
	for k := 0; k < lengthDSrch; k++ {
		d := dSrch[k]
		for j := 0; j < nbCbkSearch; j++ {
			ccArr[j] = 0.0
			for i := 0; i < nbSubfr; i++ {
				ccArr[j] += cc[i][d+cbStage2(i, j)]
			}
		}
		ccmaxNew := -1000.0
		cbimaxNew := 0
		for i := 0; i < nbCbkSearch; i++ {
			if ccArr[i] > ccmaxNew {
				ccmaxNew = ccArr[i]
				cbimaxNew = i
			}
		}
		lagLog2 := math.Log2(float64(d))
		ccmaxNewB := ccmaxNew - peShortlagBias*float64(nbSubfr)*lagLog2
		if prevLag > 0 {
			deltaLagLog2Sqr := lagLog2 - prevLagLog2
			deltaLagLog2Sqr *= deltaLagLog2Sqr
			ccmaxNewB -= pePrevlagBias * float64(nbSubfr) * (*ltpCorr) * deltaLagLog2Sqr / (deltaLagLog2Sqr + 0.5)
		}
		if ccmaxNewB > ccmaxB && ccmaxNew > float64(nbSubfr)*searchThres2 {
			ccmaxB = ccmaxNewB
			ccmax = ccmaxNew
			lag = d
			cbimax = cbimaxNew
		}
	}

	if lag == -1 {
		*ltpCorr = 0.0
		return pitchOut, 0, 0, false
	}

	*ltpCorr = ccmax / float64(nbSubfr)

	if fsKHz > 8 {
		// Search in original signal.
		if fsKHz == 12 {
			lag = int(silkRShiftRound(int64(lag*3), 1))
		} else { // 16
			lag = lag << 1
		}
		lag = clampInt(lag, minLag, maxLag)
		startLag := lag - 2
		if startLag < minLag {
			startLag = minLag
		}
		endLag := lag + 2
		if endLag > maxLag {
			endLag = maxLag
		}
		lagNew := lag
		cbimax = 0
		ccmax = -1000.0

		corrSt3 := silkPAnaCalcCorrSt3(frame, startLag, sfLength, nbSubfr, complexity)
		energiesSt3 := silkPAnaCalcEnergySt3(frame, startLag, sfLength, nbSubfr, complexity)

		contourBias := peFlatcontourBias / float64(lag)

		var nbCbk, cbSize3 int
		cbStage3 := func(k, i int) int { return 0 }
		if nbSubfr == peMaxNbSubfr {
			nbCbk = silkNbCbkSearchsStage3[complexity]
			cbSize3 = peNbCbksStage3Max
			cbStage3 = func(k, i int) int { return silkCBLagsStage3[k][i] }
		} else {
			nbCbk = peNbCbksStage3_10
			cbSize3 = peNbCbksStage3_10
			cbStage3 = func(k, i int) int { return silkCBLagsStage3_10ms[k][i] }
		}
		_ = cbSize3

		energyTmp := energyAt(frame, peLtpMemLengthMs*fsKHz, nbSubfr*sfLength) + 1.0
		lagCounter := 0
		for d := startLag; d <= endLag; d++ {
			for j := 0; j < nbCbk; j++ {
				crossCorr := 0.0
				energy := energyTmp
				for k := 0; k < nbSubfr; k++ {
					crossCorr += corrSt3[k][j][lagCounter]
					energy += energiesSt3[k][j][lagCounter]
				}
				var ccmaxNew float64
				if crossCorr > 0.0 {
					ccmaxNew = 2 * crossCorr / energy
					ccmaxNew *= 1.0 - contourBias*float64(j)
				}
				if ccmaxNew > ccmax && (d+silkCBLagsStage3[0][j]) <= maxLag {
					ccmax = ccmaxNew
					lagNew = d
					cbimax = j
				}
			}
			lagCounter++
		}

		for k := 0; k < nbSubfr; k++ {
			pitchOut[k] = lagNew + cbStage3(k, cbimax)
			pitchOut[k] = clampInt(pitchOut[k], minLag, peMaxLagMs*fsKHz)
		}
		lagIndex = lagNew - minLag
		contourIndex = cbimax
	} else { // fsKHz == 8
		cbStage3 := cbStage2
		for k := 0; k < nbSubfr; k++ {
			pitchOut[k] = lag + cbStage3(k, cbimax)
			pitchOut[k] = clampInt(pitchOut[k], minLag8kHz, peMaxLagMs*8)
		}
		lagIndex = lag - minLag8kHz
		contourIndex = cbimax
	}

	return pitchOut, lagIndex, contourIndex, true
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

	peComplexity, order, searchThres1 := e.pitchEstParams()

	ltpMem := peLtpMemLengthMs * fsKHz
	frameLen := nbSubfr * peSubfrLengthMs * fsKHz
	bufLen := ltpMem + frameLen

	if len(e.pitchHist) != ltpMem || len(signal) < frameLen {
		e.ltpCorrState = 0
		return false, 0, 0, 0
	}

	// Build the analysis buffer [history | frame] in int16 scale.
	buf := make([]float64, bufLen)
	for i := 0; i < ltpMem; i++ {
		buf[i] = e.pitchHist[i] * 32768.0
	}
	for i := 0; i < frameLen; i++ {
		buf[ltpMem+i] = signal[i] * 32768.0
	}

	// Windowed signal for the short-term LPC analysis.
	winLen := findPitchLPCWinMs * fsKHz
	if nbSubfr != peMaxNbSubfr {
		winLen = findPitchLPCWinMs2SF * fsKHz
	}
	laPitch := 2 * fsKHz // LA_PITCH_MS = 2
	if winLen > bufLen {
		winLen = bufLen
	}
	wsig := make([]float64, winLen)
	xStart := bufLen - winLen
	silkApplySineWindowFLP(wsig[:laPitch], buf[xStart:], 1, laPitch)
	copy(wsig[laPitch:winLen-laPitch], buf[xStart+laPitch:xStart+winLen-laPitch])
	silkApplySineWindowFLP(wsig[winLen-laPitch:], buf[xStart+winLen-laPitch:], 2, laPitch)

	autoCorr := make([]float64, order+1)
	silkAutocorrelationFLP(autoCorr, wsig, winLen, order+1)
	autoCorr[0] += autoCorr[0]*findPitchWhiteNoiseFraction + 1.0

	refl := make([]float64, order)
	silkSchurFLP(refl, autoCorr, order)
	a := make([]float64, order)
	silkK2aFLP(a, refl, order)
	silkBwexpanderFLP(a, order, findPitchBandwidthExpansion)

	res := make([]float64, bufLen)
	silkLPCAnalysisFilterFLP(res, a, buf, bufLen, order)

	searchThres2 := 0.6 -
		0.004*float64(order) -
		0.1*speechActivity -
		0.15*float64(e.prevSignalType>>1) -
		0.1*e.inputTilt
	if searchThres2 < 0 {
		searchThres2 = 0
	}

	ltpCorr = e.ltpCorrState
	_, lagIndex, contourIndex, voiced = silkPitchAnalysisCoreFLP(
		res, &ltpCorr, e.prevLagForPitch, searchThres1, searchThres2,
		fsKHz, peComplexity, nbSubfr)
	e.ltpCorrState = ltpCorr
	return voiced, lagIndex, contourIndex, ltpCorr
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

const scratchSizeSt3 = 22

// silkPAnaCalcCorrSt3 ports silk_P_Ana_calc_corr_st3 (stage-3 correlations).
func silkPAnaCalcCorrSt3(frame []float64, startLag, sfLength, nbSubfr, complexity int) [peMaxNbSubfr][peNbCbksStage3Max][peNbStage3Lags]float64 {
	var out [peMaxNbSubfr][peNbCbksStage3Max][peNbStage3Lags]float64
	var nbCbk, cbSize int
	lagRange := func(k, j int) int { return 0 }
	cb := func(k, i int) int { return 0 }
	if nbSubfr == peMaxNbSubfr {
		lagRange = func(k, j int) int { return silkLagRangeStage3[complexity][k][j] }
		cb = func(k, i int) int { return silkCBLagsStage3[k][i] }
		nbCbk = silkNbCbkSearchsStage3[complexity]
		cbSize = peNbCbksStage3Max
	} else {
		lagRange = func(k, j int) int { return silkLagRangeStage3_10ms[k][j] }
		cb = func(k, i int) int { return silkCBLagsStage3_10ms[k][i] }
		nbCbk = peNbCbksStage3_10
		cbSize = peNbCbksStage3_10
	}
	_ = cbSize

	target := sfLength << 2
	for k := 0; k < nbSubfr; k++ {
		var scratch [scratchSizeSt3]float64
		lagLow := lagRange(k, 0)
		lagHigh := lagRange(k, 1)
		lagCounter := 0
		for j := lagLow; j <= lagHigh; j++ {
			scratch[lagCounter] = innerProdAt(frame, target, target-(startLag+j), sfLength)
			lagCounter++
		}
		delta := lagLow
		for i := 0; i < nbCbk; i++ {
			idx := cb(k, i) - delta
			for j := 0; j < peNbStage3Lags; j++ {
				out[k][i][j] = scratch[idx+j]
			}
		}
		target += sfLength
	}
	return out
}

// silkPAnaCalcEnergySt3 ports silk_P_Ana_calc_energy_st3 (stage-3 energies).
func silkPAnaCalcEnergySt3(frame []float64, startLag, sfLength, nbSubfr, complexity int) [peMaxNbSubfr][peNbCbksStage3Max][peNbStage3Lags]float64 {
	var out [peMaxNbSubfr][peNbCbksStage3Max][peNbStage3Lags]float64
	var nbCbk, cbSize int
	lagRange := func(k, j int) int { return 0 }
	cb := func(k, i int) int { return 0 }
	if nbSubfr == peMaxNbSubfr {
		lagRange = func(k, j int) int { return silkLagRangeStage3[complexity][k][j] }
		cb = func(k, i int) int { return silkCBLagsStage3[k][i] }
		nbCbk = silkNbCbkSearchsStage3[complexity]
		cbSize = peNbCbksStage3Max
	} else {
		lagRange = func(k, j int) int { return silkLagRangeStage3_10ms[k][j] }
		cb = func(k, i int) int { return silkCBLagsStage3_10ms[k][i] }
		nbCbk = peNbCbksStage3_10
		cbSize = peNbCbksStage3_10
	}
	_ = cbSize

	target := sfLength << 2
	for k := 0; k < nbSubfr; k++ {
		var scratch [scratchSizeSt3]float64
		basis := target - (startLag + lagRange(k, 0))
		energy := energyAt(frame, basis, sfLength) + 1e-3
		lagCounter := 0
		scratch[lagCounter] = energy
		lagCounter++
		lagDiff := lagRange(k, 1) - lagRange(k, 0) + 1
		for i := 1; i < lagDiff; i++ {
			energy -= frame[basis+sfLength-i] * frame[basis+sfLength-i]
			energy += frame[basis-i] * frame[basis-i]
			scratch[lagCounter] = energy
			lagCounter++
		}
		delta := lagRange(k, 0)
		for i := 0; i < nbCbk; i++ {
			idx := cb(k, i) - delta
			for j := 0; j < peNbStage3Lags; j++ {
				out[k][i][j] = scratch[idx+j]
			}
		}
		target += sfLength
	}
	return out
}
