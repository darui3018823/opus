package celt

import "math"

// Float32-faithful ports of the libopus 1.6.1 float-build frame analysis in
// celt/celt_encoder.c (tone_lpc, tone_detect, transient_analysis,
// dynalloc_analysis) and celt/bands.c (spreading_decision). In the float
// build opus_val16/opus_val32/celt_glog are float and the fixed-point shift
// macros are identities, so every intermediate here rounds to float32; the C
// expressions that promote to double (sqrt, floor, acos, exp) are kept in
// float64 exactly where libopus does.

// toneLPC mirrors tone_lpc: a least-squares 2-tap LPC fit over forward and
// backward prediction at the given delay. It returns fail when the
// normal-equation determinant is too small.
func toneLPC(x []float32, delay int) (lpc [2]float32, fail bool) {
	n := len(x)
	var r00, r01, r11, r02, r12, r22 float32
	for i := 0; i < n-2*delay; i++ {
		r00 += float32(x[i] * x[i])
		r01 += float32(x[i] * x[i+delay])
		r02 += float32(x[i] * x[i+2*delay])
	}
	var edges float32
	for i := 0; i < delay; i++ {
		edges += float32(x[n+i-2*delay]*x[n+i-2*delay]) - float32(x[i]*x[i])
	}
	r11 = r00 + edges
	edges = 0
	for i := 0; i < delay; i++ {
		edges += float32(x[n+i-delay]*x[n+i-delay]) - float32(x[i+delay]*x[i+delay])
	}
	r22 = r11 + edges
	edges = 0
	for i := 0; i < delay; i++ {
		edges += float32(x[n+i-2*delay]*x[n+i-delay]) - float32(x[i]*x[i+delay])
	}
	r12 = r01 + edges
	// Reverse and sum to get the backward contribution.
	R00 := r00 + r22
	R01 := r01 + r12
	R11 := 2 * r11
	R02 := 2 * r02
	R12 := r12 + r01
	r00, r01, r11, r02, r12 = R00, R01, R11, R02, R12
	// Solve A*x=b, where A=[r00, r01; r01, r11] and b=[r02; r12].
	den := float32(r00*r11) - float32(r01*r01)
	if den < float32(0.001)*float32(r00*r11) {
		return lpc, true
	}
	num1 := float32(r02*r11) - float32(r01*r12)
	switch {
	case num1 >= den:
		lpc[1] = 1
	case num1 <= -den:
		lpc[1] = -1
	default:
		lpc[1] = num1 / den
	}
	num0 := float32(r00*r12) - float32(r02*r01)
	switch {
	case float32(0.5)*num0 >= den:
		lpc[0] = 1.999999
	case float32(0.5)*num0 <= -den:
		lpc[0] = -1.999999
	default:
		lpc[0] = num0 / den
	}
	return lpc, false
}

// toneDetect mirrors tone_detect: it fits a resonator to the pre-emphasised
// input (both channels summed for stereo) and returns the tone frequency in
// radians per sample (-1 when there is no clear tone) and the "toneishness"
// (squared pole radius, 0 when there is no tone).
func toneDetect(in [][]float64, n int, fs int, scratch []float32) (freq float32, toneishness float32) {
	x := scratch[:n]
	if len(in) == 2 {
		for i := 0; i < n; i++ {
			x[i] = float32(in[0][i]) + float32(in[1][i])
		}
	} else {
		for i := 0; i < n; i++ {
			x[i] = float32(in[0][i])
		}
	}
	delay := 1
	lpc, fail := toneLPC(x, delay)
	// If our LPC filter resonates too close to DC, retry the analysis with
	// down-sampling.
	for delay <= fs/3000 && (fail || (lpc[0] > 1 && lpc[1] < 0)) {
		delay *= 2
		lpc, fail = toneLPC(x, delay)
	}
	// Check that our filter has complex roots.
	if !fail && float32(lpc[0]*lpc[0])+float32(float32(3.999999)*lpc[1]) < 0 {
		toneishness = -lpc[1]
		freq = float32(math.Acos(float64(float32(0.5)*lpc[0])) / float64(delay))
		return freq, toneishness
	}
	return -1, 0
}

