package opus

import (
	"errors"
	"testing"
)

func TestSoftClipFloat32(t *testing.T) {
	pcm := []float32{1.5, -1.6, 0.75, -0.5, 1.25, -1.2}
	mem := make([]float32, 2)
	if err := SoftClipFloat32(pcm, 2, mem); err != nil {
		t.Fatal(err)
	}
	for i, v := range pcm {
		if v > 1 || v < -1 {
			t.Fatalf("sample %d = %f, want clipped into [-1,1]", i, v)
		}
	}
	if mem[0] == 0 && mem[1] == 0 {
		t.Fatalf("soft clip memory was not updated")
	}
	next := []float32{0.95, -0.95, 0.5, -0.5}
	if err := SoftClipFloat32(next, 2, mem); err != nil {
		t.Fatal(err)
	}
	for i, v := range next {
		if v > 1 || v < -1 {
			t.Fatalf("next sample %d = %f, want clipped into [-1,1]", i, v)
		}
	}
}

// Two clipped half-waves of opposite sign in one frame must each be corrected
// within their own zero-crossing bounds; a single-peak correction would
// amplify the opposite-sign excursion instead of attenuating it.
func TestSoftClipFloat32MultipleRegions(t *testing.T) {
	pcm := []float32{0.4, 1.5, 0.6, -0.5, -1.8, -0.9, 0.3, 1.2, 0.2, -0.1}
	mem := make([]float32, 1)
	if err := SoftClipFloat32(pcm, 1, mem); err != nil {
		t.Fatal(err)
	}
	for i, v := range pcm {
		if v > 1 || v < -1 {
			t.Fatalf("sample %d = %f, want clipped into [-1,1]", i, v)
		}
	}
	// Regions keep their sign after correction.
	if pcm[1] <= 0 || pcm[4] >= 0 || pcm[7] <= 0 {
		t.Fatalf("soft clip flipped a region's sign: %v", pcm)
	}
}

// A clipped half-wave that runs into the frame boundary must keep applying the
// same non-linearity at the start of the next frame (declip memory), and stop
// at the first zero crossing.
func TestSoftClipFloat32FrameContinuity(t *testing.T) {
	first := []float32{0.2, -0.6, -1.5, -1.4}
	mem := make([]float32, 1)
	if err := SoftClipFloat32(first, 1, mem); err != nil {
		t.Fatal(err)
	}
	if mem[0] <= 0 {
		t.Fatalf("declip memory = %f, want positive state for a negative clipped tail", mem[0])
	}
	second := []float32{-1.3, -0.4, 0.5, 0.9}
	if err := SoftClipFloat32(second, 1, mem); err != nil {
		t.Fatal(err)
	}
	for i, v := range second {
		if v > 1 || v < -1 {
			t.Fatalf("second frame sample %d = %f, want clipped into [-1,1]", i, v)
		}
	}
	if second[2] != 0.5 || second[3] != 0.9 {
		t.Fatalf("continuation crossed the zero crossing: %v", second)
	}
}

func TestSoftClipFloat32MultiChannel(t *testing.T) {
	pcm := []float32{1.4, -1.7, 0.3, 0.9, 0.8, -0.2, -1.2, 1.6, 0.1}
	mem := make([]float32, 3)
	if err := SoftClipFloat32(pcm, 3, mem); err != nil {
		t.Fatal(err)
	}
	for i, v := range pcm {
		if v > 1 || v < -1 {
			t.Fatalf("sample %d = %f, want clipped into [-1,1]", i, v)
		}
	}
}

func TestSoftClipFloat32Errors(t *testing.T) {
	if err := SoftClipFloat32(make([]float32, 4), 0, make([]float32, 1)); !errors.Is(err, ErrUnsupportedChannels) || !errors.Is(err, ErrBadArg) {
		t.Fatalf("channels error = %v", err)
	}
	if err := SoftClipFloat32(make([]float32, 3), 2, make([]float32, 2)); !errors.Is(err, ErrBadArg) {
		t.Fatalf("length error = %v", err)
	}
	if err := SoftClipFloat32(make([]float32, 4), 2, make([]float32, 1)); !errors.Is(err, ErrBadArg) {
		t.Fatalf("mem error = %v", err)
	}
}
