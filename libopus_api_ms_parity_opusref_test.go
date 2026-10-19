//go:build opusref && opus16

package opus

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/darui3018823/opus/internal/cgoref"
)

// Regression tests for the public API and multistream differences from
// libopus 1.6.1 found by the 2026-10-19 parity hunt. Each test compares the
// Go behaviour with libopus called through cgoref.

// TestParityMaxPacketSizeOneByte: with a one-byte output buffer libopus
// emits a one-byte SILK PLC frame for 60-120 ms packets
// (opus_encoder.c:1364, out_data_bytes==1), not the two-byte code-3 frame,
// and refuses a 100 ms packet (opus_encoder.c:1231).
func TestParityMaxPacketSizeOneByte(t *testing.T) {
	for _, fs := range []int{16000, 48000} {
		for _, ch := range []int{1, 2} {
			for _, app := range []int{ApplicationVOIP, ApplicationAudio} {
				for _, ms := range []int{20, 40, 60, 80, 100, 120} {
					for _, vbr := range []bool{false, true} {
						name := fmt.Sprintf("%dk/ch%d/app%d/%dms/vbr%v", fs/1000, ch, app, ms, vbr)
						n := fs * ms / 1000
						le, lerr := cgoref.NewAudEncoder(fs, ch, app)
						if le == nil {
							t.Fatalf("%s: libopus create %d", name, lerr)
						}
						ge, err := NewEncoderWithProfile(fs, ch, Application(app), EncoderProfileLibopus)
						if err != nil {
							t.Fatal(err)
						}
						le.Set(SetVBRRequest, b2i(vbr))
						ge.SetVBR(vbr)
						if err := ge.SetMaxPacketSize(1); err != nil {
							t.Fatal(err)
						}
						for f := 0; f < 4; f++ {
							pcm := sineF32(n, ch, fs, 440, 0.3, f*n)
							lp, lr := le.EncodeFloat(pcm, n, 1)
							gp, gerr := ge.EncodeFloat32(pcm, n)
							if (lr > 0) != (gerr == nil) {
								t.Fatalf("%s frame %d: lib=%d go=%v", name, f, lr, gerr)
							}
							if !bytes.Equal(lp, gp) {
								t.Fatalf("%s frame %d: lib=%x go=%x", name, f, lp, gp)
							}
							lv, _ := le.GetU(4031)
							if lv != ge.FinalRange() {
								t.Fatalf("%s frame %d: final range lib=%d go=%d", name, f, lv, ge.FinalRange())
							}
						}
						le.Close()
					}
				}
			}
		}
	}
}
