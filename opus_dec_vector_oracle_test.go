//go:build opusref

package opus

import (
	"encoding/binary"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
)

// TestDecoderVectorOracle decodes the official test vectors with the Go
// decoder and with libopus's plain-C float decoder (the encoder oracle's
// --dec-bit mode, opus_demo's decode loop) and compares the int16 PCM
// sample by sample, at 48 kHz and at lower output rates.
func TestDecoderVectorOracle(t *testing.T) {
	t.Parallel()
	if _, err := os.Stat(encOraclePath()); err != nil {
		t.Skip("encoder oracle not built")
	}
	outputs := []struct{ rate, channels int }{{48000, 2}, {48000, 1}, {24000, 2}, {16000, 1}, {8000, 2}}
	for v := 1; v <= 12; v++ {
		bit := filepath.Join("testdata", "opus_newvectors", fmt.Sprintf("testvector%02d.bit", v))
		if _, err := os.Stat(bit); err != nil {
			t.Skipf("test vectors not present: %v", err)
		}
		for _, o := range outputs {
			t.Run(fmt.Sprintf("tv%02d/%dk/%dch", v, o.rate/1000, o.channels), func(t *testing.T) {
				t.Parallel()
				raw := filepath.Join(t.TempDir(), "ref.raw")
				out, err := exec.Command(encOraclePath(), "--dec-bit", bit, strconv.Itoa(o.rate), strconv.Itoa(o.channels), raw).CombinedOutput()
				if err != nil {
					t.Fatalf("oracle: %v\n%s", err, out)
				}
				refBytes, err := os.ReadFile(raw)
				if err != nil {
					t.Fatal(err)
				}
				ref := make([]int16, len(refBytes)/2)
				for i := range ref {
					ref[i] = int16(binary.LittleEndian.Uint16(refBytes[2*i:]))
				}
				data, err := os.ReadFile(bit)
				if err != nil {
					t.Fatal(err)
				}
				dec, err := NewDecoder(o.rate, o.channels)
				if err != nil {
					t.Fatal(err)
				}
				var got []int16
				pcm := make([]int16, 5760*o.channels)
				packets := 0
				for pos := 0; pos+8 <= len(data); packets++ {
					n := int(binary.BigEndian.Uint32(data[pos:]))
					pos += 8
					pkt := data[pos : pos+n]
					pos += n
					var samples int
					if n == 0 {
						samples, err = dec.DecodePLC(pcm, dec.GetLastPacketDuration())
					} else {
						samples, err = dec.Decode(pkt, pcm)
					}
					if err != nil {
						t.Fatalf("packet %d: %v", packets, err)
					}
					got = append(got, pcm[:samples*o.channels]...)
				}
				if len(got) != len(ref) {
					t.Fatalf("Go %d samples, libopus %d", len(got), len(ref))
				}
				exact, first := 0, -1
				for i := range got {
					if got[i] == ref[i] {
						exact++
					} else if first < 0 {
						first = i
					}
				}
				if exact != len(got) {
					t.Errorf("%d/%d samples exact (%.3f%%), first difference at sample %d (packet ~%d)",
						exact, len(got), 100*float64(exact)/float64(len(got)), first/o.channels,
						first/o.channels/(o.rate/50))
				}
			})
		}
	}
}
