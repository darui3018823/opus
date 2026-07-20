package opus

import (
	"fmt"
	"math"
)

// MultistreamEncoder encodes several elementary Opus streams into one RFC 7845
// multistream packet. Coupled streams precede mono streams. It owns its
// elementary encoders; all parent and child operations must be serialized.
// Encode frame sizes are samples per channel and PCM is interleaved.
type MultistreamEncoder struct {
	sampleRate        int
	channels          int
	streams           int
	coupledStreams    int
	mapping           []byte
	encoders          []*Encoder
	bitrate           int
	beforeEncodeFloat func(pcm []float64, frameSize int) (commit func(), err error)
	resetPolicy       func()
}

// NewMultistreamEncoder creates a multistream encoder. channels and streams
// must be 1 through 255, coupledStreams must be between zero and streams, and
// mapping must contain one entry per input channel. Entries select one of the
// streams+coupledStreams coded channels or 255 to omit an input; every coded
// channel must be referenced at least once. The mapping is copied.
func NewMultistreamEncoder(sampleRate, channels, streams, coupledStreams int, mapping []byte, application Application) (*MultistreamEncoder, error) {
	if err := validateMultistreamLayout(channels, streams, coupledStreams, mapping, true); err != nil {
		return nil, err
	}
	encoders := make([]*Encoder, streams)
	for stream := 0; stream < streams; stream++ {
		streamChannels := 1
		if stream < coupledStreams {
			streamChannels = 2
		}
		enc, err := NewEncoder(sampleRate, streamChannels, application)
		if err != nil {
			return nil, err
		}
		encoders[stream] = enc
	}
	return &MultistreamEncoder{
		sampleRate:     sampleRate,
		channels:       channels,
		streams:        streams,
		coupledStreams: coupledStreams,
		mapping:        append([]byte(nil), mapping...),
		encoders:       encoders,
		bitrate:        BitrateAuto,
	}, nil
}

// Streams returns the number of elementary Opus streams.
func (e *MultistreamEncoder) Streams() int { return e.streams }

// CoupledStreams returns the number of stereo elementary streams.
func (e *MultistreamEncoder) CoupledStreams() int { return e.coupledStreams }

// Channels returns the number of interleaved input channels.
func (e *MultistreamEncoder) Channels() int { return e.channels }

// SampleRate returns the encoder input sample rate.
func (e *MultistreamEncoder) SampleRate() int { return e.sampleRate }

// Mapping returns a copy of the channel mapping.
func (e *MultistreamEncoder) Mapping() []byte {
	return append([]byte(nil), e.mapping...)
}

// StreamEncoder returns the parent-owned elementary encoder for stream. Direct
// controls affect future multistream packets; calls must be serialized with all
// other parent and child operations.
func (e *MultistreamEncoder) StreamEncoder(stream int) (*Encoder, error) {
	if stream < 0 || stream >= len(e.encoders) {
		return nil, fmt.Errorf("%w: stream index %d", ErrBadArg, stream)
	}
	return e.encoders[stream], nil
}

// SetBitrate sets the aggregate bitrate and distributes it by coded channel
// count. BitrateAuto and BitrateMax are applied to every elementary encoder.
func (e *MultistreamEncoder) SetBitrate(bitrate int) error {
	if bitrate == BitrateAuto || bitrate == BitrateMax {
		for _, enc := range e.encoders {
			if err := enc.SetBitrate(bitrate); err != nil {
				return err
			}
		}
		e.bitrate = bitrate
		return nil
	}
	if bitrate < 6000*e.streams || bitrate > 510000*e.streams {
		return fmt.Errorf("%w: invalid multistream bitrate %d", ErrBadArg, bitrate)
	}
	codedChannels := e.streams + e.coupledStreams
	remaining := bitrate
	for stream, enc := range e.encoders {
		weight := 1
		if stream < e.coupledStreams {
			weight = 2
		}
		streamRate := bitrate * weight / codedChannels
		if stream == len(e.encoders)-1 {
			streamRate = remaining
		}
		if streamRate < 6000 {
			streamRate = 6000
		}
		if streamRate > 510000 {
			streamRate = 510000
		}
		if err := enc.SetBitrate(streamRate); err != nil {
			return err
		}
		remaining -= streamRate
	}
	e.bitrate = bitrate
	return nil
}

// Bitrate returns the configured aggregate bitrate policy.
func (e *MultistreamEncoder) Bitrate() int { return e.bitrate }

// SetVBR applies the VBR setting to every elementary stream.
func (e *MultistreamEncoder) SetVBR(enabled bool) {
	for _, enc := range e.encoders {
		enc.SetVBR(enabled)
	}
}

// VBR returns the VBR setting of the first elementary stream, matching
// libopus multistream GET CTL semantics.
func (e *MultistreamEncoder) VBR() bool { return e.encoders[0].VBR() }

// SetVBRConstraint applies constrained VBR to every elementary stream.
func (e *MultistreamEncoder) SetVBRConstraint(enabled bool) {
	for _, enc := range e.encoders {
		enc.SetVBRConstraint(enabled)
	}
}

// VBRConstraint returns the constrained-VBR setting of the first elementary
// stream, matching libopus multistream GET CTL semantics.
func (e *MultistreamEncoder) VBRConstraint() bool { return e.encoders[0].VBRConstraint() }

