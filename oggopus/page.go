package oggopus

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
)

const (
	// CapturePattern is the four-byte signature at the start of every Ogg page.
	CapturePattern = "OggS"
	// StreamVersion is the supported Ogg page format version.
	StreamVersion = 0
	// MaxSegments is the maximum number of lacing values on one Ogg page.
	MaxSegments = 255
	// MaxPageData is the maximum payload size represented by one lacing table.
	MaxPageData = 255 * 255
)

// HeaderType contains the Ogg page header flags.
type HeaderType byte

const (
	// HeaderContinued marks a page beginning with a continued packet.
	HeaderContinued HeaderType = 0x01
	// HeaderBOS marks the first page of a logical bitstream.
	HeaderBOS HeaderType = 0x02
	// HeaderEOS marks the final page of a logical bitstream.
	HeaderEOS HeaderType = 0x04
)

// Page is one complete Ogg page. Segments contains its lacing values and Data
// contains the concatenated segment payload.
type Page struct {
	// Version is the Ogg page format version and must equal StreamVersion.
	Version byte
	// HeaderType contains the continued, BOS, and EOS flags.
	HeaderType HeaderType
	// GranulePosition is the codec-defined position for the page, or -1 when no
	// completed packet on the page establishes one. Ogg Opus uses 48 kHz samples.
	GranulePosition int64
	// Serial identifies the logical bitstream.
	Serial uint32
	// Sequence is the zero-based page sequence number within the bitstream.
	Sequence uint32
	// Checksum is populated by ParsePage. MarshalBinary ignores this value and
	// computes the checksum from the other fields.
	Checksum uint32
	// Segments contains the lacing values whose sum must equal len(Data).
	Segments []byte
	// Data contains the concatenated segment payload.
	Data []byte
}

// Continued reports whether the page begins with a continued packet.
func (p Page) Continued() bool { return p.HeaderType&HeaderContinued != 0 }

// BOS reports whether this is the first page of a logical bitstream.
func (p Page) BOS() bool { return p.HeaderType&HeaderBOS != 0 }

// EOS reports whether this is the final page of a logical bitstream.
func (p Page) EOS() bool { return p.HeaderType&HeaderEOS != 0 }

// Validate checks the page fields and lacing table, but does not compare a
// checksum because a Page does not retain its original encoded bytes.
func (p Page) Validate() error {
	if p.Version != StreamVersion {
		return fmt.Errorf("%w: Ogg version %d", ErrUnsupportedVersion, p.Version)
	}
	if p.HeaderType&^HeaderType(0x07) != 0 {
		return fmt.Errorf("%w: reserved flags 0x%02x", ErrInvalidHeaderType, byte(p.HeaderType))
	}
	if len(p.Segments) > MaxSegments {
		return fmt.Errorf("%w: %d lacing values", ErrInvalidPage, len(p.Segments))
	}
	n := 0
	for _, size := range p.Segments {
		n += int(size)
	}
	if n != len(p.Data) {
		return fmt.Errorf("%w: lacing totals %d bytes, data has %d", ErrInvalidPage, n, len(p.Data))
	}
	return nil
}

// MarshalBinary encodes a page and calculates its Ogg CRC checksum.
func (p Page) MarshalBinary() ([]byte, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	out := make([]byte, 27+len(p.Segments)+len(p.Data))
	copy(out, CapturePattern)
	out[4] = p.Version
	out[5] = byte(p.HeaderType)
	binary.LittleEndian.PutUint64(out[6:14], uint64(p.GranulePosition))
	binary.LittleEndian.PutUint32(out[14:18], p.Serial)
	binary.LittleEndian.PutUint32(out[18:22], p.Sequence)
	out[26] = byte(len(p.Segments))
	copy(out[27:], p.Segments)
	copy(out[27+len(p.Segments):], p.Data)
	binary.LittleEndian.PutUint32(out[22:26], checksum(out))
	return out, nil
}

// WritePage encodes and writes one page, recalculating its checksum. It does
// not close w.
func WritePage(w io.Writer, p Page) error {
	data, err := p.MarshalBinary()
	if err != nil {
		return err
	}
	return writeAll(w, data)
}

