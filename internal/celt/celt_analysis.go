package celt

import "math"

// This file holds the encoder-side analysis/decision functions that feed the
// already-correct symbol writers in celt_encode.go (tfEncode, dynallocEncode)
// and the spread/alloc_trim ICDF coders in encoder.go. They are float ports of
// libopus celt/celt_encoder.c (spreading_decision, dynalloc_analysis,
// alloc_trim_analysis), simplified where bit-exactness is not required: the
// external tonality analyzer, surround/LFE handling, and the spread_weight
// coupling are simplified (spread_weight is treated as uniform). The decoder reads
// whatever values these produce, so any in-range result round-trips correctly;
// these heuristics only shape quality (bit distribution).

// Spread decision symbols (libopus SPREAD_*); also the ICDF index written.
// spreadNone (0) and spreadAggressive (3) are declared in quant_pvq.go.
const (
	spreadLight  = 1
	spreadNormal = 2
)

// celtLSBDepth is the assumed input bit depth used by the dynalloc noise floor.
// libopus defaults to 24 for the float API; this only shifts the noise floor by
// a band-independent constant, so it rarely affects the chosen boosts.
const celtLSBDepth = 24

// transientInvTable is libopus' inv_table (6*64/x, trained on real data) used by
// transient_analysis to accumulate a harmonic-mean energy estimate.
var transientInvTable = [128]int{
	255, 255, 156, 110, 86, 70, 59, 51, 45, 40, 37, 33, 31, 28, 26, 25,
	23, 22, 21, 20, 19, 18, 17, 16, 16, 15, 15, 14, 13, 13, 12, 12,
	12, 12, 11, 11, 11, 10, 10, 10, 9, 9, 9, 9, 9, 9, 8, 8,
	8, 8, 8, 7, 7, 7, 7, 7, 7, 6, 6, 6, 6, 6, 6, 6,
	6, 6, 6, 6, 6, 6, 6, 6, 6, 5, 5, 5, 5, 5, 5, 5,
	5, 5, 5, 5, 5, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4,
	4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 3, 3,
	3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 2,
}

