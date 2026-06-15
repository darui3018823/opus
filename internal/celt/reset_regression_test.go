package celt

import "testing"

func TestDecoderResetClearsLastFinalRange(t *testing.T) {
	dec, err := NewDecoder(FrameSize20ms, 48000, 1)
	if err != nil {
		t.Fatal(err)
	}
	dec.lastFinalRange = 0x12345678
	dec.Reset()
	if dec.lastFinalRange != 0 {
		t.Fatalf("lastFinalRange after Reset = 0x%08x, want 0", dec.lastFinalRange)
	}
}
