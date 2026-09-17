package celt

import "math"

// Float32-faithful port of the libopus 1.6.1 encoder pitch prefilter
// (celt/celt_encoder.c run_prefilter, celt/pitch.c pitch_downsample,
// pitch_search, remove_doubling, celt/celt_lpc.c _celt_autocorr / _celt_lpc
// and the float comb_filter of celt/celt.c). Every accumulation is
// sequential float32, as in the plain C build of libopus.

// pitchXcorr32 is celt_pitch_xcorr_c: xcorr[i] = Σ_j x[j]*y[i+j] for
// i < maxPitch, each lag accumulated sequentially.
func pitchXcorr32(x, y []float32, xcorr []float32, length, maxPitch int) {
	for i := 0; i < maxPitch; i++ {
		var sum float32
		yi := y[i:]
		for j := 0; j < length; j++ {
			sum += float32(x[j] * yi[j])
		}
		xcorr[i] = sum
	}
}

// innerProd32 is celt_inner_prod_c.
func innerProd32(x, y []float32, n int) float32 {
	var xy float32
	for i := 0; i < n; i++ {
		xy += float32(x[i] * y[i])
	}
	return xy
}

// celtAutocorr32 is _celt_autocorr without a window.
func celtAutocorr32(x []float32, ac []float32, lag, n int) {
	fastN := n - lag
	pitchXcorr32(x, x, ac, fastN, lag+1)
	for k := 0; k <= lag; k++ {
		var d float32
		for i := k + fastN; i < n; i++ {
			d += float32(x[i] * x[i-k])
		}
		ac[k] += d
	}
}

// celtLPC32 is _celt_lpc (float build).
func celtLPC32(lpc []float32, ac []float32, p int) {
	for i := range lpc[:p] {
		lpc[i] = 0
	}
	errv := ac[0]
	if !(ac[0] > 1e-10) {
		return
	}
	for i := 0; i < p; i++ {
		// Sum up this iteration's reflection coefficient.
		var rr float32
		for j := 0; j < i; j++ {
			rr += float32(lpc[j] * ac[i-j])
		}
		rr += ac[i+1]
		r := -(rr / errv)
		// Update LPC coefficients and total error.
		lpc[i] = r
		for j := 0; j < (i+1)>>1; j++ {
			tmp1 := lpc[j]
			tmp2 := lpc[i-1-j]
			lpc[j] = tmp1 + float32(r*tmp2)
			lpc[i-1-j] = tmp2 + float32(r*tmp1)
		}
		errv = errv - float32(float32(r*r)*errv)
		// Bail out once we get 30 dB gain.
		if errv <= float32(0.001)*ac[0] {
			break
		}
	}
}

// celtFIR5_32 is celt_fir5.
func celtFIR5_32(x []float32, num [5]float32, n int) {
	var mem0, mem1, mem2, mem3, mem4 float32
	for i := 0; i < n; i++ {
		sum := x[i]
		sum += float32(num[0] * mem0)
		sum += float32(num[1] * mem1)
		sum += float32(num[2] * mem2)
		sum += float32(num[3] * mem3)
		sum += float32(num[4] * mem4)
		mem4 = mem3
		mem3 = mem2
		mem2 = mem1
		mem1 = mem0
		mem0 = x[i]
		x[i] = sum
	}
}

