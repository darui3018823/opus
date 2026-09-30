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

func TestLibopusPolicyPaddedShortFrames(t *testing.T) {
	for _, frameSize := range []int{120, 240, 480} {
		enc, err := NewEncoder(48000, 1, ApplicationAudio)
		if err != nil {
			t.Fatal(err)
		}
		if err := enc.SetModePolicy(ModePolicyLibopus); err != nil {
			t.Fatal(err)
		}
		enc.SetPacketPadding(7)
		packet, err := enc.EncodeFloat(make([]float64, frameSize), frameSize)
		if err != nil {
			t.Fatalf("frameSize %d: %v", frameSize, err)
		}
		if got, err := PacketGetNumSamples(packet, 48000); err != nil || got != frameSize {
			t.Fatalf("frameSize %d: packet samples = %d, %v", frameSize, got, err)
		}
		if len(packet) < 8 {
			t.Fatalf("frameSize %d: padded packet length = %d", frameSize, len(packet))
		}
	}
}

// TestInbandFECAcrossFrameDurationSwitch switches the packet duration
// between 20 ms and 10 ms (the same SILK frame count) with in-band FEC on.
// As silk_Encode's `transition`, the previous packet's LBRR frames are
// dropped instead of being written with the other frame length's syntax.
func TestInbandFECAcrossFrameDurationSwitch(t *testing.T) {
	for _, rate := range []int{8000, 16000} {
		for _, channels := range []int{1, 2} {
			enc, err := NewEncoder(rate, channels, ApplicationVOIP)
			if err != nil {
				t.Fatal(err)
			}
			if err := enc.SetBitrate(24000); err != nil {
				t.Fatal(err)
			}
			enc.SetInbandFEC(true)
			enc.SetPacketLossPerc(20)
			dec, err := NewDecoder(rate, channels)
			if err != nil {
				t.Fatal(err)
			}
			pos := 0
			for p, ms := range []int{20, 20, 20, 20, 20, 20, 20, 20, 20, 20, 20, 10, 10, 20, 10, 20} {
				n := rate * ms / 1000
				pcm := make([]float64, n*channels)
				for i := 0; i < n; i++ {
					for c := 0; c < channels; c++ {
						pcm[i*channels+c] = 0.5 * math.Sin(float64(pos+i)*0.05)
					}
				}
				pos += n
				pkt, err := enc.EncodeFloat(pcm, n)
				if err != nil {
					t.Fatalf("%d Hz %dch packet %d (%d ms): %v", rate, channels, p, ms, err)
				}
				if p == 11 {
					if has, err := PacketHasLBRR(pkt); err != nil || has {
						t.Fatalf("%d Hz %dch: the first 10 ms packet carries LBRR=%v (err %v), want none", rate, channels, has, err)
					}
				}
				if _, err := dec.DecodeFloat(pkt); err != nil {
					t.Fatalf("%d Hz %dch packet %d: decode: %v", rate, channels, p, err)
				}
			}
		}
	}
}

// TestResetMatchesFreshEncoder checks that Reset clears what OPUS_RESET_STATE
// clears: after Reset the encoder codes the same packets as a new encoder
// with the same settings, including across mode switches with mixed packet
// durations (the CELT prefill of a switch uses the new packet's stream
// channels, not those left by earlier packets).
func TestResetMatchesFreshEncoder(t *testing.T) {
	type cfg struct {
		rate, channels, bitrate int
		app                     Application
	}
	durations := []int{20, 20, 20, 20, 20, 20, 5, 5, 20, 10, 20, 2, 20}
	for _, c := range []cfg{{8000, 2, 16000, ApplicationAudio}, {16000, 2, 24000, ApplicationVOIP}, {48000, 2, 20000, ApplicationAudio}, {48000, 1, 12000, ApplicationVOIP}} {
		newEnc := func() *Encoder {
			enc, err := NewEncoder(c.rate, c.channels, c.app)
			if err != nil {
				t.Fatal(err)
			}
			if err := enc.SetBitrate(c.bitrate); err != nil {
				t.Fatal(err)
			}
			if err := enc.SetComplexity(8); err != nil {
				t.Fatal(err)
			}
			enc.SetDTX(true)
			// Without in-band FEC: OPUS_RESET_STATE keeps silk_mode, so
			// decide_fec's hysteresis (LBRR_coded) survives a reset.
			enc.SetPacketLossPerc(12)
			return enc
		}
		run := func(enc *Encoder, start int) [][]byte {
			var out [][]byte
			pos := start
			for _, ms := range durations {
				n := c.rate * ms / 1000
				if ms == 2 {
					n = c.rate / 400
				}
				pcm := strictSpeechLikeFrame(c.rate, c.channels, pos, n)
				pos += n
				pkt, err := enc.EncodeFloat(pcm, n)
				if err != nil {
					t.Fatal(err)
				}
				out = append(out, pkt)
			}
			return out
		}
		used := newEnc()
		run(used, 50000)
		if err := used.Reset(); err != nil {
			t.Fatal(err)
		}
		got := run(used, 0)
		want := run(newEnc(), 0)
		for i := range want {
			if string(got[i]) != string(want[i]) {
				t.Fatalf("%+v: packet %d after Reset differs from a new encoder (TOC %#x vs %#x)", c, i, got[i][0], want[i][0])
			}
		}
	}
}
