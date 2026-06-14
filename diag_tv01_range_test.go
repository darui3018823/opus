package opus

import (
	"testing"

	"github.com/darui3018823/opus/internal/celt"
)

// TestTV01Code3PaddingRange is the regression guard for the code-3 multi-byte
// padding bug. tv01 is CELT-only fullband stereo; several packets carry a
// padding-length run of multiple 0xFF bytes (e.g. "41 ff ff 6f ...") that the
// old splitOpusFrames mis-parsed (only one 0xFF continuation, off-by math),
// feeding the CELT decoder the wrong bytes and producing full-scale clipping.
// libopus treats each 0xFF count byte as 254 padding-data bytes plus a
// continuation; the first byte < 255 contributes its value and ends the run.
//
// Decoding these packets COLD and checking the range coder's final value
// against the opus_demo stored value proves the framing/entropy path is now
// bit-exact for multi-byte padding.
func TestTV01Code3PaddingRange(t *testing.T) {
	pkts := readOpusDemoPackets(t, "testvector01.bit")
	// Packets that use a multi-0xFF code-3 padding run.
	idxs := []int{394, 433, 1073, 1075, 1138, 1426}
	for _, idx := range idxs {
		if idx >= len(pkts) {
			t.Fatalf("tv01 has only %d packets, need %d", len(pkts), idx)
		}
		pk := pkts[idx]
		toc := pk.packet[0]
		config := int((toc >> 3) & 0x1f)
		stereo := (toc >> 2) & 1
		code := toc & 3
		channels := 1
		if stereo == 1 {
			channels = 2
		}
		var fs int
		switch config & 3 {
		case 0:
			fs = 120
		case 1:
			fs = 240
		case 2:
			fs = 480
		case 3:
			fs = 960
		}
		streams, err := splitOpusFrames(pk.packet[1:], int(code))
		if err != nil {
			t.Fatalf("pkt%d split: %v", idx, err)
		}
		cd, _ := celt.NewDecoderEx(fs, 48000, 21, channels)
		var lastRng uint32
		for _, s := range streams {
			if _, err := cd.Decode(s); err != nil {
				t.Fatalf("pkt%d decode: %v", idx, err)
			}
			lastRng = cd.LastFinalRange()
		}
		if lastRng != pk.finalRange {
			t.Errorf("pkt%d (cfg=%d ch=%d): final range %08x != want %08x",
				idx, config, channels, lastRng, pk.finalRange)
		}
	}
}
