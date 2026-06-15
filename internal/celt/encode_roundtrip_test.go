package celt

import (
	"math"
	"math/rand"
	"testing"
)

// TestCeltSilenceRoundTrip verifies that a silent input frame is encoded as a
// minimal silence packet (the lone logp-15 flag) and that the decoder
// reconstructs digital silence with a matching final range — i.e. the encoder's
// silence path is bit-symmetric with the decoder's silence handling.
func TestCeltSilenceRoundTrip(t *testing.T) {
	const sr = 48000
	const fs = 960
	for _, ch := range []int{1, 2} {
		enc, err := NewEncoder(fs, sr, ch, DefaultEncoderConfig())
		if err != nil {
			t.Fatal(err)
		}
		dec, err := NewDecoder(fs, sr, ch)
		if err != nil {
			t.Fatal(err)
		}
		silent := make([]float64, fs*ch)
		for f := 0; f < 4; f++ {
			pkt, err := enc.Encode(silent)
			if err != nil {
				t.Fatal(err)
			}
			// CBR is the default rate mode (DTX off), so silent frames are
			// padded to the full target. The point checked here is the
			// range-coder symmetry and the silent reconstruction, not the size.
			out, err := dec.Decode(pkt)
			if err != nil {
				t.Fatalf("ch=%d frame=%d decode: %v", ch, f, err)
			}
			if er, dr := enc.FinalRange(), dec.LastFinalRange(); er != dr {
				t.Errorf("ch=%d frame=%d silence range mismatch: enc=%08x dec=%08x", ch, f, er, dr)
			}
			var peak float64
			for _, v := range out {
				if a := math.Abs(v); a > peak {
					peak = a
				}
			}
			if peak > 1e-6 {
				t.Errorf("ch=%d frame=%d: silence decoded to non-silence (peak=%g)", ch, f, peak)
			}
		}
	}
}

// TestCeltSilenceMinimalSize verifies that with DTX enabled, a silent frame
// produces a minimal packet even in CBR mode, while a loud frame still fills
// the target.
func TestCeltSilenceMinimalSize(t *testing.T) {
	const sr = 48000
	const fs = 960
	cfg := DefaultEncoderConfig()
	cfg.RateMode = RateModeCBR
	enc, err := NewEncoder(fs, sr, 1, cfg)
	if err != nil {
		t.Fatal(err)
	}
	enc.SetDTX(true)

	silent := make([]float64, fs)
	pkt, err := enc.Encode(silent)
	if err != nil {
		t.Fatal(err)
	}
	if len(pkt) > 4 {
		t.Errorf("DTX silent CBR packet too large: got %d bytes, want <=4", len(pkt))
	}

	loud := make([]float64, fs)
	for i := range loud {
		loud[i] = 0.5 * math.Sin(2*math.Pi*1000*float64(i)/sr)
	}
	pkt, err = enc.Encode(loud)
	if err != nil {
		t.Fatal(err)
	}
	if len(pkt) < 100 {
		t.Errorf("CBR loud packet unexpectedly small: %d bytes", len(pkt))
	}
}

// TestCeltSilenceCBRPaddedSize verifies that without DTX, a silent frame in CBR
// mode is padded to the full target size (constant-bitrate contract).
func TestCeltSilenceCBRPaddedSize(t *testing.T) {
	const sr = 48000
	const fs = 960
	cfg := DefaultEncoderConfig()
	cfg.RateMode = RateModeCBR
	cfg.Bitrate = 64000
	enc, err := NewEncoder(fs, sr, 1, cfg)
	if err != nil {
		t.Fatal(err)
	}
	silent := make([]float64, fs)
	pkt, err := enc.Encode(silent)
	if err != nil {
		t.Fatal(err)
	}
	want := 64000 * 20 / 1000 / 8 // CELT payload bytes at 64 kbps, 20 ms
	if len(pkt) != want {
		t.Errorf("CBR (no DTX) silent packet: got %d bytes, want %d", len(pkt), want)
	}
}

// TestTransientAnalysisDetection checks the transient detector fires on a sharp
// attack and stays quiet on a steady tone, using the same SIG-domain
// (overlap‖preemph) buffer layout the encoder builds.
func TestTransientAnalysisDetection(t *testing.T) {
	const fs = 960
	const ov = 120
	const sr = 48000
	length := fs + ov

	steady := make([]float64, length)
	for i := range steady {
		// 1 kHz tone, pre-emphasised ×32768 domain order of magnitude.
		steady[i] = 8000 * math.Sin(2*math.Pi*1000*float64(i)/sr)
	}
	if isT, _, _ := transientAnalysis([][]float64{steady}, length, 1); isT {
		t.Errorf("steady tone misclassified as transient")
	}

	attack := make([]float64, length)
	for i := range attack {
		if i >= ov+fs/2 {
			// Sudden onset halfway through the frame.
			attack[i] = 12000 * math.Sin(2*math.Pi*1000*float64(i)/sr)
		}
	}
	if isT, _, _ := transientAnalysis([][]float64{attack}, length, 1); !isT {
		t.Errorf("sharp attack not detected as transient")
	}
}

