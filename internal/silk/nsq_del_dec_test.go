package silk

import "testing"

func TestNSQScaleBoundaryXQUsesFullGainPrecision(t *testing.T) {
	const (
		xqQ14   int32 = 123456789
		gainQ16 int32 = 76543
	)

	got := nsqScaleBoundaryXQ(xqQ14, gainQ16)
	const want int16 = 8801
	if got != want {
		t.Fatalf("nsqScaleBoundaryXQ=%d, want libopus full-Q16 boundary scaling %d", got, want)
	}

	oldTruncated := clamp16(silkRShiftRound(int64(silkSMULWW(xqQ14, silkRSHIFT32(gainQ16, 6))), 8))
	if got == oldTruncated {
		t.Fatalf("boundary scaling collapsed to old Q10-gain path: got=%d", got)
	}
}

func TestNSQStateCopyFromDoesNotAliasHistory(t *testing.T) {
	src := newSilkNSQState(4, 3)
	for i := range src.xq {
		src.xq[i] = int16(i + 1)
		src.sLTPShpQ14[i] = int32(100 + i)
	}
	src.sLPCQ14[0] = 11
	src.sAR2Q14[0] = 22
	src.sLFARShpQ14 = 33
	src.sDiffShpQ14 = 44
	src.lagPrev = 55
	src.sLTPBufIdx = 66
	src.sLTPShpBufIdx = 77
	src.prevGainQ16 = 88
	src.rewhiteFlag = true

	dst := newSilkNSQState(4, 3)
	dst.copyFrom(src)
	src.xq[0] = 999
	src.sLTPShpQ14[0] = 999
	if dst.xq[0] == src.xq[0] {
		t.Fatal("copyFrom aliased xq history")
	}
	if dst.sLTPShpQ14[0] == src.sLTPShpQ14[0] {
		t.Fatal("copyFrom aliased shaped LTP history")
	}
	if dst.sLPCQ14[0] != 11 || dst.sAR2Q14[0] != 22 ||
		dst.sLFARShpQ14 != 33 || dst.sDiffShpQ14 != 44 ||
		dst.lagPrev != 55 || dst.sLTPBufIdx != 66 ||
		dst.sLTPShpBufIdx != 77 || dst.prevGainQ16 != 88 ||
		!dst.rewhiteFlag {
		t.Fatalf("copyFrom did not copy scalar state: %+v", dst)
	}
}
