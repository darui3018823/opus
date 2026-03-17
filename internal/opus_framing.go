package internal

import (
	"errors"
	"fmt"
)

// Opus Mode types for TOC generation
const (
	ModeSILKOnly = 0
	ModeHybrid   = 1
	ModeCELTOnly = 2
)

// Opus bandwidth types for TOC generation
const (
	BandwidthNarrowband    = 0 // NB: 8kHz
	BandwidthMediumband    = 1 // MB: 12kHz
	BandwidthWideband      = 2 // WB: 16kHz
	BandwidthSuperwideband = 3 // SWB: 24kHz
	BandwidthFullband      = 4 // FB: 48kHz
)

// Frame Duration codes (RFC 6716 Section 3.1)
// These are frame sizes in samples at 48kHz.
const (
	FrameSize2_5ms = 120
	FrameSize5ms   = 240
	FrameSize10ms  = 480
	FrameSize20ms  = 960
	FrameSize40ms  = 1920
	FrameSize60ms  = 2880
)

// Legacy config constants (kept for backward compatibility in tests).
const (
	ConfigCELT_Mono_20ms   = 20
	ConfigCELT_Stereo_20ms = 22
)

// ParseTOC parses the Table Of Contents (TOC) byte.
// Returns configuration ID (0-31), stereo flag, and frame count code (0-3).
// RFC 6716 Section 3.1: TOC Byte: | config (5 bits) | s (1 bit) | c (2 bits) |
func ParseTOC(toc byte) (config int, stereo bool, countCode int) {
	config = int((toc >> 3) & 0x1F)
	stereo = (toc & 0x04) != 0
	countCode = int(toc & 0x03)
	return
}

// ParseTOCConfig extracts mode, bandwidth, and frame duration from a config number.
// Returns mode (SILK/Hybrid/CELT), bandwidth, and frame size in samples at 48kHz.
func ParseTOCConfig(config int) (mode, bandwidth, frameSize int) {
	switch {
	case config <= 3:
		// SILK-only NB, 10/20/40/60ms
		return ModeSILKOnly, BandwidthNarrowband, silkFrameSize(config & 3)
	case config <= 7:
		// SILK-only MB, 10/20/40/60ms
		return ModeSILKOnly, BandwidthMediumband, silkFrameSize(config & 3)
	case config <= 11:
		// SILK-only WB, 10/20/40/60ms
		return ModeSILKOnly, BandwidthWideband, silkFrameSize(config & 3)
	case config <= 13:
		// Hybrid SWB, 10/20ms
		return ModeHybrid, BandwidthSuperwideband, hybridFrameSize(config & 1)
	case config <= 15:
		// Hybrid FB, 10/20ms
		return ModeHybrid, BandwidthFullband, hybridFrameSize(config & 1)
	case config <= 19:
		// CELT-only NB, 2.5/5/10/20ms
		return ModeCELTOnly, BandwidthNarrowband, celtFrameSize(config & 3)
	case config <= 23:
		// CELT-only WB, 2.5/5/10/20ms
		return ModeCELTOnly, BandwidthWideband, celtFrameSize(config & 3)
	case config <= 27:
		// CELT-only SWB, 2.5/5/10/20ms
		return ModeCELTOnly, BandwidthSuperwideband, celtFrameSize(config & 3)
	default:
		// CELT-only FB, 2.5/5/10/20ms
		return ModeCELTOnly, BandwidthFullband, celtFrameSize(config & 3)
	}
}

func silkFrameSize(idx int) int {
	switch idx {
	case 0:
		return FrameSize10ms
	case 1:
		return FrameSize20ms
	case 2:
		return FrameSize40ms
	case 3:
		return FrameSize60ms
	}
	return FrameSize20ms
}

func hybridFrameSize(idx int) int {
	if idx == 0 {
		return FrameSize10ms
	}
	return FrameSize20ms
}

func celtFrameSize(idx int) int {
	switch idx {
	case 0:
		return FrameSize2_5ms
	case 1:
		return FrameSize5ms
	case 2:
		return FrameSize10ms
	case 3:
		return FrameSize20ms
	}
	return FrameSize20ms
}

// BandwidthForRate returns the Opus bandwidth for a given sample rate.
func BandwidthForRate(sampleRate int) (int, error) {
	switch sampleRate {
	case 8000:
		return BandwidthNarrowband, nil
	case 12000:
		return BandwidthMediumband, nil
	case 16000:
		return BandwidthWideband, nil
	case 24000:
		return BandwidthSuperwideband, nil
	case 48000:
		return BandwidthFullband, nil
	default:
		return 0, fmt.Errorf("unsupported sample rate: %d", sampleRate)
	}
}

