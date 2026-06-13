package opus_test

import (
	"encoding/binary"
	"fmt"
	"os"
	"testing"

	"github.com/darui3018823/opus/internal/celt"
	"github.com/darui3018823/opus/internal/entcode"
)

func TestFinalRange(t *testing.T) {
	data, err := os.ReadFile("testdata/opus_newvectors/testvector07.bit")
	if err != nil { t.Skip(err) }
	
	// Read first 5 packets
	pos := 0
	for pi := 0; pi < 5 && pos+8 <= len(data); pi++ {
		size := int(binary.BigEndian.Uint32(data[pos:]))
		finalRange := binary.BigEndian.Uint32(data[pos+4:])
		pkt := data[pos+8:pos+8+size]
		pos += 8 + size
		
		toc := pkt[0]
		config := (toc >> 3) & 0x1f
		stereo := (toc>>2)&1 != 0
		pktChannels := 1
		if stereo { pktChannels = 2 }
		
		// Skip non-CELT
		if config < 16 { 
			fmt.Printf("pkt%d: SILK/Hybrid, skip\n", pi)
			continue 
		}
		
		frameData := pkt[1:]
		bwIdx := (int(config) - 16) / 4
		lmIdx := int(config) & 3
		lmSizes := []int{120, 240, 480, 960}
		bwBands := []int{13, 17, 19, 21}
		
		dec, _ := celt.NewDecoderEx(lmSizes[lmIdx], 48000, bwBands[bwIdx], pktChannels)
		_ = dec
		
		// Manually create range decoder and check final range
		rd := entcode.NewDecoder(frameData)
		// Just read to exhaust the bitstream...
		// Actually we want to see what our decoder's rng is after full decode
		_ = rd
		
		// Use the actual CELT decode
		dec2, _ := celt.NewDecoderEx(lmSizes[lmIdx], 48000, bwBands[bwIdx], pktChannels)
		pcm, _ := dec2.Decode(frameData)
		
		// Get the final range from decoder
		// We need to expose the range coder state... let's just check Tell
		fmt.Printf("pkt%d: config=%d ch=%d bytes=%d expected_final=0x%08x pcm_len=%d\n",
			pi, config, pktChannels, len(frameData), finalRange, len(pcm))
	}
}