// transientAnalysis32 mirrors transient_analysis. in holds the per-channel
// analysis buffers (overlap + frame). It returns the transient flag, the
// tf_estimate, the channel that triggered it and the weak-transient flag.
func transientAnalysis32(in [][]float64, length, C int, allowWeakTransients bool, toneFreq, toneishness float32, scratch []float32) (isTransient bool, tfEstimate float32, tfChan int, weakTransient bool) {
	forwardDecay := float32(0.0625)
	if allowWeakTransients {
		forwardDecay = 0.03125
	}
	tmp := scratch[:length]
	len2 := length / 2
	maskMetric := 0
	for c := 0; c < C; c++ {
		var mem0, mem1 float32
		// High-pass filter: (1 - 2*z^-1 + z^-2) / (1 - z^-1 + .5*z^-2)
		for i := 0; i < length; i++ {
			x := float32(in[c][i])
			y := mem0 + x
			mem00 := mem0
			mem0 = float32(mem0-x) + float32(float32(0.5)*mem1)
			mem1 = x - mem00
			tmp[i] = y
		}
		// First few samples are bad because we don't propagate the memory.
		for i := 0; i < 12; i++ {
			tmp[i] = 0
		}
		var mean float32
		mem0 = 0
		// Forward pass to compute the post-echo threshold.
		for i := 0; i < len2; i++ {
			x2 := float32(tmp[2*i]*tmp[2*i]) + float32(tmp[2*i+1]*tmp[2*i+1])
			mean += x2
			mem0 = x2 + float32(float32(1-forwardDecay)*mem0)
			tmp[i] = forwardDecay * mem0
		}
		mem0 = 0
		var maxE float32
		// Backward pass to compute the pre-echo threshold.
		for i := len2 - 1; i >= 0; i-- {
			mem0 = tmp[i] + float32(float32(0.875)*mem0)
			tmp[i] = float32(0.125) * mem0
			if v := float32(0.125) * mem0; v > maxE {
				maxE = v
			}
		}
		// Frame energy: geometric mean of the energy and half the max
		// (mean * maxE * .5 * len2 promotes to double after the first product).
		mean = float32(math.Sqrt(float64(float32(mean*maxE)) * 0.5 * float64(len2)))
		// Inverse of the mean energy.
		norm := float32(len2) / (float32(1e-15) + mean)
		// Harmonic mean discarding the unreliable boundaries; the data is
		// smooth, so only 1/4th of the samples are used.
		unmask := 0
		for i := 12; i < len2-5; i += 4 {
			v := math.Floor(float64(float32(float32(64*norm) * float32(tmp[i]+float32(1e-15)))))
			if v < 0 {
				v = 0
			}
			if v > 127 {
				v = 127
			}
			unmask += transientInvTable[int(v)]
		}
		// Normalize, compensate for the 1/4th of the sample and the factor of
		// 6 in the inverse table.
		unmask = 64 * unmask * 4 / (6 * (len2 - 17))
		if unmask > maskMetric {
			tfChan = c
			maskMetric = unmask
		}
	}
	isTransient = maskMetric > 200
	// Prevent the transient detector from confusing the partial cycle of a
	// very low frequency tone with a transient.
	if toneishness > 0.98 && toneFreq < 0.026 {
		isTransient = false
		maskMetric = 0
	}
	// For low bitrates, define "weak transients" that need to be handled
	// differently to avoid partial collapse.
	if allowWeakTransients && isTransient && maskMetric < 600 {
		isTransient = false
		weakTransient = true
	}
	// Arbitrary metric for VBR boost.
	tfMax := float32(math.Sqrt(float64(27*maskMetric))) - 42
	if tfMax < 0 {
		tfMax = 0
	}
	if tfMax > 163 {
		tfMax = 163
	}
	// 0.0069 is cast to float by MULT16_16 while 0.139 stays double.
	arg := float64(float32(float32(0.0069)*tfMax)) - 0.139
	if arg < 0 {
		arg = 0
	}
	tfEstimate = float32(math.Sqrt(arg))
	return isTransient, tfEstimate, tfChan, weakTransient
}

