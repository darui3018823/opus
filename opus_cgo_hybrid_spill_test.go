//go:build opusref

package opus

import (
	"testing"

	"github.com/darui3018823/opus/internal/cgoref"
)

// TestHybridCVBROnsetLibopusConsistency guards the hybrid CVBR target-spill
// path: when the VBR SILK low band overshoots the adaptive frame target and
// the encoder emits the actual range-coder size, the stream must stay
// decodable by libopus with the same result as the Go decoder. A divergence
// here means the CELT allocation basis the encoder used no longer matches the
// packet length the decoders derive it from (this exact failure mode was
// observed with a rejected variant of the spill fix).
func TestHybridCVBROnsetLibopusConsistency(t *testing.T) {
	const (
		rate      = 48000
		channels  = 1
		bitrate   = 44000
		frameSize = rate / 50
		frames    = 10
	)
	enc, err := NewEncoder(rate, channels, ApplicationVOIP)
	if err != nil {
		t.Fatal(err)
	}
	if err := enc.SetBitrate(bitrate); err != nil {
		t.Fatal(err)
	}
	enc.SetVBR(true)
	enc.SetVBRConstraint(true)
	enc.SetSignalType(SignalVoice)

	goDec, err := NewDecoder(rate, channels)
	if err != nil {
		t.Fatal(err)
	}
	refDec, err := cgoref.NewDecoder(rate, channels)
	if err != nil {
		t.Fatal(err)
	}
	defer refDec.Close()

	stride := frameSize * channels
	var sumSq float64
	var n int
	for frame := 0; frame < frames; frame++ {
		input := hybridCVBROnsetFixture(frame*frameSize, frameSize)
		packet, err := enc.EncodeFloat(input, frameSize)
		if err != nil {
			t.Fatalf("frame %d encode: %v", frame, err)
		}
		if mode, err := PacketGetMode(packet); err != nil || mode != ModeHybrid {
			t.Fatalf("frame %d mode=%d err=%v, want hybrid", frame, mode, err)
		}
		g := make([]int16, stride)
		if _, err := goDec.Decode(packet, g); err != nil {
			t.Fatalf("frame %d go decode: %v", frame, err)
		}
		r, err := refDec.DecodeFloat(packet, frameSize)
		if err != nil {
			t.Fatalf("frame %d libopus decode: %v", frame, err)
		}
		if len(r) != stride {
			t.Fatalf("frame %d libopus decoded %d samples, want %d", frame, len(r), stride)
		}
		var frameSq float64
		for i := 0; i < stride; i++ {
			d := float64(g[i])/32768.0 - float64(r[i])
			frameSq += d * d
			sumSq += d * d
			n++
		}
		if rmse := frameSq / float64(stride); rmse > 0.001 {
			t.Fatalf("frame %d Go/libopus decode divergence: frame RMSE^2 %.6f", frame, rmse)
		}
	}
	if rmse := sumSq / float64(n); rmse > 0.0005 {
		t.Fatalf("overall Go/libopus decode divergence: RMSE^2 %.6f", rmse)
	}
}
