package oggopus

import (
	"bytes"
	"errors"
	"io"
	"testing"
)

func makeChainLink(t *testing.T, serial uint32, channels byte, vendor string, id byte, emptyEOS bool) []byte {
	t.Helper()
	var stream bytes.Buffer
	w, err := NewWriter(&stream, serial, Head{Version: 1, Channels: channels}, Tags{Vendor: vendor})
	if err != nil {
		t.Fatal(err)
	}
	toc := byte(0xf8)
	if channels == 2 {
		toc |= 0x04
	}
	options := PacketWriteOptions{GranulePosition: 960, EOS: !emptyEOS, Flush: emptyEOS}
	if err := w.WritePacket([]byte{toc, id, 0xfe}, options); err != nil {
		t.Fatal(err)
	}
	if emptyEOS {
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
	}
	return stream.Bytes()
}

func TestReaderContinuesAcrossChainedStreams(t *testing.T) {
	links := [][]byte{
		makeChainLink(t, 10, 1, "first", 1, false),
		makeChainLink(t, 20, 2, "second", 2, true),
		makeChainLink(t, 30, 1, "third", 3, false),
	}
	data := bytes.Join(links, nil)
	r, err := NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	wantSerial := []uint32{10, 20, 30}
	wantChannels := []uint8{1, 2, 1}
	wantVendor := []string{"first", "second", "third"}
	for link := range links {
		packet, err := r.NextPacket()
		if err != nil {
			t.Fatalf("link %d: %v", link, err)
		}
		if packet.LinkIndex != link || r.Link() != link {
			t.Fatalf("link index packet/reader = %d/%d, want %d", packet.LinkIndex, r.Link(), link)
		}
		if packet.Data[1] != byte(link+1) || r.Serial() != wantSerial[link] {
			t.Fatalf("link %d packet id/serial = %d/%d", link, packet.Data[1], r.Serial())
		}
		if r.Head.Channels != wantChannels[link] || r.Tags.Vendor != wantVendor[link] {
			t.Fatalf("link %d metadata = head %+v tags %+v", link, r.Head, r.Tags)
		}
	}
	for attempt := 0; attempt < 2; attempt++ {
		if _, err := r.NextPacket(); !errors.Is(err, io.EOF) {
			t.Fatalf("EOF attempt %d = %v", attempt, err)
		}
	}
}

func TestReaderChainedMalformedHeadersAreSticky(t *testing.T) {
	first := makeChainLink(t, 40, 1, "valid", 1, false)
	var malformed bytes.Buffer
	w := NewPacketWriter(&malformed, 41)
	if err := w.WritePacket([]byte("not OpusHead"), PacketWriteOptions{GranulePosition: 0, Flush: true}); err != nil {
		t.Fatal(err)
	}
	r, err := NewReader(bytes.NewReader(append(first, malformed.Bytes()...)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.NextPacket(); err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		if _, err := r.NextPacket(); !errors.Is(err, ErrInvalidOpusHead) {
			t.Fatalf("malformed attempt %d = %v", attempt, err)
		}
	}
}

func TestReaderSeekPCMWithinChainedStreams(t *testing.T) {
	first := makeChainLink(t, 50, 1, "first", 1, false)
	second := makeChainLink(t, 51, 1, "second", 2, false)
	r, err := NewReader(bytes.NewReader(append(first, second...)))
	if err != nil {
		t.Fatal(err)
	}
	if err := r.SeekPCM(0); err != nil {
		t.Fatal(err)
	}
	packet, err := r.NextPacket()
	if err != nil || packet.Data[1] != 1 || packet.LinkIndex != 0 {
		t.Fatalf("first link seek packet = %+v, %v", packet, err)
	}
	if err := r.SeekPCM(960); err != nil {
		t.Fatal(err)
	}
	packet, err = r.NextPacket()
	if err != nil || packet.Data[1] != 2 || packet.LinkIndex != 1 {
		t.Fatalf("second link after end seek = %+v, %v", packet, err)
	}
	if err := r.SeekPCM(0); err != nil {
		t.Fatal(err)
	}
	packet, err = r.NextPacket()
	if err != nil || packet.Data[1] != 2 || packet.LinkIndex != 1 {
		t.Fatalf("second link restart packet = %+v, %v", packet, err)
	}
	if _, err := r.NextPacket(); !errors.Is(err, io.EOF) {
		t.Fatalf("second link EOF = %v", err)
	}
	if err := r.SeekPCM(0); err != nil {
		t.Fatalf("seek after physical EOF: %v", err)
	}
	if _, err := r.NextPacket(); err != nil {
		t.Fatalf("decode after physical EOF seek: %v", err)
	}
}

func TestReaderRejectsReusedChainedSerial(t *testing.T) {
	data := append(makeChainLink(t, 60, 1, "first", 1, false), makeChainLink(t, 60, 1, "second", 2, false)...)
	r, err := NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.NextPacket(); err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		if _, err := r.NextPacket(); !errors.Is(err, ErrSerial) {
			t.Fatalf("attempt %d error = %v, want ErrSerial", attempt, err)
		}
	}
}

func TestReaderRejectsTruncatedPacketOnEOSPage(t *testing.T) {
	base := makeChainLink(t, 61, 1, "base", 1, false)
	reader := bytes.NewReader(base)
	var stream bytes.Buffer
	for reader.Len() > 0 {
		page, err := ReadPage(reader)
		if err != nil {
			t.Fatal(err)
		}
		if page.EOS() {
			page.Segments = append(page.Segments, 255)
			page.Data = append(page.Data, bytes.Repeat([]byte{0}, 255)...)
		}
		if err := WritePage(&stream, page); err != nil {
			t.Fatal(err)
		}
	}
	r, err := NewReader(bytes.NewReader(stream.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.NextPacket(); err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		if _, err := r.NextPacket(); !errors.Is(err, ErrTruncatedPacket) {
			t.Fatalf("attempt %d error = %v, want ErrTruncatedPacket", attempt, err)
		}
	}
}

func TestReaderRejectsMismatchedEmptyEOSGranule(t *testing.T) {
	data := makeChainLink(t, 62, 1, "empty", 1, true)
	reader := bytes.NewReader(data)
	var stream bytes.Buffer
	for reader.Len() > 0 {
		page, err := ReadPage(reader)
		if err != nil {
			t.Fatal(err)
		}
		if page.EOS() && len(page.Segments) == 0 {
			page.GranulePosition--
		}
		if err := WritePage(&stream, page); err != nil {
			t.Fatal(err)
		}
	}
	r, err := NewReader(bytes.NewReader(stream.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.NextPacket(); err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		if _, err := r.NextPacket(); !errors.Is(err, ErrInvalidOpusStream) {
			t.Fatalf("attempt %d error = %v, want ErrInvalidOpusStream", attempt, err)
		}
	}
}
