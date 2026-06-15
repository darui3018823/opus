package celt

import (
	"math/rand"
	"sort"
	"testing"
)

// bruteMedian returns the exact median (middle of the sorted values) of x.
func bruteMedian(x []float64) float64 {
	s := append([]float64(nil), x...)
	sort.Float64s(s)
	return s[len(s)/2]
}

// TestMedianHelpers verifies medianOf3 and medianOf5 against a brute-force
// sorted median over many random inputs, including duplicates. These selection
// networks have subtle branch logic, so the exhaustive check guards the port.
func TestMedianHelpers(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	for iter := 0; iter < 20000; iter++ {
		// Mix of continuous values and a small integer set to force ties.
		gen := func(n int) []float64 {
			x := make([]float64, n)
			for i := range x {
				if iter%2 == 0 {
					x[i] = r.Float64()*10 - 5
				} else {
					x[i] = float64(r.Intn(4))
				}
			}
			return x
		}
		x3 := gen(3)
		if got, want := medianOf3(x3), bruteMedian(x3); got != want {
			t.Fatalf("medianOf3(%v)=%v want %v", x3, got, want)
		}
		x5 := gen(5)
		if got, want := medianOf5(x5), bruteMedian(x5); got != want {
			t.Fatalf("medianOf5(%v)=%v want %v", x5, got, want)
		}
	}
}

// TestDynallocMedianFilterPinsFollower proves the median filter is wired into
// dynallocAnalysis. A band sitting in a deep logE2 dip surrounded by loud bands
// would, without the filter, see its follower pinned at its own (low) logE2 — so
// deepening the dip would inflate the masking depth (logE-follower) and the
// boost. The median filter instead raises the follower toward the neighbourhood
// median, so the boost at the dip band is independent of how deep the dip is.
func TestDynallocMedianFilterPinsFollower(t *testing.T) {
	const numBands = 21
	const end = 21
	const C = 1
	const lm = 3
	const dip = 10

	build := func(dipE2 float64) (logE, logE2 []float64) {
		logE = make([]float64, numBands)
		logE2 = make([]float64, numBands)
		for i := range logE {
			logE[i] = 8.0
			logE2[i] = 8.0
		}
		logE[dip] = 15.0 // a loud actual energy at the dip band
		logE2[dip] = dipE2
		return
	}

	// vbr=true, constrainedVbr=false, isTransient=false → no follower halving.
	logEa, logE2a := build(7.0) // shallow dip
	offA := dynallocAnalysis(logEa, logE2a, numBands, end, C, lm, false, true, false)

	logEb, logE2b := build(0.0) // much deeper dip; same loud logE
	offB := dynallocAnalysis(logEb, logE2b, numBands, end, C, lm, false, true, false)

	if offA[dip] <= 0 {
		t.Fatalf("expected a positive boost at the dip band, got %d", offA[dip])
	}
	if offA[dip] != offB[dip] {
		t.Fatalf("median filter not pinning follower: deepening the logE2 dip changed "+
			"the boost (shallow=%d deep=%d); without the filter the deep dip would boost more",
			offA[dip], offB[dip])
	}
	t.Logf("dip-band boost is %d regardless of dip depth (median filter active)", offA[dip])
}
