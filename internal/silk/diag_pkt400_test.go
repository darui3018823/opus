package silk

import "testing"

// TestSILKtv03Pkt400Isolated decodes tv03 packet 400 (worst 12kHz MB mono voiced
// burst per localization) in isolation and compares decoded params to the oracle.
// pkt400: config 4 (MB 12kHz 10ms), signalType=2 voiced, nb_subfr=2.
func TestSILKtv03Pkt400Isolated(t *testing.T) {
	pkt := readOpusDemoPacket(t, "testvector03.bit", 400)
	toc := pkt[0]
	config := int((toc >> 3) & 0x1f)
	countCode := int(toc & 3)
	t.Logf("pkt400: config=%d countCode=%d", config, countCode)

	nFrames, stream := silkOracleFrameCount(config, countCode, pkt[1:])
	dec, err := NewDecoderWithFrameMs(12000, 1, 10) // config 4 = 10ms
	if err != nil {
		t.Fatal(err)
	}
	tr := &decodeTrace{}
	dec.trace = tr
	if _, derr := dec.DecodeMulti(stream, nFrames); derr != nil {
		t.Fatalf("decode: %v", derr)
	}
	f := tr.Frames[0]
	t.Logf("sig=%d interp=%d", f.SignalType, f.InterpFactor)
	t.Logf("  GainsQ16  = %v (want [3080192 2621440])", f.GainsQ16)
	t.Logf("  NLSF_Q15  = %v", f.NLSFQ15)
	t.Logf("  PredCoef0 = %v", f.PredCoef0Q12)
	t.Logf("  PitchLags = %v (want [134 130])", f.PitchLags)
	t.Logf("  LTPCoefQ14= %v", f.LTPCoefQ14)
	t.Logf("  LTPScale  = %d Seed=%d", f.LTPScaleQ14, f.Seed)

	wantGains := []int32{3080192, 2621440}
	wantNLSF := []int16{2970, 3018, 4022, 12287, 13125, 14609, 17399, 21354, 24648, 25696}
	wantPred := []int16{6791, -6470, 6886, -5216, 5652, -6651, 4072, -3136, 1207, 457}
	wantPitch := []int{134, 130}
	wantLTP := []int16{-1280, 4736, 8320, -512, 384, 0, 0, 256, 0, 0}

	for i := range wantGains {
		if i < len(f.GainsQ16) && f.GainsQ16[i] != wantGains[i] {
			t.Errorf("GainsQ16[%d]=%d want=%d", i, f.GainsQ16[i], wantGains[i])
		}
	}
	for i := range wantNLSF {
		if i < len(f.NLSFQ15) && f.NLSFQ15[i] != wantNLSF[i] {
			t.Errorf("NLSF[%d]=%d want=%d", i, f.NLSFQ15[i], wantNLSF[i])
		}
	}
	for i := range wantPred {
		if i < len(f.PredCoef0Q12) && f.PredCoef0Q12[i] != wantPred[i] {
			t.Errorf("Pred[%d]=%d want=%d", i, f.PredCoef0Q12[i], wantPred[i])
		}
	}
	for i := range wantPitch {
		if i < len(f.PitchLags) && f.PitchLags[i] != wantPitch[i] {
			t.Errorf("Pitch[%d]=%d want=%d", i, f.PitchLags[i], wantPitch[i])
		}
	}
	for i := range wantLTP {
		if i < len(f.LTPCoefQ14) && f.LTPCoefQ14[i] != wantLTP[i] {
			t.Errorf("LTP[%d]=%d want=%d", i, f.LTPCoefQ14[i], wantLTP[i])
		}
	}
}
