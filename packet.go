package opus

import (
	"fmt"

	framing "github.com/darui3018823/opus/internal"
)

type packetMetadata struct {
	config          int
	mode            int
	bandwidth       int
	channels        int
	frameCount      int
	samplesPerFrame int
	totalSamples    int
}

// PacketGetConfig returns the RFC 6716 TOC configuration number (0-31).
func PacketGetConfig(data []byte) (int, error) {
	info, err := inspectPacket(data, 0)
	if err != nil {
		return 0, err
	}
	return info.config, nil
}

// PacketGetMode returns ModeSILKOnly, ModeHybrid, or ModeCELTOnly.
func PacketGetMode(data []byte) (int, error) {
	info, err := inspectPacket(data, 0)
	if err != nil {
		return 0, err
	}
	return info.mode, nil
}

// PacketGetBandwidth returns one of the Bandwidth* constants.
func PacketGetBandwidth(data []byte) (int, error) {
	info, err := inspectPacket(data, 0)
	if err != nil {
		return 0, err
	}
	return info.bandwidth, nil
}

// PacketGetNumChannels returns the channel count encoded in the packet TOC.
func PacketGetNumChannels(data []byte) (int, error) {
	info, err := inspectPacket(data, 0)
	if err != nil {
		return 0, err
	}
	return info.channels, nil
}

// PacketGetNumFrames returns the number of Opus frames in the packet.
func PacketGetNumFrames(data []byte) (int, error) {
	info, err := inspectPacket(data, 0)
	if err != nil {
		return 0, err
	}
	return info.frameCount, nil
}

// PacketGetSamplesPerFrame returns the number of samples per channel in each
// Opus frame when decoded at sampleRate.
func PacketGetSamplesPerFrame(data []byte, sampleRate int) (int, error) {
	info, err := inspectPacket(data, sampleRate)
	if err != nil {
		return 0, err
	}
	return info.samplesPerFrame, nil
}

// PacketGetNumSamples returns the packet duration in samples per channel when
// decoded at sampleRate.
func PacketGetNumSamples(data []byte, sampleRate int) (int, error) {
	info, err := inspectPacket(data, sampleRate)
	if err != nil {
		return 0, err
	}
	return info.totalSamples, nil
}

func inspectPacket(data []byte, sampleRate int) (*packetMetadata, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("%w: empty packet", ErrInvalidPacket)
	}
	if sampleRate != 0 && !isValidOpusRate(sampleRate) {
		return nil, fmt.Errorf("%w: %w: %d", ErrBadArg, ErrUnsupportedSampleRate, sampleRate)
	}

	config, stereo, countCode := framing.ParseTOC(data[0])
	frames, err := splitOpusFrames(data[1:], countCode)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidPacket, err)
	}
	for i, frame := range frames {
		if len(frame) > MaxFrameBytes {
			return nil, fmt.Errorf("%w: frame %d has %d bytes, maximum is %d", ErrInvalidPacket, i, len(frame), MaxFrameBytes)
		}
	}

	internalMode, internalBandwidth, frameSize48 := framing.ParseTOCConfig(config)
	info := &packetMetadata{
		config:     config,
		mode:       publicPacketMode(internalMode),
		bandwidth:  publicPacketBandwidth(internalBandwidth),
		channels:   1,
		frameCount: len(frames),
	}
	if stereo {
		info.channels = 2
	}
	if sampleRate != 0 {
		info.samplesPerFrame = frameSize48 * sampleRate / SampleRate48kHz
		info.totalSamples, err = packetDurationSamples(config, info.frameCount, sampleRate)
		if err != nil {
			return nil, err
		}
	}
	return info, nil
}

func publicPacketMode(mode int) int {
	switch mode {
	case framing.ModeSILKOnly:
		return ModeSILKOnly
	case framing.ModeHybrid:
		return ModeHybrid
	default:
		return ModeCELTOnly
	}
}

func publicPacketBandwidth(bandwidth int) int {
	switch bandwidth {
	case framing.BandwidthNarrowband:
		return BandwidthNarrowband
	case framing.BandwidthMediumband:
		return BandwidthMediumband
	case framing.BandwidthWideband:
		return BandwidthWideband
	case framing.BandwidthSuperwideband:
		return BandwidthSuperWideband
	default:
		return BandwidthFullband
	}
}
