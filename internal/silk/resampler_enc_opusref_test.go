//go:build opusref

package silk

import (
	"math"
	"testing"

	"github.com/darui3018823/opus/internal/cgoref"
)

// TestSILKEncoderResamplerOracle compares the encoder-direction Go resampler
// with libopus silk_resampler(forEnc=1) sample by sample over several 20 ms
// frames, for every Opus-rate to SILK-rate pair the encoder uses.
func TestSILKEncoderResamplerOracle(t *testing.T) {
	pairs := [][2]int{
		{8000, 8000}, {12000, 12000}, {16000, 16000},
		{12000, 8000}, {16000, 8000}, {16000, 12000},
		{24000, 8000}, {24000, 12000}, {24000, 16000},
		{48000, 8000}, {48000, 12000}, {48000, 16000},
	}
	for _, p := range pairs {
		in, out := p[0], p[1]
		g, err := NewEncoderResampler(in, out)
		if err != nil {
			t.Fatalf("%d->%d: %v", in, out, err)
		}
		c, err := cgoref.NewSILKResampler(in, out, true)
		if err != nil {
			t.Fatalf("%d->%d: %v", in, out, err)
		}
		frame := in / 50
		state := uint32(in + out)
		for f := 0; f < 8; f++ {
			pcm := make([]int16, frame)
			for i := range pcm {
				tm := float64(f*frame+i) / float64(in)
				state = state*1664525 + 1013904223
				noise := float64(int32(state)) / 2147483648.0
				v := 9000*math.Sin(2*math.Pi*220*tm) + 3000*math.Sin(2*math.Pi*3100*tm) + 2500*noise
				pcm[i] = int16(math.Max(-32768, math.Min(32767, math.Round(v))))
			}
			got := g.Process(pcm)
			want, err := c.Process(pcm)
			if err != nil {
				t.Fatalf("%d->%d frame %d: %v", in, out, f, err)
			}
			if len(got) != len(want) {
				t.Fatalf("%d->%d frame %d: len Go=%d C=%d", in, out, f, len(got), len(want))
			}
			for i := range got {
				if got[i] != want[i] {
					t.Fatalf("%d->%d frame %d sample %d: Go=%d C=%d", in, out, f, i, got[i], want[i])
				}
			}
		}
		t.Logf("%d->%d: 8 frames exact", in, out)
	}
}
