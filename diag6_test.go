package opus_test

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

// TestExamineV03Bytes looks at the raw bytes to understand the format
func TestExamineV03Bytes(t *testing.T) {
	vecDir := filepath.Join("testdata", "opus_newvectors")
	bitPath := filepath.Join(vecDir, "testvector03.bit")
	data, err := os.ReadFile(bitPath)
	if err != nil {
		t.Skip(err)
	}

	t.Logf("File size: %d bytes", len(data))
	t.Logf("First 64 bytes (hex):")
	for i := 0; i < 64 && i < len(data); i += 8 {
		end := i + 8
		if end > len(data) {
			end = len(data)
		}
		t.Logf("  %04d: %x", i, data[i:end])
	}

	// How does the file start? Is there a header?
	// Try interpreting first 4 bytes as packet size
	if len(data) >= 4 {
		size0 := binary.BigEndian.Uint32(data[:4])
		t.Logf("First uint32-BE (possible packet 0 size): %d", size0)
		t.Logf("  → pkt bytes: %x", data[4:4+min6(int(size0), 32)])
	}

	// Let's try a different format: maybe the file starts with sample rate, frame count, etc.
	// like what opusdec produces
	if len(data) >= 12 {
		t.Logf("Interpreting first 12 bytes as header:")
		t.Logf("  uint32[0]=%d uint32[1]=%d uint32[2]=%d",
			binary.BigEndian.Uint32(data[0:4]),
			binary.BigEndian.Uint32(data[4:8]),
			binary.BigEndian.Uint32(data[8:12]))
		t.Logf("  uint32-LE[0]=%d uint32-LE[1]=%d uint32-LE[2]=%d",
			binary.LittleEndian.Uint32(data[0:4]),
			binary.LittleEndian.Uint32(data[4:8]),
			binary.LittleEndian.Uint32(data[8:12]))
	}

	// Count total bytes used by packets in the current format
	totalPacketBytes := 0
	pktCount := 0
	d := data
	for len(d) > 0 {
		if len(d) < 4 {
			t.Logf("Trailing bytes: %d", len(d))
			break
		}
		size := binary.BigEndian.Uint32(d[:4])
		d = d[4:]
		if int(size) > len(d) {
			t.Logf("Packet %d: size=%d > remaining=%d, stopping", pktCount, size, len(d))
			break
		}
		d = d[size:]
		if len(d) >= 4 {
			d = d[4:] // final_range
		}
		totalPacketBytes += 4 + int(size) + 4
		pktCount++
	}
	t.Logf("Packets parsed: %d, bytes used: %d/%d", pktCount, totalPacketBytes, len(data))
}

func min6(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// TestDurationCheck: try to figure out the expected total audio duration
func TestDurationCheck(t *testing.T) {
	// reference dec file sizes for 8kHz, 1 channel:
	// vector03.dec = 4062720 bytes = 2031360 int16 samples
	// At 8kHz: 2031360/8000 = 253.92 seconds

	// The test stream has 998 packets. Average duration = 253.92/998 = 0.254 seconds = 254ms per packet.
	// That means each packet averages 254ms.
	// At config-0 (10ms NB), that's 25.4 frames per packet.
	// At code-3 with frameCount=25 for 10ms: 25 × 10ms = 250ms. Close.

	// My analysis gives 1506240 samples = 188.28 seconds.
	// Reference: 253.92 seconds. Delta: 65.64 seconds.

	// How many EXTRA packets would give 65.64 seconds?
	// At 20ms/frame: 65.64/0.02 = 3282 frames extra.
	// At 10ms/frame: 65.64/0.01 = 6564 frames extra.

	// Could this be from LBRR frames being decoded?
	// If each packet has 1 LBRR frame (20ms), 998 packets × 20ms = 19.96 seconds. Not enough.
	// If each packet has ~3 LBRR frames, 998 × 3 × 20ms ≈ 59.88 seconds. Close to 65.64!

	// But LBRR frames are for packet loss concealment, not regular output.

	// Let me check: maybe the run_vectors.sh uses opus_demo with a different sample count
	// or the first packet is actually a header that contains the total frame count.

	t.Logf("Analysis:")
	t.Logf("  ref samples: 2031360, my analysis: 1506240")
	t.Logf("  difference: %d samples = %.2f seconds at 8kHz", 2031360-1506240, float64(2031360-1506240)/8000.0)
	t.Logf("  998 packets, avg packet duration: %.2f ms (ref)", float64(2031360)/8000.0/998.0*1000.0)
	t.Logf("  998 packets, avg packet duration: %.2f ms (my analysis)", float64(1506240)/8000.0/998.0*1000.0)
}
