package opus

import (
	"math"
	"testing"
)

func TestHybridMultiFrameStrictBudget(t *testing.T) {
	tests := []struct {
		name     string
		rate     int
		channels int
		bitrate  int
	}{
		{name: "swb-24k-mono", rate: 24000, channels: 1, bitrate: 64000},
		{name: "fb-48k-stereo", rate: 48000, channels: 2, bitrate: 96000},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			enc, err := NewEncoder(tc.rate, tc.channels, ApplicationVOIP)
			if err != nil {
				t.Fatalf("NewEncoder: %v", err)
			}
			if err := enc.SetBitrate(tc.bitrate); err != nil {
				t.Fatalf("SetBitrate: %v", err)
			}

			frameSize := tc.rate * 40 / 1000
			packet, err := enc.EncodeFloat(hybridBudgetFixture(tc.rate, frameSize, tc.channels), frameSize)
			if err != nil {
				t.Fatalf("EncodeFloat: %v", err)
			}
			if code := int(packet[0] & 0x03); code != 1 {
				t.Fatalf("count code=%d, want 1 for equal-size 40 ms hybrid frames", code)
			}

			frames, err := splitOpusFrames(packet[1:], 1)
			if err != nil {
				t.Fatalf("splitOpusFrames: %v", err)
			}
			targetBytes := tc.bitrate * 20 / 1000 / 8
			for i, frame := range frames {
				if len(frame) != targetBytes {
					t.Fatalf("frame %d length=%d, want %d", i, len(frame), targetBytes)
				}
			}
		})
	}
}

// A hard onset can make the VBR SILK low band overshoot the nominal hybrid
// frame budget before CELT runs. CELT must raise its final VBR size to the
// post-header minimum before allocation, so the packet can exceed nominal
// without changing the decoder's allocation basis.
func TestHybridCVBROnsetBudgetOvershoot(t *testing.T) {
	const (
		rate      = 48000
		channels  = 1
		bitrate   = 44000
		frameSize = rate / 50
	)
	enc, err := NewEncoder(rate, channels, ApplicationVOIP)
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	if err := enc.SetBitrate(bitrate); err != nil {
		t.Fatalf("SetBitrate: %v", err)
	}
	enc.SetVBR(true)
	enc.SetVBRConstraint(true)
	enc.SetSignalType(SignalVoice)

	dec, err := NewDecoder(rate, channels)
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	nominalPacketBytes := 1 + bitrate*20/1000/8
	var grewPastNominal bool
	pcmOut := make([]int16, frameSize*channels)
	for frame := 0; frame < 4; frame++ {
		input := hybridCVBROnsetFixture(frame*frameSize, frameSize)
		packet, err := enc.EncodeFloat(input, frameSize)
		if err != nil {
			t.Fatalf("frame %d EncodeFloat: %v", frame, err)
		}
		mode, err := PacketGetMode(packet)
		if err != nil {
			t.Fatalf("frame %d PacketGetMode: %v", frame, err)
		}
		if mode != ModeHybrid {
			t.Fatalf("frame %d mode=%d, want hybrid", frame, mode)
		}
		if len(packet) > nominalPacketBytes {
			grewPastNominal = true
		}
		if _, err := dec.Decode(packet, pcmOut); err != nil {
			t.Fatalf("frame %d Decode: %v", frame, err)
		}
	}
	if !grewPastNominal {
		t.Fatalf("CVBR hybrid packet never exceeded nominal target %d bytes", nominalPacketBytes)
	}
}

func TestHybridCVBROnsetFinalRange(t *testing.T) {
	const (
		rate      = 48000
		channels  = 1
		bitrate   = 44000
		frameSize = rate / 50
		frames    = 6
	)
	enc, err := NewEncoder(rate, channels, ApplicationVOIP)
	if err != nil {
		t.Fatal(err)
	}
	if err := enc.SetBitrate(bitrate); err != nil {
		t.Fatal(err)
	}
	enc.SetVBR(true)
	enc.SetVBRConstraint(true)
	enc.SetSignalType(SignalVoice)

	dec, err := NewDecoder(rate, channels)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]int16, frameSize*channels)
	for frame := 0; frame < frames; frame++ {
		input := hybridCVBROnsetFixture(frame*frameSize, frameSize)
		packet, err := enc.EncodeFloat(input, frameSize)
		if err != nil {
			t.Fatalf("frame %d encode: %v", frame, err)
		}
		want := enc.FinalRange()
		if _, err := dec.Decode(packet, out); err != nil {
			t.Fatalf("frame %d decode: %v", frame, err)
		}
		if got := dec.FinalRange(); got != want {
			t.Fatalf("frame %d final range=%08x, want encoder %08x (packet bytes=%d)",
				frame, got, want, len(packet))
		}
	}
}

func hybridBudgetFixture(rate, n, channels int) []float64 {
	out := make([]float64, n*channels)
	highFreq := 10000.0
	if rate >= 48000 {
		highFreq = 16000.0
	}
	for i := 0; i < n; i++ {
		x := float64(i) / float64(rate)
		env := 0.55 + 0.35*math.Sin(2*math.Pi*3*x)
		left := env * (0.32*math.Sin(2*math.Pi*180*x) +
			0.12*math.Sin(2*math.Pi*360*x+0.4) +
			0.06*math.Sin(2*math.Pi*720*x+0.9) +
			0.025*math.Sin(2*math.Pi*1100*x+1.7))
		left += 0.035 * math.Sin(2*math.Pi*highFreq*x+0.3)
		out[i*channels] = left
		if channels == 2 {
			right := env * (0.30*math.Sin(2*math.Pi*185*x+0.2) +
				0.10*math.Sin(2*math.Pi*370*x+0.7) +
				0.05*math.Sin(2*math.Pi*740*x+1.1))
			right += 0.032 * math.Sin(2*math.Pi*highFreq*x+0.8)
			out[i*channels+1] = right
		}
	}
	return out
}

func hybridCVBROnsetFixture(start, n int) []float64 {
	out := make([]float64, n)
	for i := 0; i < n; i++ {
		sample := start + i
		t := float64(sample) / 48000.0
		env := 0.38 + 0.22*math.Sin(2*math.Pi*3.1*t+0.2)
		v := env * (0.34*math.Sin(2*math.Pi*155*t) +
			0.17*math.Sin(2*math.Pi*310*t+0.5) +
			0.09*math.Sin(2*math.Pi*620*t+1.0))
		if sample >= 960 && sample < 1920 {
			burst := math.Exp(-float64(sample-960) / 210.0)
			v += burst * (0.08*math.Sin(2*math.Pi*3600*t+0.1) +
				0.07*math.Sin(2*math.Pi*7600*t+0.4) +
				0.05*math.Sin(2*math.Pi*13200*t+0.8) +
				0.04*math.Sin(2*math.Pi*18100*t+1.2))
		}
		out[i] = v
	}
	return out
}
