// Package extensions implements the Opus packet-extension grammar carried in
// RFC 6716 code-3 padding.
package extensions

import (
	"errors"
	"fmt"
)

const (
	MinID     = 3
	MaxID     = 127
	MaxFrames = 48
)

var (
	ErrBadArg         = errors.New("extensions: bad argument")
	ErrInvalidPacket  = errors.New("extensions: invalid packet")
	ErrBufferTooSmall = errors.New("extensions: buffer too small")
)

// Extension is one decoded packet extension. Data is owned by the caller.
type Extension struct {
	ID    int
	Frame int
	Data  []byte
}

type iterator struct {
	data []byte

	currPos int
	currLen int

	repeatPos int
	repeatLen int
	lastLong  int
	srcPos    int
	srcLen    int

	trailingShortLen int
	nbFrames         int
	currFrame        int
	repeatFrame      int
	repeatL          int
}

func newIterator(data []byte, nbFrames int) *iterator {
	return &iterator{
		data:      data,
		currLen:   len(data),
		repeatLen: 0,
		lastLong:  -1,
		nbFrames:  nbFrames,
	}
}

func skipPayload(data []byte, pos, remaining, idByte, trailingShortLen int) (nextPos, nextRemaining, headerSize int, err error) {
	id := idByte >> 1
	l := idByte & 1
	switch {
	case id == 0 && l == 1, id == 2:
		return pos, remaining, 0, nil
	case id > 0 && id < 32:
		if remaining < l {
			return 0, 0, 0, ErrInvalidPacket
		}
		return pos + l, remaining - l, 0, nil
	case l == 0:
		if remaining < trailingShortLen {
			return 0, 0, 0, ErrInvalidPacket
		}
		consume := remaining - trailingShortLen
		return pos + consume, trailingShortLen, 0, nil
	default:
		bytes := 0
		for {
			if remaining < 1 || pos >= len(data) {
				return 0, 0, 0, ErrInvalidPacket
			}
			lacing := int(data[pos])
			pos++
			headerSize++
			remaining--
			if lacing > remaining {
				return 0, 0, 0, ErrInvalidPacket
			}
			bytes += lacing
			remaining -= lacing
			if lacing != 255 {
				break
			}
		}
		return pos + bytes, remaining, headerSize, nil
	}
}

func skip(data []byte, pos, remaining int) (nextPos, nextRemaining, headerSize int, err error) {
	if remaining == 0 {
		return pos, 0, 0, nil
	}
	if remaining < 1 || pos >= len(data) {
		return 0, 0, 0, ErrInvalidPacket
	}
	idByte := int(data[pos])
	nextPos, nextRemaining, headerSize, err = skipPayload(data, pos+1, remaining-1, idByte, 0)
	if err != nil {
		return 0, 0, 0, err
	}
	return nextPos, nextRemaining, headerSize + 1, nil
}

func (it *iterator) nextRepeat() (Extension, bool, error) {
	for ; it.repeatFrame < it.nbFrames; it.repeatFrame++ {
		for it.srcLen > 0 {
			repeatIDByte := int(it.data[it.srcPos])
			var err error
			it.srcPos, it.srcLen, _, err = skip(it.data, it.srcPos, it.srcLen)
			if err != nil {
				return Extension{}, false, err
			}
			if repeatIDByte <= 3 {
				continue
			}
			if it.repeatL == 0 &&
				it.repeatFrame+1 >= it.nbFrames &&
				it.srcPos == it.lastLong {
				repeatIDByte &^= 1
			}
			start := it.currPos
			nextPos, nextLen, headerSize, err := skipPayload(
				it.data, it.currPos, it.currLen, repeatIDByte, it.trailingShortLen,
			)
			if err != nil {
				return Extension{}, false, err
			}
			it.currPos, it.currLen = nextPos, nextLen
			payloadStart := start + headerSize
			return Extension{
				ID:    repeatIDByte >> 1,
				Frame: it.repeatFrame,
				Data:  append([]byte(nil), it.data[payloadStart:it.currPos]...),
			}, true, nil
		}
		it.srcPos = it.repeatPos
		it.srcLen = it.repeatLen
	}
	it.repeatPos = it.currPos
	it.lastLong = -1
	if it.repeatL == 0 {
		it.currFrame++
		if it.currFrame >= it.nbFrames {
			it.currLen = 0
		}
	}
	it.repeatFrame = 0
	return Extension{}, false, nil
}

