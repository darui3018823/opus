package opus_test

import (
	"encoding/binary"
	"fmt"
	"os"
	"testing"
)

func TestDiagTV07(t *testing.T) {
	data, err := os.ReadFile("testdata/opus_newvectors/testvector07.bit")
	if err != nil { t.Skip(err) }
	// Read first packet
	if len(data) < 8 { t.Fatal("too short") }
	size := int(binary.BigEndian.Uint32(data[:4]))
	// finalRange := binary.BigEndian.Uint32(data[4:8])
	pkt := data[8:8+size]
	fmt.Printf("TV07 pkt0: len=%d bytes\n", len(pkt))
	fmt.Printf("TOC=0x%02x bytes: %x\n", pkt[0], pkt[:min2(len(pkt),20)])
	
	ref, err := os.ReadFile("testdata/opus_newvectors/testvector07.dec")
	if err != nil { t.Skip(err) }
	fmt.Printf("ref len=%d bytes (%d samples)\n", len(ref), len(ref)/2)
	_ = ref
}

func min2(a, b int) int { if a < b { return a }; return b }
