package celt

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// TestRangeVectors decodes every packet of each CELT-only test vector with our
// CELT decoder and checks the final range coder value against the per-packet
// expected value stored in the opus_demo .bit file. A match proves the entropy
// decode (the whole CELT bit-reading path) is bit-exact with libopus.
//
// Packets whose TOC is not CELT-only (config<16: SILK/hybrid) are skipped, as
// are packets whose config changes the (bandwidth, LM, channels) — we create a
// decoder for the first CELT config seen and only check packets matching it.
func TestRangeVectors(t *testing.T) {
	dir := "../../testdata/opus_newvectors"
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		t.Skip("test vectors not found")
	}

	lmSizes := []int{120, 240, 480, 960}
	bwBands := []int{13, 17, 19, 21}

	for n := 1; n <= 12; n++ {
		n := n
		t.Run(fmt.Sprintf("tv%02d", n), func(t *testing.T) {
			path := filepath.Join(dir, fmt.Sprintf("testvector%02d.bit", n))
			data, err := os.ReadFile(path)
			if err != nil {
				t.Skip("not found:", path)
			}

			// The CELT final range is independent of inter-frame state (each packet
			// re-inits the range coder; coarse-energy prediction does not feed the
			// entropy path). So we validate every CELT-only single-frame packet with
			// a decoder configured for that packet's (config, channels), caching one
			// decoder per (config, ch) key.
			type key struct{ cfg, ch int }
			decs := map[key]*Decoder{}
			var total, matched, celtPkts, skipped int

			off := 0
			pkt := 0
			for off+8 <= len(data) {
				size := int(binary.BigEndian.Uint32(data[off:]))
				expected := binary.BigEndian.Uint32(data[off+4:])
				if off+8+size > len(data) || size < 1 {
					break
				}
				p := data[off+8 : off+8+size]
				off += 8 + size
				pkt++

				toc := p[0]
				config := int((toc >> 3) & 0x1f)
				stereo := (toc>>2)&1 != 0
				code := toc & 3
				if config < 16 || code != 0 {
					skipped++
					continue // not CELT-only single-frame
				}
				ch := 1
				if stereo {
					ch = 2
				}
				lmIdx := config & 3
				bwIdx := (config - 16) / 4
				numBands := bwBands[bwIdx]

				k := key{config, ch}
				dec := decs[k]
				if dec == nil {
					dec, err = NewDecoderEx(lmSizes[lmIdx], 48000, numBands, ch)
					if err != nil {
						t.Fatalf("NewDecoderEx: %v", err)
					}
					decs[k] = dec
				}

				celtPkts++
				total++
				if _, err := dec.Decode(p[1:]); err != nil {
					t.Logf("pkt %d decode error: %v", pkt, err)
					continue
				}
				got := dec.LastFinalRange()
				if got == expected {
					matched++
				} else if total-matched <= 5 {
					t.Logf("pkt %d: cfg=%d ch=%d range mismatch got=%08x want=%08x", pkt, config, ch, got, expected)
				}
			}

			t.Logf("celtPkts=%d checked=%d matched=%d skipped=%d", celtPkts, total, matched, skipped)
			if total > 0 && matched != total {
				t.Errorf("not all CELT packets range-exact: %d/%d", matched, total)
			}
		})
	}
}
