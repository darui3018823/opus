package kernels

import (
	"math"
	"math/rand"
	"testing"
)

// specialFloat32 returns values that stress rounding: subnormals, signed
// zeros, huge values that overflow to infinity when multiplied, and
// ordinary audio-range samples.
func specialFloat32(r *rand.Rand) float32 {
	switch r.Intn(16) {
	case 0:
		return math.Float32frombits(uint32(r.Intn(1 << 23))) // positive subnormal
	case 1:
		return -math.Float32frombits(uint32(r.Intn(1 << 23)))
	case 2:
		return float32(math.Copysign(0, -1))
	case 3:
		return 0
	case 4:
		return float32(r.NormFloat64()) * 1e30
	case 5:
		return float32(r.NormFloat64()) * 1e-20
	default:
		return float32(r.NormFloat64()) * 8192
	}
}

func fillFloat32(r *rand.Rand, s []float32, special bool) {
	for i := range s {
		if special {
			s[i] = specialFloat32(r)
		} else {
			s[i] = float32(r.NormFloat64()) * 4096
		}
	}
}

func sameBits(a, b float32) bool {
	if a != a && b != b {
		// Both NaN. The payload is not part of the contract: when both
		// operands of ADDSS are NaN, x86 returns the first one, and the Go
		// compiler may order a commutative add either way (a -race build
		// of the scalar loop already differs from a normal build).
		return true
	}
	return math.Float32bits(a) == math.Float32bits(b)
}

// withPaths runs f once for every accelerated path available on this
// build, restoring the selection afterwards.
func withPaths(t *testing.T, f func(t *testing.T)) {
	t.Helper()
	t.Run(Features(), f)
	disableAVXForTest(t, f)
}

func checkXcorr(t *testing.T, x, y []float32, length, maxPitch int) {
	t.Helper()
	got := make([]float32, maxPitch+1)
	want := make([]float32, maxPitch+1)
	got[maxPitch] = 12345 // guard: must not be written
	want[maxPitch] = 12345
	PitchXcorr32(x, y, got, length, maxPitch)
	PitchXcorr32Generic(x, y, want, length, maxPitch)
	for i := range want {
		if !sameBits(got[i], want[i]) {
			t.Fatalf("length=%d maxPitch=%d lag %d: got %v (%08x), want %v (%08x)",
				length, maxPitch, i, got[i], math.Float32bits(got[i]), want[i], math.Float32bits(want[i]))
		}
	}
}

func TestPitchXcorr32MatchesGenericAllShapes(t *testing.T) {
	withPaths(t, func(t *testing.T) {
		r := rand.New(rand.NewSource(1))
		for length := 0; length <= 40; length++ {
			for maxPitch := 0; maxPitch <= 75; maxPitch++ {
				for _, special := range []bool{false, true} {
					x := make([]float32, length)
					y := make([]float32, length+maxPitch)
					fillFloat32(r, x, special)
					fillFloat32(r, y, special)
					checkXcorr(t, x, y, length, maxPitch)
				}
			}
		}
	})
}

// TestPitchXcorr32MatchesGenericCodecShapes covers the sizes the CELT
// encoder, the CELT PLC and celtAutocorr32 call it with.
func TestPitchXcorr32MatchesGenericCodecShapes(t *testing.T) {
	shapes := [][2]int{
		{240, 244}, // encoder coarse pitch search (960>>2, 979>>2)
		{332, 155}, // PLC coarse pitch search
		{476, 5},   // pitch_downsample autocorrelation
		{1, 1}, {7, 33}, {256, 256}, {1024, 97},
	}
	withPaths(t, func(t *testing.T) {
		r := rand.New(rand.NewSource(2))
		for _, s := range shapes {
			for trial := 0; trial < 20; trial++ {
				length, maxPitch := s[0], s[1]
				x := make([]float32, length)
				y := make([]float32, length+maxPitch-1)
				fillFloat32(r, x, trial%2 == 1)
				fillFloat32(r, y, trial%2 == 1)
				checkXcorr(t, x, y, length, maxPitch)
			}
		}
	})
}

func TestPitchXcorr32NaNAndInf(t *testing.T) {
	inf := float32(math.Inf(1))
	nan := float32(math.NaN())
	withPaths(t, func(t *testing.T) {
		r := rand.New(rand.NewSource(3))
		for trial := 0; trial < 200; trial++ {
			length := 1 + r.Intn(50)
			maxPitch := 1 + r.Intn(70)
			x := make([]float32, length)
			y := make([]float32, length+maxPitch-1)
			fillFloat32(r, x, true)
			fillFloat32(r, y, true)
			for k := 0; k < 3; k++ {
				switch r.Intn(4) {
				case 0:
					x[r.Intn(len(x))] = inf
				case 1:
					y[r.Intn(len(y))] = -inf
				case 2:
					x[r.Intn(len(x))] = nan
				case 3:
					y[r.Intn(len(y))] = nan
				}
			}
			checkXcorr(t, x, y, length, maxPitch)
		}
	})
}

// TestPitchXcorr32ExactSlices checks that the kernels read no further than
// the documented bounds: y is cut to exactly length+maxPitch-1 elements, so
// an overread past the slice would show up under -race or a fault checker,
// and a wrong pointer step would change the result.
func TestPitchXcorr32ExactSlices(t *testing.T) {
	withPaths(t, func(t *testing.T) {
		r := rand.New(rand.NewSource(4))
		for _, s := range [][2]int{{240, 244}, {13, 47}, {64, 32}, {5, 9}} {
			length, maxPitch := s[0], s[1]
			backing := make([]float32, length+maxPitch+64)
			fillFloat32(r, backing, false)
			x := backing[3 : 3+length : 3+length]
			y := backing[17 : 17+length+maxPitch-1 : 17+length+maxPitch-1]
			checkXcorr(t, x, y, length, maxPitch)
		}
	})
}

func BenchmarkPitchXcorr32(b *testing.B) {
	for _, s := range []struct {
		name             string
		length, maxPitch int
	}{
		{"enc-coarse-240x244", 240, 244},
		{"plc-coarse-332x155", 332, 155},
		{"autocorr-476x5", 476, 5},
	} {
		r := rand.New(rand.NewSource(5))
		x := make([]float32, s.length)
		y := make([]float32, s.length+s.maxPitch)
		out := make([]float32, s.maxPitch)
		fillFloat32(r, x, false)
		fillFloat32(r, y, false)
		b.Run(s.name+"/generic", func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				PitchXcorr32Generic(x, y, out, s.length, s.maxPitch)
			}
		})
		b.Run(s.name+"/"+Features(), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				PitchXcorr32(x, y, out, s.length, s.maxPitch)
			}
		})
		benchNarrowerPaths(b, s.name, func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				PitchXcorr32(x, y, out, s.length, s.maxPitch)
			}
		})
	}
}