// SetComplexity applies a complexity setting to every elementary stream.
func (e *MultistreamEncoder) SetComplexity(complexity int) error {
	for _, enc := range e.encoders {
		if err := enc.SetComplexity(complexity); err != nil {
			return err
		}
	}
	return nil
}

// Complexity returns the complexity of the first elementary stream, matching
// libopus multistream GET CTL semantics.
func (e *MultistreamEncoder) Complexity() int { return e.encoders[0].Complexity() }

// SetApplication applies an application mode to every elementary stream.
func (e *MultistreamEncoder) SetApplication(application Application) error {
	for _, enc := range e.encoders {
		if err := enc.SetApplication(application); err != nil {
			return err
		}
	}
	return nil
}

// Application returns the application of the first elementary stream.
func (e *MultistreamEncoder) Application() Application { return e.encoders[0].Application() }

// SetSignalType applies a signal hint to every elementary stream.
func (e *MultistreamEncoder) SetSignalType(signal SignalType) {
	for _, enc := range e.encoders {
		enc.SetSignalType(signal)
	}
}

// SignalType returns the signal hint of the first elementary stream.
func (e *MultistreamEncoder) SignalType() SignalType { return e.encoders[0].SignalType() }

// SetDTX applies discontinuous transmission to every elementary stream.
func (e *MultistreamEncoder) SetDTX(enabled bool) {
	for _, enc := range e.encoders {
		enc.SetDTX(enabled)
	}
}

// DTX returns the DTX setting of the first elementary stream.
func (e *MultistreamEncoder) DTX() bool { return e.encoders[0].DTX() }

// SetInbandFEC applies in-band FEC to every elementary stream.
func (e *MultistreamEncoder) SetInbandFEC(enabled bool) {
	for _, enc := range e.encoders {
		enc.SetInbandFEC(enabled)
	}
}

// InbandFEC returns the in-band FEC setting of the first elementary stream.
func (e *MultistreamEncoder) InbandFEC() bool { return e.encoders[0].InbandFEC() }

// SetPacketLossPerc applies the expected packet-loss percentage to every
// elementary stream. Values retain the single-stream clamping semantics.
func (e *MultistreamEncoder) SetPacketLossPerc(perc int) {
	for _, enc := range e.encoders {
		enc.SetPacketLossPerc(perc)
	}
}

// PacketLossPerc returns the packet-loss setting of the first elementary
// stream.
func (e *MultistreamEncoder) PacketLossPerc() int { return e.encoders[0].PacketLossPerc() }

// SetLSBDepth applies the input precision hint to every elementary stream.
func (e *MultistreamEncoder) SetLSBDepth(depth int) error {
	for _, enc := range e.encoders {
		if err := enc.SetLSBDepth(depth); err != nil {
			return err
		}
	}
	return nil
}

// LSBDepth returns the input precision hint of the first elementary stream.
func (e *MultistreamEncoder) LSBDepth() int { return e.encoders[0].LSBDepth() }

// SetPredictionDisabled applies predictive-mode disabling to every elementary
// stream.
func (e *MultistreamEncoder) SetPredictionDisabled(disabled bool) {
	for _, enc := range e.encoders {
		enc.SetPredictionDisabled(disabled)
	}
}

// PredictionDisabled returns the setting of the first elementary stream.
func (e *MultistreamEncoder) PredictionDisabled() bool {
	return e.encoders[0].PredictionDisabled()
}

// SetPhaseInversionDisabled applies intensity-stereo phase-inversion disabling
// to every elementary stream.
func (e *MultistreamEncoder) SetPhaseInversionDisabled(disabled bool) {
	for _, enc := range e.encoders {
		enc.SetPhaseInversionDisabled(disabled)
	}
}

// PhaseInversionDisabled returns the setting of the first elementary stream.
func (e *MultistreamEncoder) PhaseInversionDisabled() bool {
	return e.encoders[0].PhaseInversionDisabled()
}

// SetMaxBandwidth applies the automatic-bandwidth ceiling to every elementary
// stream.
func (e *MultistreamEncoder) SetMaxBandwidth(bandwidth int) error {
	for _, enc := range e.encoders {
		if err := enc.SetMaxBandwidth(bandwidth); err != nil {
			return err
		}
	}
	return nil
}

// MaxBandwidth returns the automatic-bandwidth ceiling of the first stream.
func (e *MultistreamEncoder) MaxBandwidth() int { return e.encoders[0].MaxBandwidth() }

// SetBandwidth applies an explicit bandwidth request to every elementary
// stream.
func (e *MultistreamEncoder) SetBandwidth(bandwidth int) error {
	for _, enc := range e.encoders {
		if err := enc.SetBandwidth(bandwidth); err != nil {
			return err
		}
	}
	return nil
}

// Bandwidth returns the bandwidth state of the first elementary stream.
func (e *MultistreamEncoder) Bandwidth() int { return e.encoders[0].Bandwidth() }

// GetBandwidth is an alias for Bandwidth.
func (e *MultistreamEncoder) GetBandwidth() int { return e.Bandwidth() }

