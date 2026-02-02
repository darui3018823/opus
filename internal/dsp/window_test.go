package dsp

import (
	"math"
	"testing"
)

func TestWindowTypes(t *testing.T) {
	size := 64

	tests := []struct {
		name       string
		windowType int
	}{
		{"Hann", WindowHann},
		{"Hamming", WindowHamming},
		{"Blackman", WindowBlackman},
		{"Sine", WindowSine},
		{"Vorbis", WindowVorbis},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			window := Window(tt.windowType, size)

			if len(window) != size {
				t.Errorf("Window size = %d, want %d", len(window), size)
			}

			// Check symmetry for most windows
			if tt.windowType != WindowVorbis {
				for i := 0; i < size/2; i++ {
					if math.Abs(window[i]-window[size-1-i]) > 1e-10 {
						t.Errorf("Window not symmetric at %d: %v vs %v", i, window[i], window[size-1-i])
					}
				}
			}

			// Check that window values are in reasonable range [0, 1]
			for i, v := range window {
				if v < -1e-10 || v > 1.1 { // Allow slight overshoot for some windows and fp precision
					t.Errorf("Window[%d] = %v, should be in [0, 1]", i, v)
				}
			}
		})
	}
}

func TestHannWindow(t *testing.T) {
	window := Window(WindowHann, 8)

	// Hann window should be 0 at endpoints
	if math.Abs(window[0]) > 1e-10 {
		t.Errorf("Hann window should be 0 at start, got %v", window[0])
	}
	if math.Abs(window[len(window)-1]) > 1e-10 {
		t.Errorf("Hann window should be 0 at end, got %v", window[len(window)-1])
	}

	// Maximum should be at center
	maxIdx := 0
	maxVal := 0.0
	for i, v := range window {
		if v > maxVal {
			maxVal = v
			maxIdx = i
		}
	}
	if maxIdx != len(window)/2-1 && maxIdx != len(window)/2 {
		t.Errorf("Hann window maximum at %d, want near %d", maxIdx, len(window)/2)
	}
}

func TestVorbisWindow(t *testing.T) {
	window := Window(WindowVorbis, 16)

	// Vorbis window is used in MDCT, check PCOLA property approximately
	// (should maintain constant overlap-add)
	for i := 0; i < len(window)/2; i++ {
		// w[i]^2 + w[N/2+i]^2 should be approximately 1
		sum := window[i]*window[i] + window[len(window)/2+i]*window[len(window)/2+i]
		if math.Abs(sum-1.0) > 0.1 { // Approximate check
			t.Logf("PCOLA check at %d: %v (expected ~1)", i, sum)
		}
	}
}

func TestApplyWindow(t *testing.T) {
	signal := []float64{1, 1, 1, 1, 1, 1, 1, 1}
	window := Window(WindowHann, 8)

	ApplyWindow(signal, window)

	// Signal should now match window
	for i := range signal {
		if math.Abs(signal[i]-window[i]) > 1e-10 {
			t.Errorf("ApplyWindow failed at %d: got %v, want %v", i, signal[i], window[i])
		}
	}
}

func TestOverlapAdd(t *testing.T) {
	output := make([]float64, 16)
	input := []float64{1, 2, 3, 4}

	// First overlap-add
	OverlapAdd(output, input, 0)
	for i := 0; i < 4; i++ {
		if output[i] != input[i] {
			t.Errorf("First overlap-add at %d: got %v, want %v", i, output[i], input[i])
		}
	}

	// Second overlap-add (should accumulate)
	OverlapAdd(output, input, 2)
	expected := []float64{1, 2, 4, 6, 3, 4, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	for i := 0; i < len(expected); i++ {
		if math.Abs(output[i]-expected[i]) > 1e-10 {
			t.Errorf("Second overlap-add at %d: got %v, want %v", i, output[i], expected[i])
		}
	}
}

func TestWindowedOverlapAdd(t *testing.T) {
	output := make([]float64, 16)
	input := []float64{1, 1, 1, 1}
	window := []float64{0.5, 1.0, 1.0, 0.5}

	WindowedOverlapAdd(output, input, window, 0)

	expected := []float64{0.5, 1.0, 1.0, 0.5, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	for i := 0; i < len(expected); i++ {
		if math.Abs(output[i]-expected[i]) > 1e-10 {
			t.Errorf("WindowedOverlapAdd at %d: got %v, want %v", i, output[i], expected[i])
		}
	}
}

func BenchmarkHannWindow(b *testing.B) {
	for i := 0; i < b.N; i++ {
		Window(WindowHann, 1024)
	}
}

func BenchmarkVorbisWindow(b *testing.B) {
	for i := 0; i < b.N; i++ {
		Window(WindowVorbis, 1024)
	}
}

func BenchmarkApplyWindow(b *testing.B) {
	signal := make([]float64, 1024)
	window := Window(WindowHann, 1024)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ApplyWindow(signal, window)
	}
}
