//go:build opusref

package opus_test

// CGO golden-test: decodes every test-vector packet with BOTH libopus (via CGO)
// and our pure-Go implementation, then compares frame-by-frame.
//
// Run with:
//   go test -tags opusref -run TestCGORef ./...
//
// The test reports per-frame RMSE and an overall pass/fail for each vector.
// It does NOT require the .dec reference files — it uses libopus as ground truth.

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"

	opus "github.com/darui3018823/opus"
	"github.com/darui3018823/opus/internal/cgoref"
)

func TestCGORef(t *testing.T) {
	t.Logf("libopus version: %s", cgoref.Version())

	vecDir := filepath.Join("testdata", "opus_newvectors")
	if _, err := os.Stat(vecDir); os.IsNotExist(err) {
		t.Skip("test vectors not found — download opus_testvectors-rfc8251.tar.gz to testdata/opus_newvectors/")
	}

	type vecCase struct {
		num      int
		channels int
		rate     int
	}
	cases := []vecCase{
		{1, 2, 48000}, {2, 2, 48000}, {3, 2, 48000}, {4, 2, 48000},
		{5, 2, 48000}, {6, 2, 48000}, {7, 2, 48000}, {8, 2, 48000},
		{9, 2, 48000}, {10, 2, 48000}, {11, 2, 48000}, {12, 2, 48000},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(fmt.Sprintf("testvector%02d", tc.num), func(t *testing.T) {
			bitPath := filepath.Join(vecDir, fmt.Sprintf("testvector%02d.bit", tc.num))
			if _, err := os.Stat(bitPath); os.IsNotExist(err) {
				t.Skipf("not found: %s", bitPath)
			}

			frames, err := parseOpusDemoBit(bitPath)
			if err != nil {
				t.Fatalf("parse .bit: %v", err)
			}

			// libopus reference decoder
			ref, err := cgoref.NewDecoder(tc.rate, tc.channels)
			if err != nil {
				t.Fatalf("cgoref.NewDecoder: %v", err)
			}
			defer ref.Close()

			// pure-Go decoder under test
			got, err := opus.NewDecoder(tc.rate, tc.channels)
			if err != nil {
				t.Fatalf("opus.NewDecoder: %v", err)
			}

			const maxSPC = 5760 // max samples per channel (120ms @ 48kHz)

			var (
				totalSamples int
				totalSqErr   float64
				badFrames    int
			)

			goPCM := make([]int16, maxSPC*tc.channels)

			for i, f := range frames {
				// libopus reference output (float32)
				refOut, err := ref.DecodeFloat(f.packet, maxSPC)
				if err != nil {
					t.Logf("frame %d: libopus error: %v", i, err)
					continue
				}

				// our decoder output (int16)
				n, err := got.Decode(f.packet, goPCM)
				if err != nil {
					t.Logf("frame %d: go error: %v", i, err)
					continue
				}
				goOut := goPCM[:n*tc.channels]

				// compare
				m := len(refOut)
				if len(goOut) < m {
					m = len(goOut)
				}
				frameSqErr := 0.0
				for j := 0; j < m; j++ {
					d := float64(goOut[j])/32768.0 - float64(refOut[j])
					frameSqErr += d * d
				}
				frameRMSE := math.Sqrt(frameSqErr / float64(m))

				totalSamples += m
				totalSqErr += frameSqErr

				if frameRMSE > 0.001 {
					badFrames++
					if badFrames <= 3 {
						t.Logf("frame %d: RMSE=%.5f  go[0..3]=%v  ref[0..3]=%v",
							i, frameRMSE,
							formatI16(goOut, 4),
							formatF32(refOut, 4))
					}
				}
			}

			if totalSamples == 0 {
				t.Fatal("no samples decoded")
			}

			overallRMSE := math.Sqrt(totalSqErr / float64(totalSamples))
			badPct := float64(badFrames) / float64(len(frames)) * 100

			t.Logf("frames=%d  badFrames=%d (%.1f%%)  overallRMSE=%.5f",
				len(frames), badFrames, badPct, overallRMSE)

			if overallRMSE > 0.001 {
				t.Errorf("RMSE %.5f exceeds 0.001 threshold (libopus as reference)", overallRMSE)
			}
		})
	}
}

func formatI16(s []int16, n int) []float64 {
	if len(s) < n {
		n = len(s)
	}
	out := make([]float64, n)
	for i := range out {
		out[i] = math.Round(float64(s[i])/32768.0*1000) / 1000
	}
	return out
}

func formatF32(s []float32, n int) []float64 {
	if len(s) < n {
		n = len(s)
	}
	out := make([]float64, n)
	for i := range out {
		out[i] = math.Round(float64(s[i])*1000) / 1000
	}
	return out
}