// Lookahead returns the lookahead of the first elementary stream, matching
// libopus multistream GET CTL semantics.
func (e *MultistreamEncoder) Lookahead() int { return e.encoders[0].Lookahead() }

// SetExpertFrameDuration applies a fixed packet duration to every elementary
// stream. Argument restores frameSize-selected durations.
func (e *MultistreamEncoder) SetExpertFrameDuration(duration ExpertFrameDuration) error {
	if _, ok := frameSizeForExpertDuration(duration, e.sampleRate, 0); !ok {
		return fmt.Errorf("%w: invalid expert frame duration %d", ErrBadArg, duration)
	}
	for _, enc := range e.encoders {
		if err := enc.SetExpertFrameDuration(duration); err != nil {
			return err
		}
	}
	return nil
}

// ExpertFrameDuration returns the packet-duration selection shared by all
// elementary streams.
func (e *MultistreamEncoder) ExpertFrameDuration() ExpertFrameDuration {
	if len(e.encoders) == 0 {
		return ExpertFrameDurationArgument
	}
	return e.encoders[0].ExpertFrameDuration()
}

func (e *MultistreamEncoder) selectEncodeFrameSize(frameSize int) (int, error) {
	if len(e.encoders) == 0 {
		return 0, fmt.Errorf("%w: no multistream encoders", ErrInvalidState)
	}
	selected, err := e.encoders[0].selectEncodeFrameSize(frameSize)
	if err != nil {
		return 0, fmt.Errorf("stream 0: %w", err)
	}
	for stream := 1; stream < len(e.encoders); stream++ {
		streamSelected, err := e.encoders[stream].selectEncodeFrameSize(frameSize)
		if err != nil {
			return 0, fmt.Errorf("stream %d: %w", stream, err)
		}
		if streamSelected != selected {
			return 0, fmt.Errorf("%w: stream %d selects %d samples, stream 0 selects %d", ErrInvalidState, stream, streamSelected, selected)
		}
	}
	return selected, nil
}

// Encode encodes interleaved int16 PCM.
func (e *MultistreamEncoder) Encode(pcm []int16, frameSize int) ([]byte, error) {
	selectedFrameSize, err := e.selectEncodeFrameSize(frameSize)
	if err != nil {
		return nil, err
	}
	required := frameSize * e.channels
	if len(pcm) < required {
		return nil, fmt.Errorf("%w: insufficient PCM data: got %d, need %d", ErrBadArg, len(pcm), required)
	}
	selected := selectedFrameSize * e.channels
	floatPCM := make([]float64, selected)
	for i := range floatPCM {
		floatPCM[i] = float64(pcm[i]) / 32768
	}
	return e.encodeFloatSelected(floatPCM, selectedFrameSize)
}

// Encode24 encodes interleaved signed 24-bit PCM stored in int32 values.
func (e *MultistreamEncoder) Encode24(pcm []int32, frameSize int) ([]byte, error) {
	selectedFrameSize, err := e.selectEncodeFrameSize(frameSize)
	if err != nil {
		return nil, err
	}
	required := frameSize * e.channels
	if len(pcm) < required {
		return nil, fmt.Errorf("%w: insufficient PCM data: got %d, need %d", ErrBadArg, len(pcm), required)
	}
	selected := selectedFrameSize * e.channels
	floatPCM := make([]float64, selected)
	for i := range floatPCM {
		floatPCM[i] = float64(pcm[i]) / 8388608
	}
	return e.encodeFloatSelected(floatPCM, selectedFrameSize)
}

// EncodeFloat32 encodes interleaved float32 PCM.
func (e *MultistreamEncoder) EncodeFloat32(pcm []float32, frameSize int) ([]byte, error) {
	selectedFrameSize, err := e.selectEncodeFrameSize(frameSize)
	if err != nil {
		return nil, err
	}
	required := frameSize * e.channels
	if len(pcm) < required {
		return nil, fmt.Errorf("%w: insufficient PCM data: got %d, need %d", ErrBadArg, len(pcm), required)
	}
	selected := selectedFrameSize * e.channels
	floatPCM := make([]float64, selected)
	for i := range floatPCM {
		floatPCM[i] = float64(pcm[i])
	}
	return e.encodeFloatSelected(floatPCM, selectedFrameSize)
}

// EncodeFloat encodes interleaved float64 PCM.
func (e *MultistreamEncoder) EncodeFloat(pcm []float64, frameSize int) ([]byte, error) {
	required := frameSize * e.channels
	if len(pcm) < required {
		return nil, fmt.Errorf("%w: insufficient PCM data: got %d, need %d", ErrBadArg, len(pcm), required)
	}
	selectedFrameSize, err := e.selectEncodeFrameSize(frameSize)
	if err != nil {
		return nil, err
	}
	return e.encodeFloatSelected(pcm[:selectedFrameSize*e.channels], selectedFrameSize)
}

