package dsp

import (
	"math"
	"math/rand"
	"testing"
)

// TestIMDCTRawStaticMatchesGeneric checks the static-size backward MDCT
// against the generic path bit for bit.
func TestIMDCTRawStaticMatchesGeneric(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for _, n := range []int{960, 480, 240, 120} {
		fast := NewCELTMode(n, 120, make([]float32, 120))
		slow := NewCELTMode(n, 120, make([]float32, 120))
		for trial := 0; trial < 200; trial++ {
			X := make([]float64, n)
			scale := math.Pow(10, float64(rng.Intn(9)-4))
			for i := range X {
				X[i] = float64(float32(rng.NormFloat64() * scale))
			}
			got := fast.imdctRawStatic(X, make([]float64, n))
			want := slow.imdctRawGeneric(X, make([]float64, n))
			for i := range want {
				if math.Float64bits(got[i]) != math.Float64bits(want[i]) {
					t.Fatalf("n=%d trial %d: [%d] = %v, want %v", n, trial, i, got[i], want[i])
				}
			}
		}
	}
}

// TestMDCTForwardStaticMatchesGeneric checks the static-size forward MDCT
// against the generic path bit for bit.
func TestMDCTForwardStaticMatchesGeneric(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	window := make([]float32, 120)
	for i := range window {
		window[i] = float32(math.Sin(float64(i)))
	}
	for _, n := range []int{960, 480, 240, 120} {
		fast := NewCELTMode(n, 120, window)
		slow := NewCELTMode(n, 120, window)
		for trial := 0; trial < 200; trial++ {
			in := make([]float64, n+120)
			scale := math.Pow(10, float64(rng.Intn(9)-4))
			for i := range in {
				in[i] = float64(float32(rng.NormFloat64() * scale))
			}
			got := fast.cltMDCTForward(in, make([]float64, n), true)
			want := slow.cltMDCTForward(in, make([]float64, n), false)
			for i := range want {
				if math.Float64bits(got[i]) != math.Float64bits(want[i]) {
					t.Fatalf("n=%d trial %d: [%d] = %v, want %v", n, trial, i, got[i], want[i])
				}
			}
		}
	}
}
