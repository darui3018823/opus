package celt

import "math"

// This file holds the encoder-side analysis/decision functions that feed the
// already-correct symbol writers in celt_encode.go (tfEncode, dynallocEncode)
// and the spread/alloc_trim ICDF coders in encoder.go. They are float ports of
// libopus celt/celt_encoder.c (spreading_decision, dynalloc_analysis,
// alloc_trim_analysis), simplified where bit-exactness is not required: the
// external tonality analyzer, surround/LFE handling, and the spread_weight
// coupling are dropped (spread_weight is treated as uniform). The decoder reads
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
func transientAnalysis(bufs [][]float64, length, C int) bool {
	const forwardDecay = 0.0625 // 6.7 dB/ms forward masking (CELT-only path)
	const epsilon = 1e-15
	len2 := length / 2
	maskMetric := 0
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
		}
	}
	return maskMetric > 200
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
// uses bandLogE3(=bandLogE2) for the follower and bandLogE for the depth. The
// internal 2/3-budget break is dropped: dynallocEncode clamps the coded boost
// against the real range-coder budget and the per-band cap, keeping the result
// symmetric with the decoder.
func dynallocAnalysis(logE, logE2 []float64, numBands, end, C, lm int, isTransient, vbr, constrainedVbr bool) []int {
	offsets := make([]int, numBands)
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
	return offsets
}

// allocTrimAnalysis is the float port of libopus alloc_trim_analysis. It returns
// the allocation trim index (0..10) from the spectral tilt and, for stereo, the
// inter-channel correlation at low frequencies. tf_estimate and surround_trim
// are taken as zero (no transient/surround analysis yet). equivRate is the
// per-stream bitrate in bps. X is the normalised spectrum, logE the band log
// energies (channel-major), frameLen the per-channel coefficient count.
func allocTrimAnalysis(X, logE []float64, numBands, end, lm, C, frameLen, intensity, equivRate int) int {
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

	trimIndex := int(math.Floor(trim + 0.5))
	if trimIndex < 0 {
		trimIndex = 0
	}
	if trimIndex > 10 {
		trimIndex = 10
	}
	return trimIndex
}
