//go:build opusref

package opus

import (
	"math"
	"testing"

	"github.com/darui3018823/opus/internal/cgoref"
)

// TestCGOMultistreamMaxPacketSize checks that SetMaxPacketSize acts as the
// max_data_bytes of opus_multistream_encode: a CBR packet at BitrateMax is
// padded to it (instead of an unbounded size) and a too small buffer is
// rejected.
func TestCGOMultistreamMaxPacketSize(t *testing.T) {
	const (
		rate      = 48000
		channels  = 3
		frameSize = 960
	)
	mapping := []byte{0, 1, 2}
	for _, maxBytes := range []int{1500, 4000, 400} {
		enc, err := NewMultistreamEncoder(rate, channels, 2, 1, mapping, ApplicationAudio)
		if err != nil {
			t.Fatal(err)
		}
		if err := enc.SetModePolicy(ModePolicyLibopus); err != nil {
			t.Fatal(err)
		}
		enc.SetVBR(false)
		if err := enc.SetBitrate(BitrateMax); err != nil {
			t.Fatal(err)
		}
		if err := enc.SetMaxPacketSize(maxBytes); err != nil {
			t.Fatal(err)
		}
		ref, err := cgoref.NewMultistreamEncoder(rate, channels, 2, 1, mapping, int(ApplicationAudio))
		if err != nil {
			t.Fatal(err)
		}
		if err := ref.SetVBR(false); err != nil {
			t.Fatal(err)
		}
		if err := ref.SetBitrate(-1); err != nil { // OPUS_BITRATE_MAX
			t.Fatal(err)
		}
		for f := 0; f < 5; f++ {
			pcm := make([]float32, frameSize*channels)
			for i := range pcm {
				pcm[i] = float32(0.3 * math.Sin(float64(f*frameSize*channels+i)*0.013))
			}
			got, err := enc.EncodeFloat32(pcm, frameSize)
			if err != nil {
				t.Fatal(err)
			}
			want, err := ref.EncodeMax(pcm, frameSize, maxBytes)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != len(want) {
				t.Fatalf("max %d frame %d: %d bytes, libopus %d", maxBytes, f, len(got), len(want))
			}
		}
		ref.Close()
	}
	enc, err := NewMultistreamEncoder(rate, channels, 2, 1, mapping, ApplicationAudio)
	if err != nil {
		t.Fatal(err)
	}
	if err := enc.SetModePolicy(ModePolicyLibopus); err != nil {
		t.Fatal(err)
	}
	if err := enc.SetMaxPacketSize(2); err != nil {
		t.Fatal(err)
	}
	if _, err := enc.EncodeFloat32(make([]float32, frameSize*channels), frameSize); err == nil {
		t.Fatal("2-byte buffer for 2 streams accepted")
	}
}
