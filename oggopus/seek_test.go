package oggopus

import (
	"bytes"
	"errors"
	"io"
	"testing"
)

const seekTestPacketDuration = 960

func makeSeekTestStream(t *testing.T, packets int, preSkip uint16, endTrim int) []byte {
	t.Helper()
	var stream bytes.Buffer
	w, err := NewWriter(&stream, 77, Head{Version: 1, Channels: 1, PreSkip: preSkip}, Tags{Vendor: "seek"})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < packets; i++ {
		granule := int64((i + 1) * seekTestPacketDuration)
		options := PacketWriteOptions{GranulePosition: granule, Flush: true}
		if i == packets-1 {
			options.GranulePosition -= int64(endTrim)
			options.Flush = false
			options.EOS = true
		}
		if err := w.WritePacket([]byte{0xf8, byte(i + 1), 0xfe}, options); err != nil {
			t.Fatal(err)
		}
	}
	return stream.Bytes()
}

func TestReaderSeekStartAndInterior(t *testing.T) {
	const preSkip = 312
	data := makeSeekTestStream(t, 12, preSkip, 120)
	r, err := NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if err := r.SeekPCM(0); err != nil {
		t.Fatal(err)
	}
	first, err := r.NextPacket()
	if err != nil {
		t.Fatal(err)
	}
	if first.Data[1] != 1 || first.DiscardStart != preSkip {
		t.Fatalf("seek start packet = id %d discard %d", first.Data[1], first.DiscardStart)
	}

	const target = int64(7000)
	if err := r.SeekPCM(target); err != nil {
		t.Fatal(err)
	}
	var playable Packet
	var prerollSamples int
	for {
		packet, err := r.NextPacket()
		if err != nil {
			t.Fatal(err)
		}
		if packet.DiscardStart < packet.Duration48k {
			playable = packet
			break
		}
		prerollSamples += packet.Duration48k
	}
	if playable.Data[1] != 8 {
		t.Fatalf("first target packet id = %d, want 8", playable.Data[1])
	}
	if playable.DiscardStart != 592 {
		t.Fatalf("target discard = %d, want 592", playable.DiscardStart)
	}
	if prerollSamples < seekPreRoll48k {
		t.Fatalf("pre-roll = %d samples, want at least %d", prerollSamples, seekPreRoll48k)
	}
}

func TestReaderSeekPageBoundaryAndEnd(t *testing.T) {
	const (
		preSkip = 120
		packets = 8
		endTrim = 240
	)
	data := makeSeekTestStream(t, packets, preSkip, endTrim)
	r, err := NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	boundary := int64(5*seekTestPacketDuration - preSkip)
	if err := r.SeekPCM(boundary); err != nil {
		t.Fatal(err)
	}
	for {
		packet, err := r.NextPacket()
		if err != nil {
			t.Fatal(err)
		}
		if packet.DiscardStart == 0 {
			if packet.Data[1] != 6 {
				t.Fatalf("packet after boundary id = %d, want 6", packet.Data[1])
			}
			break
		}
	}

	end := int64(packets*seekTestPacketDuration - endTrim - preSkip)
	if err := r.SeekPCM(end); err != nil {
		t.Fatal(err)
	}
	if !r.EOS() {
		t.Fatal("Seek(end) did not mark EOS")
	}
	if _, err := r.NextPacket(); !errors.Is(err, io.EOF) {
		t.Fatalf("NextPacket after Seek(end) = %v, want EOF", err)
	}
	if err := r.SeekPCM(0); err != nil {
		t.Fatalf("SeekPCM(0) after SeekPCM(end): %v", err)
	}
	first, err := r.NextPacket()
	if err != nil {
		t.Fatal(err)
	}
	if first.Data[1] != 1 || first.DiscardStart != preSkip {
		t.Fatalf("restart packet = id %d discard %d", first.Data[1], first.DiscardStart)
	}
}

func TestReaderSeekRangeErrorsPreserveState(t *testing.T) {
	data := makeSeekTestStream(t, 6, 0, 0)
	r, err := NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	first, err := r.NextPacket()
	if err != nil {
		t.Fatal(err)
	}
	if first.Data[1] != 1 {
		t.Fatal("unexpected first packet")
	}
	for _, sample := range []int64{-1, 6*seekTestPacketDuration + 1} {
		if err := r.SeekPCM(sample); !errors.Is(err, ErrSeekOutOfRange) {
			t.Fatalf("SeekPCM(%d) error = %v", sample, err)
		}
	}
	next, err := r.NextPacket()
	if err != nil {
		t.Fatal(err)
	}
	if next.Data[1] != 2 {
		t.Fatalf("packet after failed seek id = %d, want 2", next.Data[1])
	}
}

