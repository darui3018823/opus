package celt

import "testing"

// TestDownmixCapOnlyForFloatInput: downmix_float caps the summed channels at
// +6 dBFS (65536), downmix_int24 (opus_encode24) does not.
func TestDownmixCapOnlyForFloatInput(t *testing.T) {
	const n = 480
	x := make([]float64, 2*n)
	for i := range x {
		x[i] = 1.25 // both channels over full scale: the sum is 2.5 * 32768
	}
	run := func(capSignal bool) []float32 {
		var s [3]float32
		var scratch []float32
		// subframe counts 24 kHz samples: n/2 of them cover the n inputs.
		y := make([]float32, n/2)
		downmixAndResample(x, y, &s, n/2, 0, 2, 48000, capSignal, &scratch)
		return y
	}
	capped, uncapped := run(true), run(false)
	if capped[n/8] == uncapped[n/8] {
		t.Fatalf("cap made no difference (%v)", capped[n/8])
	}
	if uncapped[n/8] <= capped[n/8] {
		t.Fatalf("uncapped %v should exceed capped %v", uncapped[n/8], capped[n/8])
	}
}
