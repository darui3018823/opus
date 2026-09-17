package celt

import (
	"math/rand"
	"testing"
)

// TestPatchTransientDecision exercises the energy-rise fallback transient
// detector directly: a steady frame must not fire, a sharp cross-frame energy
// rise must fire, and a moderate rise below the libopus threshold (1.0) must
// not fire at that threshold.
func TestPatchTransientDecision(t *testing.T) {
	const stride = 21
	const start, end = 0, 21

	flat := func(v float64) []float64 {
		e := make([]float64, stride)
		for i := range e {
			e[i] = v
		}
		return e
	}

	old := flat(0.0)

	// Steady: newE == oldE → mean increase 0 → no patch at either threshold.
	if patchTransientDecision(flat(0.0), old, stride, start, end, 1, 1.0) {
		t.Error("steady energy should not trigger patch")
	}

	// Sharp rise: +3 across all bands → mean increase 3 > 1.0 → patch.
	if !patchTransientDecision(flat(3.0), old, stride, start, end, 1, 1.0) {
		t.Error("sharp +3 dB rise should trigger patch at threshold 1.0")
	}

	// Moderate rise of +0.7: below the libopus threshold (1.0), so no patch;
	// the threshold argument still scales the decision.
	mod := flat(0.7)
	if patchTransientDecision(mod, old, stride, start, end, 1, 1.0) {
		t.Error("+0.7 rise should NOT patch at the libopus threshold (1.0)")
	}
	if !patchTransientDecision(mod, old, stride, start, end, 1, 0.5) {
		t.Error("+0.7 rise SHOULD patch at a 0.5 threshold")
	}
}

// TestPatchTransientDecisionSpread verifies the aggressive (-1 dB/band) spreading
// of the previous frame's energies: a single loud old band masks an onset across
// the bands it spreads into, so the same uniform onset that fires against a quiet
// past frame is suppressed when a loud neighbour preceded it.
func TestPatchTransientDecisionSpread(t *testing.T) {
	const stride = 21
	const start, end = 0, 21

	newE := make([]float64, stride)
	for i := range newE {
		newE[i] = 5.0 // uniform onset
	}

	// Against a quiet past frame the onset fires.
	zero := make([]float64, stride)
	if !patchTransientDecision(newE, zero, stride, start, end, 1, 1.0) {
		t.Fatal("uniform onset over a silent past frame should patch")
	}

	// A single very loud band in the past frame spreads (decaying 1 dB/band) to
	// cover the whole frame above the onset level, masking it → no patch.
	loud := make([]float64, stride)
	loud[10] = 20.0
	if patchTransientDecision(newE, loud, stride, start, end, 1, 1.0) {
		t.Error("onset masked by a spread loud old band should not patch")
	}
}

// broadbandFrames builds deterministic broadband (white-noise) frames whose level
// jumps from loAmp to hiAmp at jumpFrame, with no within-frame attack — the kind
// of cross-frame energy rise patch_transient_decision is meant to catch.
func broadbandFrames(nFrames, fs, ch, jumpFrame int, loAmp, hiAmp float64) [][]float64 {
	rng := rand.New(rand.NewSource(12345))
	frames := make([][]float64, nFrames)
	for f := 0; f < nFrames; f++ {
		amp := loAmp
		if f >= jumpFrame {
			amp = hiAmp
		}
		frame := make([]float64, fs*ch)
		for i := 0; i < fs; i++ {
			v := amp * (2*rng.Float64() - 1)
			for c := 0; c < ch; c++ {
				frame[i*ch+c] = v
			}
		}
		frames[f] = frame
	}
	return frames
}

// TestCeltPatchTransientRoundTrip feeds quiet broadband noise followed by a sudden
// loud level (no within-frame attack, but a large cross-frame energy rise), so
// patch_transient_decision promotes the loud frame to a transient. The test
// checks that every frame still round-trips with a matching final range (the
// patched isTransient decision is signalled and read back by the decoder), for
// mono and stereo.
func TestCeltPatchTransientRoundTrip(t *testing.T) {
	const sr = 48000
	const fs = 960
	const jumpFrame = 4

	for _, ch := range []int{1, 2} {
		cfg := DefaultEncoderConfig()
		cfg.Complexity = 6 // >=5 so patch_transient runs, <8 so secondMdct is off
		enc, err := NewEncoder(fs, sr, ch, cfg)
		if err != nil {
			t.Fatal(err)
		}
		dec, err := NewDecoder(fs, sr, ch)
		if err != nil {
			t.Fatal(err)
		}
		frames := broadbandFrames(8, fs, ch, jumpFrame, 0.03, 0.5)
		for f, frame := range frames {
			pkt, err := enc.Encode(frame)
			if err != nil {
				t.Fatalf("ch=%d frame=%d encode: %v", ch, f, err)
			}
			if _, err := dec.Decode(pkt); err != nil {
				t.Fatalf("ch=%d frame=%d decode: %v", ch, f, err)
			}
			if er, dr := enc.FinalRange(), dec.LastFinalRange(); er != dr {
				t.Errorf("ch=%d frame=%d final range mismatch: enc=%08x dec=%08x", ch, f, er, dr)
			}
		}
	}
}
