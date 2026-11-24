package kernels

import (
	"math"
	"math/rand"
	"testing"
)

func randInt32(r *rand.Rand, extreme bool) int32 {
	if extreme {
		return [...]int32{math.MinInt32, math.MaxInt32, -1, 0, 1, -65536, 65535}[r.Intn(7)]
	}
	switch r.Intn(3) {
	case 0:
		return int32(r.Uint32())
	case 1:
		return int32(r.NormFloat64() * 1e6)
	}
	return int32(r.NormFloat64() * 3e4)
}

func randInt16(r *rand.Rand, extreme bool) int16 {
	if extreme {
		return [...]int16{math.MinInt16, math.MaxInt16, -1, 0, 1}[r.Intn(5)]
	}
	return int16(r.Intn(65536) - 32768)
}

func TestWarpedAR4MatchesGeneric(t *testing.T) {
	withPaths(t, func(t *testing.T) {
		r := rand.New(rand.NewSource(30))
		for trial := 0; trial < 3000; trial++ {
			extreme := trial%4 == 0
			order := 2 * (1 + r.Intn(MaxShapeOrder/2))
			ar := make([]int16, order)
			for i := range ar {
				ar[i] = randInt16(r, extreme)
			}
			var a, b WarpedAR4
			a.SetCoefficients(ar, order, randInt16(r, extreme), randInt16(r, extreme), 1+r.Intn(4))
			for j := 0; j < MaxShapeOrder; j++ {
				for k := 0; k < 4; k++ {
					a.S[j][k] = randInt32(r, extreme)
				}
			}
			b = a
			// Run several samples so errors in the state update show up.
			for step := 0; step < 8; step++ {
				var diff, lf, got, want [4]int32
				for k := range diff {
					diff[k] = randInt32(r, extreme)
					lf[k] = randInt32(r, extreme)
				}
				a.Step(&diff, &lf, &got)
				b.StepGeneric(&diff, &lf, &want)
				if a.Lanes == 2 {
					// Lanes 2 and 3 are unspecified outputs.
					got[2], got[3], want[2], want[3] = 0, 0, 0, 0
				}
				if got != want {
					t.Fatalf("trial %d order %d step %d: n_AR got %v, want %v", trial, order, step, got, want)
				}
				if a.S != b.S {
					t.Fatalf("trial %d order %d step %d: state differs", trial, order, step)
				}
			}
		}
	})
}

func BenchmarkWarpedAR4(b *testing.B) {
	r := rand.New(rand.NewSource(31))
	ar := make([]int16, 24)
	for i := range ar {
		ar[i] = int16(r.Intn(8000) - 4000)
	}
	var w WarpedAR4
	w.SetCoefficients(ar, 24, 9000, -3000, 4)
	var diff, lf, out [4]int32
	for k := range diff {
		diff[k] = int32(r.Intn(1 << 20))
		lf[k] = int32(r.Intn(1 << 20))
	}
	b.Run("generic", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			w.StepGeneric(&diff, &lf, &out)
		}
	})
	b.Run(Features(), func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			w.Step(&diff, &lf, &out)
		}
	})
}
