// Package opus provides a stateful, Pure Go implementation of the Opus audio
// codec, including single-stream, multistream, surround, projection, packet
// transformation, packet inspection, PLC, and in-band FEC APIs.
//
// Encoder and decoder instances represent one logical stream and are not safe
// for concurrent use. Callers must process packets in order and serialize all
// operations on an instance, including getters, controls, and Reset. Distinct
// instances may be used concurrently. Instances must not be copied after first
// use.
//
// Methods borrow caller-provided PCM, packet, and destination slices only for
// the duration of a call. Returned packet and PCM slices are owned by the
// caller. The codec implementation has no runtime CGO dependency; optional
// reference-comparison tests use libopus only under the opusref build tag.
package opus

//go:generate go run ./internal/cmd/genversion -version VERSION -out version_gen.go

// Sample rates supported by the public encoder and decoder constructors.
const (
	SampleRate8kHz  = 8000
	SampleRate12kHz = 12000
	SampleRate16kHz = 16000
	SampleRate24kHz = 24000
	SampleRate48kHz = 48000
)

// Frame sizes in samples per channel at 48 kHz. At other sample rates, use the
// proportionally scaled sample count for the same duration.
const (
	FrameSize2_5ms = 120  // 2.5ms at 48kHz
	FrameSize5ms   = 240  // 5ms at 48kHz
	FrameSize10ms  = 480  // 10ms at 48kHz
	FrameSize20ms  = 960  // 20ms at 48kHz (most common)
	FrameSize40ms  = 1920 // 40ms at 48kHz
	FrameSize60ms  = 2880 // 60ms at 48kHz
	FrameSize80ms  = 3840 // 80ms at 48kHz
	FrameSize100ms = 4800 // 100ms at 48kHz
	FrameSize120ms = 5760 // 120ms at 48kHz (maximum packet duration)
)

// ExpertFrameDuration selects a fixed encoder packet duration. Argument keeps
// the default behavior of deriving the duration from Encode's frameSize.
type ExpertFrameDuration int

// Expert frame-duration control values.
const (
	// ExpertFrameDurationArgument derives packet duration from each Encode
	// call's frameSize argument.
	ExpertFrameDurationArgument ExpertFrameDuration = 5000
	// The remaining values select the named fixed packet duration.
	ExpertFrameDuration2_5ms ExpertFrameDuration = 5001
	ExpertFrameDuration5ms   ExpertFrameDuration = 5002
	ExpertFrameDuration10ms  ExpertFrameDuration = 5003
	ExpertFrameDuration20ms  ExpertFrameDuration = 5004
	ExpertFrameDuration40ms  ExpertFrameDuration = 5005
	ExpertFrameDuration60ms  ExpertFrameDuration = 5006
	ExpertFrameDuration80ms  ExpertFrameDuration = 5007
	ExpertFrameDuration100ms ExpertFrameDuration = 5008
	ExpertFrameDuration120ms ExpertFrameDuration = 5009
)

// Encoder application modes.
const (
	// ApplicationVOIP tunes for speech and permits SILK and hybrid routing.
	ApplicationVOIP = 2048
	// ApplicationAudio tunes for general audio.
	ApplicationAudio = 2049
	// ApplicationRestrictedLowDelay keeps encoding on the low-delay CELT path.
	ApplicationRestrictedLowDelay = 2051
)

// Coded bandwidth selections. BandwidthAuto is accepted by SetBandwidth to
// restore automatic selection; SetMaxBandwidth requires an explicit tier.
const (
	BandwidthAuto          = -1000 // automatic selection (default)
	BandwidthNarrowband    = 1101  // 4kHz
	BandwidthMediumband    = 1102  // 6kHz
	BandwidthWideband      = 1103  // 8kHz
	BandwidthSuperWideband = 1104  // 12kHz
	BandwidthFullband      = 1105  // 20kHz
)

// Stream channel selections. ChannelsAuto is accepted by SetForceChannels;
// constructors require an explicit mono or stereo channel count.
const (
	ChannelsAuto   = -1000
	ChannelsMono   = 1
	ChannelsStereo = 2
)

// Decoder gain is expressed in Q8 dB, matching OPUS_SET_GAIN.
const (
	GainQ8Min = -32768
	GainQ8Max = 32767
)

// Encoder input precision hints accepted by SetLSBDepth. The current encoder
// retains this setting for CTL parity but does not use it in codec decisions.
const (
	LSBDepthMin     = 8
	LSBDepthMax     = 24
	LSBDepthDefault = 24
)

// Packet coding modes returned by PacketGetMode.
const (
	ModeSILKOnly = 1000
	ModeHybrid   = 1001
	ModeCELTOnly = 1002
)

// Bitrate policy sentinels and libopus-compatible nominal CTL limits.
// Encoder.SetBitrate currently accepts numeric rates from 6000 through 510000
// bits per second; multistream wrappers apply their documented aggregate bounds.
const (
	BitrateAuto   = -1000
	BitrateMax    = -1
	BitrateMin    = 500    // 500 bps
	BitrateMaxVal = 512000 // 512 kbps
)

// Libopus-compatible numeric CTL request constants. This package exposes typed
// control methods rather than a generic request API; these values are provided
// for parity and protocol integration, not as method arguments.
const (
	SetBitrateRequest                = 4002
	GetBitrateRequest                = 4003
	SetForceChannelsRequest          = 4022
	GetForceChannelsRequest          = 4023
	SetMaxBandwidthRequest           = 4004
	GetMaxBandwidthRequest           = 4005
	SetBandwidthRequest              = 4008
	GetBandwidthRequest              = 4009
	SetComplexityRequest             = 4010
	GetComplexityRequest             = 4011
	SetInbandFECRequest              = 4012
	GetInbandFECRequest              = 4013
	SetPacketLossPercRequest         = 4014
	GetPacketLossPercRequest         = 4015
	SetDTXRequest                    = 4016
	GetDTXRequest                    = 4017
	SetVBRRequest                    = 4006
	GetVBRRequest                    = 4007
	SetVBRConstraintRequest          = 4020
	GetVBRConstraintRequest          = 4021
	SetSignalRequest                 = 4024
	GetSignalRequest                 = 4025
	SetApplicationRequest            = 4000
	GetApplicationRequest            = 4001
	GetLookaheadRequest              = 4027
	SetExpertFrameDurationRequest    = 4040
	GetExpertFrameDurationRequest    = 4041
	SetPredictionDisabledRequest     = 4042
	GetPredictionDisabledRequest     = 4043
	SetPhaseInversionDisabledRequest = 4046
	GetPhaseInversionDisabledRequest = 4047
	ResetStateRequest                = 4028
)

// Complexity bounds and the EncoderProfileLibopus default. NewEncoder's legacy
// profile uses complexity 5.
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

// Public single-stream size limits.
const (
	// MaxFrameSize is the maximum decoded packet duration in samples per
	// channel at 48 kHz (120 ms).
	MaxFrameSize = FrameSize120ms

	// MaxFrameBytes is the RFC 6716 maximum compressed payload size of one
	// Opus frame.
	MaxFrameBytes = 1275

	// MaxPacketFrames is the maximum number of frames in one Opus packet.
	MaxPacketFrames = 48

	// MaxPacketSize is a conservative storage bound for an unpadded
	// single-stream Opus packet: up to two framing bytes plus MaxFrameBytes
	// for each frame. Explicit SetPacketPadding can produce larger packets.
	MaxPacketSize = (MaxFrameBytes + 2) * MaxPacketFrames
)
