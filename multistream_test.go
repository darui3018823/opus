package opus

import (
	"errors"
	"math"
	"testing"
)

func TestMultistreamRoundTrip51(t *testing.T) {
	const (
		rate      = 48000
		channels  = 6
		frameSize = 960
	)
	mapping := []byte{0, 4, 1, 2, 3, 5}
	enc, err := NewMultistreamEncoder(rate, channels, 4, 2, mapping, ApplicationAudio)
	if err != nil {
		t.Fatal(err)
	}
	enc.SetVBR(true)
	if err := enc.SetBitrate(256000); err != nil {
		t.Fatal(err)
	}
	pcm := make([]float64, frameSize*channels)
	for i := 0; i < frameSize; i++ {
		for ch := 0; ch < channels; ch++ {
			pcm[i*channels+ch] = 0.35 * math.Sin(2*math.Pi*float64(220+ch*113)*float64(i)/rate)
		}
	}
	packet, err := enc.EncodeFloat(pcm, frameSize)
	if err != nil {
		t.Fatal(err)
	}
	packets, duration, err := splitMultistreamPackets(packet, 4, rate)
	if err != nil {
		t.Fatal(err)
	}
	if len(packets) != 4 || duration != frameSize {
		t.Fatalf("split got %d packets, duration %d", len(packets), duration)
	}

	dec, err := NewMultistreamDecoder(rate, channels, 4, 2, mapping)
	if err != nil {
		t.Fatal(err)
	}
	out, err := dec.DecodeFloat(packet)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != len(pcm) {
		t.Fatalf("decoded %d samples, want %d", len(out), len(pcm))
	}
	for ch := 0; ch < channels; ch++ {
		var energy float64
		for i := 0; i < frameSize; i++ {
			v := out[i*channels+ch]
			energy += v * v
		}
		if energy == 0 {
			t.Fatalf("channel %d decoded to silence", ch)
		}
	}
	if enc.FinalRange() != dec.FinalRange() {
		t.Fatalf("final range encoder=%08x decoder=%08x", enc.FinalRange(), dec.FinalRange())
	}
}

func TestMultistreamMappingDuplicatesAndSilence(t *testing.T) {
	enc, err := NewMultistreamEncoder(48000, 2, 1, 1, []byte{0, 1}, ApplicationAudio)
	if err != nil {
		t.Fatal(err)
	}
	packet, err := enc.Encode(make([]int16, 1920), 960)
	if err != nil {
		t.Fatal(err)
	}
	dec, err := NewMultistreamDecoder(48000, 4, 1, 1, []byte{0, 1, 0, 255})
	if err != nil {
		t.Fatal(err)
	}
	out, err := dec.DecodeFloat(packet)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 960; i++ {
		if out[4*i] != out[4*i+2] {
			t.Fatalf("duplicate mapping differs at sample %d", i)
		}
		if out[4*i+3] != 0 {
			t.Fatalf("mapping 255 channel is non-zero at sample %d", i)
		}
	}
}

func TestMultistreamRejectsDurationMismatch(t *testing.T) {
	enc20, _ := NewEncoder(48000, 1, ApplicationAudio)
	enc40, _ := NewEncoder(48000, 1, ApplicationAudio)
	p20, err := enc20.Encode(make([]int16, 960), 960)
	if err != nil {
		t.Fatal(err)
	}
	p40, err := enc40.Encode(make([]int16, 1920), 1920)
	if err != nil {
		t.Fatal(err)
	}
	first, err := makeSelfDelimitedPacket(p20)
	if err != nil {
		t.Fatal(err)
	}
	packet := append(first, p40...)
	dec, err := NewMultistreamDecoder(48000, 2, 2, 0, []byte{0, 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dec.DecodeFloat(packet); !errors.Is(err, ErrInvalidPacket) {
		t.Fatalf("duration mismatch error = %v", err)
	}
}

func TestSelfDelimitedPacketRoundTrip(t *testing.T) {
	enc, err := NewEncoder(48000, 2, ApplicationAudio)
	if err != nil {
		t.Fatal(err)
	}
	enc.SetVBR(true)
	for _, frameSize := range []int{120, 960, 1920, 2880} {
		packet, err := enc.Encode(make([]int16, frameSize*2), frameSize)
		if err != nil {
			t.Fatal(err)
		}
		selfDelimited, err := makeSelfDelimitedPacket(packet)
		if err != nil {
			t.Fatal(err)
		}
		got, used, err := parseSelfDelimitedPacket(append(selfDelimited, 1, 2, 3))
		if err != nil {
			t.Fatal(err)
		}
		if used != len(selfDelimited) {
			t.Fatalf("frameSize %d consumed %d, want %d", frameSize, used, len(selfDelimited))
		}
		wantSamples, _ := PacketGetNumSamples(packet, 48000)
		gotSamples, _ := PacketGetNumSamples(got, 48000)
		if gotSamples != wantSamples {
			t.Fatalf("frameSize %d samples %d, want %d", frameSize, gotSamples, wantSamples)
		}
	}
}
