package opus

import (
	"fmt"

	framing "github.com/darui3018823/opus/internal"
	"github.com/darui3018823/opus/internal/entcode"
)

// PacketHasLBRR reports whether a SILK-only or hybrid packet carries in-band
// FEC/LBRR data for a previous lost packet. CELT-only packets always return
// false because CELT has no LBRR layer.
func PacketHasLBRR(packet []byte) (bool, error) {
	info, err := inspectPacket(packet, SampleRate48kHz)
	if err != nil {
		return false, err
	}
	if info.mode == ModeCELTOnly {
		return false, nil
	}
	config, _, countCode := framing.ParseTOC(packet[0])
	streams, err := splitOpusFrames(packet[1:], countCode)
	if err != nil {
		return false, fmt.Errorf("%w: %v", ErrInvalidPacket, err)
	}
	nSilkFrames := silkSubframesPerOpusFrame(config)
	// libopus and DecodeFEC use only the first Opus frame of a packed
	// packet as the carrier for the immediately preceding loss. LBRR in a
	// later frame is not recoverable through that API and must not make this
	// helper report true.
	return silkStreamHasLBRR(streams[0], nSilkFrames, info.channels)
}

func silkStreamHasLBRR(stream []byte, nFrames, channels int) (bool, error) {
	if nFrames < 1 || nFrames > 3 || (channels != 1 && channels != 2) {
		return false, fmt.Errorf("%w: invalid SILK LBRR probe geometry", ErrInvalidPacket)
	}
	// A one-byte digital-silence SILK payload carries no LBRR header.
	if len(stream) < 2 {
		return false, nil
	}
	dec := entcode.NewDecoder(stream)
	if dec.Error() != nil {
		return false, fmt.Errorf("%w: %v", ErrInvalidPacket, dec.Error())
	}
	for ch := 0; ch < channels; ch++ {
		for i := 0; i < nFrames; i++ {
			_ = dec.DecodeBitLogp(1) // VAD flag
		}
		if dec.DecodeBitLogp(1) {
			return true, nil
		}
	}
	if dec.Error() != nil {
		return false, fmt.Errorf("%w: %v", ErrInvalidPacket, dec.Error())
	}
	return false, nil
}
