package opus

import (
	"math"
	"testing"
)

// TestEncoderSpectralPurity is a delay-invariant quality probe: it encodes a
// pure tone, decodes it, and measures what fraction of the decoded signal's
// power sits at the input frequency (via a Goertzel-style projection). A correct
// tonal reconstruction concentrates nearly all power at the tone; folding/noise
// or a wrong PVQ shape spreads it out.
func TestEncoderSpectralPurity(t *testing.T) {
	const sampleRate = 48000
	const frameSize = 960
	const nFrames = 24

	for _, freq := range []float64{440, 1000, 2000, 4000, 8000} {
		freq := freq
		t.Run("", func(t *testing.T) {
			enc, _ := NewEncoder(sampleRate, 1, ApplicationAudio)
			dec, _ := NewDecoder(sampleRate, 1)
			var out []float64
			for f := 0; f < nFrames; f++ {
				frame := make([]float64, frameSize)
				for i := 0; i < frameSize; i++ {
					frame[i] = 0.5 * math.Sin(2*math.Pi*freq*float64(f*frameSize+i)/sampleRate)
				}
				pkt, err := enc.EncodeFloat(frame, frameSize)
				if err != nil {
					t.Fatal(err)
				}
				d, err := dec.DecodeFloat(pkt)
				if err != nil {
					t.Fatal(err)
				}
				out = append(out, d...)
			}
			// Steady region.
			seg := out[8*frameSize : 20*frameSize]
			var totPow float64
			var re, im float64
			for n, x := range seg {
				totPow += x * x
				ang := 2 * math.Pi * freq * float64(8*frameSize+n) / sampleRate
				re += x * math.Cos(ang)
				im += x * math.Sin(ang)
			}
			tonePow := 2 * (re*re + im*im) / float64(len(seg))
			frac := 0.0
			if totPow > 0 {
				frac = tonePow / totPow
			}
			t.Logf("freq=%.0f tonePow/totPow=%.3f totRMS=%.4f", freq, frac, math.Sqrt(totPow/float64(len(seg))))
		})
	}
}
