package celt

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// TestPktCategorize categorizes config-31 packets in tv07 by stereo flag and
// whether their final range matches, to test the hypothesis that mismatches
// correlate with untested decode paths (stereo, boost>0) rather than inter-frame
// state. Range is per-packet independent, so a fresh decoder per packet should
// give identical results to a continuous one for range purposes.
func TestPktCategorize(t *testing.T) {
	dir := "../../testdata/opus_newvectors"
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		t.Skip("test vectors not found")
	}
	lmSizes := []int{120, 240, 480, 960}
	bwBands := []int{13, 17, 19, 21}

	path := filepath.Join(dir, "testvector07.bit")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skip("not found:", path)
	}

	off, pkt := 0, 0
	matchMono, mismatchMono, matchStereo, mismatchStereo := 0, 0, 0, 0
	firstMis := -1
	var firstMisInfo string
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
		if config != 31 || code != 0 {
			continue
		}
		ch := 1
		if stereo {
			ch = 2
		}
		lmIdx := config & 3
		bwIdx := (config - 16) / 4
		// fresh decoder per packet — proves range independence of history
		dec, derr := NewDecoderEx(lmSizes[lmIdx], 48000, bwBands[bwIdx], ch)
		if derr != nil {
			t.Fatalf("NewDecoderEx: %v", derr)
		}
		if _, derr := dec.Decode(p[1:]); derr != nil {
			t.Logf("pkt %d decode error: %v", pkt, derr)
			continue
		}
		got := dec.LastFinalRange()
		ok := got == expected
		switch {
		case ok && !stereo:
			matchMono++
		case !ok && !stereo:
			mismatchMono++
		case ok && stereo:
			matchStereo++
		case !ok && stereo:
			mismatchStereo++
		}
		if !ok && firstMis < 0 {
			firstMis = pkt
			firstMisInfo = fmt.Sprintf("pkt=%d stereo=%v size=%d got=%08x want=%08x", pkt, stereo, size, got, expected)
		}
	}
	t.Logf("mono: match=%d mismatch=%d | stereo: match=%d mismatch=%d", matchMono, mismatchMono, matchStereo, mismatchStereo)
	t.Logf("first mismatch (fresh decoder): %s", firstMisInfo)
}
