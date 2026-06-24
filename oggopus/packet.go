package oggopus

import (
	"fmt"
	"io"
)

// Packet is a reconstructed Ogg packet. GranulePosition is meaningful only
// for the last packet completed on a page; it is -1 for earlier packets.
type Packet struct {
	Data              []byte
	GranulePosition   int64
	Serial            uint32
	PageSequence      uint32
	BOS               bool
	EOS               bool
	FirstPacketOnPage bool
	LastPacketOnPage  bool
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
}

func NewPacketReader(r io.Reader) *PacketReader {
	return &PacketReader{r: r}
}

func (r *PacketReader) Serial() (uint32, bool) { return r.serial, r.haveSerial }
func (r *PacketReader) EOS() bool              { return r.eos }

// Next returns the next complete packet.
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

	if page.Continued() && len(r.partial) == 0 {
		return ErrUnexpectedContinue
	}
	if !page.Continued() && len(r.partial) != 0 {
		r.partial = nil
		return ErrMissingContinue
	}

	completions := 0
	for _, lace := range page.Segments {
		if lace < 255 {
			completions++
		}
	}
	completed := make([]Packet, 0, completions)
	offset := 0
	for i, lace := range page.Segments {
		if i == 0 && len(r.partial) == 0 && page.BOS() {
			r.partialBOS = true
		}
		size := int(lace)
		r.partial = append(r.partial, page.Data[offset:offset+size]...)
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
		if len(r.partial) != 0 {
			r.partial = nil
			r.terminalErr = ErrTruncatedPacket
		}
	}
	return nil
}

// PacketWriteOptions controls page metadata for a packet.
type PacketWriteOptions struct {
	GranulePosition int64
	Flush           bool
	EOS             bool
}

// PacketWriter writes packets into a single logical Ogg bitstream. It packs
// packets into pages until Flush is requested or the 255-segment page limit is
// reached. The first page is marked BOS automatically.
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

func NewPacketWriter(w io.Writer, serial uint32) *PacketWriter {
	return &PacketWriter{
		w:           w,
		serial:      serial,
		pageGranule: -1,
		lastGranule: -1,
	}
}

func (w *PacketWriter) Serial() uint32   { return w.serial }
func (w *PacketWriter) Sequence() uint32 { return w.sequence }

// WritePacket adds one packet. GranulePosition is the total 48 kHz sample
// count through this packet for Ogg Opus streams.
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

// Flush finishes the current page without ending the logical stream.
func (w *PacketWriter) Flush() error {
	if w.closed {
		return ErrWriterClosed
	}
	return w.flush(false)
}

// Close writes an EOS page. If the final packet was already flushed, Close
// emits an empty EOS page carrying the final granule position.
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
