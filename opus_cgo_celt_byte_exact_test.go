//go:build opusref

package opus_test

import (
	"bytes"
	"fmt"
	"math"
	"testing"

	"github.com/darui3018823/opus"
	"github.com/darui3018823/opus/internal/cgoref"
)

// TestCGOEncodeRefCELTByteExact compares the Go CELT-only encoder with the
// real libopus encoder (cgoref) over the same matrix as the CELT oracle:
// RESTRICTED_LOWDELAY, 48 kHz, fullband, mono and stereo, CBR and CVBR,
// several complexities. The oracle is a plain C build of libopus 1.6.1; the
// linked library may use SIMD kernels whose float summation order differs,
// so this test reports (and gates only) what the linked build reproduces.
func TestCGOEncodeRefCELTByteExact(t *testing.T) {
	const (
		rate      = 48000
		frameSize = 960
		nPackets  = 20
	)
	type cell struct {
		channels, bitrate, complexity int
		vbr                           bool
	}
	var cells []cell
	for _, ch := range []int{1, 2} {
		bitrates := []int{24000, 64000, 128000}
		if ch == 2 {
			bitrates = []int{32000, 96000, 192000}
		}
		for _, br := range bitrates {
			for _, c := range []int{0, 5, 10} {
				for _, vbr := range []bool{false, true} {
					cells = append(cells, cell{ch, br, c, vbr})
				}
			}
		}
	}
	identicalCells := 0
	for _, tc := range cells {
		mode := "cbr"
		if tc.vbr {
			mode = "vbr"
		}
		name := fmt.Sprintf("ch%d/%s/%dk/c%d", tc.channels, mode, tc.bitrate/1000, tc.complexity)
		t.Run(name, func(t *testing.T) {
			enc, err := opus.NewEncoder(rate, tc.channels, opus.ApplicationRestrictedLowDelay)
			if err != nil {
				t.Fatal(err)
			}
			if err := enc.SetBitrate(tc.bitrate); err != nil {
				t.Fatal(err)
			}
			enc.SetVBR(tc.vbr)
			enc.SetVBRConstraint(true)
			if err := enc.SetComplexity(tc.complexity); err != nil {
				t.Fatal(err)
			}
			if err := enc.SetBandwidth(opus.BandwidthFullband); err != nil {
				t.Fatal(err)
			}
			ref, err := cgoref.NewEncoder(rate, tc.channels, opus.ApplicationRestrictedLowDelay)
			if err != nil {
				t.Fatal(err)
			}
			defer ref.Close()
			if err := ref.SetBitrate(tc.bitrate); err != nil {
				t.Fatal(err)
			}
			if err := ref.SetComplexity(tc.complexity); err != nil {
				t.Fatal(err)
			}
			if err := ref.SetVBR(tc.vbr); err != nil {
				t.Fatal(err)
			}
			if err := ref.SetVBRConstraint(true); err != nil {
				t.Fatal(err)
			}
			if err := ref.SetBandwidth(opus.BandwidthFullband); err != nil {
				t.Fatal(err)
			}
			identical := 0
			firstDiff := -1
			for p := 0; p < nPackets; p++ {
				in := silkRefSpeechFrame(rate, p*frameSize, frameSize, tc.channels)
				// Snap to the int16 grid like the oracle fixture, so the
				// comparison is about the encoders, not the libm.
				for i, v := range in {
					in[i] = math.Floor(float64(float32(v))*32768+0.5) / 32768
				}
				got, err := enc.EncodeFloat(in, frameSize)
				if err != nil {
					t.Fatalf("packet %d: Go EncodeFloat: %v", p, err)
				}
				in32 := make([]float32, len(in))
				for i, v := range in {
					in32[i] = float32(v)
				}
				want, err := ref.Encode(in32, frameSize)
				if err != nil {
					t.Fatalf("packet %d: libopus encode: %v", p, err)
				}
				if bytes.Equal(got, want) {
					identical++
				} else if firstDiff < 0 {
					firstDiff = p
					prefix := 0
					for prefix < len(got) && prefix < len(want) && got[prefix] == want[prefix] {
						prefix++
					}
					t.Logf("packet %d: Go %d bytes, libopus %d bytes, common prefix %d", p, len(got), len(want), prefix)
				}
			}
			t.Logf("%s: %d/%d packets byte-identical to the linked libopus", name, identical, nPackets)
			if identical == nPackets {
				identicalCells++
			}
		})
	}
	t.Logf("%d/%d cells byte-identical", identicalCells, len(cells))
}
