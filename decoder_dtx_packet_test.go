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

// TestDecoderShortFramesInOtherModes decodes frames of at most one byte
// whose TOC mode differs from the previous packet's: they are concealed in
// the previous mode, which stays the mode later concealment continues.
func TestDecoderShortFramesInOtherModes(t *testing.T) {
	enc, err := NewEncoder(16000, 1, ApplicationVOIP)
	if err != nil {
		t.Fatal(err)
	}
	if err := enc.SetBitrate(12000); err != nil {
		t.Fatal(err)
	}
	var silk [][]byte
	for p := 0; p < 4; p++ {
		pkt, err := enc.EncodeFloat(strictSpeechLikeFrame(16000, 1, p*320, 320), 320)
		if err != nil {
			t.Fatal(err)
		}
		if mode, _ := PacketGetMode(pkt); mode != ModeSILKOnly {
			t.Fatalf("setup packet %d mode %d, want SILK", p, mode)
		}
		silk = append(silk, pkt)
	}
	for _, short := range [][]byte{{0x7f, 0x02}, {0xff, 0x02}, {0x7c}, {0xf8}} {
		dec, err := NewDecoder(48000, 2)
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range silk[:3] {
			if _, err := dec.DecodeFloat(p); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := dec.DecodeFloat(short); err != nil {
			t.Fatalf("% x after SILK: %v", short, err)
		}
		if dec.FinalRange() != 0 {
			t.Fatalf("% x after SILK: final range %08x", short, dec.FinalRange())
		}
		if _, err := dec.DecodePLCFloat(960); err != nil {
			t.Fatalf("PLC after % x: %v", short, err)
		}
		if _, err := dec.DecodeFloat(silk[3]); err != nil {
			t.Fatalf("SILK after % x: %v", short, err)
		}
	}
}
