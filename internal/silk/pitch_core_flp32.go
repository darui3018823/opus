package silk

import "math"

// pitch_core_flp32.go is a float32-faithful port of libopus
// silk/float/pitch_analysis_core_FLP.c and the helpers that
// silk/float/find_pitch_lags_FLP.c uses. Values are carried in float64
// slices but every silk_float operation is rounded to float32 through f32,
// and the double accumulations (silk_energy_FLP, silk_inner_product_FLP,
// the recursive normalizers) stay in float64, so the results match the
// scalar C build bit for bit.

// silkFloat2Int16Even mirrors silk_float2short: float2int rounds to nearest
// even (SSE cvtss2si) and the result saturates to int16.
func silkFloat2Int16Even(x float64) int16 {
	return silkSAT16(silkFloat2Int(x))
}

func silkFloat2ShortArray32(out []int16, in []float64) {
	for i := range out {
		out[i] = silkFloat2Int16Even(in[i])
	}
}

// silkLog2FLP mirrors silk_log2: (silk_float)(3.32192809488736 * log10(x)).
func silkLog2FLP(x float64) float64 {
	return f32(3.32192809488736 * math.Log10(x))
}

// celtPitchXcorrFLP ports the float celt_pitch_xcorr_c: xcorr[i] is the
// correlation of x with y shifted by i, accumulated in float32 with the
// four-lag xcorr_kernel interleaving for the unrolled part and sequential
// float32 accumulation (celt_inner_prod) for the remainder.
func celtPitchXcorrFLP(x, y []float64, xcorr []float64, length, maxPitch int) {
	i := 0
	for ; i < maxPitch-3; i += 4 {
		var sum [4]float64
		xcorrKernelFLP(x, y[i:], &sum, length)
		xcorr[i] = sum[0]
		xcorr[i+1] = sum[1]
		xcorr[i+2] = sum[2]
		xcorr[i+3] = sum[3]
	}
	for ; i < maxPitch; i++ {
		sum := 0.0
		for j := 0; j < length; j++ {
			sum = f32(sum + f32(x[j]*y[i+j]))
		}
		xcorr[i] = sum
	}
}

// xcorrKernelFLP ports xcorr_kernel_c for the float build (MAC16_16 is a
// float multiply-add, rounded at each step).
func xcorrKernelFLP(x, y []float64, sum *[4]float64, length int) {
	mac := func(acc, a, b float64) float64 { return f32(acc + f32(a*b)) }
	xi, yi := 0, 0
	y0 := y[yi]
	yi++
	y1 := y[yi]
	yi++
	y2 := y[yi]
	yi++
	y3 := 0.0
	j := 0
	for ; j < length-3; j += 4 {
		tmp := x[xi]
		xi++
		y3 = y[yi]
		yi++
		sum[0] = mac(sum[0], tmp, y0)
		sum[1] = mac(sum[1], tmp, y1)
		sum[2] = mac(sum[2], tmp, y2)
		sum[3] = mac(sum[3], tmp, y3)
		tmp = x[xi]
		xi++
		y0 = y[yi]
		yi++
		sum[0] = mac(sum[0], tmp, y1)
		sum[1] = mac(sum[1], tmp, y2)
		sum[2] = mac(sum[2], tmp, y3)
		sum[3] = mac(sum[3], tmp, y0)
		tmp = x[xi]
		xi++
		y1 = y[yi]
		yi++
		sum[0] = mac(sum[0], tmp, y2)
		sum[1] = mac(sum[1], tmp, y3)
		sum[2] = mac(sum[2], tmp, y0)
		sum[3] = mac(sum[3], tmp, y1)
		tmp = x[xi]
		xi++
		y2 = y[yi]
		yi++
		sum[0] = mac(sum[0], tmp, y3)
		sum[1] = mac(sum[1], tmp, y0)
		sum[2] = mac(sum[2], tmp, y1)
		sum[3] = mac(sum[3], tmp, y2)
	}
	if j < length {
		j++
		tmp := x[xi]
		xi++
		y3 = y[yi]
		yi++
		sum[0] = mac(sum[0], tmp, y0)
		sum[1] = mac(sum[1], tmp, y1)
		sum[2] = mac(sum[2], tmp, y2)
		sum[3] = mac(sum[3], tmp, y3)
	}
	if j < length {
		j++
		tmp := x[xi]
		xi++
		y0 = y[yi]
		yi++
		sum[0] = mac(sum[0], tmp, y1)
		sum[1] = mac(sum[1], tmp, y2)
		sum[2] = mac(sum[2], tmp, y3)
		sum[3] = mac(sum[3], tmp, y0)
	}
	if j < length {
		tmp := x[xi]
		y1 = y[yi]
		sum[0] = mac(sum[0], tmp, y2)
		sum[1] = mac(sum[1], tmp, y3)
		sum[2] = mac(sum[2], tmp, y0)
		sum[3] = mac(sum[3], tmp, y1)
	}
}

