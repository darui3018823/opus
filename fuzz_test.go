package opus

import (
	"bytes"
	"reflect"
	"sort"
	"testing"
)

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

// FuzzPacketExtensions exercises the RFC packet framing and extension-padding
// parsers together. Successfully parsed extensions must survive regeneration.
func FuzzPacketExtensions(f *testing.F) {
	base := []byte{byte(16 << 3), 0x11, 0x22, 0x33}
	f.Add(base)
	packet, err := PacketExtensionsGenerate(base, []PacketExtension{
		{ID: 3, Frame: 0, Data: []byte{0x7a}},
		{ID: ExtensionIDDRED, Frame: 0, Data: []byte("opaque")},
	}, 32)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(packet)
	f.Add([]byte{})

	f.Fuzz(func(t *testing.T, data []byte) {
		count, countErr := PacketExtensionsCount(data)
		extensions, parseErr := PacketExtensionsParse(data)
		if (countErr == nil) != (parseErr == nil) {
			t.Fatalf("count error=%v, parse error=%v", countErr, parseErr)
		}
		if parseErr != nil {
			return
		}
		if count != len(extensions) {
			t.Fatalf("count=%d, parsed=%d", count, len(extensions))
		}
		regenerated, err := PacketExtensionsGenerate(data, extensions, 0)
		if err != nil {
			t.Fatalf("regenerate parsed extensions: %v", err)
		}
		got, err := PacketExtensionsParse(regenerated)
		if err != nil {
			t.Fatalf("parse regenerated extensions: %v", err)
		}
		if !reflect.DeepEqual(canonicalExtensions(got), canonicalExtensions(extensions)) {
			t.Fatalf("regenerated extensions differ: got %#v, want %#v", got, extensions)
		}
	})
}

func canonicalExtensions(extensions []PacketExtension) []PacketExtension {
	out := append([]PacketExtension(nil), extensions...)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Frame != out[j].Frame {
			return out[i].Frame < out[j].Frame
		}
		if out[i].ID != out[j].ID {
			return out[i].ID < out[j].ID
		}
		return bytes.Compare(out[i].Data, out[j].Data) < 0
	})
	return out
}

// FuzzMultistreamDecode targets Appendix B self-delimited packet parsing with
// one coupled stream followed by one mono stream.
func FuzzMultistreamDecode(f *testing.F) {
	mapping := []byte{0, 1, 2}
	enc, err := NewMultistreamEncoder(48000, 3, 2, 1, mapping, ApplicationAudio)
	if err != nil {
		f.Fatal(err)
	}
	packet, err := enc.Encode(make([]int16, 960*3), 960)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(packet)
	f.Add([]byte{})

	f.Fuzz(func(t *testing.T, data []byte) {
		dec, err := NewMultistreamDecoder(48000, 3, 2, 1, mapping)
		if err != nil {
			t.Fatalf("NewMultistreamDecoder: %v", err)
		}
		_, _ = dec.DecodeFloat(data)
	})
}

// FuzzRepacketizer targets packet splitting, frame accumulation, packet
// reconstruction, and code-3 padding removal.
func FuzzRepacketizer(f *testing.F) {
	enc, err := NewEncoder(48000, 1, ApplicationAudio)
	if err != nil {
		f.Fatal(err)
	}
	packet, err := enc.Encode(make([]int16, 960), 960)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(packet, uint8(32))
	f.Add([]byte{}, uint8(0))

	f.Fuzz(func(t *testing.T, data []byte, extra uint8) {
		rp := NewRepacketizer()
		if err := rp.Cat(data); err != nil {
			return
		}
		out, err := rp.Out()
		if err != nil {
			t.Fatalf("Out after successful Cat: %v", err)
		}
		canonical, err := PacketUnpad(out)
		if err != nil {
			t.Fatalf("PacketUnpad output: %v", err)
		}
		if !bytes.Equal(canonical, out) {
			t.Fatalf("repacketizer output is not canonical")
		}
		padded, err := PacketPad(out, len(out)+int(extra%64))
		if err != nil {
			return
		}
		unpadded, err := PacketUnpad(padded)
		if err != nil {
			t.Fatalf("PacketUnpad padded output: %v", err)
		}
		if !bytes.Equal(unpadded, out) {
			t.Fatalf("pad/unpad changed repacketizer output")
		}
	})
}
