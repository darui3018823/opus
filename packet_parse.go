package opus

import "fmt"

// PacketParse splits an Opus packet into its frames like libopus
// opus_packet_parse. It returns the TOC byte, the frames (sub-slices of data,
// not copies) and the payload offset, the number of bytes before the first
// frame (TOC and framing header). Validation is libopus's: a code 3 packet
// may carry at most 120 ms, and a frame whose size is implicit may not exceed
// 1275 bytes. Unlike InspectPacket it does not otherwise limit the packet's
// duration. Malformed packets return an error wrapping ErrInvalidPacket.
func PacketParse(data []byte) (toc byte, frames [][]byte, payloadOffset int, err error) {
	invalid := func() (byte, [][]byte, int, error) {
		return 0, nil, 0, fmt.Errorf("%w: malformed packet framing", ErrInvalidPacket)
	}
	if len(data) == 0 {
		return invalid()
	}
	toc = data[0]
	framesize := packetSamplesPerFrame48k(toc)
	pos := 1
	n := len(data) - 1
	lastSize := n
	var count int
	var size [48]int
	switch toc & 0x3 {
	case 0:
		count = 1
	case 1:
		count = 2
		if n&1 != 0 {
			return invalid()
		}
		lastSize = n / 2
		size[0] = lastSize
	case 2:
		count = 2
		bytes, s := parseFrameSize(data[pos:], n)
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
				n -= min(p, 254)
				if p != 255 {
					break
				}
			}
		}
		if n < 0 {
			return invalid()
		}
		if ch&0x80 != 0 {
			lastSize = n
			for i := 0; i < count-1; i++ {
				bytes, s := parseFrameSize(data[pos:], n)
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
		} else {
			lastSize = n / count
			if lastSize*count != n {
				return invalid()
			}
			for i := 0; i < count-1; i++ {
				size[i] = lastSize
			}
		}
	}
	if lastSize > 1275 {
		return invalid()
	}
	size[count-1] = lastSize
	payloadOffset = pos
	frames = make([][]byte, count)
	for i := range frames {
		frames[i] = data[pos : pos+size[i] : pos+size[i]]
		pos += size[i]
	}
	return toc, frames, payloadOffset, nil
}

// parseFrameSize is libopus parse_size: a one- or two-byte frame length from
// the first n bytes of data, returning the bytes consumed and the size (-1
// when the length is truncated).
func parseFrameSize(data []byte, n int) (bytes, size int) {
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

// packetSamplesPerFrame48k is opus_packet_get_samples_per_frame at 48 kHz.
func packetSamplesPerFrame48k(toc byte) int {
	switch {
	case toc&0x80 != 0:
		return (48000 << ((toc >> 3) & 0x3)) / 400
	case toc&0x60 == 0x60:
		if toc&0x08 != 0 {
			return 48000 / 50
		}
		return 48000 / 100
	default:
		shift := (toc >> 3) & 0x3
		if shift == 3 {
			return 48000 * 60 / 1000
		}
		return (48000 << shift) / 100
	}
}
