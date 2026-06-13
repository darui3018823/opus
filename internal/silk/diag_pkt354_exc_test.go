package silk

import "testing"

// TestSILKtv03Pkt354Exc decodes tv03 pkt354 (unvoiced, the first packet whose
// 12kHz output diverges from the oracle, at sample 7) in isolation and compares
// the reconstructed excitation excQ14 against the libopus oracle SILK_EXC_Q14.
// Since the warm state is irrelevant to excitation (it depends only on pulses,
// seed and quant offset, all decoded per-packet), this isolates whether the
// sample-7 divergence is a decode/excitation bug or a synthesis-state bug.
func TestSILKtv03Pkt354Exc(t *testing.T) {
	pkt := readOpusDemoPacket(t, "testvector03.bit", 354)
	toc := pkt[0]
	config := int((toc >> 3) & 0x1f)
	countCode := int(toc & 3)
	nFrames, stream := silkOracleFrameCount(config, countCode, pkt[1:])
	dec, err := NewDecoderWithFrameMs(12000, 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	tr := &decodeTrace{}
	dec.trace = tr
	if _, derr := dec.DecodeMulti(stream, nFrames); derr != nil {
		t.Fatalf("decode: %v", derr)
	}
	f := tr.Frames[0]
	t.Logf("sig=%d qoff=%d seed=%d", f.SignalType, f.QuantOffset, f.Seed)
	t.Logf("our pulses[0:16] = %v", f.Pulses[:16])
	t.Logf("oracle  [0:16]   = [-1 0 -2 -3 0 -1 0 3 0 -1 -2 0 -2 4 -3 0]")
	t.Logf("our tell: beforePulses=%d beforeSigns=%d afterPulses=%d rng=%08x", f.TellBeforePulse, f.TellBeforeSigns, f.TellAfterPulses, f.RngAfterPulses)
	t.Logf("oracle tell: afterIndices=47 afterPulses=357")

	oracleMag := []int{1, 0, 2, 3, 0, 1, 0, 3, 0, 1, 2, 0, 2, 4, 3, 0, 1, 3, 1, 0, 1, 2, 2, 1, 2, 0, 0, 1, 0, 3, 1, 1, 1, 1, 1, 2, 1, 1, 1, 0, 0, 0, 0, 1, 1, 1, 0, 1, 0, 2, 0, 0, 1, 0, 1, 0, 0, 0, 1, 0, 1, 0, 0, 1, 0, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1, 0, 0, 1, 0, 1, 0, 1, 0, 1, 0, 1, 1, 0, 0, 0, 0, 0, 0, 0, 1, 2, 0, 2, 2, 1, 2, 3, 4, 2, 5, 5, 3, 2, 2, 0, 5, 1, 4, 2, 0, 0, 4, 0}
	for i := 0; i < 120 && i < len(f.Pulses); i++ {
		m := int(f.Pulses[i])
		if m < 0 {
			m = -m
		}
		if m != oracleMag[i] {
			t.Logf("MAGNITUDE DIFF at index %d: our=%d oracle=%d", i, m, oracleMag[i])
		}
	}

	// Oracle SILK_EXC_Q14 (first 16 of 120).
	wantExc := []int32{-11264, -3840, 27648, -44032, 3840, 11264, 3840, 51712, 3840, -11264, -27648, 3840, 27648, -68096, 44032}
	t.Logf("our excQ14[0:15] = %v", f.ExcQ14[:15])
	t.Logf("oracle  [0:15]   = %v", wantExc)
	for i := range wantExc {
		if f.ExcQ14[i] != wantExc[i] {
			t.Errorf("excQ14[%d]=%d want=%d", i, f.ExcQ14[i], wantExc[i])
		}
	}
}
