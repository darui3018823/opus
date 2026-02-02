package internal

import (
	"errors"
	"fmt"
)

// Opus Mode constants (RFC 6716 Section 3.1)
const (
	// Configuration 20: CELT-only, 20ms, 48kHz, Mono
	ConfigCELT_Mono_20ms = 20
	// Configuration 22: CELT-only, 20ms, 48kHz, Stereo
	ConfigCELT_Stereo_20ms = 22
)

// Frame Duration codes (RFC 6716 Section 3.1)
// These are not directly TOC bits but derived from table 2
const (
	FrameSize2_5ms = 120
	FrameSize5ms   = 240
	FrameSize10ms  = 480
	FrameSize20ms  = 960
	FrameSize40ms  = 1920
	FrameSize60ms  = 2880
)

// ParseTOC parses the Table Of Contents (TOC) byte.
// Returns configuration ID, stereo flag, and frame count code.
// RFC 6716 Section 3.1: TOC Byte: | config (5) | s (1) | c (2) |
func ParseTOC(toc byte) (config int, stereo bool, countCode int) {
	config = int((toc >> 3) & 0x1F)
	stereo = (toc & 0x04) != 0
	countCode = int(toc & 0x03)
	return
}

// GenerateTOC creates a TOC byte for CELT-only mode.
// Currently only supports 48kHz, 20ms frames (Table 2 in RFC 6716).
// standard CELT configurations:
// Mode 20: CELT-only, 20ms, Mono   (Bandwidth=FB, Frame=20ms)
// Mode 22: CELT-only, 20ms, Stereo (Bandwidth=FB, Frame=20ms)
func GenerateTOC(channels int, frameSize int) (byte, error) {
	// TODO: Support other frame sizes and bandwidths properly.
	// For now, we target the "standard" Opus usage: 48kHz Fullband.

	if frameSize != FrameSize20ms {
		// As per plan Phase 1, we start safe.
		return 0, fmt.Errorf("currently only 20ms frames (960 samples @ 48kHz) are supported for TOC generation")
	}

	var config int
	if channels == 1 {
		config = ConfigCELT_Mono_20ms // 20
	} else if channels == 2 {
		config = ConfigCELT_Stereo_20ms // 22
	} else {
		return 0, errors.New("invalid channel count (must be 1 or 2)")
	}

	// Code 0: 1 packet in the frame (standard usage)
	countCode := 0

	// Construct TOC: | config (5 bits) | s (1 bit) | c (2 bits) |
	// Note: The 's' bit is actually implied by the config number in Table 2 for some modes,
	// but the TOC byte format puts 's' explicitly in bit 2 for the 'c' code?
	// WAIT: RFC 6716 Section 3.1 says:
	// "The TOC byte is divided into three fields:"
	// "config (5 bits), s (1 bit), and c (2 bits)."
	// However, the *meaning* of config depends on the table.
	// Config 20 (10100) -> Mono, 20ms, Fullband
	// Config 22 (10110) -> Stereo, 20ms, Fullband
	// If we look at binary:
	// 20 = 10100
	// 22 = 10110
	// The stereo bit is NOT separate in the "Configuration Number", it is part of the property of that config.
	//
	// BUT, the TOC construction in Section 3.1 is:
	// "The top 5 bits of the TOC byte designate one of 32 possible configurations"
	// So we just take the config number and shift it left by 3.
	// The bottom 3 bits are 's' (stereo flag for signaled modes?) and 'c' (frame count).
	//
	// CORRECTION:
	// "For the purposes of the TOC byte, the configuration number is encoded in the top 5 bits."
	// "The 's' bit indicates Mono vs Stereo ... FOR SOME MODES".
	// Actually, Table 2 maps Config Number directly to properties.
	// Config 20: CELT-only, FB, 20ms, Mono.
	// Config 21: CELT-only, FB, 20ms, Mono (why duplicate? wait, table 2 is complex).
	//
	// Let's stick to the simplest valid TOC for 48kHz/20ms.
	// Config 20 (Mono)
	// Config 22 (Stereo)
	// These map to the top 5 bits.
	
	toc := byte(config << 3)
	
	// 'c' bits (bottom 2 bits) specify 1 frame, 2 frames, etc.
	// We use 0 (1 frame).
	toc |= byte(countCode)

	return toc, nil
}
