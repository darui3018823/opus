package oggopus

import (
	"bytes"
	"io"
	"testing"
)

// FuzzOggParsers exercises page CRC/lacing parsing, OpusHead, OpusTags, packet
// continuation state, and complete Ogg Opus stream parsing.
func FuzzOggParsers(f *testing.F) {
	page, err := (Page{
		Version:         StreamVersion,
		HeaderType:      HeaderBOS,
		GranulePosition: 0,
		Serial:          1,
		Segments:        []byte{3},
		Data:            []byte{0xf8, 0xff, 0xfe},
	}).MarshalBinary()
	if err != nil {
		f.Fatal(err)
	}
	f.Add(page)

	head, err := (Head{
		Version:         1,
		Channels:        2,
		PreSkip:         312,
		InputSampleRate: 48000,
	}).MarshalBinary()
	if err != nil {
		f.Fatal(err)
	}
	f.Add(head)

	tags, err := (Tags{
		Vendor:   "opus-go",
		Comments: []string{"TITLE=fuzz seed"},
	}).MarshalBinary()
	if err != nil {
		f.Fatal(err)
	}
	f.Add(tags)

	var stream bytes.Buffer
	writer, err := NewWriter(&stream, 7, Head{
		Version:         1,
		Channels:        1,
		PreSkip:         312,
		InputSampleRate: 48000,
	}, Tags{Vendor: "opus-go"})
	if err != nil {
		f.Fatal(err)
	}
	if err := writer.WritePacket([]byte{0xf8, 0xff, 0xfe}, PacketWriteOptions{
		GranulePosition: 960,
		EOS:             true,
	}); err != nil {
		f.Fatal(err)
	}
	f.Add(stream.Bytes())
	f.Add([]byte{})

	f.Fuzz(func(t *testing.T, data []byte) {
		if page, consumed, err := ParsePage(data); err == nil {
			encoded, err := page.MarshalBinary()
			if err != nil {
				t.Fatalf("marshal parsed page: %v", err)
			}
			if !bytes.Equal(encoded, data[:consumed]) {
				t.Fatalf("page round trip changed encoded bytes")
			}
		}

		if head, err := ParseHead(data); err == nil {
			encoded, err := head.MarshalBinary()
			if err != nil {
				t.Fatalf("marshal parsed OpusHead: %v", err)
			}
			if _, err := ParseHead(encoded); err != nil {
				t.Fatalf("reparse OpusHead: %v", err)
			}
		}

		if tags, err := ParseTags(data); err == nil {
			encoded, err := tags.MarshalBinary()
			if err != nil {
				t.Fatalf("marshal parsed OpusTags: %v", err)
			}
			if _, err := ParseTags(encoded); err != nil {
				t.Fatalf("reparse OpusTags: %v", err)
			}
		}

		packets := NewPacketReader(bytes.NewReader(data))
		for range 8 {
			if _, err := packets.Next(); err != nil {
				break
			}
		}

		reader, err := NewReader(bytes.NewReader(data))
		if err != nil {
			return
		}
		for range 8 {
			if _, err := reader.NextPacket(); err != nil {
				if err != io.EOF {
					return
				}
				break
			}
		}
	})
}
