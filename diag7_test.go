package opus_test

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

// TestCode3ByConfig breaks down code-3 packets by config
func TestCode3ByConfig(t *testing.T) {
	vecDir := filepath.Join("testdata", "opus_newvectors")
	bitPath := filepath.Join(vecDir, "testvector03.bit")
	data, err := os.ReadFile(bitPath)
	if err != nil {
		t.Skip(err)
	}

	// Per config: total M values and expected samples
	byConfig := make(map[int][2]int64) // config -> [totalM, totalSamples]
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
		if cc != 3 {
			continue
		}
		if len(pkt) < 2 {
			continue
		}

		M := int(pkt[1] & 0x3f)
		if M < 1 {
			M = 1
		}

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
		entry := byConfig[config]
		entry[0] += int64(M)
		entry[1] += int64(M * samplesPerFrame)
		byConfig[config] = entry
	}

	total := int64(0)
	for config := 0; config < 16; config++ {
		entry := byConfig[config]
		if entry[0] == 0 {
			continue
		}
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
		t.Logf("config=%d (%dms): M_total=%d samples=%d", config, frameMs, entry[0], entry[1])
		total += entry[1]
	}
	t.Logf("total code-3 samples (my calc): %d", total)
	t.Logf("reference code-3 would be: %d", 2031360-204560)
	t.Logf("ratio: %.4f", float64(2031360-204560)/float64(total))
}

// TestTOCBreakdown - breakdown by config × cc
func TestTOCBreakdown(t *testing.T) {
	vecDir := filepath.Join("testdata", "opus_newvectors")
	bitPath := filepath.Join(vecDir, "testvector03.bit")
	data, err := os.ReadFile(bitPath)
	if err != nil {
		t.Skip(err)
	}

	type key struct {
		config int
		cc     int
		stereo int
	}
	type entry struct {
		count   int
		samples int
	}
	byKey := make(map[key]*entry)
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
		stereo := int((toc >> 2) & 1)
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
			} else {
				nFrames = 1
			}
		}

		k := key{config, cc, stereo}
		e := byKey[k]
		if e == nil {
			e = &entry{}
			byKey[k] = e
		}
		e.count++
		e.samples += nFrames * frameMs * rate / 1000
	}

	total := 0
	for k, e := range byKey {
		var frameMs int
		if k.config < 12 {
			switch k.config & 3 {
			case 0:
				frameMs = 10
			case 1:
				frameMs = 20
			case 2:
				frameMs = 40
			case 3:
				frameMs = 60
			}
		} else if k.config < 16 {
			if k.config&1 == 0 {
				frameMs = 10
			} else {
				frameMs = 20
			}
		}
		_ = frameMs
		t.Logf("config=%d stereo=%d cc=%d: count=%d samples=%d", k.config, k.stereo, k.cc, e.count, e.samples)
		total += e.samples
	}
	t.Logf("TOTAL: %d, ref: %d", total, 2031360)
}
