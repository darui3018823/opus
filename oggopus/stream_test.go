package oggopus

import (
	"bytes"
	"errors"
	"io"
	"testing"
)

func TestOggOpusWriterReaderGranuleEOSRoundTrip(t *testing.T) {
	head := Head{
		Version:         1,
		Channels:        2,
		PreSkip:         312,
		InputSampleRate: 48000,
		OutputGain:      64,
		MappingFamily:   0,
	}
	tags := Tags{Vendor: "deterministic", Comments: []string{"ARTIST=test"}}
	audio := [][]byte{{0xf8, 0xff, 0xfe}, {0xf8, 1, 2, 3}}

	write := func() []byte {
		var stream bytes.Buffer
		writer, err := NewWriter(&stream, 1234, head, tags)
		if err != nil {
			t.Fatal(err)
		}
		if err := writer.WritePacket(audio[0], PacketWriteOptions{GranulePosition: 960}); err != nil {
			t.Fatal(err)
		}
		if err := writer.WritePacket(audio[1], PacketWriteOptions{
			GranulePosition: 1608,
			EOS:             true,
		}); err != nil {
			t.Fatal(err)
		}
		return append([]byte(nil), stream.Bytes()...)
	}
	firstEncoding := write()
	if secondEncoding := write(); !bytes.Equal(firstEncoding, secondEncoding) {
		t.Fatal("stream encoding was not deterministic")
	}

	reader, err := NewReader(bytes.NewReader(firstEncoding))
	if err != nil {
		t.Fatal(err)
	}
	if reader.Serial() != 1234 || reader.Head.PreSkip != 312 || reader.Head.OutputGain != 64 {
		t.Fatalf("header metadata = %+v serial %d", reader.Head, reader.Serial())
	}
	if reader.Tags.Vendor != tags.Vendor {
		t.Fatalf("tags = %+v", reader.Tags)
	}
	for i, want := range audio {
		packet, err := reader.NextPacket()
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(packet.Data, want) {
			t.Fatalf("packet %d mismatch", i)
		}
		if i == 0 && packet.GranulePosition != -1 {
			t.Fatalf("first packet granule = %d", packet.GranulePosition)
		}
		if i == 1 && (packet.GranulePosition != 1608 || !packet.EOS) {
			t.Fatalf("final packet metadata = %+v", packet)
		}
	}
	if _, err := reader.NextPacket(); !errors.Is(err, io.EOF) {
		t.Fatalf("end error = %v", err)
	}
}

func TestOggOpusCloseWritesEmptyEOSPage(t *testing.T) {
	var stream bytes.Buffer
	writer, err := NewWriter(&stream, 88,
		Head{Version: 1, Channels: 1},
		Tags{Vendor: "test"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.WritePacket([]byte{0xf8, 0xff, 0xfe}, PacketWriteOptions{
		GranulePosition: 648,
		Flush:           true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	data := stream.Bytes()
	var last Page
	for len(data) > 0 {
		page, n, err := ParsePage(data)
		if err != nil {
			t.Fatal(err)
		}
		last = page
		data = data[n:]
	}
	if !last.EOS() || len(last.Segments) != 0 || last.GranulePosition != 648 {
		t.Fatalf("EOS page = %+v", last)
	}
}

func TestOggOpusTagsMaySpanMultiplePages(t *testing.T) {
	tags := Tags{
		Vendor:   "test",
		Comments: []string{"COVERART=" + string(bytes.Repeat([]byte{'x'}, MaxPageData+1000))},
	}
	var stream bytes.Buffer
	writer, err := NewWriter(&stream, 99, Head{Version: 1, Channels: 1}, tags)
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.WritePacket([]byte{0xf8, 0xff, 0xfe}, PacketWriteOptions{
		GranulePosition: 648,
		EOS:             true,
	}); err != nil {
		t.Fatal(err)
	}
	reader, err := NewReader(bytes.NewReader(stream.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if len(reader.Tags.Comments) != 1 || reader.Tags.Comments[0] != tags.Comments[0] {
		t.Fatal("multi-page OpusTags mismatch")
	}
}

func TestOggOpusReaderValidatesHeaderPageRules(t *testing.T) {
	head, _ := (Head{Version: 1, Channels: 1}).MarshalBinary()
	tags, _ := (Tags{Vendor: "test"}).MarshalBinary()

	var bad bytes.Buffer
	writer := NewPacketWriter(&bad, 1)
	if err := writer.WritePacket(head, PacketWriteOptions{GranulePosition: 1, Flush: true}); err != nil {
		t.Fatal(err)
	}
	if err := writer.WritePacket(tags, PacketWriteOptions{GranulePosition: 0, EOS: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := NewReader(bytes.NewReader(bad.Bytes())); !errors.Is(err, ErrInvalidOpusStream) {
		t.Fatalf("header page error = %v", err)
	}
}

func TestOggOpusReaderRejectsMissingFinalEOS(t *testing.T) {
	var stream bytes.Buffer
	writer, err := NewWriter(&stream, 100,
		Head{Version: 1, Channels: 1},
		Tags{Vendor: "test"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.WritePacket([]byte{0xf8, 0xff, 0xfe}, PacketWriteOptions{
		GranulePosition: 960,
		Flush:           true,
	}); err != nil {
		t.Fatal(err)
	}
	reader, err := NewReader(bytes.NewReader(stream.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reader.NextPacket(); err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		if _, err := reader.NextPacket(); !errors.Is(err, ErrInvalidOpusStream) {
			t.Fatalf("attempt %d missing EOS error = %v", attempt, err)
		}
	}
}
