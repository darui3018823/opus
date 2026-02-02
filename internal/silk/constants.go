// Package silk implements the SILK speech codec for Opus.
// SILK is optimized for voice and low bitrates (6-20 kbps).
package silk

// Sample rate configurations
const (
	SampleRate8kHz  = 8000
	SampleRate12kHz = 12000
	SampleRate16kHz = 16000
	SampleRate24kHz = 24000
)

// Frame size configurations
const (
	FrameSize10ms = 10 // 10 milliseconds
	FrameSize20ms = 20 // 20 milliseconds (default)
	FrameSize40ms = 40 // 40 milliseconds
	FrameSize60ms = 60 // 60 milliseconds
)

// LPC (Linear Predictive Coding) orders
const (
	LPCOrderNB  = 10 // Narrowband (8 kHz)
	LPCOrderMB  = 12 // Mediumband (12 kHz)
	LPCOrderWB  = 16 // Wideband (16 kHz)
	LPCOrderSWB = 18 // Super-wideband (24 kHz)
	MinLPCOrder = 10 // Minimum LPC order
	MaxLPCOrder = 18 // Maximum LPC order
)

// Pitch analysis parameters
const (
	PitchLagMin     = 2   // Minimum pitch lag
	PitchLagMax     = 300 // Maximum pitch lag
	MinPitchLag     = PitchLagMin // Alias for compatibility
	MaxPitchLag     = PitchLagMax // Alias for compatibility
	PitchSubframes  = 4   // Number of subframes for pitch analysis
)

// NLSF (Normalized Line Spectral Frequencies) parameters
const (
	NLSFOrderNB     = 10    // Narrowband NLSF order
	NLSFOrderMB     = 12    // Mediumband NLSF order
	NLSFOrderWB     = 16    // Wideband NLSF order
	NLSFOrderSWB    = 18    // Super-wideband NLSF order
	NLSFStages      = 2     // Number of NLSF quantization stages
	NLSFMinSpacing  = 0.01  // Minimum spacing between NLSF coefficients (radians)
)

// Gain quantization
const (
	GainLevels     = 32    // Number of gain quantization levels
	MaxDeltaGainQ  = 64    // Maximum delta gain quantization
	GainMinDB      = -60.0 // Minimum gain in dB
	GainMaxDB      = 40.0  // Maximum gain in dB
	GainQuantStep  = 0.5   // Gain quantization step in dB
)

// Bandwidth types
type Bandwidth int

const (
	BandwidthNarrowband Bandwidth = iota // 8 kHz
	BandwidthMediumband                  // 12 kHz
	BandwidthWideband                    // 16 kHz
	BandwidthSuperwideband              // 24 kHz
)

// Voice activity detection
const (
	VADThreshold                 = 0.5   // Voice activity threshold
	VADHistorySize               = 5     // Number of frames to keep in history
	VADHangoverFrames            = 3     // Number of frames to keep active after speech
	VADEnergyThresholdDefault    = 0.01  // Default energy threshold
	VADEnergyThresholdMin        = 0.001 // Minimum energy threshold
	VADSpectralFlatnessThreshold = 0.6   // Spectral flatness threshold
	VADZeroCrossingThreshold     = 0.5   // Zero crossing rate threshold
)

// Subframe parameters
const (
	MaxSubframes    = 4   // Maximum number of subframes
	SubframeLength  = 40  // Subframe length in samples (for 8kHz)
)

// Complexity levels
const (
	ComplexityMin = 0
	ComplexityMax = 10
	ComplexityDefault = 5
)
