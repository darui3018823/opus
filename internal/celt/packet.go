package celt

import (
	"errors"
	"fmt"
)

// Packet represents a decoded CELT packet header
type Packet struct {
	// TOC byte fields
	Config      int  // Configuration number (0-31)
	Stereo      bool // Stereo flag
	FrameCount  int  // Number of frames (1, 2, or 3)
	
	// Derived fields
	FrameSize   int  // Frame size in samples
	Bandwidth   int  // Bandwidth mode
	
	// Frame data
	Frames      [][]byte // Raw frame data for each frame
}

// ParseTOC parses the Table of Contents (TOC) byte from an Opus packet
// TOC byte format (RFC 6716):
// - bits 0-4: config (determines frame size and bandwidth)
// - bit 5: stereo flag (0=mono, 1=stereo)
// - bits 6-7: frame count code
func ParseTOC(toc byte) (*Packet, error) {
	p := &Packet{}
	
	// Extract config (bits 0-4)
	p.Config = int(toc & 0x1F)
	
	// Extract stereo flag (bit 5)
	p.Stereo = (toc & 0x20) != 0
	
	// Extract frame count code (bits 6-7)
	frameCode := (toc >> 6) & 0x03
	
	// Determine frame count from code
	switch frameCode {
	case 0:
		p.FrameCount = 1
	case 1, 2:
		p.FrameCount = 2
	case 3:
		// Code 3 means VBR with frame count in next byte
		// For now, assume 2 frames (will be refined when parsing full packet)
		p.FrameCount = 2
	}
	
	// Derive frame size and bandwidth from config
	p.deriveFromConfig()
	
	return p, nil
}

// deriveFromConfig derives frame size and bandwidth from config number
func (p *Packet) deriveFromConfig() {
	config := p.Config
	
	// Config mapping (simplified from RFC 6716 Table 2)
	// Configs 0-15: SILK/Hybrid (not handled here)
	// Configs 16-19: CELT narrowband (4kHz)
	// Configs 20-23: CELT wideband (8kHz)
	// Configs 24-27: CELT super-wideband (12kHz)
	// Configs 28-31: CELT fullband (20kHz)
	
	if config >= 16 && config <= 19 {
		// CELT narrowband
		p.Bandwidth = BandwidthNarrowband
		p.FrameSize = []int{FrameSize2_5ms, FrameSize5ms, FrameSize10ms, FrameSize20ms}[config-16]
	} else if config >= 20 && config <= 23 {
		// CELT wideband
		p.Bandwidth = BandwidthWideband
		p.FrameSize = []int{FrameSize2_5ms, FrameSize5ms, FrameSize10ms, FrameSize20ms}[config-20]
	} else if config >= 24 && config <= 27 {
		// CELT super-wideband
		p.Bandwidth = BandwidthSuperwideband
		p.FrameSize = []int{FrameSize2_5ms, FrameSize5ms, FrameSize10ms, FrameSize20ms}[config-24]
	} else if config >= 28 && config <= 31 {
		// CELT fullband
		p.Bandwidth = BandwidthFullband
		p.FrameSize = []int{FrameSize2_5ms, FrameSize5ms, FrameSize10ms, FrameSize20ms}[config-28]
	} else {
		// SILK or hybrid mode (configs 0-15)
		// For pure CELT implementation, default to fullband 20ms
		p.Bandwidth = BandwidthFullband
		p.FrameSize = FrameSize20ms
	}
}

// ParsePacket parses a complete CELT packet
func ParsePacket(data []byte) (*Packet, error) {
	if len(data) < 1 {
		return nil, errors.New("celt: packet too short")
	}
	
	// Parse TOC byte
	packet, err := ParseTOC(data[0])
	if err != nil {
		return nil, err
	}
	
	// For single frame, the rest is the frame data
	if packet.FrameCount == 1 {
		if len(data) < 2 {
			return nil, errors.New("celt: packet too short for frame data")
		}
		packet.Frames = [][]byte{data[1:]}
		return packet, nil
	}
	
	// For multiple frames, we need to parse frame boundaries
	// This is a simplified version - full implementation would handle CBR/VBR codes
	if len(data) < 2 {
		return nil, errors.New("celt: packet too short for multi-frame")
	}
	
	// Simple equal-length frame split for now
	frameDataLen := len(data) - 1
	frameSizes := make([]int, packet.FrameCount)
	bytesPerFrame := frameDataLen / packet.FrameCount
	
	for i := 0; i < packet.FrameCount; i++ {
		frameSizes[i] = bytesPerFrame
	}
	
	// Handle remainder
	remainder := frameDataLen % packet.FrameCount
	for i := 0; i < remainder; i++ {
		frameSizes[i]++
	}
	
	// Extract frames
	packet.Frames = make([][]byte, packet.FrameCount)
	offset := 1 // Skip TOC byte
	for i := 0; i < packet.FrameCount; i++ {
		if offset+frameSizes[i] > len(data) {
			return nil, fmt.Errorf("celt: invalid frame boundaries")
		}
		packet.Frames[i] = data[offset : offset+frameSizes[i]]
		offset += frameSizes[i]
	}
	
	return packet, nil
}

// Channels returns the number of channels
func (p *Packet) Channels() int {
	if p.Stereo {
		return 2
	}
	return 1
}
