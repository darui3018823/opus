package celt

import "testing"

func TestSpectralTonalitySlopeTracksActiveBandPosition(t *testing.T) {
	const (
		channels = 2
		frameLen = FrameSize20ms
		end      = 19
		lm       = 3
	)
	makeSpectrum := func(active []int) ([]float64, []float64) {
		X := make([]float64, channels*frameLen)
		logE := make([]float64, channels*NumBands48000)
		for i := range logE {
			logE[i] = -20
		}
		M := 1 << lm
		for _, band := range active {
			for c := 0; c < channels; c++ {
				X[c*frameLen+M*int(EBands48000[band])] = 1
				logE[c*NumBands48000+band] = 0
			}
		}
		return X, logE
	}

	lowX, lowLogE := makeSpectrum([]int{0, 1, 2, 3, 4})
	low := spectralTonalitySlope(lowX, lowLogE, NumBands48000, end, lm, channels, frameLen)
	if low >= -0.4 {
		t.Fatalf("low-band tonality slope = %.4f, want below -0.4", low)
	}

	highX, highLogE := makeSpectrum([]int{14, 15, 16, 17, 18})
	high := spectralTonalitySlope(highX, highLogE, NumBands48000, end, lm, channels, frameLen)
	if high <= 0.4 {
		t.Fatalf("high-band tonality slope = %.4f, want above 0.4", high)
	}
}

func TestSpectralTonalitySlopeIgnoresNormalisedFloorBands(t *testing.T) {
	const (
		channels = 2
		frameLen = FrameSize20ms
		end      = 19
		lm       = 3
	)
	X := make([]float64, channels*frameLen)
	logE := make([]float64, channels*NumBands48000)
	for i := range logE {
		logE[i] = -20
	}
	M := 1 << lm
	for c := 0; c < channels; c++ {
		// The only active band is low and tonal.
		X[c*frameLen+M*int(EBands48000[1])] = 1
		logE[c*NumBands48000+1] = 0
		// A normalised but inaudible high-band coefficient must not reverse the
		// slope after the relative-energy gate is applied.
		X[c*frameLen+M*int(EBands48000[18])] = 1
	}
	got := spectralTonalitySlope(X, logE, NumBands48000, end, lm, channels, frameLen)
	if got >= 0 {
		t.Fatalf("tonality slope with high-band floor = %.4f, want negative", got)
	}
}
