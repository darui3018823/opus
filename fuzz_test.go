package opus

import "testing"

// FuzzDecode feeds arbitrary bytes to the int16 decoder and asserts that no
// input causes a panic. Malformed Opus packets must be rejected with an error,
// never crash the decoder.
func FuzzDecode(f *testing.F) {
	// Seed corpus: a few minimal TOC-prefixed shapes (config/stereo/code bits).
	f.Add([]byte{0xfc, 0x00, 0x00}) // CELT FB 20ms stereo, code 0
	f.Add([]byte{0x00, 0x00})       // SILK NB 10ms mono
	f.Add([]byte{0x0c, 0xff, 0xff}) // hybrid SWB 10ms
	f.Add([]byte{0x41, 0xff, 0xff}) // code-1 with padding-like tail
	f.Add([]byte{})                 // empty

	// 120 ms at 48 kHz is the largest Opus frame: 5760 samples per channel.
	pcm := make([]int16, 5760*2)

	f.Fuzz(func(t *testing.T, data []byte) {
		dec, err := NewDecoder(48000, 2)
		if err != nil {
			t.Fatalf("NewDecoder: %v", err)
		}
		// Errors are expected on malformed input; we only require no panic.
		_, _ = dec.Decode(data, pcm)
	})
}

// FuzzDecodeFloat is the float64 counterpart of FuzzDecode.
func FuzzDecodeFloat(f *testing.F) {
	f.Add([]byte{0xfc, 0x00, 0x00})
	f.Add([]byte{0x00, 0x00})
	f.Add([]byte{})

	f.Fuzz(func(t *testing.T, data []byte) {
		dec, err := NewDecoder(48000, 2)
		if err != nil {
			t.Fatalf("NewDecoder: %v", err)
		}
		_, _ = dec.DecodeFloat(data)
	})
}
