package dsp

import "math"

// Window types
const (
	WindowHann = iota
	WindowHamming
	WindowBlackman
	WindowSine
	WindowVorbis
)

// Window generates a window function of the specified type and length.
func Window(windowType, length int) []float64 {
	window := make([]float64, length)

	switch windowType {
	case WindowHann:
		hannWindow(window)
	case WindowHamming:
		hammingWindow(window)
	case WindowBlackman:
		blackmanWindow(window)
	case WindowSine:
		sineWindow(window)
	case WindowVorbis:
		vorbisWindow(window)
	default:
		// Default to rectangular (all ones)
		for i := range window {
			window[i] = 1.0
		}
	}

	return window
}

// hannWindow generates a Hann window.
// w(n) = 0.5 * (1 - cos(2πn/(N-1)))
func hannWindow(window []float64) {
	n := len(window)
	for i := 0; i < n; i++ {
		window[i] = 0.5 * (1.0 - math.Cos(2.0*math.Pi*float64(i)/float64(n-1)))
	}
}

// hammingWindow generates a Hamming window.
// w(n) = 0.54 - 0.46 * cos(2πn/(N-1))
func hammingWindow(window []float64) {
	n := len(window)
	for i := 0; i < n; i++ {
		window[i] = 0.54 - 0.46*math.Cos(2.0*math.Pi*float64(i)/float64(n-1))
	}
}

// blackmanWindow generates a Blackman window.
// w(n) = 0.42 - 0.5*cos(2πn/(N-1)) + 0.08*cos(4πn/(N-1))
func blackmanWindow(window []float64) {
	n := len(window)
	for i := 0; i < n; i++ {
		t := float64(i) / float64(n-1)
		window[i] = 0.42 - 0.5*math.Cos(2.0*math.Pi*t) + 0.08*math.Cos(4.0*math.Pi*t)
	}
}

// sineWindow generates a sine window.
// w(n) = sin(π(n+0.5)/N)
func sineWindow(window []float64) {
	n := len(window)
	for i := 0; i < n; i++ {
		window[i] = math.Sin(math.Pi * (float64(i) + 0.5) / float64(n))
	}
}

// vorbisWindow generates a Vorbis window (used in CELT).
// w(n) = sin(π/2 * sin²(π(n+0.5)/N))
func vorbisWindow(window []float64) {
	n := len(window)
	for i := 0; i < n; i++ {
		sine := math.Sin(math.Pi * (float64(i) + 0.5) / float64(n))
		window[i] = math.Sin(0.5 * math.Pi * sine * sine)
	}
}

// ApplyWindow multiplies the input signal by a window function in-place.
func ApplyWindow(signal []float64, window []float64) {
	if len(signal) != len(window) {
		panic("dsp: signal and window must have same length")
	}

	for i := range signal {
		signal[i] *= window[i]
	}
}

// OverlapAdd performs overlap-add operation for windowed frames.
func OverlapAdd(output []float64, input []float64, offset int) {
	if offset < 0 || offset+len(input) > len(output) {
		panic("dsp: overlap-add bounds exceeded")
	}

	for i, v := range input {
		output[offset+i] += v
	}
}

// WindowedOverlapAdd applies window and then performs overlap-add.
func WindowedOverlapAdd(output []float64, input []float64, window []float64, offset int) {
	if len(input) != len(window) {
		panic("dsp: input and window must have same length")
	}
	if offset < 0 || offset+len(input) > len(output) {
		panic("dsp: overlap-add bounds exceeded")
	}

	for i := range input {
		output[offset+i] += input[i] * window[i]
	}
}
