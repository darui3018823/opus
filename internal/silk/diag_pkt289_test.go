package silk

import (
	"testing"
)

// TestSILKPkt289Isolated decodes tv02 packet 289 (the worst mono-region voiced
// burst per TestSILKResidualLocalization) in ISOLATION (fresh decoder state) and
// compares the bitstream-decoded parameters against the libopus oracle trace.
//
// Because each Opus packet is range-coded independently, the decoded indices /
// gains / NLSF / LPC of frame 0 (cond=0, absolute coding) do NOT depend on prior
// packets. So if these match the oracle, the residual at pkt289 comes purely from
// warm synthesis state (LPC/LTP history) carried across packets, NOT from a
// per-packet decode bug. If they differ, we have found a decode bug.
//
// Oracle pkt289 (config=0, NB 10ms, signalType=2 voiced):
//
//	GAINS_Q16   = [1925120, 1925120]
//	NLSF_Q15    = [2970 3699 3987 10547 16040 20354 21548 24598 25593 28080]
//	PREDCOEF0   = [659 2818 4082 -2367 -2494 -1364 958 233 -150 758]
//	PITCHL      = [33, 33]
//	LTPCOEF_Q14 = [-128 512 15872 256 -512 -128 512 15872 256 -512]
//	SUM_PULSES  = [11 10 6 4 7]
func TestSILKPkt289Isolated(t *testing.T) {
	pkt := readOpusDemoPacket(t, "testvector02.bit", 289)
	toc := pkt[0]
	config := int((toc >> 3) & 0x1f)
	countCode := int(toc & 3)
	t.Logf("pkt289: TOC=0x%02x config=%d countCode=%d stereo=%d", toc, config, countCode, (toc>>2)&1)

	nFrames, stream := silkOracleFrameCount(config, countCode, pkt[1:])

	// config 0 => NB 10ms.
	frameMs := 10
	switch config & 3 {
	case 1:
		frameMs = 20
	}
	dec, err := NewDecoderWithFrameMs(8000, 1, frameMs)
	if err != nil {
		t.Fatal(err)
	}
	tr := &decodeTrace{}
	dec.trace = tr
	pcm, derr := dec.DecodeMulti(stream, nFrames)
	if derr != nil {
		t.Fatalf("decode: %v", derr)
	}
	t.Logf("nFrames=%d frameSize=%d produced=%d", nFrames, dec.frameSize, len(pcm))

	if len(tr.Frames) == 0 {
		t.Fatal("no traced frames")
	}
	f := tr.Frames[0]
	t.Logf("frame0: sig=%d qoff=%d interp=%d", f.SignalType, f.QuantOffset, f.InterpFactor)
	t.Logf("  GainsQ16  = %v", f.GainsQ16)
	t.Logf("  NLSF_Q15  = %v", f.NLSFQ15)
	t.Logf("  PredCoef0 = %v", f.PredCoef0Q12)

	wantGains := []int32{1925120, 1925120}
	wantNLSF := []int16{2970, 3699, 3987, 10547, 16040, 20354, 21548, 24598, 25593, 28080}
	wantPred := []int16{659, 2818, 4082, -2367, -2494, -1364, 958, 233, -150, 758}

	cmpI32 := func(name string, got []int32, want []int32) {
		if len(got) != len(want) {
			t.Errorf("%s len=%d want=%d", name, len(got), len(want))
			return
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("%s[%d]=%d want=%d", name, i, got[i], want[i])
			}
		}
	}
	cmpI16 := func(name string, got, want []int16) {
		if len(got) != len(want) {
			t.Errorf("%s len=%d want=%d", name, len(got), len(want))
			return
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("%s[%d]=%d want=%d", name, i, got[i], want[i])
			}
		}
	}
	cmpI32("GainsQ16", f.GainsQ16, wantGains)
	cmpI16("NLSF_Q15", f.NLSFQ15, wantNLSF)
	cmpI16("PredCoef0", f.PredCoef0Q12, wantPred)

	// Voiced-specific decode.
	t.Logf("  PitchLags   = %v", f.PitchLags)
	t.Logf("  LTPCoefQ14  = %v", f.LTPCoefQ14)
	t.Logf("  LTPScaleQ14 = %d  Seed=%d", f.LTPScaleQ14, f.Seed)

	wantPitch := []int{33, 33}
	wantLTP := []int16{-128, 512, 15872, 256, -512, -128, 512, 15872, 256, -512}
	if len(f.PitchLags) != len(wantPitch) {
		t.Errorf("PitchLags len=%d want=%d", len(f.PitchLags), len(wantPitch))
	} else {
		for i := range wantPitch {
			if f.PitchLags[i] != wantPitch[i] {
				t.Errorf("PitchLags[%d]=%d want=%d", i, f.PitchLags[i], wantPitch[i])
			}
		}
	}
	cmpI16("LTPCoefQ14", f.LTPCoefQ14, wantLTP)
	if f.LTPScaleQ14 != 15565 {
		t.Errorf("LTPScaleQ14=%d want=15565", f.LTPScaleQ14)
	}

	// Pulses: compare per-shell-block magnitude sums against oracle SUM_PULSES.
	// SILK_SUM_PULSES = [11 10 6 4 7] across 5 shell blocks of 16 samples.
	sums := make([]int, 5)
	for i, p := range f.Pulses {
		blk := i / 16
		if blk < 5 {
			if p < 0 {
				sums[blk] += int(-p)
			} else {
				sums[blk] += int(p)
			}
		}
	}
	t.Logf("  Pulses sum/block = %v (oracle SUM_PULSES=[11 10 6 4 7])", sums)
	wantSums := []int{11, 10, 6, 4, 7}
	for i := range wantSums {
		if sums[i] != wantSums[i] {
			t.Errorf("sumPulses[block %d]=%d want=%d", i, sums[i], wantSums[i])
		}
	}
}