// transientAnalysis is the float port of libopus celt_encoder.c transient_analysis
// (the !FIXED_POINT branch). bufs[c] is the per-channel analysis buffer of length
// `length` (= frameSize+overlap), holding the pre-emphasised signal (overlap from
// the previous frame followed by the current frame), identical in layout to
// libopus' `in`. It returns whether the frame should use short MDCT blocks.
//
// The metric is a bitrate-normalised temporal noise-to-mask ratio: a high-pass
// filter isolates fast energy changes, forward/backward leaky integrators build
// pre/post-echo masking thresholds, and the harmonic mean of the resulting
// envelope (via transientInvTable) is compared against the frame energy. A
// "spiky" envelope (an attack surrounded by quiet) yields a large mask_metric.
// The absolute signal scale cancels in `norm`, so the ×32768 domain used here
// matches libopus' threshold. The weak-transient/tone-detection refinements
// (only used for low-bitrate hybrid) are omitted.
//
// It returns (isTransient, tfChan, tfEstimate): tfChan is the channel with the
// strongest transient (the one tf_analysis later analyses), and tfEstimate is
// the libopus VBR/tf-bias metric derived from the masking strength (used as the
// l1_metric bias in tfAnalysis).
func transientAnalysis(bufs [][]float64, length, C int) (bool, int, float64) {
	const forwardDecay = 0.0625 // 6.7 dB/ms forward masking (CELT-only path)
	const epsilon = 1e-15
	len2 := length / 2
	maskMetric := 0
	tfChan := 0
	tmp := make([]float64, length)

	for c := 0; c < C; c++ {
		in := bufs[c]
		// High-pass filter: (1 - 2*z^-1 + z^-2) / (1 - z^-1 + .5*z^-2),
		// expressed with the shortened dependency chain libopus uses in float.
		var mem0, mem1 float64
		for i := 0; i < length; i++ {
			x := in[i]
			y := mem0 + x
			mem00 := mem0
			mem0 = mem0 - x + 0.5*mem1
			mem1 = x - mem00
			tmp[i] = y
		}
		// First few samples are unreliable (memory not propagated).
		for i := 0; i < 12 && i < length; i++ {
			tmp[i] = 0
		}

		// Forward pass: group by two and build the post-echo threshold.
		var mean float64
		mem0 = 0
		for i := 0; i < len2; i++ {
			x2 := tmp[2*i]*tmp[2*i] + tmp[2*i+1]*tmp[2*i+1]
			mean += x2
			mem0 = x2 + (1.0-forwardDecay)*mem0
			tmp[i] = forwardDecay * mem0
		}

		// Backward pass: pre-echo threshold (13.9 dB/ms) and envelope max.
		mem0 = 0
		var maxE float64
		for i := len2 - 1; i >= 0; i-- {
			mem0 = tmp[i] + 0.875*mem0
			tmp[i] = 0.125 * mem0
			if tmp[i] > maxE {
				maxE = tmp[i]
			}
		}

		// Frame energy: geometric mean of the energy and half the max.
		mean = math.Sqrt(mean * maxE * 0.5 * float64(len2))
		norm := float64(len2) / (epsilon + mean)

		// Harmonic mean of the envelope, sampling every 4th point.
		unmask := 0
		for i := 12; i < len2-5; i += 4 {
			id := int(math.Floor(64.0 * norm * (tmp[i] + epsilon)))
			if id < 0 {
				id = 0
			}
			if id > 127 {
				id = 127
			}
			unmask += transientInvTable[id]
		}
		// Normalise (1/4 sampling, factor of 6 in the table).
		unmask = 64 * unmask * 4 / (6 * (len2 - 17))
		if unmask > maskMetric {
			maskMetric = unmask
			tfChan = c
		}
	}
	isTransient := maskMetric > 200

	// tf_estimate: an arbitrary metric of transient strength, used to bias the
	// l1_metric in tf_analysis toward good frequency resolution when the frame is
	// not strongly transient. Float port of libopus transient_analysis tail.
	tfMax := math.Sqrt(27.0*float64(maskMetric)) - 42.0
	if tfMax < 0 {
		tfMax = 0
	}
	if tfMax > 163 {
		tfMax = 163
	}
	v := 0.0069*tfMax - 0.139
	if v < 0 {
		v = 0
	}
	tfEstimate := math.Sqrt(v)
	return isTransient, tfChan, tfEstimate
}

// patchTransientDecision is the float port of libopus celt_encoder.c
// patch_transient_decision. It is the fallback transient detector run after the
// MDCT: when the time-domain transientAnalysis did not flag the frame but the
// band energies show a sharp rise over the previous frame (an onset), the frame
// should be re-coded with short blocks to limit pre-echo.
//
// newE is the current frame's per-band log2-amplitude band energies (bandLogE)
// and oldE the previous frame's (oldBandE); both are channel-major with the given
// per-channel stride. An aggressive -6 dB/band ("octave") spreading function is
// applied to oldE first so that energy in unrelated neighbouring bands does not
// mask a genuine localised onset. The mean positive increase of newE over the
// spread old energy across [max(2,start), end-1) is compared against threshold
// (libopus uses 1 dB; the caller may lower it for voice-leaning content, which
// benefits from eager short-block switching on plosives). start/end are the
// coded band range and C the channel count.
func patchTransientDecision(newE, oldE []float64, stride, start, end, C int, threshold float64) bool {
	from := start
	if from < 2 {
		from = 2
	}
	if end-1 <= from {
		return false
	}
	spreadOld := make([]float64, end)
	if C == 1 {
		spreadOld[start] = oldE[start]
		for i := start + 1; i < end; i++ {
			spreadOld[i] = math.Max(spreadOld[i-1]-1.0, oldE[i])
		}
	} else {
		spreadOld[start] = math.Max(oldE[start], oldE[stride+start])
		for i := start + 1; i < end; i++ {
			spreadOld[i] = math.Max(spreadOld[i-1]-1.0,
				math.Max(oldE[i], oldE[stride+i]))
		}
	}
	for i := end - 2; i >= start; i-- {
		spreadOld[i] = math.Max(spreadOld[i], spreadOld[i+1]-1.0)
	}
	var meanDiff float64
	for c := 0; c < C; c++ {
		for i := from; i < end-1; i++ {
			x1 := math.Max(0, newE[i+c*stride])
			x2 := math.Max(0, spreadOld[i])
			meanDiff += math.Max(0, x1-x2)
		}
	}
	meanDiff /= float64(C * (end - 1 - from))
	return meanDiff > threshold
}

