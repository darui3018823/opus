package celt

import (
	"math"
	"testing"

	"github.com/darui3018823/opus/internal/dsp"
)

// TestMDCTShortLongScaleOffset locks the per-band log2-amplitude offset between
// the short-block band energy (used for bandLogE on transient frames) and the
// long-block band energy (the basis for bandLogE2). libopus compensates the
// short-vs-long MDCT scaling by adding +LM/2 (HALF32(SHL32(LM,DB_SHIFT))) to its
// long-block bandLogE2. This test confirms the same correction aligns this Go
// MDCT pair: the measured average of (short - long) must be ≈ LM/2, so adding
// +LM/2 to the long block reproduces the short-block scale.
func TestMDCTShortLongScaleOffset(t *testing.T) {
	const sr = 48000
	mode := NewMode(FrameSize20ms, sr, 1)
	frameSize := mode.FrameSize // 960
	ov := mode.Overlap          // 120
	nBase := mode.NBase         // 120
	lm := mode.LM               // 3
	M := 1 << uint(lm)          // 8
	numBands := mode.Bands.NumBands

	win := celtWindow(ov)
	longMode := dsp.NewCELTMode(frameSize, ov, win)
	shortMode := dsp.NewCELTMode(ov, ov, win)

	// Build an analysis buffer [overlap || frame] of a broadband signal so every
	// band has energy, plus a transient burst in the middle of the frame.
	buf := make([]float64, frameSize+ov)
	for i := range buf {
		x := 0.0
		for _, f := range []float64{440, 1000, 3000, 7000, 12000} {
			x += math.Sin(2 * math.Pi * f * float64(i) / sr)
		}
		if i >= ov+400 && i < ov+440 {
			x += 4.0
		}
		buf[i] = x * 32768.0 / 5.0
	}

	bandLog := func(coeffs []float64) []float64 {
		out := make([]float64, numBands)
		for b := range numBands {
			lo := M * int(EBands48000[b])
			hi := M * int(EBands48000[b+1])
			sumsq := 1e-27
			for j := lo; j < hi; j++ {
				sumsq += coeffs[j] * coeffs[j]
			}
			out[b] = math.Log2(math.Sqrt(sumsq)) - EMean(b)
		}
		return out
	}

	longCoeffs := longMode.CLTMDCTForward(buf)
	bandLogLong := bandLog(longCoeffs)

	shortCoeffs := make([]float64, frameSize)
	for b := range M {
		sub := buf[b*nBase : b*nBase+nBase+ov]
		sc := shortMode.CLTMDCTForward(sub)
		for i := range nBase {
			shortCoeffs[b+i*M] = sc[i]
		}
	}
	bandLogShort := bandLog(shortCoeffs)

	var sumDiff float64
	for b := range numBands {
		sumDiff += bandLogShort[b] - bandLogLong[b]
	}
	avg := sumDiff / float64(numBands)
	want := float64(lm) / 2

	t.Logf("LM=%d  avg(short-long)=%.4f   want LM/2=%.1f", lm, avg, want)
	if math.Abs(avg-want) > 0.25 {
		t.Fatalf("short-vs-long band-energy offset %.4f is not ≈ LM/2 (%.1f); the "+
			"+LM/2 bandLogE2 correction would be miscalibrated", avg, want)
	}
}
