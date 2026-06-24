package opus

import (
	"bytes"
	"errors"
	"testing"
)

func TestRepacketizerRoundTrip(t *testing.T) {
	enc, err := NewEncoder(48000, 1, ApplicationAudio)
	if err != nil {
		t.Fatal(err)
	}
	enc.SetVBR(true)
	rp := NewRepacketizer()
	var packets [][]byte
	for i := 0; i < 3; i++ {
		pcm := make([]int16, 960)
		for j := range pcm {
			pcm[j] = int16((i + 1) * (j%200 - 100))
		}
		packet, err := enc.Encode(pcm, 960)
		if err != nil {
			t.Fatal(err)
		}
		packets = append(packets, packet)
		if err := rp.Cat(packet); err != nil {
			t.Fatal(err)
		}
	}
	if rp.NumFrames() != 3 {
		t.Fatalf("NumFrames = %d, want 3", rp.NumFrames())
	}
	combined, err := rp.Out()
	if err != nil {
		t.Fatal(err)
	}
	if samples, err := PacketGetNumSamples(combined, 48000); err != nil || samples != 2880 {
		t.Fatalf("combined samples = %d, err=%v", samples, err)
	}
	firstTwo, err := rp.OutRange(0, 2)
	if err != nil {
		t.Fatal(err)
	}
	if frames, err := PacketGetNumFrames(firstTwo); err != nil || frames != 2 {
		t.Fatalf("range frames = %d, err=%v", frames, err)
	}

	seqDec, _ := NewDecoder(48000, 1)
	var sequential []float64
	for _, packet := range packets {
		pcm, err := seqDec.DecodeFloat(packet)
		if err != nil {
			t.Fatal(err)
		}
		sequential = append(sequential, pcm...)
	}
	combinedDec, _ := NewDecoder(48000, 1)
	got, err := combinedDec.DecodeFloat(combined)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(sequential) {
		t.Fatalf("decoded lengths = %d, want %d", len(got), len(sequential))
	}
	for i := range got {
		if got[i] != sequential[i] {
			t.Fatalf("decoded sample %d differs: got=%g want=%g", i, got[i], sequential[i])
		}
	}

	rp.Reset()
	if rp.NumFrames() != 0 {
		t.Fatalf("NumFrames after Reset = %d", rp.NumFrames())
	}
}

func TestPacketPadUnpad(t *testing.T) {
	enc, err := NewEncoder(48000, 1, ApplicationAudio)
	if err != nil {
		t.Fatal(err)
	}
	packet, err := enc.Encode(make([]int16, 960), 960)
	if err != nil {
		t.Fatal(err)
	}
	padded, err := PacketPad(packet, len(packet)+300)
	if err != nil {
		t.Fatal(err)
	}
	if len(padded) != len(packet)+300 {
		t.Fatalf("padded length = %d, want %d", len(padded), len(packet)+300)
	}
	unpadded, err := PacketUnpad(padded)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(unpadded, packet) {
		t.Fatalf("unpad changed packet:\n got %x\nwant %x", unpadded, packet)
	}
	if _, err := PacketPad(packet, len(packet)-1); !errors.Is(err, ErrBadArg) {
		t.Fatalf("shrinking pad error = %v, want ErrBadArg", err)
	}
}

func TestRepacketizerRejectsMismatchAndOverDuration(t *testing.T) {
	audio, _ := NewEncoder(48000, 1, ApplicationAudio)
	p20, _ := audio.Encode(make([]int16, 960), 960)
	p10, _ := audio.Encode(make([]int16, 480), 480)
	rp := NewRepacketizer()
	if err := rp.Cat(p20); err != nil {
		t.Fatal(err)
	}
	if err := rp.Cat(p10); !errors.Is(err, ErrInvalidPacket) {
		t.Fatalf("TOC mismatch error = %v, want ErrInvalidPacket", err)
	}
	for i := 1; i < 6; i++ {
		if err := rp.Cat(p20); err != nil {
			t.Fatal(err)
		}
	}
	if err := rp.Cat(p20); !errors.Is(err, ErrInvalidPacket) {
		t.Fatalf("over-duration error = %v, want ErrInvalidPacket", err)
	}
}