func (e *MultistreamEncoder) encodeFloatSelected(pcm []float64, selectedFrameSize int) ([]byte, error) {
	var commitPolicy func()
	if e.beforeEncodeFloat != nil {
		var err error
		commitPolicy, err = e.beforeEncodeFloat(pcm, selectedFrameSize)
		if err != nil {
			return nil, err
		}
	}
	packets := make([][]byte, e.streams)
	for stream, enc := range e.encoders {
		streamChannels := enc.Channels()
		streamPCM := make([]float64, selectedFrameSize*streamChannels)
		for codedChannel := 0; codedChannel < streamChannels; codedChannel++ {
			mapped := stream*2 + codedChannel
			if stream >= e.coupledStreams {
				mapped = stream + e.coupledStreams
			}
			inputChannel := findMappedChannel(e.mapping, byte(mapped))
			if inputChannel < 0 {
				return nil, fmt.Errorf("%w: coded channel %d is unmapped", ErrInvalidState, mapped)
			}
			for i := 0; i < selectedFrameSize; i++ {
				streamPCM[i*streamChannels+codedChannel] = pcm[i*e.channels+inputChannel]
			}
		}
		packet, err := enc.EncodeFloat(streamPCM, selectedFrameSize)
		if err != nil {
			return nil, fmt.Errorf("stream %d: %w", stream, err)
		}
		packets[stream] = packet
	}
	packet, err := joinMultistreamPackets(packets)
	if err != nil {
		return nil, err
	}
	if commitPolicy != nil {
		commitPolicy()
	}
	return packet, nil
}

// Reset resets every elementary encoder while retaining configuration.
func (e *MultistreamEncoder) Reset() error {
	for _, enc := range e.encoders {
		if err := enc.Reset(); err != nil {
			return err
		}
	}
	if e.resetPolicy != nil {
		e.resetPolicy()
	}
	return nil
}

// FinalRange returns the XOR of all elementary stream final ranges.
func (e *MultistreamEncoder) FinalRange() uint32 {
	var final uint32
	for _, enc := range e.encoders {
		final ^= enc.FinalRange()
	}
	return final
}

// MultistreamDecoder decodes RFC 7845 multistream packets. It owns its
// elementary decoders; all parent and child operations must be serialized.
// Integer decode destinations need packetDuration*Channels() interleaved
// values, and integer decode methods return samples per channel.
type MultistreamDecoder struct {
	sampleRate     int
	channels       int
	streams        int
	coupledStreams int
	mapping        []byte
	decoders       []*Decoder
}

// NewMultistreamDecoder creates a multistream decoder. channels and streams
// must be 1 through 255, coupledStreams must be between zero and streams, and
// mapping must contain one entry per output channel. Entries select one of the
// streams+coupledStreams coded channels or 255 for a silent output. The mapping
// is copied.
func NewMultistreamDecoder(sampleRate, channels, streams, coupledStreams int, mapping []byte) (*MultistreamDecoder, error) {
	if err := validateMultistreamLayout(channels, streams, coupledStreams, mapping, false); err != nil {
		return nil, err
	}
	decoders := make([]*Decoder, streams)
	for stream := 0; stream < streams; stream++ {
		streamChannels := 1
		if stream < coupledStreams {
			streamChannels = 2
		}
		dec, err := NewDecoder(sampleRate, streamChannels)
		if err != nil {
			return nil, err
		}
		decoders[stream] = dec
	}
	return &MultistreamDecoder{
		sampleRate:     sampleRate,
		channels:       channels,
		streams:        streams,
		coupledStreams: coupledStreams,
		mapping:        append([]byte(nil), mapping...),
		decoders:       decoders,
	}, nil
}

// Streams returns the number of elementary Opus streams.
func (d *MultistreamDecoder) Streams() int { return d.streams }

// CoupledStreams returns the number of stereo elementary streams.
func (d *MultistreamDecoder) CoupledStreams() int { return d.coupledStreams }

// Channels returns the number of interleaved output channels.
func (d *MultistreamDecoder) Channels() int { return d.channels }

// SampleRate returns the decoder output sample rate in Hz.
func (d *MultistreamDecoder) SampleRate() int { return d.sampleRate }

// Mapping returns a copy of the output channel mapping.
func (d *MultistreamDecoder) Mapping() []byte {
	return append([]byte(nil), d.mapping...)
}

// StreamDecoder returns the parent-owned elementary decoder for stream. Calls
// must be serialized with all other parent and child operations.
func (d *MultistreamDecoder) StreamDecoder(stream int) (*Decoder, error) {
	if stream < 0 || stream >= len(d.decoders) {
		return nil, fmt.Errorf("%w: stream index %d", ErrBadArg, stream)
	}
	return d.decoders[stream], nil
}

// SetGain applies Q8 dB output gain to every elementary decoder.
func (d *MultistreamDecoder) SetGain(gainQ8 int) error {
	for _, dec := range d.decoders {
		if err := dec.SetGain(gainQ8); err != nil {
			return err
		}
	}
	return nil
}

// Gain returns the gain of the first elementary decoder, matching libopus
// multistream GET CTL semantics.
func (d *MultistreamDecoder) Gain() int { return d.decoders[0].Gain() }

// SetPhaseInversionDisabled applies intensity-stereo phase-inversion disabling
// to every elementary decoder.
func (d *MultistreamDecoder) SetPhaseInversionDisabled(disabled bool) {
	for _, dec := range d.decoders {
		dec.SetPhaseInversionDisabled(disabled)
	}
}