// TestCeltTransientRoundTrip drives the encoder onto the short-MDCT path with a
// genuine attack (a loud burst inside an otherwise silent frame) and checks two
// things: (1) every frame decodes with a matching final range — the short-block
// bitstream is valid and the self-decoder stays in sync, including across the
// transient↔steady boundary; (2) short blocks confine the burst's quantization
// noise in time, so the decoded pre-attack region is markedly quieter than with
// long blocks (the classic pre-echo reduction).
func TestCeltTransientRoundTrip(t *testing.T) {
	const sr = 48000
	const fs = 960
	const attackFrame = 4
	const burst0 = 700 // attack onset within the frame

	gen := func() [][]float64 {
		var fr [][]float64
		for f := 0; f < 8; f++ {
			frame := make([]float64, fs)
			if f == attackFrame {
				for i := burst0; i < burst0+160; i++ {
					n := f*fs + i
					frame[i] = 0.8 * math.Sin(2*math.Pi*2500*float64(n)/sr)
				}
			}
			fr = append(fr, frame)
		}
		return fr
	}

	// run encodes/decodes the signal at a given complexity (5 = transient
	// detection on → short blocks; 0 = transient off → long blocks) and returns
	// the concatenated decoded output. It fails on any final-range mismatch.
	run := func(complexity int) []float64 {
		cfg := DefaultEncoderConfig()
		cfg.Complexity = complexity
		enc, err := NewEncoder(fs, sr, 1, cfg)
		if err != nil {
			t.Fatal(err)
		}
		dec, err := NewDecoder(fs, sr, 1)
		if err != nil {
			t.Fatal(err)
		}
		var out []float64
		for f, frame := range gen() {
			pkt, err := enc.Encode(frame)
			if err != nil {
				t.Fatal(err)
			}
			dout, err := dec.Decode(pkt)
			if err != nil {
				t.Fatal(err)
			}
			if er, dr := enc.FinalRange(), dec.LastFinalRange(); er != dr {
				t.Fatalf("complexity=%d frame=%d range mismatch: enc=%08x dec=%08x", complexity, f, er, dr)
			}
			out = append(out, dout...)
		}
		return out
	}

	// preEcho measures decoded RMS in the silent region before the burst.
	preEcho := func(out []float64) float64 {
		base := attackFrame * fs
		var e float64
		n := 0
		for i := base; i < base+burst0-120; i++ {
			e += out[i] * out[i]
			n++
		}
		return math.Sqrt(e / float64(n))
	}

	shortPre := preEcho(run(5))
	longPre := preEcho(run(0))
	t.Logf("pre-echo RMS before burst: short=%.6f long=%.6f (long/short=%.2fx)", shortPre, longPre, longPre/shortPre)
	if shortPre >= longPre {
		t.Errorf("short blocks did not reduce pre-echo: short=%.6f long=%.6f", shortPre, longPre)
	}
}

// TestStereoAnalysisDecision checks the dual-stereo decision: highly correlated
// channels (L==R) should pick joint mid/side (false), while decorrelated channels
// should pick independent L/R coding (true).
func TestStereoAnalysisDecision(t *testing.T) {
	const frameLen = 800
	const lm = 3
	hi := int(EBands48000[13]) << lm // bands the analysis actually inspects

	corr := make([]float64, 2*frameLen)
	for j := 0; j < hi; j++ {
		v := math.Sin(float64(j))
		corr[j] = v
		corr[frameLen+j] = v // L == R
	}
	if stereoAnalysis(corr, frameLen, lm) {
		t.Errorf("correlated (L==R) channels: got dual_stereo=true, want false (mid/side)")
	}

	rng := rand.New(rand.NewSource(1))
	decorr := make([]float64, 2*frameLen)
	for j := 0; j < hi; j++ {
		decorr[j] = rng.NormFloat64()
		decorr[frameLen+j] = rng.NormFloat64() // independent
	}
	if !stereoAnalysis(decorr, frameLen, lm) {
		t.Errorf("decorrelated channels: got dual_stereo=false, want true (independent L/R)")
	}
}

