package opus

import (
	"errors"
	"fmt"

	packetext "github.com/darui3018823/opus/internal/extensions"
)

// parsedPacket is the result of opus_packet_parse_impl.
type parsedPacket struct {
	toc     byte
	frames  [][]byte
	padding []byte
	// payloadOffset is the header size before the first frame and
	// packetOffset the whole (self-delimited) packet's size in data.
	payloadOffset int
	packetOffset  int
}

// libopusParseSize is parse_size: a one- or two-byte frame length from the
// first n bytes of data (-1 when truncated) and the bytes it takes.
func libopusParseSize(data []byte, n int) (bytes, size int) {
	switch {
	case n < 1:
		return -1, -1
	case data[0] < 252:
		return 1, int(data[0])
	case n < 2:
		return -1, -1
	default:
		return 2, 4*int(data[1]) + int(data[0])
	}
}

// appendFrameSize is encode_size.
func appendFrameSize(dst []byte, size int) []byte {
	if size < 252 {
		return append(dst, byte(size))
	}
	first := 252 + size&3
	return append(dst, byte(first), byte((size-first)>>2))
}

// packetParseImpl is opus_packet_parse_impl. With selfDelimited the last
// frame carries an explicit size, as in all but the last stream of a
// multistream packet.
func packetParseImpl(data []byte, selfDelimited bool) (parsedPacket, error) {
	invalid := func() (parsedPacket, error) {
		return parsedPacket{}, fmt.Errorf("%w: malformed packet framing", ErrInvalidPacket)
	}
	if len(data) == 0 {
		return invalid()
	}
	framesize := packetSamplesPerFrame48k(data[0])
	toc := data[0]
	pos := 1
	n := len(data) - 1
	lastSize := n
	cbr := false
	pad := 0
	var count int
	var size [48]int
	switch toc & 3 {
	case 0:
		count = 1
	case 1:
		count, cbr = 2, true
		if !selfDelimited {
			if n&1 != 0 {
				return invalid()
			}
			lastSize = n / 2
			size[0] = lastSize
		}
	case 2:
		count = 2
		bytes, s := libopusParseSize(data[pos:], n)
		n -= bytes
		if s < 0 || s > n {
			return invalid()
		}
		size[0] = s
		pos += bytes
		lastSize = n - s
	default:
		if n < 1 {
			return invalid()
		}
		ch := data[pos]
		pos++
		count = int(ch & 0x3f)
		if count <= 0 || framesize*count > 5760 {
			return invalid()
		}
		n--
		if ch&0x40 != 0 {
			for {
				if n <= 0 {
					return invalid()
				}
				p := int(data[pos])
				pos++
				n--
				tmp := p
				if p == 255 {
					tmp = 254
				}
				n -= tmp
				pad += tmp
				if p != 255 {
					break
				}
			}
		}
		if n < 0 {
			return invalid()
		}
		cbr = ch&0x80 == 0
		if !cbr {
			lastSize = n
			for i := 0; i < count-1; i++ {
				bytes, s := libopusParseSize(data[pos:], n)
				n -= bytes
				if s < 0 || s > n {
					return invalid()
				}
				size[i] = s
				pos += bytes
				lastSize -= bytes + s
			}
			if lastSize < 0 {
				return invalid()
			}
		} else if !selfDelimited {
			lastSize = n / count
			if lastSize*count != n {
				return invalid()
			}
			for i := 0; i < count-1; i++ {
				size[i] = lastSize
			}
		}
	}
	if selfDelimited {
		bytes, s := libopusParseSize(data[pos:], n)
		n -= bytes
		if s < 0 || s > n {
			return invalid()
		}
		pos += bytes
		size[count-1] = s
		if cbr {
			if s*count > n {
				return invalid()
			}
			for i := 0; i < count-1; i++ {
				size[i] = s
			}
		} else if bytes+s > lastSize {
			return invalid()
		}
	} else {
		if lastSize > 1275 {
			return invalid()
		}
		size[count-1] = lastSize
	}
	out := parsedPacket{toc: toc, payloadOffset: pos, frames: make([][]byte, count)}
	for i := 0; i < count; i++ {
		out.frames[i] = data[pos : pos+size[i] : pos+size[i]]
		pos += size[i]
	}
	out.padding = data[pos : pos+pad : pos+pad]
	out.packetOffset = pos + pad
	return out, nil
}