// GenerateTOCExt creates a TOC byte for any supported mode/bandwidth/frame-size combination.
//
// RFC 6716 Table 2 defines 32 configurations (0-31):
//
//	Configs  0- 3: SILK-only  NB  (10/20/40/60ms)
//	Configs  4- 7: SILK-only  MB  (10/20/40/60ms)
//	Configs  8-11: SILK-only  WB  (10/20/40/60ms)
//	Configs 12-13: Hybrid     SWB (10/20ms)
//	Configs 14-15: Hybrid     FB  (10/20ms)
//	Configs 16-19: CELT-only  NB  (2.5/5/10/20ms)
//	Configs 20-23: CELT-only  WB  (2.5/5/10/20ms)
//	Configs 24-27: CELT-only  SWB (2.5/5/10/20ms)
//	Configs 28-31: CELT-only  FB  (2.5/5/10/20ms)
func GenerateTOCExt(mode, bandwidth, channels int, frameSizeSamples int) (byte, error) {
	if channels != 1 && channels != 2 {
		return 0, errors.New("invalid channel count (must be 1 or 2)")
	}

	config, err := configNumber(mode, bandwidth, frameSizeSamples)
	if err != nil {
		return 0, err
	}

	// Construct TOC: | config (5 bits) | s (1 bit) | c (2 bits) |
	toc := byte(config) << 3
	if channels == 2 {
		toc |= 0x04 // set stereo bit
	}
	// c = 0: single frame in packet
	return toc, nil
}

// configNumber returns the RFC 6716 config number for the given mode/bandwidth/framesize.
func configNumber(mode, bandwidth, frameSizeSamples int) (int, error) {
	switch mode {
	case ModeSILKOnly:
		durIdx, err := silkDurationIndex(frameSizeSamples)
		if err != nil {
			return 0, err
		}
		switch bandwidth {
		case BandwidthNarrowband:
			return 0 + durIdx, nil
		case BandwidthMediumband:
			return 4 + durIdx, nil
		case BandwidthWideband:
			return 8 + durIdx, nil
		default:
			return 0, fmt.Errorf("SILK mode does not support bandwidth %d", bandwidth)
		}

	case ModeHybrid:
		durIdx, err := hybridDurationIndex(frameSizeSamples)
		if err != nil {
			return 0, err
		}
		switch bandwidth {
		case BandwidthSuperwideband:
			return 12 + durIdx, nil
		case BandwidthFullband:
			return 14 + durIdx, nil
		default:
			return 0, fmt.Errorf("Hybrid mode does not support bandwidth %d", bandwidth)
		}

	case ModeCELTOnly:
		durIdx, err := celtDurationIndex(frameSizeSamples)
		if err != nil {
			return 0, err
		}
		switch bandwidth {
		case BandwidthNarrowband:
			return 16 + durIdx, nil
		case BandwidthWideband:
			return 20 + durIdx, nil
		case BandwidthSuperwideband:
			return 24 + durIdx, nil
		case BandwidthFullband:
			return 28 + durIdx, nil
		default:
			return 0, fmt.Errorf("CELT mode does not support bandwidth %d", bandwidth)
		}

	default:
		return 0, fmt.Errorf("unknown mode: %d", mode)
	}
}

func silkDurationIndex(frameSizeSamples int) (int, error) {
	switch frameSizeSamples {
	case FrameSize10ms:
		return 0, nil
	case FrameSize20ms:
		return 1, nil
	case FrameSize40ms:
		return 2, nil
	case FrameSize60ms:
		return 3, nil
	default:
		return 0, fmt.Errorf("SILK mode does not support frame size %d samples", frameSizeSamples)
	}
}

func hybridDurationIndex(frameSizeSamples int) (int, error) {
	switch frameSizeSamples {
	case FrameSize10ms:
		return 0, nil
	case FrameSize20ms:
		return 1, nil
	default:
		return 0, fmt.Errorf("Hybrid mode does not support frame size %d samples", frameSizeSamples)
	}
}

func celtDurationIndex(frameSizeSamples int) (int, error) {
	switch frameSizeSamples {
	case FrameSize2_5ms:
		return 0, nil
	case FrameSize5ms:
		return 1, nil
	case FrameSize10ms:
		return 2, nil
	case FrameSize20ms:
		return 3, nil
	default:
		return 0, fmt.Errorf("CELT mode does not support frame size %d samples", frameSizeSamples)
	}
}

// GenerateTOC creates a TOC byte for CELT-only fullband mode.
// This is the legacy interface. For multi-rate support, the encoder uses
// GenerateTOCExt internally and this function remains for backward compatibility.
//
// frameSize is in samples at 48kHz. Only 960 (20ms) is supported by this function.
func GenerateTOC(channels int, frameSize int) (byte, error) {
	if frameSize != FrameSize20ms {
		return 0, fmt.Errorf("currently only 20ms frames (960 samples @ 48kHz) are supported for legacy TOC generation")
	}

	return GenerateTOCExt(ModeCELTOnly, BandwidthFullband, channels, FrameSize20ms)
}
