package oggopus

import (
	"fmt"
	"io"
)

// Reader parses one complete Ogg Opus logical bitstream.
type Reader struct {
	packets *PacketReader
	Head    Head
	Tags    Tags
}

// NewReader reads and validates the mandatory OpusHead and OpusTags packets.
func NewReader(r io.Reader) (*Reader, error) {
	packets := NewPacketReader(r)
	headPacket, err := packets.Next()
	if err != nil {
		return nil, fmt.Errorf("%w: read OpusHead: %v", ErrInvalidOpusStream, err)
	}
	if !headPacket.BOS || headPacket.PageSequence != 0 || headPacket.GranulePosition != 0 ||
		!headPacket.FirstPacketOnPage || !headPacket.LastPacketOnPage {
		return nil, fmt.Errorf("%w: OpusHead must be the only packet on BOS page 0 with granule 0", ErrInvalidOpusStream)
	}
	head, err := ParseHead(headPacket.Data)
	if err != nil {
		return nil, err
	}
	tagsPacket, err := packets.Next()
	if err != nil {
		return nil, fmt.Errorf("%w: read OpusTags: %v", ErrInvalidOpusStream, err)
	}
	if tagsPacket.BOS || tagsPacket.GranulePosition != 0 || !tagsPacket.LastPacketOnPage {
		return nil, fmt.Errorf("%w: OpusTags must finish its page with granule 0", ErrInvalidOpusStream)
	}
	tags, err := ParseTags(tagsPacket.Data)
	if err != nil {
		return nil, err
	}
	return &Reader{packets: packets, Head: head, Tags: tags}, nil
}

func (r *Reader) Serial() uint32 {
	serial, _ := r.packets.Serial()
	return serial
}

func (r *Reader) EOS() bool { return r.packets.EOS() }

// NextPacket returns the next Opus audio packet and its Ogg metadata.
func (r *Reader) NextPacket() (Packet, error) {
	packet, err := r.packets.Next()
	if err != nil {
		return Packet{}, err
	}
	if len(packet.Data) == 0 {
		return Packet{}, fmt.Errorf("%w: zero-length audio packet", ErrInvalidOpusStream)
	}
	return packet, nil
}

// Writer writes one complete Ogg Opus logical bitstream.
type Writer struct {
	packets *PacketWriter
}

// NewWriter validates and writes the mandatory headers. The ID header is
// placed alone on page 0, and the comment header finishes page 1.
func NewWriter(w io.Writer, serial uint32, head Head, tags Tags) (*Writer, error) {
	headPacket, err := head.MarshalBinary()
	if err != nil {
		return nil, err
	}
	tagsPacket, err := tags.MarshalBinary()
	if err != nil {
		return nil, err
	}
	packets := NewPacketWriter(w, serial)
	if err := packets.WritePacket(headPacket, PacketWriteOptions{GranulePosition: 0, Flush: true}); err != nil {
		return nil, err
	}
	if err := packets.WritePacket(tagsPacket, PacketWriteOptions{GranulePosition: 0, Flush: true}); err != nil {
		return nil, err
	}
	return &Writer{packets: packets}, nil
}

func (w *Writer) Serial() uint32 { return w.packets.Serial() }

// WritePacket writes one Opus audio packet. Set EOS on the final packet to
// end the stream on the same page; otherwise Close emits an empty EOS page.
func (w *Writer) WritePacket(data []byte, options PacketWriteOptions) error {
	if len(data) == 0 {
		return fmt.Errorf("%w: zero-length audio packet", ErrInvalidOpusStream)
	}
	return w.packets.WritePacket(data, options)
}

func (w *Writer) Flush() error { return w.packets.Flush() }
func (w *Writer) Close() error { return w.packets.Close() }
