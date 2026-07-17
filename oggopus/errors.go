// Package oggopus implements Ogg page framing and the Ogg Opus mapping.
//
// Page, PacketReader, and PacketWriter provide low-level Ogg framing with CRC,
// lacing, continuation, serial-number, and sequence validation. Reader and
// Writer add OpusHead, OpusTags, packet-duration, pre-skip, end-trim, and
// 48 kHz granule-position handling. Reader advances through chained logical
// streams automatically and can seek with RFC 7845 decoder pre-roll when its
// source implements io.ReadSeeker. Writer creates one logical stream; callers
// create a chain by finishing each Writer and creating another on the same
// destination. Multiplexed physical streams are not demultiplexed.
//
// Reader, Writer, PacketReader, and PacketWriter are stateful, must not be
// copied after first use, and are not safe for concurrent use. They borrow
// their io.Reader or io.Writer for their lifetime but do not close it. Parsed
// packet and metadata byte slices are caller-owned copies. This package
// validates and transports Opus packets; it does not decode PCM.
package oggopus

import "errors"

var (
	// ErrInvalidCapture indicates that an Ogg page does not start with "OggS".
	ErrInvalidCapture = errors.New("oggopus: invalid Ogg capture pattern")
	// ErrUnsupportedVersion indicates an unsupported Ogg or OpusHead version.
	ErrUnsupportedVersion = errors.New("oggopus: unsupported version")
	// ErrInvalidHeaderType indicates that an Ogg page uses reserved flags.
	ErrInvalidHeaderType = errors.New("oggopus: invalid page header type")
	// ErrInvalidPage indicates inconsistent Ogg page fields or lacing.
	ErrInvalidPage = errors.New("oggopus: invalid Ogg page")
	// ErrChecksum indicates that an encoded Ogg page failed CRC verification.
	ErrChecksum = errors.New("oggopus: page checksum mismatch")
	// ErrSerial indicates an unexpected or reused logical-stream serial number.
	ErrSerial = errors.New("oggopus: unexpected bitstream serial number")
	// ErrSequence indicates a non-consecutive Ogg page sequence number.
	ErrSequence = errors.New("oggopus: non-consecutive page sequence number")
	// ErrUnexpectedContinue indicates a continued page without a packet prefix.
	ErrUnexpectedContinue = errors.New("oggopus: unexpected continued packet")
	// ErrMissingContinue indicates a partial packet followed by a fresh page.
	ErrMissingContinue = errors.New("oggopus: missing continued packet page")
	// ErrTruncatedPacket indicates an unfinished packet at logical stream end.
	ErrTruncatedPacket = errors.New("oggopus: truncated packet")
	// ErrAfterEOS indicates page data encountered after an EOS page.
	ErrAfterEOS = errors.New("oggopus: data after end-of-stream page")
	// ErrInvalidOpusHead indicates a malformed or inconsistent OpusHead packet.
	ErrInvalidOpusHead = errors.New("oggopus: invalid OpusHead packet")
	// ErrInvalidOpusTags indicates a malformed or non-UTF-8 OpusTags packet.
	ErrInvalidOpusTags = errors.New("oggopus: invalid OpusTags packet")
	// ErrInvalidOpusStream indicates invalid Ogg Opus headers, timing, or layout.
	ErrInvalidOpusStream = errors.New("oggopus: invalid Ogg Opus stream")
	// ErrWriterClosed indicates an operation on a finalized writer.
	ErrWriterClosed = errors.New("oggopus: writer is closed")
	// ErrInvalidGranule indicates an invalid packet granule position.
	ErrInvalidGranule = errors.New("oggopus: invalid granule position")
	// ErrNotSeekable indicates that Reader.SeekPCM has no io.ReadSeeker source.
	ErrNotSeekable = errors.New("oggopus: source is not seekable")
	// ErrSeekOutOfRange indicates a sample outside the current logical stream.
	ErrSeekOutOfRange = errors.New("oggopus: seek sample is out of range")
)
