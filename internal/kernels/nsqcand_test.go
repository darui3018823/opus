package kernels

import (
	"math/rand"
	"testing"
)

func randNSQCand4(r *rand.Rand, extreme bool) NSQCand4 {
	var c NSQCand4
	lf := randInt32(r, extreme)
	offset := int32([]int{32, 100, 240, 256}[r.Intn(4)])
	if extreme && r.Intn(2) == 0 {
		offset = randInt32(r, true)
	}
	lambda := int32(r.Intn(8000))
	if extreme {
		lambda = randInt32(r, true)
	} else if r.Intn(3) == 0 {
		lambda = 2048 + int32(r.Intn(3)) - 1 // the branch boundary
	}
	c.SetSubframe(lf, offset, lambda)
	scale := func(x int32) int32 {
		if extreme {
			return x
		}
		return x >> uint(r.Intn(20))
	}
	c.SetSample(scale(randInt32(r, extreme)), scale(randInt32(r, extreme)), scale(randInt32(r, extreme)))
	for k := 0; k < 4; k++ {
		c.Seed[k] = int32(r.Uint32())
		c.NAR[k] = scale(randInt32(r, extreme))
		c.LPCPred[k] = scale(randInt32(r, extreme))
		c.Shape[k] = scale(randInt32(r, extreme))
		c.LFAR[k] = scale(randInt32(r, extreme))
		c.RD[k] = scale(randInt32(r, extreme))
	}
	return c
}

func TestNSQCand4MatchesGeneric(t *testing.T) {
	withPaths(t, func(t *testing.T) {
		r := rand.New(rand.NewSource(50))
		for trial := 0; trial < 200000; trial++ {
			a := randNSQCand4(r, trial%5 == 0)
			b := a
			a.Step()
			b.StepGeneric()
			if a != b {
				t.Fatalf("trial %d:\n got  %+v\n want %+v", trial, a, b)
			}
		}
	})
}

// TestNSQCand4Residuals sweeps every residual the limiter can produce
// against both lambda regimes and all offsets, so each quantizer branch
// and its boundaries are hit.
func TestNSQCand4Residuals(t *testing.T) {
	withPaths(t, func(t *testing.T) {
		for _, lambda := range []int32{0, 1024, 2047, 2048, 2049, 4000, 8191, 40000} {
			for _, offset := range []int32{32, 100, 240, 256} {
				var a NSQCand4
				a.SetSubframe(0, offset, lambda)
				for x := int32(-33 << 10); x <= 32<<10; x += 4 {
					// With zero shaping and prediction, r = x_Q10 - round(x... ) :
					// drive r through x_Q10 directly.
					a.SetSample(x, 0, 0)
					for k := range a.Seed {
						a.Seed[k] = int32(k) // non-negative after RAND for some lanes
						a.NAR[k], a.LPCPred[k], a.Shape[k], a.LFAR[k], a.RD[k] = 0, int32(k)*3, 0, 0, 0
					}
					b := a
					a.Step()
					b.StepGeneric()
					if a != b {
						t.Fatalf("lambda %d offset %d x %d:\n got  %+v\n want %+v", lambda, offset, x, a, b)
					}
				}
			}
		}
	})
}

func BenchmarkNSQCand4(b *testing.B) {
	r := rand.New(rand.NewSource(51))
	c := randNSQCand4(r, false)
	b.Run("generic", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			c.StepGeneric()
		}
	})
	b.Run(Features(), func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			c.Step()
		}
	})
}
