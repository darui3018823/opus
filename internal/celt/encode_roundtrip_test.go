package celt

import (
	"math"
	"testing"
)

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