// silkApplySineWindowFLP32 ports silk_apply_sine_window_FLP with float32
// recursion state.
func silkApplySineWindowFLP32(out, in []float64, winType, length int) {
	// freq = PI / (length + 1) with PI the silk_float 3.1415926536f: a
	// float32 division (the double quotient rounds differently for some
	// lengths, e.g. the 12 kHz shaping slope of 72).
	freq := float64(float32(3.1415926536) / float32(length+1))
	c := f32(2.0 - f32(freq*freq))
	var s0, s1 float64
	if winType < 2 {
		s0 = 0.0
		s1 = freq
	} else {
		s0 = 1.0
		s1 = f32(0.5 * c)
	}
	for k := 0; k < length; k += 4 {
		out[k+0] = f32(f32(in[k+0]*0.5) * f32(s0+s1))
		out[k+1] = f32(in[k+1] * s1)
		s0 = f32(f32(c*s1) - s0)
		out[k+2] = f32(f32(in[k+2]*0.5) * f32(s1+s0))
		out[k+3] = f32(in[k+3] * s0)
		s1 = f32(f32(c*s0) - s1)
	}
}

// silkAutocorrelationFLP32 ports silk_autocorrelation_FLP.
func silkAutocorrelationFLP32(results, input []float64, n, count int) {
	if count > n {
		count = n
	}
	for i := 0; i < count; i++ {
		results[i] = f32(silkInnerProductFLP32(input, input[i:], n-i))
	}
}

// silkSchurFLP32 ports silk_schur_FLP: double recursion with float32 outputs.
func silkSchurFLP32(reflCoef, autoCorr []float64, order int) float64 {
	var c [silkMaxShapeLPCOrder + 1][2]float64
	for k := 0; k <= order; k++ {
		c[k][0] = autoCorr[k]
		c[k][1] = autoCorr[k]
	}
	for k := 0; k < order; k++ {
		// silk_max_float is a macro, so the double energy is compared with
		// the float literal and used unconverted.
		denom := c[0][1]
		if denom < float64(float32(1e-9)) {
			denom = float64(float32(1e-9))
		}
		rcTmp := -c[k+1][0] / denom
		reflCoef[k] = f32(rcTmp)
		for n := 0; n < order-k; n++ {
			ctmp1 := c[n+k+1][0]
			ctmp2 := c[n][1]
			c[n+k+1][0] = ctmp1 + float64(ctmp2*rcTmp)
			c[n][1] = ctmp2 + float64(ctmp1*rcTmp)
		}
	}
	return f32(c[0][1])
}

// silkK2aFLP32 ports silk_k2a_FLP in float32.
func silkK2aFLP32(a, rc []float64, order int) {
	for k := 0; k < order; k++ {
		rck := rc[k]
		for n := 0; n < (k+1)>>1; n++ {
			tmp1 := a[n]
			tmp2 := a[k-n-1]
			a[n] = f32(tmp1 + f32(tmp2*rck))
			a[k-n-1] = f32(tmp2 + f32(tmp1*rck))
		}
		a[k] = -rck
	}
}

// silkBwexpanderFLP32 ports silk_bwexpander_FLP in float32.
func silkBwexpanderFLP32(ar []float64, d int, chirp float64) {
	cfac := f32(chirp)
	for i := 0; i < d-1; i++ {
		ar[i] = f32(ar[i] * cfac)
		cfac = f32(cfac * chirp)
	}
	ar[d-1] = f32(ar[d-1] * cfac)
}

// silkInsertionSortDecreasingFLP32 is silk_insertion_sort_decreasing_FLP; it
// only moves values, so the float64 version is already exact.
func silkInsertionSortDecreasingFLP32(a []float64, idx []int, L, K int) {
	silkInsertionSortDecreasingFLP(a, idx, L, K)
}

