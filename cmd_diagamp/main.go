package main

import (
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"github.com/darui3018823/opus"
)

func main() {
	data, _ := os.ReadFile("testdata/testvector07.bit")
	offset := 8
	totalFrames := int(binary.BigEndian.Uint16(data[6:8]))
	dec, _ := opus.NewDecoder(48000, 2)
	maxAbs := 0.0
	sumSq := 0.0
	count := 0
	for f := 0; f < totalFrames && f < 10; f++ {
		pktLen := int(binary.BigEndian.Uint16(data[offset:]))
		offset += 2
		pkt := data[offset : offset+pktLen]
		offset += pktLen
		pcm := make([]int16, 5760*2)
		n, _ := dec.Decode(pkt, pcm)
		for i := 0; i < n*2; i++ {
			v := math.Abs(float64(pcm[i]))
			if v > maxAbs {
				maxAbs = v
			}
			sumSq += float64(pcm[i]) * float64(pcm[i])
			count++
		}
	}
	fmt.Printf("maxAbs=%.1f rms=%.3f (in int16 units)\n", maxAbs, math.Sqrt(sumSq/float64(count)))
}
