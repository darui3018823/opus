package oggopus

import (
	"bytes"
	"fmt"
	"io"
	"reflect"
	"testing"
)

const (
	maxStreamFuzzInput       = 128
	maxStreamFuzzLinks       = 3
	maxStreamFuzzPackets     = 6
	maxStreamFuzzPayload     = 32
	maxStreamFuzzReads       = 20
	streamFuzzLargeTagBytes  = 96 << 10
	maxStreamFuzzNormalBytes = 8 << 10
)

type streamFuzzCursor struct {
	data []byte
	pos  int
}

func (c *streamFuzzCursor) byte() byte {
	if len(c.data) == 0 {
		return 0
	}
	b := c.data[c.pos%len(c.data)]
	c.pos++
	return b
}

func (c *streamFuzzCursor) uint16() uint16 {
	return uint16(c.byte()) | uint16(c.byte())<<8
}

type streamFuzzPacket struct {
	data     []byte
	duration int
	flush    bool
}

type streamFuzzLink struct {
	serial   uint32
	head     Head
	tags     Tags
	packets  []streamFuzzPacket
	trim     int
	emptyEOS bool
}

type streamFuzzSpec struct {
	links            []streamFuzzLink
	mutation         byte
	mutationPage     byte
	mutationArgument byte
	seekLink         int
	seekSample       uint16
	largeTag         bool
}

type streamFuzzPacketResult struct {
	Data            []byte
	GranulePosition int64
	Duration48k     int
	DiscardStart    int
	DiscardEnd      int
	LinkIndex       int
	Serial          uint32
	PageSequence    uint32
	BOS             bool
	EOS             bool
	FirstOnPage     bool
	LastOnPage      bool
	ReaderSerial    uint32
	ReaderLink      int
	Channels        uint8
	PreSkip         uint16
	Vendor          string
}

type streamFuzzCallResult struct {
	Packet *streamFuzzPacketResult
	Error  string
}

type streamFuzzReplay struct {
	NewError   string
	SeekCalled bool
	SeekError  string
	Calls      []streamFuzzCallResult
}

// FuzzOggOpusReaderWriter exercises complete Writer-to-Reader streams rather
// than isolated pages. The byte input is a bounded schema for valid chained
// streams followed by at most one structured corruption.
func FuzzOggOpusReaderWriter(f *testing.F) {
	f.Add([]byte{0, 0, 0, 0, 0, 0, 0, 1, 0, 2, 7, 3, 4, 5})
	f.Add([]byte{2, 0, 3, 2, 0xff, 0x03, 0, 5, 1, 4, 9, 2, 8, 6, 3, 7, 11})
	f.Add([]byte{1, 3, 4, 0, 0, 0, 0, 2, 1, 3, 5, 8, 13, 21})
	// Explicitly retain the bounded large-comment continuation lane.
	f.Add([]byte{0, 0, 0, 0, 0, 0, 0xff, 1, 1, 2, 3, 4, 5, 6})

	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) == 0 || len(data) > maxStreamFuzzInput {
			return
		}
		spec := makeStreamFuzzSpec(data)
		first := writeStreamFuzzSpec(t, spec)
		second := writeStreamFuzzSpec(t, spec)
		if !bytes.Equal(first, second) {
			t.Fatal("Writer output is not deterministic")
		}
		if !spec.largeTag && len(first) > maxStreamFuzzNormalBytes {
			t.Fatalf("normal generated stream has %d bytes, bound is %d", len(first), maxStreamFuzzNormalBytes)
		}

		encoded := mutateStreamFuzzData(t, first, spec)
		normalA := replayStreamFuzz(encoded, -1, 0)
		normalB := replayStreamFuzz(encoded, -1, 0)
		if !reflect.DeepEqual(normalA, normalB) {
			t.Fatalf("non-deterministic Reader result:\nA=%+v\nB=%+v", normalA, normalB)
		}
		seekA := replayStreamFuzz(encoded, spec.seekLink, spec.seekSample)
		seekB := replayStreamFuzz(encoded, spec.seekLink, spec.seekSample)
		if !reflect.DeepEqual(seekA, seekB) {
			t.Fatalf("non-deterministic seek result:\nA=%+v\nB=%+v", seekA, seekB)
		}

		if spec.mutation == 0 {
			checkValidStreamFuzzReplay(t, spec, normalA)
			if !seekA.SeekCalled || seekA.SeekError != "" {
				t.Fatalf("valid stream seek failed: called=%v err=%q", seekA.SeekCalled, seekA.SeekError)
			}
		}
	})
}

