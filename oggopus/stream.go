package oggopus

import (
	"fmt"
	"io"

	opus "github.com/darui3018823/opus"
)

// Reader parses one complete Ogg Opus logical bitstream.
type Reader struct {
	packets             *PacketReader
	pending             []Packet
	preSkipRemaining    int
	previousPageGranule int64
	haveAudioGranule    bool
	Head                Head
	Tags                Tags
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
	return &Reader{
		packets:          packets,
		preSkipRemaining: int(head.PreSkip),
		Head:             head,
		Tags:             tags,
	}, nil
}

func (r *Reader) Serial() uint32 {
	serial, _ := r.packets.Serial()
	return serial
}

func (r *Reader) EOS() bool { return r.packets.EOS() }

// NextPacket returns the next Opus audio packet and its Ogg metadata.
func (r *Reader) NextPacket() (Packet, error) {
	if len(r.pending) == 0 {
		if err := r.readAudioPage(); err != nil {
			return Packet{}, err
		}
	}
	packet := r.pending[0]
	r.pending = r.pending[1:]
	return packet, nil
}

func (r *Reader) readAudioPage() error {
	var pagePackets []Packet
	for {
		packet, err := r.packets.Next()
		if err != nil {
			return err
		}
		if len(packet.Data) == 0 {
			return fmt.Errorf("%w: zero-length audio packet", ErrInvalidOpusStream)
		}
		duration, err := r.packetDuration(packet.Data)
		if err != nil {
			return fmt.Errorf("%w: packet duration: %v", ErrInvalidOpusStream, err)
		}
		packet.Duration48k = duration
		pagePackets = append(pagePackets, packet)
		if packet.LastPacketOnPage {
			break
		}
	}

	last := &pagePackets[len(pagePackets)-1]
	granule := last.GranulePosition
	if granule < 0 {
		return fmt.Errorf("%w: completed audio page has no granule position", ErrInvalidOpusStream)
	}
	var pageDuration int64
	for i := range pagePackets {
		pageDuration += int64(pagePackets[i].Duration48k)
	}

	naturalEnd := pageDuration
	if r.haveAudioGranule {
		naturalEnd += r.previousPageGranule
		if last.EOS {
			if granule < r.previousPageGranule || granule > naturalEnd {
				return fmt.Errorf("%w: EOS granule %d outside [%d,%d]", ErrInvalidOpusStream, granule, r.previousPageGranule, naturalEnd)
			}
		} else if granule != naturalEnd {
			return fmt.Errorf("%w: audio granule %d, want %d", ErrInvalidOpusStream, granule, naturalEnd)
		}
	} else if last.EOS {
		if granule < int64(r.Head.PreSkip) {
			return fmt.Errorf("%w: EOS granule %d is smaller than pre-skip %d", ErrInvalidOpusStream, granule, r.Head.PreSkip)
		}
		if granule > naturalEnd {
			naturalEnd = granule
		}
	} else if granule < naturalEnd {
		return fmt.Errorf("%w: initial audio granule %d is smaller than page duration %d", ErrInvalidOpusStream, granule, naturalEnd)
	}

	for i := range pagePackets {
		discard := min(r.preSkipRemaining, pagePackets[i].Duration48k)
		pagePackets[i].DiscardStart = discard
		r.preSkipRemaining -= discard
	}
	if last.EOS {
		trim := naturalEnd - granule
		for i := len(pagePackets) - 1; i >= 0 && trim > 0; i-- {
			available := pagePackets[i].Duration48k - pagePackets[i].DiscardStart
			discard := min(int64(available), trim)
			pagePackets[i].DiscardEnd = int(discard)
			trim -= discard
		}
		if trim != 0 {
			return fmt.Errorf("%w: end trim exceeds decoded audio", ErrInvalidOpusStream)
		}
	}
	r.previousPageGranule = granule
	r.haveAudioGranule = true
	r.pending = pagePackets
	return nil
}

func (r *Reader) packetDuration(data []byte) (int, error) {
	if r.Head.MappingFamily == 0 {
		return opus.PacketGetNumSamples(data, opus.SampleRate48kHz)
	}
	return opus.MultistreamPacketGetNumSamples(data, int(r.Head.StreamCount), opus.SampleRate48kHz)
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
