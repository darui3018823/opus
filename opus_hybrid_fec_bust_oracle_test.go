//go:build opusref

package opus

import (
	"bytes"
	"fmt"
	"os"
	"testing"
)

// TestHybridFECBustOracle covers CBR stereo hybrid packets whose SILK part,
// with the previous packet's LBRR data in front of it, overruns the frame:
// libopus skips CELT for that frame and sends a one-byte "PLC frame" padded
// to the CBR size (opus_encode_frame_native). Every packet and final range
// must match libopus 1.6.1, and the reference must contain such a frame.
func TestHybridFECBustOracle(t *testing.T) {
	if _, err := os.Stat(encOraclePath()); err != nil {
		t.Skipf("encoder oracle not built (%s): run pwsh scripts/oracle/build_encoder.ps1", encOraclePath())
	}
	cases := []struct {
		bitrate, loss int
		vbr           bool
		wantBust      bool
	}{
		{28000, 20, false, true},
		{32000, 20, false, true},
		{40000, 20, false, true},
		{32000, 20, true, false},
		{32000, 0, false, false},
	}
	wl := perfWorkload{channels: 2, gen: perfSpeechFrame}
	frames := perfInputFrames(wl)
	var pcm []float32
	for _, f := range frames {
		for _, v := range f {
			pcm = append(pcm, float32(v))
		}
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("%dk/loss%d/vbr=%v", tc.bitrate/1000, tc.loss, tc.vbr), func(t *testing.T) {
			steps := []aStep{
				{op: 'c', ctl: "vbr", val: fecBustBool(tc.vbr)}, {op: 'c', ctl: "vbrc", val: 1},
				{op: 'c', ctl: "complexity", val: 5}, {op: 'c', ctl: "bitrate", val: tc.bitrate},
				{op: 'c', ctl: "signal", val: 3001}, {op: 'c', ctl: "fec", val: fecBustBool(tc.loss > 0)},
				{op: 'c', ctl: "loss", val: tc.loss},
			}
			for range frames {
				steps = append(steps, aStep{op: 'e', val: perfFrameSize})
			}
			var ref []cEvent
			for _, ev := range runAuditOracle(t, aCfg{rate: 48000, ch: 2, app: "voip", pcm: pcm, steps: steps, oracle: encOraclePath()}) {
				if ev.kind == 'P' {
					ref = append(ref, ev)
				}
			}
			if len(ref) != len(frames) {
				t.Fatalf("oracle returned %d packets, want %d", len(ref), len(frames))
			}
			enc, err := NewEncoder(48000, 2, ApplicationVOIP)
			if err != nil {
				t.Fatal(err)
			}
			enc.SetVBR(tc.vbr)
			enc.SetVBRConstraint(true)
			if err := enc.SetComplexity(5); err != nil {
				t.Fatal(err)
			}
			if err := enc.SetBitrate(tc.bitrate); err != nil {
				t.Fatal(err)
			}
			enc.SetSignalType(SignalVoice)
			enc.SetInbandFEC(tc.loss > 0)
			enc.SetPacketLossPerc(tc.loss)
			busts := 0
			for i, f := range frames {
				got, err := enc.EncodeFloat(f, perfFrameSize)
				if err != nil {
					t.Fatalf("frame %d: %v", i, err)
				}
				want := ref[i].pkt
				if !bytes.Equal(got, want) {
					t.Fatalf("frame %d: packet differs\n go % x\n  c % x", i, got, want)
				}
				if r := enc.FinalRange(); r != ref[i].rng {
					t.Fatalf("frame %d: final range %08x, libopus %08x", i, r, ref[i].rng)
				}
				// TOC code 3, one frame with padding, a zero payload byte.
				if len(want) > 3 && want[0]&3 == 3 && want[1] == 0x41 && want[len(want)-1-int(want[2])] == 0 {
					busts++
				}
			}
			if tc.wantBust && busts == 0 {
				t.Fatal("no PLC frame in the reference: the case no longer covers the overrun")
			}
			t.Logf("%d packets identical, %d PLC frames", len(frames), busts)
		})
	}
}

func fecBustBool(b bool) int {
	if b {
		return 1
	}
	return 0
}
