package opus

import "testing"

// TestDecoderTOCOnlyPackets follows opus_decode_frame for frames of at most
// one byte: they are concealed in the previous mode (silence before any
// packet, without recording a mode) and report a zero final range.
func TestDecoderTOCOnlyPackets(t *testing.T) {
	for _, toc := range []byte{0xfc, 0x78, 0x0b << 3} {
		dec, err := NewDecoder(48000, 1)
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 3; i++ {
			pcm, err := dec.DecodeFloat([]byte{toc})
			if err != nil {
				t.Fatalf("toc %#x packet %d before any audio: %v", toc, i, err)
			}
			for _, v := range pcm {
				if v != 0 {
					t.Fatalf("toc %#x packet %d before any audio is not silent", toc, i)
				}
			}
			if dec.FinalRange() != 0 {
				t.Fatalf("toc %#x packet %d: final range %08x", toc, i, dec.FinalRange())
			}
		}

		enc, err := NewEncoder(48000, 1, ApplicationAudio)
		if err != nil {
			t.Fatal(err)
		}
		if err := enc.SetModePolicy(ModePolicyLibopus); err != nil {
			t.Fatal(err)
		}
		pcm := strictSpeechLikeFrame(48000, 1, 0, 960)
		pkt, err := enc.EncodeFloat(pcm, 960)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := dec.DecodeFloat(pkt); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 3; i++ {
			if _, err := dec.DecodeFloat([]byte{pkt[0] &^ 3}); err != nil {
				t.Fatalf("toc %#x: TOC-only packet %d after audio: %v", toc, i, err)
			}
			if dec.FinalRange() != 0 {
				t.Fatalf("toc %#x: TOC-only packet %d after audio: final range %08x", toc, i, dec.FinalRange())
			}
		}
	}
}
