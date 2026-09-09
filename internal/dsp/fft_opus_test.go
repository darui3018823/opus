package dsp

import (
	"math"
	"strconv"
	"testing"
)

func TestOpusFFTFactors(t *testing.T) {
	wants := map[int][]int{
		60:  {5, 12, 3, 4, 4, 1},
		120: {5, 24, 3, 8, 2, 4, 4, 1},
		240: {5, 48, 3, 16, 4, 4, 4, 1},
		480: {5, 96, 3, 32, 4, 8, 2, 4, 4, 1},
	}
	for n, want := range wants {
		got := opusFFTFactors(n)
		if len(got) != len(want) {
			t.Fatalf("n=%d factors=%v, want %v", n, got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("n=%d factors=%v, want %v", n, got, want)
			}
		}
	}
}

func TestOpusFFTMatchesDFT(t *testing.T) {
	for _, n := range []int{60, 120, 240, 480} {
		t.Run(strconv.Itoa(n), func(t *testing.T) {
			input := make([]Complex, n)
			for i := range input {
				input[i] = Complex{
					Real: math.Sin(0.17*float64(i)) + 0.25*math.Cos(0.031*float64(i*i)),
					Imag: 0.4 * math.Cos(0.11*float64(i)),
				}
			}
			got := opusFFT(input)
			want := AnyFFT(input)
			maxErr := 0.0
			for i := range got {
				err := math.Hypot(got[i].Real-want[i].Real, got[i].Imag-want[i].Imag)
				if err > maxErr {
					maxErr = err
				}
			}
			if maxErr > 2e-4 {
				t.Fatalf("n=%d maximum FFT error=%g", n, maxErr)
			}
		})
	}
}

func TestOpusFFTMatchesLibopusFloatBits(t *testing.T) {
	wants := map[int]uint64{
		60:  0x8b3a7986ce7cd103,
		120: 0x1414064ac6384bd1,
		240: 0x99ed5feec9b83b4e,
		480: 0x6b5add6a08d83318,
	}
	for _, n := range []int{60, 120, 240, 480} {
		t.Run(strconv.Itoa(n), func(t *testing.T) {
			input := make([]Complex, n)
			for i := range input {
				realPart := float32((i*37)%257-128) * (1.0 / 64.0)
				imagPart := float32((i*73)%251-125) * (1.0 / 128.0)
				input[i] = Complex{Real: float64(realPart), Imag: float64(imagPart)}
			}
			output := opusFFT(input)
			hash := uint64(14695981039346656037)
			for _, value := range output {
				for _, bits := range []uint32{
					math.Float32bits(float32(value.Real)),
					math.Float32bits(float32(value.Imag)),
				} {
					for shift := uint(0); shift < 32; shift += 8 {
						hash ^= uint64(byte(bits >> shift))
						hash *= 1099511628211
					}
				}
			}
			if hash != wants[n] {
				t.Fatalf("float output hash=%016x, libopus=%016x", hash, wants[n])
			}
		})
	}
}
