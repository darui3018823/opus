package opus

import (
	"errors"
	"testing"
)

// TestFrameSizeErrorsAreBadArg: libopus answers an invalid frame size with
// OPUS_BAD_ARG (frame_size_select returns -1 and opus_encode returns
// OPUS_BAD_ARG, opus_encoder.c:2680; opus_decode_native rejects a PLC/FEC
// frame_size that is not a multiple of 2.5 ms, opus_decoder.c:733). The Go
// errors keep wrapping ErrUnsupportedFrameSize as well.
func TestFrameSizeErrorsAreBadArg(t *testing.T) {
	check := func(name string, err error) {
		t.Helper()
		if !errors.Is(err, ErrBadArg) || !errors.Is(err, ErrUnsupportedFrameSize) {
			t.Fatalf("%s: err = %v, want ErrBadArg and ErrUnsupportedFrameSize", name, err)
		}
	}
	enc, err := NewEncoder(48000, 1, ApplicationAudio)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []int{0, 100, 961, 960 * 7} {
		_, err := enc.Encode(make([]int16, max(n, 1)), n)
		check("Encode", err)
	}
	silk, err := NewEncoder(48000, 1, ApplicationRestrictedSILK)
	if err != nil {
		t.Fatal(err)
	}
	_, err = silk.Encode(make([]int16, 240), 240)
	check("restricted SILK 5 ms", err)

	dec, err := NewDecoder(48000, 1)
	if err != nil {
		t.Fatal(err)
	}
	_, err = dec.DecodePLC(make([]int16, 1000), 100)
	check("DecodePLC", err)
	_, err = dec.DecodeFECWithDuration([]byte{0x08}, make([]int16, 1000), 100)
	check("DecodeFECWithDuration", err)

	ms, err := NewMultistreamDecoder(48000, 2, 2, 0, []byte{0, 1})
	if err != nil {
		t.Fatal(err)
	}
	_, err = ms.DecodePLC(make([]int16, 1000), 100)
	check("multistream DecodePLC", err)
}

// TestMultistreamProfileDefaults: EncoderProfileLibopus gives every
// elementary stream libopus's create-time defaults (opus_encoder_init:
// complexity 9, VBR, constrained VBR, automatic bitrate); the legacy
// constructors keep complexity 5 and CBR.
func TestMultistreamProfileDefaults(t *testing.T) {
	ms, err := NewMultistreamEncoderWithProfile(48000, 3, 2, 1, []byte{0, 1, 2}, ApplicationAudio, EncoderProfileLibopus)
	if err != nil {
		t.Fatal(err)
	}
	sur, err := NewSurroundEncoderWithProfile(48000, 6, MappingFamilyVorbis, ApplicationAudio, EncoderProfileLibopus)
	if err != nil {
		t.Fatal(err)
	}
	proj, err := NewProjectionEncoderWithProfile(48000, 4, MappingFamilyProjection, ApplicationAudio, EncoderProfileLibopus)
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := NewMultistreamEncoder(48000, 3, 2, 1, []byte{0, 1, 2}, ApplicationAudio)
	if err != nil {
		t.Fatal(err)
	}
	for name, e := range map[string]*MultistreamEncoder{"ms": ms, "surround": sur.MultistreamEncoder, "projection": proj.multistream} {
		for s, enc := range e.encoders {
			if enc.Complexity() != ComplexityDefault || !enc.VBR() || !enc.VBRConstraint() || enc.Bitrate() != BitrateAuto {
				t.Fatalf("%s stream %d: complexity %d vbr %v constraint %v bitrate %d", name, s, enc.Complexity(), enc.VBR(), enc.VBRConstraint(), enc.Bitrate())
			}
		}
	}
	if legacy.Complexity() != 5 || legacy.VBR() {
		t.Fatalf("legacy defaults changed: complexity %d vbr %v", legacy.Complexity(), legacy.VBR())
	}
	if _, err := NewSurroundEncoderWithProfile(48000, 2, 0, ApplicationAudio, EncoderProfile(9)); !errors.Is(err, ErrBadArg) {
		t.Fatalf("unknown profile = %v, want ErrBadArg", err)
	}
}

// TestPacketLossPercRejectsOutOfRange: OPUS_SET_PACKET_LOSS_PERC returns
// OPUS_BAD_ARG for values outside 0..100 and keeps the previous value
// (opus_encoder.c:2982); the multistream CTL forwards it to every stream.
func TestPacketLossPercRejectsOutOfRange(t *testing.T) {
	enc, err := NewEncoder(48000, 1, ApplicationVOIP)
	if err != nil {
		t.Fatal(err)
	}
	enc.SetPacketLossPerc(30)
	for _, v := range []int{-1, 101, 1000} {
		enc.SetPacketLossPerc(v)
		if got := enc.PacketLossPerc(); got != 30 {
			t.Fatalf("SetPacketLossPerc(%d) changed the setting to %d", v, got)
		}
	}
	ms, err := NewMultistreamEncoder(48000, 2, 2, 0, []byte{0, 1}, ApplicationVOIP)
	if err != nil {
		t.Fatal(err)
	}
	ms.SetPacketLossPerc(100)
	ms.SetPacketLossPerc(101)
	if got := ms.PacketLossPerc(); got != 100 {
		t.Fatalf("multistream loss = %d, want 100", got)
	}
}
