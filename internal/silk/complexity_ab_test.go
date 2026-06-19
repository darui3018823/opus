package silk

import (
	"math"
	"testing"
)

// TestComplexityEffectiveScaling verifies that SetComplexity is actually wired
// through to the SILK encoder: the pitch-estimation depth and the noise-shaping /
// delayed-decision NSQ parameters must change with the complexity level, and
// every level (including the maximum, where shapingLPCOrder reaches
// silkMaxShapeLPCOrder) must encode without panicking. The high-complexity path
// previously overflowed a fixed-size scratch array in silkSchurFLP.
func TestComplexityEffectiveScaling(t *testing.T) {
	const fs = 16000
	const frameMs = 20
	n := fs * frameMs / 1000

	makeFrame := func(phase float64) []float64 {
		x := make([]float64, n)
		for i := range x {
			tt := (phase + float64(i)) / fs
			x[i] = 0.5*math.Sin(2*math.Pi*180*tt) +
				0.25*math.Sin(2*math.Pi*360*tt) +
				0.12*math.Sin(2*math.Pi*540*tt)
		}
		return x
	}

	encodeAt := func(cx int) (bytes int, snr float64) {
		enc, err := NewEncoder(fs, 1)
		if err != nil {
			t.Fatal(err)
		}
		if err := enc.SetComplexity(cx); err != nil {
			t.Fatal(err)
		}
		_ = enc.SetBitrate(24000)
		dec, err := NewDecoder(fs, 1)
		if err != nil {
			t.Fatal(err)
		}
		var sigEnergy, errEnergy float64
		phase := 0.0
		for f := 0; f < 8; f++ {
			in := makeFrame(phase)
			phase += float64(n)
			pkt, err := enc.Encode(in)
			if err != nil {
				t.Fatalf("cx=%d frame=%d encode: %v", cx, f, err)
			}
			bytes += len(pkt)
			out, err := dec.Decode(pkt)
			if err != nil {
				t.Fatalf("cx=%d frame=%d decode: %v", cx, f, err)
			}
			if len(out) > n {
				out = out[:n]
			}
			if f >= 2 { // skip warm-up frames
				for i := 0; i < n; i++ {
					sigEnergy += in[i] * in[i]
					d := in[i] - out[i]
					errEnergy += d * d
				}
			}
		}
		return bytes, 10 * math.Log10(sigEnergy/(errEnergy+1e-12))
	}

	// 1) Parameter table must scale with complexity (pitch depth + NSQ states).
	enc, _ := NewEncoder(fs, 1)
	_ = enc.SetComplexity(0)
	peLo, ordLo, _ := enc.pitchEstParams()
	cfgLo := enc.silkComplexityConfig()
	_ = enc.SetComplexity(10)
	peHi, ordHi, _ := enc.pitchEstParams()
	cfgHi := enc.silkComplexityConfig()
	if !(peHi > peLo) || !(ordHi > ordLo) {
		t.Errorf("pitch depth did not scale: cx0=(pe=%d,ord=%d) cx10=(pe=%d,ord=%d)", peLo, ordLo, peHi, ordHi)
	}
	if !(cfgHi.nStatesDelayedDecision > cfgLo.nStatesDelayedDecision) {
		t.Errorf("NSQ states did not scale: cx0=%d cx10=%d", cfgLo.nStatesDelayedDecision, cfgHi.nStatesDelayedDecision)
	}
	if !(cfgHi.shapingLPCOrder > cfgLo.shapingLPCOrder) {
		t.Errorf("shaping LPC order did not scale: cx0=%d cx10=%d", cfgLo.shapingLPCOrder, cfgHi.shapingLPCOrder)
	}

	// 2) Every complexity level must encode/decode without panicking, and the
	//    low end must not match the high end (the setting has a real effect).
	bytesLo, snrLo := encodeAt(0)
	for cx := 1; cx <= 10; cx++ {
		encodeAt(cx) // mainly guards against the high-complexity schur overflow panic
	}
	bytesHi, snrHi := encodeAt(10)
	t.Logf("cx0: bytes=%d snr=%.2f dB | cx10: bytes=%d snr=%.2f dB", bytesLo, snrLo, bytesHi, snrHi)
	if snrHi <= snrLo {
		t.Errorf("max complexity did not improve quality: cx0 snr=%.2f cx10 snr=%.2f", snrLo, snrHi)
	}
}
