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
	sum = (3*sum + (((3-lastDecision)<<7)+64) + 2) >> 2
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
// (the libopus "boost" integer). logE is the mean-subtracted log2-amplitude band
// energy laid out channel-major [c*numBands+i]. The internal 2/3-budget break is
// dropped: dynallocEncode clamps the coded boost against the real range-coder
// budget and the per-band cap, keeping the result symmetric with the decoder.
func dynallocAnalysis(logE []float64, numBands, end, C, lm int, isTransient, vbr, constrainedVbr bool) []int {
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
		f[0] = logE[c*numBands]
		for i := 1; i < end; i++ {
			if logE[c*numBands+i] > logE[c*numBands+i-1]+0.5 {
				last = i
			}
			f[i] = math.Min(f[i-1]+1.5, logE[c*numBands+i])
		}
		for i := last - 1; i >= 0; i-- {
			f[i] = math.Min(f[i], math.Min(f[i+1]+2.0, logE[c*numBands+i]))
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
