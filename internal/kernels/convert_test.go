package kernels

import (
	"math"
	"math/rand"
	"testing"
)

func TestConversionsMatchGo(t *testing.T) {
	withPaths(t, func(t *testing.T) {
		r := rand.New(rand.NewSource(90))
		for trial := 0; trial < 500; trial++ {
			n := r.Intn(70)
			src := make([]float64, n)
			for i := range src {
				switch r.Intn(5) {
				case 0:
					src[i] = math.Float64frombits(r.Uint64()) // any pattern, NaN included
				case 1:
					src[i] = float64(float32(r.NormFloat64())) + math.Ldexp(float64(r.Intn(3)-1), -30)
				default:
					src[i] = r.NormFloat64() * 1e3
				}
			}
			got := make([]float32, n+1)
			got[n] = 42
			Float64sToFloat32s(got[:n], src)
			for i, v := range src {
				want := float32(v)
				if math.Float32bits(got[i]) != math.Float32bits(want) && !(got[i] != got[i] && want != want) {
					t.Fatalf("to32 %v: got %08x want %08x", v, math.Float32bits(got[i]), math.Float32bits(want))
				}
			}
			if got[n] != 42 {
				t.Fatal("wrote past dst")
			}
			back := make([]float64, n)
			Float32sToFloat64s(back, got[:n])
			for i, v := range got[:n] {
				want := float64(v)
				if math.Float64bits(back[i]) != math.Float64bits(want) && !(v != v) {
					t.Fatalf("to64 %v: got %x want %x", v, math.Float64bits(back[i]), math.Float64bits(want))
				}
			}
		}
	})
}
