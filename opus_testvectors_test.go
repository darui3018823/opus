package opus_test

import (
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"

	opus "github.com/darui3018823/opus"
)

// opusDemoFrame holds one frame from an opus_demo .bit file.
type opusDemoFrame struct {
	packet     []byte
	finalRange uint32
}

// parseOpusDemoBit reads an opus_demo proprietary bitstream file.
// Format per frame as written by opus_demo: uint32-BE size,
// uint32-BE final_range, then size bytes payload.
func parseOpusDemoBit(path string) ([]opusDemoFrame, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var frames []opusDemoFrame
	for len(data) > 0 {
		if len(data) < 4 {
			break
		}
		size := binary.BigEndian.Uint32(data[:4])
		data = data[4:]
		if len(data) < 4 {
			return nil, fmt.Errorf("truncated final range")
		}
		fr := binary.BigEndian.Uint32(data[:4])
		data = data[4:]
		if int(size) > len(data) {
			return nil, fmt.Errorf("truncated packet: need %d, have %d", size, len(data))
		}
		pkt := make([]byte, size)
		copy(pkt, data[:size])
		data = data[size:]
		frames = append(frames, opusDemoFrame{packet: pkt, finalRange: fr})
	}
	return frames, nil
}

// readDecFile reads a .dec file (16-bit signed little-endian PCM) as float64.
func readDecFile(path string) ([]float64, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	n := len(data) / 2
	out := make([]float64, n)
	for i := 0; i < n; i++ {
		v := int16(binary.LittleEndian.Uint16(data[i*2 : i*2+2]))
		out[i] = float64(v) / 32768.0
	}
	return out, nil
}

// opusTOCMode returns "SILK", "Hybrid", or "CELT" for a packet TOC byte.
func opusTOCMode(toc byte) string {
	config := (toc >> 3) & 0x1f
	switch {
	case config < 12:
		return "SILK"
	case config < 16:
		return "Hybrid"
	default:
		return "CELT"
	}
}

// opusTOCSampleRate returns the nominal sample rate from the TOC config field.
func opusTOCSampleRate(toc byte) int {
	config := (toc >> 3) & 0x1f
	switch {
	case config < 4:
		return 8000 // SILK NB
	case config < 8:
		return 12000 // SILK MB
	case config < 12:
		return 16000 // SILK WB
	case config < 14:
		return 24000 // Hybrid SWB
	case config < 16:
		return 48000 // Hybrid FB
	case config < 20:
		return 8000 // CELT NB
	case config < 24:
		return 16000 // CELT WB
	case config < 28:
		return 24000 // CELT SWB
	default:
		return 48000 // CELT FB
	}
}

// opusTOCChannels returns channels from stereo bit.
func opusTOCChannels(toc byte) int {
	if (toc>>2)&1 != 0 {
		return 2
	}
	return 1
}

// TestOfficialVectors tests against the RFC 8251 official Opus test vectors.
// Test vectors must be at testdata/opus_newvectors/ (download separately).
// Packets in CELT-only mode are decoded and compared against reference.
// SILK and Hybrid packets are skipped until those codecs are implemented.
func TestOfficialVectors(t *testing.T) {
	vecDir := filepath.Join("testdata", "opus_newvectors")
	if _, err := os.Stat(vecDir); os.IsNotExist(err) {
		t.Skip("test vectors not found at testdata/opus_newvectors/ — download opus_testvectors-rfc8251.tar.gz")
	}

	type vecCase struct {
		num      int
		channels int
		rate     int
	}
	// RFC 8251 .dec reference files in this tree are 48 kHz stereo PCM.
	cases := []vecCase{
		{1, 2, 48000}, {2, 2, 48000}, {3, 2, 48000}, {4, 2, 48000},
		{5, 2, 48000}, {6, 2, 48000}, {7, 2, 48000}, {8, 2, 48000},
		{9, 2, 48000}, {10, 2, 48000}, {11, 2, 48000}, {12, 2, 48000},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(fmt.Sprintf("testvector%02d", tc.num), func(t *testing.T) {
			bitPath := filepath.Join(vecDir, fmt.Sprintf("testvector%02d.bit", tc.num))
			decPath := filepath.Join(vecDir, fmt.Sprintf("testvector%02d.dec", tc.num))

			if _, err := os.Stat(bitPath); os.IsNotExist(err) {
				t.Skipf("not found: %s", bitPath)
			}

			frames, err := parseOpusDemoBit(bitPath)
			if err != nil {
				t.Fatalf("parse .bit: %v", err)
			}
			if len(frames) == 0 {
				t.Fatal("empty bitstream")
			}

			// Determine mode from first packet TOC.
			firstTOC := frames[0].packet[0]
			mode := opusTOCMode(firstTOC)
			_ = mode // All modes now supported

			// Decode all CELT frames and concatenate PCM.
			dec, err := opus.NewDecoder(tc.rate, tc.channels)
			if err != nil {
				t.Fatalf("NewDecoder: %v", err)
			}

			var decoded []int16
			for i, f := range frames {
				pcm := make([]int16, 5760*tc.channels) // max frame 120ms@48kHz
				n, err := dec.Decode(f.packet, pcm)
				if err != nil {
					t.Fatalf("frame %d Decode: %v", i, err)
				}
				decoded = append(decoded, pcm[:n*tc.channels]...)
			}

			// Load reference.
			ref, err := readDecFile(decPath)
			if err != nil {
				t.Fatalf("read .dec: %v", err)
			}

			if len(decoded) != len(ref) {
				t.Errorf("PCM length mismatch: got %d, want %d", len(decoded), len(ref))
			}

			// Compute RMSE and check within tolerance.
			n := min(len(decoded), len(ref))
			rmse := 0.0
			for i := 0; i < n; i++ {
				d := float64(decoded[i])/32768.0 - ref[i]
				rmse += d * d
			}
			rmse = math.Sqrt(rmse / float64(n))
			maxAbs := 0.0
			sumSq2 := 0.0
			for i := 0; i < n; i++ {
				v := math.Abs(float64(decoded[i]))
				if v > maxAbs {
					maxAbs = v
				}
				sumSq2 += float64(decoded[i]) * float64(decoded[i])
			}
			// Compute ref rms and find max region
			refRmsSq := 0.0
			refMaxAbs := 0.0
			refMaxIdx := 0
			for i := 0; i < n; i++ {
				v := math.Abs(ref[i])
				if v > refMaxAbs {
					refMaxAbs = v
					refMaxIdx = i
				}
				refRmsSq += ref[i] * ref[i]
			}
			t.Logf("ref_rms=%.6f (float) ref_maxAbs=%.6f at idx=%d", math.Sqrt(refRmsSq/float64(n)), refMaxAbs, refMaxIdx)
			if refMaxIdx >= 10 && refMaxIdx+10 < n {
				decodedSlice := make([]int, 20)
				refSlice := make([]float64, 20)
				for i := 0; i < 20; i++ {
					decodedSlice[i] = int(decoded[refMaxIdx-10+i])
					refSlice[i] = ref[refMaxIdx-10+i]
				}
				t.Logf("near ref_max: decoded=%v", decodedSlice)
				t.Logf("near ref_max: ref(float)=%v", refSlice)
			}
			t.Logf("frames=%d PCM=%d RMSE=%.6f decoded_maxAbs=%.1f decoded_rms=%.3f", len(frames), len(decoded), rmse, maxAbs, math.Sqrt(sumSq2/float64(n)))
			if rmse > 0.001 {
				t.Errorf("RMSE %.6f exceeds 0.001 threshold", rmse)
			}
		})
	}
}