// repacketizerFrame is one frame held by a Repacketizer: the padding (and
// its frame count) of the packet it came from rides on that packet's first
// frame, as in OpusRepacketizer.
type repacketizerFrame struct {
	data          []byte
	padding       []byte
	paddingFrames int
}

// catPacket is opus_repacketizer_cat_impl on a frame list with the given
// TOC and frame duration (samples at 8 kHz); it returns the extended list.
func catPacket(frames []repacketizerFrame, toc *byte, framesize *int, data []byte, selfDelimited bool) ([]repacketizerFrame, error) {
	if len(data) < 1 {
		return frames, fmt.Errorf("%w: empty packet", ErrInvalidPacket)
	}
	if len(frames) == 0 {
		*toc = data[0]
		*framesize = packetSamplesPerFrame48k(data[0]) / 6
	} else if *toc&0xfc != data[0]&0xfc {
		return frames, fmt.Errorf("%w: repacketizer TOC mismatch", ErrInvalidPacket)
	}
	count := 1
	switch data[0] & 3 {
	case 1, 2:
		count = 2
	case 3:
		if len(data) < 2 {
			return frames, fmt.Errorf("%w: missing frame count", ErrInvalidPacket)
		}
		count = int(data[1] & 0x3f)
	}
	if count < 1 {
		return frames, fmt.Errorf("%w: no frames", ErrInvalidPacket)
	}
	if (count+len(frames))*(*framesize) > 960 {
		return frames, fmt.Errorf("%w: repacketized duration exceeds 120 ms", ErrInvalidPacket)
	}
	p, err := packetParseImpl(data, selfDelimited)
	if err != nil {
		return frames, err
	}
	for i, f := range p.frames {
		rf := repacketizerFrame{data: append([]byte(nil), f...)}
		if i == 0 {
			rf.padding = append([]byte(nil), p.padding...)
			rf.paddingFrames = len(p.frames)
		}
		frames = append(frames, rf)
	}
	return frames, nil
}

