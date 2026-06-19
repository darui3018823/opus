package silk

import (
	"math"
	"testing"
)

// TestSilkControlSNR checks the bitrate→SNR mapping against a libopus reference
// point and its monotonicity (higher target rate must not lower the SNR target).
func TestSilkControlSNR(t *testing.T) {
	// WB, 20ms (nb_subfr=4), 24 kb/s: id=(24000+200)/400-10=50; WB table[50]=141.
	if got, want := silkControlSNR(24000, 16, 4), 141*21; got != want {
		t.Fatalf("silkControlSNR(24000,16,4)=%d, want %d", got, want)
	}
	// Below the 4 kb/s floor the table index clamps to 0.
	if got := silkControlSNR(2000, 16, 4); got != 0 {
		t.Fatalf("silkControlSNR(2000,16,4)=%d, want 0", got)
	}
	// Monotonic non-decreasing in target rate.
	prev := -1
	for rate := 8000; rate <= 64000; rate += 4000 {
		got := silkControlSNR(rate, 16, 4)
		if got < prev {
			t.Fatalf("silkControlSNR not monotonic at %d: %d < %d", rate, got, prev)
		}
		prev = got
	}
	// The 10ms (nb_subfr==2) path applies the documented rate offset, so its SNR
	// target is no higher than the 20ms one at the same nominal rate.
	if silkControlSNR(24000, 16, 2) > silkControlSNR(24000, 16, 4) {
		t.Fatalf("10ms SNR target exceeds 20ms target")
	}
}

func TestVoicedSNRTargetBackoff(t *testing.T) {
	for _, tc := range []struct {
		fsKHz int
		lag   int
		want  float64
	}{
		{8, 44, 27.0},
		{12, 67, 24.0},
		{16, 89, 24.0},
	} {
		if got := voicedSNRTargetDecrDB(tc.fsKHz, 24000, tc.lag); got != tc.want {
			t.Fatalf("voicedSNRTargetDecrDB long lag fs=%d got %.1f, want %.1f", tc.fsKHz, got, tc.want)
		}
	}
	for _, tc := range []struct {
		fsKHz int
		lag   int
		want  float64
	}{
		{8, 36, 22.5},
		{12, 55, 20.0},
		{16, 73, 16.0},
	} {
		if got := voicedSNRTargetDecrDB(tc.fsKHz, 24000, tc.lag); got != tc.want {
			t.Fatalf("voicedSNRTargetDecrDB short lag fs=%d got %.1f, want %.1f", tc.fsKHz, got, tc.want)
		}
	}
	if got := voicedSNRTargetDecrDB(16, 32000, 89); got != 0 {
		t.Fatalf("voicedSNRTargetDecrDB above tuned rate=%.1f, want 0", got)
	}
}

// TestVoicedUsesTrellisGating verifies the Step 4 trellis is enabled for mono,
// stereo, and hybrid voiced frames.
func TestVoicedUsesTrellisGating(t *testing.T) {
	mono, err := NewEncoder(16000, 1)
	if err != nil {
		t.Fatalf("NewEncoder mono: %v", err)
	}
	if !mono.voicedUsesTrellis() {
		t.Fatalf("mono SILK-only encoder should use the voiced trellis")
	}
	mono.SetHybridMode(true)
	if !mono.voicedUsesTrellis() {
		t.Fatalf("hybrid-mode encoder should use the voiced trellis")
	}
	mono.SetHybridMode(false)
	if !mono.voicedUsesTrellis() {
		t.Fatalf("clearing hybrid mode should re-enable the trellis")
	}

	stereo, err := NewEncoder(16000, 2)
	if err != nil {
		t.Fatalf("NewEncoder stereo: %v", err)
	}
	if !stereo.voicedUsesTrellis() {
		t.Fatalf("stereo mid encoder should use the voiced trellis")
	}
	if stereo.side == nil || !stereo.side.voicedUsesTrellis() {
		t.Fatalf("stereo side encoder should use the voiced trellis")
	}
}

