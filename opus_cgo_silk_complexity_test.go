//go:build opusref

package opus_test

import (
	"bytes"
	"testing"

	opus "github.com/darui3018823/opus"
	"github.com/darui3018823/opus/internal/cgoref"
)

// TestCGOEncodeRefSILKComplexity encodes 8/12/16 kHz mono voice at 24 kbps
// CVBR at every complexity setting against the real libopus encoder; every
// cell must be byte-identical (complexities 0 and 1 exercise the plain
// silk_NSQ, the others the delayed-decision quantiser with 2-4 states).
func TestCGOEncodeRefSILKComplexity(t *testing.T) {
	for _, rate := range []int{8000, 12000, 16000} {
		for cplx := 0; cplx <= 10; cplx++ {
			const channels, nPackets, bitrate = 1, 12, 24000
			frameSize := rate * 20 / 1000
			enc, _ := opus.NewEncoder(rate, channels, opus.ApplicationVOIP)
			enc.SetBitrate(bitrate)
			enc.SetVBR(true)
			enc.SetVBRConstraint(true)
			enc.SetComplexity(cplx)
			enc.SetSignalType(opus.SignalVoice)
			ref, err := cgoref.NewEncoder(rate, channels, opus.ApplicationVOIP)
			if err != nil {
				t.Fatal(err)
			}
			ref.SetBitrate(bitrate)
			ref.SetComplexity(cplx)
			ref.SetVoiceMode()
			eq, first := 0, -1
			var sizes [2]int
			for p := 0; p < nPackets; p++ {
				in := silkRefSpeechFrame(rate, p*frameSize, frameSize, channels)
				a, _ := enc.EncodeFloat(in, frameSize)
				in32 := make([]float32, len(in))
				for i, v := range in {
					in32[i] = float32(v)
				}
				b, err := ref.Encode(in32, frameSize)
				if err != nil {
					t.Fatal(err)
				}
				if bytes.Equal(a, b) {
					eq++
				} else if first < 0 {
					first = p
					sizes = [2]int{len(a), len(b)}
				}
			}
			if eq == nPackets {
				t.Logf("rate %d complexity %d: all %d packets byte-identical", rate, cplx, nPackets)
			} else {
				t.Errorf("rate %d complexity %d: %d/%d packets identical (first diff %d, sizes Go/C %v)", rate, cplx, eq, nPackets, first, sizes)
			}
			ref.Close()
		}
	}
}
