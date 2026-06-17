package silk

import (
	"math"
	"testing"
)

// TestLTPGainQuantPeriodic checks the ported weighted-VQ gain quantizer
// (silk_find_LTP_FLP + silk_quant_LTP_gains): for a strongly periodic voiced
// signal it must pick a predictor that removes most of the long-term residual
// (positive coding gain) and return in-range per-subframe indices.
func TestLTPGainQuantPeriodic(t *testing.T) {
	const rate = 16000
	enc, err := NewEncoder(rate, 1)
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}

	// Prime the pitch history with a continuous tone so the LTP look-back lands
	// on real signal rather than the zeroed start-up history (the first frame
	// after reset genuinely cannot predict, like libopus first_frame_after_reset).
	hist := len(enc.pitchHist)
	tone := func(n int) float64 { return 0.25 * math.Sin(2*math.Pi*200*float64(n)/rate) }
	for i := range enc.pitchHist {
		enc.pitchHist[i] = tone(i - hist)
	}
	signal := make([]float64, enc.frameSize)
	for i := range signal {
		signal[i] = tone(i)
	}
	cb := getNLSFCB(enc.lpcOrder)
	nlsf := enc.analyzeNLSF(signal, cb, SignalTypeVoiced)
	pitchLag, _ := enc.analyzePitch(signal)
	lags := make([]int, enc.nSubframes)
	for sf := range lags {
		lags[sf] = pitchLag
	}

	perIdx, gainIndices, coeffs := enc.selectLTPGainsVQ(signal, nlsf.lpcQ12, lags)
	if perIdx < 0 || perIdx > 2 {
		t.Fatalf("periodicity index %d out of range", perIdx)
	}
	if len(gainIndices) != enc.nSubframes {
		t.Fatalf("gainIndices len=%d, want %d", len(gainIndices), enc.nSubframes)
	}
	cbSize := len(ltpGainCodebook(perIdx))
	for sf, idx := range gainIndices {
		if idx < 0 || idx >= cbSize {
			t.Fatalf("subframe %d gain index %d out of codebook range %d", sf, idx, cbSize)
		}
	}

	// The chosen predictor should remove residual energy relative to no
	// prediction: compare LPC-only vs LPC+LTP residual for the periodic tone.
	lpcOnly := enc.ltpResidualEnergyPerSubframe(signal, nlsf.lpcQ12, SignalTypeUnvoiced, lags, coeffs)
	lpcLTP := enc.ltpResidualEnergyPerSubframe(signal, nlsf.lpcQ12, SignalTypeVoiced, lags, coeffs)
	improved := 0
	for sf := 0; sf < enc.nSubframes; sf++ {
		if lpcLTP[sf] < lpcOnly[sf] {
			improved++
		}
	}
	if improved == 0 {
		t.Fatalf("LTP predictor did not reduce residual on any subframe (lpcOnly=%v lpcLTP=%v)", lpcOnly, lpcLTP)
	}
}

// TestLTPSumLogGainStateRollback verifies the cumulative sum_log_gain state is
// part of the frame snapshot so the rate-control search (which runs the gain VQ
// many times per frame) does not corrupt it.
func TestLTPSumLogGainStateRollback(t *testing.T) {
	enc, err := NewEncoder(16000, 1)
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	enc.ltpSumLogGainQ7 = 42.0
	snap := enc.snapshotFrameState()
	enc.ltpSumLogGainQ7 = 999.0
	enc.restoreFrameState(snap)
	if enc.ltpSumLogGainQ7 != 42.0 {
		t.Fatalf("ltpSumLogGainQ7 not restored: got %v, want 42", enc.ltpSumLogGainQ7)
	}
}
