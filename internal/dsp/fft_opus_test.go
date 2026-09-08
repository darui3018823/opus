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