// PhaseInversionDisabled returns the setting of the first elementary decoder.
func (d *MultistreamDecoder) PhaseInversionDisabled() bool {
	return d.decoders[0].PhaseInversionDisabled()
}

// Bandwidth returns the last decoded bandwidth of the first elementary stream.
func (d *MultistreamDecoder) Bandwidth() int { return d.decoders[0].Bandwidth() }

// GetBandwidth is an alias for Bandwidth.
func (d *MultistreamDecoder) GetBandwidth() int { return d.Bandwidth() }

// GetLastPacketDuration returns the last decoded duration of the first
// elementary stream in output-rate samples per channel.
func (d *MultistreamDecoder) GetLastPacketDuration() int {
	return d.decoders[0].GetLastPacketDuration()
}

// DecodeFloat decodes a multistream packet to interleaved float64 PCM.
func (d *MultistreamDecoder) DecodeFloat(data []byte) ([]float64, error) {
	packets, duration, err := splitMultistreamPackets(data, d.streams, d.sampleRate)
	if err != nil {
		return nil, err
	}
	out := make([]float64, duration*d.channels)
	for stream, packet := range packets {
		streamPCM, err := d.decoders[stream].DecodeFloat(packet)
		if err != nil {
			return nil, fmt.Errorf("stream %d: %w", stream, err)
		}
		streamChannels := d.decoders[stream].Channels()
		for outputChannel, mapped := range d.mapping {
			if mapped == 255 {
				continue
			}
			sourceStream, sourceChannel := codedChannelLocation(int(mapped), d.coupledStreams)
			if sourceStream != stream || sourceChannel >= streamChannels {
				continue
			}
			for i := 0; i < duration; i++ {
				out[i*d.channels+outputChannel] = streamPCM[i*streamChannels+sourceChannel]
			}
		}
	}
	return out, nil
}

// DecodeFloat32 decodes a multistream packet to interleaved float32 PCM.
func (d *MultistreamDecoder) DecodeFloat32(data []byte) ([]float32, error) {
	pcm, err := d.DecodeFloat(data)
	if err != nil {
		return nil, err
	}
	out := make([]float32, len(pcm))
	for i := range out {
		out[i] = float32(pcm[i])
	}
	return out, nil
}

// Decode decodes a multistream packet to interleaved int16 PCM.
func (d *MultistreamDecoder) Decode(data []byte, pcm []int16) (int, error) {
	duration, err := multistreamPacketDuration(data, d.streams, d.sampleRate)
	if err != nil {
		return 0, err
	}
	required := duration * d.channels
	if len(pcm) < required {
		return 0, fmt.Errorf("%w: got %d samples, need %d", ErrBufferTooSmall, len(pcm), required)
	}
	floatPCM, err := d.DecodeFloat(data)
	if err != nil {
		return 0, err
	}
	for i, sample := range floatPCM {
		scaled := sample * 32768
		if scaled > 32767 {
			scaled = 32767
		} else if scaled < -32768 {
			scaled = -32768
		}
		pcm[i] = int16(math.Round(scaled))
	}
	return duration, nil
}

// DecodePLC performs packet-loss concealment for frameSize samples per channel
// on every elementary stream.
func (d *MultistreamDecoder) DecodePLC(pcm []int16, frameSize int) (int, error) {
	if !isValidLossFrameSize(frameSize, d.sampleRate) {
		return 0, fmt.Errorf("%w: frameSize %d at %d Hz", ErrUnsupportedFrameSize, frameSize, d.sampleRate)
	}
	required := frameSize * d.channels
	if len(pcm) < required {
		return 0, fmt.Errorf("%w: got %d samples, need %d", ErrBufferTooSmall, len(pcm), required)
	}

	floatPCM, err := d.DecodePLCFloat(frameSize)
	if err != nil {
		return 0, err
	}
	floatToInt16(pcm[:required], floatPCM)
	return frameSize, nil
}

// DecodePLCFloat performs packet-loss concealment on every elementary stream
// and returns interleaved float64 PCM.
func (d *MultistreamDecoder) DecodePLCFloat(frameSize int) ([]float64, error) {
	if !isValidLossFrameSize(frameSize, d.sampleRate) {
		return nil, fmt.Errorf("%w: frameSize %d at %d Hz", ErrUnsupportedFrameSize, frameSize, d.sampleRate)
	}
	for stream, dec := range d.decoders {
		if err := dec.validatePLCState(frameSize); err != nil {
			return nil, fmt.Errorf("stream %d: %w", stream, err)
		}
	}
	out := make([]float64, frameSize*d.channels)
	for stream, dec := range d.decoders {
		streamPCM, err := dec.DecodePLCFloat(frameSize)
		if err != nil {
			return nil, fmt.Errorf("stream %d: %w", stream, err)
		}
		d.mapStreamFloat(out, streamPCM, stream, frameSize)
	}
	return out, nil
}

// DecodePLCFloat32 is DecodePLCFloat with float32 output.
func (d *MultistreamDecoder) DecodePLCFloat32(frameSize int) ([]float32, error) {
	pcm, err := d.DecodePLCFloat(frameSize)
	if err != nil {
		return nil, err
	}
	out := make([]float32, len(pcm))
	for i := range out {
		out[i] = float32(pcm[i])
	}
	return out, nil
}

