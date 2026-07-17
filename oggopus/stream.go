package oggopus

import (
	"errors"
	"fmt"
	"io"

	opus "github.com/darui3018823/opus"
)

// Reader parses an Ogg Opus physical stream, including chained logical
// streams. It borrows its source, is stateful, and is not safe for concurrent
// use. It does not demultiplex interleaved logical streams.
type Reader struct {
	source              io.Reader
	packets             *PacketReader
	seeker              io.ReadSeeker
	audioOffset         int64
	audioSequence       uint32
	serial              uint32
	pending             []Packet
	preSkipRemaining    int
	previousPageGranule int64
	haveAudioGranule    bool
	seekDiscardActive   bool
	seekTargetGranule   int64
	atEnd               bool
	linkEndOffset       int64
	linkFinalGranule    int64
	haveLinkEnd         bool
	linkIndex           int
	physicalEOF         bool
	terminalErr         error
	seenSerials         map[uint32]struct{}
	// Head is the current logical stream's identification header. It is updated
	// when NextPacket advances to a chained stream.
	Head Head
	// Tags is the current logical stream's comment header. It is updated when
	// NextPacket advances to a chained stream.
	Tags Tags
}

// NewReader synchronously reads and validates the first logical stream's
// mandatory OpusHead and OpusTags packets. SeekPCM is available only when r
// also implements io.ReadSeeker.
func NewReader(r io.Reader) (*Reader, error) {
	seeker, _ := r.(io.ReadSeeker)
	packets := NewPacketReader(r)
	headPacket, head, tags, err := readLinkHeaders(packets)
	if err != nil {
		return nil, err
	}
	var audioOffset int64
	if seeker != nil {
		audioOffset, err = seeker.Seek(0, io.SeekCurrent)
		if err != nil {
			return nil, fmt.Errorf("%w: determine audio offset: %v", ErrNotSeekable, err)
		}
	}
	// RFC 7845 permits a pasted live stream to begin its audio data with a
	// continued packet whose missing prefix must be discarded.
	packets.allowOrphan = true
	return &Reader{
		source:           r,
		packets:          packets,
		seeker:           seeker,
		audioOffset:      audioOffset,
		audioSequence:    packets.nextSeq,
		serial:           headPacket.Serial,
		preSkipRemaining: int(head.PreSkip),
		Head:             head,
		Tags:             tags,
		seenSerials:      map[uint32]struct{}{headPacket.Serial: {}},
	}, nil
}

func readLinkHeaders(packets *PacketReader) (Packet, Head, Tags, error) {
	headPacket, err := packets.Next()
	if err != nil {
		return Packet{}, Head{}, Tags{}, fmt.Errorf("%w: read OpusHead: %w", ErrInvalidOpusStream, err)
	}
	if !headPacket.BOS || headPacket.PageSequence != 0 || headPacket.GranulePosition != 0 ||
		!headPacket.FirstPacketOnPage || !headPacket.LastPacketOnPage {
		return Packet{}, Head{}, Tags{}, fmt.Errorf("%w: OpusHead must be the only packet on BOS page 0 with granule 0", ErrInvalidOpusStream)
	}
	head, err := ParseHead(headPacket.Data)
	if err != nil {
		return Packet{}, Head{}, Tags{}, err
	}
	tagsPacket, err := packets.Next()
	if err != nil {
		return Packet{}, Head{}, Tags{}, fmt.Errorf("%w: read OpusTags: %w", ErrInvalidOpusStream, err)
	}
	if tagsPacket.BOS || tagsPacket.GranulePosition != 0 || !tagsPacket.LastPacketOnPage {
		return Packet{}, Head{}, Tags{}, fmt.Errorf("%w: OpusTags must finish its page with granule 0", ErrInvalidOpusStream)
	}
	tags, err := ParseTags(tagsPacket.Data)
	if err != nil {
		return Packet{}, Head{}, Tags{}, err
	}
	return headPacket, head, tags, nil
}

// Serial returns the current logical stream's serial number. NextPacket may
// change it when advancing to a chained stream.
func (r *Reader) Serial() uint32 {
	return r.serial
}

// Link returns the zero-based index of the current chained logical stream.
func (r *Reader) Link() int { return r.linkIndex }

// EOS reports whether an EOS page for the current link has been read. Pending
// packets or another chained logical stream may still remain.
func (r *Reader) EOS() bool { return r.atEnd || r.packets.EOS() }

// NextPacket returns the next duration-validated Opus audio packet and its Ogg
// timing metadata. DiscardStart and DiscardEnd tell a decoder how many samples
// per channel to omit. At a logical-stream boundary it advances automatically
// and updates Head, Tags, Serial, and Link; io.EOF means the physical stream is
// exhausted. A terminal format error is returned again on later calls.
func (r *Reader) NextPacket() (Packet, error) {
	if r.terminalErr != nil {
		return Packet{}, r.terminalErr
	}
	if r.physicalEOF {
		return Packet{}, io.EOF
	}
	if r.atEnd {
		r.atEnd = false
		if err := r.advanceLink(); err != nil {
			return Packet{}, err
		}
	}
	for len(r.pending) == 0 {
		if err := r.readAudioPage(); err != nil {
			if errors.Is(err, io.EOF) && r.packets.EOS() {
				if err := r.validateLogicalEOS(); err != nil {
					r.terminalErr = err
					return Packet{}, err
				}
				if err := r.advanceLink(); err != nil {
					return Packet{}, err
				}
				continue
			}
			if errors.Is(err, io.EOF) {
				err = fmt.Errorf("%w: physical EOF before EOS", ErrInvalidOpusStream)
			}
			r.terminalErr = err
			return Packet{}, err
		}
	}
	packet := r.pending[0]
	r.pending = r.pending[1:]
	return packet, nil
}

