package opus

import (
	"math"
	"testing"
)

func TestPhaseInversionControl(t *testing.T) {
	enc, err := NewEncoder(48000, 2, ApplicationAudio)
	if err != nil {
		t.Fatal(err)
	}
	if enc.PhaseInversionDisabled() {
		t.Fatal("encoder phase inversion disabled by default")
	}
	enc.SetPhaseInversionDisabled(true)
	if !enc.PhaseInversionDisabled() {
		t.Fatal("encoder phase inversion setting was not retained")
	}
	for i, celtEnc := range enc.celtEncoders {
		if celtEnc != nil && !celtEnc.PhaseInversionDisabled() {
			t.Fatalf("CELT encoder %d did not receive phase inversion setting", i)
		}
	}

	dec, err := NewDecoder(48000, 2)
	if err != nil {
		t.Fatal(err)
	}
	if dec.PhaseInversionDisabled() {
		t.Fatal("decoder phase inversion disabled by default")
	}
	dec.SetPhaseInversionDisabled(true)
	if !dec.PhaseInversionDisabled() {
		t.Fatal("decoder phase inversion setting was not retained")
	}
	for bw := range dec.celtDecoders {
		for lm := range dec.celtDecoders[bw] {
			for ch := range dec.celtDecoders[bw][lm] {
				celtDec := dec.celtDecoders[bw][lm][ch]
				if celtDec != nil && !celtDec.PhaseInversionDisabled() {
					t.Fatalf("CELT decoder [%d][%d][%d] did not receive setting", bw, lm, ch)
				}
			}
		}
	}
}

func TestPhaseInversionDisabledRoundTrip(t *testing.T) {
	enc, err := NewEncoder(48000, 2, ApplicationAudio)
	if err != nil {
		t.Fatal(err)
	}
	enc.SetVBR(true)
	enc.SetPhaseInversionDisabled(true)
	pcm := make([]float64, 960*2)
	for i := 0; i < 960; i++ {
		// Anti-correlated stereo drives intensity-stereo inversion decisions.
		v := 0.6 * math.Sin(2*math.Pi*997*float64(i)/48000)
		pcm[2*i], pcm[2*i+1] = v, -v
	}
	packet, err := enc.EncodeFloat(pcm, 960)
	if err != nil {
		t.Fatal(err)
	}
	dec, err := NewDecoder(48000, 2)
	if err != nil {
		t.Fatal(err)
	}
	out, err := dec.DecodeFloat(packet)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != len(pcm) {
		t.Fatalf("decoded %d samples, want %d", len(out), len(pcm))
	}
}
