package oggopus

import (
	"fmt"
	"io"
)

// Packet is a reconstructed Ogg packet. GranulePosition is meaningful only
// for the last packet completed on a page; it is -1 for earlier packets.
type Packet struct {
	// Data is a caller-owned copy of the reconstructed packet payload.
	Data []byte
	// GranulePosition is meaningful only for the last packet completed on a
	// page; it is -1 for earlier packets. Reader interprets it in 48 kHz samples.
	GranulePosition int64
	// Duration48k is the decoded packet duration per channel at 48 kHz.
	// PacketReader leaves it zero; Reader populates it for audio packets.
	Duration48k int
	// DiscardStart is the decoded samples per channel to remove at the beginning
	// for Opus pre-skip or seeking. Reader populates it.
	DiscardStart int
	// DiscardEnd is the decoded samples per channel to remove at the end for
	// granule-position trimming. Reader populates it.
	DiscardEnd int
	// LinkIndex is the zero-based chained logical-stream index. PacketReader
	// leaves it zero; Reader populates it for audio packets.
	LinkIndex int
	// Serial identifies the packet's logical bitstream.
	Serial uint32
	// PageSequence is the sequence number of the page completing the packet.
	PageSequence uint32
	// BOS reports whether the packet begins on a BOS page.
	BOS bool
	// EOS reports whether the packet is the last packet completed on an EOS
	// page. An empty EOS page produces no Packet value.
	EOS bool
	// FirstPacketOnPage reports whether this is the first packet completed on
	// its page; a continued packet may have begun on an earlier page.
	FirstPacketOnPage bool
	// LastPacketOnPage reports whether this is the final packet completed on its page.
	LastPacketOnPage bool
}

// PacketReader reconstructs packets from a single logical Ogg bitstream.
type PacketReader struct {
	r           io.Reader
	serial      uint32
	haveSerial  bool
	nextSeq     uint32
	partial     []byte
	partialBOS  bool
	queue       []Packet
	eos         bool
	terminalErr error
	allowOrphan bool
	eosGranule  int64
	haveEOS     bool
}

// NewPacketReader returns a stateful packet reader that borrows r. It accepts
// one logical bitstream and enforces its serial number and page sequence.
func NewPacketReader(r io.Reader) *PacketReader {
	return &PacketReader{r: r}
}

func newPacketReaderAt(r io.Reader, serial, sequence uint32, allowOrphan bool) *PacketReader {
	return &PacketReader{
		r:           r,
		serial:      serial,
		haveSerial:  true,
		nextSeq:     sequence,
		allowOrphan: allowOrphan,
	}
}

// Serial returns the logical-stream serial and whether a page has established it.
func (r *PacketReader) Serial() (uint32, bool) { return r.serial, r.haveSerial }

// EOS reports whether an EOS page has been read. Queued packets from that page
// may still remain to be returned by Next.
func (r *PacketReader) EOS() bool { return r.eos }

// Next returns the next complete packet with caller-owned Data. It validates
// page CRCs, serial numbers, sequences, and continuation. After queued EOS
// packets are returned, it returns io.EOF.
func (r *PacketReader) Next() (Packet, error) {
	for len(r.queue) == 0 {
		if r.terminalErr != nil {
			err := r.terminalErr
			r.terminalErr = nil
			return Packet{}, err
		}
		if r.eos {
			return Packet{}, io.EOF
		}
		if err := r.readPage(); err != nil {
			if err == io.EOF && len(r.partial) != 0 {
				r.partial = nil
				return Packet{}, ErrTruncatedPacket
			}
			return Packet{}, err
		}
	}
	packet := r.queue[0]
	r.queue = r.queue[1:]
	return packet, nil
}

func (r *PacketReader) readPage() error {
	page, err := ReadPage(r.r)
	if err != nil {
		return err
	}
	if r.eos {
		return ErrAfterEOS
	}
	if !r.haveSerial {
		r.serial = page.Serial
		r.haveSerial = true
		r.nextSeq = page.Sequence
	} else if page.Serial != r.serial {
		return fmt.Errorf("%w: got %d, want %d", ErrSerial, page.Serial, r.serial)
	}
	if page.Sequence != r.nextSeq {
		return fmt.Errorf("%w: got %d, want %d", ErrSequence, page.Sequence, r.nextSeq)
	}
	r.nextSeq++

	segments := page.Segments
	pageData := page.Data
	if page.Continued() && len(r.partial) == 0 {
		if !r.allowOrphan {
			return ErrUnexpectedContinue
		}
		discardBytes := 0
		discardSegments := 0
		for discardSegments < len(segments) {
			lace := segments[discardSegments]
			discardBytes += int(lace)
			discardSegments++
			if lace < 255 {
				r.allowOrphan = false
				break
			}
		}
		segments = segments[discardSegments:]
		pageData = pageData[discardBytes:]
	} else {
		r.allowOrphan = false
	}
	if !page.Continued() && len(r.partial) != 0 {
		r.partial = nil
		return ErrMissingContinue
	}

	completions := 0
	for _, lace := range segments {
		if lace < 255 {
			completions++
		}
	}
	completed := make([]Packet, 0, completions)
	offset := 0
	for i, lace := range segments {
		if i == 0 && len(r.partial) == 0 && page.BOS() {
			r.partialBOS = true
		}
		size := int(lace)
		r.partial = append(r.partial, pageData[offset:offset+size]...)
		offset += size
		if lace < 255 {
			completed = append(completed, Packet{
				Data:         append([]byte(nil), r.partial...),
				Serial:       page.Serial,
				PageSequence: page.Sequence,
				BOS:          r.partialBOS,
			})
			r.partial = r.partial[:0]
			r.partialBOS = false
		}
	}
	for i := range completed {
		completed[i].FirstPacketOnPage = i == 0
		completed[i].LastPacketOnPage = i == len(completed)-1
		if completed[i].LastPacketOnPage {
			completed[i].GranulePosition = page.GranulePosition
			completed[i].EOS = page.EOS()
		} else {
			completed[i].GranulePosition = -1
		}
	}
	r.queue = append(r.queue, completed...)
	if page.EOS() {
		r.eos = true
		r.eosGranule = page.GranulePosition
		r.haveEOS = true
		if len(r.partial) != 0 {
			r.partial = nil
			r.terminalErr = ErrTruncatedPacket
		}
	}
	return nil
}

