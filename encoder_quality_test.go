package opus

import (
	"math"
	"testing"
)

// TestEncoderRoundTripAlignedSNR measures the encoder->decoder reconstruction
// quality after compensating for the codec's algorithmic delay. The baseline
// harness (encoder_roundtrip_test.go) compares out[i]-in[i] with no alignment,
// which understates quality because the MDCT analysis/synthesis introduces a
// fixed sample delay (a delayed sine looks decorrelated). Here we search for the
// best integer delay over a steady middle region and report the aligned SNR.
func TestEncoderRoundTripAlignedSNR(t *testing.T) {
	const sampleRate = 48000
	const frameSize = 960
	const nFrames = 16

	cases := []struct {
		name     string
		channels int
		gen      func(n, ch int) []float64
		minSNR   float64
	}{
		{"sine440-mono", 1, func(n, ch int) []float64 {
			out := make([]float64, frameSize*ch)
			for i := 0; i < frameSize; i++ {
				s := 0.5 * math.Sin(2*math.Pi*440*float64(n+i)/sampleRate)
				out[i] = s
			}
			return out
		}, 30},
		{"sine1k-mono", 1, func(n, ch int) []float64 {
			out := make([]float64, frameSize*ch)
			for i := 0; i < frameSize; i++ {
				out[i] = 0.5 * math.Sin(2*math.Pi*1000*float64(n+i)/sampleRate)
			}
			return out
		}, 30},
		{"sine4k-mono", 1, func(n, ch int) []float64 {
			out := make([]float64, frameSize*ch)
			for i := 0; i < frameSize; i++ {
				out[i] = 0.5 * math.Sin(2*math.Pi*4000*float64(n+i)/sampleRate)
			}
			return out
		}, 24},
		{"sine1k-stereo", 2, func(n, ch int) []float64 {
			out := make([]float64, frameSize*ch)
			for i := 0; i < frameSize; i++ {
				s := 0.5 * math.Sin(2*math.Pi*1000*float64(n+i)/sampleRate)
				out[i*ch] = s
				out[i*ch+1] = s
			}
			return out
		}, 28},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			enc, err := NewEncoder(sampleRate, tc.channels, ApplicationAudio)
			if err != nil {
				t.Fatal(err)
			}
			dec, err := NewDecoder(sampleRate, tc.channels)
			if err != nil {
				t.Fatal(err)
			}
			var in, out []float64
			for f := 0; f < nFrames; f++ {
				frame := tc.gen(f*frameSize, tc.channels)
				pkt, err := enc.EncodeFloat(frame, frameSize)
				if err != nil {
					t.Fatalf("frame %d: %v", f, err)
				}
				dout, err := dec.DecodeFloat(pkt)
				if err != nil {
					t.Fatalf("frame %d: %v", f, err)
				}
				in = append(in, frame...)
				out = append(out, dout...)
			}

			// Use channel 0 only, over a steady middle region.
			ch := tc.channels
			lo, hi := 4*frameSize, 12*frameSize
			bestErr, bestScale, bestDelay := math.Inf(1), 1.0, 0
			for d := 0; d <= 3*frameSize; d++ {
				var dot, e2out float64
				for i := lo; i < hi; i++ {
					oi := i - d
					if oi < 0 || oi*ch >= len(out) {
						continue
					}
					a := in[i*ch]
					b := out[oi*ch]
					dot += a * b
					e2out += b * b
				}
				if e2out == 0 {
					continue
				}
				scale := dot / e2out
				var e float64
				for i := lo; i < hi; i++ {
					oi := i - d
					if oi < 0 || oi*ch >= len(out) {
						continue
					}
					r := in[i*ch] - scale*out[oi*ch]
					e += r * r
				}
				e = math.Sqrt(e / float64(hi-lo))
				if e < bestErr {
					bestErr, bestScale, bestDelay = e, scale, d
				}
			}
			var inRMS float64
			for i := lo; i < hi; i++ {
				inRMS += in[i*ch] * in[i*ch]
			}
			inRMS = math.Sqrt(inRMS / float64(hi-lo))
			snr := 20 * math.Log10(inRMS/bestErr)
			t.Logf("%s: alignedSNR=%.2fdB delay=%d scale=%.4f inRMS=%.4f errRMS=%.5f",
				tc.name, snr, bestDelay, bestScale, inRMS, bestErr)
			if snr < tc.minSNR {
				t.Errorf("%s: aligned SNR %.2fdB below %.2fdB", tc.name, snr, tc.minSNR)
			}
		})
	}
}
