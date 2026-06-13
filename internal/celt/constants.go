// Package celt implements the CELT (Constrained Energy Lapped Transform) codec.
// CELT is the transform-based layer of Opus, optimized for music and general audio.
package celt

import "math"

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
	// Band boundaries from libopus eNBands48000[] = {0,1,2,...,100} (21 bands).
	// BandSizes and BandStart are at LM=0 (2.5ms / 120-sample scale).
	// At LM=3 (20ms), multiply by 8 to get actual MDCT bin counts.
	// Total at LM=0: 100 bins; at LM=3: 800 coded bins out of 960.
	Bands960 = &BandConfig{
		NumBands:  21,
		BandSizes: []int{1, 1, 1, 1, 1, 1, 1, 1, 2, 2, 2, 2, 4, 4, 4, 6, 6, 8, 12, 18, 22},
		BandStart: []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 10, 12, 14, 16, 20, 24, 28, 34, 40, 48, 60, 78},
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
	FrameSize   int         // Frame size in samples at this sample rate
	SampleRate  int         // Sample rate in Hz (8000/12000/16000/24000/48000)
	NBase       int         // Base frame size at LM=0 (= SampleRate/400 for 2.5ms)
	LM          int         // log2(FrameSize/NBase): 0=2.5ms,1=5ms,2=10ms,3=20ms
	Overlap     int         // Overlap size = NBase (fixed per sample rate)
	Channels    int         // Number of channels
	Bands       *BandConfig // Band configuration
}

// celtNumBands returns the number of CELT frequency bands for a given sample rate.
// All rates use a prefix of EBands48000.
func celtNumBands(sampleRate int) int {
	switch {
	case sampleRate <= 8000:
		return 13
	case sampleRate <= 12000:
		return 15
	case sampleRate <= 16000:
		return 17
	case sampleRate <= 24000:
		return 19
	default: // 48000
		return 21
	}
}

// celtNBase returns the 2.5ms frame size for a CELT sample rate.
func celtNBase(sampleRate int) int {
	return sampleRate / 400 // 2.5ms = sampleRate * 0.0025
}

// celtLM returns log2(frameSize/NBase).
func celtLM(frameSize, nBase int) int {
	lm := 0
	for (nBase << uint(lm)) < frameSize {
		lm++
	}
	return lm
}

// NewMode creates a new CELT mode
func NewMode(frameSize, sampleRate, channels int) *Mode {
	return NewModeEx(frameSize, sampleRate, celtNumBands(sampleRate), channels)
}

// NewModeEx creates a CELT mode with an explicit number of bands.
// Use this when the bandwidth is determined by the TOC config rather than sampleRate
// (e.g. NB packet decoded at 48kHz output: frameSize=120, sampleRate=48000, numBands=13).
func NewModeEx(frameSize, sampleRate, numBands, channels int) *Mode {
	nBase := celtNBase(sampleRate)
	if nBase < 1 {
		nBase = 1
	}
	lm := celtLM(frameSize, nBase)
	if numBands < 1 {
		numBands = celtNumBands(sampleRate)
	}
	overlap := nBase // overlap = NBase (fixed per bandwidth)

	bands := buildBandConfig(numBands)

	return &Mode{
		FrameSize:  frameSize,
		SampleRate: sampleRate,
		NBase:      nBase,
		LM:         lm,
		Overlap:    overlap,
		Channels:   channels,
		Bands:      bands,
	}
}

// celtWindow computes the CELT overlap window of length n using the formula from libopus.
// w[i] = sin(π/2 * sin²(π*(i+0.5)/(2*n)))
// For n=120 returns the precomputed Window120 table cast to float32 slice.
func celtWindow(n int) []float32 {
	if n == 120 {
		w := make([]float32, 120)
		copy(w, Window120[:])
		return w
	}
	w := make([]float32, n)
	for i := 0; i < n; i++ {
		x := math.Pi * (float64(i) + 0.5) / (2.0 * float64(n))
		s := math.Sin(x)
		w[i] = float32(math.Sin(math.Pi / 2.0 * s * s))
	}
	return w
}

// buildBandConfig creates a BandConfig from the first numBands entries of EBands48000.
// BandStart and BandSizes are at LM=0 scale.
func buildBandConfig(numBands int) *BandConfig {
	if numBands > len(EBands48000)-1 {
		numBands = len(EBands48000) - 1
	}
	bc := &BandConfig{
		NumBands:  numBands,
		BandSizes: make([]int, numBands),
		BandStart: make([]int, numBands),
	}
	for i := 0; i < numBands; i++ {
		bc.BandStart[i] = int(EBands48000[i])
		bc.BandSizes[i] = int(EBands48000[i+1]) - int(EBands48000[i])
	}
	return bc
}