func makeStreamFuzzSpec(data []byte) streamFuzzSpec {
	c := streamFuzzCursor{data: data}
	linkCount := 1 + int(c.byte())%maxStreamFuzzLinks
	spec := streamFuzzSpec{
		links:            make([]streamFuzzLink, 0, linkCount),
		mutation:         c.byte() % 9,
		mutationPage:     c.byte(),
		mutationArgument: c.byte(),
		seekLink:         int(c.byte()) % linkCount,
		seekSample:       c.uint16(),
		largeTag:         c.byte() == 0xff,
	}
	for linkIndex := 0; linkIndex < linkCount; linkIndex++ {
		channels := uint8(1 + c.byte()%2)
		packetCount := 1 + int(c.byte())%maxStreamFuzzPackets
		link := streamFuzzLink{
			serial:   0x4f505553 + uint32(linkIndex)*0x01010101 + uint32(c.byte()),
			emptyEOS: c.byte()&1 != 0,
			packets:  make([]streamFuzzPacket, 0, packetCount),
		}
		var total int
		for packetIndex := 0; packetIndex < packetCount; packetIndex++ {
			durations := [...]struct {
				toc       byte
				samples48 int
			}{{0x80, 120}, {0x88, 240}, {0x90, 480}, {0x98, 960}, {0xf8, 960}}
			choice := durations[int(c.byte())%len(durations)]
			payloadSize := 1 + int(c.byte())%maxStreamFuzzPayload
			packet := make([]byte, payloadSize+1)
			packet[0] = choice.toc
			if channels == 2 {
				packet[0] |= 0x04
			}
			for i := 1; i < len(packet); i++ {
				packet[i] = c.byte() ^ byte(linkIndex*37+packetIndex*11+i)
			}
			link.packets = append(link.packets, streamFuzzPacket{
				data:     packet,
				duration: choice.samples48,
				flush:    c.byte()&1 != 0,
			})
			total += choice.samples48
		}
		preSkipLimit := min(total, 960)
		preSkip := int(c.uint16()) % (preSkipLimit + 1)
		link.head = Head{
			Version:         1,
			Channels:        channels,
			PreSkip:         uint16(preSkip),
			InputSampleRate: 48000,
			OutputGain:      int16(c.uint16()),
			MappingFamily:   0,
		}
		link.tags = Tags{
			Vendor: fmt.Sprintf("opus-go-fuzz-%d-%02x", linkIndex, c.byte()),
			Comments: []string{
				fmt.Sprintf("LINK=%d", linkIndex),
				fmt.Sprintf("VALUE=%02x%02x%02x", c.byte(), c.byte(), c.byte()),
			},
		}
		if spec.largeTag && linkIndex == int(spec.mutationPage)%linkCount {
			link.tags.Comments = append(link.tags.Comments,
				"CONTINUATION="+string(bytes.Repeat([]byte{'x'}, streamFuzzLargeTagBytes)))
		}
		if !link.emptyEOS {
			lastDuration := link.packets[len(link.packets)-1].duration
			maxTrim := min(lastDuration, total-preSkip)
			link.trim = int(c.uint16()) % (maxTrim + 1)
		}
		spec.links = append(spec.links, link)
	}
	return spec
}

func writeStreamFuzzSpec(t *testing.T, spec streamFuzzSpec) []byte {
	t.Helper()
	var stream bytes.Buffer
	for _, link := range spec.links {
		writer, err := NewWriter(&stream, link.serial, link.head, link.tags)
		if err != nil {
			t.Fatalf("NewWriter: %v", err)
		}
		granule := int64(0)
		for packetIndex, packet := range link.packets {
			granule += int64(packet.duration)
			last := packetIndex == len(link.packets)-1
			options := PacketWriteOptions{
				GranulePosition: granule,
				Flush:           packet.flush,
			}
			if last {
				options.Flush = link.emptyEOS
				options.EOS = !link.emptyEOS
				if options.EOS {
					options.GranulePosition -= int64(link.trim)
				}
			}
			if err := writer.WritePacket(packet.data, options); err != nil {
				t.Fatalf("WritePacket link serial=%d packet=%d: %v", link.serial, packetIndex, err)
			}
		}
		if link.emptyEOS {
			if err := writer.Close(); err != nil {
				t.Fatalf("Close link serial=%d: %v", link.serial, err)
			}
		}
	}
	return append([]byte(nil), stream.Bytes()...)
}

