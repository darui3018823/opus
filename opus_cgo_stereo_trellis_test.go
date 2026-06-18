//go:build opusref

package opus

import (
	"math"
	"testing"

	"github.com/darui3018823/opus/internal/cgoref"
)

func TestCGOStereoTrellisFinalRange(t *testing.T) {
	const rate = 16000
	const frameSize = rate * 20 / 1000
	enc, err := NewEncoder(rate, 2, ApplicationVOIP)
	if err != nil {
		t.Fatal(err)
	}
	if err := enc.SetBitrate(24000); err != nil {
		t.Fatal(err)
	}
	ref, err := cgoref.NewDecoder(rate, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer ref.Close()
	for frame := 0; frame < 4; frame++ {
		pcm := make([]float64, frameSize*2)
		for i := 0; i < frameSize; i++ {
			v := 0.25 * math.Sin(2*math.Pi*180*float64(frame*frameSize+i)/rate)
			pcm[2*i], pcm[2*i+1] = v, v
		}
		pkt, err := enc.EncodeFloat(pcm, frameSize)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := ref.DecodeFloat(pkt, frameSize); err != nil {
			t.Fatal(err)
		}
		refRange, err := ref.FinalRange()
		if err != nil {
			t.Fatal(err)
		}
		encoderRange := enc.silkEncoder.LastFinalRange()
		if encoderRange != refRange {
			t.Fatalf("frame %d: final range encoder=%08x libopus=%08x (packet=%d bytes)", frame, encoderRange, refRange, len(pkt))
		}
	}
}
