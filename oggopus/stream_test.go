package oggopus

import (
	"bytes"
	"errors"
	"io"
	"testing"

	opus "github.com/darui3018823/opus"
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

func TestOggOpusReaderReadsHighBitrateWriterOutput(t *testing.T) {
	// 1275-byte packets fill pages so that a packet completes on a page that
	// ends inside the next one.
	packet := make([]byte, 1275)
	packet[0] = 0xfc // CELT FB 20 ms, code 0
	var stream bytes.Buffer
	writer, err := NewWriter(&stream, 9, Head{Version: 1, Channels: 2, PreSkip: 312, InputSampleRate: 48000}, Tags{Vendor: "test"})
	if err != nil {
		t.Fatal(err)
	}
	const packets = 100
	for i := range packets {
		if err := writer.WriteValidatedPacket(packet, PacketWriteOptions{GranulePosition: int64(i+1) * 960}); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	reader, err := NewReader(bytes.NewReader(stream.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for {
		_, err := reader.NextPacket()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("after %d packets: %v", n, err)
		}
		n++
	}
	if n != packets {
		t.Fatalf("read %d packets, want %d", n, packets)
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

func TestWriterWriteValidatedPacket(t *testing.T) {
	encoder, err := opus.NewEncoder(opus.SampleRate48kHz, 1, opus.ApplicationAudio)
	if err != nil {
		t.Fatal(err)
	}
	packet, err := encoder.Encode(make([]int16, opus.FrameSize20ms), opus.FrameSize20ms)
	if err != nil {
		t.Fatal(err)
	}

	var stream bytes.Buffer
	writer, err := NewWriter(&stream, 101, Head{Version: 1, Channels: 1}, Tags{Vendor: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.WriteValidatedPacket([]byte{0xff, 0}, PacketWriteOptions{GranulePosition: 960}); !errors.Is(err, ErrInvalidOpusStream) {
		t.Fatalf("invalid packet error = %v, want ErrInvalidOpusStream", err)
	}
	if err := writer.WriteValidatedPacket(packet, PacketWriteOptions{GranulePosition: 960, EOS: true}); err != nil {
		t.Fatalf("valid packet: %v", err)
	}

	reader, err := NewReader(bytes.NewReader(stream.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	got, err := reader.NextPacket()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got.Data, packet) || got.Duration48k != opus.FrameSize20ms || !got.EOS {
		t.Fatalf("packet = %+v, want a 20 ms EOS packet", got)
	}
}

func TestWriterWriteValidatedPacketMultistream(t *testing.T) {
	encoder, err := opus.NewMultistreamEncoder(opus.SampleRate48kHz, 3, 2, 1, []byte{0, 1, 2}, opus.ApplicationAudio)
	if err != nil {
		t.Fatal(err)
	}
	packet, err := encoder.Encode(make([]int16, 3*opus.FrameSize20ms), opus.FrameSize20ms)
	if err != nil {
		t.Fatal(err)
	}

	var stream bytes.Buffer
	writer, err := NewWriter(&stream, 102, Head{
		Version:        1,
		Channels:       3,
		MappingFamily:  1,
		StreamCount:    2,
		CoupledCount:   1,
		ChannelMapping: []uint8{0, 1, 2},
	}, Tags{Vendor: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.WriteValidatedPacket(packet, PacketWriteOptions{GranulePosition: 960, EOS: true}); err != nil {
		t.Fatalf("multistream packet: %v", err)
	}

	reader, err := NewReader(bytes.NewReader(stream.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	got, err := reader.NextPacket()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got.Data, packet) || got.Duration48k != opus.FrameSize20ms || !got.EOS {
		t.Fatalf("packet = %+v, want a 20 ms EOS packet", got)
	}
}

func TestOggOpusReaderRejectsMultiPageEOSBeforePreSkip(t *testing.T) {
	var stream bytes.Buffer
	writer, err := NewWriter(&stream, 3, Head{Version: 1, Channels: 1, PreSkip: 312, InputSampleRate: 48000}, Tags{Vendor: "test"})
	if err != nil {
		t.Fatal(err)
	}
	packet := []byte{0xe0, 0xff} // CELT FB 2.5 ms
	if err := writer.WritePacket(packet, PacketWriteOptions{GranulePosition: 120, Flush: true}); err != nil {
		t.Fatal(err)
	}
	// The final granule (200) ends the stream before its 312-sample pre-skip.
	if err := writer.WritePacket(packet, PacketWriteOptions{GranulePosition: 200, EOS: true}); err != nil {
		t.Fatal(err)
	}
	reader, err := NewReader(bytes.NewReader(stream.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	for {
		_, err = reader.NextPacket()
		if err != nil {
			break
		}
	}
	if !errors.Is(err, ErrInvalidOpusStream) {
		t.Fatalf("error = %v, want ErrInvalidOpusStream", err)
	}
}

func TestOggOpusCloseWithoutAudioFails(t *testing.T) {
	var stream bytes.Buffer
	writer, err := NewWriter(&stream, 4, Head{Version: 1, Channels: 1, PreSkip: 312, InputSampleRate: 48000}, Tags{Vendor: "test"})
	if err != nil {
		t.Fatal(err)
	}
	headers := stream.Len()
	if err := writer.Close(); !errors.Is(err, ErrInvalidOpusStream) {
		t.Fatalf("Close error = %v, want ErrInvalidOpusStream", err)
	}
	if stream.Len() != headers {
		t.Fatalf("Close wrote %d bytes after the headers", stream.Len()-headers)
	}
	// The writer is still usable.
	if err := writer.WritePacket([]byte{0xf8, 0xff, 0xfe}, PacketWriteOptions{GranulePosition: 960}); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := NewReader(bytes.NewReader(stream.Bytes())); err != nil {
		t.Fatal(err)
	}
}

func TestOggOpusProjectionFamily3RoundTrip(t *testing.T) {
	enc, err := opus.NewProjectionEncoder(48000, 4, 3, opus.ApplicationAudio)
	if err != nil {
		t.Fatal(err)
	}
	head := Head{
		Version:         1,
		Channels:        4,
		PreSkip:         uint16(enc.Lookahead()),
		InputSampleRate: 48000,
		OutputGain:      int16(enc.DemixingMatrixGain()),
		MappingFamily:   3,
		StreamCount:     uint8(enc.Streams()),
		CoupledCount:    uint8(enc.CoupledStreams()),
		DemixingMatrix:  enc.DemixingMatrixBytes(),
	}
	var stream bytes.Buffer
	writer, err := NewWriter(&stream, 12, head, Tags{Vendor: "test"})
	if err != nil {
		t.Fatal(err)
	}
	pcm := make([]int16, 960*4)
	for i := range pcm {
		pcm[i] = int16((i * 37) % 2000)
	}
	const packets = 5
	for i := range packets {
		packet, err := enc.Encode(pcm, 960)
		if err != nil {
			t.Fatal(err)
		}
		if err := writer.WriteValidatedPacket(packet, PacketWriteOptions{GranulePosition: int64(i+1) * 960}); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	reader, err := NewReader(bytes.NewReader(stream.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(reader.Head.DemixingMatrix, head.DemixingMatrix) {
		t.Fatal("demixing matrix changed in the round trip")
	}
	dec, err := opus.NewProjectionDecoder(48000, 4, int(reader.Head.StreamCount), int(reader.Head.CoupledCount), reader.Head.DemixingMatrix)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]int16, 5760*4)
	n := 0
	for {
		packet, err := reader.NextPacket()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if _, err := dec.Decode(packet.Data, out); err != nil {
			t.Fatal(err)
		}
		n++
	}
	if n != packets {
		t.Fatalf("decoded %d packets, want %d", n, packets)
	}
}
