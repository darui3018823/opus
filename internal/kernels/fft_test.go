package kernels

import (
	"math"
	"math/rand"
	"testing"
)

func randTwiddle(r *rand.Rand, special bool) func(q, j int) (float32, float32) {
	return func(q, j int) (float32, float32) {
		if special {
			return specialFloat32(r), specialFloat32(r)
		}
		a := r.Float64() * 2 * math.Pi
		return float32(math.Cos(a)), float32(math.Sin(a))
	}
}

func sameSlices(a, b []float32) int {
	for i := range a {
		if !sameBits(a[i], b[i]) {
			return i
		}
	}
	return -1
}

// fftShapes are the (radix, m, groups, mm) stages of the static-mode FFTs
// (60/120/240/480 points), plus a few others.
var fftShapes = [][4]int{
	{5, 12, 1, 60}, {3, 4, 5, 12}, {5, 24, 1, 120}, {3, 8, 5, 24},
	{4, 4, 15, 16}, {5, 48, 1, 240}, {3, 16, 5, 48}, {4, 4, 15, 16},
	{5, 96, 1, 480}, {3, 32, 5, 96}, {4, 8, 15, 32}, {4, 4, 3, 20},
	{5, 4, 3, 20}, {3, 4, 2, 13},
}

func TestFFTStagesMatchGeneric(t *testing.T) {
	withPaths(t, func(t *testing.T) {
		r := rand.New(rand.NewSource(60))
		for trial := 0; trial < 300; trial++ {
			s := fftShapes[trial%len(fftShapes)]
			p, m, groups, mm := s[0], s[1], s[2], s[3]
			special := trial%3 == 0
			tw := FFTTwiddleTable(p, m, randTwiddle(r, special))
			n := 2 * ((groups-1)*mm + p*m)
			a := make([]float32, n)
			fillFloat32(r, a, special)
			b := append([]float32(nil), a...)
			ya, _ := randTwiddle(r, special)(1, 0)
			yi, _ := randTwiddle(r, special)(1, 0)
			yb, ybi := randTwiddle(r, special)(1, 0)
			switch p {
			case 3:
				FFTRadix3(a, tw, yi, m, groups, mm)
				FFTRadix3Generic(b, tw, yi, m, groups, mm)
			case 4:
				FFTRadix4(a, tw, m, groups, mm)
				FFTRadix4Generic(b, tw, m, groups, mm)
			case 5:
				FFTRadix5(a, tw, [2]float32{ya, yi}, [2]float32{yb, ybi}, m, groups, mm)
				FFTRadix5Generic(b, tw, [2]float32{ya, yi}, [2]float32{yb, ybi}, m, groups, mm)
			}
			if i := sameSlices(a, b); i >= 0 {
				t.Fatalf("trial %d radix %d m %d groups %d: float %d got %v want %v", trial, p, m, groups, i, a[i], b[i])
			}
		}
	})
}

func BenchmarkFFTRadix5(b *testing.B) {
	r := rand.New(rand.NewSource(61))
	tw := FFTTwiddleTable(5, 96, randTwiddle(r, false))
	f := make([]float32, 960)
	fillFloat32(r, f, false)
	ya, yb := [2]float32{0.3, -0.95}, [2]float32{-0.8, -0.58}
	b.Run("generic", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			FFTRadix5Generic(f, tw, ya, yb, 96, 1, 480)
		}
	})
	b.Run(Features(), func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			FFTRadix5(f, tw, ya, yb, 96, 1, 480)
		}
	})
}
