package oggopus

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"unicode/utf8"
)

const (
	// OpusHeadSignature identifies an Ogg Opus identification header.
	OpusHeadSignature = "OpusHead"
	// OpusTagsSignature identifies an Ogg Opus comment header.
	OpusTagsSignature = "OpusTags"
)

// Head is the Ogg Opus identification header. PreSkip and granule positions
// are measured at 48 kHz. OutputGain is signed Q7.8 dB.
type Head struct {
	// Version is the OpusHead version. Values 1 through 15 are accepted.
	Version uint8
	// Channels is the decoded output channel count.
	Channels uint8
	// PreSkip is the number of decoded samples per channel to discard at 48 kHz.
	PreSkip uint16
	// InputSampleRate is an informational source-rate hint in Hz.
	InputSampleRate uint32
	// OutputGain is the playback gain in signed Q7.8 dB.
	OutputGain int16
	// MappingFamily selects the channel mapping semantics.
	MappingFamily uint8
	// StreamCount is the number of elementary Opus streams for nonzero families.
	StreamCount uint8
	// CoupledCount is the number of stereo elementary streams.
	CoupledCount uint8
	// ChannelMapping maps output channels to coded channels. A value of 255
	// means silence. Family 0 requires this slice to be empty.
	ChannelMapping []uint8
}

// Validate checks the OpusHead version, channel counts, and mapping fields.
func (h Head) Validate() error {
	if h.Version == 0 || h.Version > 15 {
		return fmt.Errorf("%w: version %d", ErrInvalidOpusHead, h.Version)
	}
	if h.Channels == 0 {
		return fmt.Errorf("%w: zero channels", ErrInvalidOpusHead)
	}
	if h.MappingFamily == 0 {
		if h.Channels > 2 {
			return fmt.Errorf("%w: mapping family 0 has %d channels", ErrInvalidOpusHead, h.Channels)
		}
		if len(h.ChannelMapping) != 0 {
			return fmt.Errorf("%w: mapping family 0 includes a mapping table", ErrInvalidOpusHead)
		}
		return nil
	}
	if h.StreamCount == 0 || h.CoupledCount > h.StreamCount {
		return fmt.Errorf("%w: invalid stream counts %d/%d", ErrInvalidOpusHead, h.StreamCount, h.CoupledCount)
	}
	decodedChannels := int(h.StreamCount) + int(h.CoupledCount)
	if decodedChannels > 255 || len(h.ChannelMapping) != int(h.Channels) {
		return fmt.Errorf("%w: invalid channel mapping size", ErrInvalidOpusHead)
	}
	for _, index := range h.ChannelMapping {
		if int(index) >= decodedChannels && index != 255 {
			return fmt.Errorf("%w: channel index %d out of range", ErrInvalidOpusHead, index)
		}
	}
	if h.MappingFamily == 1 && h.Channels > 8 {
		return fmt.Errorf("%w: mapping family 1 has %d channels", ErrInvalidOpusHead, h.Channels)
	}
	return nil
}

// MarshalBinary validates h and returns a new encoded OpusHead packet. Fields
// unknown to this implementation cannot be represented for future versions.
func (h Head) MarshalBinary() ([]byte, error) {
	if err := h.Validate(); err != nil {
		return nil, err
	}
	size := 19
	if h.MappingFamily != 0 {
		size += 2 + int(h.Channels)
	}
	out := make([]byte, size)
	copy(out, OpusHeadSignature)
	out[8] = h.Version
	out[9] = h.Channels
	binary.LittleEndian.PutUint16(out[10:12], h.PreSkip)
	binary.LittleEndian.PutUint32(out[12:16], h.InputSampleRate)
	binary.LittleEndian.PutUint16(out[16:18], uint16(h.OutputGain))
	out[18] = h.MappingFamily
	if h.MappingFamily != 0 {
		out[19] = h.StreamCount
		out[20] = h.CoupledCount
		copy(out[21:], h.ChannelMapping)
	}
	return out, nil
}

