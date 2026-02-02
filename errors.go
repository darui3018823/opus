package opus

import "errors"

// Common Opus errors
var (
	// ErrBadArg indicates that one or more arguments are invalid
	ErrBadArg = errors.New("opus: bad argument")

	// ErrBufferTooSmall indicates that the provided buffer is too small
	ErrBufferTooSmall = errors.New("opus: buffer too small")

	// ErrInternalError indicates an internal error occurred
	ErrInternalError = errors.New("opus: internal error")

	// ErrInvalidPacket indicates the packet is invalid or corrupted
	ErrInvalidPacket = errors.New("opus: invalid packet")

	// ErrUnimplemented indicates a feature is not yet implemented
	ErrUnimplemented = errors.New("opus: unimplemented")

	// ErrInvalidState indicates the encoder/decoder is in an invalid state
	ErrInvalidState = errors.New("opus: invalid state")

	// ErrAllocFail indicates memory allocation failed
	ErrAllocFail = errors.New("opus: allocation failed")

	// ErrUnsupportedSampleRate indicates the sample rate is not supported
	ErrUnsupportedSampleRate = errors.New("opus: unsupported sample rate")

	// ErrUnsupportedChannels indicates the channel count is not supported
	ErrUnsupportedChannels = errors.New("opus: unsupported number of channels")

	// ErrUnsupportedFrameSize indicates the frame size is not supported
	ErrUnsupportedFrameSize = errors.New("opus: unsupported frame size")

	// ErrUnsupportedBandwidth indicates the bandwidth is not supported
	ErrUnsupportedBandwidth = errors.New("opus: unsupported bandwidth")
)
