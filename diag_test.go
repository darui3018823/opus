package opus_test

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestDiagVector03(t *testing.T) {
	vecDir := filepath.Join("testdata", "opus_newvectors")
	bitPath := filepath.Join(vecDir, "testvector03.bit")
	data, err := os.ReadFile(bitPath)
	if err != nil {
		t.Skip(err)
	}

	// Count packets by type
	pktCount := 0
	var tocSeen [256]int
	code3Total := 0
	code3Frames := 0

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
			continue
		}

		toc := pkt[0]
		tocSeen[toc]++
		cc := int(toc & 3)
		pktCount++

		if cc == 3 && len(pkt) >= 2 {
			code3Total++
			fc := int(pkt[1] & 0x3f)
			code3Frames += fc
		}
	}

	t.Logf("total packets=%d", pktCount)
	for toc, count := range tocSeen {
		if count > 0 {
			config := (toc >> 3) & 0x1f
			cc := toc & 3
			t.Logf("  toc=0x%02x config=%d cc=%d count=%d", toc, config, cc, count)
		}
	}
	t.Logf("code3: total packets=%d total frames encoded=%d", code3Total, code3Frames)

	// dec file size
	decPath := filepath.Join(vecDir, "testvector03.dec")
	decStat, _ := os.Stat(decPath)
	if decStat != nil {
		t.Logf("dec file: %d bytes = %d samples at 8kHz mono", decStat.Size(), decStat.Size()/2)
	}
}

func TestDiagVector01(t *testing.T) {
	vecDir := filepath.Join("testdata", "opus_newvectors")
	bitPath := filepath.Join(vecDir, "testvector01.bit")
	data, err := os.ReadFile(bitPath)
	if err != nil {
		t.Skip(err)
	}

	pktCount := 0
	var tocSeen [256]int
	code3Total := 0
	code3Frames := 0

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
			continue
		}

		toc := pkt[0]
		tocSeen[toc]++
		cc := int(toc & 3)
		pktCount++

		if cc == 3 && len(pkt) >= 2 {
			code3Total++
			fc := int(pkt[1] & 0x3f)
			code3Frames += fc
		}
	}

	t.Logf("total packets=%d", pktCount)
	for toc, count := range tocSeen {
		if count > 0 {
			config := (toc >> 3) & 0x1f
			cc := toc & 3
			t.Logf("  toc=0x%02x config=%d cc=%d count=%d", toc, config, cc, count)
		}
	}
	t.Logf("code3: total packets=%d total frames encoded=%d", code3Total, code3Frames)

	decPath := filepath.Join(vecDir, "testvector01.dec")
	decStat, _ := os.Stat(decPath)
	if decStat != nil {
		t.Logf("dec file: %d bytes = %d samples at 48kHz stereo = %d per channel", decStat.Size(), decStat.Size()/2, decStat.Size()/4)
	}
}

func TestSilkFrameStructure(t *testing.T) {
	vecDir := filepath.Join("testdata", "opus_newvectors")

	// For each vector, determine what modes exist and how many samples expected
	type vecInfo struct {
		num   int
		rate  int
		chans int
	}
	vecs := []vecInfo{
		{1, 48000, 2}, {2, 48000, 1}, {3, 8000, 1}, {4, 8000, 1},
		{5, 8000, 1}, {6, 8000, 2}, {7, 16000, 2}, {8, 16000, 2},
		{9, 16000, 1}, {10, 8000, 1}, {11, 48000, 1}, {12, 24000, 1},
	}

	for _, v := range vecs {
		bitPath := filepath.Join(vecDir, fmt.Sprintf("testvector%02d.bit", v.num))
		data, err := os.ReadFile(bitPath)
		if err != nil {
			continue
		}

		decPath := filepath.Join(vecDir, fmt.Sprintf("testvector%02d.dec", v.num))
		decStat, _ := os.Stat(decPath)
		if decStat == nil {
			continue
		}
		refSamples := int(decStat.Size()) / 2 // 16-bit samples (total including all channels)

		// Now count using correct frame duration from config
		totalSamples := 0
		pktCount := 0
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
				continue
			}

			toc := pkt[0]
			config := int((toc >> 3) & 0x1f)
			cc := int(toc & 3)

			// Determine frame duration in ms
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
			} else {
				switch config & 3 {
				case 0:
					frameMs = 2
				case 1:
					frameMs = 5
				case 2:
					frameMs = 10
				case 3:
					frameMs = 20
				}
			}

			// Number of Opus frames in this packet
			var nFrames int
			switch cc {
			case 0:
				nFrames = 1
			case 1:
				nFrames = 2
			case 2:
				nFrames = 2
			case 3:
				if len(pkt) >= 2 {
					nFrames = int(pkt[1] & 0x3f)
				}
			}
			if nFrames < 1 {
				nFrames = 1
			}

			// Total samples this packet contributes
			// Each Opus frame produces frameMs*rate/1000 samples per channel
			samplesPerFrame := frameMs * v.rate / 1000
			totalSamples += nFrames * samplesPerFrame * v.chans
			pktCount++
		}

		t.Logf("Vector %02d (rate=%d ch=%d): pkts=%d computed=%d ref=%d diff=%d (%.1f%%)",
			v.num, v.rate, v.chans, pktCount, totalSamples, refSamples, refSamples-totalSamples,
			100.0*float64(refSamples-totalSamples)/float64(refSamples))
	}
}
