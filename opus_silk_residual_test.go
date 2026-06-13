package opus_test

import (
	"fmt"
	"math"
	"path/filepath"
	"sort"
	"testing"

	opus "github.com/darui3018823/opus"
)

// TestSILKResidualLocalization decodes a SILK vector packet-by-packet and splits
// the 48 kHz RMSE by region (mono-internal vs stereo-internal packets) and by
// SILK internal rate, then lists the worst packets. This localizes where the
// remaining sub-0.0015 residual comes from after the resampler inputDelay fix.
func TestSILKResidualLocalization(t *testing.T) {
	for _, num := range []int{2, 4} {
		t.Run(fmt.Sprintf("tv%02d", num), func(t *testing.T) {
			vecDir := filepath.Join("testdata", "opus_newvectors")
			bitPath := filepath.Join(vecDir, fmt.Sprintf("testvector%02d.bit", num))
			decPath := filepath.Join(vecDir, fmt.Sprintf("testvector%02d.dec", num))

			frames, err := parseOpusDemoBit(bitPath)
			if err != nil {
				t.Skipf("parse .bit: %v", err)
			}
			ref, err := readDecFile(decPath)
			if err != nil {
				t.Skipf("read .dec: %v", err)
			}

			dec, err := opus.NewDecoder(48000, 2)
			if err != nil {
				t.Fatal(err)
			}

			var monoSq, stereoSq float64
			var monoN, stereoN int
			type pktErr struct {
				idx, config, stereo int
				rmse                float64
			}
			var worst []pktErr

			off := 0 // interleaved sample offset into ref/decoded
			for i, f := range frames {
				toc := f.packet[0]
				config := int((toc >> 3) & 0x1f)
				stereo := int((toc >> 2) & 1)

				pcm := make([]int16, 5760*2)
				n, derr := dec.Decode(f.packet, pcm)
				if derr != nil {
					t.Fatalf("pkt%d decode: %v", i, derr)
				}
				ns := n * 2 // interleaved samples produced

				var pSq float64
				pN := 0
				for j := 0; j < ns && off+j < len(ref); j++ {
					d := float64(pcm[j])/32768.0 - ref[off+j]
					sq := d * d
					pSq += sq
					pN++
					if stereo == 1 {
						stereoSq += sq
						stereoN++
					} else {
						monoSq += sq
						monoN++
					}
				}
				off += ns
				if pN > 0 {
					worst = append(worst, pktErr{i, config, stereo, math.Sqrt(pSq / float64(pN))})
				}
			}

			rmse := func(s float64, n int) float64 {
				if n == 0 {
					return 0
				}
				return math.Sqrt(s / float64(n))
			}
			t.Logf("tv%02d: mono-region RMSE=%.6f (n=%d), stereo-region RMSE=%.6f (n=%d)",
				num, rmse(monoSq, monoN), monoN, rmse(stereoSq, stereoN), stereoN)

			sort.Slice(worst, func(a, b int) bool { return worst[a].rmse > worst[b].rmse })
			t.Logf("tv%02d worst 12 packets:", num)
			for k := 0; k < 12 && k < len(worst); k++ {
				w := worst[k]
				t.Logf("  pkt%4d config=%d stereo=%d rmse=%.6f", w.idx, w.config, w.stereo, w.rmse)
			}
		})
	}
}
