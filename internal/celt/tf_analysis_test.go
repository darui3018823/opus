package celt

import (
	"math"
	"testing"
)

// buildAlternatingSpectrum lays out a single-channel normalised spectrum where
// even bands are maximally sparse (all energy in one bin) and odd bands are
// "white" (energy spread evenly). After the Haar splits in tf_analysis the white
// bands shrink in L1 (so deeper time/freq splitting helps → metric pushed toward
// tf_res=1) while the sparse bands do not (metric stays near 0 → tf_res=0), so a
// low switch cost yields a varying tf_res while a high one flattens it.
func buildAlternatingSpectrum(end, lm int) ([]float64, int) {
	M := 1 << uint(lm)
	n0 := M * int(EBands48000[NumBands48000])
	X := make([]float64, n0)
	for i := 0; i < end; i++ {
		lo := M * int(EBands48000[i])
		hi := M * int(EBands48000[i+1])
		if i%2 == 0 {
			X[lo] = 1.0
		} else {
			v := 1.0 / math.Sqrt(float64(hi-lo))
			for j := lo; j < hi; j++ {
				X[j] = v
			}
		}
	}
	return X, n0
}

// TestTFAnalysisLambdaSwitchCost checks the Viterbi switch-cost wiring: a huge
// lambda must collapse the per-band tf_res decisions to a single constant path,
// while a zero switch cost lets each band follow its own L1 metric (so the path
// is not flat for a spectrum whose bands genuinely differ).
func TestTFAnalysisLambdaSwitchCost(t *testing.T) {
	lm := 3
	end := 21
	X, n0 := buildAlternatingSpectrum(end, lm)
	importance := make([]int, end)
	for i := range importance {
		importance[i] = 13
	}

	flat := make([]int, NumBands48000)
	tfAnalysis(end, false, flat, 1<<20, X, n0, lm, 0, 0.0, importance)
	for i := 1; i < end; i++ {
		if flat[i] != flat[0] {
			t.Fatalf("a huge lambda must force a flat tf_res path; got a change at band %d: %v", i, flat[:end])
		}
	}

	varied := make([]int, NumBands48000)
	tfAnalysis(end, false, varied, 0, X, n0, lm, 0, 0.0, importance)
	isFlat := true
	for i := 1; i < end; i++ {
		if varied[i] != varied[0] {
			isFlat = false
			break
		}
	}
	if isFlat {
		t.Fatalf("with no switch cost a band-varying spectrum must yield a non-flat tf_res; got flat %v", varied[:end])
	}

	// Every decision must be a valid pre-mapping 0/1 flag.
	for i := 0; i < end; i++ {
		if varied[i] != 0 && varied[i] != 1 {
			t.Fatalf("tf_res[%d]=%d is not a 0/1 decision", i, varied[i])
		}
	}
}

// TestTFAnalysisImportanceWeights confirms importance scales a band's pull on the
// decision: raising one band's importance while zeroing the rest makes that band
// reach its metric-preferred tf_res even against a switch cost the uniform-weight
// case could not overcome.
func TestTFAnalysisImportanceWeights(t *testing.T) {
	lm := 3
	end := 21
	X, n0 := buildAlternatingSpectrum(end, lm)

	// Pick a wide, "white" band (odd index, many bins) whose metric prefers
	// tf_res=1, and a lambda that a uniform weight of 13 cannot beat.
	target := 19 // odd → white band, also wide at LM=3
	lambda := 400

	uniform := make([]int, end)
	for i := range uniform {
		uniform[i] = 13
	}
	resUniform := make([]int, NumBands48000)
	tfAnalysis(end, false, resUniform, lambda, X, n0, lm, 0, 0.0, uniform)

	weighted := make([]int, end)
	for i := range weighted {
		weighted[i] = 1
	}
	weighted[target] = 10000
	resWeighted := make([]int, NumBands48000)
	tfAnalysis(end, false, resWeighted, lambda, X, n0, lm, 0, 0.0, weighted)

	if resUniform[target] == resWeighted[target] {
		t.Logf("uniform=%v", resUniform[:end])
		t.Logf("weighted=%v", resWeighted[:end])
		t.Fatalf("dominant importance at band %d should change its tf_res relative to the uniform case", target)
	}
}

func TestAllocTrimAnalysisUsesTFEstimate(t *testing.T) {
	const (
		end      = NumBands48000
		frameLen = FrameSize20ms
	)
	X := make([]float64, frameLen)
	logE := make([]float64, NumBands48000)
	withoutTF := allocTrimAnalysis(X, logE, NumBands48000, end, 3, 1, frameLen, end, 0, 0, 64000, true)
	withTF := allocTrimAnalysis(X, logE, NumBands48000, end, 3, 1, frameLen, end, 0.8, 0, 64000, true)
	if withTF >= withoutTF {
		t.Fatalf("allocation trim with tfEstimate=0.8 is %d, without=%d; want lower", withTF, withoutTF)
	}
}