// ParseHead validates and parses an OpusHead packet. ChannelMapping is copied.
// Version 1 requires the exact defined length; trailing fields accepted for
// versions 2 through 15 are not preserved.
func ParseHead(packet []byte) (Head, error) {
	if len(packet) < 19 || !bytes.Equal(packet[:8], []byte(OpusHeadSignature)) {
		return Head{}, ErrInvalidOpusHead
	}
	h := Head{
		Version:         packet[8],
		Channels:        packet[9],
		PreSkip:         binary.LittleEndian.Uint16(packet[10:12]),
		InputSampleRate: binary.LittleEndian.Uint32(packet[12:16]),
		OutputGain:      int16(binary.LittleEndian.Uint16(packet[16:18])),
		MappingFamily:   packet[18],
	}
	expected := 19
	if h.MappingFamily != 0 {
		expected += 2 + int(h.Channels)
		if len(packet) < expected {
			return Head{}, ErrInvalidOpusHead
		}
		h.StreamCount = packet[19]
		h.CoupledCount = packet[20]
		h.ChannelMapping = append([]byte(nil), packet[21:expected]...)
	}
	if h.Version == 1 && len(packet) != expected {
		return Head{}, fmt.Errorf("%w: unexpected trailing data", ErrInvalidOpusHead)
	}
	if err := h.Validate(); err != nil {
		return Head{}, err
	}
	return h, nil
}

// Tags is an Ogg Opus comment header. Vendor and Comments must be UTF-8.
// Comment field-name syntax is not otherwise enforced. Extra preserves
// optional trailing binary data.
type Tags struct {
	// Vendor identifies the producing application.
	Vendor string
	// Comments contains UTF-8 comment entries, conventionally NAME=value.
	Comments []string
	// Extra is optional trailing binary data preserved during parsing and marshaling.
	Extra []byte
}

// Validate checks that Vendor and every Comments entry are valid UTF-8.
func (t Tags) Validate() error {
	if !utf8.ValidString(t.Vendor) {
		return fmt.Errorf("%w: vendor is not UTF-8", ErrInvalidOpusTags)
	}
	for i, comment := range t.Comments {
		if !utf8.ValidString(comment) {
			return fmt.Errorf("%w: comment %d is not UTF-8", ErrInvalidOpusTags, i)
		}
	}
	return nil
}

// MarshalBinary validates t and returns a new encoded OpusTags packet.
func (t Tags) MarshalBinary() ([]byte, error) {
	if err := t.Validate(); err != nil {
		return nil, err
	}
	size := 8 + 4 + len(t.Vendor) + 4 + len(t.Extra)
	for _, comment := range t.Comments {
		size += 4 + len(comment)
	}
	out := make([]byte, size)
	copy(out, OpusTagsSignature)
	offset := 8
	binary.LittleEndian.PutUint32(out[offset:offset+4], uint32(len(t.Vendor)))
	offset += 4
	copy(out[offset:], t.Vendor)
	offset += len(t.Vendor)
	binary.LittleEndian.PutUint32(out[offset:offset+4], uint32(len(t.Comments)))
	offset += 4
	for _, comment := range t.Comments {
		binary.LittleEndian.PutUint32(out[offset:offset+4], uint32(len(comment)))
		offset += 4
		copy(out[offset:], comment)
		offset += len(comment)
	}
	copy(out[offset:], t.Extra)
	return out, nil
}

// ParseTags validates and parses an OpusTags packet. Extra and the returned
// strings do not alias packet.
func ParseTags(packet []byte) (Tags, error) {
	if len(packet) < 16 || !bytes.Equal(packet[:8], []byte(OpusTagsSignature)) {
		return Tags{}, ErrInvalidOpusTags
	}
	offset := 8
	vendorLen := uint64(binary.LittleEndian.Uint32(packet[offset : offset+4]))
	offset += 4
	if vendorLen > uint64(len(packet)-offset) {
		return Tags{}, ErrInvalidOpusTags
	}
	t := Tags{Vendor: string(packet[offset : offset+int(vendorLen)])}
	offset += int(vendorLen)
	if len(packet)-offset < 4 {
		return Tags{}, ErrInvalidOpusTags
	}
	count := uint64(binary.LittleEndian.Uint32(packet[offset : offset+4]))
	offset += 4
	if count > uint64(len(packet)-offset)/4 {
		return Tags{}, ErrInvalidOpusTags
	}
	t.Comments = make([]string, 0, int(count))
	for range count {
		if len(packet)-offset < 4 {
			return Tags{}, ErrInvalidOpusTags
		}
		n := uint64(binary.LittleEndian.Uint32(packet[offset : offset+4]))
		offset += 4
		if n > uint64(len(packet)-offset) {
			return Tags{}, ErrInvalidOpusTags
		}
		t.Comments = append(t.Comments, string(packet[offset:offset+int(n)]))
		offset += int(n)
	}
	t.Extra = append([]byte(nil), packet[offset:]...)
	if err := t.Validate(); err != nil {
		return Tags{}, err
	}
	return t, nil
}
