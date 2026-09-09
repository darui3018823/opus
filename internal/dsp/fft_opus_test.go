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

func TestCLTMDCTBackwardMatchesLibopusFloatBits(t *testing.T) {
	wants := map[int]uint64{
		960: 0x0280683ffd82ab4d,
		480: 0xaf316500239ea5d0,
		240: 0x2b1b707b749b9d80,
		120: 0x2c99f57be8f85c2e,
	}
	window := make([]float32, 120)
	for i := range window {
		inner := math.Sin(0.5 * math.Pi * (float64(i) + 0.5) / 120)
		window[i] = float32(math.Sin(0.5 * math.Pi * inner * inner))
	}
	for _, n := range []int{960, 480, 240, 120} {
		t.Run(strconv.Itoa(n), func(t *testing.T) {
			input := make([]float64, n)
			for i := range input {
				input[i] = float64(float32((i*29)%263-131) * (1.0 / 256.0))
			}
			carry := make([]float64, 120)
			for i := 0; i < 60; i++ {
				carry[i] = float64(float32((i*17)%61-30) * (1.0 / 512.0))
			}
			mode := NewCELTMode(n, 120, window)
			output := mode.CLTMDCTBackward(input, carry)
			output = append(output, carry[:60]...)
			hash := uint64(14695981039346656037)
			for _, value := range output {
				bits := math.Float32bits(float32(value))
				for shift := uint(0); shift < 32; shift += 8 {
					hash ^= uint64(byte(bits >> shift))
					hash *= 1099511628211
				}
			}
			if hash != wants[n] {
				t.Fatalf("float output hash=%016x, libopus=%016x", hash, wants[n])
			}
		})
	}
}
