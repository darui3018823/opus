package kernels

import (
	"math"
	"math/rand"
	"testing"
)

func fillFloat64(r *rand.Rand, s []float64, special bool) {
	for i := range s {
		if special && r.Intn(4) == 0 {
			// values that round differently to float32, and overflow
			s[i] = [...]float64{1e39, -1e39, 1 + 1.0/(1<<24), 3.4028235677973366e38, 1e-46, -0.0}[r.Intn(6)]
			continue
		}
		s[i] = r.NormFloat64() * 1000
	}
}

func bitrevPerm(r *rand.Rand, n int) []int {
	return r.Perm(n)
}

func TestIMDCTRotationsMatchGeneric(t *testing.T) {
	withPaths(t, func(t *testing.T) {
		r := rand.New(rand.NewSource(70))
		for trial := 0; trial < 400; trial++ {
			n4 := []int{60, 120, 240, 480, 4, 8, 12}[trial%7]
			special := trial%3 == 0
			x := make([]float64, 2*n4)
			fillFloat64(r, x, special)
			trig := make([]float32, 2*n4)
			fillFloat32(r, trig, special)
			br := bitrevPerm(r, n4)
			a := make([]float32, 2*n4)
			b := make([]float32, 2*n4)
			IMDCTPreRotate(a, x, trig, NewBitRev(br), n4)
			IMDCTPreRotateGeneric(b, x, trig, br, n4)
			if i := sameSlices(a, b); i >= 0 {
				t.Fatalf("pre n4 %d trial %d: float %d got %v want %v", n4, trial, i, a[i], b[i])
			}
			fillFloat32(r, a, special)
			ba := make([]float64, 2*n4)
			bb := make([]float64, 2*n4)
			for i := range ba {
				ba[i], bb[i] = 7, 7
			}
			IMDCTPostRotate(ba, a, trig, n4)
			IMDCTPostRotateGeneric(bb, a, trig, n4)
			for i := range ba {
				if math.Float64bits(ba[i]) != math.Float64bits(bb[i]) && !(ba[i] != ba[i] && bb[i] != bb[i]) {
					t.Fatalf("post n4 %d trial %d: buf %d got %v want %v", n4, trial, i, ba[i], bb[i])
				}
			}
		}
	})
}

func BenchmarkIMDCTRotations(b *testing.B) {
	r := rand.New(rand.NewSource(71))
	const n4 = 480
	x := make([]float64, 2*n4)
	fillFloat64(r, x, false)
	trig := make([]float32, 2*n4)
	fillFloat32(r, trig, false)
	br := bitrevPerm(r, n4)
	f := make([]float32, 2*n4)
	buf := make([]float64, 2*n4)
	b.Run("generic", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			IMDCTPreRotateGeneric(f, x, trig, br, n4)
			IMDCTPostRotateGeneric(buf, f, trig, n4)
		}
	})
	vbr := NewBitRev(br)
	b.Run(Features(), func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			IMDCTPreRotate(f, x, trig, vbr, n4)
			IMDCTPostRotate(buf, f, trig, n4)
		}
	})
}