// TestIntensityHysteresis checks the intensity-band decision: at a typical
// 64 kbps stereo rate it should select an intermediate band (so high bands use
// single-channel intensity coding), and the hysteresis must keep a previous
// choice stable against a small rate change near the boundary.
func TestIntensityHysteresis(t *testing.T) {
	th := intensityThresholds[:]
	hy := intensityHysteresis[:]
	n := len(th)

	// 64 kbps → val 64. thresholds[14]=62, thresholds[15]=67 → index 15.
	got := hysteresisDecision(64, th, hy, n, 0)
	if got != 15 {
		t.Errorf("intensity at 64 kbps: got band %d, want 15", got)
	}

	// Just below the boundary back down to band 14 from prev=15: with hysteresis,
	// val=63 (> thresholds[14]-hysteresis[14]=62-4=58) should stay at 15.
	if got := hysteresisDecision(63, th, hy, n, 15); got != 15 {
		t.Errorf("hysteresis from prev=15 at val=63: got %d, want 15 (sticky)", got)
	}
	// A large drop must overcome hysteresis and move down.
	if got := hysteresisDecision(40, th, hy, n, 15); got >= 15 {
		t.Errorf("large rate drop from prev=15 at val=40: got %d, want <15", got)
	}
}

// TestCeltStereoDecisionRoundTrip drives the encoder with correlated (L==R),
// anti-correlated (L==-R), and decorrelated stereo material so that both the
// joint mid/side and the independent dual-stereo branches, plus intensity-stereo
// high bands, are exercised. Each frame must decode with a matching final range
// (encoder/decoder stay in sync) and reconstruct non-trivial audio.
func TestCeltStereoDecisionRoundTrip(t *testing.T) {
	const sr = 48000
	const fs = 960

	gens := map[string]func(n int) []float64{
		"correlated": func(n int) []float64 {
			out := make([]float64, fs*2)
			for i := 0; i < fs; i++ {
				s := 0.5 * math.Sin(2*math.Pi*1000*float64(n+i)/sr)
				out[i*2], out[i*2+1] = s, s
			}
			return out
		},
		"anti-correlated": func(n int) []float64 {
			out := make([]float64, fs*2)
			for i := 0; i < fs; i++ {
				s := 0.5 * math.Sin(2*math.Pi*1000*float64(n+i)/sr)
				out[i*2], out[i*2+1] = s, -s
			}
			return out
		},
		"decorrelated": func(n int) []float64 {
			out := make([]float64, fs*2)
			for i := 0; i < fs; i++ {
				out[i*2] = 0.5 * math.Sin(2*math.Pi*700*float64(n+i)/sr)
				out[i*2+1] = 0.5 * math.Sin(2*math.Pi*1600*float64(n+i)/sr)
			}
			return out
		},
	}

	for name, gen := range gens {
		t.Run(name, func(t *testing.T) {
			enc, err := NewEncoder(fs, sr, 2, DefaultEncoderConfig())
			if err != nil {
				t.Fatal(err)
			}
			dec, err := NewDecoder(fs, sr, 2)
			if err != nil {
				t.Fatal(err)
			}
			var peak float64
			for f := 0; f < 6; f++ {
				pkt, err := enc.Encode(gen(f * fs))
				if err != nil {
					t.Fatal(err)
				}
				out, err := dec.Decode(pkt)
				if err != nil {
					t.Fatal(err)
				}
				if er, dr := enc.FinalRange(), dec.LastFinalRange(); er != dr {
					t.Fatalf("frame=%d range mismatch: enc=%08x dec=%08x", f, er, dr)
				}
				for _, v := range out {
					if a := math.Abs(v); a > peak {
						peak = a
					}
				}
			}
			if peak < 0.05 {
				t.Errorf("decoded peak too small (%g): reconstruction collapsed", peak)
			}
		})
	}
}

// TestCeltEncodeDecodeFinalRange checks that a self-encoded CELT frame decodes
// with a matching final range. A mismatch means the encoder and decoder
// desynced mid-frame (e.g. an asymmetric band-split symbol), which corrupts
// everything after the divergence point.
func TestCeltEncodeDecodeFinalRange(t *testing.T) {
	const sr = 48000
	const fs = 960
	for _, freq := range []float64{1000, 2000, 4000} {
		enc, err := NewEncoder(fs, sr, 1, DefaultEncoderConfig())
		if err != nil {
			t.Fatal(err)
		}
		dec, err := NewDecoder(fs, sr, 1)
		if err != nil {
			t.Fatal(err)
		}
		for f := 0; f < 4; f++ {
			frame := make([]float64, fs)
			for i := 0; i < fs; i++ {
				frame[i] = 0.5 * math.Sin(2*math.Pi*freq*float64(f*fs+i)/sr)
			}
			pkt, err := enc.Encode(frame)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := dec.Decode(pkt); err != nil {
				t.Fatal(err)
			}
			er := enc.FinalRange()
			dr := dec.LastFinalRange()
			if er != dr {
				t.Errorf("freq=%.0f frame=%d range mismatch: enc=%08x dec=%08x", freq, f, er, dr)
				break
			}
		}
	}
}
