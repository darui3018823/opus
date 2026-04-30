package opus

import (
	"testing"
)

func TestEncoderTOCGeneration(t *testing.T) {
	// 48kHz Mono -> Config 20 -> TOC 0xA0 (10100 0 00)
	t.Run("48kHz Mono TOC", func(t *testing.T) {
		enc, err := NewEncoder(48000, 1, ApplicationAudio)
		if err != nil {
			t.Fatalf("Failed to create encoder: %v", err)
		}

		// 1 frame of silence
		pcm := make([]int16, 960)
		packet, err := enc.Encode(pcm, 960)
		if err != nil {
			t.Fatalf("Encode failed: %v", err)
		}

		if len(packet) < 1 {
			t.Fatal("Packet too short")
		}

		toc := packet[0]
		// Expected: Config 20 (10100) | s=0 | c=0 -> 10100000 = 0xA0
		// But wait, ParseTOC implementation uses:
		// config = (toc >> 3) & 0x1F
		// If config is 20, toc >> 3 should be 20.
		// 0xA0 >> 3 = 10100000 >> 3 = 00010100 = 20. Correct.

		if toc != 0xF8 {
			t.Errorf("Expected TOC 0xF8 (Config 31), got 0x%X", toc)
		}
	})

	// 48kHz Stereo -> Config 22 -> TOC 0xB0 (10110 0 00)
	t.Run("48kHz Stereo TOC", func(t *testing.T) {
		enc, err := NewEncoder(48000, 2, ApplicationAudio)
		if err != nil {
			t.Fatalf("Failed to create encoder: %v", err)
		}

		pcm := make([]int16, 960*2)
		packet, err := enc.Encode(pcm, 960)
		if err != nil {
			t.Fatalf("Encode failed: %v", err)
		}

		if len(packet) < 1 {
			t.Fatal("Packet too short")
		}

		toc := packet[0]
		// Expected: Config 22 (10110) | s=0 | c=0 -> 10110000 = 0xB0
		if toc != 0xFC {
			t.Errorf("Expected TOC 0xFC (Config 31 stereo), got 0x%X", toc)
		}
	})
}