// pitchDownsample32 is pitch_downsample with factor 2: a half-band
// decimation followed by a 4th-order LPC whitening filter with a zero.
func pitchDownsample32(x [][]float32, xLP []float32, length, C int) {
	const factor = 2
	const offset = factor / 2
	for i := 1; i < length; i++ {
		xLP[i] = float32(float32(0.25)*x[0][factor*i-offset]) + float32(float32(0.25)*x[0][factor*i+offset]) + float32(float32(0.5)*x[0][factor*i])
	}
	xLP[0] = float32(float32(0.25)*x[0][offset]) + float32(float32(0.5)*x[0][0])
	if C == 2 {
		for i := 1; i < length; i++ {
			xLP[i] += float32(float32(0.25)*x[1][factor*i-offset]) + float32(float32(0.25)*x[1][factor*i+offset]) + float32(float32(0.5)*x[1][factor*i])
		}
		xLP[0] += float32(float32(0.25)*x[1][offset]) + float32(float32(0.5)*x[1][0])
	}
	var ac [5]float32
	celtAutocorr32(xLP, ac[:], 4, length)
	// Noise floor -40 dB.
	ac[0] *= 1.0001
	// Lag windowing.
	for i := 1; i <= 4; i++ {
		w := float32(0.008) * float32(i)
		ac[i] -= float32(float32(ac[i]*w) * w)
	}
	var lpc [4]float32
	celtLPC32(lpc[:], ac[:], 4)
	tmp := float32(1)
	for i := 0; i < 4; i++ {
		tmp = float32(0.9) * tmp
		lpc[i] = lpc[i] * tmp
	}
	// Add a zero.
	const c1 = float32(0.8)
	var lpc2 [5]float32
	lpc2[0] = lpc[0] + float32(0.8)
	lpc2[1] = lpc[1] + float32(c1*lpc[0])
	lpc2[2] = lpc[2] + float32(c1*lpc[1])
	lpc2[3] = lpc[3] + float32(c1*lpc[2])
	lpc2[4] = float32(c1 * lpc[3])
	celtFIR5_32(xLP, lpc2, length)
}

// findBestPitch32 is find_best_pitch (float build).
func findBestPitch32(xcorr []float32, y []float32, length, maxPitch int, bestPitch *[2]int) {
	Syy := float32(1)
	bestNum := [2]float32{-1, -1}
	bestDen := [2]float32{0, 0}
	bestPitch[0] = 0
	bestPitch[1] = 1
	for j := 0; j < length; j++ {
		Syy += float32(y[j] * y[j])
	}
	for i := 0; i < maxPitch; i++ {
		if xcorr[i] > 0 {
			// Considering the range of xcorr16, this should avoid both
			// underflows and overflows (inf) when squaring xcorr16.
			xcorr16 := xcorr[i] * float32(1e-12)
			num := float32(xcorr16 * xcorr16)
			if float32(num*bestDen[1]) > float32(bestNum[1]*Syy) {
				if float32(num*bestDen[0]) > float32(bestNum[0]*Syy) {
					bestNum[1] = bestNum[0]
					bestDen[1] = bestDen[0]
					bestPitch[1] = bestPitch[0]
					bestNum[0] = num
					bestDen[0] = Syy
					bestPitch[0] = i
				} else {
					bestNum[1] = num
					bestDen[1] = Syy
					bestPitch[1] = i
				}
			}
		}
		Syy += float32(y[i+length]*y[i+length]) - float32(y[i]*y[i])
		if Syy < 1 {
			Syy = 1
		}
	}
}

// pitchSearch32 is pitch_search: a coarse search at 4x decimation, a fine
// search at 2x around the two best candidates and a pseudo-interpolation.
func pitchSearch32(xLP []float32, y []float32, length, maxPitch int) int {
	lag := length + maxPitch
	xLP4 := make([]float32, length>>2)
	yLP4 := make([]float32, lag>>2)
	xcorr := make([]float32, maxPitch>>1)
	// Downsample by 2 again.
	for j := 0; j < length>>2; j++ {
		xLP4[j] = xLP[2*j]
	}
	for j := 0; j < lag>>2; j++ {
		yLP4[j] = y[2*j]
	}
	var bestPitch [2]int
	// Coarse search with 4x decimation.
	pitchXcorr32(xLP4, yLP4, xcorr, length>>2, maxPitch>>2)
	findBestPitch32(xcorr, yLP4, length>>2, maxPitch>>2, &bestPitch)
	// Finer search with 2x decimation.
	for i := 0; i < maxPitch>>1; i++ {
		xcorr[i] = 0
		if abs(i-2*bestPitch[0]) > 2 && abs(i-2*bestPitch[1]) > 2 {
			continue
		}
		sum := innerProd32(xLP, y[i:], length>>1)
		if sum < -1 {
			sum = -1
		}
		xcorr[i] = sum
	}
	findBestPitch32(xcorr, y, length>>1, maxPitch>>1, &bestPitch)
	// Refine by pseudo-interpolation.
	offset := 0
	if bestPitch[0] > 0 && bestPitch[0] < (maxPitch>>1)-1 {
		a := xcorr[bestPitch[0]-1]
		b := xcorr[bestPitch[0]]
		c := xcorr[bestPitch[0]+1]
		if (c - a) > float32(0.7)*(b-a) {
			offset = 1
		} else if (a - c) > float32(0.7)*(b-c) {
			offset = -1
		}
	}
	return 2*bestPitch[0] - offset
}