// celtExp2DB is celt_exp2_db (= celt_exp2) of the float build without
// FLOAT_APPROX: (float)exp(0.6931471805599453094*x).
func celtExp2DB(x float32) float32 {
	return float32(math.Exp(0.6931471805599453094 * float64(x)))
}

// dynallocAnalysisResult carries the outputs of dynalloc_analysis.
type dynallocAnalysisResult struct {
	offsets      []int // per-band boost counts, in the units dynallocEncode codes
	importance   []int
	spreadWeight []int
	totBoost     int // Q3 bits
	maxDepth     float32
}

// dynallocAnalysis32 mirrors dynalloc_analysis. bandLogE, bandLogE2 and
// oldBandE are channel-major (c*nbEBands+i) float32-valued slices;
// surroundDynalloc may be nil (all zero); lsbDepth is the encoder's
// OPUS_SET_LSB_DEPTH; effectiveBytes the libopus effectiveBytes.
func dynallocAnalysis32(bandLogE, bandLogE2, oldBandE []float64, nbEBands, start, end, C, lsbDepth, lm int,
	isTransient, vbr, constrainedVBR bool, effectiveBytes int, surroundDynalloc []float64,
	toneFreq, toneishness float32, analysis *AnalysisInfo) dynallocAnalysisResult {
	return dynallocAnalysis32Scratch(bandLogE, bandLogE2, oldBandE, nbEBands, start, end, C, lsbDepth, lm,
		isTransient, vbr, constrainedVBR, effectiveBytes, surroundDynalloc, toneFreq, toneishness, analysis, nil)
}

// dynallocScratch holds dynallocAnalysis32's work buffers so a frame does
// not allocate; the result slices alias it until the next call.
type dynallocScratch struct {
	ints  []int
	f32   []float32
	mask  []float32
	sig   []float32
	f32ok bool
}

