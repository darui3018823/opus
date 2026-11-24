package kernels

import (
	"math/rand"
	"testing"
)

func TestCombMatchesGeneric(t *testing.T) {
	withPaths(t, func(t *testing.T) {
		r := rand.New(rand.NewSource(80))
		for trial := 0; trial < 3000; trial++ {
			special := trial%4 == 0
			p0 := 6 + r.Intn(1020)
			p1 := 6 + r.Intn(1020)
			if trial%5 == 0 {
				p0, p1 = 6+r.Intn(4), 6+r.Intn(4) // shortest periods
			}
			start := max(p0, p1) + 2 + r.Intn(3)
			n := r.Intn(200)
			x := make([]float32, start+n+3)
			fillFloat32(r, x, special)
			var g CombGains
			for i := range g {
				g[i] = float32(r.NormFloat64() * 0.3)
				if special && r.Intn(3) == 0 {
					g[i] = specialFloat32(r)
				}
			}
			w := make([]float32, n+1)
			for i := range w {
				w[i] = float32(r.Float64())
			}
			if special {
				fillFloat32(r, w, true)
			}
			a := append([]float32(nil), x...)
			b := append([]float32(nil), x...)
			if trial%2 == 0 {
				CombConst(a, start, n, p1, g[3], g[4], g[5])
				CombConstGeneric(b, start, n, p1, g[3], g[4], g[5])
			} else {
				CombOverlap(a, start, n, p0, p1, &g, w)
				CombOverlapGeneric(b, start, n, p0, p1, &g, w)
			}
			if i := sameSlices(a, b); i >= 0 {
				t.Fatalf("trial %d p0 %d p1 %d n %d: x[%d] got %v want %v", trial, p0, p1, n, i, a[i], b[i])
			}
		}
	})
}

func BenchmarkComb(b *testing.B) {
	r := rand.New(rand.NewSource(81))
	x := make([]float32, 1100+960)
	fillFloat32(r, x, false)
	w := make([]float32, 120)
	for i := range w {
		w[i] = float32(r.Float64())
	}
	g := CombGains{0.1, 0.05, 0.01, 0.2, 0.1, 0.02}
	b.Run("generic", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			CombOverlapGeneric(x, 1100, 120, 500, 510, &g, w)
			CombConstGeneric(x, 1220, 840, 510, g[3], g[4], g[5])
		}
	})
	b.Run(Features(), func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			CombOverlap(x, 1100, 120, 500, 510, &g, w)
			CombConst(x, 1220, 840, 510, g[3], g[4], g[5])
		}
	})
}