// dualInnerProd32 is dual_inner_prod_c.
func dualInnerProd32(x, y01, y02 []float32, n int) (xy1, xy2 float32) {
	for i := 0; i < n; i++ {
		xy1 += float32(x[i] * y01[i])
		xy2 += float32(x[i] * y02[i])
	}
	return xy1, xy2
}

// computePitchGain32 is compute_pitch_gain (float build).
func computePitchGain32(xy, xx, yy float32) float32 {
	return xy / float32(math.Sqrt(float64(float32(1)+float32(xx*yy))))
}

var secondCheck = [16]int{0, 0, 3, 2, 3, 2, 5, 2, 3, 2, 3, 2, 5, 2, 3, 2}

// removeDoubling32 is remove_doubling. x is the whole downsampled pitch
// buffer (maxPeriod/2 samples of history followed by the frame), and T0 the
// candidate period at full rate; it returns the prefilter gain and the
// refined period.
func removeDoubling32(x []float32, maxPeriod, minPeriod, N, T0 int, prevPeriod int, prevGain float32) (float32, int) {
	minPeriod0 := minPeriod
	maxPeriod /= 2
	minPeriod /= 2
	T0 /= 2
	prevPeriod /= 2
	N /= 2
	xs := x[maxPeriod:] // xs[-i] is x[maxPeriod-i]
	at := func(i int) float32 { return x[maxPeriod+i] }
	if T0 >= maxPeriod {
		T0 = maxPeriod - 1
	}
	T := T0
	yyLookup := make([]float32, maxPeriod+1)
	xx, xy := dualInnerProd32(xs, xs, x[maxPeriod-T0:], N)
	yyLookup[0] = xx
	yy := xx
	for i := 1; i <= maxPeriod; i++ {
		yy = yy + float32(at(-i)*at(-i)) - float32(at(N-i)*at(N-i))
		if yy < 0 {
			yyLookup[i] = 0
		} else {
			yyLookup[i] = yy
		}
	}
	yy = yyLookup[T0]
	bestXY := xy
	bestYY := yy
	g0 := computePitchGain32(xy, xx, yy)
	g := g0
	// Look for any pitch at T/k.
	for k := 2; k <= 15; k++ {
		T1 := (2*T0 + k) / (2 * k)
		if T1 < minPeriod {
			break
		}
		// Look for another strong correlation at T1b.
		var T1b int
		if k == 2 {
			if T1+T0 > maxPeriod {
				T1b = T0
			} else {
				T1b = T0 + T1
			}
		} else {
			T1b = (2*secondCheck[k]*T0 + k) / (2 * k)
		}
		xy1, xy2 := dualInnerProd32(xs, x[maxPeriod-T1:], x[maxPeriod-T1b:], N)
		xy = float32(0.5) * (xy1 + xy2)
		yy = float32(0.5) * (yyLookup[T1] + yyLookup[T1b])
		g1 := computePitchGain32(xy, xx, yy)
		var cont float32
		if abs(T1-prevPeriod) <= 1 {
			cont = prevGain
		} else if abs(T1-prevPeriod) <= 2 && 5*k*k < T0 {
			cont = float32(0.5) * prevGain
		}
		thresh := float32(0.7)*g0 - cont
		if thresh < 0.3 {
			thresh = 0.3
		}
		// Bias against very high pitch (very short period) to avoid
		// false-positives due to short-term correlation.
		if T1 < 3*minPeriod {
			thresh = float32(0.85)*g0 - cont
			if thresh < 0.4 {
				thresh = 0.4
			}
		} else if T1 < 2*minPeriod {
			thresh = float32(0.9)*g0 - cont
			if thresh < 0.5 {
				thresh = 0.5
			}
		}
		if g1 > thresh {
			bestXY = xy
			bestYY = yy
			T = T1
			g = g1
		}
	}
	if bestXY < 0 {
		bestXY = 0
	}
	var pg float32
	if bestYY <= bestXY {
		pg = 1
	} else {
		pg = bestXY / (bestYY + 1)
	}
	var xcorr [3]float32
	for k := 0; k < 3; k++ {
		xcorr[k] = innerProd32(xs, x[maxPeriod-(T+k-1):], N)
	}
	offset := 0
	if (xcorr[2] - xcorr[0]) > float32(0.7)*(xcorr[1]-xcorr[0]) {
		offset = 1
	} else if (xcorr[0] - xcorr[2]) > float32(0.7)*(xcorr[1]-xcorr[2]) {
		offset = -1
	}
	if pg > g {
		pg = g
	}
	T0 = 2*T + offset
	if T0 < minPeriod0 {
		T0 = minPeriod0
	}
	return pg, T0
}

