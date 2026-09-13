package silk

import (
	"math"
	"slices"
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

func TestAnalyzeNLSFUsesResetPredictionGainLimit(t *testing.T) {
	const rate = 16000
	enc, err := NewEncoder(rate, 1)
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}

	signal := make([]float64, enc.frameSize)
	for i := range signal {
		tm := float64(i) / rate
		signal[i] = 0.20 * (0.72*math.Sin(2*math.Pi*180*tm) +
			0.22*math.Sin(2*math.Pi*360*tm+0.3) +
			0.09*math.Sin(2*math.Pi*540*tm+0.7))
	}

	cb := getNLSFCB(enc.lpcOrder)
	minInvGain := lpcMinInvGain(0, 1, true)
	targetQ15, interpFactor := silkFindLPCFLP(signal, minInvGain, len(signal), 1,
		cb.order, false, true, enc.prevNLSFQ15)
	wantCB1, wantRaw, wantQ15, _ := enc.silkProcessNLSFs(cb, targetQ15,
		enc.prevNLSFQ15, interpFactor, SignalTypeVoiced)

	got := enc.analyzeNLSF(signal, cb, SignalTypeVoiced)
	if got.cb1Idx != wantCB1 || !slices.Equal(got.rawIdx, wantRaw) || !slices.Equal(got.nlsfQ15, wantQ15) {
		t.Fatalf("reset NLSF does not use reset gain limit: got cb1=%d raw=%v nlsf=%v, want cb1=%d raw=%v nlsf=%v",
			got.cb1Idx, got.rawIdx, got.nlsfQ15, wantCB1, wantRaw, wantQ15)
	}
}

func TestLastHalfBurgNLSFSmoke(t *testing.T) {
	const (
		order       = 10
		nbSubfr     = 4
		subfrLength = 50
	)
	pre := makeStackedLPCInPreFixture(subfrLength, nbSubfr, order)
	nlsf, lpc := lastHalfBurgNLSF(pre, subfrLength, order, nbSubfr, 1e-4)
	if len(nlsf) != order {
		t.Fatalf("NLSF len=%d, want %d", len(nlsf), order)
	}
	if len(lpc) != order {
		t.Fatalf("LPC len=%d, want %d", len(lpc), order)
	}
	for i, v := range nlsf {
		if v < 0 || v > 32767 {
			t.Fatalf("NLSF[%d]=%d out of Q15 range", i, v)
		}
		if i > 0 && v <= nlsf[i-1] {
			t.Fatalf("NLSF not ordered at %d: %d <= %d", i, v, nlsf[i-1])
		}
	}
}

func TestInterpolatedLPCEndpointUsesTransmittedNLSF(t *testing.T) {
	enc, err := NewEncoder(16000, 1)
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	cb := getNLSFCB(enc.lpcOrder)
	enc.prevNLSFQ15 = make([]int16, cb.order)
	for i := range enc.prevNLSFQ15 {
		enc.prevNLSFQ15[i] = int16((i + 1) * 32768 / (cb.order + 1))
	}
	transmitted := make([]int16, cb.order)
	for i := range transmitted {
		transmitted[i] = int16((i + 1) * 32768 / (cb.order + 1))
	}
	transmitted[0] += 90
	transmitted[5] -= 160
	silkNLSFStabilize(transmitted, cb.deltaMinQ15, cb.order)

	const interpFactor = 2
	got := interpolatedLPCForTransmittedNLSF(enc.prevNLSFQ15, transmitted, interpFactor, cb)
	wantNLSF := interpolateNLSFQ15(enc.prevNLSFQ15, transmitted, interpFactor, cb)
	want := nlsfToLPCLibopus(wantNLSF, cb.order)
	if len(got) != len(want) {
		t.Fatalf("interp LPC len=%d, want %d", len(got), len(want))
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("interp LPC[%d]=%d, want %d", i, got[i], want[i])
		}
	}
	if interpolatedLPCForTransmittedNLSF(enc.prevNLSFQ15, transmitted, 4, cb) != nil {
		t.Fatal("interp LPC non-nil for factor 4")
	}
}

func makeStackedLPCInPreFixture(subfrLength, nbSubfr, order int) []float64 {
	pre := make([]float64, subfrLength*nbSubfr)
	for sf := 0; sf < nbSubfr; sf++ {
		for i := 0; i < subfrLength; i++ {
			n := float64(sf*(subfrLength-order) + i - order)
			pre[sf*subfrLength+i] =
				0.35*math.Sin(2*math.Pi*n/37.0) +
					0.18*math.Sin(2*math.Pi*n/19.0) +
					0.04*float64(sf)
		}
	}
	return pre
}
