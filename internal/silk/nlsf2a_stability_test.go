package silk

import "testing"

func TestNLSF2AStabilizesNearSingularVector(t *testing.T) {
	nlsf := []int16{4385, 4388, 8949, 8952, 11063, 11066, 13394, 20071, 32303, 32306}
	want := []int16{8391, -6031, -6184, 14683, -11347, 723, 4959, -5418, 2943, -1514}
	got := nlsfToLPCLibopus(nlsf, len(nlsf))
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("coefficient %d = %d, want %d (got=%v)", i, got[i], want[i], got)
		}
	}
	if gain := silkLPCInversePredGainQ12(got, len(got)); gain == 0 {
		t.Fatalf("stabilized LPC still reports zero inverse prediction gain")
	}
}

func TestNLSFToLPCIntoMatchesAllocatingWrapper(t *testing.T) {
	nlsf := []int16{4385, 4388, 8949, 8952, 11063, 11066, 13394, 20071, 32303, 32306}
	want := nlsfToLPCLibopus(nlsf, len(nlsf))
	var scratch [silkMaxLPCOrder]int16
	got := nlsfToLPCLibopusInto(scratch[:], nlsf, len(nlsf))
	if len(got) != len(want) {
		t.Fatalf("destination LPC length = %d, want %d", len(got), len(want))
	}
	if &got[0] != &scratch[0] {
		t.Fatal("destination LPC conversion did not reuse caller storage")
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("coefficient %d = %d, want %d", i, got[i], want[i])
		}
	}
}
