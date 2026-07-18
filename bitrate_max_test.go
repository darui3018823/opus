package opus

import (
	"bytes"
	"fmt"
	"testing"

	framing "github.com/darui3018823/opus/internal"
)

func TestBitrateMaxUsesConstituentFrameDuration(t *testing.T) {
	for _, rate := range []int{8000, 12000, 16000, 24000, 48000} {
		for _, durationEighthMs := range []int{20, 40, 80, 160, 320, 480, 640, 800, 960} {
			duration := float64(durationEighthMs) / 8
			t.Run(fmt.Sprintf("%dHz/%.1fms", rate, duration), func(t *testing.T) {
				frameSize := rate * durationEighthMs / 8000
				enc, err := NewEncoder(rate, 1, ApplicationAudio)
				if err != nil {
					t.Fatal(err)
				}
				if err := enc.SetBitrate(BitrateMax); err != nil {
					t.Fatal(err)
				}
				if _, err := enc.EncodeFloat(make([]float64, frameSize), frameSize); err != nil {
					t.Fatal(err)
				}

				codedFrameSize := frameSize
				if base := rate / 50; codedFrameSize > base {
					codedFrameSize = base
				}
				want := MaxFrameBytes * 8 * rate / codedFrameSize
				if want > 1500000 {
					want = 1500000
				}
				if got := enc.EffectiveBitrate(); got != want {
					t.Fatalf("EffectiveBitrate()=%d, want %d", got, want)
				}
			})
		}
	}
}

func TestBitrateMaxLongPacketsMatchNumericFrameMaximum(t *testing.T) {
	const rate = 48000
	for _, durationMs := range []int{20, 40, 60, 80, 100, 120} {
		t.Run(fmt.Sprintf("%dms", durationMs), func(t *testing.T) {
			frameSize := rate * durationMs / 1000
			input := strictSpeechLikeFrame(rate, 1, 0, frameSize)

			encode := func(bitrate int) ([]byte, uint32) {
				t.Helper()
				enc, err := NewEncoder(rate, 1, ApplicationAudio)
				if err != nil {
					t.Fatal(err)
				}
				enc.SetVBR(false)
				if err := enc.SetBitrate(bitrate); err != nil {
					t.Fatal(err)
				}
				packet, err := enc.EncodeFloat(input, frameSize)
				if err != nil {
					t.Fatal(err)
				}
				if got := enc.EffectiveBitrate(); got != 510000 {
					t.Fatalf("EffectiveBitrate()=%d, want 510000", got)
				}
				return packet, enc.FinalRange()
			}

			maxPacket, maxRange := encode(BitrateMax)
			numericPacket, numericRange := encode(510000)
			if !bytes.Equal(maxPacket, numericPacket) {
				t.Fatal("BitrateMax packet differs from numeric 510000 packet")
			}
			if maxRange != numericRange {
				t.Fatalf("FinalRange max=%08x numeric=%08x", maxRange, numericRange)
			}

			_, _, countCode := framing.ParseTOC(maxPacket[0])
			frames, err := splitOpusFrames(maxPacket[1:], countCode)
			if err != nil {
				t.Fatal(err)
			}
			for i, frame := range frames {
				if len(frame) > MaxFrameBytes {
					t.Fatalf("frame %d has %d bytes, max %d", i, len(frame), MaxFrameBytes)
				}
			}
		})
	}
}

func TestBitrateMaxLongVoicePacketsRemainCELT(t *testing.T) {
	const rate = 48000
	for _, durationMs := range []int{40, 60, 80, 100, 120} {
		t.Run(fmt.Sprintf("%dms", durationMs), func(t *testing.T) {
			frameSize := rate * durationMs / 1000
			enc, err := NewEncoder(rate, 1, ApplicationVOIP)
			if err != nil {
				t.Fatal(err)
			}
			enc.SetSignalType(SignalVoice)
			if err := enc.SetBitrate(BitrateMax); err != nil {
				t.Fatal(err)
			}
			packet, err := enc.EncodeFloat(strictSpeechLikeFrame(rate, 1, 0, frameSize), frameSize)
			if err != nil {
				t.Fatal(err)
			}
			mode, err := PacketGetMode(packet)
			if err != nil {
				t.Fatal(err)
			}
			if mode != ModeCELTOnly {
				t.Fatalf("mode=%d, want CELT-only", mode)
			}
		})
	}
}