func mutateStreamFuzzData(t *testing.T, encoded []byte, spec streamFuzzSpec) []byte {
	t.Helper()
	if spec.mutation == 0 {
		return encoded
	}
	data := append([]byte(nil), encoded...)
	switch spec.mutation {
	case 1:
		index := int(spec.mutationArgument) * len(data) / 256
		data[index] ^= 1 << (spec.mutationPage & 7)
		return data
	case 2:
		cut := int(spec.mutationArgument) * len(data) / 256
		return data[:cut]
	}

	pages := parseStreamFuzzPages(t, data)
	selected := int(spec.mutationPage) % len(pages)
	page := &pages[selected]
	switch spec.mutation {
	case 3:
		delta := int64(int8(spec.mutationArgument))
		if delta == 0 {
			delta = 1
		}
		page.GranulePosition += delta
	case 4:
		page.Serial ^= uint32(spec.mutationArgument) + 1
	case 5:
		page.Sequence += uint32(spec.mutationArgument) + 1
	case 6:
		page.HeaderType ^= HeaderContinued
	case 7:
		page.HeaderType ^= HeaderEOS
	case 8:
		if len(page.Data) > 0 {
			page.Data[int(spec.mutationArgument)%len(page.Data)] ^= 1 << (spec.mutationPage & 7)
		} else {
			page.HeaderType ^= HeaderBOS
		}
	}
	var out bytes.Buffer
	for _, p := range pages {
		if err := WritePage(&out, p); err != nil {
			t.Fatalf("rewrite mutated page: %v", err)
		}
	}
	return out.Bytes()
}

func parseStreamFuzzPages(t *testing.T, data []byte) []Page {
	t.Helper()
	pages := make([]Page, 0, 8)
	for len(data) > 0 {
		page, consumed, err := ParsePage(data)
		if err != nil {
			t.Fatalf("Writer produced an invalid page: %v", err)
		}
		pages = append(pages, page)
		data = data[consumed:]
	}
	if len(pages) == 0 {
		t.Fatal("Writer produced no pages")
	}
	return pages
}

func replayStreamFuzz(data []byte, seekLink int, seekSelector uint16) streamFuzzReplay {
	reader, err := NewReader(bytes.NewReader(data))
	if err != nil {
		return streamFuzzReplay{NewError: err.Error()}
	}
	result := streamFuzzReplay{}
	if seekLink >= 0 {
		for reader.Link() < seekLink {
			packet, err := reader.NextPacket()
			if err != nil {
				result.Calls = append(result.Calls, streamFuzzCallResult{Error: err.Error()})
				return result
			}
			result.Calls = append(result.Calls, streamFuzzCallResult{Packet: snapshotStreamFuzzPacket(reader, packet)})
		}
		playable, err := streamFuzzPlayable(data, reader.audioOffset, reader.Serial(), reader.Head.PreSkip)
		if err != nil {
			result.SeekCalled = true
			result.SeekError = err.Error()
			return result
		}
		sample := int64(seekSelector) % (playable + 1)
		result.SeekCalled = true
		if err := reader.SeekPCM(sample); err != nil {
			result.SeekError = err.Error()
			return result
		}
	}
	for range maxStreamFuzzReads {
		packet, err := reader.NextPacket()
		if err != nil {
			result.Calls = append(result.Calls, streamFuzzCallResult{Error: err.Error()})
			// EOF and terminal stream errors are expected to be repeatable.
			_, repeated := reader.NextPacket()
			if repeated != nil {
				result.Calls = append(result.Calls, streamFuzzCallResult{Error: repeated.Error()})
			}
			break
		}
		result.Calls = append(result.Calls, streamFuzzCallResult{Packet: snapshotStreamFuzzPacket(reader, packet)})
	}
	return result
}

