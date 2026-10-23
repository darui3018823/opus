package silk

import "testing"

// TestLBRRSpeechActivityThreshold pins SILK_FIX_CONST(0.3f, 8): the float
// constant is scaled, rounded by +0.5 and truncated, which gives 77, not 76.
func TestLBRRSpeechActivityThreshold(t *testing.T) {
	c := float32(0.3)
	want := int32(c*float32(1<<8) + 0.5)
	if lbrrSpeechActivityThresQ8 != want {
		t.Fatalf("lbrrSpeechActivityThresQ8 = %d, want %d", lbrrSpeechActivityThresQ8, want)
	}
}
