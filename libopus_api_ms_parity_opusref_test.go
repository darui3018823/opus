//go:build opusref && opus16

package opus

import (
	"bytes"
	"errors"
	"fmt"
	"math"
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

// TestParityProjectionCreateErrors: opus_projection_ambisonics_encoder_create
// reports OPUS_ALLOC_FAIL when its size query fails (a family other than 3
// or a channel count without a matrix, opus_projection_encoder.c:373) and
// the init error otherwise (OPUS_BAD_ARG for a bad sample rate). Family 2
// is a Go extension that libopus's projection API does not offer.
func TestParityProjectionCreateErrors(t *testing.T) {
	for _, fs := range []int{16000, 44100, 48000} {
		for _, family := range []int{0, 1, 2, 3, 4, 255} {
			for ch := 0; ch <= 230; ch++ {
				_, _, _, _, lerr := cgoref.AudAmbisonics(fs, ch, family, ApplicationAudio)
				_, gerr := NewProjectionEncoder(fs, ch, family, ApplicationAudio)
				if family == MappingFamilyAmbisonics {
					if lerr != -7 {
						t.Fatalf("fs=%d family 2 ch=%d: libopus %d, want ALLOC_FAIL", fs, ch, lerr)
					}
					continue
				}
				got := goErrCode(gerr)
				if lerr == -7 && !errors.Is(gerr, ErrAllocFail) || lerr != -7 && got != lerr {
					t.Fatalf("fs=%d family=%d ch=%d: libopus %d, go %v", fs, family, ch, lerr, gerr)
				}
			}
		}
	}
}

// TestParityRestrictedApplicationCTLs: OPUS_SET_APPLICATION fails on a
// RESTRICTED_SILK / RESTRICTED_CELT encoder even with its own value
// (opus_encoder.c:2789), and a RESTRICTED_SILK encoder ignores
// OPUS_SET_PHASE_INVERSION_DISABLED and reads it back as 0
// (opus_encoder.c:3181, 3192).
func TestParityRestrictedApplicationCTLs(t *testing.T) {
	for _, app := range auditApps {
		for _, v := range []int{ApplicationVOIP, ApplicationAudio, ApplicationRestrictedLowDelay, ApplicationRestrictedSILK, ApplicationRestrictedCELT} {
			le, _ := cgoref.NewAudEncoder(48000, 2, app)
			ge, err := NewEncoder(48000, 2, Application(app))
			if err != nil {
				t.Fatal(err)
			}
			lr := le.Set(SetApplicationRequest, v)
			gerr := ge.SetApplication(Application(v))
			if (lr == 0) != (gerr == nil) {
				t.Fatalf("app=%d SetApplication(%d): lib=%d go=%v", app, v, lr, gerr)
			}
			if la, _ := le.Get(GetApplicationRequest); la != int(ge.Application()) {
				t.Fatalf("app=%d SetApplication(%d): application lib=%d go=%d", app, v, la, ge.Application())
			}
			le.Close()
		}
		for _, v := range []bool{true, false, true} {
			le, _ := cgoref.NewAudEncoder(48000, 2, app)
			ge, _ := NewEncoder(48000, 2, Application(app))
			le.Set(SetPhaseInversionDisabledRequest, b2i(v))
			ge.SetPhaseInversionDisabled(v)
			if lv, _ := le.Get(GetPhaseInversionDisabledRequest); lv != b2i(ge.PhaseInversionDisabled()) {
				t.Fatalf("app=%d phase inversion %v: lib=%d go=%v", app, v, lv, ge.PhaseInversionDisabled())
			}
			le.Close()
		}
	}
}

// parityPackets encodes a few libopus packets of the given application and
// bandwidth (SILK-only for WB voice, hybrid for FB voice, CELT for
// restricted low delay), with in-band FEC.
func parityPackets(t *testing.T, fs, ch, app, bw, n, frames int) [][]byte {
	t.Helper()
	le, lerr := cgoref.NewAudEncoder(fs, ch, app)
	if le == nil {
		t.Fatalf("libopus create %d", lerr)
	}
	defer le.Close()
	le.Set(SetBandwidthRequest, bw)
	le.Set(SetInbandFECRequest, 1)
	le.Set(SetPacketLossPercRequest, 20)
	le.Set(SetBitrateRequest, 32000)
	var out [][]byte
	for f := 0; f < frames; f++ {
		p, r := le.EncodeFloat(sineF32(n, ch, fs, 300, 0.3, f*n), n, 1500)
		if r <= 0 {
			t.Fatalf("libopus encode %d", r)
		}
		out = append(out, p)
	}
	return out
}

// TestParityDecodeFECEmptyPacket: opus_decode with no packet and
// decode_fec=1 conceals frame_size samples (opus_decoder.c:763) instead of
// failing. The FEC decode of a carrier whose first frame has at most one
// byte runs the PLC and leaves rangeFinal 0 (opus_decoder.c:313, 698).
func TestParityDecodeFECEmptyPacket(t *testing.T) {
	type cfg struct {
		name    string
		app, bw int
	}
	for _, c := range []cfg{
		{"silk", ApplicationVOIP, BandwidthWideband},
		{"hybrid", ApplicationVOIP, BandwidthFullband},
		{"celt", ApplicationRestrictedLowDelay, BandwidthFullband},
	} {
		for _, ch := range []int{1, 2} {
			name := fmt.Sprintf("%s/ch%d", c.name, ch)
			const fs, n = 48000, 960
			pkts := parityPackets(t, fs, ch, c.app, c.bw, n, 6)
			ld, _ := cgoref.NewAudDecoder(fs, ch)
			gd, err := NewDecoder(fs, ch)
			if err != nil {
				t.Fatal(err)
			}
			for _, p := range pkts[:4] {
				ld.Decode(p, n, ch, false)
				if _, err := gd.DecodeFloat32(p); err != nil {
					t.Fatal(err)
				}
			}
			// Empty packet with FEC: concealment of n samples.
			lp, lr := ld.Decode(nil, n, ch, true)
			gp, gerr := gd.DecodeFECFloat32(nil, n)
			if lr != n || gerr != nil || len(gp) != len(lp) {
				t.Fatalf("%s empty FEC: lib=%d go=%d,%v", name, lr, len(gp)/ch, gerr)
			}
			// CELT concealment differs from libopus by a few float LSBs (a
			// decoder PLC residual outside this API fix): compare SILK and
			// hybrid concealment only.
			for i := range lp {
				if c.name != "celt" && math.Abs(float64(lp[i]-gp[i])) > 1e-6 {
					t.Fatalf("%s empty FEC sample %d: lib=%g go=%g", name, i, lp[i], gp[i])
				}
			}
			i16 := make([]int16, n*ch)
			if got, err := gd.DecodeFEC(nil, i16); err != nil || got != n {
				t.Fatalf("%s DecodeFEC(nil) = %d, %v", name, got, err)
			}
			ld.Decode(nil, n, ch, true)
			// FEC from a carrier holding the next packet's TOC only.
			carrier := []byte{pkts[4][0] &^ 3}
			lp, lr = ld.Decode(carrier, n, ch, true)
			gp, gerr = gd.DecodeFECFloat32(carrier, n)
			if lr != n || gerr != nil || len(gp) != len(lp) {
				t.Fatalf("%s 1-byte FEC: lib=%d go=%d,%v", name, lr, len(gp)/ch, gerr)
			}
			lv, _ := ld.Get(4031)
			if uint32(lv) != gd.FinalRange() {
				t.Fatalf("%s 1-byte FEC final range lib=%d go=%d", name, uint32(lv), gd.FinalRange())
			}
			ld.Close()
		}
	}
}
