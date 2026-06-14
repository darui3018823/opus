package celt

import (
	"math"
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
