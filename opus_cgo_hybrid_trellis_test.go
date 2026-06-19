//go:build opusref

package opus

import (
	"math"
	"testing"

	"github.com/darui3018823/opus/internal/cgoref"
)

func TestCGOHybridTrellisFinalRange(t *testing.T) {
	const (
		rate      = 24000
		frameSize = rate * 20 / 1000
	)
	enc, err := NewEncoder(rate, 1, ApplicationVOIP)
	if err != nil {
		t.Fatal(err)
	}
	if err := enc.SetBitrate(64000); err != nil {
		t.Fatal(err)
	}
	dec, err := NewDecoder(rate, 1)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := cgoref.NewDecoder(rate, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer ref.Close()

	var oursEnergy, refEnergy float64
	for frame := 0; frame < 8; frame++ {
		pcm := hybridTrellisFixture(rate, frame*frameSize, frameSize)
		packet, err := enc.EncodeFloat(pcm, frameSize)
		if err != nil {
			t.Fatalf("frame %d encode: %v", frame, err)
		}
		ours, err := dec.DecodeFloat(packet)
		if err != nil {
			t.Fatalf("frame %d decode: %v", frame, err)
		}
		refOut, err := ref.DecodeFloat(packet, frameSize)
		if err != nil {
			t.Fatalf("frame %d libopus decode: %v", frame, err)
		}
		refRange, err := ref.FinalRange()
		if err != nil {
			t.Fatal(err)
		}
		if encoderRange := enc.celtEncoder.FinalRange(); encoderRange != refRange {
			t.Fatalf("frame %d encoder/libopus final range: %08x != %08x", frame, encoderRange, refRange)
		}
		if decoderRange := dec.lastCeltDec.LastFinalRange(); decoderRange != refRange {
			t.Fatalf("frame %d decoder/libopus final range: %08x != %08x", frame, decoderRange, refRange)
		}
		for i := range ours {
			oursEnergy += ours[i] * ours[i]
			v := float64(refOut[i])
			refEnergy += v * v
		}
	}
	ratio := math.Sqrt(oursEnergy / refEnergy)
	t.Logf("decoder/libopus RMS ratio=%g", ratio)
	if ratio < 0.5 || ratio > 2.0 {
		t.Fatalf("decoder/libopus RMS ratio=%g", ratio)
	}
}

func hybridTrellisFixture(rate, start, n int) []float64 {
	out := make([]float64, n)
	for i := 0; i < n; i++ {
		x := float64(start+i) / float64(rate)
		env := 0.55 + 0.35*math.Sin(2*math.Pi*3*x)
		out[i] = env * (0.32*math.Sin(2*math.Pi*180*x) +
			0.12*math.Sin(2*math.Pi*360*x+0.4) +
			0.06*math.Sin(2*math.Pi*720*x+0.9) +
			0.025*math.Sin(2*math.Pi*1100*x+1.7))
		out[i] += 0.035 * math.Sin(2*math.Pi*10000*x+0.3)
	}
	return out
}
