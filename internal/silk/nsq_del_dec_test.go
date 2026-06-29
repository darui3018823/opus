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
