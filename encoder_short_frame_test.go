package opus

import (
	"fmt"
	"math"
	"testing"
)

func TestEncoderShortFramesAllRatesAndChannels(t *testing.T) {
	for _, sampleRate := range []int{8000, 12000, 16000, 24000, 48000} {
		for _, channels := range []int{1, 2} {
			for _, durationNumerator := range []int{1, 2, 4} {
				frameSize := sampleRate * durationNumerator / 400
				name := fmtTestName(sampleRate, channels, durationNumerator)
				t.Run(name, func(t *testing.T) {
					enc, err := NewEncoder(sampleRate, channels, ApplicationRestrictedLowDelay)
					if err != nil {
						t.Fatal(err)
					}
					pcm := make([]float32, frameSize*channels)
					for i := 0; i < frameSize; i++ {
						s := float32(0.35 * math.Sin(2*math.Pi*440*float64(i)/float64(sampleRate)))
						for c := 0; c < channels; c++ {
							pcm[i*channels+c] = s
						}
					}
					packet, err := enc.EncodeFloat32(pcm, frameSize)
					if err != nil {
						t.Fatal(err)
					}
					gotSamples, err := PacketGetNumSamples(packet, sampleRate)
					if err != nil {
						t.Fatal(err)
					}
					if gotSamples != frameSize {
						t.Fatalf("packet samples = %d, want %d", gotSamples, frameSize)
					}
					if gotMode, err := PacketGetMode(packet); err != nil || gotMode != ModeCELTOnly {
						t.Fatalf("packet mode = %d, %v; want CELT-only", gotMode, err)
					}
					dec, err := NewDecoder(sampleRate, channels)
					if err != nil {
						t.Fatal(err)
					}
					out, err := dec.DecodeFloat32(packet)
					if err != nil {
						t.Fatal(err)
					}
					if len(out) != frameSize*channels {
						t.Fatalf("decoded length = %d, want %d", len(out), frameSize*channels)
					}
				})
			}
		}
	}
}

func fmtTestName(sampleRate, channels, durationNumerator int) string {
	duration := map[int]string{1: "2.5ms", 2: "5ms", 4: "10ms"}[durationNumerator]
	channelName := map[int]string{1: "mono", 2: "stereo"}[channels]
	return duration + "/" + channelName + "/" + fmt.Sprintf("%dHz", sampleRate)
}

func TestEncoderShortFrameControlsAndTransitions(t *testing.T) {
	const sampleRate = 48000
	enc, err := NewEncoder(sampleRate, 1, ApplicationAudio)
	if err != nil {
		t.Fatal(err)
	}
	enc.SetVBR(true)
	enc.SetVBRConstraint(false)
	enc.SetDTX(true)
	if err := enc.SetBandwidth(BandwidthWideband); err != nil {
		t.Fatal(err)
	}

	for _, frameSize := range []int{960, 120, 240, 480, 960} {
		pcm := make([]float64, frameSize)
		for i := range pcm {
			pcm[i] = 0.25 * math.Sin(2*math.Pi*700*float64(i)/sampleRate)
		}
		packet, err := enc.EncodeFloat(pcm, frameSize)
		if err != nil {
			t.Fatalf("EncodeFloat(%d): %v", frameSize, err)
		}
		if got, err := PacketGetNumSamples(packet, sampleRate); err != nil || got != frameSize {
			t.Fatalf("frameSize %d packet samples = %d, %v", frameSize, got, err)
		}
		if got, err := PacketGetBandwidth(packet); err != nil || got != BandwidthWideband {
			t.Fatalf("frameSize %d bandwidth = %d, %v", frameSize, got, err)
		}
	}

	if err := enc.Reset(); err != nil {
		t.Fatal(err)
	}
	enc.SetPacketPadding(7)
	packet, err := enc.EncodeFloat(make([]float64, 120), 120)
	if err != nil {
		t.Fatal(err)
	}
	if len(packet) < 8 {
		t.Fatalf("padded short packet length = %d", len(packet))
	}
}
