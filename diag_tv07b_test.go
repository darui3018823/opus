package opus_test

import (
	"encoding/binary"
	"fmt"
	"os"
	"testing"
)

func TestDiagTV07b(t *testing.T) {
	data, _ := os.ReadFile("testdata/opus_newvectors/testvector07.bit")
	total := 0
	nPkts := 0
	configs := map[byte]int{}
	stereos := map[bool]int{}
	countcodes := map[byte]int{}
	pos := 0
	for pos+8 <= len(data) {
		size := int(binary.BigEndian.Uint32(data[pos:]))
		pos += 8
		if pos+size > len(data) {
			break
		}
		toc := data[pos]
		config := (toc >> 3) & 0x1f
		stereo := (toc>>2)&1 != 0
		cc := toc & 3
		configs[config]++
		stereos[stereo]++
		countcodes[cc]++
		nPkts++
		pos += size
	}
	fmt.Printf("Total bytes: %d, packets: %d\n", total, nPkts)
	fmt.Printf("configs: %v\n", configs)
	fmt.Printf("stereos: %v\n", stereos)
	fmt.Printf("countcodes: %v\n", countcodes)

	// Show first 5 packets
	pos = 0
	for i := 0; i < 5 && pos+8 <= len(data); i++ {
		size := int(binary.BigEndian.Uint32(data[pos:]))
		final := binary.BigEndian.Uint32(data[pos+4:])
		pkt := data[pos+8 : pos+8+size]
		toc := pkt[0]
		fmt.Printf("pkt%d: size=%d final=0x%08x toc=0x%02x (config=%d stereo=%v cc=%d)\n",
			i, size, final, toc, (toc>>3)&0x1f, (toc>>2)&1 != 0, toc&3)
		pos += 8 + size
	}
}
