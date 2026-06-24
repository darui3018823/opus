//go:build opusref

package opus

import (
	"math"
	"testing"

	"github.com/darui3018823/opus/internal/cgoref"
)

func TestCGOMultistreamInteroperability(t *testing.T) {
	const (
		rate      = 48000
		channels  = 6
		streams   = 4
		coupled   = 2
		frameSize = 960
	)
	mapping := []byte{0, 4, 1, 2, 3, 5}
	pcm := make([]float32, frameSize*channels)
	for i := 0; i < frameSize; i++ {
		for ch := 0; ch < channels; ch++ {
			pcm[i*channels+ch] = float32(0.25 * math.Sin(2*math.Pi*float64(180+97*ch)*float64(i)/rate))
		}
	}

	goEnc, err := NewMultistreamEncoder(rate, channels, streams, coupled, mapping, ApplicationAudio)
	if err != nil {
		t.Fatal(err)
	}
	goEnc.SetVBR(true)
	goPacket, err := goEnc.EncodeFloat32(pcm, frameSize)
	if err != nil {
		t.Fatal(err)
	}
	refDec, err := cgoref.NewMultistreamDecoder(rate, channels, streams, coupled, mapping)
	if err != nil {
		t.Fatal(err)
	}
	defer refDec.Close()
	refOut, err := refDec.DecodeFloat(goPacket, frameSize)
	if err != nil {
		t.Fatalf("libopus rejected Go multistream packet: %v", err)
	}
	if len(refOut) != len(pcm) {
		t.Fatalf("libopus decoded %d samples, want %d", len(refOut), len(pcm))
	}

	refEnc, err := cgoref.NewMultistreamEncoder(rate, channels, streams, coupled, mapping, ApplicationAudio)
	if err != nil {
		t.Fatal(err)
	}
	defer refEnc.Close()
	refPacket, err := refEnc.Encode(pcm, frameSize)
	if err != nil {
		t.Fatal(err)
	}
	goDec, err := NewMultistreamDecoder(rate, channels, streams, coupled, mapping)
	if err != nil {
		t.Fatal(err)
	}
	goOut, err := goDec.DecodeFloat32(refPacket)
	if err != nil {
		t.Fatalf("Go decoder rejected libopus multistream packet: %v", err)
	}
	if len(goOut) != len(pcm) {
		t.Fatalf("Go decoded %d samples, want %d", len(goOut), len(pcm))
	}
}
