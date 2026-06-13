package main

import (
	"encoding/binary"
	"fmt"
	"os"
)

func main() {
	for _, fname := range []string{
		`testdata/opus_newvectors/testvector01.bit`,
		`testdata/opus_newvectors/testvector07.bit`,
		`testdata/opus_newvectors/testvector08.bit`,
	} {
		data, _ := os.ReadFile(fname)
		pos := 0
		seen := map[byte]int{}
		frameCount := 0
		for pos+8 <= len(data) {
			size := int(binary.BigEndian.Uint32(data[pos:]))
			pos += 8
			if pos+size > len(data) {
				break
			}
			toc := data[pos]
			seen[toc]++
			frameCount++
			pos += size
		}
		fmt.Printf("--- %s (frames=%d) ---\n", fname, frameCount)
		for toc, cnt := range seen {
			config := (toc >> 3) & 0x1f
			stereo := (toc>>2)&1 != 0
			cc := toc & 0x3
			dur := []string{"2.5ms", "5ms", "10ms", "20ms"}[config&3]
			fmt.Printf("  TOC=0x%02x config=%d dur=%s stereo=%v cc=%d  count=%d\n",
				toc, config, dur, stereo, cc, cnt)
		}
	}
}
