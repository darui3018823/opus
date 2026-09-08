//go:build opusref

package opus_test

// CGO golden-test: decodes every test-vector packet with BOTH libopus (via CGO)
// and our pure-Go implementation, then compares frame-by-frame.
//
// Run with:
//   go test -tags opusref -run TestCGORef ./...
//
// The test reports exact int16 sample agreement, first/max divergence, and
// per-frame RMSE, with an overall pass/fail for each vector. It does NOT require
// the .dec reference files — it uses libopus as ground truth.

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
		num                int
		channels           int
		rate               int
		maxRangeMismatches int
	}
	cases := []vecCase{
		{1, 2, 48000, 0}, {2, 2, 48000, 0}, {3, 2, 48000, 0}, {4, 2, 48000, 0},
		{5, 2, 48000, 0}, {6, 2, 48000, 0}, {7, 2, 48000, 0}, {8, 2, 48000, 1},
		{9, 2, 48000, 1}, {10, 2, 48000, 13}, {11, 2, 48000, 0}, {12, 2, 48000, 12},
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
				totalSamples     int
				exactSamples     int
				totalSqErrLSB    float64
				badFrames        int
				maxAbsDiffLSB    int
				firstDiffFrame   = -1
				firstDiffSample  = -1
				firstDiffGo      int16
				firstDiffLibopus int16
				rangeMismatches  int
				firstRangeFrame  = -1
				firstRangeGo     uint32
				firstRangeWant   uint32
			)

			goPCM := make([]int16, maxSPC*tc.channels)

			for i, f := range frames {
				// Use libopus' opus_decode int16 path so the oracle includes the
				// FLOAT2INT16 conversion used by the C API.
				refOut, err := ref.Decode(f.packet, maxSPC)
				if err != nil {
					t.Logf("frame %d: libopus error: %v", i, err)
					continue
				}
				refRange, err := ref.FinalRange()
				if err != nil {
					t.Fatalf("frame %d: libopus final range: %v", i, err)
				}
				if refRange != f.finalRange {
					t.Errorf("frame %d: libopus final range=%08x, bitstream=%08x", i, refRange, f.finalRange)
				}

				// our decoder output (int16)
				n, err := got.Decode(f.packet, goPCM)
				if err != nil {
					t.Logf("frame %d: go error: %v", i, err)
					continue
				}
				goOut := goPCM[:n*tc.channels]
				if goRange := got.FinalRange(); goRange != f.finalRange {
					rangeMismatches++
					if firstRangeFrame < 0 {
						firstRangeFrame = i
						firstRangeGo = goRange
						firstRangeWant = f.finalRange
					}
				}

				// compare
				m := len(refOut)
				if len(goOut) < m {
					m = len(goOut)
				}
				frameSqErrLSB := 0.0
				for j := 0; j < m; j++ {
					delta := int(goOut[j]) - int(refOut[j])
					if delta == 0 {
						exactSamples++
					} else if firstDiffFrame < 0 {
						firstDiffFrame = i
						firstDiffSample = j
						firstDiffGo = goOut[j]
						firstDiffLibopus = refOut[j]
					}
					absDelta := delta
					if absDelta < 0 {
						absDelta = -absDelta
					}
					if absDelta > maxAbsDiffLSB {
						maxAbsDiffLSB = absDelta
					}
					frameSqErrLSB += float64(delta * delta)
				}
				frameRMSE := math.Sqrt(frameSqErrLSB/float64(m)) / 32768

				totalSamples += m
				totalSqErrLSB += frameSqErrLSB

				if frameRMSE > 0.001 {
					badFrames++
					if badFrames <= 3 {
						t.Logf("frame %d: RMSE=%.5f  go[0..3]=%v  ref[0..3]=%v",
							i, frameRMSE,
							formatI16(goOut, 4),
							formatI16(refOut, 4))
					}
				}
			}

			if totalSamples == 0 {
				t.Fatal("no samples decoded")
			}

			overallRMSE := math.Sqrt(totalSqErrLSB/float64(totalSamples)) / 32768
			badPct := float64(badFrames) / float64(len(frames)) * 100
			exactPct := float64(exactSamples) / float64(totalSamples) * 100

			t.Logf("frames=%d badFrames=%d (%.1f%%) exactSamples=%d/%d (%.3f%%) maxAbsDiff=%d LSB firstDiff=frame:%d sample:%d go:%d libopus:%d rangeMismatches=%d firstRange=frame:%d go:%08x want:%08x overallRMSE=%.5f",
				len(frames), badFrames, badPct, exactSamples, totalSamples, exactPct,
				maxAbsDiffLSB, firstDiffFrame, firstDiffSample, firstDiffGo, firstDiffLibopus,
				rangeMismatches, firstRangeFrame, firstRangeGo, firstRangeWant, overallRMSE)

			if overallRMSE > 0.001 {
				t.Errorf("RMSE %.5f exceeds 0.001 threshold (libopus as reference)", overallRMSE)
			}
			if rangeMismatches > tc.maxRangeMismatches {
				t.Errorf("final-range mismatches %d exceed baseline %d", rangeMismatches, tc.maxRangeMismatches)
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
