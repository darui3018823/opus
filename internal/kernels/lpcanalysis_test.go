package kernels

import (
	"math/rand"
	"testing"
)

func TestLPCAnalysisFilterMatchesGeneric(t *testing.T) {
	withPaths(t, func(t *testing.T) {
		r := rand.New(rand.NewSource(90))
		for trial := 0; trial < 5000; trial++ {
			extreme := trial%4 == 0
			order := []int{10, 16, 1 + r.Intn(16)}[trial%3]
			length := r.Intn(lpcAnalysisMaxLen + 40)
			if trial%7 == 0 {
				length = order + r.Intn(8) // tails and length < order+4
			}
			in := make([]int32, length)
			for i := range in {
				in[i] = randInt32(r, extreme)
			}
			b := make([]int16, order)
			for i := range b {
				b[i] = randInt16(r, extreme)
			}
			got := make([]int16, length+3)
			want := make([]int16, length+3)
			for i := range got {
				got[i], want[i] = 7, 7
			}
			LPCAnalysisFilter(got, in, b, length, order)
			LPCAnalysisFilterGeneric(want, in, b, length, order)
			for i := range got {
				if got[i] != want[i] {
					t.Fatalf("trial %d order %d length %d: out[%d] got %d want %d", trial, order, length, i, got[i], want[i])
				}
			}
		}
	})
}

// TestLPCAnalysisFilterWrapsAndSaturates drives the int32 sum through
// wrap-around and the result into both saturation limits.
func TestLPCAnalysisFilterWrapsAndSaturates(t *testing.T) {
	withPaths(t, func(t *testing.T) {
		for _, order := range []int{10, 16} {
			const length = 96
			b := make([]int16, order)
			for i := range b {
				b[i] = -32768
			}
			for _, fill := range []int32{-32768, 32767, -1 << 31, 1<<31 - 1, 0x7fff8000} {
				in := make([]int32, length)
				for i := range in {
					in[i] = fill
					if i%3 == 0 {
						in[i] = -fill
					}
				}
				got := make([]int16, length)
				want := make([]int16, length)
				LPCAnalysisFilter(got, in, b, length, order)
				LPCAnalysisFilterGeneric(want, in, b, length, order)
				if string(int16Bytes(got)) != string(int16Bytes(want)) {
					t.Fatalf("order %d fill %d: got %v want %v", order, fill, got, want)
				}
			}
		}
	})
}

func int16Bytes(s []int16) []byte {
	out := make([]byte, 2*len(s))
	for i, v := range s {
		out[2*i], out[2*i+1] = byte(v), byte(v>>8)
	}
	return out
}

func BenchmarkLPCAnalysisFilter(b *testing.B) {
	r := rand.New(rand.NewSource(91))
	in := make([]int32, 400)
	for i := range in {
		in[i] = int32(int16(r.Intn(65536) - 32768))
	}
	coef := make([]int16, 16)
	for i := range coef {
		coef[i] = int16(r.Intn(8192) - 4096)
	}
	out := make([]int16, len(in))
	run := func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			LPCAnalysisFilter(out, in, coef, len(in), 16)
		}
	}
	b.Run(Features(), run)
	b.Run("generic", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			LPCAnalysisFilterGeneric(out, in, coef, len(in), 16)
		}
	})
}