// repacketizeOutRange is opus_repacketizer_out_range_impl: frames
// [begin,end) as one packet of at most maxlen bytes (code 0-3, padded to
// maxlen with pad), keeping the extensions of the frames' packets
// (renumbered) after extra.
func repacketizeOutRange(toc byte, all []repacketizerFrame, begin, end, maxlen int, selfDelimited, pad bool, extra []packetext.Extension) ([]byte, error) {
	if begin < 0 || begin >= end || end > len(all) {
		return nil, fmt.Errorf("%w: invalid repacketizer range [%d,%d)", ErrBadArg, begin, end)
	}
	frames := all[begin:end]
	count := len(frames)
	lens := make([]int, count)
	for i, f := range frames {
		lens[i] = len(f.data)
	}
	tooSmall := func() ([]byte, error) {
		return nil, fmt.Errorf("%w: packet needs more than %d bytes", ErrBufferTooSmall, maxlen)
	}

	exts := append([]packetext.Extension(nil), extra...)
	for i, f := range frames {
		if f.paddingFrames == 0 {
			continue
		}
		parsed, err := packetext.Parse(f.padding, f.paddingFrames)
		if err != nil {
			return nil, fmt.Errorf("%w: padding extensions: %v", ErrInternalError, err)
		}
		for _, e := range parsed {
			e.Frame += i
			exts = append(exts, e)
		}
	}

	totSize := 0
	if selfDelimited {
		totSize = 1
		if lens[count-1] >= 252 {
			totSize++
		}
	}
	data := make([]byte, 0, min(maxlen, 1<<16))
	switch count {
	case 1:
		totSize += lens[0] + 1
		if totSize > maxlen {
			return tooSmall()
		}
		data = append(data, toc&0xfc)
	case 2:
		if lens[1] == lens[0] {
			totSize += 2*lens[0] + 1
			if totSize > maxlen {
				return tooSmall()
			}
			data = append(data, toc&0xfc|1)
		} else {
			totSize += lens[0] + lens[1] + 2
			if lens[0] >= 252 {
				totSize++
			}
			if totSize > maxlen {
				return tooSmall()
			}
			data = append(data, toc&0xfc|2)
			data = appendFrameSize(data, lens[0])
		}
	}
	var extData []byte
	onesBegin, onesEnd, extBegin := 0, 0, 0
	if count > 2 || (pad && totSize < maxlen) || len(exts) > 0 {
		// Code 3, restarting the header for the padding case.
		data = data[:0]
		totSize = 0
		if selfDelimited {
			totSize = 1
			if lens[count-1] >= 252 {
				totSize++
			}
		}
		vbr := false
		for i := 1; i < count; i++ {
			if lens[i] != lens[0] {
				vbr = true
				break
			}
		}
		if vbr {
			totSize += 2
			for i := 0; i < count-1; i++ {
				totSize += 1 + lens[i]
				if lens[i] >= 252 {
					totSize++
				}
			}
			totSize += lens[count-1]
			if totSize > maxlen {
				return tooSmall()
			}
			data = append(data, toc&0xfc|3, byte(count|0x80))
		} else {
			totSize += count*lens[0] + 2
			if totSize > maxlen {
				return tooSmall()
			}
			data = append(data, toc&0xfc|3, byte(count))
		}
		padAmount := 0
		if pad {
			padAmount = maxlen - totSize
		}
		if len(exts) > 0 {
			var err error
			extData, err = packetext.Generate(exts, count, 0)
			if err != nil {
				if errors.Is(err, packetext.ErrBadArg) {
					return nil, fmt.Errorf("%w: %v", ErrBadArg, err)
				}
				return nil, fmt.Errorf("%w: %v", ErrInternalError, err)
			}
			if len(extData) > maxlen-totSize {
				return tooSmall()
			}
			if !pad {
				padAmount = len(extData) + 1
				if len(extData) > 0 {
					padAmount = len(extData) + (len(extData)+253)/254
				}
			}
		}
		if padAmount != 0 {
			data[1] |= 0x40
			nb255s := (padAmount - 1) / 255
			if totSize+len(extData)+nb255s+1 > maxlen {
				return tooSmall()
			}
			extBegin = totSize + padAmount - len(extData)
			onesBegin = totSize + nb255s + 1
			onesEnd = totSize + padAmount - len(extData)
			for i := 0; i < nb255s; i++ {
				data = append(data, 255)
			}
			data = append(data, byte(padAmount-255*nb255s-1))
			totSize += padAmount
		}
		if vbr {
			for i := 0; i < count-1; i++ {
				data = appendFrameSize(data, lens[i])
			}
		}
	}
	if selfDelimited {
		data = appendFrameSize(data, lens[count-1])
	}
	for _, f := range frames {
		data = append(data, f.data...)
	}
	// The padding area: 0x01 padding bytes before the extensions, or zeros
	// when padding without extensions.
	for len(data) < totSize {
		data = append(data, 0)
	}
	for i := onesBegin; i < onesEnd; i++ {
		data[i] = 0x01
	}
	if len(extData) > 0 {
		copy(data[extBegin:], extData)
	}
	if pad && len(exts) == 0 {
		// libopus zero-fills only when there are no extensions.
		for i := onesBegin; i < onesEnd; i++ {
			data[i] = 0
		}
	}
	return data[:totSize], nil
}

// outRangeUnbounded is the maxlen that stands for an output buffer large
// enough for any repacketized packet (48 frames of 1275 bytes with their
// framing, plus the extension areas they carried).
func outRangeUnbounded(frames []repacketizerFrame) int {
	n := 3 + 48*(1275+3)
	for _, f := range frames {
		n += 2*len(f.padding) + 64
	}
	return n
}
