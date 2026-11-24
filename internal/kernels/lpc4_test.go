package kernels

import (
	"math/rand"
	"testing"
)

func TestLPC4MatchesGeneric(t *testing.T) {
	withPaths(t, func(t *testing.T) {
		r := rand.New(rand.NewSource(40))
		for trial := 0; trial < 2000; trial++ {
			extreme := trial%4 == 0
			order := 1 + r.Intn(16)
			if trial%3 == 0 {
				order = []int{10, 16}[r.Intn(2)]
			}
			a := make([]int16, order)
			for i := range a {
				a[i] = randInt16(r, extreme)
			}
			var p LPC4
			p.SetCoefficients(a, order, 1+r.Intn(4))
			for row := range p.H {
				for k := range p.H[row] {
					p.H[row][k] = randInt32(r, extreme)
				}
			}
			for _, i := range []int{0, r.Intn(LPCRows - 15), LPCRows - 16} {
				var got, want [4]int32
				p.Predict(i, &got)
				p.PredictGeneric(i, &want)
				if p.Lanes == 2 {
					got[2], got[3], want[2], want[3] = 0, 0, 0, 0
				}
				if got != want {
					t.Fatalf("trial %d order %d i %d: got %v, want %v", trial, order, i, got, want)
				}
			}
		}
	})
}

func BenchmarkLPC4(b *testing.B) {
	r := rand.New(rand.NewSource(41))
	a := make([]int16, 16)
	for i := range a {
		a[i] = int16(r.Intn(8000) - 4000)
	}
	var p LPC4
	p.SetCoefficients(a, 16, 4)
	var out [4]int32
	b.Run("generic", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			p.PredictGeneric(i&63, &out)
		}
	})
	b.Run(Features(), func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			p.Predict(i&63, &out)
		}
	})
}
