// Package oggopus implements Ogg page framing and the Ogg Opus mapping.
package oggopus

import "errors"

var (
	ErrInvalidCapture     = errors.New("oggopus: invalid Ogg capture pattern")
	ErrUnsupportedVersion = errors.New("oggopus: unsupported version")
	ErrInvalidHeaderType  = errors.New("oggopus: invalid page header type")
	ErrInvalidPage        = errors.New("oggopus: invalid Ogg page")
	ErrChecksum           = errors.New("oggopus: page checksum mismatch")
	ErrSerial             = errors.New("oggopus: unexpected bitstream serial number")
	ErrSequence           = errors.New("oggopus: non-consecutive page sequence number")
	ErrUnexpectedContinue = errors.New("oggopus: unexpected continued packet")
	ErrMissingContinue    = errors.New("oggopus: missing continued packet page")
	ErrTruncatedPacket    = errors.New("oggopus: truncated packet")
	ErrAfterEOS           = errors.New("oggopus: data after end-of-stream page")
	ErrInvalidOpusHead    = errors.New("oggopus: invalid OpusHead packet")
	ErrInvalidOpusTags    = errors.New("oggopus: invalid OpusTags packet")
	ErrInvalidOpusStream  = errors.New("oggopus: invalid Ogg Opus stream")
	ErrWriterClosed       = errors.New("oggopus: writer is closed")
	ErrInvalidGranule     = errors.New("oggopus: invalid granule position")
	ErrNotSeekable        = errors.New("oggopus: source is not seekable")
	ErrSeekOutOfRange     = errors.New("oggopus: seek sample is out of range")
)
