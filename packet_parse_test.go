package opus

import (
	"bytes"
	"errors"
	"testing"
)

func TestPacketParse(t *testing.T) {
	tests := []struct {
		name   string
		packet []byte
		offset int
		frames [][]byte
	}{
		{"code 0", []byte{0xf8, 1, 2, 3}, 1, [][]byte{{1, 2, 3}}},
		{"code 0 empty", []byte{0xf8}, 1, [][]byte{{}}},
		{"code 1", []byte{0xf9, 1, 2, 3, 4}, 1, [][]byte{{1, 2}, {3, 4}}},
		{"code 2", []byte{0xfa, 1, 9, 7, 8}, 2, [][]byte{{9}, {7, 8}}},
		{"code 3 CBR", []byte{0xfb, 0x03, 1, 2, 3}, 2, [][]byte{{1}, {2}, {3}}},
		{"code 3 VBR padded", []byte{0xfb, 0xc2, 2, 1, 5, 6, 7, 0, 0}, 4, [][]byte{{5}, {6, 7}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			toc, frames, off, err := PacketParse(tc.packet)
			if err != nil {
				t.Fatal(err)
			}
			if toc != tc.packet[0] || off != tc.offset || len(frames) != len(tc.frames) {
				t.Fatalf("toc %#x offset %d frames %d", toc, off, len(frames))
			}
			for i := range frames {
				if !bytes.Equal(frames[i], tc.frames[i]) {
					t.Fatalf("frame %d = %v, want %v", i, frames[i], tc.frames[i])
				}
			}
		})
	}

	for _, p := range [][]byte{
		nil,
		{0xf9, 1, 2, 3},    // code 1 with an odd payload
		{0xfa, 5, 1},       // code 2 size past the end
		{0xfb},             // code 3 without a count byte
		{0xfb, 0x00},       // zero frames
		{0x1b, 0x03},       // 3 x 60 ms > 120 ms
		{0xfb, 0x42, 0xff}, // truncated padding
		append([]byte{0xf8}, make([]byte, 1276)...), // implicit frame > 1275 bytes
	} {
		if _, _, _, err := PacketParse(p); !errors.Is(err, ErrInvalidPacket) {
			t.Errorf("PacketParse(%x) error = %v, want ErrInvalidPacket", p, err)
		}
	}
}
