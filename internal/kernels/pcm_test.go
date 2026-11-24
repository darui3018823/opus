package kernels

import (
	"math"
	"math/rand"
	"os"
	"testing"
)

func pcmTestValues(r *rand.Rand, n int) []float32 {
	edges := []float32{
		0, float32(math.Copysign(0, -1)), 1, -1, 1.0001, -1.0001,
		32767.0 / 32768, -32768.0 / 32768, 32766.5 / 32768, -32767.5 / 32768,
		0.5 / 32768, 1.5 / 32768, 2.5 / 32768, -0.5 / 32768, -1.5 / 32768,
		float32(math.Inf(1)), float32(math.Inf(-1)), float32(math.NaN()),
		-float32(math.NaN()), math.MaxFloat32, -math.MaxFloat32,
		math.Float32frombits(1), -math.Float32frombits(1),
	}
	s := make([]float32, n)
	for i := range s {
		switch r.Intn(4) {
		case 0:
			s[i] = edges[r.Intn(len(edges))]
		case 1: // exact half-LSB points exercise round-half-even
			s[i] = float32(r.Intn(65536)-32768) / 32768
			s[i] += float32(0.5) / 32768
		default:
			s[i] = float32(r.NormFloat64() * 0.5)
		}
	}
	return s
}

func TestFloat32ToInt16MatchesGeneric(t *testing.T) {
	withPaths(t, func(t *testing.T) {
		r := rand.New(rand.NewSource(20))
		for trial := 0; trial < 2000; trial++ {
			n := r.Intn(100)
			src := pcmTestValues(r, n)
			want := make([]int16, n)
			got := make([]int16, n+1)
			got[n] = 0x5a5a
			Float32ToInt16Generic(want, src)
			Float32ToInt16(got[:n], src)
			for i := 0; i < n; i++ {
				if got[i] != want[i] {
					t.Fatalf("trial %d sample %d (%v, %08x): got %d, want %d",
						trial, i, src[i], math.Float32bits(src[i]), got[i], want[i])
				}
			}
			if got[n] != 0x5a5a {
				t.Fatalf("trial %d: wrote past dst", trial)
			}
		}
	})
}

// TestFloat32ToInt16AllFloat32 sweeps one 64Ki block of float32 bit
// patterns in every 61. OPUS_KERNELS_EXHAUSTIVE=1 checks all 2^32.
func TestFloat32ToInt16AllFloat32(t *testing.T) {
	if testing.Short() {
		t.Skip("float32 sweep")
	}
	stride := uint64(61)
	if os.Getenv("OPUS_KERNELS_EXHAUSTIVE") == "1" {
		stride = 1
	}
	withPaths(t, func(t *testing.T) {
		const chunk = 1 << 16
		src := make([]float32, chunk)
		got := make([]int16, chunk)
		want := make([]int16, chunk)
		for base := uint64(0); base < 1<<32; base += chunk * stride {
			for i := range src {
				src[i] = math.Float32frombits(uint32(base) + uint32(i))
			}
			Float32ToInt16(got, src)
			Float32ToInt16Generic(want, src)
			for i := range src {
				if got[i] != want[i] {
					t.Fatalf("bits %08x: got %d, want %d", math.Float32bits(src[i]), got[i], want[i])
				}
			}
		}
	})
}

func BenchmarkFloat32ToInt16(b *testing.B) {
	r := rand.New(rand.NewSource(21))
	src := make([]float32, 1920) // 20 ms stereo at 48 kHz
	for i := range src {
		src[i] = float32(r.NormFloat64() * 0.3)
	}
	dst := make([]int16, len(src))
	b.Run("generic", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			Float32ToInt16Generic(dst, src)
		}
	})
	b.Run(Features(), func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			Float32ToInt16(dst, src)
		}
	})
}
