package silk

import (
	"bufio"
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// parseOracleFrameOut reads a captured oracle trace file where each line has the
// form "[SILK_FRAME_OUT] n=160 v[0]=.. v[1]=.. ..." and returns one []int16 per
// line (one SILK frame of 8 kHz samples).
func parseOracleFrameOut(t *testing.T, path string) [][]int16 {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Skipf("oracle trace not found: %v", err)
	}
	defer f.Close()

	var frames [][]int16
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if !strings.Contains(line, "SILK_FRAME_OUT") {
			continue
		}
		var n int
		for _, tok := range strings.Fields(line) {
			if strings.HasPrefix(tok, "n=") {
				n, _ = strconv.Atoi(tok[2:])
			}
		}
		samples := make([]int16, n)
		for _, tok := range strings.Fields(line) {
			if !strings.HasPrefix(tok, "v[") {
				continue
			}
			close := strings.IndexByte(tok, ']')
			if close < 0 {
				continue
			}
			idx, err1 := strconv.Atoi(tok[2:close])
			val, err2 := strconv.Atoi(tok[close+2:])
			if err1 != nil || err2 != nil || idx < 0 || idx >= n {
				continue
			}
			samples[idx] = int16(val)
		}
		frames = append(frames, samples)
	}
	return frames
}

// TestSILK8kHzVsOracle decodes packets 0..2 of testvector02 in sequence with our
// SILK decoder and compares the raw 8 kHz int16 output against the libopus oracle
// SILK_FRAME_OUT trace.  This isolates whether the residual RMSE=0.024 at 48 kHz
// comes from the SILK synthesis itself or from the 8->48 kHz resampler.
func TestSILK8kHzVsOracle(t *testing.T) {
	dec, err := NewDecoderWithFrameMs(8000, 1, 20)
	if err != nil {
		t.Fatal(err)
	}

	var sumSq float64
	var nTotal int
	var globalMaxDiff int

	for pktIdx := 0; pktIdx <= 2; pktIdx++ {
		oracle := parseOracleFrameOut(t, "testdata_oracle_tv02_pkt"+strconv.Itoa(pktIdx)+".txt")

		pkt := readOpusDemoPacket(t, "testvector02.bit", pktIdx)
		toc := pkt[0]
		config := int((toc >> 3) & 0x1f)
		countCode := int(toc & 3)
		nFrames, stream := silkOracleFrameCount(config, countCode, pkt[1:])

		pcm, decErr := dec.DecodeMulti(stream, nFrames)
		if decErr != nil {
			t.Fatalf("pkt%d decode error: %v", pktIdx, decErr)
		}
		if len(pcm) != nFrames*dec.frameSize {
			t.Fatalf("pkt%d: got %d samples, want %d", pktIdx, len(pcm), nFrames*dec.frameSize)
		}
		if len(oracle) != nFrames {
			t.Fatalf("pkt%d: oracle has %d frames, decoder produced %d", pktIdx, len(oracle), nFrames)
		}

		for fr := 0; fr < nFrames; fr++ {
			want := oracle[fr]
			var frSumSq float64
			frMaxDiff := 0
			firstDiffIdx := -1
			for i := 0; i < dec.frameSize && i < len(want); i++ {
				got := int(math.Round(pcm[fr*dec.frameSize+i] * 32768.0))
				diff := got - int(want[i])
				if diff < 0 {
					diff = -diff
				}
				if diff > frMaxDiff {
					frMaxDiff = diff
				}
				if diff > 0 && firstDiffIdx < 0 {
					firstDiffIdx = i
				}
				frSumSq += float64(diff) * float64(diff)
				sumSq += float64(diff) * float64(diff)
				nTotal++
			}
			if frMaxDiff > globalMaxDiff {
				globalMaxDiff = frMaxDiff
			}
			frRMSE := math.Sqrt(frSumSq / float64(dec.frameSize))
			t.Logf("pkt%d frame%d: maxDiff=%d firstDiff@%d rmse(LSB)=%.2f", pktIdx, fr, frMaxDiff, firstDiffIdx, frRMSE)
		}
	}

	rmseLSB := math.Sqrt(sumSq / float64(nTotal))
	rmseNorm := rmseLSB / 32768.0
	t.Logf("TOTAL: samples=%d maxDiff=%d rmse=%.3f LSB = %.6f normalized", nTotal, globalMaxDiff, rmseLSB, rmseNorm)
}

