//go:build opusref

package opus

import (
	"math"
	"testing"

	"github.com/darui3018823/opus/internal/cgoref"
)

// TestCGOCELTAllocTrimGateAfterBoosts decodes libopus CBR packets whose
// dynalloc boosts consume most of the budget (a lone tone in a wide high
// band). libopus then skips the alloc_trim symbol because the gate compares
// the fractional tell with the budget left after the boosts; reading it
// desynchronizes the range decoder.
func TestCGOCELTAllocTrimGateAfterBoosts(t *testing.T) {
	const (
		rate      = 48000
		frameSize = 960
	)
	for _, tc := range []struct {
		freq    float64
		bitrate int
	}{{19000, 32000}, {17000, 24000}, {19000, 48000}, {15000, 16000}} {
		enc, err := cgoref.NewEncoder(rate, 1, int(ApplicationAudio))
		if err != nil {
			t.Fatal(err)
		}
		if err := enc.SetVBR(false); err != nil {
			t.Fatal(err)
		}
		if err := enc.SetBitrate(tc.bitrate); err != nil {
			t.Fatal(err)
		}
		if err := enc.SetMusicMode(); err != nil {
			t.Fatal(err)
		}
		ref, err := cgoref.NewDecoder(rate, 1)
		if err != nil {
			t.Fatal(err)
		}
		dec, err := NewDecoder(rate, 1)
		if err != nil {
			t.Fatal(err)
		}
		for f := 0; f < 40; f++ {
			pcm := make([]float32, frameSize)
			for i := range pcm {
				pcm[i] = float32(0.5 * math.Sin(2*math.Pi*tc.freq*float64(f*frameSize+i)/rate))
			}
			packet, err := enc.Encode(pcm, frameSize)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := ref.DecodeFloat(packet, frameSize); err != nil {
				t.Fatal(err)
			}
			if _, err := dec.DecodeFloat(packet); err != nil {
				t.Fatal(err)
			}
			want, err := ref.FinalRange()
			if err != nil {
				t.Fatal(err)
			}
			if got := dec.FinalRange(); got != want {
				t.Fatalf("%g Hz %d bps frame %d: final range %08x, libopus %08x", tc.freq, tc.bitrate, f, got, want)
			}
		}
		enc.Close()
		ref.Close()
	}
}
