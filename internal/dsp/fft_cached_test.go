package dsp

import (
	"math"
	"testing"
)

// TestRealFFTIntoMatchesRealFFT checks that the cached-table transform is
// bit-identical to RealFFT.
func TestRealFFTIntoMatchesRealFFT(t *testing.T) {
	seed := uint32(7)
	for _, n := range []int{1, 2, 4, 64, 256, 1024, 2048} {
		in := make([]float64, n)
		for i := range in {
			seed = seed*1664525 + 1013904223
			in[i] = float64(int32(seed)) / (1 << 31)
		}
		want, err := RealFFT(in)
		if err != nil {
			t.Fatal(err)
		}
		got, err := RealFFTInto(in, make([]Complex, n))
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != len(want) {
			t.Fatalf("n=%d: %d bins, want %d", n, len(got), len(want))
		}
		for k := range want {
			if math.Float64bits(got[k].Real) != math.Float64bits(want[k].Real) ||
				math.Float64bits(got[k].Imag) != math.Float64bits(want[k].Imag) {
				t.Fatalf("n=%d bin %d: got %v, want %v", n, k, got[k], want[k])
			}
		}
	}
}