// TestSILK8kHzGrowth decodes tv02 packets 0..20 in sequence and, for the packets
// where an oracle trace was captured, reports the per-packet 8 kHz divergence from
// libopus. This characterizes how SILK synthesis error accumulates over the file.
func TestSILK8kHzGrowth(t *testing.T) {
	dec, err := NewDecoderWithFrameMs(8000, 1, 20)
	if err != nil {
		t.Fatal(err)
	}

	haveOracle := map[int]bool{0: true, 1: true, 2: true, 3: true, 4: true, 5: true, 10: true, 20: true}

	for pktIdx := 0; pktIdx <= 20; pktIdx++ {
		pkt := readOpusDemoPacket(t, "testvector02.bit", pktIdx)
		toc := pkt[0]
		config := int((toc >> 3) & 0x1f)
		countCode := int(toc & 3)
		nFrames, stream := silkOracleFrameCount(config, countCode, pkt[1:])

		pcm, decErr := dec.DecodeMulti(stream, nFrames)
		if decErr != nil {
			t.Fatalf("pkt%d decode error: %v", pktIdx, decErr)
		}
		if !haveOracle[pktIdx] {
			continue
		}
		oracle := parseOracleFrameOut(t, "testdata_oracle_tv02_pkt"+strconv.Itoa(pktIdx)+".txt")
		if len(oracle) != nFrames {
			t.Logf("pkt%d: oracle frames=%d decoder frames=%d (skip)", pktIdx, len(oracle), nFrames)
			continue
		}
		var sumSq float64
		maxDiff := 0
		for fr := 0; fr < nFrames; fr++ {
			for i := 0; i < dec.frameSize && i < len(oracle[fr]); i++ {
				got := int(math.Round(pcm[fr*dec.frameSize+i] * 32768.0))
				diff := got - int(oracle[fr][i])
				if diff < 0 {
					diff = -diff
				}
				if diff > maxDiff {
					maxDiff = diff
				}
				sumSq += float64(diff) * float64(diff)
			}
		}
		rmse := math.Sqrt(sumSq / float64(nFrames*dec.frameSize))
		t.Logf("pkt%2d: 8kHz maxDiff=%4d rmse=%7.2f LSB (%.6f norm)", pktIdx, maxDiff, rmse, rmse/32768.0)
	}
}

// TestSILKResamplerVsDec validates the SILK resampler in isolation: it feeds the
// libopus oracle's 8 kHz SILK frames (so synthesis error is excluded) through our
// resampler with the same 1-sample sMid delay libopus uses, then compares the
// 48 kHz result against the reference .dec output (mono channel).
func TestSILKResamplerVsDec(t *testing.T) {
	// Reference 48 kHz stereo int16 PCM.
	decPath := filepath.Join("..", "..", "testdata", "opus_newvectors", "testvector02.dec")
	raw, err := os.ReadFile(decPath)
	if err != nil {
		t.Skipf(".dec not found: %v", err)
	}
	refStereo := make([]int16, len(raw)/2)
	for i := range refStereo {
		refStereo[i] = int16(binary.LittleEndian.Uint16(raw[i*2:]))
	}

	rs, err := NewResampler(8000, 48000)
	if err != nil {
		t.Fatal(err)
	}

	var sMid int16 // 1-sample delay carry, init 0 (libopus sStereo.sMid)
	var got []int16
	for pktIdx := 0; pktIdx <= 2; pktIdx++ {
		frames := parseOracleFrameOut(t, "testdata_oracle_tv02_pkt"+strconv.Itoa(pktIdx)+".txt")
		for _, frame := range frames {
			n := len(frame)
			rin := make([]int16, n)
			rin[0] = sMid
			copy(rin[1:], frame[:n-1])
			sMid = frame[n-1]
			got = append(got, rs.Process(rin)...)
		}
	}

	// Compare against the reference mono channel (ch0 of stereo .dec).
	var sumSq float64
	maxDiff := 0
	firstDiff := -1
	n := len(got)
	if n*2 > len(refStereo) {
		n = len(refStereo) / 2
	}
	for i := 0; i < n; i++ {
		ref := int(refStereo[i*2])
		diff := int(got[i]) - ref
		if diff < 0 {
			diff = -diff
		}
		if diff > maxDiff {
			maxDiff = diff
		}
		if diff > 0 && firstDiff < 0 {
			firstDiff = i
		}
		sumSq += float64(diff) * float64(diff)
	}
	rmse := math.Sqrt(sumSq / float64(n))
	t.Logf("resampler-only vs .dec: samples=%d maxDiff=%d firstDiff@%d rmse=%.3f LSB = %.6f normalized",
		n, maxDiff, firstDiff, rmse, rmse/32768.0)
}