func (r *Reader) validateLogicalEOS() error {
	if !r.packets.haveEOS {
		return fmt.Errorf("%w: missing EOS page metadata", ErrInvalidOpusStream)
	}
	if r.haveLinkEnd {
		return nil
	}
	if !r.haveAudioGranule || r.packets.eosGranule != r.previousPageGranule {
		return fmt.Errorf("%w: empty EOS granule %d, want %d", ErrInvalidOpusStream, r.packets.eosGranule, r.previousPageGranule)
	}
	if r.seeker != nil {
		offset, err := r.seeker.Seek(0, io.SeekCurrent)
		if err != nil {
			return err
		}
		r.linkEndOffset = offset
		r.linkFinalGranule = r.packets.eosGranule
		r.haveLinkEnd = true
	}
	return nil
}

func (r *Reader) advanceLink() error {
	packets := NewPacketReader(r.source)
	headPacket, head, tags, err := readLinkHeaders(packets)
	if err != nil {
		if errors.Is(err, io.EOF) {
			r.physicalEOF = true
			return io.EOF
		}
		r.terminalErr = err
		return err
	}
	if _, duplicate := r.seenSerials[headPacket.Serial]; duplicate {
		err := fmt.Errorf("%w: chained stream reuses serial %d", ErrSerial, headPacket.Serial)
		r.terminalErr = err
		return err
	}
	r.seenSerials[headPacket.Serial] = struct{}{}
	packets.allowOrphan = true
	r.packets = packets
	r.pending = nil
	r.serial = headPacket.Serial
	r.linkIndex++
	r.Head = head
	r.Tags = tags
	r.preSkipRemaining = int(head.PreSkip)
	r.previousPageGranule = 0
	r.haveAudioGranule = false
	r.seekDiscardActive = false
	r.atEnd = false
	r.haveLinkEnd = false
	if r.seeker != nil {
		r.audioOffset, err = r.seeker.Seek(0, io.SeekCurrent)
		if err != nil {
			r.terminalErr = err
			return err
		}
		r.audioSequence = packets.nextSeq
	}
	return nil
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
		packet.LinkIndex = r.linkIndex
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
	pageStart := int64(0)
	if r.haveAudioGranule {
		pageStart = r.previousPageGranule
	} else if granule >= pageDuration {
		pageStart = granule - pageDuration
	}
	if !r.haveAudioGranule && pageStart > 0 && r.preSkipRemaining > 0 {
		alreadySkipped := min(int64(r.preSkipRemaining), pageStart)
		r.preSkipRemaining -= int(alreadySkipped)
	}

	for i := range pagePackets {
		discard := min(r.preSkipRemaining, pagePackets[i].Duration48k)
		pagePackets[i].DiscardStart = discard
		r.preSkipRemaining -= discard
	}
	if r.seekDiscardActive {
		packetStart := pageStart
		for i := range pagePackets {
			duration := int64(pagePackets[i].Duration48k)
			if r.seekTargetGranule > packetStart {
				discard := min(duration, r.seekTargetGranule-packetStart)
				if int(discard) > pagePackets[i].DiscardStart {
					pagePackets[i].DiscardStart = int(discard)
				}
			}
			packetStart += duration
		}
		if pageStart+pageDuration >= r.seekTargetGranule {
			r.seekDiscardActive = false
		}
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
	if last.EOS && r.seeker != nil {
		if offset, err := r.seeker.Seek(0, io.SeekCurrent); err == nil {
			r.linkEndOffset = offset
			r.linkFinalGranule = granule
			r.haveLinkEnd = true
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

// Writer writes one complete Ogg Opus logical bitstream. It is stateful, is not
// safe for concurrent use, and borrows but does not close its destination. To
// create a chained stream, finish one Writer and create another on the same
// destination with a different serial number.
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

// Serial returns the logical stream's serial number.
func (w *Writer) Serial() uint32 { return w.packets.Serial() }

// WritePacket writes one non-empty packet. Set EOS on the final packet to end
// the stream on the same page; otherwise Close emits an empty EOS page. This
// method does not validate Opus framing, duration, or granule timing.
func (w *Writer) WritePacket(data []byte, options PacketWriteOptions) error {
	if len(data) == 0 {
		return fmt.Errorf("%w: zero-length audio packet", ErrInvalidOpusStream)
	}
	return w.packets.WritePacket(data, options)
}

// Flush finishes the buffered audio page without ending the logical stream.
func (w *Writer) Flush() error { return w.packets.Flush() }

// Close writes an EOS page when needed. It is idempotent after success and
// does not close the underlying writer.
func (w *Writer) Close() error { return w.packets.Close() }
