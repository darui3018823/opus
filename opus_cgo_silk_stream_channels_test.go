//go:build opusref

package opus_test

import (
	"bytes"
	"testing"

	opus "github.com/darui3018823/opus"
	"github.com/darui3018823/opus/internal/cgoref"
)

// Stereo input at rates where libopus codes a mono stream, plus a bitrate
// schedule that forces stereo->mono->stereo transitions.
func TestZZScratchDownmixRef(t *testing.T) {
	type sched struct {
		name  string
		rates []int
	}
	for _, rate := range []int{16000, 8000, 48000} {
		for _, sc := range []sched{
			{"static16k", []int{16000}},
			{"static20k", []int{20000}},
			{"32to16to32", []int{32000, 32000, 32000, 32000, 16000, 16000, 16000, 16000, 16000, 32000, 32000, 32000, 32000, 32000}},
		} {
			const channels, nPackets = 2, 14
			frameSize := rate * 20 / 1000
			enc, _ := opus.NewEncoder(rate, channels, opus.ApplicationVOIP)
			enc.SetVBR(true)
			enc.SetVBRConstraint(true)
			enc.SetComplexity(5)
			enc.SetSignalType(opus.SignalVoice)
			if rate > 16000 {
				enc.SetBandwidth(opus.BandwidthWideband)
			}
			ref, err := cgoref.NewEncoder(rate, channels, opus.ApplicationVOIP)
			if err != nil {
				t.Fatal(err)
			}
			ref.SetComplexity(5)
			ref.SetVoiceMode()
			if rate > 16000 {
				ref.SetBandwidth(opus.BandwidthWideband)
			}
			eq, first := 0, -1
			for p := 0; p < nPackets; p++ {
				br := sc.rates[p%len(sc.rates)]
				enc.SetBitrate(br)
				ref.SetBitrate(br)
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
				} else {
					if first < 0 {
						first = p
					}
					t.Logf("  %s rate %d packet %d (br %d): Go %d bytes toc %#x | libopus %d bytes toc %#x", sc.name, rate, p, br, len(a), a[0], len(b), b[0])
				}
			}
			if eq != nPackets {
				t.Errorf("%s rate %d: %d/%d packets identical (first diff %d)", sc.name, rate, eq, nPackets, first)
			} else {
				t.Logf("%s rate %d: all %d packets byte-identical", sc.name, rate, nPackets)
			}
			ref.Close()
		}
	}
}
