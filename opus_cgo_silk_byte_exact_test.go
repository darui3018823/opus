//go:build opusref

package opus_test

import (
	"bytes"
	"fmt"
	"testing"

	opus "github.com/darui3018823/opus"
	"github.com/darui3018823/opus/internal/cgoref"
)

// TestCGOEncodeRefSILKByteExact encodes the mono and stereo speech fixtures
// with the Go encoder and the real libopus 1.6.1 encoder (cgoref) under
// identical settings — VOIP, constrained VBR, complexity 5, voice signal, with
// and without in-band FEC at 20 % loss — and requires byte-identical packets
// for every cell libopus codes as SILK-only with the same stream channel
// count and bandwidth. Cells where libopus picks another mode (hybrid at
// 32 kbps for 24/48 kHz input), downmixes a stereo stream to mono or narrows
// the bandwidth are the open mode/channel/bandwidth-policy gap and are only
// logged.
func TestCGOEncodeRefSILKByteExact(t *testing.T) {
	const nPackets = 14
	type cell struct{ channels, rate, bitrate int }
	var cells []cell
	for _, rate := range []int{8000, 12000, 16000, 24000, 48000} {
		for _, bitrate := range []int{16000, 24000, 32000} {
			cells = append(cells, cell{1, rate, bitrate})
		}
	}
	for _, rate := range []int{8000, 12000, 16000} {
		for _, bitrate := range []int{16000, 20000, 24000, 32000, 40000, 48000} {
			cells = append(cells, cell{2, rate, bitrate})
		}
	}
	for _, fec := range []bool{false, true} {
		for _, c := range cells {
			channels, rate, bitrate := c.channels, c.rate, c.bitrate
			name := "nofec"
			if fec {
				name = "fec20"
			}
			t.Run(fmt.Sprintf("%s/%dch/%dk/%dkbps", name, channels, rate/1000, bitrate/1000), func(t *testing.T) {
				frameSize := rate * 20 / 1000
				enc, err := opus.NewEncoder(rate, channels, opus.ApplicationVOIP)
				if err != nil {
					t.Fatal(err)
				}
				if err := enc.SetBitrate(bitrate); err != nil {
					t.Fatal(err)
				}
				enc.SetVBR(true)
				enc.SetVBRConstraint(true)
				if err := enc.SetComplexity(5); err != nil {
					t.Fatal(err)
				}
				enc.SetSignalType(opus.SignalVoice)
				ref, err := cgoref.NewEncoder(rate, channels, opus.ApplicationVOIP)
				if err != nil {
					t.Fatal(err)
				}
				defer ref.Close()
				if err := ref.SetBitrate(bitrate); err != nil {
					t.Fatal(err)
				}
				if err := ref.SetComplexity(5); err != nil {
					t.Fatal(err)
				}
				if err := ref.SetVoiceMode(); err != nil {
					t.Fatal(err)
				}
				if fec {
					enc.SetPacketLossPerc(20)
					enc.SetInbandFEC(true)
					if err := ref.SetPacketLossPerc(20); err != nil {
						t.Fatal(err)
					}
					if err := ref.SetInbandFEC(true); err != nil {
						t.Fatal(err)
					}
				}
				for p := 0; p < nPackets; p++ {
					in := silkRefSpeechFrame(rate, p*frameSize, frameSize, channels)
					got, err := enc.EncodeFloat(in, frameSize)
					if err != nil {
						t.Fatalf("packet %d: Go EncodeFloat: %v", p, err)
					}
					in32 := make([]float32, len(in))
					for i, v := range in {
						in32[i] = float32(v)
					}
					want, err := ref.Encode(in32, frameSize)
					if err != nil {
						t.Fatalf("packet %d: libopus encode: %v", p, err)
					}
					refConfig, goConfig := (want[0]>>3)&0x1f, (got[0]>>3)&0x1f
					switch {
					case refConfig >= 12 && refConfig < 16:
						t.Logf("packet %d: libopus codes hybrid (config %d), Go config %d: mode policy not yet mirrored", p, refConfig, goConfig)
						return
					case want[0]&0x04 != got[0]&0x04:
						t.Logf("packet %d: libopus stream channels differ (TOC %#x vs Go %#x): channel policy (stereo->mono downmix) not yet mirrored", p, want[0], got[0])
						return
					case refConfig != goConfig:
						t.Logf("packet %d: libopus bandwidth differs (config %d vs Go %d): bandwidth policy not yet mirrored", p, refConfig, goConfig)
						return
					}
					if !bytes.Equal(got, want) {
						t.Fatalf("packet %d differs from libopus: Go %d bytes (TOC %#x), libopus %d bytes (TOC %#x)", p, len(got), got[0], len(want), want[0])
					}
				}
				t.Logf("all %d packets byte-identical to libopus", nPackets)
			})
		}
	}
}
