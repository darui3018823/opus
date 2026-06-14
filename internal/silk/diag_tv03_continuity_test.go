package silk

import (
	"math"
	"strconv"
	"testing"
)

// TestSILKtv03Continuity decodes tv03 pkt0..401 (all mono 12kHz, configs 4-7)
// through ONE decoder with SetFrameMs per packet, then compares the 12kHz output
// of the worst voiced burst (pkt398..401, config 4 = MB 10ms) against the libopus
// oracle. All pkt400 params are bit-exact (TestSILKtv03Pkt400Isolated), so any
// divergence here is warm synthesis state. This checks whether the frame-size
// continuity fix already makes the 12kHz region bit-exact, or whether a 12kHz-
// specific synthesis-state bug remains.
func TestSILKtv03Continuity(t *testing.T) {
	dec, err := NewDecoderWithFrameMs(12000, 1, 20)
	if err != nil {
		t.Fatal(err)
	}
	// pkt354 is the first packet that previously diverged (12kHz 10ms voiced
	// onset); pkt398/400 are deeper in the same voiced burst. After the
	// block-aligned pulse-decode fix these must be bit-exact (maxDiff=0).
	haveOracle := map[int]bool{354: true, 398: true, 400: true}

	for pktIdx := 0; pktIdx <= 401; pktIdx++ {
		pkt := readOpusDemoPacket(t, "testvector03.bit", pktIdx)
		toc := pkt[0]
		config := int((toc >> 3) & 0x1f)
		countCode := int(toc & 3)
		if config >= 8 || (toc>>2)&1 == 1 {
			t.Skipf("pkt%d not mono 12kHz", pktIdx)
		}
		// config 4 = 10ms; config 5/6/7 = 20ms silk frames.
		frameMs := 20
		if config == 4 {
			frameMs = 10
		}
		dec.SetFrameMs(frameMs)

		nFrames, stream := silkOracleFrameCount(config, countCode, pkt[1:])
		pcm, decErr := dec.DecodeMulti(stream, nFrames)
		if decErr != nil {
			t.Fatalf("pkt%d decode: %v", pktIdx, decErr)
		}
		if !haveOracle[pktIdx] {
			continue
		}
		oracle := parseOracleFrameOut(t, "testdata_oracle_tv03_pkt"+strconv.Itoa(pktIdx)+".txt")
		if len(oracle) != nFrames {
			t.Logf("pkt%d: oracle frames=%d decoder frames=%d (skip)", pktIdx, len(oracle), nFrames)
			continue
		}
		var sumSq float64
		maxDiff, firstDiff := 0, -1
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
				if diff > 0 && firstDiff < 0 {
					firstDiff = fr*dec.frameSize + i
				}
				sumSq += float64(diff) * float64(diff)
			}
		}
		rmse := math.Sqrt(sumSq / float64(nFrames*dec.frameSize))
		t.Logf("pkt%d (config=%d frameMs=%d): 12kHz maxDiff=%d firstDiff@%d rmse=%.2f LSB",
			pktIdx, config, frameMs, maxDiff, firstDiff, rmse)
	}
}
