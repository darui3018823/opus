package opus_test

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// TestDetailedVectorAnalysis does a careful per-packet analysis
func TestDetailedVectorAnalysis(t *testing.T) {
	vecDir := filepath.Join("testdata", "opus_newvectors")

	type vecInfo struct {
		num   int
		rate  int
		chans int
	}
	vecs := []vecInfo{
		{3, 8000, 1}, {5, 8000, 1},
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
		refSamples := int(decStat.Size()) / 2

		totalSamples := 0
		pktCount := 0
		byConfig := make(map[int]int) // config -> total samples
		byCC := make(map[int]int)     // cc -> count
		code3FrameHist := make(map[int]int) // frame_count -> count of such packets

		origData := make([]byte, len(data))
		copy(origData, data)

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
					if nFrames < 1 {
						nFrames = 1
					}
					code3FrameHist[nFrames]++
				}
			}
			if nFrames < 1 {
				nFrames = 1
			}

			samplesPerFrame := frameMs * v.rate / 1000
			contrib := nFrames * samplesPerFrame * v.chans
			totalSamples += contrib
			byConfig[config] += contrib
			byCC[cc]++
			pktCount++
		}

		t.Logf("Vector %02d (rate=%d ch=%d): pkts=%d computed=%d ref=%d diff=%d",
			v.num, v.rate, v.chans, pktCount, totalSamples, refSamples, refSamples-totalSamples)

		// Show contribution by config
		for cfg := 0; cfg < 16; cfg++ {
			if byConfig[cfg] > 0 {
				var frameMs int
				if cfg < 12 {
					switch cfg & 3 {
					case 0: frameMs = 10
					case 1: frameMs = 20
					case 2: frameMs = 40
					case 3: frameMs = 60
					}
				} else {
					if cfg&1 == 0 { frameMs = 10 } else { frameMs = 20 }
				}
				t.Logf("  config=%d (SILK %dms): contributed %d samples", cfg, frameMs, byConfig[cfg])
			}
		}

		// code3 frame histogram
		t.Logf("  code-3 frame counts histogram:")
		for fc := 1; fc <= 48; fc++ {
			if code3FrameHist[fc] > 0 {
				t.Logf("    frameCount=%d: %d packets", fc, code3FrameHist[fc])
			}
		}
	}
}