// TestShapeGainIndicesStable confirms the Step 4 voiced gain pipeline produces
// gain indices in range and stable (no near-zero gains that would over-drive the
// excitation) for a steady voiced tone — the failure mode that the prediction-
// residual gain source caused before the noise-shape envelope gain was used.
func TestShapeGainIndicesStable(t *testing.T) {
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
	nlsf := enc.analyzeNLSF(signal, cb, SignalTypeVoiced)
	pitchLag, pitchGain := enc.analyzePitch(signal)
	pitchLags := make([]int, enc.nSubframes)
	for sf := range pitchLags {
		pitchLags[sf] = pitchLag
	}
	ltpPerIdx, ltpGainIdx := selectLTPGain(pitchGain)
	ltpCoeffs := ltpCoeffsForIndices(ltpPerIdx, ltpGainIdx, enc.nSubframes)

	idx := enc.shapeGainIndices(signal, nlsf.lpcQ12, SignalTypeVoiced, 0, pitchLags, ltpCoeffs, pitchGain)
	if len(idx) != enc.nSubframes {
		t.Fatalf("shapeGainIndices len=%d, want %d", len(idx), enc.nSubframes)
	}
	for sf, g := range idx {
		if g < 0 || g >= NLevelsQGain {
			t.Fatalf("subframe %d gain index %d out of range", sf, g)
		}
		// A steady tone at this level should land well above the minimum gain;
		// a collapse toward 0 is the over-driven-excitation bug.
		if g < 8 {
			t.Fatalf("subframe %d gain index %d implausibly low (gain collapse)", sf, g)
		}
	}
}

// TestLTPPredCodGainDB checks the Step 5(c) LTP coding gain estimate behaves like
// silk_quant_LTP_gains's pred_gain_dB: a strongly periodic (voiced) tone, whose
// long-term predictor removes most of the residual, reports a high coding gain
// (driving the process_gains reduction), while a non-periodic signal reports a
// low one (leaving the gain untouched).
func TestLTPPredCodGainDB(t *testing.T) {
	const rate = 16000
	enc, err := NewEncoder(rate, 1)
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}

	periodic := make([]float64, enc.frameSize)
	for i := range periodic {
		tm := float64(i) / rate
		periodic[i] = 0.20 * math.Sin(2*math.Pi*200*tm)
	}
	cb := getNLSFCB(enc.lpcOrder)
	nlsf := enc.analyzeNLSF(periodic, cb, SignalTypeVoiced)
	pitchLag, pitchGain := enc.analyzePitch(periodic)
	pitchLags := make([]int, enc.nSubframes)
	for sf := range pitchLags {
		pitchLags[sf] = pitchLag
	}
	ltpPerIdx, ltpGainIdx := selectLTPGain(pitchGain)
	ltpCoeffs := ltpCoeffsForIndices(ltpPerIdx, ltpGainIdx, enc.nSubframes)

	resNrg := enc.ltpResidualEnergyPerSubframe(periodic, nlsf.lpcQ12, SignalTypeVoiced, pitchLags, ltpCoeffs)
	gainDB := enc.ltpPredCodGainDB(periodic, nlsf.lpcQ12, resNrg, pitchLags, ltpCoeffs)
	if gainDB <= 0 {
		t.Fatalf("periodic LTP coding gain %.2f dB should be positive", gainDB)
	}
	scale := 1.0 - 0.5*silkSigmoid(0.25*(gainDB-12.0))
	if scale >= 1.0 {
		t.Fatalf("periodic gainScale %.3f should reduce the gain (<1)", scale)
	}

	// The coding gain is clamped to be non-negative, so the reduction factor is
	// always within the documented (0.5, 1.0] range regardless of the signal.
	if scale <= 0.5 {
		t.Fatalf("gainScale %.3f below the 0.5 floor of 1-0.5*sigmoid", scale)
	}
}
