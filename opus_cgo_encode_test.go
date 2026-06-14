//go:build opusref

package opus_test

// CGO encoder cross-validation: encodes synthetic signals with the pure-Go
// ENCODER, decodes the resulting packets with libopus (via CGO), and measures
// the delay-aligned reconstruction SNR. This proves the encoder emits
// standard-compliant Opus streams that an independent reference decoder accepts
// and reconstructs correctly — a stronger guarantee than the self-decoder
// round-trip (which our own decoder might pass even on a non-conformant stream).
//
// Run with (PowerShell, needs gcc + libopus):
//   go test -tags opusref -run TestCGOEncodeRef .

import (
	"math"
	"testing"

	opus "github.com/darui3018823/opus"
	"github.com/darui3018823/opus/internal/cgoref"
)

// alignedSNR searches for the best integer delay (and gain) of out vs in over a
// steady middle region of channel 0, returning the resulting SNR in dB. The CELT
// MDCT analysis/synthesis introduces a fixed sample delay, so a naive sample-wise
// comparison understates quality; this mirrors TestEncoderRoundTripAlignedSNR.
func alignedSNR(in, out []float64, ch, frameSize int) (snr float64, delay int, scale float64) {
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
		sc := dot / e2out
		var e float64
		for i := lo; i < hi; i++ {
			oi := i - d
			if oi < 0 || oi*ch >= len(out) {
				continue
			}
			r := in[i*ch] - sc*out[oi*ch]
			e += r * r
		}
		e = math.Sqrt(e / float64(hi-lo))
		if e < bestErr {
			bestErr, bestScale, bestDelay = e, sc, d
		}
	}
	var inRMS float64
	for i := lo; i < hi; i++ {
		inRMS += in[i*ch] * in[i*ch]
	}
	inRMS = math.Sqrt(inRMS / float64(hi-lo))
	return 20 * math.Log10(inRMS/bestErr), bestDelay, bestScale
}

func TestCGOEncodeRef(t *testing.T) {
	t.Logf("libopus version: %s", cgoref.Version())

	const sampleRate = 48000
	const frameSize = 960
	const nFrames = 16
	const maxSPC = 5760

	cases := []struct {
		name     string
		channels int
		minSNR   float64
		gen      func(n, ch int) []float64
	}{
		{"sine440-mono", 1, 24, func(n, ch int) []float64 {
			out := make([]float64, frameSize*ch)
			for i := 0; i < frameSize; i++ {
				out[i] = 0.5 * math.Sin(2*math.Pi*440*float64(n+i)/sampleRate)
			}
			return out
		}},
		{"sine1k-mono", 1, 24, func(n, ch int) []float64 {
			out := make([]float64, frameSize*ch)
			for i := 0; i < frameSize; i++ {
				out[i] = 0.5 * math.Sin(2*math.Pi*1000*float64(n+i)/sampleRate)
			}
			return out
		}},
		{"sine4k-mono", 1, 18, func(n, ch int) []float64 {
			out := make([]float64, frameSize*ch)
			for i := 0; i < frameSize; i++ {
				out[i] = 0.5 * math.Sin(2*math.Pi*4000*float64(n+i)/sampleRate)
			}
			return out
		}},
		{"sine1k-stereo", 2, 22, func(n, ch int) []float64 {
			out := make([]float64, frameSize*ch)
			for i := 0; i < frameSize; i++ {
				s := 0.5 * math.Sin(2*math.Pi*1000*float64(n+i)/sampleRate)
				out[i*ch], out[i*ch+1] = s, s
			}
			return out
		}},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			enc, err := opus.NewEncoder(sampleRate, tc.channels, opus.ApplicationAudio)
			if err != nil {
				t.Fatalf("NewEncoder: %v", err)
			}
			ref, err := cgoref.NewDecoder(sampleRate, tc.channels)
			if err != nil {
				t.Fatalf("cgoref.NewDecoder: %v", err)
			}
			defer ref.Close()

			var in, out []float64
			for f := 0; f < nFrames; f++ {
				frame := tc.gen(f*frameSize, tc.channels)
				pkt, err := enc.EncodeFloat(frame, frameSize)
				if err != nil {
					t.Fatalf("frame %d: EncodeFloat: %v", f, err)
				}
				refOut, err := ref.DecodeFloat(pkt, maxSPC)
				if err != nil {
					t.Fatalf("frame %d: libopus decode (encoder emitted a non-conformant packet): %v", f, err)
				}
				in = append(in, frame...)
				for _, v := range refOut {
					out = append(out, float64(v))
				}
			}

			snr, delay, scale := alignedSNR(in, out, tc.channels, frameSize)
			t.Logf("%s: libopus-decoded alignedSNR=%.2fdB delay=%d scale=%.4f", tc.name, snr, delay, scale)
			if snr < tc.minSNR {
				t.Errorf("%s: libopus-decoded aligned SNR %.2fdB below %.2fdB", tc.name, snr, tc.minSNR)
			}
		})
	}
}

// TestCGOEncodeRefSilence checks that a silent input encoded by our encoder
// decodes to (near) silence in libopus — i.e. the silence/DTX path produces a
// conformant packet, not garbage that a reference decoder turns into noise.
func TestCGOEncodeRefSilence(t *testing.T) {
	const sampleRate = 48000
	const frameSize = 960
	const maxSPC = 5760

	for _, ch := range []int{1, 2} {
		enc, err := opus.NewEncoder(sampleRate, ch, opus.ApplicationAudio)
		if err != nil {
			t.Fatalf("NewEncoder: %v", err)
		}
		ref, err := cgoref.NewDecoder(sampleRate, ch)
		if err != nil {
			t.Fatalf("cgoref.NewDecoder: %v", err)
		}
		silent := make([]float64, frameSize*ch)
		var peak float64
		for f := 0; f < 4; f++ {
			pkt, err := enc.EncodeFloat(silent, frameSize)
			if err != nil {
				t.Fatalf("ch=%d frame=%d: EncodeFloat: %v", ch, f, err)
			}
			refOut, err := ref.DecodeFloat(pkt, maxSPC)
			if err != nil {
				t.Fatalf("ch=%d frame=%d: libopus decode: %v", ch, f, err)
			}
			for _, v := range refOut {
				if a := math.Abs(float64(v)); a > peak {
					peak = a
				}
			}
		}
		ref.Close()
		if peak > 1e-3 {
			t.Errorf("ch=%d: silent input decoded by libopus to non-silence (peak=%g)", ch, peak)
		}
	}
}
