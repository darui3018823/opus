package opus_test

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	opus "github.com/darui3018823/opus"
)

// TestDecoderSampleCount traces exactly where sample count goes wrong
func TestDecoderSampleCount(t *testing.T) {
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

	var byCC [4]int
	var byCCExpected [4]int
	total := 0
	totalExpected := 0
	pktNum := 0
	errCount := 0

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

		// Compute expected samples
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
		pcm := make([]int16, 48000*2) // large enough
		n, err2 := dec.Decode(pkt, pcm)
		if err2 != nil {
			errCount++
			if errCount <= 5 {
				t.Logf("pkt %d decode error: %v (toc=0x%02x config=%d cc=%d)", pktNum, err2, toc, config, cc)
			}
		}

		total += n
		byCC[cc] += n
		byCCExpected[cc] += expectedSamples
		totalExpected += expectedSamples
		pktNum++
	}

	t.Logf("decode errors: %d", errCount)
	t.Logf("total decoded=%d, total expected=%d, ref=2031360", total, totalExpected)
	for cc := 0; cc < 4; cc++ {
		t.Logf("  cc=%d: decoded=%d expected=%d diff=%d",
			cc, byCC[cc], byCCExpected[cc], byCCExpected[cc]-byCC[cc])
	}
	fmt.Sprintf("done")
}