func (it *iterator) next() (Extension, bool, error) {
	if it.currLen < 0 {
		return Extension{}, false, ErrInvalidPacket
	}
	if it.repeatFrame > 0 {
		ext, ok, err := it.nextRepeat()
		if ok || err != nil {
			return ext, ok, err
		}
	}
	for it.currLen > 0 {
		start := it.currPos
		id := int(it.data[start]) >> 1
		l := int(it.data[start]) & 1
		nextPos, nextLen, headerSize, err := skip(it.data, it.currPos, it.currLen)
		if err != nil {
			return Extension{}, false, err
		}
		it.currPos, it.currLen = nextPos, nextLen
		switch id {
		case 1:
			if l == 0 {
				it.currFrame++
			} else {
				increment := int(it.data[start+1])
				if increment == 0 {
					continue
				}
				it.currFrame += increment
			}
			if it.currFrame >= it.nbFrames {
				it.currLen = -1
				return Extension{}, false, ErrInvalidPacket
			}
			it.repeatPos = it.currPos
			it.lastLong = -1
			it.trailingShortLen = 0
		case 2:
			it.repeatL = l
			it.repeatFrame = it.currFrame + 1
			it.repeatLen = start - it.repeatPos
			it.srcPos = it.repeatPos
			it.srcLen = it.repeatLen
			ext, ok, err := it.nextRepeat()
			if ok || err != nil {
				return ext, ok, err
			}
		default:
			if id <= 2 {
				continue
			}
			if id >= 32 {
				it.lastLong = it.currPos
				it.trailingShortLen = 0
			} else {
				it.trailingShortLen += l
			}
			payloadStart := start + headerSize
			return Extension{
				ID:    id,
				Frame: it.currFrame,
				Data:  append([]byte(nil), it.data[payloadStart:it.currPos]...),
			}, true, nil
		}
	}
	return Extension{}, false, nil
}

// Parse decodes an extension-bearing padding region in bitstream order.
// Repeat indicators are expanded into one Extension per target frame.
func Parse(data []byte, nbFrames int) ([]Extension, error) {
	if nbFrames < 1 || nbFrames > MaxFrames {
		return nil, fmt.Errorf("%w: frame count %d", ErrBadArg, nbFrames)
	}
	it := newIterator(data, nbFrames)
	var out []Extension
	for {
		ext, ok, err := it.next()
		if err != nil {
			return nil, err
		}
		if !ok {
			return out, nil
		}
		out = append(out, ext)
	}
}

// Count validates and counts the extensions in an extension-bearing padding
// region. Repeat indicators count once for every expanded frame occurrence.
func Count(data []byte, nbFrames int) (int, error) {
	exts, err := Parse(data, nbFrames)
	if err != nil {
		return 0, err
	}
	return len(exts), nil
}

func appendPayload(dst []byte, ext Extension, last bool) ([]byte, error) {
	if ext.ID < MinID || ext.ID > MaxID {
		return nil, fmt.Errorf("%w: extension ID %d", ErrBadArg, ext.ID)
	}
	if ext.ID < 32 {
		if len(ext.Data) > 1 {
			return nil, fmt.Errorf("%w: short extension ID %d has %d payload bytes", ErrBadArg, ext.ID, len(ext.Data))
		}
		return append(dst, ext.Data...), nil
	}
	if !last {
		n := len(ext.Data)
		for n >= 255 {
			dst = append(dst, 255)
			n -= 255
		}
		dst = append(dst, byte(n))
	}
	return append(dst, ext.Data...), nil
}

func appendExtension(dst []byte, ext Extension, last bool) ([]byte, error) {
	l := 0
	if ext.ID < 32 {
		l = len(ext.Data)
	} else if !last {
		l = 1
	}
	dst = append(dst, byte(ext.ID<<1|l))
	return appendPayload(dst, ext, last)
}

