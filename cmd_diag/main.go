package main

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
)

func main() {
	vecDir := filepath.Join("testdata", "opus_newvectors")
	for i := 1; i <= 12; i++ {
		bitPath := filepath.Join(vecDir, fmt.Sprintf("testvector%02d.bit", i))
		data, err := os.ReadFile(bitPath)
		if err != nil {
			fmt.Printf("vec%02d: error %v\n", i, err)
			continue
		}
		size := binary.BigEndian.Uint32(data[:4])
		pkt := data[8 : 8+size]
		toc := pkt[0]
		config := (toc >> 3) & 0x1f
		stereo := (toc >> 2) & 1
		framecode := toc & 3
		var mode, rate string
		switch {
		case config < 4:
			mode, rate = "SILK", "NB(8kHz)"
		case config < 8:
			mode, rate = "SILK", "MB(12kHz)"
		case config < 12:
			mode, rate = "SILK", "WB(16kHz)"
		case config < 14:
			mode, rate = "Hybrid", "SWB(24kHz)"
		case config < 16:
			mode, rate = "Hybrid", "FB(48kHz)"
		case config < 20:
			mode, rate = "CELT", "NB(8kHz)"
		case config < 24:
			mode, rate = "CELT", "WB(16kHz)"
		case config < 28:
			mode, rate = "CELT", "SWB(24kHz)"
		default:
			mode, rate = "CELT", "FB(48kHz)"
		}
		ch := 1
		if stereo == 1 {
			ch = 2
		}
		fmt.Printf("vec%02d: config=%2d mode=%-6s rate=%-10s ch=%d framecode=%d\n", i, config, mode, rate, ch, framecode)
	}
}
