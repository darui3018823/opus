package opus_test

import (
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"testing"

	opus "github.com/darui3018823/opus"
)

func TestDiagCELT(t *testing.T) {
	data, err := os.ReadFile("testdata/opus_newvectors/testvector07.bit")
	if err != nil {
		t.Skip("testdata not found:", err)
	}
	refData, err := os.ReadFile("testdata/opus_newvectors/testvector07.dec")
	if err != nil {
		t.Skip("ref not found:", err)
	}
	nRef := len(refData) / 2
	ref := make([]float64, nRef)
	for i := 0; i < nRef; i++ {
		v := int16(binary.LittleEndian.Uint16(refData[i*2 : i*2+2]))
		ref[i] = float64(v) / 32768.0
	}

	dec, _ := opus.NewDecoder(48000, 2)
	pos := 0
	for pi := 0; pi < 5 && pos+8 <= len(data); pi++ {
		size := int(binary.BigEndian.Uint32(data[pos:]))
		_ = binary.BigEndian.Uint32(data[pos+4:])
		pkt := data[pos+8 : pos+8+size]
		pos += 8 + size
		pcm := make([]int16, 5760*2)
		n, _ := dec.Decode(pkt, pcm)

		refFrame := ref[:n*2]
		ref = ref[n*2:]

		rmse := 0.0
		maxPCM := int16(0)
		for i := 0; i < n*2; i++ {
			d := float64(pcm[i])/32768.0 - refFrame[i]
			rmse += d * d
			if pcm[i] > maxPCM {
				maxPCM = pcm[i]
			}
			if -pcm[i] > maxPCM {
				maxPCM = -pcm[i]
			}
		}
		rmse = math.Sqrt(rmse / float64(n*2))

		// Print a few non-overlap samples (after sample 240 in interleaved)
		fmt.Printf("pkt%d: RMSE=%.4f maxPCM=%d\n", pi, rmse, maxPCM)
		for i := 240; i < 260 && i < n*2; i++ {
			fmt.Printf("  [%d] pcm=%d ref=%.4f\n", i, pcm[i], refFrame[i])
		}
	}
}