// dynallocAnalysis32Scratch is dynallocAnalysis32 with reusable storage
// (nil allocates).
func dynallocAnalysis32Scratch(bandLogE, bandLogE2, oldBandE []float64, nbEBands, start, end, C, lsbDepth, lm int,
	isTransient, vbr, constrainedVBR bool, effectiveBytes int, surroundDynalloc []float64,
	toneFreq, toneishness float32, analysis *AnalysisInfo, sc *dynallocScratch) dynallocAnalysisResult {
	if sc == nil {
		sc = &dynallocScratch{}
	}
	if cap(sc.ints) < 3*nbEBands {
		sc.ints = make([]int, 3*nbEBands)
	}
	if cap(sc.f32) < (C+2)*nbEBands {
		sc.f32 = make([]float32, (C+2)*nbEBands)
	}
	if cap(sc.mask) < nbEBands {
		sc.mask = make([]float32, nbEBands)
		sc.sig = make([]float32, nbEBands)
	}
	ints := sc.ints[:3*nbEBands]
	for i := range ints {
		ints[i] = 0
	}
	f32 := sc.f32[:(C+2)*nbEBands]
	for i := range f32 {
		f32[i] = 0
	}
	res := dynallocAnalysisResult{
		offsets:      ints[:nbEBands:nbEBands],
		importance:   ints[nbEBands : 2*nbEBands : 2*nbEBands],
		spreadWeight: ints[2*nbEBands : 3*nbEBands : 3*nbEBands],
	}
	follower := f32[: C*nbEBands : C*nbEBands]
	noiseFloor := f32[C*nbEBands : (C+1)*nbEBands : (C+1)*nbEBands]
	bandLogE3 := f32[(C+1)*nbEBands : (C+2)*nbEBands : (C+2)*nbEBands]
	maxDepth := float32(-31.9)
	for i := 0; i < end; i++ {
		// Noise floor must take into account eMeans, the depth, the width of
		// the bands and the preemphasis filter (approx. square of bark band ID).
		nf := float32(float32(0.0625)*float32(LogN400[i])) + float32(0.5)
		nf += float32(9 - lsbDepth)
		nf -= float32(EMean(i))
		nf += float32(float32(float32(0.0062)*float32(i+5)) * float32(i+5))
		noiseFloor[i] = nf
	}
	for c := 0; c < C; c++ {
		for i := 0; i < end; i++ {
			if d := float32(bandLogE[c*nbEBands+i]) - noiseFloor[i]; d > maxDepth {
				maxDepth = d
			}
		}
	}
	res.maxDepth = maxDepth
	{
		// Compute a really simple masking model to avoid taking into account
		// completely masked bands when computing the spreading decision.
		mask := sc.mask[:nbEBands]
		sig := sc.sig[:nbEBands]
		for i := range mask {
			mask[i], sig[i] = 0, 0
		}
		for i := 0; i < end; i++ {
			mask[i] = float32(bandLogE[i]) - noiseFloor[i]
		}
		if C == 2 {
			for i := 0; i < end; i++ {
				if v := float32(bandLogE[nbEBands+i]) - noiseFloor[i]; v > mask[i] {
					mask[i] = v
				}
			}
		}
		copy(sig, mask[:end])
		for i := 1; i < end; i++ {
			if v := mask[i-1] - 2; v > mask[i] {
				mask[i] = v
			}
		}
		for i := end - 2; i >= 0; i-- {
			if v := mask[i+1] - 3; v > mask[i] {
				mask[i] = v
			}
		}
		for i := 0; i < end; i++ {
			// Compute SMR: Mask is never more than 72 dB below the peak and
			// never below the noise floor.
			m := maxDepth - 12
			if m < 0 {
				m = 0
			}
			if mask[i] > m {
				m = mask[i]
			}
			smr := sig[i] - m
			shift := -int(math.Floor(float64(float32(0.5) + smr)))
			if shift < 0 {
				shift = 0
			}
			if shift > 5 {
				shift = 5
			}
			res.spreadWeight[i] = 32 >> uint(shift)
		}
	}
	// Make sure that dynamic allocation can't make us bust the budget. We
	// enable the feature starting at 24 kb/s for 20-ms frames and 96 kb/s for
	// 2.5 ms frames.
	if effectiveBytes < 30+5*lm {
		for i := start; i < end; i++ {
			res.importance[i] = 13
		}
		return res
	}
	last := 0
	for c := 0; c < C; c++ {
		for i := 0; i < end; i++ {
			bandLogE3[i] = float32(bandLogE2[c*nbEBands+i])
		}
		if lm == 0 {
			// For 2.5 ms frames, the first 8 bands have just one bin, so the
			// energy is highly unreliable (high variance). For that reason,
			// we take the max with the previous energy so that at least 2
			// bins are getting used.
			for i := 0; i < 8 && i < end; i++ {
				if v := float32(oldBandE[c*nbEBands+i]); v > bandLogE3[i] {
					bandLogE3[i] = v
				}
			}
		}
		f := follower[c*nbEBands:]
		f[0] = bandLogE3[0]
		for i := 1; i < end; i++ {
			// The last band to be at least 3 dB higher than the previous one
			// is the last we'll consider. Otherwise, we run into problems on
			// bandlimited signals.
			if bandLogE3[i] > bandLogE3[i-1]+float32(0.5) {
				last = i
			}
			f[i] = f[i-1] + float32(1.5)
			if bandLogE3[i] < f[i] {
				f[i] = bandLogE3[i]
			}
		}
		for i := last - 1; i >= 0; i-- {
			v := f[i+1] + 2
			if bandLogE3[i] < v {
				v = bandLogE3[i]
			}
			if v < f[i] {
				f[i] = v
			}
		}
		// Combine with a median filter to avoid dynalloc triggering
		// unnecessarily. The "offset" value controls how conservative we are
		// -- a higher offset reduces the impact of the median filter and makes
		// dynalloc use more bits.
		const offset = float32(1)
		for i := 2; i < end-2; i++ {
			if v := medianOf5F32(bandLogE3[i-2:i+3]) - offset; v > f[i] {
				f[i] = v
			}
		}
		tmp := medianOf3F32(bandLogE3[0:3]) - offset
		if tmp > f[0] {
			f[0] = tmp
		}
		if tmp > f[1] {
			f[1] = tmp
		}
		tmp = medianOf3F32(bandLogE3[end-3:end]) - offset
		if tmp > f[end-2] {
			f[end-2] = tmp
		}
		if tmp > f[end-1] {
			f[end-1] = tmp
		}
		for i := 0; i < end; i++ {
			if noiseFloor[i] > f[i] {
				f[i] = noiseFloor[i]
			}
		}
	}
	if C == 2 {
		for i := start; i < end; i++ {
			// Consider 24 dB "cross-talk".
			if v := follower[i] - 4; v > follower[nbEBands+i] {
				follower[nbEBands+i] = v
			}
			if v := follower[nbEBands+i] - 4; v > follower[i] {
				follower[i] = v
			}
			a := float32(bandLogE[i]) - follower[i]
			if a < 0 {
				a = 0
			}
			b := float32(bandLogE[nbEBands+i]) - follower[nbEBands+i]
			if b < 0 {
				b = 0
			}
			follower[i] = float32(0.5) * float32(a+b)
		}
	} else {
		for i := start; i < end; i++ {
			v := float32(bandLogE[i]) - follower[i]
			if v < 0 {
				v = 0
			}
			follower[i] = v
		}
	}
	if surroundDynalloc != nil {
		for i := start; i < end; i++ {
			if v := float32(surroundDynalloc[i]); v > follower[i] {
				follower[i] = v
			}
		}
	}
	for i := start; i < end; i++ {
		v := follower[i]
		if v > 4 {
			v = 4
		}
		res.importance[i] = int(math.Floor(float64(float32(0.5) + float32(13*celtExp2DB(v)))))
	}
	// For non-transient CBR/CVBR frames, halve the dynalloc contribution.
	if (!vbr || constrainedVBR) && !isTransient {
		for i := start; i < end; i++ {
			follower[i] = float32(0.5) * follower[i]
		}
	}
	for i := start; i < end; i++ {
		if i < 8 {
			follower[i] *= 2
		}
		if i >= 12 {
			follower[i] = float32(0.5) * follower[i]
		}
	}
	// Compensate for Opus' under-allocation on tones.
	if toneishness > 0.98 {
		freqBin := int(math.Floor(0.5 + float64(float32(toneFreq*120))/math.Pi))
		for i := start; i < end; i++ {
			lo, hi := int(EBands48000[i]), int(EBands48000[i+1])
			if freqBin >= lo && freqBin <= hi {
				follower[i] += 2
			}
			if freqBin >= lo-1 && freqBin <= hi+1 {
				follower[i] += 1
			}
			if freqBin >= lo-2 && freqBin <= hi+2 {
				follower[i] += 1
			}
			if freqBin >= lo-3 && freqBin <= hi+3 {
				follower[i] += 0.5
			}
		}
		if freqBin >= int(EBands48000[end]) {
			follower[end-1] += 2
			follower[end-2] += 1
		}
	}
	if analysis != nil && analysis.Valid {
		for i := start; i < analysisLeakBands && i < end; i++ {
			follower[i] = follower[i] + float32(float32(1.0/64)*float32(analysis.LeakBoost[i]))
		}
	}
	totBoost := 0
	for i := start; i < end; i++ {
		if follower[i] > 4 {
			follower[i] = 4
		}
		width := C * (int(EBands48000[i+1]) - int(EBands48000[i])) << uint(lm)
		var boost, boostBits int
		switch {
		case width < 6:
			boost = int(follower[i])
			boostBits = boost * width << 3
		case width > 48:
			boost = int(follower[i] * 8)
			boostBits = (boost * width << 3) / 8
		default:
			boost = int(float32(follower[i]*float32(width)) / 6)
			boostBits = boost * 6 << 3
		}
		// For CBR and non-transient CVBR frames, limit dynalloc to 2/3 of the bits.
		if (!vbr || (constrainedVBR && !isTransient)) && (totBoost+boostBits)>>3>>3 > 2*effectiveBytes/3 {
			capBits := (2 * effectiveBytes / 3) << 3 << 3
			res.offsets[i] = capBits - totBoost
			totBoost = capBits
			break
		}
		res.offsets[i] = boost
		totBoost += boostBits
	}
	res.totBoost = totBoost
	return res
}

