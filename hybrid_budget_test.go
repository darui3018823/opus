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
