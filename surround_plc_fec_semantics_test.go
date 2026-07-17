package opus

import "testing"

func TestSurroundDecodeLossContract(t *testing.T) {
	const (
		rate      = 16000
		channels  = 6
		frameSize = 120 // 7.5 ms
	)
	dec, err := NewSurroundDecoder(rate, channels, MappingFamilyVorbis)
	if err != nil {
		t.Fatal(err)
	}
	pcm, err := dec.DecodePLCFloat(frameSize)
	if err != nil {
		t.Fatal(err)
	}
	if len(pcm) != frameSize*channels {
		t.Fatalf("PLC length=%d, want %d", len(pcm), frameSize*channels)
	}
	for i, sample := range pcm {
		if sample != 0 {
			t.Fatalf("initial surround PLC sample %d=%g, want zero", i, sample)
		}
	}
	if dec.FinalRange() != 0 {
		t.Fatalf("initial surround range=%08x, want zero", dec.FinalRange())
	}
}
