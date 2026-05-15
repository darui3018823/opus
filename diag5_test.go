package opus_test

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	opus "github.com/darui3018823/opus"
)

// TestDecoderCode3Trace traces individual code-3 packet decoding
func TestDecoderCode3Trace(t *testing.T) {
	vecDir := filepath.Join("testdata", "opus_newvectors")
	bitPath := filepath.Join(vecDir, "testvector03.bit")
	data, err := os.ReadFile(bitPath)
	if err != nil {
		t.Skip(err)
	}

	dec, err := opus.NewDecoder(8000, 1)
	if err != nil {
		t.Fatal(err)
	}

	shown := 0
	pktNum := 0
	totalDecoded := 0
	totalExpected := 0
	mismatchCount := 0

	for len(data) > 0 {
		if len(data) < 4 {
			break
		}
		size := binary.BigEndian.Uint32(data[:4])
		data = data[4:]
		if int(size) > len(data) {
			break
		}
		pkt := data[:size]
		data = data[size:]
		if len(data) >= 4 {
			data = data[4:]
		}
		if len(pkt) < 1 {
			pktNum++
			continue
		}

		toc := pkt[0]
		config := int((toc >> 3) & 0x1f)
		cc := int(toc & 3)

		var frameMs int
		if config < 12 {
			switch config & 3 {
			case 0:
				frameMs = 10
			case 1:
				frameMs = 20
			case 2:
				frameMs = 40
			case 3:
				frameMs = 60
			}
		} else if config < 16 {
			if config&1 == 0 {
				frameMs = 10
			} else {
				frameMs = 20
			}
		}

		var expectedSamples int
		switch cc {
		case 0:
			expectedSamples = frameMs * 8000 / 1000
		case 1:
			expectedSamples = 2 * frameMs * 8000 / 1000
		case 2:
			expectedSamples = 2 * frameMs * 8000 / 1000
		case 3:
			if len(pkt) >= 2 {
				nf := int(pkt[1] & 0x3f)
				if nf < 1 {
					nf = 1
				}
				expectedSamples = nf * frameMs * 8000 / 1000
			} else {
				expectedSamples = frameMs * 8000 / 1000
			}
		}

		// Decode
		pcm := make([]int16, 120000) // large buffer
		n, err2 := dec.Decode(pkt, pcm)
		if err2 != nil && shown < 5 {
			t.Logf("pkt %d error: %v", pktNum, err2)
		}

		totalDecoded += n
		totalExpected += expectedSamples

		if n != expectedSamples && cc == 3 {
			mismatchCount++
			if shown < 20 {
				frameCount := 0
				vbr := false
				padding := false
				if len(pkt) >= 2 {
					frameCount = int(pkt[1] & 0x3f)
					vbr = (pkt[1] & 0x80) != 0
					padding = (pkt[1] & 0x40) != 0
				}
				t.Logf("MISMATCH pkt %d: toc=0x%02x config=%d cc=3 frameCount=%d vbr=%v pad=%v pktSize=%d decoded=%d expected=%d",
					pktNum, toc, config, frameCount, vbr, padding, len(pkt), n, expectedSamples)
				shown++
			}
		}
		pktNum++
	}

	t.Logf("total: decoded=%d expected=%d ref=2031360", totalDecoded, totalExpected)
	t.Logf("cc=3 mismatches: %d", mismatchCount)
	fmt.Sprintf("done")
}