// DecodePLC24 performs packet-loss concealment to signed 24-bit PCM in int32.
func (d *MultistreamDecoder) DecodePLC24(pcm []int32, frameSize int) (int, error) {
	if !isValidLossFrameSize(frameSize, d.sampleRate) {
		return 0, fmt.Errorf("%w: frameSize %d at %d Hz", ErrUnsupportedFrameSize, frameSize, d.sampleRate)
	}
	required := frameSize * d.channels
	if len(pcm) < required {
		return 0, fmt.Errorf("%w: got %d samples, need %d", ErrBufferTooSmall, len(pcm), required)
	}
	floatPCM, err := d.DecodePLCFloat(frameSize)
	if err != nil {
		return 0, err
	}
	floatToInt24(pcm[:required], floatPCM)
	return frameSize, nil
}

// DecodeFEC decodes in-band forward-error-correction data from the multistream
// packet following a loss. Elementary CELT streams, which carry no FEC data,
// are recovered with packet-loss concealment for the shared packet duration.
func (d *MultistreamDecoder) DecodeFEC(data []byte, pcm []int16) (int, error) {
	packets, duration, err := splitMultistreamPackets(data, d.streams, d.sampleRate)
	if err != nil {
		return 0, err
	}
	_ = packets
	return d.DecodeFECWithDuration(data, pcm, duration)
}

// DecodeFECWithDuration decodes recovery data for exactly frameSize lost
// samples per channel on every elementary stream.
func (d *MultistreamDecoder) DecodeFECWithDuration(data []byte, pcm []int16, frameSize int) (int, error) {
	if !isValidLossFrameSize(frameSize, d.sampleRate) {
		return 0, fmt.Errorf("%w: frameSize %d at %d Hz", ErrUnsupportedFrameSize, frameSize, d.sampleRate)
	}
	required := frameSize * d.channels
	if len(pcm) < required {
		return 0, fmt.Errorf("%w: got %d samples, need %d", ErrBufferTooSmall, len(pcm), required)
	}
	floatPCM, err := d.DecodeFECFloat(data, frameSize)
	if err != nil {
		return 0, err
	}
	floatToInt16(pcm[:required], floatPCM)
	return frameSize, nil
}

// DecodeFECFloat decodes recovery data for exactly frameSize lost samples per
// channel and returns interleaved float64 PCM.
func (d *MultistreamDecoder) DecodeFECFloat(data []byte, frameSize int) ([]float64, error) {
	if !isValidLossFrameSize(frameSize, d.sampleRate) {
		return nil, fmt.Errorf("%w: frameSize %d at %d Hz", ErrUnsupportedFrameSize, frameSize, d.sampleRate)
	}
	packets, _, err := splitMultistreamPackets(data, d.streams, d.sampleRate)
	if err != nil {
		return nil, err
	}

	// Preflight every child before any decoder state advances.
	for stream, packet := range packets {
		if err := d.decoders[stream].validateFECState(packet, frameSize); err != nil {
			return nil, fmt.Errorf("stream %d: %w", stream, err)
		}
	}

	staged := make([]*Decoder, len(d.decoders))
	out := make([]float64, frameSize*d.channels)
	for stream, packet := range packets {
		staged[stream], err = d.decoders[stream].cloneState()
		if err != nil {
			return nil, fmt.Errorf("stream %d: %w", stream, err)
		}
		streamPCM, err := staged[stream].DecodeFECFloat(packet, frameSize)
		if err != nil {
			return nil, fmt.Errorf("stream %d: %w", stream, err)
		}
		d.mapStreamFloat(out, streamPCM, stream, frameSize)
	}
	for stream := range staged {
		*d.decoders[stream] = *staged[stream]
	}
	return out, nil
}

// DecodeFECFloat32 is DecodeFECFloat with float32 output.
func (d *MultistreamDecoder) DecodeFECFloat32(data []byte, frameSize int) ([]float32, error) {
	pcm, err := d.DecodeFECFloat(data, frameSize)
	if err != nil {
		return nil, err
	}
	out := make([]float32, len(pcm))
	for i := range out {
		out[i] = float32(pcm[i])
	}
	return out, nil
}

// DecodeFEC24 decodes recovery data to signed 24-bit PCM stored in int32.
func (d *MultistreamDecoder) DecodeFEC24(data []byte, pcm []int32, frameSize int) (int, error) {
	if !isValidLossFrameSize(frameSize, d.sampleRate) {
		return 0, fmt.Errorf("%w: frameSize %d at %d Hz", ErrUnsupportedFrameSize, frameSize, d.sampleRate)
	}
	required := frameSize * d.channels
	if len(pcm) < required {
		return 0, fmt.Errorf("%w: got %d samples, need %d", ErrBufferTooSmall, len(pcm), required)
	}
	floatPCM, err := d.DecodeFECFloat(data, frameSize)
	if err != nil {
		return 0, err
	}
	floatToInt24(pcm[:required], floatPCM)
	return frameSize, nil
}

