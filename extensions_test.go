package opus

import (
	"bytes"
	"errors"
	"reflect"
	"testing"
)

func TestPacketExtensionsRoundTrip(t *testing.T) {
	base := []byte{byte(16 << 3), 0x11, 0x22, 0x33}
	exts := []PacketExtension{
		{ID: 3, Frame: 0, Data: []byte{0x7a}},
		{ID: ExtensionIDDRED, Frame: 0, Data: []byte("opaque DRED")},
		{ID: ExtensionIDQEXT, Frame: 0, Data: []byte("opaque QEXT")},
	}
	packet, err := PacketExtensionsGenerate(base, exts, 64)
	if err != nil {
		t.Fatal(err)
	}
	if packet[0]&3 != 3 || packet[1]&0x40 == 0 {
		t.Fatalf("extensions were not integrated into code-3 padding: % x", packet[:2])
	}
	if got, err := PacketExtensionsCount(packet); err != nil || got != len(exts) {
		t.Fatalf("PacketExtensionsCount = %d, %v; want %d, nil", got, err, len(exts))
	}
	got, err := PacketExtensionsParse(packet)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, exts) {
		t.Fatalf("extensions:\n got %#v\nwant %#v", got, exts)
	}

	stripped, err := PacketExtensionsGenerate(packet, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(stripped, base) {
		t.Fatalf("stripped packet = % x, want % x", stripped, base)
	}
}

func TestPacketExtensionsFrameSpecificAndShared(t *testing.T) {
	base := []byte{byte(16<<3 | 1), 0x10, 0x20, 0x30, 0x40}
	input := []PacketExtension{
		{ID: 3, Frame: ExtensionFrameAll, Data: []byte{0xaa}},
		{ID: 33, Frame: 1, Data: []byte("frame one")},
	}
	packet, err := PacketExtensionsGenerate(base, input, 0)
	if err != nil {
		t.Fatal(err)
	}
	got, err := PacketExtensionsParse(packet)
	if err != nil {
		t.Fatal(err)
	}
	want := []PacketExtension{
		{ID: 3, Frame: 0, Data: []byte{0xaa}},
		{ID: 3, Frame: 1, Data: []byte{0xaa}},
		{ID: 33, Frame: 1, Data: []byte("frame one")},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("shared expansion:\n got %#v\nwant %#v", got, want)
	}
}

func TestPacketExtensionsPreserveAudioFramesAndOrdinaryPadding(t *testing.T) {
	base := []byte{byte(16<<3 | 2), 2, 0x11, 0x22, 0x33, 0x44, 0x55}
	beforeCode := int(base[0] & 3)
	before, err := splitOpusFrames(base[1:], beforeCode)
	if err != nil {
		t.Fatal(err)
	}
	packet, err := PacketExtensionsGenerate(base, []PacketExtension{
		{ID: 31, Frame: 0},
		{ID: 32, Frame: 1, Data: bytes.Repeat([]byte{0xcc}, 300)},
	}, 320)
	if err != nil {
		t.Fatal(err)
	}
	after, _, _, err := packetExtensionLayout(packet)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("audio frames changed:\n got % x\nwant % x", after, before)
	}
}

func TestPacketExtensionsMalformedPadding(t *testing.T) {
	base := []byte{byte(16 << 3), 0x55}
	packet, err := PacketExtensionsGenerate(base, []PacketExtension{
		{ID: 32, Frame: 0, Data: []byte("payload")},
	}, 16)
	if err != nil {
		t.Fatal(err)
	}
	packet[len(packet)-8] = 32<<1 | 1
	packet[len(packet)-7] = 255
	if _, err := PacketExtensionsParse(packet); !errors.Is(err, ErrInvalidPacket) {
		t.Fatalf("malformed extension error = %v, want ErrInvalidPacket", err)
	}
}

func TestPacketExtensionsArgumentErrors(t *testing.T) {
	base := []byte{byte(16 << 3), 0x55}
	if _, err := PacketExtensionsGenerate(base, []PacketExtension{{ID: 2, Frame: 0}}, 0); !errors.Is(err, ErrBadArg) {
		t.Fatalf("invalid ID error = %v, want ErrBadArg", err)
	}
	if _, err := PacketExtensionsGenerate(base, []PacketExtension{{ID: 32, Frame: 1}}, 0); !errors.Is(err, ErrBadArg) {
		t.Fatalf("invalid frame error = %v, want ErrBadArg", err)
	}
	if _, err := PacketExtensionsGenerate(base, []PacketExtension{{ID: 32, Frame: 0, Data: []byte("payload")}}, 1); !errors.Is(err, ErrBufferTooSmall) {
		t.Fatalf("small padding error = %v, want ErrBufferTooSmall", err)
	}
}