func medianOf3F32(x []float32) float32 {
	var t0, t1, t2 float32
	if x[0] > x[1] {
		t0, t1 = x[1], x[0]
	} else {
		t0, t1 = x[0], x[1]
	}
	t2 = x[2]
	if t1 < t2 {
		return t1
	}
	if t0 < t2 {
		return t2
	}
	return t0
}

func medianOf5F32(x []float32) float32 {
	t0, t1, t2, t3, t4 := x[0], x[1], x[2], x[3], x[4]
	if t0 > t1 {
		t0, t1 = t1, t0
	}
	if t3 > t4 {
		t3, t4 = t4, t3
	}
	if t0 > t3 {
		t0, t3 = t3, t0
		t1, t4 = t4, t1
	}
	if t2 > t1 {
		if t1 < t3 {
			if t2 < t3 {
				return t2
			}
			return t3
		}
		if t4 < t1 {
			return t4
		}
		return t1
	}
	if t2 < t3 {
		if t1 < t3 {
			return t1
		}
		return t3
	}
	if t2 < t4 {
		return t2
	}
	return t4
}

// spreadingDecision32 mirrors spreading_decision, including the
// spread_weight weighting from dynalloc_analysis and the high-frequency
// tapset follower.
func spreadingDecision32(X []float64, N0, end, C, M int, average *int, lastDecision int,
	hfAverage, tapsetDecision *int, updateHF bool, spreadWeight []int) int {
	if M*int(EBands48000[end]-EBands48000[end-1]) <= 8 {
		return spreadNone
	}
	sum, nbBands, hfSum := 0, 0, 0
	for c := 0; c < C; c++ {
		for i := 0; i < end; i++ {
			N := M * int(EBands48000[i+1]-EBands48000[i])
			if N <= 8 {
				continue
			}
			x := X[c*N0+M*int(EBands48000[i]):]
			var tcount [3]int
			// Compute rough CDF of |x[j]|.
			for j := 0; j < N; j++ {
				xj := float32(x[j])
				x2N := float32(xj*xj) * float32(N)
				if x2N < 0.25 {
					tcount[0]++
				}
				if x2N < 0.0625 {
					tcount[1]++
				}
				if x2N < 0.015625 {
					tcount[2]++
				}
			}
			// Only include four last bands (8 kHz and up).
			if i > NumBands48000-4 {
				hfSum += 32 * (tcount[1] + tcount[0]) / N
			}
			tmp := 0
			if 2*tcount[2] >= N {
				tmp++
			}
			if 2*tcount[1] >= N {
				tmp++
			}
			if 2*tcount[0] >= N {
				tmp++
			}
			sum += tmp * spreadWeight[i]
			nbBands += spreadWeight[i]
		}
	}
	if updateHF {
		if hfSum != 0 {
			hfSum /= C * (4 - NumBands48000 + end)
		}
		*hfAverage = (*hfAverage + hfSum) >> 1
		hfSum = *hfAverage
		if *tapsetDecision == 2 {
			hfSum += 4
		} else if *tapsetDecision == 0 {
			hfSum -= 4
		}
		switch {
		case hfSum > 22:
			*tapsetDecision = 2
		case hfSum > 18:
			*tapsetDecision = 1
		default:
			*tapsetDecision = 0
		}
	}
	sum = (sum << 8) / nbBands
	// Recursive averaging.
	sum = (sum + *average) >> 1
	*average = sum
	// Hysteresis.
	sum = (3*sum + (((3 - lastDecision) << 7) + 64) + 2) >> 2
	switch {
	case sum < 80:
		return spreadAggressive
	case sum < 256:
		return spreadNormal
	case sum < 384:
		return spreadLight
	default:
		return spreadNone
	}
}