func TestBitrateMaxMatchesNumericAfterHybridHistory(t *testing.T) {
	const (
		rate       = 48000
		durationMs = 60
	)
	frameSize := rate * durationMs / 1000
	maxEnc, err := NewEncoder(rate, 1, ApplicationVOIP)
	if err != nil {
		t.Fatal(err)
	}
	numericEnc, err := NewEncoder(rate, 1, ApplicationVOIP)
	if err != nil {
		t.Fatal(err)
	}
	for _, enc := range []*Encoder{maxEnc, numericEnc} {
		enc.SetSignalType(SignalVoice)
		if err := enc.SetBitrate(85000); err != nil {
			t.Fatal(err)
		}
	}
	history := strictSpeechLikeFrame(rate, 1, 0, frameSize)
	maxHistory, err := maxEnc.EncodeFloat(history, frameSize)
	if err != nil {
		t.Fatal(err)
	}
	numericHistory, err := numericEnc.EncodeFloat(history, frameSize)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(maxHistory, numericHistory) {
		t.Fatal("identical hybrid history packets differ")
	}
	if mode, err := PacketGetMode(maxHistory); err != nil || mode != ModeHybrid {
		t.Fatalf("history mode=%d err=%v, want hybrid", mode, err)
	}
	if err := maxEnc.SetBitrate(BitrateMax); err != nil {
		t.Fatal(err)
	}
	if err := numericEnc.SetBitrate(510000); err != nil {
		t.Fatal(err)
	}

	for packetIndex := 1; packetIndex <= 2; packetIndex++ {
		input := strictSpeechLikeFrame(rate, 1, packetIndex*frameSize, frameSize)
		maxPacket, err := maxEnc.EncodeFloat(input, frameSize)
		if err != nil {
			t.Fatal(err)
		}
		numericPacket, err := numericEnc.EncodeFloat(input, frameSize)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(maxPacket, numericPacket) {
			t.Fatalf("packet %d differs after hybrid history", packetIndex)
		}
		if maxEnc.FinalRange() != numericEnc.FinalRange() {
			t.Fatalf("packet %d FinalRange max=%08x numeric=%08x", packetIndex, maxEnc.FinalRange(), numericEnc.FinalRange())
		}
	}
}

func TestNumericBitrateHighRequestsClampLikeLibopus(t *testing.T) {
	const rate = 48000
	for _, channels := range []int{1, 2} {
		for _, request := range []int{510001, 600000, 750000, 1500000, 1500001} {
			name := fmt.Sprintf("%dch/%d", channels, request)
			t.Run(name, func(t *testing.T) {
				enc, err := NewEncoder(rate, channels, ApplicationAudio)
				if err != nil {
					t.Fatal(err)
				}
				if err := enc.SetBitrate(request); err != nil {
					t.Fatal(err)
				}
				wantSetting := request
				if ceiling := 750000 * channels; wantSetting > ceiling {
					wantSetting = ceiling
				}
				if got := enc.Bitrate(); got != wantSetting {
					t.Fatalf("Bitrate()=%d, want %d", got, wantSetting)
				}
				if got := enc.EffectiveBitrate(); got != 510000 {
					t.Fatalf("20 ms EffectiveBitrate()=%d, want 510000", got)
				}

				short, err := NewEncoder(rate, channels, ApplicationAudio)
				if err != nil {
					t.Fatal(err)
				}
				if err := short.SetBitrate(request); err != nil {
					t.Fatal(err)
				}
				frameSize := rate / 100
				if _, err := short.EncodeFloat(make([]float64, frameSize*channels), frameSize); err != nil {
					t.Fatal(err)
				}
				wantEffective := wantSetting
				if wantEffective > 1020000 {
					wantEffective = 1020000
				}
				if got := short.EffectiveBitrate(); got != wantEffective {
					t.Fatalf("10 ms EffectiveBitrate()=%d, want %d", got, wantEffective)
				}
			})
		}
	}
}