func (d *MultistreamDecoder) mapStreamFloat(out, streamPCM []float64, stream, duration int) {
	streamChannels := d.decoders[stream].Channels()
	for outputChannel, mapped := range d.mapping {
		if mapped == 255 {
			continue
		}
		sourceStream, sourceChannel := codedChannelLocation(int(mapped), d.coupledStreams)
		if sourceStream != stream || sourceChannel >= streamChannels {
			continue
		}
		for i := 0; i < duration; i++ {
			out[i*d.channels+outputChannel] = streamPCM[i*streamChannels+sourceChannel]
		}
	}
}

// Decode24 decodes a multistream packet to signed 24-bit PCM in int32 values.
func (d *MultistreamDecoder) Decode24(data []byte, pcm []int32) (int, error) {
	duration, err := multistreamPacketDuration(data, d.streams, d.sampleRate)
	if err != nil {
		return 0, err
	}
	required := duration * d.channels
	if len(pcm) < required {
		return 0, fmt.Errorf("%w: got %d samples, need %d", ErrBufferTooSmall, len(pcm), required)
	}
	floatPCM, err := d.DecodeFloat(data)
	if err != nil {
		return 0, err
	}
	for i, sample := range floatPCM {
		scaled := sample * 8388608
		if scaled > 8388607 {
			scaled = 8388607
		} else if scaled < -8388608 {
			scaled = -8388608
		}
		pcm[i] = int32(math.Round(scaled))
	}
	return duration, nil
}

// Reset resets every elementary decoder.
func (d *MultistreamDecoder) Reset() error {
	for _, dec := range d.decoders {
		if err := dec.Reset(); err != nil {
			return err
		}
	}
	return nil
}

// FinalRange returns the XOR of all elementary stream final ranges.
func (d *MultistreamDecoder) FinalRange() uint32 {
	var final uint32
	for _, dec := range d.decoders {
		final ^= dec.FinalRange()
	}
	return final
}

func validateMultistreamLayout(channels, streams, coupledStreams int, mapping []byte, encoder bool) error {
	if channels < 1 || channels > 255 || streams < 1 || streams > 255 ||
		coupledStreams < 0 || coupledStreams > streams ||
		streams+coupledStreams > 255 || len(mapping) != channels {
		return fmt.Errorf("%w: invalid multistream layout", ErrBadArg)
	}
	if encoder && streams+coupledStreams > channels {
		return fmt.Errorf("%w: %d coded channels exceed %d input channels", ErrBadArg, streams+coupledStreams, channels)
	}
	maxMapped := streams + coupledStreams
	for channel, mapped := range mapping {
		if mapped != 255 && int(mapped) >= maxMapped {
			return fmt.Errorf("%w: mapping[%d]=%d exceeds coded channel count %d", ErrBadArg, channel, mapped, maxMapped)
		}
	}
	if encoder {
		for mapped := 0; mapped < maxMapped; mapped++ {
			if findMappedChannel(mapping, byte(mapped)) < 0 {
				return fmt.Errorf("%w: coded channel %d is not mapped", ErrBadArg, mapped)
			}
		}
	}
	return nil
}

func findMappedChannel(mapping []byte, coded byte) int {
	for channel, mapped := range mapping {
		if mapped == coded {
			return channel
		}
	}
	return -1
}

func codedChannelLocation(coded, coupledStreams int) (stream, channel int) {
	if coded < 2*coupledStreams {
		return coded / 2, coded & 1
	}
	return coded - coupledStreams, 0
}

func joinMultistreamPackets(packets [][]byte) ([]byte, error) {
	if len(packets) == 0 {
		return nil, fmt.Errorf("%w: no elementary streams", ErrBadArg)
	}
	var out []byte
	for i, packet := range packets {
		if i == len(packets)-1 {
			out = append(out, packet...)
			continue
		}
		selfDelimited, err := makeSelfDelimitedPacket(packet)
		if err != nil {
			return nil, fmt.Errorf("stream %d: %w", i, err)
		}
		out = append(out, selfDelimited...)
	}
	return out, nil
}

func makeSelfDelimitedPacket(packet []byte) ([]byte, error) {
	if _, err := inspectPacket(packet, 48000); err != nil {
		return nil, err
	}
	toc := packet[0]
	frames, err := splitOpusFrames(packet[1:], int(toc&3))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidPacket, err)
	}
	allEqual := true
	for i := 1; i < len(frames); i++ {
		allEqual = allEqual && len(frames[i]) == len(frames[0])
	}
	vbr := !allEqual
	payload, code, err := packOpusFrames(frames, vbr)
	if err != nil {
		return nil, err
	}
	headerLen := len(payload)
	for _, frame := range frames {
		headerLen -= len(frame)
	}
	lastLength, err := encodeOpusFrameLength(len(frames[len(frames)-1]))
	if err != nil {
		return nil, err
	}
	out := make([]byte, 0, 1+len(payload)+len(lastLength))
	out = append(out, toc&0xFC|byte(code))
	out = append(out, payload[:headerLen]...)
	out = append(out, lastLength...)
	out = append(out, payload[headerLen:]...)
	return out, nil
}

