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
