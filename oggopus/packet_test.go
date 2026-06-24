package oggopus

import (
	"bytes"
	"errors"
	"io"
	"testing"
)

func TestPacketWriterReaderLacingContinuedAndGranule(t *testing.T) {
	var stream bytes.Buffer
	writer := NewPacketWriter(&stream, 0xdecafbad)
	large := make([]byte, MaxPageData+4975)
	for i := range large {
		large[i] = byte(i * 31)
	}
	if err := writer.WritePacket(large, PacketWriteOptions{GranulePosition: 960}); err != nil {
		t.Fatal(err)
	}
	if err := writer.WritePacket([]byte("tail"), PacketWriteOptions{
		GranulePosition: 1920,
		EOS:             true,
	}); err != nil {
		t.Fatal(err)
	}

	raw := stream.Bytes()
	first, n, err := ParsePage(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !first.BOS() || first.Continued() || first.EOS() {
		t.Fatalf("first flags = 0x%02x", first.HeaderType)
	}
	if first.GranulePosition != -1 || len(first.Segments) != MaxSegments {
		t.Fatalf("first page granule/segments = %d/%d", first.GranulePosition, len(first.Segments))
	}
	second, _, err := ParsePage(raw[n:])
	if err != nil {
		t.Fatal(err)
	}
	if !second.Continued() || !second.EOS() {
		t.Fatalf("second flags = 0x%02x", second.HeaderType)
	}
	if second.GranulePosition != 1920 {
		t.Fatalf("second granule = %d", second.GranulePosition)
	}

	reader := NewPacketReader(bytes.NewReader(raw))
	gotLarge, err := reader.Next()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(gotLarge.Data, large) {
		t.Fatal("large packet mismatch")
	}
	if !gotLarge.BOS {
		t.Fatal("multi-page first packet lost BOS metadata")
	}
	if gotLarge.GranulePosition != -1 || gotLarge.EOS {
		t.Fatalf("large packet metadata = granule %d, eos %v", gotLarge.GranulePosition, gotLarge.EOS)
	}
	gotTail, err := reader.Next()
	if err != nil {
		t.Fatal(err)
	}
	if string(gotTail.Data) != "tail" || gotTail.GranulePosition != 1920 || !gotTail.EOS {
		t.Fatalf("tail = %+v", gotTail)
	}
	if _, err := reader.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("end error = %v", err)
	}
	if !reader.EOS() {
		t.Fatal("EOS not retained")
	}
}

func TestPacketWriterExact255MultipleUsesZeroTerminator(t *testing.T) {
	var stream bytes.Buffer
	writer := NewPacketWriter(&stream, 1)
	packet := bytes.Repeat([]byte{9}, 510)
	if err := writer.WritePacket(packet, PacketWriteOptions{GranulePosition: 480, EOS: true}); err != nil {
		t.Fatal(err)
	}
	page, _, err := ParsePage(stream.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(page.Segments, []byte{255, 255, 0}) {
		t.Fatalf("lacing = %v", page.Segments)
	}
	reader := NewPacketReader(bytes.NewReader(stream.Bytes()))
	got, err := reader.Next()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got.Data, packet) {
		t.Fatal("packet mismatch")
	}
}

func TestPacketWriterFullPageStartsNextPacketFresh(t *testing.T) {
	var stream bytes.Buffer
	writer := NewPacketWriter(&stream, 5)
	for i := range MaxSegments {
		if err := writer.WritePacket([]byte{byte(i)}, PacketWriteOptions{GranulePosition: int64(i + 1)}); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.WritePacket([]byte("next"), PacketWriteOptions{GranulePosition: 256, EOS: true}); err != nil {
		t.Fatal(err)
	}
	first, n, err := ParsePage(stream.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := ParsePage(stream.Bytes()[n:])
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Segments) != MaxSegments || second.Continued() {
		t.Fatalf("page boundary: first segments %d, second flags 0x%02x", len(first.Segments), second.HeaderType)
	}
}

func TestPacketReaderSequenceContinuationAndTruncation(t *testing.T) {
	first := Page{
		Version:         0,
		GranulePosition: -1,
		Serial:          9,
		Sequence:        0,
		Segments:        []byte{255},
		Data:            bytes.Repeat([]byte{1}, 255),
	}
	second := Page{
		Version:         0,
		HeaderType:      HeaderContinued,
		GranulePosition: 960,
		Serial:          9,
		Sequence:        1,
		Segments:        []byte{1},
		Data:            []byte{2},
	}
	var valid bytes.Buffer
	if err := WritePage(&valid, first); err != nil {
		t.Fatal(err)
	}
	if err := WritePage(&valid, second); err != nil {
		t.Fatal(err)
	}

	truncated := NewPacketReader(bytes.NewReader(valid.Bytes()[:len(valid.Bytes())-1]))
	if _, err := truncated.Next(); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("truncation error = %v", err)
	}

	second.Sequence = 2
	var skipped bytes.Buffer
	_ = WritePage(&skipped, first)
	_ = WritePage(&skipped, second)
	if _, err := NewPacketReader(&skipped).Next(); !errors.Is(err, ErrSequence) {
		t.Fatalf("sequence error = %v", err)
	}

	second.Sequence = 1
	second.HeaderType = 0
	var missing bytes.Buffer
	_ = WritePage(&missing, first)
	_ = WritePage(&missing, second)
	if _, err := NewPacketReader(&missing).Next(); !errors.Is(err, ErrMissingContinue) {
		t.Fatalf("continuation error = %v", err)
	}
}

func TestPacketReaderRejectsUnfinishedPacketAtEOFAndEOS(t *testing.T) {
	page := Page{
		Version:         0,
		HeaderType:      HeaderBOS,
		GranulePosition: -1,
		Serial:          4,
		Segments:        []byte{255},
		Data:            bytes.Repeat([]byte{3}, 255),
	}
	var stream bytes.Buffer
	_ = WritePage(&stream, page)
	if _, err := NewPacketReader(bytes.NewReader(stream.Bytes())).Next(); !errors.Is(err, ErrTruncatedPacket) {
		t.Fatalf("EOF error = %v", err)
	}

	page.HeaderType |= HeaderEOS
	stream.Reset()
	_ = WritePage(&stream, page)
	if _, err := NewPacketReader(bytes.NewReader(stream.Bytes())).Next(); !errors.Is(err, ErrTruncatedPacket) {
		t.Fatalf("EOS error = %v", err)
	}
}
