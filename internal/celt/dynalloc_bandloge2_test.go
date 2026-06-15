package celt

import (
	"math"
	"testing"
)

// TestDynallocUsesBandLogE2 proves that dynallocAnalysis actually consults the
// separate bandLogE2 array (the long-block energy used on transients) when
// building its masking follower, rather than ignoring it. The masking depth that
// drives the per-band boost is bandLogE - follower, and the follower is built
// from bandLogE2; so lowering bandLogE2 at a peak band (without touching bandLogE)
// must lower the follower there and therefore raise the boost.
func TestDynallocUsesBandLogE2(t *testing.T) {
	const numBands = 21
	const end = 21
	const C = 1
	const lm = 3

	// Flat spectrum with a tonal peak at band 10.
	logE := make([]float64, numBands)
	for i := range logE {
		logE[i] = 0.0
	}
	logE[10] = 6.0

	// Baseline: bandLogE2 == bandLogE (the non-secondMdct / non-transient case).
	off1, _ := dynallocAnalysis(logE, append([]float64(nil), logE...), numBands, end, C, lm, false, false, false)

	// Now feed a bandLogE2 whose peak band is *lower* than bandLogE there, as a
	// sharper long-block estimate might be. The follower at band 10 should drop,
	// increasing the masking depth (logE[10]-follower) and hence the boost.
	logE2 := append([]float64(nil), logE...)
	logE2[10] = 0.0
	off2, _ := dynallocAnalysis(logE, logE2, numBands, end, C, lm, false, false, false)

	if off2[10] <= off1[10] {
		t.Fatalf("bandLogE2 not driving the follower: boost at peak band did not "+
			"increase when bandLogE2 was lowered (off1[10]=%d off2[10]=%d)", off1[10], off2[10])
	}
	t.Logf("peak-band boost: bandLogE2==bandLogE → %d, lowered bandLogE2 → %d", off1[10], off2[10])
}

// TestCeltSecondMdctRoundTrip drives the encoder at complexity 10 (secondMdct
// path enabled) with a transient, tonal frame so the second long-block MDCT and
// bandLogE2-based dynalloc are exercised. Every frame — including the transient —
// must decode with a matching final range, confirming the new analysis path
// stays bit-symmetric with the decoder (the decoder reads whatever boosts the
// encoder wrote, so any in-range result round-trips).
func TestCeltSecondMdctRoundTrip(t *testing.T) {
	const sr = 48000
	const fs = 960
	const attackFrame = 4
	const burst0 = 700

	// Tonal background plus a transient burst on attackFrame, so dynalloc has
	// both tonal peaks to boost and a transient frame to trigger secondMdct.
	gen := func() [][]float64 {
		var fr [][]float64
		for f := 0; f < 8; f++ {
			frame := make([]float64, fs)
			for i := 0; i < fs; i++ {
				n := f*fs + i
				frame[i] = 0.3 * math.Sin(2*math.Pi*1200*float64(n)/sr)
			}
			if f == attackFrame {
				for i := burst0; i < burst0+160; i++ {
					n := f*fs + i
					frame[i] += 0.8 * math.Sin(2*math.Pi*2500*float64(n)/sr)
				}
			}
			fr = append(fr, frame)
		}
		return fr
	}

	cfg := DefaultEncoderConfig()
	cfg.Complexity = 10
	enc, err := NewEncoder(fs, sr, 1, cfg)
	if err != nil {
		t.Fatal(err)
	}
	dec, err := NewDecoder(fs, sr, 1)
	if err != nil {
		t.Fatal(err)
	}
	for f, frame := range gen() {
		pkt, err := enc.Encode(frame)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := dec.Decode(pkt); err != nil {
			t.Fatal(err)
		}
		if er, dr := enc.FinalRange(), dec.LastFinalRange(); er != dr {
			t.Fatalf("complexity=10 frame=%d range mismatch: enc=%08x dec=%08x", f, er, dr)
		}
	}
}