// silkPitchAnalysisCoreFLP32 ports silk_pitch_analysis_core_FLP. frame holds
// the whitened residual of length (PE_LTP_MEM_LENGTH_MS +
// nbSubfr*PE_SUBFR_LENGTH_MS)*fsKHz in float32-representable int16 scale;
// ltpCorr is the previous frame's normalized correlation on input and the
// new one on output. It returns the per-subframe lags, lag index, contour
// index, and whether the frame is voiced.
func silkPitchAnalysisCoreFLP32(
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

	frame8kHz := make([]float64, frameLength8kHz)
	frame4kHz := make([]float64, frameLength4kHz)
	frame8FIX := make([]int16, frameLength8kHz)
	frame4FIX := make([]int16, frameLength4kHz)
	filtState := make([]int32, 6)

	switch fsKHz {
	case 16:
		frame16FIX := make([]int16, frameLength)
		silkFloat2ShortArray32(frame16FIX, frame[:frameLength])
		silkResamplerDown2(filtState[:2], frame8FIX, frame16FIX)
		silkShort2FloatArray(frame8kHz, frame8FIX)
	case 12:
		frame12FIX := make([]int16, frameLength)
		silkFloat2ShortArray32(frame12FIX, frame[:frameLength])
		silkResamplerDown2_3(filtState[:6], frame8FIX, frame12FIX)
		silkShort2FloatArray(frame8kHz, frame8FIX)
	default:
		silkFloat2ShortArray32(frame8FIX, frame[:frameLength8kHz])
	}

	for i := range filtState {
		filtState[i] = 0
	}
	silkResamplerDown2(filtState[:2], frame4FIX, frame8FIX)
	silkShort2FloatArray(frame4kHz, frame4FIX)

	// Low-pass filter: silk_ADD_SAT16 on int16-valued floats.
	for i := frameLength4kHz - 1; i > 0; i-- {
		frame4kHz[i] = float64(silkSAT16(int32(frame4kHz[i]) + int32(frame4kHz[i-1])))
	}

	// ── FIRST STAGE, operating in 4 kHz ──────────────────────────────────
	c0 := make([]float64, peCBufLen)
	xcorr := make([]float64, peMaxLagMs*4-peMinLagMs*4+1)
	target := sfLength4kHz << 2
	for k := 0; k < nbSubfr>>1; k++ {
		basis := target - minLag4kHz
		celtPitchXcorrFLP(frame4kHz[target:], frame4kHz[target-maxLag4kHz:], xcorr, sfLength8kHz, maxLag4kHz-minLag4kHz+1)

		crossCorr := xcorr[maxLag4kHz-minLag4kHz]
		normalizer := silkEnergyFLP32(frame4kHz[target:target+sfLength8kHz]) +
			silkEnergyFLP32(frame4kHz[basis:basis+sfLength8kHz]) +
			f32(float64(sfLength8kHz)*4000.0)
		c0[minLag4kHz] = f32(c0[minLag4kHz] + f32(2*crossCorr/normalizer))

		for d := minLag4kHz + 1; d <= maxLag4kHz; d++ {
			basis--
			crossCorr = xcorr[maxLag4kHz-d]
			normalizer += float64(frame4kHz[basis]*frame4kHz[basis]) -
				float64(frame4kHz[basis+sfLength8kHz]*frame4kHz[basis+sfLength8kHz])
			c0[d] = f32(c0[d] + f32(2*crossCorr/normalizer))
		}
		target += sfLength8kHz
	}

	// Apply short-lag bias.
	for i := maxLag4kHz; i >= minLag4kHz; i-- {
		c0[i] = f32(c0[i] - f32(f32(c0[i]*float64(i))/4096.0))
	}

	lengthDSrch := 4 + 2*complexity
	dSrch := make([]int, peDSrchLength)
	silkInsertionSortDecreasingFLP32(c0[minLag4kHz:maxLag4kHz+1], dSrch, maxLag4kHz-minLag4kHz+1, lengthDSrch)

	cmax := c0[minLag4kHz]
	if cmax < f32(0.2) {
		*ltpCorr = 0.0
		return pitchOut, 0, 0, false
	}

	threshold := f32(searchThres1 * cmax)
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

	// ── SECOND STAGE, operating at 8 kHz ─────────────────────────────────
	cc := make([][]float64, peMaxNbSubfr)
	for i := range cc {
		cc[i] = make([]float64, peCBufLen)
	}
	stage2 := frame8kHz
	if fsKHz == 8 {
		stage2 = frame
	}
	stage2Off := peLtpMemLengthMs * 8
	for k := 0; k < nbSubfr; k++ {
		base := stage2Off + k*sfLength8kHz
		energyTmp := silkEnergyFLP32(stage2[base:base+sfLength8kHz]) + 1.0
		for j := 0; j < lengthDComp; j++ {
			d := dCompOut[j]
			crossCorr := silkInnerProductFLP32(stage2[base-d:], stage2[base:], sfLength8kHz)
			if crossCorr > 0.0 {
				energy := silkEnergyFLP32(stage2[base-d : base-d+sfLength8kHz])
				cc[k][d] = f32(2 * crossCorr / (energy + energyTmp))
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
		prevLagLog2 = silkLog2FLP(float64(prevLag))
	}

	var nbCbkSearch int
	cbStage2 := func(i, j int) int { return silkCBLagsStage2[i][j] }
	if nbSubfr == peMaxNbSubfr {
		if fsKHz == 8 && complexity > silkPEMinComplex {
			nbCbkSearch = peNbCbksStage2Ext
		} else {
			nbCbkSearch = peNbCbksStage2
		}
	} else {
		cbStage2 = func(i, j int) int { return silkCBLagsStage2_10ms[i][j] }
		nbCbkSearch = peNbCbksStage2_10
	}

	ccArr := make([]float64, peNbCbksStage2Ext)
	nbSubfrF := float64(nbSubfr)
	shortlagBias := f32(f32(peShortlagBias) * nbSubfrF)
	prevlagBias := f32(f32(pePrevlagBias) * nbSubfrF)
	thres2 := f32(nbSubfrF * searchThres2)
	for k := 0; k < lengthDSrch; k++ {
		d := dSrch[k]
		for j := 0; j < nbCbkSearch; j++ {
			ccArr[j] = 0.0
			for i := 0; i < nbSubfr; i++ {
				ccArr[j] = f32(ccArr[j] + cc[i][d+cbStage2(i, j)])
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
		lagLog2 := silkLog2FLP(float64(d))
		ccmaxNewB := f32(ccmaxNew - f32(shortlagBias*lagLog2))
		if prevLag > 0 {
			deltaLagLog2Sqr := f32(lagLog2 - prevLagLog2)
			deltaLagLog2Sqr = f32(deltaLagLog2Sqr * deltaLagLog2Sqr)
			ccmaxNewB = f32(ccmaxNewB - f32(f32(f32(prevlagBias*(*ltpCorr))*deltaLagLog2Sqr)/f32(deltaLagLog2Sqr+0.5)))
		}
		if ccmaxNewB > ccmaxB && ccmaxNew > thres2 {
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

	*ltpCorr = f32(ccmax / nbSubfrF)

	if fsKHz > 8 {
		if fsKHz == 12 {
			lag = int(silkRShiftRound(int64(silkSMULBB(int32(lag), 3)), 1))
		} else {
			lag <<= 1
		}
		lag = clampInt(lag, minLag, maxLag)
		startLag := silkMaxInt(lag-2, minLag)
		endLag := silkMinInt(lag+2, maxLag)
		lagNew := lag
		cbimax = 0
		ccmax = -1000.0

		corrSt3 := silkPAnaCalcCorrSt3FLP32(frame, startLag, sfLength, nbSubfr, complexity)
		energiesSt3 := silkPAnaCalcEnergySt3FLP32(frame, startLag, sfLength, nbSubfr, complexity)

		contourBias := f32(f32(peFlatcontourBias) / float64(lag))

		var nbCbk int
		cbStage3 := func(k, i int) int { return silkCBLagsStage3[k][i] }
		if nbSubfr == peMaxNbSubfr {
			nbCbk = silkNbCbkSearchsStage3[complexity]
		} else {
			nbCbk = peNbCbksStage3_10
			cbStage3 = func(k, i int) int { return silkCBLagsStage3_10ms[k][i] }
		}

		targetOff := peLtpMemLengthMs * fsKHz
		energyTmp := silkEnergyFLP32(frame[targetOff:targetOff+nbSubfr*sfLength]) + 1.0
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
					ccmaxNew = f32(2 * crossCorr / energy)
					// Reduce depending on flatness of contour.
					ccmaxNew = f32(ccmaxNew * f32(1.0-f32(contourBias*float64(j))))
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
			pitchOut[k] = clampInt(lagNew+cbStage3(k, cbimax), minLag, peMaxLagMs*fsKHz)
		}
		lagIndex = lagNew - minLag
		contourIndex = cbimax
	} else {
		for k := 0; k < nbSubfr; k++ {
			pitchOut[k] = clampInt(lag+cbStage2(k, cbimax), minLag8kHz, peMaxLagMs*8)
		}
		lagIndex = lag - minLag8kHz
		contourIndex = cbimax
	}
	return pitchOut, lagIndex, contourIndex, true
}

// silkPAnaCalcCorrSt3FLP32 ports silk_P_Ana_calc_corr_st3 with the float
// celt_pitch_xcorr kernel.
func silkPAnaCalcCorrSt3FLP32(frame []float64, startLag, sfLength, nbSubfr, complexity int) [peMaxNbSubfr][peNbCbksStage3Max][peNbStage3Lags]float64 {
	var crossCorr [peMaxNbSubfr][peNbCbksStage3Max][peNbStage3Lags]float64
	var lagRange func(k, col int) int
	var lagCB func(k, i int) int
	var nbCbkSearch int
	if nbSubfr == peMaxNbSubfr {
		lagRange = func(k, col int) int { return silkLagRangeStage3[complexity][k][col] }
		lagCB = func(k, i int) int { return silkCBLagsStage3[k][i] }
		nbCbkSearch = silkNbCbkSearchsStage3[complexity]
	} else {
		lagRange = func(k, col int) int { return silkLagRangeStage3_10ms[k][col] }
		lagCB = func(k, i int) int { return silkCBLagsStage3_10ms[k][i] }
		nbCbkSearch = peNbCbksStage3_10
	}

	const scratchSize = 22
	var scratch [scratchSize]float64
	xcorr := make([]float64, scratchSize)
	target := sfLength << 2
	for k := 0; k < nbSubfr; k++ {
		lagCounter := 0
		lagLow := lagRange(k, 0)
		lagHigh := lagRange(k, 1)
		celtPitchXcorrFLP(frame[target:], frame[target-startLag-lagHigh:], xcorr, sfLength, lagHigh-lagLow+1)
		for j := lagLow; j <= lagHigh; j++ {
			scratch[lagCounter] = xcorr[lagHigh-j]
			lagCounter++
		}
		delta := lagRange(k, 0)
		for i := 0; i < nbCbkSearch; i++ {
			idx := lagCB(k, i) - delta
			for j := 0; j < peNbStage3Lags; j++ {
				crossCorr[k][i][j] = scratch[idx+j]
			}
		}
		target += sfLength
	}
	return crossCorr
}

// silkPAnaCalcEnergySt3FLP32 ports silk_P_Ana_calc_energy_st3: the energies
// are computed recursively in double with float32 snapshots.
func silkPAnaCalcEnergySt3FLP32(frame []float64, startLag, sfLength, nbSubfr, complexity int) [peMaxNbSubfr][peNbCbksStage3Max][peNbStage3Lags]float64 {
	var energies [peMaxNbSubfr][peNbCbksStage3Max][peNbStage3Lags]float64
	var lagRange func(k, col int) int
	var lagCB func(k, i int) int
	var nbCbkSearch int
	if nbSubfr == peMaxNbSubfr {
		lagRange = func(k, col int) int { return silkLagRangeStage3[complexity][k][col] }
		lagCB = func(k, i int) int { return silkCBLagsStage3[k][i] }
		nbCbkSearch = silkNbCbkSearchsStage3[complexity]
	} else {
		lagRange = func(k, col int) int { return silkLagRangeStage3_10ms[k][col] }
		lagCB = func(k, i int) int { return silkCBLagsStage3_10ms[k][i] }
		nbCbkSearch = peNbCbksStage3_10
	}

	const scratchSize = 22
	var scratch [scratchSize]float64
	target := sfLength << 2
	for k := 0; k < nbSubfr; k++ {
		lagCounter := 0
		basis := target - (startLag + lagRange(k, 0))
		energy := silkEnergyFLP32(frame[basis:basis+sfLength]) + 1e-3
		scratch[lagCounter] = f32(energy)
		lagCounter++

		lagDiff := lagRange(k, 1) - lagRange(k, 0) + 1
		for i := 1; i < lagDiff; i++ {
			energy -= float64(frame[basis+sfLength-i] * frame[basis+sfLength-i])
			energy += float64(frame[basis-i] * frame[basis-i])
			scratch[lagCounter] = f32(energy)
			lagCounter++
		}

		delta := lagRange(k, 0)
		for i := 0; i < nbCbkSearch; i++ {
			idx := lagCB(k, i) - delta
			for j := 0; j < peNbStage3Lags; j++ {
				energies[k][i][j] = scratch[idx+j]
			}
		}
		target += sfLength
	}
	return energies
}

// findPitchLagsResult collects the checkpoints of silkFindPitchLagsFLP32.
type findPitchLagsResult struct {
	autoCorr     []float64
	reflCoef     []float64
	a            []float64
	resNrg       float64
	predGain     float64
	threshold    float64
	res          []float64
	pitchL       []int
	lagIndex     int
	contourIndex int
	ltpCorr      float64
	voiced       bool
}

// silkFindPitchLagsFLP32 ports silk_find_pitch_lags_FLP on an explicit
// x_buf of ltp_mem_length + frame_length + la_pitch float32-valued samples in
// int16 scale. When runCore is false only the whitening stages run (the
// libopus inactive/first-frame branch).
func silkFindPitchLagsFLP32(xBuf []float64, laPitch, winLen, order, fsKHz, nbSubfr, peComplexity int,
	speechActivityQ8, prevSignalType, inputTiltQ15, pitchEstThresholdQ16, prevLag int, ltpCorrIn float64, runCore bool) findPitchLagsResult {
	bufLen := len(xBuf)
	var r findPitchLagsResult

	wsig := make([]float64, winLen)
	xStart := bufLen - winLen
	silkApplySineWindowFLP32(wsig[:laPitch], xBuf[xStart:], 1, laPitch)
	copy(wsig[laPitch:winLen-laPitch], xBuf[xStart+laPitch:xStart+winLen-laPitch])
	silkApplySineWindowFLP32(wsig[winLen-laPitch:], xBuf[xStart+winLen-laPitch:], 2, laPitch)

	r.autoCorr = make([]float64, order+1)
	silkAutocorrelationFLP32(r.autoCorr, wsig, winLen, order+1)
	// auto_corr[0] += auto_corr[0] * FIND_PITCH_WHITE_NOISE_FRACTION + 1
	r.autoCorr[0] = f32(r.autoCorr[0] + f32(f32(r.autoCorr[0]*f32(findPitchWhiteNoiseFraction))+1))

	r.reflCoef = make([]float64, order)
	r.resNrg = silkSchurFLP32(r.reflCoef, r.autoCorr, order)
	denom := r.resNrg
	if denom < 1.0 {
		denom = 1.0
	}
	r.predGain = f32(r.autoCorr[0] / denom)

	r.a = make([]float64, order)
	silkK2aFLP32(r.a, r.reflCoef, order)
	silkBwexpanderFLP32(r.a, order, f32(findPitchBandwidthExpansion))

	r.res = make([]float64, bufLen)
	silkLPCAnalysisFilterFLP32(r.res, r.a, xBuf, bufLen, order)

	r.pitchL = make([]int, nbSubfr)
	if !runCore {
		return r
	}
	thrhld := f32(0.6)
	thrhld = f32(thrhld - f32(f32(0.004)*float64(order)))
	thrhld = f32(thrhld - f32(f32(f32(0.1)*float64(speechActivityQ8))*f32(1.0/256.0)))
	thrhld = f32(thrhld - f32(f32(0.15)*float64(prevSignalType>>1)))
	thrhld = f32(thrhld - f32(f32(f32(0.1)*float64(inputTiltQ15))*f32(1.0/32768.0)))
	r.threshold = thrhld
	thres1 := f32(float64(pitchEstThresholdQ16) / 65536.0)

	ltpCorr := ltpCorrIn
	pitchL, lagIndex, contourIndex, voiced := silkPitchAnalysisCoreFLP32(r.res, &ltpCorr, prevLag, thres1, thrhld, fsKHz, peComplexity, nbSubfr)
	r.pitchL = pitchL
	r.lagIndex = lagIndex
	r.contourIndex = contourIndex
	r.ltpCorr = ltpCorr
	r.voiced = voiced
	return r
}
