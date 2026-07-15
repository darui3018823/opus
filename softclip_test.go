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

func TestSoftClipFloat32Errors(t *testing.T) {
	if err := SoftClipFloat32(make([]float32, 4), 3, make([]float32, 3)); !errors.Is(err, ErrUnsupportedChannels) || !errors.Is(err, ErrBadArg) {
		t.Fatalf("channels error = %v", err)
	}
	if err := SoftClipFloat32(make([]float32, 3), 2, make([]float32, 2)); !errors.Is(err, ErrBadArg) {
		t.Fatalf("length error = %v", err)
	}
	if err := SoftClipFloat32(make([]float32, 4), 2, make([]float32, 1)); !errors.Is(err, ErrBadArg) {
		t.Fatalf("mem error = %v", err)
	}
}
