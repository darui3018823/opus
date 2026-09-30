//go:build opusref

package opus

import (
	"encoding/binary"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"testing"

	"github.com/darui3018823/opus/internal/cgoref"
)

// TestCGOPacketParseMatchesLibopus compares PacketParse with libopus
// opus_packet_parse on every official-vector packet and on random and
// truncated packets of every framing code.
func TestCGOPacketParseMatchesLibopus(t *testing.T) {
	var packets [][]byte
	for v := 1; v <= 12; v++ {
		data, err := os.ReadFile(filepath.Join("testdata", "opus_newvectors", fmt.Sprintf("testvector%02d.bit", v)))
		if err != nil {
			continue
		}
		for pos := 0; pos+8 <= len(data); {
			n := int(binary.BigEndian.Uint32(data[pos:]))
			pos += 8
			if n > 0 {
				packets = append(packets, data[pos:pos+n])
			}
			pos += n
		}
	}
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < 20000; i++ {
		p := make([]byte, rng.Intn(40))
		rng.Read(p)
		if len(p) > 1 && rng.Intn(2) == 0 {
			// Bias code 3 headers towards plausible counts and flags.
			p[0] |= 3
			p[1] = byte(rng.Intn(4)+1) | byte(rng.Intn(4))<<6
		}
		packets = append(packets, p)
	}
	for _, size := range []int{1275, 1276, 2550, 2551, 2552} {
		for code := byte(0); code < 4; code++ {
			p := make([]byte, size+1)
			p[0] = 0xf8 | code
			if code == 3 && len(p) > 1 {
				p[1] = 2
			}
			packets = append(packets, p)
		}
	}
	packets = append(packets, []byte{})

	for i, p := range packets {
		ret, ctoc, csizes, coff := cgoref.PacketParse(p)
		toc, frames, off, err := PacketParse(p)
		if ret < 0 {
			if err == nil {
				t.Fatalf("packet %d (%x): libopus rejects (%d), Go accepts", i, p, ret)
			}
			continue
		}
		if err != nil {
			t.Fatalf("packet %d (%x): libopus accepts, Go: %v", i, p, err)
		}
		if toc != ctoc || off != coff || len(frames) != len(csizes) {
			t.Fatalf("packet %d: Go toc %#x off %d frames %d, libopus %#x %d %d", i, toc, off, len(frames), ctoc, coff, len(csizes))
		}
		pos := off
		for f, frame := range frames {
			if len(frame) != csizes[f] {
				t.Fatalf("packet %d frame %d: Go %d bytes, libopus %d", i, f, len(frame), csizes[f])
			}
			if len(frame) > 0 && &frame[0] != &p[pos] {
				t.Fatalf("packet %d frame %d does not alias the packet at %d", i, f, pos)
			}
			pos += len(frame)
		}
	}
}
