package opus

import "fmt"

// Repacketizer combines Opus frames with matching TOC configurations without
// decoding and re-encoding their audio, like libopus's OpusRepacketizer. The
// packet extensions carried in each added packet's padding are kept (with
// their frame numbers moved to the frames' new positions). It is mutable,
// must not be copied after first use, and is not safe for concurrent use. Cat
// copies accumulated frames.
type Repacketizer struct {
	toc       byte
	framesize int // samples per frame at 8 kHz
	frames    []repacketizerFrame
}

// NewRepacketizer creates an empty single-stream Opus repacketizer.
func NewRepacketizer() *Repacketizer { return &Repacketizer{} }

// Reset removes all frames accumulated by the repacketizer.
func (r *Repacketizer) Reset() {
	r.toc = 0
	r.framesize = 0
	r.frames = nil
}

// NumFrames returns the number of accumulated Opus frames.
func (r *Repacketizer) NumFrames() int { return len(r.frames) }

// Cat appends every frame from packet (opus_repacketizer_cat). All packets
// must have the same TOC configuration and channel count, and the
// accumulated duration must not exceed the Opus 120 ms packet limit.
func (r *Repacketizer) Cat(packet []byte) error {
	frames, err := catPacket(r.frames, &r.toc, &r.framesize, packet, false)
	if err != nil {
		return err
	}
	r.frames = frames
	return nil
}

func parseTOCForRepacketizer(toc byte) (config int, stereo bool, code int) {
	config = int(toc >> 3)
	stereo = toc&0x04 != 0
	code = int(toc & 0x03)
	return
}

// Out returns a caller-owned packet containing every accumulated frame without
// clearing the repacketizer. An empty repacketizer returns ErrBadArg.
func (r *Repacketizer) Out() ([]byte, error) {
	return r.OutRange(0, len(r.frames))
}

// OutRange returns a caller-owned packet containing frames [begin,end) without
// clearing the repacketizer (opus_repacketizer_out_range with a large enough
// buffer). Invalid or empty ranges return ErrBadArg.
func (r *Repacketizer) OutRange(begin, end int) ([]byte, error) {
	return repacketizeOutRange(r.toc, r.frames, begin, end, outRangeUnbounded(r.frames), false, false, nil)
}

// PacketPad returns packet padded to exactly newLen bytes using RFC 6716 code-3
// packet padding, like opus_packet_pad: the encoded Opus frames are not
// modified, extensions in the packet's padding are kept, and the returned slice
// is caller-owned. A newLen equal to len(packet) returns a copy without
// parsing the packet; a smaller one, or an empty packet, returns ErrBadArg.
func PacketPad(packet []byte, newLen int) ([]byte, error) {
	if len(packet) < 1 {
		return nil, fmt.Errorf("%w: empty packet", ErrBadArg)
	}
	if newLen == len(packet) {
		return append([]byte(nil), packet...), nil
	}
	if newLen < len(packet) {
		return nil, fmt.Errorf("%w: padded length %d is smaller than packet length %d", ErrBadArg, newLen, len(packet))
	}
	var toc byte
	var framesize int
	frames, err := catPacket(nil, &toc, &framesize, packet, false)
	if err != nil {
		return nil, err
	}
	return repacketizeOutRange(toc, frames, 0, len(frames), newLen, false, true, nil)
}

// PacketUnpad removes RFC 6716 packet padding and the extensions it carries,
// and returns compact framing for the packet's unchanged Opus frames
// (opus_packet_unpad).
func PacketUnpad(packet []byte) ([]byte, error) {
	if len(packet) < 1 {
		return nil, fmt.Errorf("%w: empty packet", ErrBadArg)
	}
	return unpadPacket(packet, len(packet), false)
}

// unpadPacket is the repacketizer step of opus_packet_unpad on one (possibly
// self-delimited) packet: its frames without padding or extensions.
func unpadPacket(packet []byte, maxlen int, selfDelimited bool) ([]byte, error) {
	var toc byte
	var framesize int
	frames, err := catPacket(nil, &toc, &framesize, packet, selfDelimited)
	if err != nil {
		return nil, err
	}
	for i := range frames {
		frames[i].padding, frames[i].paddingFrames = nil, 0
	}
	return repacketizeOutRange(toc, frames, 0, len(frames), maxlen, selfDelimited, false, nil)
}

// MultistreamPacketPad returns an RFC 7845 multistream packet padded to
// exactly newLen bytes, like opus_multistream_packet_pad: the first
// streams-1 self-delimited packets are kept as they are and the final
// elementary packet is padded with PacketPad. A newLen equal to len(packet)
// returns a copy; a smaller one, or an empty packet, returns ErrBadArg.
func MultistreamPacketPad(packet []byte, streams, newLen int) ([]byte, error) {
	if len(packet) < 1 {
		return nil, fmt.Errorf("%w: empty packet", ErrBadArg)
	}
	if newLen == len(packet) {
		return append([]byte(nil), packet...), nil
	}
	if newLen < len(packet) {
		return nil, fmt.Errorf("%w: padded length %d is smaller than packet length %d", ErrBadArg, newLen, len(packet))
	}
	amount := newLen - len(packet)
	offset := 0
	for s := 0; s < streams-1; s++ {
		if len(packet)-offset <= 0 {
			return nil, fmt.Errorf("%w: missing stream %d", ErrInvalidPacket, s)
		}
		p, err := packetParseImpl(packet[offset:], true)
		if err != nil {
			return nil, fmt.Errorf("stream %d: %w", s, err)
		}
		offset += p.packetOffset
	}
	last := packet[offset:]
	padded, err := PacketPad(last, len(last)+amount)
	if err != nil {
		return nil, err
	}
	out := make([]byte, 0, newLen)
	out = append(out, packet[:offset]...)
	return append(out, padded...), nil
}

// MultistreamPacketUnpad removes RFC 6716 padding and extensions from every
// elementary stream and returns compact RFC 7845 multistream framing
// (opus_multistream_packet_unpad).
func MultistreamPacketUnpad(packet []byte, streams int) ([]byte, error) {
	if len(packet) < 1 {
		return nil, fmt.Errorf("%w: empty packet", ErrBadArg)
	}
	var out []byte
	offset := 0
	for s := 0; s < streams; s++ {
		selfDelimited := s != streams-1
		rest := packet[offset:]
		if len(rest) <= 0 {
			return nil, fmt.Errorf("%w: missing stream %d", ErrInvalidPacket, s)
		}
		p, err := packetParseImpl(rest, selfDelimited)
		if err != nil {
			return nil, fmt.Errorf("stream %d: %w", s, err)
		}
		unpadded, err := unpadPacket(rest[:p.packetOffset], len(rest), selfDelimited)
		if err != nil {
			return nil, fmt.Errorf("stream %d: %w", s, err)
		}
		out = append(out, unpadded...)
		offset += p.packetOffset
	}
	return out, nil
}
