package opus

import (
	"fmt"

	framing "github.com/darui3018823/opus/internal"
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
	_, _, countCode := framing.ParseTOC(packet[0])
	frames, err := splitOpusFrames(packet[1:], countCode)
	if err != nil {
		return false, fmt.Errorf("%w: %v", ErrInvalidPacket, err)
	}
	// opus_packet_has_lbrr reads the flags straight from the first byte of
	// the first Opus frame (the carrier for the preceding loss): after the
	// nb_frames VAD bits of each channel's header.
	first := frames[0]
	if len(first) == 0 {
		return false, nil
	}
	nbFrames := 1
	if info.samplesPerFrame > 960 {
		nbFrames = info.samplesPerFrame / 960
	}
	lbrr := (first[0]>>(7-nbFrames))&1 != 0
	if info.channels == 2 {
		lbrr = lbrr || (first[0]>>(6-2*nbFrames))&1 != 0
	}
	return lbrr, nil
}
