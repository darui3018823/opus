package opus

import "fmt"

// Repacketizer combines Opus frames with matching TOC configurations without
// decoding and re-encoding their audio. It is mutable, must not be copied after
// first use, and is not safe for concurrent use. Cat copies accumulated frames.
type Repacketizer struct {
	toc             byte
	frames          [][]byte
	samplesPerFrame int
}

// NewRepacketizer creates an empty single-stream Opus repacketizer.
func NewRepacketizer() *Repacketizer { return &Repacketizer{} }

// Reset removes all frames accumulated by the repacketizer.
func (r *Repacketizer) Reset() {
	r.toc = 0
	r.frames = nil
	r.samplesPerFrame = 0
}

// NumFrames returns the number of accumulated Opus frames.
func (r *Repacketizer) NumFrames() int { return len(r.frames) }

// Cat appends every frame from packet. All packets must have the same TOC
// configuration and channel count, and the accumulated duration must not exceed
// the Opus 120 ms packet limit.
func (r *Repacketizer) Cat(packet []byte) error {
	if _, err := inspectPacket(packet, SampleRate48kHz); err != nil {
		return err
	}
	samplesPerFrame, err := PacketGetSamplesPerFrame(packet, SampleRate48kHz)
	if err != nil {
		return err
	}
	baseTOC := packet[0] & 0xfc
	if len(r.frames) != 0 && (r.toc != baseTOC || r.samplesPerFrame != samplesPerFrame) {
		return fmt.Errorf("%w: repacketizer TOC mismatch", ErrInvalidPacket)
	}
	_, _, code := parseTOCForRepacketizer(packet[0])
	frames, err := splitOpusFrames(packet[1:], code)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidPacket, err)
	}
	if len(r.frames)+len(frames) > MaxPacketFrames ||
		(len(r.frames)+len(frames))*samplesPerFrame > MaxFrameSize {
		return fmt.Errorf("%w: repacketized duration exceeds 120 ms", ErrInvalidPacket)
	}
	if len(r.frames) == 0 {
		r.toc = baseTOC
		r.samplesPerFrame = samplesPerFrame
	}
	for _, frame := range frames {
		r.frames = append(r.frames, append([]byte(nil), frame...))
	}
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
// clearing the repacketizer. Invalid or empty ranges return ErrBadArg.
func (r *Repacketizer) OutRange(begin, end int) ([]byte, error) {
	if begin < 0 || end <= begin || end > len(r.frames) {
		return nil, fmt.Errorf("%w: invalid repacketizer range [%d,%d)", ErrBadArg, begin, end)
	}
	payload, code, err := packOpusFrames(r.frames[begin:end], false)
	if err != nil {
		return nil, err
	}
	packet := make([]byte, 1, 1+len(payload))
	packet[0] = r.toc | byte(code)
	packet = append(packet, payload...)
	return packet, nil
}

// PacketPad returns packet padded to exactly newLen bytes using RFC 6716 code-3
// packet padding. The encoded Opus frames are not modified and the returned
// slice is caller-owned. newLen must be at least len(packet) and large enough
// to represent the required code-3 framing overhead.
func PacketPad(packet []byte, newLen int) ([]byte, error) {
	if newLen < len(packet) {
		return nil, fmt.Errorf("%w: padded length %d is smaller than packet length %d", ErrBadArg, newLen, len(packet))
	}
	if _, err := inspectPacket(packet, SampleRate48kHz); err != nil {
		return nil, err
	}
	if newLen == len(packet) {
		return append([]byte(nil), packet...), nil
	}
	_, _, code := parseTOCForRepacketizer(packet[0])
	frames, err := splitOpusFrames(packet[1:], code)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidPacket, err)
	}
	payload, outCode, err := packOpusFramesToPacketSize(frames, false, newLen)
	if err != nil {
		return nil, err
	}
	if 1+len(payload) != newLen {
		return nil, fmt.Errorf("%w: cannot represent padded length %d", ErrBadArg, newLen)
	}
	out := make([]byte, 1, newLen)
	out[0] = packet[0]&0xfc | byte(outCode)
	out = append(out, payload...)
	return out, nil
}

// PacketUnpad removes RFC 6716 packet padding and returns canonical compact
// framing for the packet's unchanged Opus frames.
func PacketUnpad(packet []byte) ([]byte, error) {
	if _, err := inspectPacket(packet, SampleRate48kHz); err != nil {
		return nil, err
	}
	_, _, code := parseTOCForRepacketizer(packet[0])
	frames, err := splitOpusFrames(packet[1:], code)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidPacket, err)
	}
	payload, outCode, err := packOpusFrames(frames, false)
	if err != nil {
		return nil, err
	}
	out := make([]byte, 1, 1+len(payload))
	out[0] = packet[0]&0xfc | byte(outCode)
	out = append(out, payload...)
	return out, nil
}

// MultistreamPacketPad returns an RFC 7845 multistream packet padded to exactly
// newLen bytes. Padding is applied to the final elementary Opus packet; earlier
// self-delimited stream lengths are regenerated canonically. The returned slice
// is caller-owned, and newLen must accommodate any regenerated framing overhead.
func MultistreamPacketPad(packet []byte, streams, newLen int) ([]byte, error) {
	if newLen < len(packet) {
		return nil, fmt.Errorf("%w: padded length %d is smaller than packet length %d", ErrBadArg, newLen, len(packet))
	}
	packets, _, err := splitMultistreamPackets(packet, streams, SampleRate48kHz)
	if err != nil {
		return nil, err
	}
	if newLen == len(packet) {
		return append([]byte(nil), packet...), nil
	}
	canonical, err := joinMultistreamPackets(packets)
	if err != nil {
		return nil, err
	}
	if newLen < len(canonical) {
		return nil, fmt.Errorf("%w: padded length %d is smaller than canonical packet length %d", ErrBadArg, newLen, len(canonical))
	}
	last := len(packets) - 1
	paddedLastLen := len(packets[last]) + (newLen - len(canonical))
	packets[last], err = PacketPad(packets[last], paddedLastLen)
	if err != nil {
		return nil, err
	}
	out, err := joinMultistreamPackets(packets)
	if err != nil {
		return nil, err
	}
	if len(out) != newLen {
		return nil, fmt.Errorf("%w: cannot represent multistream padded length %d", ErrBadArg, newLen)
	}
	return out, nil
}

// MultistreamPacketUnpad removes RFC 6716 padding from every elementary stream
// and returns canonical RFC 7845 multistream framing.
func MultistreamPacketUnpad(packet []byte, streams int) ([]byte, error) {
	packets, _, err := splitMultistreamPackets(packet, streams, SampleRate48kHz)
	if err != nil {
		return nil, err
	}
	for i := range packets {
		packets[i], err = PacketUnpad(packets[i])
		if err != nil {
			return nil, fmt.Errorf("stream %d: %w", i, err)
		}
	}
	return joinMultistreamPackets(packets)
}
