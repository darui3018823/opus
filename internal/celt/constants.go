// Package celt implements the CELT (Constrained Energy Lapped Transform) codec.
// CELT is the transform-based layer of Opus, optimized for music and general audio.
package celt

// CELT mode constants
const (
	// Frame sizes (in samples at 48kHz)
	FrameSize2_5ms = 120
	FrameSize5ms   = 240
	FrameSize10ms  = 480
	FrameSize20ms  = 960
	FrameSize40ms  = 1920
	FrameSize60ms  = 2880
)

// Bandwidth configurations
const (
	BandwidthNarrowband    = 0 // 4 kHz
	BandwidthMediumband    = 1 // 6 kHz
	BandwidthWideband      = 2 // 8 kHz
	BandwidthSuperwideband = 3 // 12 kHz
	BandwidthFullband      = 4 // 20 kHz
)

// Number of bands for different configurations
const (
	MaxBands    = 21 // Maximum number of frequency bands
	MaxPitch    = 1024
	MaxPeriod   = 1024
	MinPeriod   = 15
	MaxOverlap  = 120
)

// Quantization constants
const (
	MaxFineEnergy = 7  // Maximum bits for fine energy
	MinSpread     = 0
	MaxSpread     = 3
)

// Band configuration defines how MDCT bins are grouped into bands
type BandConfig struct {
	NumBands  int      // Number of bands
	BandSizes []int    // Number of bins in each band
	BandStart []int    // Starting bin for each band
	Norm      []float64 // Normalization factors
}

// Standard CELT band configurations for different frame sizes
var (
	// Bands for 120-sample frames (2.5ms at 48kHz)
	Bands120 = &BandConfig{
		NumBands:  13,
		BandSizes: []int{1, 1, 1, 1, 1, 1, 1, 1, 2, 2, 2, 3, 4},
		BandStart: []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 10, 12, 14, 17},
	}
	
	// Bands for 240-sample frames (5ms at 48kHz)
	Bands240 = &BandConfig{
		NumBands:  17,
		BandSizes: []int{1, 1, 1, 1, 1, 1, 1, 2, 2, 2, 2, 2, 2, 2, 3, 4, 5},
		BandStart: []int{0, 1, 2, 3, 4, 5, 6, 7, 9, 11, 13, 15, 17, 19, 21, 24, 28},
	}
	
	// Bands for 480-sample frames (10ms at 48kHz)  
	Bands480 = &BandConfig{
		NumBands:  19,
		BandSizes: []int{1, 1, 1, 1, 1, 1, 1, 1, 2, 2, 2, 2, 2, 2, 4, 4, 8, 8, 16},
		BandStart: []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 10, 12, 14, 16, 18, 20, 24, 28, 36, 44},
	}
	
	// Bands for 960-sample frames (20ms at 48kHz) - Most common
	Bands960 = &BandConfig{
		NumBands:  21,
		BandSizes: []int{1, 1, 1, 1, 1, 1, 1, 2, 2, 2, 2, 4, 4, 4, 8, 8, 8, 16, 16, 32, 32},
		BandStart: []int{0, 1, 2, 3, 4, 5, 6, 7, 9, 11, 13, 15, 19, 23, 27, 35, 43, 51, 67, 83, 115},
	}
)

// GetBandConfig returns the appropriate band configuration for a frame size
func GetBandConfig(frameSize int) *BandConfig {
	switch frameSize {
	case FrameSize2_5ms:
		return Bands120
	case FrameSize5ms:
		return Bands240
	case FrameSize10ms:
		return Bands480
	case FrameSize20ms, FrameSize40ms, FrameSize60ms:
		return Bands960
	default:
		return Bands960 // Default to 20ms
	}
}

// Mode represents a CELT coding mode
type Mode struct {
	FrameSize   int         // Frame size in samples
	SampleRate  int         // Sample rate in Hz
	Overlap     int         // Overlap size
	Channels    int         // Number of channels
	Bands       *BandConfig // Band configuration
}

// NewMode creates a new CELT mode
func NewMode(frameSize, sampleRate, channels int) *Mode {
	// Calculate overlap as 1/4 of frame size (typical for CELT)
	overlap := frameSize / 4
	if overlap > MaxOverlap {
		overlap = MaxOverlap
	}
	
	return &Mode{
		FrameSize:  frameSize,
		SampleRate: sampleRate,
		Overlap:    overlap,
		Channels:   channels,
		Bands:      GetBandConfig(frameSize),
	}
}