func splitMultistreamPackets(data []byte, streams, sampleRate int) ([][]byte, int, error) {
	if streams < 1 {
		return nil, 0, fmt.Errorf("%w: invalid stream count %d", ErrBadArg, streams)
	}
	packets := make([][]byte, streams)
	offset := 0
	duration := 0
	for stream := 0; stream < streams; stream++ {
		var packet []byte
		var used int
		var err error
		if stream == streams-1 {
			if offset >= len(data) {
				return nil, 0, fmt.Errorf("%w: missing stream %d", ErrInvalidPacket, stream)
			}
			packet = append([]byte(nil), data[offset:]...)
			used = len(data) - offset
		} else {
			packet, used, err = parseSelfDelimitedPacket(data[offset:])
			if err != nil {
				return nil, 0, fmt.Errorf("stream %d: %w", stream, err)
			}
		}
		info, err := inspectPacket(packet, sampleRate)
		if err != nil {
			return nil, 0, fmt.Errorf("stream %d: %w", stream, err)
		}
		if stream == 0 {
			duration = info.totalSamples
		} else if info.totalSamples != duration {
			return nil, 0, fmt.Errorf("%w: stream %d duration %d differs from %d", ErrInvalidPacket, stream, info.totalSamples, duration)
		}
		packets[stream] = packet
		offset += used
	}
	if offset != len(data) {
		return nil, 0, fmt.Errorf("%w: %d trailing bytes", ErrInvalidPacket, len(data)-offset)
	}
	return packets, duration, nil
}

func multistreamPacketDuration(data []byte, streams, sampleRate int) (int, error) {
	_, duration, err := splitMultistreamPackets(data, streams, sampleRate)
	return duration, err
}

// MultistreamPacketGetNumSamples returns a multistream packet's duration in
// samples per channel. Every elementary stream must have the same duration.
func MultistreamPacketGetNumSamples(data []byte, streams, sampleRate int) (int, error) {
	return multistreamPacketDuration(data, streams, sampleRate)
}

func parseSelfDelimitedPacket(data []byte) ([]byte, int, error) {
	if len(data) < 2 {
		return nil, 0, fmt.Errorf("%w: truncated self-delimited packet", ErrInvalidPacket)
	}
	toc := data[0]
	code := int(toc & 3)
	pos := 1
	count := 1
	cbr := false
	padding := 0
	sizes := make([]int, 0, 48)

	switch code {
	case 0:
	case 1:
		count, cbr = 2, true
	case 2:
		count = 2
		n, used, err := parseOpusFrameLength(data[pos:])
		if err != nil {
			return nil, 0, fmt.Errorf("%w: %v", ErrInvalidPacket, err)
		}
		sizes = append(sizes, n)
		pos += used
	case 3:
		if pos >= len(data) {
			return nil, 0, fmt.Errorf("%w: missing frame count", ErrInvalidPacket)
		}
		ch := data[pos]
		pos++
		count = int(ch & 0x3F)
		if count < 1 || count > 48 {
			return nil, 0, fmt.Errorf("%w: invalid frame count %d", ErrInvalidPacket, count)
		}
		if ch&0x40 != 0 {
			for {
				if pos >= len(data) {
					return nil, 0, fmt.Errorf("%w: missing padding count", ErrInvalidPacket)
				}
				p := int(data[pos])
				pos++
				if p == 255 {
					padding += 254
					continue
				}
				padding += p
				break
			}
		}
		cbr = ch&0x80 == 0
		if !cbr {
			for i := 0; i < count-1; i++ {
				n, used, err := parseOpusFrameLength(data[pos:])
				if err != nil {
					return nil, 0, fmt.Errorf("%w: %v", ErrInvalidPacket, err)
				}
				sizes = append(sizes, n)
				pos += used
			}
		}
	}

	last, used, err := parseOpusFrameLength(data[pos:])
	if err != nil {
		return nil, 0, fmt.Errorf("%w: missing self-delimited length: %v", ErrInvalidPacket, err)
	}
	pos += used
	if last > MaxFrameBytes {
		return nil, 0, fmt.Errorf("%w: frame length %d", ErrInvalidPacket, last)
	}
	if cbr {
		sizes = sizes[:0]
		for i := 0; i < count; i++ {
			sizes = append(sizes, last)
		}
	} else {
		sizes = append(sizes, last)
	}
	if len(sizes) != count {
		return nil, 0, fmt.Errorf("%w: got %d frame sizes for %d frames", ErrInvalidPacket, len(sizes), count)
	}
	payloadBytes := 0
	for _, size := range sizes {
		if size < 0 || size > MaxFrameBytes {
			return nil, 0, fmt.Errorf("%w: invalid frame length %d", ErrInvalidPacket, size)
		}
		payloadBytes += size
	}
	consumed := pos + payloadBytes + padding
	if consumed > len(data) {
		return nil, 0, fmt.Errorf("%w: self-delimited packet exceeds input", ErrInvalidPacket)
	}
	frames := make([][]byte, count)
	framePos := pos
	for i, size := range sizes {
		frames[i] = data[framePos : framePos+size]
		framePos += size
	}
	vbr := !cbr
	if count == 1 {
		vbr = false
	}
	payload, canonicalCode, err := packOpusFrames(frames, vbr)
	if err != nil {
		return nil, 0, err
	}
	packet := make([]byte, 1, 1+len(payload))
	packet[0] = toc&0xFC | byte(canonicalCode)
	packet = append(packet, payload...)
	return packet, consumed, nil
}
