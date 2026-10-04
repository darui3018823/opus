package opus

import (
	"bytes"
	"errors"
	"testing"
)

// TestPacketPadKeepsExtensions checks the libopus repacketizer semantics of
// padding: extensions survive, large paddings encode their length with 255
// continuation bytes, and the frames are unchanged.
func TestPacketPadKeepsExtensions(t *testing.T) {
	base := []byte{0xfc, 1, 2, 3, 4, 5}
	withExt, err := PacketExtensionsGenerate(base, []PacketExtension{{ID: 40, Frame: 0, Data: []byte("ext")}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, extra := range []int{1, 2, 254, 255, 256, 257, 600} {
		padded, err := PacketPad(withExt, len(withExt)+extra)
		if err != nil {
			t.Fatalf("pad +%d: %v", extra, err)
		}
		if len(padded) != len(withExt)+extra {
			t.Fatalf("pad +%d: %d bytes", extra, len(padded))
		}
		exts, err := PacketExtensionsParse(padded)
		if err != nil || len(exts) != 1 || exts[0].ID != 40 || !bytes.Equal(exts[0].Data, []byte("ext")) {
			t.Fatalf("pad +%d: extensions %+v, %v", extra, exts, err)
		}
		_, frames, _, err := PacketParse(padded)
		if err != nil || len(frames) != 1 || !bytes.Equal(frames[0], base[1:]) {
			t.Fatalf("pad +%d: frames %x, %v", extra, frames, err)
		}
		unpadded, err := PacketUnpad(padded)
		if err != nil || !bytes.Equal(unpadded, base) {
			t.Fatalf("unpad +%d: %x, %v", extra, unpadded, err)
		}
	}
	// opus_packet_pad returns OPUS_OK for len == new_len without parsing.
	if got, err := PacketPad([]byte{0xff}, 1); err != nil || !bytes.Equal(got, []byte{0xff}) {
		t.Fatalf("same-length pad of an invalid packet = %x, %v", got, err)
	}
	if _, err := PacketPad(nil, 4); !errors.Is(err, ErrBadArg) {
		t.Fatalf("empty packet: %v, want ErrBadArg", err)
	}
}

// TestMultistreamPacketPadKeepsEarlierStreams checks that only the last
// elementary packet changes, like opus_multistream_packet_pad.
func TestMultistreamPacketPadKeepsEarlierStreams(t *testing.T) {
	// Stream 0: self-delimited code 0 with an explicit 3-byte size, stream 1:
	// a regular 2-byte frame.
	first := []byte{0xf8, 3, 9, 9, 9}
	packet := append(append([]byte(nil), first...), 0xf8, 7, 7)
	padded, err := MultistreamPacketPad(packet, 2, len(packet)+300)
	if err != nil {
		t.Fatal(err)
	}
	if len(padded) != len(packet)+300 || !bytes.Equal(padded[:len(first)], first) {
		t.Fatalf("padded %d bytes, prefix %x", len(padded), padded[:len(first)])
	}
	unpadded, err := MultistreamPacketUnpad(padded, 2)
	if err != nil || !bytes.Equal(unpadded, packet) {
		t.Fatalf("unpad = %x, %v; want %x", unpadded, err, packet)
	}
}
