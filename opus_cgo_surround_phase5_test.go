//go:build opusref

package opus

import (
	"testing"

	"github.com/darui3018823/opus/internal/cgoref"
)

func TestOpusSurroundPhase5Scoreboard(t *testing.T) {
	const (
		rate      = 48000
		frameSize = 960
		frames    = 18
	)
	for _, tc := range []struct {
		name     string
		channels int
		bitrate  int
		kind     surroundFixtureKind
	}{
		{"5.1-role-rich", 6, 256000, surroundFixtureRoleRich},
		{"5.1-silent-rear", 6, 192000, surroundFixtureSilentRear},
		{"7.1-role-rich", 8, 320000, surroundFixtureRoleRich},
		{"7.1-duplicate-sides", 8, 256000, surroundFixtureDuplicateSides},
	} {
		t.Run(tc.name, func(t *testing.T) {
			goEnc, err := NewSurroundEncoder(rate, tc.channels, MappingFamilyVorbis, ApplicationAudio)
			if err != nil {
				t.Fatal(err)
			}
			goEnc.SetVBR(true)
			goEnc.SetVBRConstraint(true)
			if err := goEnc.SetBitrate(tc.bitrate); err != nil {
				t.Fatal(err)
			}
			refEnc, err := cgoref.NewAmbisonicsMultistreamEncoder(rate, tc.channels, MappingFamilyVorbis, ApplicationAudio)
			if err != nil {
				t.Fatal(err)
			}
			defer refEnc.Close()
			if err := refEnc.SetBitrate(tc.bitrate); err != nil {
				t.Fatal(err)
			}
			if err := refEnc.SetVBR(true); err != nil {
				t.Fatal(err)
			}
			if err := refEnc.SetVBRConstraint(true); err != nil {
				t.Fatal(err)
			}

			goRefDec, err := cgoref.NewMultistreamDecoder(rate, tc.channels, goEnc.Streams(), goEnc.CoupledStreams(), goEnc.Mapping())
			if err != nil {
				t.Fatal(err)
			}
			defer goRefDec.Close()
			refGoDec, err := NewSurroundDecoder(rate, tc.channels, MappingFamilyVorbis)
			if err != nil {
				t.Fatal(err)
			}

			var input, goOut, refOut []float64
			goStreamBytes := make([]int, goEnc.Streams())
			refStreamBytes := make([]int, goEnc.Streams())
			for frame := 0; frame < frames; frame++ {
				pcm := surroundPhase5Fixture(tc.kind, tc.channels, frame*frameSize, frameSize, rate)
				for _, sample := range pcm {
					input = append(input, float64(sample))
				}
				goPacket, err := goEnc.EncodeFloat32(pcm, frameSize)
				if err != nil {
					t.Fatalf("Go encode frame %d: %v", frame, err)
				}
				refPacket, err := refEnc.EncodeFloat(pcm, frameSize)
				if err != nil {
					t.Fatalf("libopus encode frame %d: %v", frame, err)
				}
				accumulateSurroundStreamBytes(t, goPacket, goEnc.Streams(), goStreamBytes)
				accumulateSurroundStreamBytes(t, refPacket, goEnc.Streams(), refStreamBytes)

				decodedGo, err := goRefDec.DecodeFloat(goPacket, frameSize)
				if err != nil {
					t.Fatalf("libopus decode Go frame %d: %v", frame, err)
				}
				for _, sample := range decodedGo {
					goOut = append(goOut, float64(sample))
				}
				decodedRef, err := refGoDec.DecodeFloat32(refPacket)
				if err != nil {
					t.Fatalf("Go decode libopus frame %d: %v", frame, err)
				}
				for _, sample := range decodedRef {
					refOut = append(refOut, float64(sample))
				}
			}

			goSNR := surroundChannelSNRs(input, goOut, tc.channels, frameSize)
			refSNR := surroundChannelSNRs(input, refOut, tc.channels, frameSize)
			goWeighted := surroundWeightedSNR(goSNR, tc.channels-1)
			refWeighted := surroundWeightedSNR(refSNR, tc.channels-1)
			t.Logf("Go streams=%v weighted=%.3fdB channels=%v", goStreamBytes, goWeighted, goSNR)
			t.Logf("ref streams=%v weighted=%.3fdB channels=%v gap=%.3fdB", refStreamBytes, refWeighted, refSNR, refWeighted-goWeighted)
			for channel, snr := range goSNR {
				if channel != tc.channels-1 && snr < 0 {
					t.Fatalf("Go channel %d collapsed: %.2f dB", channel, snr)
				}
			}
		})
	}
}

func accumulateSurroundStreamBytes(t *testing.T, packet []byte, streams int, totals []int) {
	t.Helper()
	children, _, err := splitMultistreamPackets(packet, streams, 48000)
	if err != nil {
		t.Fatal(err)
	}
	for stream := range children {
		totals[stream] += len(children[stream])
	}
}

func surroundWeightedSNR(snr []float64, lfe int) float64 {
	var sum, weight float64
	for channel, value := range snr {
		w := 1.0
		if channel == lfe {
			w = 0.25
		}
		sum += w * value
		weight += w
	}
	return sum / weight
}