func streamFuzzPlayable(data []byte, start int64, serial uint32, preSkip uint16) (int64, error) {
	reader := bytes.NewReader(data)
	end := int64(len(data))
	granule, _, err := findLogicalStreamEnd(reader, start, end, serial)
	if err != nil {
		return 0, err
	}
	playable := granule - int64(preSkip)
	if playable < 0 {
		return 0, fmt.Errorf("negative playable duration %d", playable)
	}
	return playable, nil
}

func snapshotStreamFuzzPacket(reader *Reader, packet Packet) *streamFuzzPacketResult {
	return &streamFuzzPacketResult{
		Data:            append([]byte(nil), packet.Data...),
		GranulePosition: packet.GranulePosition,
		Duration48k:     packet.Duration48k,
		DiscardStart:    packet.DiscardStart,
		DiscardEnd:      packet.DiscardEnd,
		LinkIndex:       packet.LinkIndex,
		Serial:          packet.Serial,
		PageSequence:    packet.PageSequence,
		BOS:             packet.BOS,
		EOS:             packet.EOS,
		FirstOnPage:     packet.FirstPacketOnPage,
		LastOnPage:      packet.LastPacketOnPage,
		ReaderSerial:    reader.Serial(),
		ReaderLink:      reader.Link(),
		Channels:        reader.Head.Channels,
		PreSkip:         reader.Head.PreSkip,
		Vendor:          reader.Tags.Vendor,
	}
}

func checkValidStreamFuzzReplay(t *testing.T, spec streamFuzzSpec, replay streamFuzzReplay) {
	t.Helper()
	if replay.NewError != "" {
		t.Fatalf("valid stream rejected by NewReader: %s", replay.NewError)
	}
	var packets []*streamFuzzPacketResult
	for _, call := range replay.Calls {
		if call.Packet != nil {
			packets = append(packets, call.Packet)
			continue
		}
		if call.Error != io.EOF.Error() {
			t.Fatalf("valid stream read failed: %s", call.Error)
		}
	}
	wantCount := 0
	for _, link := range spec.links {
		wantCount += len(link.packets)
	}
	if len(packets) != wantCount {
		t.Fatalf("read %d packets, want %d", len(packets), wantCount)
	}
	offset := 0
	for linkIndex, link := range spec.links {
		var discardedStart, discardedEnd int
		for packetIndex, want := range link.packets {
			got := packets[offset]
			offset++
			if !bytes.Equal(got.Data, want.data) || got.Duration48k != want.duration {
				t.Fatalf("link %d packet %d mismatch: duration=%d/%d data=%x/%x", linkIndex, packetIndex, got.Duration48k, want.duration, got.Data, want.data)
			}
			if got.LinkIndex != linkIndex || got.ReaderLink != linkIndex || got.Serial != link.serial || got.ReaderSerial != link.serial {
				t.Fatalf("link %d packet %d identity mismatch: %+v", linkIndex, packetIndex, got)
			}
			if got.Channels != link.head.Channels || got.PreSkip != link.head.PreSkip || got.Vendor != link.tags.Vendor {
				t.Fatalf("link %d packet %d metadata mismatch: %+v", linkIndex, packetIndex, got)
			}
			if got.DiscardStart < 0 || got.DiscardEnd < 0 || got.DiscardStart+got.DiscardEnd > got.Duration48k {
				t.Fatalf("link %d packet %d invalid discard metadata: %+v", linkIndex, packetIndex, got)
			}
			discardedStart += got.DiscardStart
			discardedEnd += got.DiscardEnd
		}
		if discardedStart != int(link.head.PreSkip) {
			t.Fatalf("link %d start discard=%d, want %d", linkIndex, discardedStart, link.head.PreSkip)
		}
		if discardedEnd != link.trim {
			t.Fatalf("link %d end discard=%d, want %d", linkIndex, discardedEnd, link.trim)
		}
	}
	if len(replay.Calls) < 2 || replay.Calls[len(replay.Calls)-1].Error != io.EOF.Error() || replay.Calls[len(replay.Calls)-2].Error != io.EOF.Error() {
		t.Fatalf("valid stream did not produce repeatable EOF: %+v", replay.Calls)
	}
}
