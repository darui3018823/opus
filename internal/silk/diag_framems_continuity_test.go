package silk

import (
	"math"
	"strconv"
	"testing"
)

// TestSILKFrameMsContinuity verifies the hypothesis that the residual at the
// tv02 mono voiced burst (pkt289..) is caused by SILK synthesis-state
// discontinuity across a frame-size change, NOT by a decode bug (all pkt289
// params are bit-exact, see TestSILKPkt289Isolated).
//
// tv02 pkt283-288 are config 1 (NB 20ms), then pkt289+ switch to config 0 (NB
// 10ms). libopus uses ONE SILK decoder per channel whose state carries across
// this switch. Here we decode pkt0..291 sequentially with a SINGLE 8 kHz mono
// decoder, calling SetFrameMs per packet so the synthesis state is continuous,
// then compare the 8 kHz output of pkt288..291 against the libopus oracle.
//
// If maxDiff is small (~Q14 rounding, <=~20) across the frame-size switch, the
// hypothesis is confirmed and the fix is to share one decoder per (rate,channel)
// in opus.go instead of separate 10ms/20ms instances.
func TestSILKFrameMsContinuity(t *testing.T) {
	dec, err := NewDecoderWithFrameMs(8000, 1, 20)
	if err != nil {
		t.Fatal(err)
	}

	haveOracle := map[int]bool{288: true, 289: true, 290: true, 291: true}

	for pktIdx := 0; pktIdx <= 291; pktIdx++ {
		pkt := readOpusDemoPacket(t, "testvector02.bit", pktIdx)
		toc := pkt[0]
		config := int((toc >> 3) & 0x1f)
		countCode := int(toc & 3)
		if config >= 4 {
			t.Skipf("pkt%d config=%d is not 8kHz NB; test assumes mono 8kHz region", pktIdx, config)
		}

		// frameMs: config 0 = 10ms, config 1/2/3 = 20ms (silk frames).
		frameMs := 20
		if config == 0 {
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
		oracle := parseOracleFrameOut(t, "testdata_oracle_tv02_pkt"+strconv.Itoa(pktIdx)+".txt")
		if len(oracle) != nFrames {
			t.Logf("pkt%d: oracle frames=%d decoder frames=%d (skip)", pktIdx, len(oracle), nFrames)
			continue
		}
		var sumSq float64
		maxDiff := 0
		firstDiff := -1
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
		t.Logf("pkt%d (config=%d frameMs=%d nFrames=%d): 8kHz maxDiff=%d firstDiff@%d rmse=%.2f LSB (%.6f norm)",
			pktIdx, config, frameMs, nFrames, maxDiff, firstDiff, rmse, rmse/32768.0)
	}
}