// stereoAnalysis is the float port of libopus bands.c stereo_analysis. It chooses
// between dual stereo (independent L/R coding) and joint mid/side coding by
// comparing an L1-norm entropy proxy of the L/R representation against the M/S
// representation over the low bands (0..12). X is the normalised spectrum with a
// per-channel stride of frameLen, so the right channel begins at X[frameLen]. It
// returns true when coding the channels independently (dual stereo) is expected
// to be cheaper than mid/side.
func stereoAnalysis(X []float64, frameLen, lm int) bool {
	const epsilon = 1e-15
	sumLR := epsilon
	sumMS := epsilon
	for i := 0; i < 13; i++ {
		for j := int(EBands48000[i]) << uint(lm); j < int(EBands48000[i+1])<<uint(lm); j++ {
			l := X[j]
			r := X[frameLen+j]
			m := l + r
			s := l - r
			sumLR += math.Abs(l) + math.Abs(r)
			sumMS += math.Abs(m) + math.Abs(s)
		}
	}
	sumMS *= 0.707107 // 1/sqrt(2): M/S are scaled by this in the codec
	thetas := 13
	// We don't need thetas for the lower bands with LM<=1.
	if lm <= 1 {
		thetas -= 8
	}
	w := int(EBands48000[13]) << uint(lm+1)
	return float64(w+thetas)*sumMS > float64(w)*sumLR
}

// intensityThresholds and intensityHysteresis drive the intensity-stereo band
// decision (libopus celt_encoder.c). The lookup value is the equivalent bitrate
// in kbps; the result is the first band index that uses intensity (single-channel)
// coding. The hysteresis biases the decision toward the previous value to avoid
// chattering between frames.
var intensityThresholds = [21]int{
	/* 0  1  2  3   4   5   6   7   8   9  10  11  12  13  14  15  16  17  18   19   20 */
	1, 2, 3, 4, 5, 6, 7, 8, 16, 24, 36, 44, 50, 56, 62, 67, 72, 79, 88, 106, 134,
}
var intensityHysteresis = [21]int{
	1, 1, 1, 1, 1, 1, 1, 1, 1, 2, 2, 2, 3, 3, 4, 5, 6, 8, 8, 8, 8,
}

// hysteresisDecision is the float port of libopus hysteresis_decision: it maps
// val to an index over thresholds[], biased toward prev so the choice does not
// flip on small fluctuations near a boundary.
func hysteresisDecision(val int, thresholds, hysteresis []int, n, prev int) int {
	i := 0
	for ; i < n; i++ {
		if val < thresholds[i] {
			break
		}
	}
	if i > prev && val < thresholds[prev]+hysteresis[prev] {
		i = prev
	}
	if i < prev && prev > 0 && val > thresholds[prev-1]-hysteresis[prev-1] {
		i = prev
	}
	if i > n-1 {
		i = n - 1
	}
	if i < 0 {
		i = 0
	}
	return i
}

// medianOf3 returns the median of x[0..2] (libopus median_of_3).
func medianOf3(x []float64) float64 {
	var t0, t1 float64
	if x[0] > x[1] {
		t0, t1 = x[1], x[0]
	} else {
		t0, t1 = x[0], x[1]
	}
	t2 := x[2]
	if t1 < t2 {
		return t1
	} else if t0 < t2 {
		return t2
	}
	return t0
}