// combFilter32 is the float comb_filter: y[i] = x[i] filtered with the
// crossfade from (T0, g0, tapset0) to (T1, g1, tapset1) over the window.
// x[xi+i] is the current sample and x[xi+i-T] its history.
func combFilter32(y []float32, x []float32, xi, T0, T1, N int, g0, g1 float32, tapset0, tapset1 int, window []float32, overlap int) {
	if g0 == 0 && g1 == 0 {
		copy(y[:N], x[xi:xi+N])
		return
	}
	// When the gain is zero, T0 and/or T1 is set to zero. We need to have
	// then be at least 2 to avoid processing garbage data.
	if T0 < combFilterMinPeriod {
		T0 = combFilterMinPeriod
	}
	if T1 < combFilterMinPeriod {
		T1 = combFilterMinPeriod
	}
	g00 := g0 * pfCombGains32[tapset0][0]
	g01 := g0 * pfCombGains32[tapset0][1]
	g02 := g0 * pfCombGains32[tapset0][2]
	g10 := g1 * pfCombGains32[tapset1][0]
	g11 := g1 * pfCombGains32[tapset1][1]
	g12 := g1 * pfCombGains32[tapset1][2]
	x1 := x[xi-T1+1]
	x2 := x[xi-T1]
	x3 := x[xi-T1-1]
	x4 := x[xi-T1-2]
	// If the filter didn't change, we don't need the overlap.
	if g0 == g1 && T0 == T1 && tapset0 == tapset1 {
		overlap = 0
	}
	i := 0
	for ; i < overlap; i++ {
		x0 := x[xi+i-T1+2]
		f := window[i] * window[i]
		v := x[xi+i]
		v += float32(float32((1-f)*g00) * x[xi+i-T0])
		v += float32(float32((1-f)*g01) * (x[xi+i-T0+1] + x[xi+i-T0-1]))
		v += float32(float32((1-f)*g02) * (x[xi+i-T0+2] + x[xi+i-T0-2]))
		v += float32(float32(f*g10) * x2)
		v += float32(float32(f*g11) * (x1 + x3))
		v += float32(float32(f*g12) * (x0 + x4))
		y[i] = v
		x4 = x3
		x3 = x2
		x2 = x1
		x1 = x0
	}
	if g1 == 0 {
		copy(y[overlap:N], x[xi+overlap:xi+N])
		return
	}
	// Compute the part with the constant filter (comb_filter_const_c).
	x4 = x[xi+i-T1-2]
	x3 = x[xi+i-T1-1]
	x2 = x[xi+i-T1]
	x1 = x[xi+i-T1+1]
	for ; i < N; i++ {
		x0 := x[xi+i-T1+2]
		v := x[xi+i] + float32(g10*x2)
		v += float32(g11 * (x1 + x3))
		v += float32(g12 * (x0 + x4))
		y[i] = v
		x4 = x3
		x3 = x2
		x2 = x1
		x1 = x0
	}
}

var pfCombGains32 = [3][3]float32{
	{0.3066406250, 0.2170410156, 0.1296386719},
	{0.4638671875, 0.2680664062, 0},
	{0.7998046875, 0.1000976562, 0},
}

// prefilterResult carries the run_prefilter outputs for the frame.
type prefilterResult struct {
	pfOn       bool
	pitchIndex int
	gain       float32
	qg         int
	tapset     int
}