// allocTrimAnalysis32 mirrors alloc_trim_analysis (float build): the trim
// from the equivalent rate, the stereo correlation (updating
// st->stereo_saving), the spectral tilt of the (biased) band log energies,
// the surround trim, the transient estimate and the analysis tonality slope.
func allocTrimAnalysis32(X, bandLogE []float64, nbEBands, end, lm, C, N0 int, analysis *AnalysisInfo, stereoSaving *float32,
	tfEstimate float32, intensity int, surroundTrim float32, equivRate int) int {
	trim := float32(5)
	// At low bitrate, reducing the trim seems to help. At higher bitrates,
	// it's less clear what's best, so we're keeping it as it was before.
	if equivRate < 64000 {
		trim = 4
	} else if equivRate < 80000 {
		frac := (equivRate - 64000) >> 10
		trim = 4 + float32(float32(1.0/16)*float32(frac))
	}
	if C == 2 {
		var sum float32
		// Compute inter-channel correlation for low frequencies.
		for i := 0; i < 8; i++ {
			lo := int(EBands48000[i]) << uint(lm)
			n := (int(EBands48000[i+1]) - int(EBands48000[i])) << uint(lm)
			sum += innerProd64as32(X[lo:], X[N0+lo:], n)
		}
		sum = float32(float32(1.0/8) * sum)
		if sum < 0 {
			sum = -sum
		}
		if sum > 1 {
			sum = 1
		}
		minXC := sum
		for i := 8; i < intensity; i++ {
			lo := int(EBands48000[i]) << uint(lm)
			n := (int(EBands48000[i+1]) - int(EBands48000[i])) << uint(lm)
			p := innerProd64as32(X[lo:], X[N0+lo:], n)
			if p < 0 {
				p = -p
			}
			if p < minXC {
				minXC = p
			}
		}
		if minXC < 0 {
			minXC = -minXC
		}
		if minXC > 1 {
			minXC = 1
		}
		// mid-side savings estimations based on the LF average
		logXC := float32(celtLog2F32(float64(float32(1.001) - float32(sum*sum))))
		// mid-side savings estimations based on min correlation
		logXC2 := float32(celtLog2F32(float64(float32(1.001) - float32(minXC*minXC))))
		if h := float32(float32(0.5) * logXC); h > logXC2 {
			logXC2 = h
		}
		if v := float32(float32(0.75) * logXC); v > -4 {
			trim += v
		} else {
			trim += -4
		}
		if v := *stereoSaving + float32(0.25); v < -(float32(float32(0.5) * logXC2)) {
			*stereoSaving = v
		} else {
			*stereoSaving = -(float32(float32(0.5) * logXC2))
		}
	}
	// Estimate spectral tilt.
	var diff float32
	for c := 0; c < C; c++ {
		for i := 0; i < end-1; i++ {
			diff += float32(float32(bandLogE[i+c*nbEBands]) * float32(2+2*i-end))
		}
	}
	diff /= float32(C * (end - 1))
	tilt := (diff + 1) / 6
	if tilt > 2 {
		tilt = 2
	}
	if tilt < -2 {
		tilt = -2
	}
	trim -= tilt
	trim -= surroundTrim
	trim -= float32(2 * tfEstimate)
	if analysis.Valid {
		v := float32(2 * (analysis.TonalitySlope + float32(0.05)))
		if v > 2 {
			v = 2
		}
		if v < -2 {
			v = -2
		}
		trim -= v
	}
	trimIndex := int(math.Floor(float64(float32(0.5) + trim)))
	if trimIndex < 0 {
		trimIndex = 0
	}
	if trimIndex > 10 {
		trimIndex = 10
	}
	return trimIndex
}

// innerProd64as32 is celt_inner_prod over float32-valued float64 storage.
func innerProd64as32(x, y []float64, n int) float32 {
	var xy float32
	for i := 0; i < n; i++ {
		xy += float32(float32(x[i]) * float32(y[i]))
	}
	return xy
}
