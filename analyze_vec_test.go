package opus_test

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// silkConfigFrameMs returns the frame duration in ms for SILK configs 0-11.
func silkConfigFrameMs(config int) int {
	// config & 3 gives: 0=10ms, 1=20ms, 2=40ms, 3=60ms
	switch config & 3 {
	case 0:
		return 10
	case 1:
		return 20
	case 2:
		return 40
	case 3:
		return 60
	}
	return 20
}

// silkConfigRateKHz returns the SILK sample rate in kHz for a given config.
func silkConfigRateKHz(config int) int {
	switch {
	case config < 4:
		return 8 // NB
	case config < 8:
		return 12 // MB
	case config < 12:
		return 16 // WB
	case config < 16:
		return 16 // Hybrid (SILK at 16kHz)
	default:
		return 0 // CELT-only
	}
}

// computeExpectedSamples computes the expected number of output samples at outRate
// for one Opus packet with the given TOC and payload.
func computeExpectedSamples(pkt []byte, outRate int) int {
	if len(pkt) < 1 {
		return 0
	}
	toc := pkt[0]
	config := int((toc >> 3) & 0x1f)
	cc := int(toc & 3)

	// Determine Opus frame count
	var nOpusFrames int
	if cc < 3 {
		if cc == 0 {
			nOpusFrames = 1
		} else {
			nOpusFrames = 2
		}
	} else {
		// countCode=3: parse frame count byte
		if len(pkt) >= 2 {
			nOpusFrames = int(pkt[1] & 0x3f)
		}
	}
	if nOpusFrames < 1 {
		nOpusFrames = 1
	}

	// Frame duration
	var frameDurationMs int
	switch {
	case config < 12:
		frameDurationMs = silkConfigFrameMs(config)
	case config < 16:
		// Hybrid
		if config&1 == 0 {
			frameDurationMs = 10
		} else {
			frameDurationMs = 20
		}
	case config < 20:
		// CELT NB
		switch config & 3 {
		case 0:
			frameDurationMs = 2
		case 1:
			frameDurationMs = 5
		case 2:
			frameDurationMs = 10
		case 3:
			frameDurationMs = 20
		}
	case config < 24:
		frameDurationMs = []int{2, 5, 10, 20}[config&3]
	case config < 28:
		frameDurationMs = []int{2, 5, 10, 20}[config&3]
	default:
		frameDurationMs = []int{2, 5, 10, 20}[config&3]
	}

	totalMs := frameDurationMs * nOpusFrames
	return totalMs * outRate / 1000
}

func TestSampleCounts(t *testing.T) {
	// Test that sample counts per packet are reasonable for vector03 (8kHz mono)
	vecDir := filepath.Join("testdata", "opus_newvectors")
	if _, err := os.Stat(vecDir); os.IsNotExist(err) {
		t.Skip("test vectors not found")
	}

	bitPath := filepath.Join(vecDir, "testvector03.bit")
	data, err := os.ReadFile(bitPath)
	if err != nil {
		t.Fatal(err)
	}

	totalSamples := 0
	pktNum := 0
	code3Count := 0
	code3TotalSamples := 0
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
		// expected output at 8kHz mono
		expected := computeExpectedSamples(pkt, 8000)
		totalSamples += expected
		if pktNum < 10 {
			t.Logf("pkt %d: toc=0x%02x config=%d cc=%d expected_samples=%d pktsize=%d", pktNum, toc, config, cc, expected, len(pkt))
		}
		if cc == 3 && len(pkt) >= 2 {
			code3Count++
			code3TotalSamples += expected
			if code3Count <= 5 {
				frameCount := int(pkt[1] & 0x3f)
				t.Logf("code3 pkt %d: toc=0x%02x config=%d payload[0]=0x%02x frameCount=%d pktsize=%d computed_samples=%d",
					pktNum, toc, config, pkt[1], frameCount, len(pkt), expected)
			}
		}
		pktNum++
	}
	t.Logf("total_packets=%d total_expected_samples=%d code3_count=%d code3_total_samples=%d",
		pktNum, totalSamples, code3Count, code3TotalSamples)
	// Reference: 2031360 samples
	t.Logf("reference: 2031360, diff=%d", 2031360-totalSamples)
}

func TestAnalyzeTOC(t *testing.T) {
	vecDir := filepath.Join("testdata", "opus_newvectors")
	if _, err := os.Stat(vecDir); os.IsNotExist(err) {
		t.Skip("test vectors not found")
	}

	vecRates := map[int]int{1: 48000, 2: 48000, 3: 8000, 4: 8000, 5: 8000, 6: 8000, 7: 16000, 8: 16000, 9: 16000, 10: 8000, 11: 48000, 12: 24000}
	vecChans := map[int]int{1: 2, 2: 1, 3: 1, 4: 1, 5: 1, 6: 2, 7: 2, 8: 2, 9: 1, 10: 1, 11: 1, 12: 1}

	for _, num := range []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12} {
		bitPath := filepath.Join(vecDir, fmt.Sprintf("testvector%02d.bit", num))
		if _, err := os.Stat(bitPath); os.IsNotExist(err) {
			continue
		}
		data, err := os.ReadFile(bitPath)
		if err != nil {
			t.Fatalf("read: %v", err)
		}

		outRate := vecRates[num]
		outChans := vecChans[num]
		totalExpected := 0
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
			if len(pkt) > 0 {
				samples := computeExpectedSamples(pkt, outRate)
				totalExpected += samples * outChans
			}
			total++
		}

		// Get actual .dec file size
		decPath := filepath.Join(vecDir, fmt.Sprintf("testvector%02d.dec", num))
		decStat, _ := os.Stat(decPath)
		decSamples := 0
		if decStat != nil {
			decSamples = int(decStat.Size()) / 2 // 16-bit samples
		}
		t.Logf("Vector %02d: total_packets=%d expected_computed=%d dec_file_samples=%d at_rate=%d channels=%d", num, total, totalExpected, decSamples, outRate, outChans)
		t.Logf("  diff=%d (%.1f%%)", decSamples-totalExpected, 100.0*float64(decSamples-totalExpected)/float64(decSamples))
	}
}
