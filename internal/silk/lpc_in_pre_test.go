package silk

import (
	"math"
	"testing"
)

func TestBuildLPCInPreUnvoicedPrependsAndScales(t *testing.T) {
	x := []float64{100, 101, 1, 2, 3, 4, 5, 6}
	got := buildLPCInPre(x, []int{3, 3}, []float64{0.5, 2.0}, nil, nil, 2, false)
	want := []float64{
		50, 50.5, 0.5, 1, 1.5,
		4, 6, 8, 10, 12,
	}
	if len(got) != len(want) {
		t.Fatalf("len=%d, want %d", len(got), len(want))
	}
	for i := range want {
		if math.Abs(got[i]-want[i]) > 1e-12 {
			t.Fatalf("got[%d]=%g, want %g (full=%v)", i, got[i], want[i], got)
		}
	}
}

func TestBuildLPCInPreVoicedLTPResidualAndInvGain(t *testing.T) {
	x := []float64{8, 4, 2, 1, 10, 20, 30}
	ltp := [][]float64{{0, 0, 0.5, 0, 0}}
	got := buildLPCInPre(x, []int{3}, []float64{2.0}, ltp, []int{2}, 1, true)
	want := []float64{
		2 * (1 - 0.5*4),
		2 * (10 - 0.5*2),
		2 * (20 - 0.5*1),
		2 * (30 - 0.5*10),
	}
	if len(got) != len(want) {
		t.Fatalf("len=%d, want %d", len(got), len(want))
	}
	for i := range want {
		if math.Abs(got[i]-want[i]) > 1e-12 {
			t.Fatalf("got[%d]=%g, want %g (full=%v)", i, got[i], want[i], got)
		}
	}
}

func TestLPCMinInvGainFormula(t *testing.T) {
	got := lpcMinInvGain(6.0, 0.5, false)
	want := math.Pow(2.0, 6.0/3.0) / maxPredictionPowerGain / (0.25 + 0.75*0.5)
	if math.Abs(got-want) > 1e-15 {
		t.Fatalf("minInvGain=%g, want %g", got, want)
	}
	reset := lpcMinInvGain(30.0, 1.0, true)
	if reset != 1.0/maxPredictionPowerGainAfterReset {
		t.Fatalf("reset minInvGain=%g, want %g", reset, 1.0/maxPredictionPowerGainAfterReset)
	}
}
