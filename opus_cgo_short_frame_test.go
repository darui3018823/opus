//go:build opusref

package opus_test

import (
	"fmt"
	"math"
	"testing"

	opus "github.com/darui3018823/opus"
	"github.com/darui3018823/opus/internal/cgoref"
)

func TestCGOEncodeRefShortFrames(t *testing.T) {
	const sampleRate = 48000
	for _, channels := range []int{1, 2} {
		for _, frameSize := range []int{120, 240, 480} {
			t.Run(fmt.Sprintf("%dch/%dsamples", channels, frameSize), func(t *testing.T) {
				enc, err := opus.NewEncoder(sampleRate, channels, opus.ApplicationRestrictedLowDelay)
				if err != nil {
					t.Fatal(err)
				}
				pcm := make([]float32, frameSize*channels)
				for i := 0; i < frameSize; i++ {
					s := float32(0.3 * math.Sin(2*math.Pi*1000*float64(i)/sampleRate))
					for c := 0; c < channels; c++ {
						pcm[i*channels+c] = s
					}
				}
				packet, err := enc.EncodeFloat32(pcm, frameSize)
				if err != nil {
					t.Fatal(err)
				}
				dec, err := cgoref.NewDecoder(sampleRate, channels)
				if err != nil {
					t.Fatal(err)
				}
				defer dec.Close()
				out, err := dec.DecodeFloat(packet, frameSize)
				if err != nil {
					t.Fatalf("libopus decode: %v", err)
				}
				if len(out) != frameSize*channels {
					t.Fatalf("libopus decoded %d samples, want %d", len(out), frameSize*channels)
				}
			})
		}
	}
}
