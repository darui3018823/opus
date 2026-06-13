package opus_test

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// TestCode3PacketDetails examines what code-3 packets actually contain
func TestCode3PacketDetails(t *testing.T) {
	vecDir := filepath.Join("testdata", "opus_newvectors")
	bitPath := filepath.Join(vecDir, "testvector03.bit")
	data, err := os.ReadFile(bitPath)
	if err != nil {
		t.Skip(err)
	}

	shown := 0
	pktNum := 0
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
		stereo := (toc >> 2) & 1

		if cc == 3 && shown < 10 {
			frameCount := 0
			vbr := false
			padding := false
			if len(pkt) >= 2 {
				frameCount = int(pkt[1] & 0x3f)
				vbr = (pkt[1] & 0x80) != 0
				padding = (pkt[1] & 0x40) != 0
			}
			payloadSize := len(pkt) - 2 // after toc byte and code-3 byte
			if payloadSize < 0 {
				payloadSize = 0
			}
			t.Logf("pkt %d: toc=0x%02x config=%d stereo=%d cc=3 frameCount=%d vbr=%v padding=%v pktSize=%d payloadAfterCode3=%d bytesPerFrame=%.1f",
				pktNum, toc, config, stereo, frameCount, vbr, padding, len(pkt), payloadSize,
				float64(payloadSize)/float64(max3(frameCount, 1)))
			shown++
		}
		pktNum++
	}

	// Also show what actual ref count for code-3 vs code-0 contributions are
	data2, _ := os.ReadFile(bitPath)
	var code0Samples, code1Samples, code2Samples, code3Samples int
	rate := 8000

	for len(data2) > 0 {
		if len(data2) < 4 {
			break
		}
		size := binary.BigEndian.Uint32(data2[:4])
		data2 = data2[4:]
		if int(size) > len(data2) {
			break
		}
		pkt := data2[:size]
		data2 = data2[size:]
		if len(data2) >= 4 {
			data2 = data2[4:]
		}
		if len(pkt) < 1 {
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

		samplesPerFrame := frameMs * rate / 1000
		switch cc {
		case 0:
			code0Samples += samplesPerFrame
		case 1:
			code1Samples += 2 * samplesPerFrame
		case 2:
			code2Samples += 2 * samplesPerFrame
		case 3:
			if len(pkt) >= 2 {
				nf := int(pkt[1] & 0x3f)
				if nf < 1 {
					nf = 1
				}
				code3Samples += nf * samplesPerFrame
			} else {
				code3Samples += samplesPerFrame
			}
		}
	}

	t.Logf("code0=%d code1=%d code2=%d code3=%d total=%d ref=2031360",
		code0Samples, code1Samples, code2Samples, code3Samples,
		code0Samples+code1Samples+code2Samples+code3Samples)
}

func max3(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// TestHybridSampleCount - investigate whether hybrid mode packets contribute correctly
func TestHybridSampleCount(t *testing.T) {
	vecDir := filepath.Join("testdata", "opus_newvectors")
	bitPath := filepath.Join(vecDir, "testvector03.bit")
	data, err := os.ReadFile(bitPath)
	if err != nil {
		t.Skip(err)
	}

	// Try: code-3 packets contribute 1 frame worth of samples each (not N frames)
	// That would mean the code-3 byte doesn't mean N Opus frames but something else
	var totalV1, totalV2 int
	rate := 8000

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

		samplesPerFrame := frameMs * rate / 1000

		switch cc {
		case 0:
			totalV1 += samplesPerFrame
			totalV2 += samplesPerFrame
		case 1:
			totalV1 += 2 * samplesPerFrame
			totalV2 += 2 * samplesPerFrame
		case 2:
			totalV1 += 2 * samplesPerFrame
			totalV2 += 2 * samplesPerFrame
		case 3:
			// V1: use frame count from payload
			if len(pkt) >= 2 {
				nf := int(pkt[1] & 0x3f)
				if nf < 1 {
					nf = 1
				}
				totalV1 += nf * samplesPerFrame
			} else {
				totalV1 += samplesPerFrame
			}
			// V2: treat as 1 Opus frame (code-3 byte is SILK-specific, not Opus frame count)
			totalV2 += samplesPerFrame
		}
	}

	t.Logf("V1 (code-3=N frames): computed=%d ref=2031360 diff=%d", totalV1, 2031360-totalV1)
	t.Logf("V2 (code-3=1 frame):  computed=%d ref=2031360 diff=%d", totalV2, 2031360-totalV2)
	t.Logf("")
	t.Logf("Analysis: if code-3 is 1 Opus frame, what could make up the difference?")
	t.Logf("  Diff for V2: %d samples = %.1f seconds at 8kHz", 2031360-totalV2, float64(2031360-totalV2)/8000.0)

	// Count code-3 packets
	data2, _ := os.ReadFile(bitPath)
	code3Count := 0
	code3Avg := 0
	for len(data2) > 0 {
		if len(data2) < 4 {
			break
		}
		size := binary.BigEndian.Uint32(data2[:4])
		data2 = data2[4:]
		if int(size) > len(data2) {
			break
		}
		pkt := data2[:size]
		data2 = data2[size:]
		if len(data2) >= 4 {
			data2 = data2[4:]
		}
		if len(pkt) < 1 {
			continue
		}
		if int(pkt[0]&3) == 3 && len(pkt) >= 2 {
			code3Count++
			code3Avg += int(pkt[1] & 0x3f)
		}
	}
	if code3Count > 0 {
		t.Logf("code3Count=%d, total code-3 frames=%d, avg frames per packet=%.1f",
			code3Count, code3Avg, float64(code3Avg)/float64(code3Count))
	}
}

func TestV03SILKStreamAnalysis(t *testing.T) {
	// For code-3 SILK packets: the entire payload after the code-3 byte
	// is ONE SILK range-coded stream encoding N frames.
	// Check if this interpretation gives the correct sample count.
	vecDir := filepath.Join("testdata", "opus_newvectors")
	bitPath := filepath.Join(vecDir, "testvector03.bit")
	data, err := os.ReadFile(bitPath)
	if err != nil {
		t.Skip(err)
	}

	// Theory: all code values (0,1,2,3) for SILK = ONE SILK stream per packet
	// Code-0: 1 Opus frame → nFrames in SILK stream = 1
	// Code-1: 2 Opus frames → nFrames in SILK stream = 2
	// Code-2: 2 Opus frames (two separate SILK streams, split by size prefix)
	// Code-3: N Opus frames → nFrames in SILK stream = N (from payload[0]&0x3f)
	// All frames per stream contribute frameMs samples each
	rate := 8000
	total := 0

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

		samplesPerFrame := frameMs * rate / 1000

		switch cc {
		case 0:
			total += samplesPerFrame
		case 1:
			total += 2 * samplesPerFrame
		case 2:
			total += 2 * samplesPerFrame
		case 3:
			if len(pkt) >= 2 {
				nf := int(pkt[1] & 0x3f)
				if nf < 1 {
					nf = 1
				}
				total += nf * samplesPerFrame
			} else {
				total += samplesPerFrame
			}
		}
	}

	t.Logf("Total computed samples=%d ref=2031360 diff=%d (%.2f%%)",
		total, 2031360-total, 100.0*float64(2031360-total)/2031360.0)

	// Let's also look at what happens if the 10ms config decode uses subframe-per-frame=1
	// vs if we treat every decoded output as a single 20ms subframe
	fmt.Sprintf("done") // prevent unused import
}