// runPrefilter mirrors run_prefilter. in holds the per-channel analysis
// buffers (overlap + N pre-emphasised samples, float64 storage of float32
// values); on return the frame part is comb-filtered and the overlap part
// replaced by the previous frame's filtered tail (st->in_mem). The
// prefilter history, in_mem and the period/gain/tapset state live on the
// encoder.
func (e *Encoder) runPrefilter(in [][]float64, N, overlap int, enabled bool, tfEstimate float32,
	nbAvailableBytes int, toneFreq, toneishness float32, window []float32) prefilterResult {
	CC := len(in)
	maxPeriod := combFilterMaxPeriod
	minPeriod := combFilterMinPeriod
	if len(e.prefilterPre) != CC || len(e.prefilterPre[0]) < N+maxPeriod {
		e.prefilterPre = make([][]float32, CC)
		for c := range e.prefilterPre {
			e.prefilterPre[c] = make([]float32, N+maxPeriod)
		}
		e.prefilterY = make([]float32, N)
	}
	pre := e.prefilterPre
	for c := 0; c < CC; c++ {
		copy(pre[c], e.prefilterMem[c])
		for i := 0; i < N; i++ {
			pre[c][maxPeriod+i] = float32(in[c][overlap+i])
		}
	}
	var gain1 float32
	pitchIndex := minPeriod
	switch {
	case enabled && toneishness > 0.99:
		// If we detect that the signal is dominated by a single tone, don't
		// rely on the standard pitch estimator, as it can become unreliable.
		multiple := 1
		// Using aliased version of the postfilter above 24 kHz. First value
		// is purposely slightly above pi to avoid triggering for Fs=48kHz.
		if toneFreq >= 3.1416 {
			toneFreq = float32(3.141593) - toneFreq
		}
		// If the pitch is too high for our post-filter, apply pitch doubling
		// until we can get something that fits.
		for toneFreq >= float32(multiple)*float32(0.39) {
			multiple++
		}
		if toneFreq > 0.006148 {
			pitchIndex = int(math.Floor(0.5 + 2.0*math.Pi*float64(multiple)/float64(toneFreq)))
			if pitchIndex > maxPeriod-2 {
				pitchIndex = maxPeriod - 2
			}
		} else {
			// If the pitch is too low, using a very high pitch will actually
			// give us an improvement due to the DC component of the filter
			// that will be close to our tone.
			pitchIndex = minPeriod
		}
		gain1 = 0.75
	case enabled && e.complexity >= 5:
		pitchBuf := make([]float32, (maxPeriod+N)>>1)
		pitchDownsample32(pre, pitchBuf, (maxPeriod+N)>>1, CC)
		// Don't search for the fir last 1.5 octave of the range because
		// there's too many false-positives due to short-term correlation.
		pitchIndex = pitchSearch32(pitchBuf[maxPeriod>>1:], pitchBuf, N, maxPeriod-3*minPeriod)
		pitchIndex = maxPeriod - pitchIndex
		gain1, pitchIndex = removeDoubling32(pitchBuf, maxPeriod, minPeriod, N, pitchIndex, e.prefilterPeriod, e.prefilterGain)
		if pitchIndex > maxPeriod-2 {
			pitchIndex = maxPeriod - 2
		}
		gain1 = float32(0.7) * gain1
		if e.lossRate > 2 {
			gain1 = float32(0.5) * gain1
		}
		if e.lossRate > 4 {
			gain1 = float32(0.5) * gain1
		}
		if e.lossRate > 8 {
			gain1 = 0
		}
	}
	// Gain threshold for enabling the prefilter/postfilter.
	pfThreshold := float32(0.2)
	// Adjusting the threshold based on rate and continuity.
	if abs(pitchIndex-e.prefilterPeriod)*10 > pitchIndex {
		pfThreshold += 0.2
		// Completely disable the prefilter on strong transients without
		// continuity.
		if tfEstimate > 0.98 {
			gain1 = 0
		}
	}
	if nbAvailableBytes < 25 {
		pfThreshold += 0.1
	}
	if nbAvailableBytes < 35 {
		pfThreshold += 0.1
	}
	if e.prefilterGain > 0.4 {
		pfThreshold -= 0.1
	}
	if e.prefilterGain > 0.55 {
		pfThreshold -= 0.1
	}
	// Hard threshold at 0.2.
	if pfThreshold < 0.2 {
		pfThreshold = 0.2
	}
	res := prefilterResult{tapset: e.tapsetDecision}
	if gain1 < pfThreshold {
		gain1 = 0
	} else {
		if d := gain1 - e.prefilterGain; d < 0.1 && d > -0.1 {
			gain1 = e.prefilterGain
		}
		qg := int(math.Floor(float64(float32(0.5)+float32(gain1*32)/3))) - 1
		if qg < 0 {
			qg = 0
		}
		if qg > 7 {
			qg = 7
		}
		gain1 = float32(0.09375) * float32(qg+1)
		res.pfOn = true
		res.qg = qg
	}
	// Apply the filter with a crossfade from the previous parameters, and
	// measure whether it helped.
	offset := e.mode.NBase - overlap
	if e.prefilterPeriod < minPeriod {
		e.prefilterPeriod = minPeriod
	}
	y := e.prefilterY[:N]
	var before, after [2]float32
	cancelPitch := false
	for c := 0; c < CC; c++ {
		for i := 0; i < N; i++ {
			v := float32(in[c][overlap+i])
			if v < 0 {
				v = -v
			}
			before[c] += v
		}
		if offset > 0 {
			combFilter32(y, pre[c], maxPeriod, e.prefilterPeriod, e.prefilterPeriod, offset,
				-e.prefilterGain, -e.prefilterGain, e.prefilterTapset, e.prefilterTapset, nil, 0)
		}
		combFilter32(y[offset:], pre[c], maxPeriod+offset, e.prefilterPeriod, pitchIndex, N-offset,
			-e.prefilterGain, -gain1, e.prefilterTapset, res.tapset, window, overlap)
		for i := 0; i < N; i++ {
			v := y[i]
			if v < 0 {
				v = -v
			}
			after[c] += v
			in[c][overlap+i] = float64(y[i])
		}
	}
	if CC == 2 {
		var thresh [2]float32
		thresh[0] = float32(float32(float32(0.25)*gain1)*before[0]) + float32(float32(0.01)*before[1])
		thresh[1] = float32(float32(float32(0.25)*gain1)*before[1]) + float32(float32(0.01)*before[0])
		// Don't use the filter if one channel gets significantly worse.
		if after[0]-before[0] > thresh[0] || after[1]-before[1] > thresh[1] {
			cancelPitch = true
		}
		// Use the filter only if at least one channel gets significantly better.
		if before[0]-after[0] < thresh[0] && before[1]-after[1] < thresh[1] {
			cancelPitch = true
		}
	} else if after[0] > before[0] {
		// Check that the mono channel actually got better.
		cancelPitch = true
	}
	// If needed, revert to a gain of zero.
	if cancelPitch {
		for c := 0; c < CC; c++ {
			for i := 0; i < N; i++ {
				in[c][overlap+i] = float64(pre[c][maxPeriod+i])
			}
			combFilter32(y[offset:], pre[c], maxPeriod+offset, e.prefilterPeriod, pitchIndex, overlap,
				-e.prefilterGain, 0, e.prefilterTapset, res.tapset, window, overlap)
			for i := 0; i < overlap; i++ {
				in[c][overlap+offset+i] = float64(y[offset+i])
			}
		}
		gain1 = 0
		res.pfOn = false
		res.qg = 0
	}
	for c := 0; c < CC; c++ {
		// The MDCT overlap comes from the previous filtered frame (st->in_mem,
		// kept in e.overlap); the new tail is the filtered frame's last
		// overlap samples.
		for i := 0; i < overlap; i++ {
			in[c][i] = e.overlap[c][i]
		}
		for i := 0; i < overlap; i++ {
			e.overlap[c][i] = in[c][N+i]
		}
		if N > maxPeriod {
			copy(e.prefilterMem[c], pre[c][N:N+maxPeriod])
		} else {
			copy(e.prefilterMem[c], e.prefilterMem[c][N:])
			copy(e.prefilterMem[c][maxPeriod-N:], pre[c][maxPeriod:maxPeriod+N])
		}
	}
	res.gain = gain1
	res.pitchIndex = pitchIndex
	return res
}
