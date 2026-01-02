// Package opus provides a Pure Go implementation of the Opus audio codec.
// This implementation is based on the official libopus reference implementation
// and aims for complete compatibility without using CGO.
package opus

// Opus version constants
const (
	Version      = "0.1.0"
	VersionMajor = 0
	VersionMinor = 1
	VersionPatch = 0
)

// Sample rates supported by Opus
const (
	SampleRate8kHz  = 8000
	SampleRate12kHz = 12000
	SampleRate16kHz = 16000
	SampleRate24kHz = 24000
	SampleRate48kHz = 48000
)

// Frame sizes in samples (at 48kHz)
const (
	FrameSize2_5ms = 120  // 2.5ms at 48kHz
	FrameSize5ms   = 240  // 5ms at 48kHz
	FrameSize10ms  = 480  // 10ms at 48kHz
	FrameSize20ms  = 960  // 20ms at 48kHz (most common)
	FrameSize40ms  = 1920 // 40ms at 48kHz
	FrameSize60ms  = 2880 // 60ms at 48kHz
)

// Application types
const (
	ApplicationVOIP               = 2048 // Voice over IP
	ApplicationAudio              = 2049 // General audio
	ApplicationRestrictedLowDelay = 2051 // Lowest latency
)

// Bandwidth types
const (
	BandwidthNarrowband   = 1101 // 4kHz
	BandwidthMediumband   = 1102 // 6kHz
	BandwidthWideband     = 1103 // 8kHz
	BandwidthSuperWideband = 1104 // 12kHz
	BandwidthFullband     = 1105 // 20kHz
)

// Channel modes
const (
	ChannelsMono   = 1
	ChannelsStereo = 2
)

// Opus modes (internal)
const (
	ModeSILKOnly = 1000
	ModeHybrid   = 1001
	ModeCELTOnly = 1002
)

// Bitrate constants
const (
	BitrateAuto   = -1000
	BitrateMax    = -1
	BitrateMin    = 500      // 500 bps
	BitrateMaxVal = 512000   // 512 kbps
)

// Encoder/Decoder control codes (CTL)
const (
	SetBitrateRequest           = 4002
	GetBitrateRequest           = 4003
SetForceChannelsRequest     = 4022
	GetForceChannelsRequest     = 4023
	SetMaxBandwidthRequest      = 4004
	GetMaxBandwidthRequest      = 4005
	SetBandwidthRequest         = 4008
	GetBandwidthRequest         = 4009
	SetComplexityRequest        = 4010
	GetComplexityRequest        = 4011
	SetInbandFECRequest         = 4012
	GetInbandFECRequest         = 4013
	SetPacketLossPercRequest    = 4014
	GetPacketLossPercRequest    = 4015
	SetDTXRequest               = 4016
	GetDTXRequest               = 4017
	SetVBRRequest               = 4006
	GetVBRRequest               = 4007
	SetVBRConstraintRequest     = 4020
	GetVBRConstraintRequest     = 4021
	SetSignalRequest            = 4024
	GetSignalRequest            = 4025
	SetApplicationRequest       = 4000
	GetApplicationRequest       = 4001
	GetLookaheadRequest         = 4027
	SetExpertFrameDurationRequest = 4040
	GetExpertFrameDurationRequest = 4041
	SetPredictionDisabledRequest  = 4042
	GetPredictionDisabledRequest  = 4043
	ResetStateRequest           = 4028
)

// Signal types
const (
	SignalAuto  = -1000
	SignalVoice = 3001
	SignalMusic = 3002
)

// Complexity (0-10)
const (
	ComplexityMin     = 0
	ComplexityMax     = 10
	ComplexityDefault = 9
)

// Packet loss percentage (0-100)
const (
	PacketLossPercMin = 0
	PacketLossPercMax = 100
)

// Maximum packet size
const (
	MaxPacketSize = 1500 // bytes
	MaxFrameSize  = 2880 // samples at 48kHz for 60ms
)
