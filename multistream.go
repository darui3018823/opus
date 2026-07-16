package opus

import (
	"fmt"
	"math"

	framing "github.com/darui3018823/opus/internal"
)

// MultistreamEncoder encodes several elementary Opus streams into one RFC 7845
// multistream packet. Coupled streams precede mono streams.
type MultistreamEncoder struct {
	sampleRate     int
	channels       int
	streams        int
	coupledStreams int
	mapping        []byte
	encoders       []*Encoder
	bitrate        int
}

// NewMultistreamEncoder creates a multistream encoder. mapping maps each input
// channel to a coded channel index, or 255 to omit that channel.
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

// StreamEncoder returns the elementary encoder for stream.
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

// SetVBRConstraint applies constrained VBR to every elementary stream.
func (e *MultistreamEncoder) SetVBRConstraint(enabled bool) {
	for _, enc := range e.encoders {
		enc.SetVBRConstraint(enabled)
	}
}

// SetComplexity applies a complexity setting to every elementary stream.
func (e *MultistreamEncoder) SetComplexity(complexity int) error {
	for _, enc := range e.encoders {
		if err := enc.SetComplexity(complexity); err != nil {
			return err
		}
	}
	return nil
}

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
	return joinMultistreamPackets(packets)
}

// Reset resets every elementary encoder while retaining configuration.
func (e *MultistreamEncoder) Reset() error {
	for _, enc := range e.encoders {
		if err := enc.Reset(); err != nil {
			return err
		}
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

// MultistreamDecoder decodes RFC 7845 multistream packets.
type MultistreamDecoder struct {
	sampleRate     int
	channels       int
	streams        int
	coupledStreams int
	mapping        []byte
	decoders       []*Decoder
}

// NewMultistreamDecoder creates a multistream decoder.
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

func (d *MultistreamDecoder) Streams() int        { return d.streams }
func (d *MultistreamDecoder) CoupledStreams() int { return d.coupledStreams }
func (d *MultistreamDecoder) Channels() int       { return d.channels }
func (d *MultistreamDecoder) SampleRate() int     { return d.sampleRate }

func (d *MultistreamDecoder) Mapping() []byte {
	return append([]byte(nil), d.mapping...)
}

// StreamDecoder returns the elementary decoder for stream.
func (d *MultistreamDecoder) StreamDecoder(stream int) (*Decoder, error) {
	if stream < 0 || stream >= len(d.decoders) {
		return nil, fmt.Errorf("%w: stream index %d", ErrBadArg, stream)
	}
	return d.decoders[stream], nil
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
	if !isValidPacketFrameSize(frameSize, d.sampleRate) {
		return 0, fmt.Errorf("%w: frameSize %d at %d Hz", ErrUnsupportedFrameSize, frameSize, d.sampleRate)
	}
	required := frameSize * d.channels
	if len(pcm) < required {
		return 0, fmt.Errorf("%w: got %d samples, need %d", ErrBufferTooSmall, len(pcm), required)
	}

	// Keep caller-owned PCM untouched unless every elementary stream succeeds.
	for stream, dec := range d.decoders {
		if err := dec.validatePLCState(frameSize); err != nil {
			return 0, fmt.Errorf("stream %d: %w", stream, err)
		}
	}
	out := make([]int16, required)
	for stream, dec := range d.decoders {
		streamChannels := dec.Channels()
		streamPCM := make([]int16, frameSize*streamChannels)
		decoded, err := dec.DecodePLC(streamPCM, frameSize)
		if err != nil {
			return 0, fmt.Errorf("stream %d: %w", stream, err)
		}
		if decoded != frameSize {
			return 0, fmt.Errorf("%w: stream %d decoded %d samples, want %d", ErrInvalidState, stream, decoded, frameSize)
		}

		for outputChannel, mapped := range d.mapping {
			if mapped == 255 {
				continue
			}
			sourceStream, sourceChannel := codedChannelLocation(int(mapped), d.coupledStreams)
			if sourceStream != stream || sourceChannel >= streamChannels {
				continue
			}
			for i := 0; i < frameSize; i++ {
				out[i*d.channels+outputChannel] = streamPCM[i*streamChannels+sourceChannel]
			}
		}
	}

	copy(pcm[:required], out)
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
	required := duration * d.channels
	if len(pcm) < required {
		return 0, fmt.Errorf("%w: got %d samples, need %d", ErrBufferTooSmall, len(pcm), required)
	}

	// Preflight every child before any decoder state advances.
	usePLC := make([]bool, len(packets))
	for stream, packet := range packets {
		info, err := inspectPacket(packet, d.sampleRate)
		if err != nil {
			return 0, fmt.Errorf("stream %d: %w", stream, err)
		}
		usePLC[stream] = info.mode == ModeCELTOnly || d.decoders[stream].prevMode == framing.ModeCELTOnly
		if usePLC[stream] {
			err = d.decoders[stream].validatePLCState(duration)
		} else {
			err = d.decoders[stream].validateFECState(packet)
		}
		if err != nil {
			return 0, fmt.Errorf("stream %d: %w", stream, err)
		}
	}

	// Keep caller-owned PCM untouched unless every elementary stream succeeds.
	out := make([]int16, required)
	for stream, packet := range packets {
		streamChannels := d.decoders[stream].Channels()
		streamPCM := make([]int16, duration*streamChannels)

		var decoded int
		if usePLC[stream] {
			decoded, err = d.decoders[stream].DecodePLC(streamPCM, duration)
		} else {
			decoded, err = d.decoders[stream].DecodeFEC(packet, streamPCM)
		}
		if err != nil {
			return 0, fmt.Errorf("stream %d: %w", stream, err)
		}
		if decoded != duration {
			return 0, fmt.Errorf("%w: stream %d decoded %d samples, want %d", ErrInvalidState, stream, decoded, duration)
		}

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

	copy(pcm[:required], out)
	return duration, nil
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