// PacketWriteOptions controls page metadata for a packet.
type PacketWriteOptions struct {
	// GranulePosition becomes the page granule when this packet is the last
	// packet completed on that page. Only non-negativity is validated.
	GranulePosition int64
	// Flush finishes the current page after adding the packet.
	Flush bool
	// EOS finishes an EOS page and closes the PacketWriter. It takes precedence
	// over Flush.
	EOS bool
}

// PacketWriter writes packets into a single logical Ogg bitstream. It packs
// packets into pages until Flush is requested or the 255-segment page limit is
// reached. The first page is marked BOS automatically. It borrows its output
// writer, copies packet bytes during WritePacket, and is not safe for
// concurrent use.
type PacketWriter struct {
	w                   io.Writer
	serial              uint32
	sequence            uint32
	segments            []byte
	data                []byte
	pageContinued       bool
	nextPageContinued   bool
	pageGranule         int64
	lastGranule         int64
	haveCompletedPacket bool
	wrotePage           bool
	closed              bool
}

// NewPacketWriter returns a packet writer with page sequence zero for serial.
func NewPacketWriter(w io.Writer, serial uint32) *PacketWriter {
	return &PacketWriter{
		w:           w,
		serial:      serial,
		pageGranule: -1,
		lastGranule: -1,
	}
}

// Serial returns the logical-stream serial number.
func (w *PacketWriter) Serial() uint32 { return w.serial }

// Sequence returns the sequence number of the next page to be written.
func (w *PacketWriter) Sequence() uint32 { return w.sequence }

// WritePacket adds one packet. For Ogg Opus, callers normally supply the total
// decoded 48 kHz samples per channel through the packet. This low-level writer
// accepts zero-length packets and checks neither Opus framing nor timing; it
// only requires a non-negative granule position.
func (w *PacketWriter) WritePacket(data []byte, options PacketWriteOptions) error {
	if w.closed {
		return ErrWriterClosed
	}
	if options.GranulePosition < 0 {
		return ErrInvalidGranule
	}
	remaining := data
	continued := false
	for len(remaining) >= 255 {
		if err := w.addSegment(remaining[:255], 255, continued); err != nil {
			return err
		}
		remaining = remaining[255:]
		continued = true
	}
	if err := w.addSegment(remaining, byte(len(remaining)), continued); err != nil {
		return err
	}
	w.pageGranule = options.GranulePosition
	w.lastGranule = options.GranulePosition
	w.haveCompletedPacket = true
	if options.EOS {
		if err := w.flush(true); err != nil {
			return err
		}
		w.closed = true
		return nil
	}
	if options.Flush {
		return w.Flush()
	}
	return nil
}

func (w *PacketWriter) addSegment(data []byte, lace byte, continued bool) error {
	if len(w.segments) == MaxSegments {
		w.nextPageContinued = continued
		if err := w.flush(false); err != nil {
			return err
		}
	}
	if len(w.segments) == 0 {
		w.pageContinued = w.nextPageContinued
		w.nextPageContinued = false
	}
	w.segments = append(w.segments, lace)
	w.data = append(w.data, data...)
	if lace < 255 {
		w.nextPageContinued = false
	}
	return nil
}

// Flush finishes the current page without ending the logical stream. It is a
// no-op when no segments are buffered and does not flush or close the
// underlying writer.
func (w *PacketWriter) Flush() error {
	if w.closed {
		return ErrWriterClosed
	}
	return w.flush(false)
}

// Close writes an EOS page. If the final packet was already flushed, Close
// emits an empty EOS page carrying the final granule position. A successful
// Close is idempotent and does not close the underlying writer.
func (w *PacketWriter) Close() error {
	if w.closed {
		return nil
	}
	if w.nextPageContinued || (len(w.segments) > 0 && w.segments[len(w.segments)-1] == 255) {
		return ErrTruncatedPacket
	}
	if !w.haveCompletedPacket {
		return ErrInvalidGranule
	}
	if err := w.flush(true); err != nil {
		return err
	}
	w.closed = true
	return nil
}

func (w *PacketWriter) flush(eos bool) error {
	if len(w.segments) == 0 && !eos {
		return nil
	}
	header := HeaderType(0)
	if w.pageContinued {
		header |= HeaderContinued
	}
	if !w.wrotePage {
		header |= HeaderBOS
	}
	if eos {
		header |= HeaderEOS
	}
	granule := w.pageGranule
	if len(w.segments) == 0 {
		granule = w.lastGranule
	} else if w.segments[len(w.segments)-1] == 255 {
		granule = -1
	}
	page := Page{
		Version:         StreamVersion,
		HeaderType:      header,
		GranulePosition: granule,
		Serial:          w.serial,
		Sequence:        w.sequence,
		Segments:        append([]byte(nil), w.segments...),
		Data:            append([]byte(nil), w.data...),
	}
	if err := WritePage(w.w, page); err != nil {
		return err
	}
	w.sequence++
	w.wrotePage = true
	w.segments = w.segments[:0]
	w.data = w.data[:0]
	w.pageGranule = -1
	w.haveCompletedPacket = w.lastGranule >= 0
	return nil
}
