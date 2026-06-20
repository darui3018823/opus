package opus

import (
	"errors"
	"fmt"

	packetext "github.com/darui3018823/opus/internal/extensions"
)

const (
	// ExtensionFrameAll applies an extension to every frame in a packet.
	// Generation expands it to frame-specific entries and uses the Opus repeat
	// grammar when possible.
	ExtensionFrameAll = -1

	// ExtensionIDDRED is the extension ID assigned by libopus to Deep
	// Redundancy payloads. This package transports the payload but does not
	// implement the neural DRED codec.
	ExtensionIDDRED = 126

	// ExtensionIDQEXT is the extension ID assigned by libopus to CELT quality
	// extension payloads. This package transports the payload but does not
	// implement QEXT DSP.
	ExtensionIDQEXT = 124
)

// PacketExtension is an opaque Opus packet extension associated with one
// zero-based frame. Data is copied on both input and output.
type PacketExtension struct {
	ID    int
	Frame int
	Data  []byte
}

// PacketExtensionsCount validates packet framing and its padding extension
// stream, then returns the number of extensions after repeat expansion.
func PacketExtensionsCount(packet []byte) (int, error) {
	_, padding, frameCount, err := packetExtensionLayout(packet)
	if err != nil {
		return 0, err
	}
	count, err := packetext.Count(padding, frameCount)
	if err != nil {
		return 0, mapExtensionError(err)
	}
	return count, nil
}

// PacketExtensionsParse returns packet extensions in bitstream order. Repeat
// indicators are expanded into frame-specific entries.
func PacketExtensionsParse(packet []byte) ([]PacketExtension, error) {
	_, padding, frameCount, err := packetExtensionLayout(packet)
	if err != nil {
		return nil, err
	}
	parsed, err := packetext.Parse(padding, frameCount)
	if err != nil {
		return nil, mapExtensionError(err)
	}
	out := make([]PacketExtension, len(parsed))
	for i, ext := range parsed {
		out[i] = PacketExtension{
			ID:    ext.ID,
			Frame: ext.Frame,
			Data:  append([]byte(nil), ext.Data...),
		}
	}
	return out, nil
}

// PacketExtensionsGenerate returns a packet with the same encoded audio frames
// and a replacement extension stream in its code-3 padding area.
//
// paddingBytes is the exact size of the trailing extension/padding area. Zero
// selects the minimal size. A positive value smaller than the encoded
// extension stream returns ErrBufferTooSmall. Existing packet padding and
// extensions are replaced.
func PacketExtensionsGenerate(packet []byte, extensions []PacketExtension, paddingBytes int) ([]byte, error) {
	frames, _, frameCount, err := packetExtensionLayout(packet)
	if err != nil {
		return nil, err
	}
	if paddingBytes < 0 {
		return nil, fmt.Errorf("%w: negative extension padding size %d", ErrBadArg, paddingBytes)
	}
	for _, ext := range extensions {
		if ext.Frame != ExtensionFrameAll && (ext.Frame < 0 || ext.Frame >= frameCount) {
			return nil, fmt.Errorf("%w: extension frame %d outside packet frame range [0,%d)", ErrBadArg, ext.Frame, frameCount)
		}
	}

	expanded := make([]packetext.Extension, 0, len(extensions))
	for frame := 0; frame < frameCount; frame++ {
		for _, ext := range extensions {
			if ext.Frame != ExtensionFrameAll && ext.Frame != frame {
				continue
			}
			expanded = append(expanded, packetext.Extension{
				ID: ext.ID, Frame: frame, Data: append([]byte(nil), ext.Data...),
			})
		}
	}
	padding, err := packetext.Generate(expanded, frameCount, paddingBytes)
	if err != nil {
		return nil, mapExtensionError(err)
	}

	if len(padding) == 0 {
		payload, code, err := packOpusFrames(frames, false)
		if err != nil {
			return nil, err
		}
		out := make([]byte, 1, 1+len(payload))
		out[0] = packet[0]&0xfc | byte(code)
		return append(out, payload...), nil
	}

	vbr := false
	for i := 1; i < len(frames); i++ {
		if len(frames[i]) != len(frames[0]) {
			vbr = true
			break
		}
	}
	payload, err := packOpusFramesCode3(frames, vbr, true, len(padding))
	if err != nil {
		return nil, err
	}
	copy(payload[len(payload)-len(padding):], padding)
	out := make([]byte, 1, 1+len(payload))
	out[0] = packet[0]&0xfc | 0x03
	return append(out, payload...), nil
}

func packetExtensionLayout(packet []byte) (frames [][]byte, padding []byte, frameCount int, err error) {
	if _, err = inspectPacket(packet, SampleRate48kHz); err != nil {
		return nil, nil, 0, err
	}
	_, _, code := parseTOCForRepacketizer(packet[0])
	frames, err = splitOpusFrames(packet[1:], code)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("%w: %v", ErrInvalidPacket, err)
	}
	frameCount = len(frames)
	if code != 3 {
		return frames, nil, frameCount, nil
	}
	body := packet[1:]
	if len(body) < 1 || body[0]&0x40 == 0 {
		return frames, nil, frameCount, nil
	}
	body = body[1:]
	padLen := 0
	for {
		if len(body) == 0 {
			return nil, nil, 0, fmt.Errorf("%w: missing padding count", ErrInvalidPacket)
		}
		p := int(body[0])
		body = body[1:]
		if p == 255 {
			padLen += 254
			continue
		}
		padLen += p
		break
	}
	if padLen > len(body) {
		return nil, nil, 0, fmt.Errorf("%w: padding length %d exceeds packet", ErrInvalidPacket, padLen)
	}
	return frames, packet[len(packet)-padLen:], frameCount, nil
}

func mapExtensionError(err error) error {
	switch {
	case errors.Is(err, packetext.ErrBadArg):
		return fmt.Errorf("%w: %v", ErrBadArg, err)
	case errors.Is(err, packetext.ErrBufferTooSmall):
		return fmt.Errorf("%w: %v", ErrBufferTooSmall, err)
	default:
		return fmt.Errorf("%w: %v", ErrInvalidPacket, err)
	}
}
