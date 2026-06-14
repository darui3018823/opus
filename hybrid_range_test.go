package opus

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"github.com/darui3018823/opus/internal/celt"
	"github.com/darui3018823/opus/internal/entcode"
	"github.com/darui3018823/opus/internal/silk"
)

type opusDemoPkt struct {
	packet     []byte
	finalRange uint32
}

// readOpusDemoPackets parses an opus_demo .bit file:
// [BE u32 size][BE u32 final_range][payload] repeated.
func readOpusDemoPackets(t *testing.T, vec string) []opusDemoPkt {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "opus_newvectors", vec))
	if err != nil {
		t.Skip(err)
	}
	var out []opusDemoPkt
	for len(data) >= 8 {
		size := binary.BigEndian.Uint32(data[:4])
		fr := binary.BigEndian.Uint32(data[4:8])
		data = data[8:]
		if int(size) > len(data) {
			break
		}
		p := append([]byte(nil), data[:size]...)
		data = data[size:]
		out = append(out, opusDemoPkt{packet: p, finalRange: fr})
	}
	return out
}

// TestHybridRangeExact verifies the hybrid decode path (SILK low band + hybrid
// redundancy flag + CELT high band, sharing one range decoder) consumes the
// bitstream exactly: the range coder's final value must equal the encoder's
// stored final range. This is independent of synthesis warm-up, so a cold
// per-packet decode is a valid bit-exact guard for the hybrid entropy path.
func TestHybridRangeExact(t *testing.T) {
	for _, vec := range []string{"testvector05.bit", "testvector06.bit"} {
		pkts := readOpusDemoPackets(t, vec)
		tested := 0
		for idx, pk := range pkts {
			if len(pk.packet) < 2 {
				continue
			}
			toc := pk.packet[0]
			config := int((toc >> 3) & 0x1f)
			if config < 12 || config >= 16 { // hybrid configs only
				continue
			}
			// Only single-frame (code 0) packets keep this isolated decode simple.
			if toc&0x3 != 0 {
				continue
			}
			channels := 1
			if (toc>>2)&1 == 1 {
				channels = 2
			}
			stream := pk.packet[1:]
			frameMs := 20
			if config&1 == 0 {
				frameMs = 10
			}
			celtEnd := 21
			if config < 14 {
				celtEnd = 19
			}
			fs := 960
			if frameMs == 10 {
				fs = 480
			}

			sd, err := silk.NewDecoderWithFrameMs(16000, channels, frameMs)
			if err != nil {
				t.Fatal(err)
			}
			cd, err := celt.NewDecoderEx(fs, 48000, 21, channels)
			if err != nil {
				t.Fatal(err)
			}

			dec := entcode.NewDecoder(stream)
			if _, err := sd.DecodeMultiWithDecoder(dec, 1); err != nil {
				t.Fatalf("%s pkt %d: SILK decode: %v", vec, idx, err)
			}
			if dec.ECTell()+37 <= len(stream)*8 {
				_ = dec.DecodeBitLogp(12) // hybrid redundancy flag
			}
			if _, err := cd.DecodeHybrid(dec, len(stream), 17, celtEnd); err != nil {
				t.Fatalf("%s pkt %d: CELT decode: %v", vec, idx, err)
			}
			if got := dec.GetRng(); got != pk.finalRange {
				t.Errorf("%s pkt %d (config %d, %dch): final range %08x, want %08x",
					vec, idx, config, channels, got, pk.finalRange)
			}
			tested++
			if tested >= 8 {
				break
			}
		}
		if tested == 0 {
			t.Errorf("%s: no single-frame hybrid packets found to test", vec)
		} else {
			t.Logf("%s: verified %d hybrid packets bit-exact", vec, tested)
		}
	}
}