// Generate encodes extensions using the libopus repeat and separator grammar.
// targetLen==0 produces the minimal representation. A positive targetLen
// produces exactly that many bytes by prepending extension padding bytes.
func Generate(extensions []Extension, nbFrames, targetLen int) ([]byte, error) {
	if nbFrames < 1 || nbFrames > MaxFrames || targetLen < 0 {
		return nil, fmt.Errorf("%w: frames=%d target=%d", ErrBadArg, nbFrames, targetLen)
	}
	n := len(extensions)
	frameMin := make([]int, nbFrames)
	frameMax := make([]int, nbFrames)
	frameRepeat := make([]int, nbFrames)
	for f := range frameMin {
		frameMin[f] = n
	}
	for i, ext := range extensions {
		if ext.Frame < 0 || ext.Frame >= nbFrames {
			return nil, fmt.Errorf("%w: extension frame %d", ErrBadArg, ext.Frame)
		}
		if ext.ID < MinID || ext.ID > MaxID {
			return nil, fmt.Errorf("%w: extension ID %d", ErrBadArg, ext.ID)
		}
		if ext.ID < 32 && len(ext.Data) > 1 {
			return nil, fmt.Errorf("%w: short extension ID %d has %d payload bytes", ErrBadArg, ext.ID, len(ext.Data))
		}
		if i < frameMin[ext.Frame] {
			frameMin[ext.Frame] = i
		}
		if i+1 > frameMax[ext.Frame] {
			frameMax[ext.Frame] = i + 1
		}
	}
	copy(frameRepeat, frameMin)

	currFrame := 0
	written := 0
	out := make([]byte, 0)
	for f := 0; f < nbFrames; f++ {
		lastLongIdx := -1
		repeatCount := 0
		if f+1 < nbFrames {
			for i := frameMin[f]; i < frameMax[f]; i++ {
				if extensions[i].Frame != f {
					continue
				}
				g := f + 1
				for ; g < nbFrames; g++ {
					idx := frameRepeat[g]
					if idx >= frameMax[g] || extensions[idx].Frame != g ||
						extensions[idx].ID != extensions[i].ID ||
						(extensions[idx].ID < 32 && len(extensions[idx].Data) != len(extensions[i].Data)) {
						break
					}
				}
				if g < nbFrames {
					break
				}
				if extensions[i].ID >= 32 {
					lastLongIdx = frameRepeat[nbFrames-1]
				}
				for g = f + 1; g < nbFrames; g++ {
					j := frameRepeat[g] + 1
					for j < frameMax[g] && extensions[j].Frame != g {
						j++
					}
					frameRepeat[g] = j
				}
				repeatCount++
				frameRepeat[f] = i
			}
		}
		for i := frameMin[f]; i < frameMax[f]; i++ {
			if extensions[i].Frame != f {
				continue
			}
			if f != currFrame {
				diff := f - currFrame
				if diff == 1 {
					out = append(out, 0x02)
				} else {
					out = append(out, 0x03, byte(diff))
				}
				currFrame = f
			}
			var err error
			out, err = appendExtension(out, extensions[i], written == n-1)
			if err != nil {
				return nil, err
			}
			written++
			if repeatCount > 0 && frameRepeat[f] == i {
				nbRepeated := repeatCount * (nbFrames - (f + 1))
				last := written+nbRepeated == n || (lastLongIdx < 0 && i+1 >= frameMax[f])
				repeatByte := byte(0x05)
				if last {
					repeatByte = 0x04
				}
				out = append(out, repeatByte)
				for g := f + 1; g < nbFrames; g++ {
					j := frameMin[g]
					for ; j < frameRepeat[g]; j++ {
						if extensions[j].Frame != g {
							continue
						}
						out, err = appendPayload(out, extensions[j], last && j == lastLongIdx)
						if err != nil {
							return nil, err
						}
						written++
					}
					frameMin[g] = j
				}
				if last {
					currFrame++
				}
			}
		}
	}
	if written != n {
		return nil, fmt.Errorf("%w: wrote %d of %d extensions", ErrBadArg, written, n)
	}
	if targetLen > 0 {
		if len(out) > targetLen {
			return nil, fmt.Errorf("%w: need %d bytes, target is %d", ErrBufferTooSmall, len(out), targetLen)
		}
		padding := make([]byte, targetLen-len(out))
		for i := range padding {
			padding[i] = 0x01
		}
		out = append(padding, out...)
	}
	return out, nil
}