// ParsePage parses exactly the first page in data and returns the number of
// bytes consumed. It verifies the capture pattern, version, flags, length,
// lacing table, and CRC. Trailing data is left unconsumed, and returned lacing
// and payload slices are caller-owned copies. Truncation returns
// io.ErrUnexpectedEOF.
func ParsePage(data []byte) (Page, int, error) {
	if len(data) < 27 {
		return Page{}, 0, io.ErrUnexpectedEOF
	}
	if !bytes.Equal(data[:4], []byte(CapturePattern)) {
		return Page{}, 0, ErrInvalidCapture
	}
	segmentCount := int(data[26])
	headerLen := 27 + segmentCount
	if len(data) < headerLen {
		return Page{}, 0, io.ErrUnexpectedEOF
	}
	bodyLen := 0
	for _, size := range data[27:headerLen] {
		bodyLen += int(size)
	}
	pageLen := headerLen + bodyLen
	if len(data) < pageLen {
		return Page{}, 0, io.ErrUnexpectedEOF
	}
	encoded := data[:pageLen]
	want := binary.LittleEndian.Uint32(encoded[22:26])
	if checksumWithZeroedField(encoded) != want {
		return Page{}, 0, ErrChecksum
	}
	p := Page{
		Version:         encoded[4],
		HeaderType:      HeaderType(encoded[5]),
		GranulePosition: int64(binary.LittleEndian.Uint64(encoded[6:14])),
		Serial:          binary.LittleEndian.Uint32(encoded[14:18]),
		Sequence:        binary.LittleEndian.Uint32(encoded[18:22]),
		Checksum:        want,
		Segments:        append([]byte(nil), encoded[27:headerLen]...),
		Data:            append([]byte(nil), encoded[headerLen:pageLen]...),
	}
	if err := p.Validate(); err != nil {
		return Page{}, 0, err
	}
	return p, pageLen, nil
}

// ReadPage reads and CRC-verifies exactly one page from r. It returns io.EOF
// when no page bytes are available and io.ErrUnexpectedEOF for a partial page.
func ReadPage(r io.Reader) (Page, error) {
	header := make([]byte, 27)
	n, err := io.ReadFull(r, header)
	if err != nil {
		if err == io.EOF && n > 0 {
			err = io.ErrUnexpectedEOF
		}
		return Page{}, err
	}
	if !bytes.Equal(header[:4], []byte(CapturePattern)) {
		return Page{}, ErrInvalidCapture
	}
	segmentCount := int(header[26])
	rest := make([]byte, segmentCount)
	if _, err := io.ReadFull(r, rest); err != nil {
		return Page{}, unexpectedEOF(err)
	}
	bodyLen := 0
	for _, size := range rest {
		bodyLen += int(size)
	}
	body := make([]byte, bodyLen)
	if _, err := io.ReadFull(r, body); err != nil {
		return Page{}, unexpectedEOF(err)
	}
	encoded := make([]byte, 0, len(header)+len(rest)+len(body))
	encoded = append(encoded, header...)
	encoded = append(encoded, rest...)
	encoded = append(encoded, body...)
	p, _, err := ParsePage(encoded)
	return p, err
}

func unexpectedEOF(err error) error {
	if err == io.EOF {
		return io.ErrUnexpectedEOF
	}
	return err
}

func checksumWithZeroedField(page []byte) uint32 {
	var crc uint32
	for i, value := range page {
		if i >= 22 && i < 26 {
			value = 0
		}
		crc ^= uint32(value) << 24
		for range 8 {
			if crc&0x80000000 != 0 {
				crc = crc<<1 ^ 0x04c11db7
			} else {
				crc <<= 1
			}
		}
	}
	return crc
}

func checksum(page []byte) uint32 {
	var crc uint32
	for _, value := range page {
		crc ^= uint32(value) << 24
		for range 8 {
			if crc&0x80000000 != 0 {
				crc = crc<<1 ^ 0x04c11db7
			} else {
				crc <<= 1
			}
		}
	}
	return crc
}

func writeAll(w io.Writer, data []byte) error {
	for len(data) > 0 {
		n, err := w.Write(data)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		data = data[n:]
	}
	return nil
}