func TestReaderSeekRejectsNonSeekableSource(t *testing.T) {
	data := makeSeekTestStream(t, 2, 0, 0)
	r, err := NewReader(bytes.NewBuffer(data))
	if err != nil {
		t.Fatal(err)
	}
	if err := r.SeekPCM(0); !errors.Is(err, ErrNotSeekable) {
		t.Fatalf("SeekPCM error = %v, want ErrNotSeekable", err)
	}
}

func TestPacketReaderSeekResyncDropsOrphanContinuation(t *testing.T) {
	page := Page{
		Version:         StreamVersion,
		HeaderType:      HeaderContinued | HeaderEOS,
		GranulePosition: seekTestPacketDuration,
		Serial:          99,
		Sequence:        10,
		Segments:        []byte{3, 3},
		Data:            []byte{1, 2, 3, 0xf8, 0xff, 0xfe},
	}
	var stream bytes.Buffer
	if err := WritePage(&stream, page); err != nil {
		t.Fatal(err)
	}
	r := newPacketReaderAt(bytes.NewReader(stream.Bytes()), page.Serial, page.Sequence, true)
	packet, err := r.Next()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(packet.Data, []byte{0xf8, 0xff, 0xfe}) || !packet.EOS {
		t.Fatalf("resynchronized packet = %+v", packet)
	}
}

func TestReaderAcceptsInitialContinuedAudioPage(t *testing.T) {
	const serial = 101
	head, err := (Head{Version: 1, Channels: 1}).MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	tags, err := (Tags{Vendor: "continued"}).MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	var stream bytes.Buffer
	for _, page := range []Page{
		{Version: 0, HeaderType: HeaderBOS, GranulePosition: 0, Serial: serial, Sequence: 0, Segments: []byte{byte(len(head))}, Data: head},
		{Version: 0, GranulePosition: 0, Serial: serial, Sequence: 1, Segments: []byte{byte(len(tags))}, Data: tags},
		{
			Version:         0,
			HeaderType:      HeaderContinued | HeaderEOS,
			GranulePosition: seekTestPacketDuration,
			Serial:          serial,
			Sequence:        2,
			Segments:        []byte{3, 3},
			Data:            []byte{1, 2, 3, 0xf8, 0xff, 0xfe},
		},
	} {
		if err := WritePage(&stream, page); err != nil {
			t.Fatal(err)
		}
	}
	data := stream.Bytes()
	for _, seekFirst := range []bool{false, true} {
		r, err := NewReader(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		if seekFirst {
			if err := r.SeekPCM(0); err != nil {
				t.Fatal(err)
			}
		}
		packet, err := r.NextPacket()
		if err != nil {
			t.Fatalf("seek=%v: %v", seekFirst, err)
		}
		if !bytes.Equal(packet.Data, []byte{0xf8, 0xff, 0xfe}) {
			t.Fatalf("seek=%v packet = %x", seekFirst, packet.Data)
		}
	}
}

func TestReaderSeekStartPreservesExpectedSequence(t *testing.T) {
	data := makeSeekTestStream(t, 2, 0, 0)
	reader := bytes.NewReader(data)
	var rebuilt bytes.Buffer
	pageIndex := 0
	for reader.Len() > 0 {
		page, err := ReadPage(reader)
		if err != nil {
			t.Fatal(err)
		}
		if pageIndex == 2 {
			page.Sequence += 7
		}
		if err := WritePage(&rebuilt, page); err != nil {
			t.Fatal(err)
		}
		pageIndex++
	}
	r, err := NewReader(bytes.NewReader(rebuilt.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if err := r.SeekPCM(0); err != nil {
		t.Fatal(err)
	}
	if _, err := r.NextPacket(); !errors.Is(err, ErrSequence) {
		t.Fatalf("NextPacket error = %v, want ErrSequence", err)
	}
}

func TestScanNextPageHonorsEndBoundary(t *testing.T) {
	first, err := (Page{
		Version:         StreamVersion,
		GranulePosition: 960,
		Serial:          1,
		Segments:        []byte{3},
		Data:            []byte{0xf8, 0xff, 0xfe},
	}).MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	second, err := (Page{
		Version:         StreamVersion,
		HeaderType:      HeaderBOS,
		GranulePosition: 0,
		Serial:          2,
		Segments:        []byte{3},
		Data:            []byte{1, 2, 3},
	}).MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	data := append(first, second...)
	from := int64(len(first) - 2)
	end := int64(len(first))
	if _, _, _, err := scanNextPage(bytes.NewReader(data), from, end); !errors.Is(err, io.EOF) {
		t.Fatalf("scanNextPage crossed end boundary: %v", err)
	}

	// A capture pattern inside the range is also rejected when its page body
	// extends beyond the current logical-stream boundary.
	if _, _, _, err := scanNextPage(bytes.NewReader(data), 0, int64(len(first)-1)); !errors.Is(err, io.EOF) {
		t.Fatalf("scanNextPage accepted page extending beyond end: %v", err)
	}
}
