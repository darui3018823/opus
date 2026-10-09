package opus

import "testing"

func TestClampMultistreamFrameSizeLimitsToOneHundredTwentyMs(t *testing.T) {
	for _, tc := range []struct{ frameSize, rate, want int }{
		{960, 48000, 960},
		{5760, 48000, 5760},
		{5761, 48000, 5760},
		{100000, 48000, 5760},
		{1920, 16000, 1920},
		{1921, 16000, 1920},
	} {
		if got := clampMultistreamFrameSize(tc.frameSize, tc.rate); got != tc.want {
			t.Errorf("clamp(%d, %d)=%d want %d", tc.frameSize, tc.rate, got, tc.want)
		}
	}
}