// medianOf5 returns the median of x[0..4] (libopus median_of_5).
func medianOf5(x []float64) float64 {
	t2 := x[2]
	var t0, t1, t3, t4 float64
	if x[0] > x[1] {
		t0, t1 = x[1], x[0]
	} else {
		t0, t1 = x[0], x[1]
	}
	if x[3] > x[4] {
		t3, t4 = x[4], x[3]
	} else {
		t3, t4 = x[3], x[4]
	}
	if t0 > t3 {
		t0, t3 = t3, t0
		t1, t4 = t4, t1
	}
	if t2 > t1 {
		if t1 < t3 {
			return math.Min(t2, t3)
		}
		return math.Min(t4, t1)
	}
	if t2 < t3 {
		return math.Min(t1, t3)
	}
	return math.Min(t2, t4)
}

// innerProdF returns the dot product of the first n elements of a and b.
func innerProdF(a, b []float64, n int) float64 {
	var s float64
	for j := 0; j < n; j++ {
		s += a[j] * b[j]
	}
	return s
}

// spreadingDecision is the float port of libopus spreading_decision (classic
// uniform-weight variant). X is the normalised spectrum laid out per channel
// ([c*frameLen + M*eBands[i] + j]); average is the recursively-averaged tonality
// state (initialise to 256) and lastDecision is the previous frame's spread.
// It returns one of the spread* symbols.
func spreadingDecision(X []float64, frameLen, end, C, M int, average *int, lastDecision int) int {
	if M*int(EBands48000[end]-EBands48000[end-1]) <= 8 {
		return spreadNone
	}
	sum := 0
	nbBands := 0
	for c := 0; c < C; c++ {
		for i := 0; i < end; i++ {
			N := M * int(EBands48000[i+1]-EBands48000[i])
			if N <= 8 {
				continue
			}
			x := X[c*frameLen+M*int(EBands48000[i]):]
			var tcount [3]int
			for j := 0; j < N; j++ {
				x2N := x[j] * x[j] * float64(N)
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
			sum += tmp
			nbBands++
		}
	}
	if nbBands == 0 {
		return lastDecision
	}
	sum = (sum << 8) / nbBands
	// Recursive averaging.
	sum = (sum + *average) >> 1
	*average = sum
	// Hysteresis toward the previous decision.
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

// dynallocAnalysis is the float port of libopus dynalloc_analysis. It returns
// per-band boost counts (offsets[]) in the units consumed by dynallocEncode
// (the libopus "boost" integer). logE and logE2 are mean-subtracted
// log2-amplitude band energies laid out channel-major [c*numBands+i]: logE is
// bandLogE (from the actual, possibly short-block, MDCT) and logE2 is bandLogE2,
// a long-block estimate that on transient frames has better frequency resolution
// (on non-transient frames the caller passes logE2==logE). The per-band masking
// follower is built from logE2, while the final masking depth (how far each band
// sticks out above the follower) is measured against logE — exactly as libopus
// uses bandLogE3(=bandLogE2) for the follower and bandLogE for the depth. A
// median filter over logE2 raises the follower toward the local median (less a
// 1 dB offset) so a single loud bin in an otherwise quiet band does not trigger
// dynalloc. The internal 2/3-budget break is dropped: dynallocEncode clamps the coded boost
// against the real range-coder budget and the per-band cap, keeping the result
// symmetric with the decoder.
// It also returns importance[]: a per-band weight (libopus, ~13*2^maskingDepth)
// consumed by tf_analysis to weigh how much each band's time-frequency decision
// matters. Bands carrying more energy above the masking follower are weighted
// more heavily; bands at or below the floor get the default weight of 13.
func dynallocAnalysis(logE, logE2 []float64, numBands, end, C, lm int, isTransient, vbr, constrainedVbr bool) ([]int, []int) {
	offsets := make([]int, numBands)
	importance := make([]int, numBands)
	for i := range importance {
		importance[i] = 13
	}
	follower := make([]float64, C*numBands)
	noiseFloor := make([]float64, numBands)
	for i := 0; i < end; i++ {
		noiseFloor[i] = 0.0625*float64(LogN400[i]) + 0.5 +
			float64(9-celtLSBDepth) - EMean(i) +
			0.0062*float64((i+5)*(i+5))
	}
	for c := 0; c < C; c++ {
		f := follower[c*numBands : c*numBands+numBands]
		last := 0
		f[0] = logE2[c*numBands]
		for i := 1; i < end; i++ {
			if logE2[c*numBands+i] > logE2[c*numBands+i-1]+0.5 {
				last = i
			}
			f[i] = math.Min(f[i-1]+1.5, logE2[c*numBands+i])
		}
		for i := last - 1; i >= 0; i-- {
			f[i] = math.Min(f[i], math.Min(f[i+1]+2.0, logE2[c*numBands+i]))
		}
		// Combine with a median filter to avoid dynalloc triggering when a
		// single bin within a band is loud but the band overall is not. The
		// follower is raised toward the local median of logE2 (less a 1 dB
		// offset that keeps the filter conservative). libopus dynalloc_analysis.
		const medianOffset = 1.0
		le := logE2[c*numBands : c*numBands+numBands]
		if end >= 3 {
			for i := 2; i < end-2; i++ {
				f[i] = math.Max(f[i], medianOf5(le[i-2:i+3])-medianOffset)
			}
			tmp := medianOf3(le[0:3]) - medianOffset
			f[0] = math.Max(f[0], tmp)
			f[1] = math.Max(f[1], tmp)
			tmp = medianOf3(le[end-3:end]) - medianOffset
			f[end-2] = math.Max(f[end-2], tmp)
			f[end-1] = math.Max(f[end-1], tmp)
		}
		for i := 0; i < end; i++ {
			f[i] = math.Max(f[i], noiseFloor[i])
		}
	}
	if C == 2 {
		for i := 0; i < end; i++ {
			follower[numBands+i] = math.Max(follower[numBands+i], follower[i]-4.0)
			follower[i] = math.Max(follower[i], follower[numBands+i]-4.0)
			follower[i] = 0.5 * (math.Max(0, logE[i]-follower[i]) +
				math.Max(0, logE[numBands+i]-follower[numBands+i]))
		}
	} else {
		for i := 0; i < end; i++ {
			follower[i] = math.Max(0, logE[i]-follower[i])
		}
	}
	// Per-band importance from the masking depth, BEFORE the halving/scaling that
	// follows (libopus computes importance at exactly this point). 2^depth grows
	// the weight for bands that stick out above the masking follower.
	for i := 0; i < end; i++ {
		d := follower[i]
		if d > 4.0 {
			d = 4.0
		}
		importance[i] = int(math.Floor(0.5 + 13.0*math.Exp2(d)))
	}
	if (!vbr || constrainedVbr) && !isTransient {
		for i := 0; i < end; i++ {
			follower[i] *= 0.5
		}
	}
	for i := 0; i < end; i++ {
		if i < 8 {
			follower[i] *= 2.0
		}
		if i >= 12 {
			follower[i] *= 0.5
		}
		follower[i] = math.Min(follower[i], 4.0)
	}
	for i := 0; i < end; i++ {
		width := C * int(EBands48000[i+1]-EBands48000[i]) << uint(lm)
		var boost int
		switch {
		case width < 6:
			boost = int(follower[i])
		case width > 48:
			boost = int(follower[i] * 8)
		default:
			boost = int(follower[i] * float64(width) / 6.0)
		}
		offsets[i] = boost
	}
	return offsets, importance
}

// l1Metric is the float port of libopus celt_encoder.c l1_metric: the L1 norm of
// the coefficients, biased upward by lm*bias*L1 so deeper (more time-resolved)
// splits are slightly penalised when in doubt (preferring frequency resolution).
func l1Metric(tmp []float64, n, lm int, bias float64) float64 {
	var l1 float64
	for i := 0; i < n; i++ {
		l1 += math.Abs(tmp[i])
	}
	l1 += float64(lm) * bias * l1
	return l1
}

func iabs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

func imin(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// tfAnalysis is the float port of libopus celt_encoder.c tf_analysis. For each
// band it measures an L1 sparsity metric of the coefficients at several Haar
// split depths (l1_metric after repeated haar1), picking the depth that best
// concentrates energy; the per-band best level becomes a metric. A two-pass
// Viterbi then chooses tf_res[] (the per-band 0/1 resolution decision, pre
// tf_select mapping) and tf_select to minimise the total importance-weighted
// distance to the realisable tf_select_table resolutions plus a per-switch cost
// (lambda). tfRes[0..end) is filled on return and the chosen tf_select returned.
//
// X is the normalised spectrum, channel-major with per-channel stride n0; tfChan
// selects the analysed channel. importance[] weights each band's contribution;
// tfEstimate biases l1_metric toward frequency resolution at low transient energy.
func tfAnalysis(end int, isTransient bool, tfRes []int, lambda int, X []float64, n0, lm, tfChan int, tfEstimate float64, importance []int) int {
	isT := 0
	if isTransient {
		isT = 1
	}
	lambdaTerm := lambda
	if isTransient {
		lambdaTerm = 0 // (isTransient ? 0 : lambda) on the cost1 init
	}
	bias := 0.04 * math.Max(-0.25, 0.5-tfEstimate)

	metric := make([]int, end)
	path0 := make([]int, end)
	path1 := make([]int, end)
	maxN := (int(EBands48000[end]) - int(EBands48000[end-1])) << uint(lm)
	tmp := make([]float64, maxN)
	tmp1 := make([]float64, maxN)

	for i := 0; i < end; i++ {
		bw := int(EBands48000[i+1]) - int(EBands48000[i])
		N := bw << uint(lm)
		narrow := bw == 1
		copy(tmp[:N], X[tfChan*n0+(int(EBands48000[i])<<uint(lm)):])

		lmArg := 0
		if isTransient {
			lmArg = lm
		}
		L1 := l1Metric(tmp[:N], N, lmArg, bias)
		bestL1 := L1
		bestLevel := 0

		// Check the -1 (one extra freq-resolution) case for transients.
		if isTransient && !narrow {
			copy(tmp1[:N], tmp[:N])
			haar1(tmp1[:N], N>>uint(lm), 1<<uint(lm))
			L1 = l1Metric(tmp1[:N], N, lm+1, bias)
			if L1 < bestL1 {
				bestL1 = L1
				bestLevel = -1
			}
		}

		kEnd := lm
		if !isTransient && !narrow {
			kEnd = lm + 1
		}
		for k := 0; k < kEnd; k++ {
			var B int
			if isTransient {
				B = lm - k - 1
			} else {
				B = k + 1
			}
			haar1(tmp[:N], N>>uint(k), 1<<uint(k))
			L1 = l1Metric(tmp[:N], N, B, bias)
			if L1 < bestL1 {
				bestL1 = L1
				bestLevel = k + 1
			}
		}

		// metric is in Q1 (×2) to allow a half-way point for narrow bands.
		if isTransient {
			metric[i] = 2 * bestLevel
		} else {
			metric[i] = -2 * bestLevel
		}
		if narrow && (metric[i] == 0 || metric[i] == -2*lm) {
			metric[i]--
		}
	}

	// Choose tf_select by evaluating the full Viterbi cost for both selectors.
	tfSelect := 0
	var selcost [2]int
	for sel := 0; sel < 2; sel++ {
		cost0 := importance[0] * iabs(metric[0]-2*tfSelectTable[lm][4*isT+2*sel+0])
		cost1 := importance[0]*iabs(metric[0]-2*tfSelectTable[lm][4*isT+2*sel+1]) + lambdaTerm
		for i := 1; i < end; i++ {
			curr0 := imin(cost0, cost1+lambda)
			curr1 := imin(cost0+lambda, cost1)
			cost0 = curr0 + importance[i]*iabs(metric[i]-2*tfSelectTable[lm][4*isT+2*sel+0])
			cost1 = curr1 + importance[i]*iabs(metric[i]-2*tfSelectTable[lm][4*isT+2*sel+1])
		}
		selcost[sel] = imin(cost0, cost1)
	}
	// Conservatively only allow tf_select=1 for transients.
	if selcost[1] < selcost[0] && isTransient {
		tfSelect = 1
	}

	// Viterbi forward pass with the chosen tf_select, recording back-pointers.
	cost0 := importance[0] * iabs(metric[0]-2*tfSelectTable[lm][4*isT+2*tfSelect+0])
	cost1 := importance[0]*iabs(metric[0]-2*tfSelectTable[lm][4*isT+2*tfSelect+1]) + lambdaTerm
	for i := 1; i < end; i++ {
		from0 := cost0
		from1 := cost1 + lambda
		var curr0 int
		if from0 < from1 {
			curr0 = from0
			path0[i] = 0
		} else {
			curr0 = from1
			path0[i] = 1
		}

		from0 = cost0 + lambda
		from1 = cost1
		var curr1 int
		if from0 < from1 {
			curr1 = from0
			path1[i] = 0
		} else {
			curr1 = from1
			path1[i] = 1
		}
		cost0 = curr0 + importance[i]*iabs(metric[i]-2*tfSelectTable[lm][4*isT+2*tfSelect+0])
		cost1 = curr1 + importance[i]*iabs(metric[i]-2*tfSelectTable[lm][4*isT+2*tfSelect+1])
	}
	if cost0 < cost1 {
		tfRes[end-1] = 0
	} else {
		tfRes[end-1] = 1
	}
	// Viterbi backward pass to resolve the path.
	for i := end - 2; i >= 0; i-- {
		if tfRes[i+1] == 1 {
			tfRes[i] = path1[i+1]
		} else {
			tfRes[i] = path0[i+1]
		}
	}
	return tfSelect
}

// allocTrimAnalysis is the float port of libopus alloc_trim_analysis. It returns
// the allocation trim index (0..10) from the spectral tilt and, for stereo, the
// inter-channel correlation at low frequencies. surroundTrim is derived by the
// multistream channel-role masking analysis.
// equivRate is the
// per-stream bitrate in bps. X is the normalised spectrum, logE the band log
// energies (channel-major), frameLen the per-channel coefficient count.
func allocTrimAnalysis(X, logE []float64, numBands, end, lm, C, frameLen, intensity int, tfEstimate, surroundTrim float64, equivRate int, useTonalitySlope bool) int {
	M := 1 << uint(lm)
	trim := 5.0
	switch {
	case equivRate < 64000:
		trim = 4.0
	case equivRate < 80000:
		frac := float64(equivRate-64000) / 1024.0
		trim = 4.0 + (1.0/16.0)*frac
	}

	if C == 2 {
		sum := 0.0
		for i := 0; i < 8; i++ {
			n := M * int(EBands48000[i+1]-EBands48000[i])
			off := M * int(EBands48000[i])
			sum += innerProdF(X[off:], X[frameLen+off:], n)
		}
		sum *= 1.0 / 8.0
		sum = math.Min(1.0, math.Abs(sum))
		minXC := sum
		for i := 8; i < intensity && i < end; i++ {
			n := M * int(EBands48000[i+1]-EBands48000[i])
			off := M * int(EBands48000[i])
			p := math.Abs(innerProdF(X[off:], X[frameLen+off:], n))
			minXC = math.Min(minXC, p)
		}
		minXC = math.Min(1.0, math.Abs(minXC))
		logXC := math.Log2(1.001 - sum*sum)
		_ = math.Max(0.5*logXC, math.Log2(1.001-minXC*minXC)) // logXC2: feeds stereo_saving (unused here)
		trim += math.Max(-4.0, 0.75*logXC)
	}

	// Spectral tilt: positive diff means a falling spectrum (more LF energy).
	diff := 0.0
	for c := 0; c < C; c++ {
		for i := 0; i < end-1; i++ {
			diff += logE[c*numBands+i] * float64(2+2*i-end)
		}
	}
	diff /= float64(C * (end - 1))
	trim -= math.Max(-2.0, math.Min(2.0, (diff+1.0)/6.0))
	trim -= 2 * tfEstimate
	trim -= surroundTrim
	if C == 2 && useTonalitySlope {
		tonalitySlope := spectralTonalitySlope(X, logE, numBands, end, lm, C, frameLen)
		trim -= math.Max(-2, math.Min(2, 2*(tonalitySlope+0.05)))
	}

	trimIndex := int(math.Floor(trim + 0.5))
	if trimIndex < 0 {
		trimIndex = 0
	}
	if trimIndex > 10 {
		trimIndex = 10
	}
	return trimIndex
}

// spectralTonalitySlope approximates the frequency slope of libopus's external
// tonality analysis from the CELT spectrum already available here. Per-band
// spectral concentration supplies a bounded tonality proxy, while a relative
// energy gate prevents normalised quantisation-floor bands from influencing the
// result. Negative values mean tonality is concentrated in lower bands.
func spectralTonalitySlope(X, logE []float64, numBands, end, lm, C, frameLen int) float64 {
	if C < 1 || end < 1 || len(X) < C*frameLen || len(logE) < C*numBands {
		return 0
	}
	maxLogE := logE[0]
	for c := 0; c < C; c++ {
		for i := 0; i < end; i++ {
			if value := logE[c*numBands+i]; value > maxLogE {
				maxLogE = value
			}
		}
	}
	M := 1 << uint(lm)
	slope := 0.0
	for i := 0; i < end; i++ {
		bandLogE := logE[i]
		for c := 1; c < C; c++ {
			if value := logE[c*numBands+i]; value > bandLogE {
				bandLogE = value
			}
		}
		// Eight log2-amplitude units are roughly 48 dB. Quieter bands do not
		// carry a reliable tonality estimate after normalisation.
		if bandLogE < maxLogE-8 {
			continue
		}
		N := M * int(EBands48000[i+1]-EBands48000[i])
		if N < 2 {
			continue
		}
		bandTonality := 0.0
		for c := 0; c < C; c++ {
			off := c*frameLen + M*int(EBands48000[i])
			var sum2, sum4 float64
			for j := 0; j < N; j++ {
				x2 := X[off+j] * X[off+j]
				sum2 += x2
				sum4 += x2 * x2
			}
			if sum2 > 0 {
				concentration := (float64(N)*sum4/(sum2*sum2) - 1) / float64(N-1)
				bandTonality += math.Max(0, math.Min(1, concentration))
			}
		}
		bandTonality /= float64(C)
		slope += bandTonality * float64(i-8)
	}
	return math.Max(-1, math.Min(1, slope/64))
}

// surroundMaskTrim isolates libopus' mask-slope contribution to allocation
// trim. The per-band dynalloc and VBR consumers are intentionally left for
// separate measured decisions.
func surroundMaskTrim(mask []float64, channels, numBands, maskEnd int) float64 {
	if channels < 1 || maskEnd < 2 || len(mask) < channels*numBands {
		return 0
	}
	var maskAverage, slope float64
	count := 0
	for channel := 0; channel < channels; channel++ {
		for band := 0; band < maskEnd; band++ {
			value := math.Max(-2, math.Min(0.25, mask[channel*numBands+band]))
			if value > 0 {
				value *= 0.5
			}
			width := int(EBands48000[band+1] - EBands48000[band])
			maskAverage += value * float64(width)
			count += width
			slope += value * float64(1+2*band-maskEnd)
		}
	}
	if count == 0 {
		return 0
	}
	maskAverage = maskAverage/float64(count) + 0.2
	slope *= 6 / float64(channels*(maskEnd-1)*(maskEnd+1)*maskEnd)
	slope *= 0.5
	slope = math.Max(-0.031, math.Min(0.031, slope))

	middleBand := 0
	for middleBand+1 < maskEnd && EBands48000[middleBand+1] < EBands48000[maskEnd]/2 {
		middleBand++
	}
	unmaskedBands := 0
	for band := 0; band < maskEnd; band++ {
		unmask := mask[band]
		for channel := 1; channel < channels; channel++ {
			unmask = math.Max(unmask, mask[channel*numBands+band])
		}
		unmask = math.Min(unmask, 0) - (maskAverage + slope*float64(band-middleBand))
		if unmask > 0.25 {
			unmaskedBands++
		}
	}
	if unmaskedBands >= 3 && maskAverage+0.25 > 0 {
		return 0
	}
	return 64 * slope
}
