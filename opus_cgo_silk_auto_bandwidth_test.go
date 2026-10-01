//go:build opusref

package opus_test

import (
	"bytes"
	"testing"

	opus "github.com/darui3018823/opus"
	"github.com/darui3018823/opus/internal/cgoref"
)

// TestCGOEncodeRefSILKAutoBandwidth checks the automatic bandwidth decision
// against the real libopus encoder at low bitrates: below the 9 kbps voice
// threshold libopus codes narrowband (SILK at 8 kHz through the 12/16/48 ->
// 8 kHz encoder resampler), above it wideband, and every packet must match.
func TestCGOEncodeRefSILKAutoBandwidth(t *testing.T) {
	for _, rate := range []int{16000, 48000, 12000} {
		for _, bitrate := range []int{6000, 8000, 9000, 10000, 12000} {
			const channels, nPackets = 1, 12
			frameSize := rate * 20 / 1000
			enc, _ := opus.NewEncoder(rate, channels, opus.ApplicationVOIP)
			enc.SetBitrate(bitrate)
			enc.SetVBR(true)
			enc.SetVBRConstraint(true)
			enc.SetComplexity(5)
			enc.SetSignalType(opus.SignalVoice)
			ref, err := cgoref.NewEncoder(rate, channels, opus.ApplicationVOIP)
			if err != nil {
				t.Fatal(err)
			}
			ref.SetBitrate(bitrate)
			ref.SetComplexity(5)
			ref.SetVoiceMode()
			eq, first := 0, -1
			var firstToc [2]byte
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
					firstToc = [2]byte{a[0], b[0]}
				}
			}
			if eq != nPackets {
				t.Errorf("rate %d bitrate %d: %d/%d packets identical (first diff %d, Go TOC %#x, libopus TOC %#x)", rate, bitrate, eq, nPackets, first, firstToc[0], firstToc[1])
			} else {
				t.Logf("rate %d bitrate %d: all %d packets byte-identical", rate, bitrate, nPackets)
			}
			ref.Close()
		}
	}
}
