package silk

import (
	"testing"
)

func oraclePkt2Frame0Params() (gainsQ16 []int32, pitchLags []int, ltpScaleQ14 int16, lpcCoeffsQ12 [][]int16, ltpCoeffsQ14 [][5]int16, pulses []int16, wantSubframes [][]int16) {
	gainsQ16 = []int32{4915200, 4194304, 12713984, 28049408}
	pitchLags = []int{44, 45, 46, 47}
	ltpScaleQ14 = int16(15565)

	lpcQ12 := []int16{6277, -2578, 684, -409, 884, -1164, -103, -1673, 2905, -1094}
	lpcCoeffsQ12 = [][]int16{lpcQ12, lpcQ12, lpcQ12, lpcQ12}

	ltpCoeffsQ14 = [][5]int16{
		{-896, 2560, 12928, -896, 512},
		{1664, 2816, 4992, 2944, 1536},
		{-128, 4608, 8192, 3456, -768},
		{1664, 2816, 4992, 2944, 1536},
	}

	pulses = []int16{
		0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1, -1, 0, 0, 0,
		0, 0, 0, 0, 0, 0, 1, 0, 0, 0, 1, 0, 0, 0, 0, 0,
		0, 0, 0, 0, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
		0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
		-1, 0, 0, -1, 0, 0, -1, 0, 0, 0, 0, 0, 0, 0, 1, 0,
		0, 0, 0, 0, -1, 0, 0, 0, 0, 0, 0, 0, -1, 0, 0, 0,
		0, 0, 0, 0, 0, 0, 0, 0, 0, 0, -1, 0, 0, 0, 0, 0,
		-1, 0, 0, 0, 0, -1, 1, 0, 0, 0, -1, 0, 0, 0, 0, 0,
		0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
		0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
	}

	wantSubframes = [][]int16{
		{10, 47, 48, 43, 47, 54, 59, 70, 63, 22, 11, -48, -11, -11, -42, -58, -55, -25, -15, -9, -44, -40, 67, 114, 122, 116, 61, 56, 60, 43, 5, -50, -81, -84, -61, -88, -60, -44, 9, 53},
		{72, 92, 98, 139, 152, 158, 142, 119, 105, 81, 52, 5, -24, -45, -72, -94, -121, -120, -111, -93, -78, -59, -28, 3, -27, -39, -26, -51, -51, -61, -146, -208, -226, -237, -248, -242, -245, -224, -97, 2},
		{67, 138, 216, 311, 564, 684, 672, 667, 668, 665, 615, 504, 474, 413, 356, 265, 133, 30, -46, -100, -188, -258, -312, -313, -269, -235, -376, -421, -379, -328, -294, -313, -493, -558, -471, -424, -464, -699, -979, -1030},
		{-936, -874, -522, -324, -135, 190, 535, 809, 988, 1136, 1159, 1276, 1319, 1209, 1032, 804, 614, 407, 210, -28, -244, -381, -493, -546, -612, -649, -613, -535, -413, -293, -155, -24, 113, 210, 243, 245, 224, 196, 101, -64},
	}
	return
}

func countSynthMismatches(t *testing.T, dec *Decoder, gainsQ16 []int32, pitchLags []int, ltpScaleQ14 int16, lpcCoeffsQ12 [][]int16, ltpCoeffsQ14 [][5]int16, pulses []int16, wantSubframes [][]int16, label string) int {
	t.Helper()
	got := dec.synthesize(pulses, gainsQ16, lpcCoeffsQ12, pitchLags, ltpCoeffsQ14, ltpScaleQ14, SignalTypeVoiced, 0, 0)
	if len(got) != dec.frameSize {
		t.Fatalf("%s: output length=%d want=%d", label, len(got), dec.frameSize)
	}
	mismatches := 0
	firstLogged := false
	for sf, want := range wantSubframes {
		for i, wantSample := range want {
			fi := sf*dec.subfrmLen + i
			if got[fi] == wantSample {
				continue
			}
			mismatches++
			if !firstLogged {
				t.Logf("%s: first mismatch sf=%d index=%d frame_index=%d got=%d want=%d diff=%d",
					label, sf, i, fi, got[fi], wantSample, int(got[fi])-int(wantSample))
				firstLogged = true
			}
		}
	}
	return mismatches
}

func TestSynthesizeOracle(t *testing.T) {
	dec, err := NewDecoderWithFrameMs(8000, 1, 20)
	if err != nil {
		t.Fatal(err)
	}

	gainsQ16, pitchLags, ltpScaleQ14, lpcCoeffsQ12, ltpCoeffsQ14, pulses, wantSubframes := oraclePkt2Frame0Params()

	if len(wantSubframes) != dec.nSubframes {
		t.Fatalf("oracle subframes=%d want=%d", len(wantSubframes), dec.nSubframes)
	}
	for sf, want := range wantSubframes {
		if len(want) != dec.subfrmLen {
			t.Fatalf("oracle subframe %d length=%d want=%d", sf, len(want), dec.subfrmLen)
		}
	}

	// The oracle frame (tv02 pkt2 frame0) depends on ltpState/lpcState/prevGain
	// warmed by pkt0 and pkt1, so a cold decoder cannot reproduce it. This is by
	// design — bit-exact correctness is asserted by TestSynthesizeOracleWarm.
	// Here we only confirm cold synthesis runs and produces a full frame.
	mismatches := countSynthMismatches(t, dec, gainsQ16, pitchLags, ltpScaleQ14, lpcCoeffsQ12, ltpCoeffsQ14, pulses, wantSubframes, "cold")
	t.Logf("cold-start mismatches vs warm-dependent oracle: %d/%d (expected nonzero)", mismatches, dec.frameSize)
}

// TestSynthesizeOracleWarm warms the decoder by decoding pkt0 and pkt1 from
// testvector02.bit (6 SILK frames total), then calls synthesize() directly with
// the oracle parameters for pkt2 frame0.  A match proves the algorithm is
// correct; a mismatch means the bug is in synthesize() itself (not cold start).
func TestSynthesizeOracleWarm(t *testing.T) {
	dec, err := NewDecoderWithFrameMs(8000, 1, 20)
	if err != nil {
		t.Fatal(err)
	}

	// Decode pkt0 and pkt1 to warm ltpState / lpcState / prevGainQ16.
	for pktIdx := 0; pktIdx <= 1; pktIdx++ {
		pkt := readOpusDemoPacket(t, "testvector02.bit", pktIdx)
		toc := pkt[0]
		config := int((toc >> 3) & 0x1f)
		countCode := int(toc & 3)
		nFrames, stream := silkOracleFrameCount(config, countCode, pkt[1:])
		if _, decErr := dec.DecodeMulti(stream, nFrames); decErr != nil {
			t.Logf("pkt%d warm-up decode error (ignored): %v", pktIdx, decErr)
		}
	}
	t.Logf("warm-up done: prevGainQ16=%d ltpState[159]=%d lpcState[0]=%d",
		dec.prevGainQ16, dec.ltpState[len(dec.ltpState)-1], dec.lpcState[0])

	gainsQ16, pitchLags, ltpScaleQ14, lpcCoeffsQ12, ltpCoeffsQ14, pulses, wantSubframes := oraclePkt2Frame0Params()

	mismatches := countSynthMismatches(t, dec, gainsQ16, pitchLags, ltpScaleQ14, lpcCoeffsQ12, ltpCoeffsQ14, pulses, wantSubframes, "warm")
	if mismatches != 0 {
		t.Fatalf("synthesize warm-start mismatches: %d/%d samples — algorithm bug in synthesize()", mismatches, dec.frameSize)
	}
}
