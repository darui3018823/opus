package opus

import (
	"fmt"
	"math"
)

const (
	// MappingFamilyProjection identifies RFC 8486 family 3: ACN/SN3D
	// Ambisonics represented through a mixing/demixing matrix pair.
	MappingFamilyProjection = 3
)

// ProjectionEncoder encodes RFC 8486 mapping family 2 or 3 Ambisonics.
// The packet payload is an ordinary Opus multistream packet; family, stream
// counts, mapping, demixing matrix, and matrix gain belong in container or
// signalling metadata. It is stateful and owns its elementary encoders; calls
// through StreamEncoder must be serialized with parent operations.
type ProjectionEncoder struct {
	sampleRate     int
	channels       int
	mappingFamily  int
	streams        int
	coupledStreams int
	mapping        []byte
	mixing         *MappingMatrix
	demixing       *MappingMatrix
	multistream    *MultistreamEncoder
	bitrate        int
}

// NewProjectionEncoder creates an Ambisonics encoder for RFC 8486 mapping
// family 2 or 3. Family 2 supports orders 0 through 14; family 3 uses the
// first- through fifth-order matrices provided by libopus 1.6.1.
//
// As opus_projection_ambisonics_encoder_create, whose size query fails
// before anything else, a family or channel count without a family-3
// matrix returns an error wrapping ErrAllocFail (OPUS_ALLOC_FAIL) as well
// as ErrBadArg; family 2 is a Go extension and only wraps ErrBadArg.
func NewProjectionEncoder(sampleRate, channels, mappingFamily int, application Application) (*ProjectionEncoder, error) {
	if mappingFamily != MappingFamilyAmbisonics {
		// opus_projection_ambisonics_encoder_get_size returns 0.
		if mappingFamily != MappingFamilyProjection {
			return nil, fmt.Errorf("%w: %w: projection supports mapping family 2 or 3", ErrBadArg, ErrAllocFail)
		}
		if _, err := predefinedAmbisonicsMatrices(channels); err != nil {
			return nil, fmt.Errorf("%w: %w", ErrAllocFail, err)
		}
	}
	_, nondiegetic, err := ambisonicsOrder(channels)
	if err != nil {
		return nil, err
	}

	var streams, coupledStreams int
	var mapping []byte
	var mixing, demixing *MappingMatrix
	switch mappingFamily {
	case MappingFamilyAmbisonics:
		coupledStreams = nondiegetic / 2
		streams = channels - coupledStreams
		mapping = ambisonicsFamily2Mapping(channels, coupledStreams)
		mixing, err = identityMappingMatrix(channels)
		if err != nil {
			return nil, err
		}
	case MappingFamilyProjection:
		streams = (channels + 1) / 2
		coupledStreams = channels / 2
		mapping = identityChannelMapping(channels)
		matrices, matrixErr := predefinedAmbisonicsMatrices(channels)
		if matrixErr != nil {
			return nil, matrixErr
		}
		mixing, demixing = matrices.mixing, matrices.demixing
	default:
		return nil, fmt.Errorf("%w: projection supports mapping family 2 or 3", ErrBadArg)
	}

	ms, err := NewMultistreamEncoder(sampleRate, channels, streams, coupledStreams, mapping, application)
	if err != nil {
		return nil, err
	}
	e := &ProjectionEncoder{
		sampleRate:     sampleRate,
		channels:       channels,
		mappingFamily:  mappingFamily,
		streams:        streams,
		coupledStreams: coupledStreams,
		mapping:        append([]byte(nil), mapping...),
		mixing:         mixing,
		demixing:       demixing,
		multistream:    ms,
		bitrate:        BitrateAuto,
	}
	if mappingFamily == MappingFamilyAmbisonics {
		ms.mappingType = msMappingAmbisonics
	}
	ms.policyChanged = e.configureForPolicy
	e.configureStreams()
	if ms.libopusPolicy {
		e.configureForPolicy()
	}
	return e, nil
}

// NewProjectionEncoderWithProfile is NewProjectionEncoder with an explicit
// defaults profile; see NewMultistreamEncoderWithProfile.
// EncoderProfileLibopus gives the defaults of
// opus_projection_ambisonics_encoder_create: automatic bitrate,
// complexity 9 and constrained VBR.
func NewProjectionEncoderWithProfile(sampleRate, channels, mappingFamily int, application Application, profile EncoderProfile) (*ProjectionEncoder, error) {
	if err := validateEncoderProfile(profile); err != nil {
		return nil, err
	}
	enc, err := NewProjectionEncoder(sampleRate, channels, mappingFamily, application)
	if err != nil {
		return nil, err
	}
	if profile == EncoderProfileLibopus {
		if err := enc.SetBitrate(BitrateAuto); err != nil {
			return nil, err
		}
		if err := enc.multistream.applyEncoderProfile(profile); err != nil {
			return nil, err
		}
	}
	return enc, nil
}

// SetModePolicy selects the automatic decision policy; see
// MultistreamEncoder.SetModePolicy. Under ModePolicyLibopus, mapping family
// 3 follows opus_projection_encode* and family 2
// opus_multistream_surround_encoder's ambisonics mapping.
func (e *ProjectionEncoder) SetModePolicy(policy ModePolicy) error {
	return e.multistream.SetModePolicy(policy)
}

// ModePolicy reports the automatic decision policy.
func (e *ProjectionEncoder) ModePolicy() ModePolicy { return e.multistream.ModePolicy() }

// configureForPolicy sets up the elementary streams for the selected
// policy (libopus leaves prediction enabled).
func (e *ProjectionEncoder) configureForPolicy() {
	if !e.multistream.libopusPolicy {
		for _, enc := range e.multistream.encoders {
			enc.libopusForcedMode = -1
		}
		e.configureStreams()
		return
	}
	for _, enc := range e.multistream.encoders {
		enc.SetPredictionDisabled(false)
	}
}

// NewAmbisonicsEncoder is an alias for NewProjectionEncoder.
func NewAmbisonicsEncoder(sampleRate, channels, mappingFamily int, application Application) (*ProjectionEncoder, error) {
	return NewProjectionEncoder(sampleRate, channels, mappingFamily, application)
}

// SampleRate returns the encoder input sample rate in Hz.
func (e *ProjectionEncoder) SampleRate() int { return e.sampleRate }

// Channels returns the number of interleaved Ambisonics input channels.
func (e *ProjectionEncoder) Channels() int { return e.channels }

// MappingFamily returns the configured RFC 8486 mapping family.
func (e *ProjectionEncoder) MappingFamily() int { return e.mappingFamily }

// Streams returns the number of elementary Opus streams.
func (e *ProjectionEncoder) Streams() int { return e.streams }

// CoupledStreams returns the number of stereo elementary streams.
func (e *ProjectionEncoder) CoupledStreams() int { return e.coupledStreams }

// Bitrate returns the configured aggregate bitrate or policy sentinel.
func (e *ProjectionEncoder) Bitrate() int { return e.bitrate }

// FinalRange returns the XOR of all elementary stream final ranges for the
// most recently encoded packet.
func (e *ProjectionEncoder) FinalRange() uint32 { return e.multistream.FinalRange() }

// Mapping returns the RFC 8486 family-2 channel mapping. Family 3 uses a
// demixing matrix instead of a channel mapping table and returns nil.
func (e *ProjectionEncoder) Mapping() []byte {
	if e.mappingFamily == MappingFamilyProjection {
		return nil
	}
	return append([]byte(nil), e.mapping...)
}

// DemixingMatrix returns a copy of the family-3 demixing matrix. Family 2
// has no demixing matrix and returns nil.
func (e *ProjectionEncoder) DemixingMatrix() *MappingMatrix {
	if e.demixing == nil {
		return nil
	}
	matrix, _ := NewMappingMatrix(e.demixing.rows, e.demixing.cols, e.demixing.gain, e.demixing.data)
	return matrix
}

// DemixingMatrixBytes returns the RFC 8486 little-endian matrix payload.
func (e *ProjectionEncoder) DemixingMatrixBytes() []byte {
	if e.demixing == nil {
		return nil
	}
	return e.demixing.Bytes()
}

// DemixingMatrixGain returns the family-3 matrix gain in signed Q8 dB.
func (e *ProjectionEncoder) DemixingMatrixGain() int {
	if e.demixing == nil {
		return 0
	}
	return e.demixing.gain
}

// StreamEncoder returns the stateful elementary encoder at stream. Calls made
// through the returned encoder affect subsequent projection packets.
func (e *ProjectionEncoder) StreamEncoder(stream int) (*Encoder, error) {
	return e.multistream.StreamEncoder(stream)
}

// SetBitrate sets the aggregate bitrate. Ambisonics divides numeric rates
// equally between elementary streams, matching libopus' family-2 policy.
func (e *ProjectionEncoder) SetBitrate(bitrate int) error {
	bitrate, err := clampMultistreamBitrate(bitrate, e.channels)
	if err != nil {
		return err
	}
	e.bitrate = bitrate
	e.multistream.bitrate = bitrate
	return nil
}

// SetVBR applies the VBR setting to every elementary stream.
func (e *ProjectionEncoder) SetVBR(enabled bool) {
	e.multistream.SetVBR(enabled)
}

// SetVBRConstraint applies constrained VBR to every elementary stream.
func (e *ProjectionEncoder) SetVBRConstraint(enabled bool) {
	e.multistream.SetVBRConstraint(enabled)
}

// SetComplexity applies a complexity setting to every elementary stream.
func (e *ProjectionEncoder) SetComplexity(complexity int) error {
	return e.multistream.SetComplexity(complexity)
}

// SetExpertFrameDuration applies a fixed packet duration to every elementary
// stream. Argument restores frameSize-selected durations.
func (e *ProjectionEncoder) SetExpertFrameDuration(duration ExpertFrameDuration) error {
	return e.multistream.SetExpertFrameDuration(duration)
}

// ExpertFrameDuration returns the configured packet-duration selection.
func (e *ProjectionEncoder) ExpertFrameDuration() ExpertFrameDuration {
	return e.multistream.ExpertFrameDuration()
}

// Encode encodes frameSize samples per channel of interleaved int16 PCM.
func (e *ProjectionEncoder) Encode(pcm []int16, frameSize int) ([]byte, error) {
	required := frameSize * e.channels
	if len(pcm) < required {
		return nil, fmt.Errorf("%w: insufficient PCM data: got %d, need %d", ErrBadArg, len(pcm), required)
	}
	floatPCM := make([]float64, required)
	for i := range floatPCM {
		floatPCM[i] = float64(pcm[i]) / 32768
	}
	return e.encodeFloatDepth(floatPCM, frameSize, 16)
}

// Encode24 encodes interleaved signed 24-bit PCM stored in int32 values.
func (e *ProjectionEncoder) Encode24(pcm []int32, frameSize int) ([]byte, error) {
	required := frameSize * e.channels
	if len(pcm) < required {
		return nil, fmt.Errorf("%w: insufficient PCM data: got %d, need %d", ErrBadArg, len(pcm), required)
	}
	floatPCM := make([]float64, required)
	for i := range floatPCM {
		floatPCM[i] = float64(pcm[i]) / 8388608
	}
	return e.EncodeFloat(floatPCM, frameSize)
}

// EncodeFloat32 encodes frameSize samples per channel of interleaved float32 PCM.
func (e *ProjectionEncoder) EncodeFloat32(pcm []float32, frameSize int) ([]byte, error) {
	required := frameSize * e.channels
	if len(pcm) < required {
		return nil, fmt.Errorf("%w: insufficient PCM data: got %d, need %d", ErrBadArg, len(pcm), required)
	}
	floatPCM := make([]float64, required)
	for i := range floatPCM {
		floatPCM[i] = float64(pcm[i])
	}
	return e.EncodeFloat(floatPCM, frameSize)
}

// EncodeFloat encodes frameSize samples per channel of interleaved float64 PCM.
func (e *ProjectionEncoder) EncodeFloat(pcm []float64, frameSize int) ([]byte, error) {
	return e.encodeFloatDepth(pcm, frameSize, 24)
}

// encodeFloatDepth encodes one packet; lsbDepth is the input's sample depth.
func (e *ProjectionEncoder) encodeFloatDepth(pcm []float64, frameSize, lsbDepth int) ([]byte, error) {
	required := frameSize * e.channels
	if len(pcm) < required {
		return nil, fmt.Errorf("%w: insufficient PCM data: got %d, need %d", ErrBadArg, len(pcm), required)
	}
	if len(e.multistream.encoders) == 0 {
		return nil, fmt.Errorf("%w: no projection streams", ErrInvalidState)
	}
	selectedFrameSize, err := e.multistream.selectEncodeFrameSize(frameSize)
	if err != nil {
		return nil, err
	}
	if !e.multistream.libopusPolicy {
		if err := e.prepareRates(selectedFrameSize); err != nil {
			return nil, err
		}
	}
	mixed := pcm[:required]
	if e.mappingFamily == MappingFamilyProjection && e.multistream.libopusPolicy {
		mixed = e.mixing.multiplyFloat32(pcm, selectedFrameSize, e.channels, e.channels)
	} else if e.mappingFamily == MappingFamilyProjection {
		var err error
		mixed, err = e.mixing.multiplyFloat64(mixed, frameSize, e.channels)
		if err != nil {
			return nil, err
		}
	}
	if e.multistream.libopusPolicy {
		// opus_projection_encode*: the streams code the mixed channels, the
		// tonality analysis reads the unmixed input.
		var analysis []float64
		if e.mappingFamily == MappingFamilyProjection {
			analysis = pcm[:selectedFrameSize*e.channels]
		}
		return e.multistream.encodeLibopus(mixed[:selectedFrameSize*e.channels], selectedFrameSize, lsbDepth, analysis)
	}
	return e.multistream.EncodeFloat(mixed, frameSize)
}

// Reset clears all elementary encoder state while retaining configuration.
func (e *ProjectionEncoder) Reset() error {
	return e.multistream.Reset()
}

func (e *ProjectionEncoder) configureStreams() {
	for _, enc := range e.multistream.encoders {
		enc.SetPredictionDisabled(true)
	}
}

func (e *ProjectionEncoder) prepareRates(frameSize int) error {
	switch e.bitrate {
	case BitrateAuto:
		total := (e.streams+e.coupledStreams)*(e.sampleRate+60*e.sampleRate/frameSize) + e.streams*15000
		return e.setEqualStreamRates(total)
	case BitrateMax:
		for _, enc := range e.multistream.encoders {
			if err := enc.SetBitrate(BitrateMax); err != nil {
				return err
			}
		}
		return nil
	default:
		return e.setEqualStreamRates(e.bitrate)
	}
}

func (e *ProjectionEncoder) setEqualStreamRates(total int) error {
	perStream := total / e.streams
	perStream = max(6000, min(510000, perStream))
	for stream, enc := range e.multistream.encoders {
		if err := enc.SetBitrate(perStream); err != nil {
			return fmt.Errorf("stream %d bitrate %d: %w", stream, perStream, err)
		}
	}
	return nil
}

// ProjectionDecoder decodes a multistream packet and applies an RFC 8486
// family-3 demixing matrix. It is stateful and owns its elementary decoders;
// calls through StreamDecoder must be serialized with parent operations.
type ProjectionDecoder struct {
	sampleRate     int
	channels       int
	streams        int
	coupledStreams int
	demixing       *MappingMatrix
	multistream    *MultistreamDecoder
}

// NewProjectionDecoder creates a family-3 decoder from the little-endian Q15
// matrix stored in signalling metadata. The matrix must have channels rows
// and streams+coupledStreams columns. Like opus_projection_decoder_init,
// channels may not exceed streams+coupledStreams; when it is smaller, only
// the first channels coded channels are demixed.
func NewProjectionDecoder(sampleRate, channels, streams, coupledStreams int, demixingMatrix []byte) (*ProjectionDecoder, error) {
	codedChannels := streams + coupledStreams
	if channels > codedChannels {
		return nil, fmt.Errorf("%w: %d output channels exceed %d coded channels", ErrBadArg, channels, codedChannels)
	}
	matrix, err := NewMappingMatrixFromBytes(channels, codedChannels, 0, demixingMatrix)
	if err != nil {
		return nil, err
	}
	mapping := identityChannelMapping(codedChannels)
	ms, err := NewMultistreamDecoder(sampleRate, codedChannels, streams, coupledStreams, mapping)
	if err != nil {
		return nil, err
	}
	return &ProjectionDecoder{
		sampleRate:     sampleRate,
		channels:       channels,
		streams:        streams,
		coupledStreams: coupledStreams,
		demixing:       matrix,
		multistream:    ms,
	}, nil
}

// SampleRate returns the decoder output sample rate in Hz.
func (d *ProjectionDecoder) SampleRate() int { return d.sampleRate }

// Channels returns the number of interleaved demixed output channels.
func (d *ProjectionDecoder) Channels() int { return d.channels }

// Streams returns the number of elementary Opus streams.
func (d *ProjectionDecoder) Streams() int { return d.streams }

// CoupledStreams returns the number of stereo elementary streams.
func (d *ProjectionDecoder) CoupledStreams() int { return d.coupledStreams }

// FinalRange returns the XOR of all elementary stream final ranges for the
// most recently decoded packet.
func (d *ProjectionDecoder) FinalRange() uint32 { return d.multistream.FinalRange() }

// StreamDecoder returns the stateful elementary decoder at stream.
func (d *ProjectionDecoder) StreamDecoder(stream int) (*Decoder, error) {
	return d.multistream.StreamDecoder(stream)
}

// SetGain sets the output gain of every elementary decoder in Q8 dB
// (OPUS_SET_GAIN on the projection decoder).
func (d *ProjectionDecoder) SetGain(gainQ8 int) error { return d.multistream.SetGain(gainQ8) }

// Gain returns the output gain in Q8 dB.
func (d *ProjectionDecoder) Gain() int { return d.multistream.Gain() }

// DecodeFloat decodes and demixes a packet to caller-owned interleaved float64 PCM.
func (d *ProjectionDecoder) DecodeFloat(data []byte) ([]float64, error) {
	coded, err := d.multistream.DecodeFloat(data)
	if err != nil {
		return nil, err
	}
	return d.demixFloat(coded), nil
}

// DecodeFloat32 decodes and demixes a packet to caller-owned interleaved float32 PCM.
func (d *ProjectionDecoder) DecodeFloat32(data []byte) ([]float32, error) {
	return float64sToFloat32s(d.DecodeFloat(data))
}

// Decode decodes and demixes a packet into pcm and returns samples per channel.
// An empty packet conceals len(pcm)/Channels() samples, like DecodePLC.
func (d *ProjectionDecoder) Decode(data []byte, pcm []int16) (int, error) {
	if len(data) == 0 {
		// Like opus_decode with no packet: conceal a buffer's worth.
		return d.DecodePLC(pcm, len(pcm)/d.channels)
	}
	duration, err := multistreamPacketDuration(data, d.streams, d.sampleRate)
	if err != nil {
		return 0, err
	}
	if err := d.checkOutput(len(pcm), duration); err != nil {
		return 0, err
	}
	coded, err := d.multistream.decodeMapped(data, true)
	if err != nil {
		return 0, err
	}
	d.demixInt16(pcm, coded, duration)
	return duration, nil
}

// Decode24 decodes and demixes a packet to signed 24-bit PCM stored in int32.
// An empty packet conceals len(pcm)/Channels() samples, like DecodePLC.
func (d *ProjectionDecoder) Decode24(data []byte, pcm []int32) (int, error) {
	if len(data) == 0 {
		// Like opus_decode24 with no packet: conceal a buffer's worth.
		return d.DecodePLC24(pcm, len(pcm)/d.channels)
	}
	duration, err := multistreamPacketDuration(data, d.streams, d.sampleRate)
	if err != nil {
		return 0, err
	}
	if err := d.checkOutput(len(pcm), duration); err != nil {
		return 0, err
	}
	coded, err := d.multistream.DecodeFloat(data)
	if err != nil {
		return 0, err
	}
	d.demixInt24(pcm, coded, duration)
	return duration, nil
}

// DecodePLC conceals frameSize lost samples per channel on every elementary
// stream and demixes the result (opus_projection_decode with no packet).
func (d *ProjectionDecoder) DecodePLC(pcm []int16, frameSize int) (int, error) {
	frameSize = clampMultistreamFrameSize(frameSize, d.sampleRate)
	if err := d.checkLossOutput(len(pcm), frameSize); err != nil {
		return 0, err
	}
	coded, err := d.multistream.DecodePLCFloat(frameSize)
	if err != nil {
		return 0, err
	}
	d.demixInt16(pcm, coded, frameSize)
	return frameSize, nil
}

// DecodePLC24 is DecodePLC with signed 24-bit output stored in int32.
func (d *ProjectionDecoder) DecodePLC24(pcm []int32, frameSize int) (int, error) {
	frameSize = clampMultistreamFrameSize(frameSize, d.sampleRate)
	if err := d.checkLossOutput(len(pcm), frameSize); err != nil {
		return 0, err
	}
	coded, err := d.multistream.DecodePLCFloat(frameSize)
	if err != nil {
		return 0, err
	}
	d.demixInt24(pcm, coded, frameSize)
	return frameSize, nil
}

// DecodePLCFloat is DecodePLC with float64 output.
func (d *ProjectionDecoder) DecodePLCFloat(frameSize int) ([]float64, error) {
	frameSize = clampMultistreamFrameSize(frameSize, d.sampleRate)
	coded, err := d.multistream.DecodePLCFloat(frameSize)
	if err != nil {
		return nil, err
	}
	return d.demixFloat(coded), nil
}

// DecodePLCFloat32 is DecodePLC with float32 output.
func (d *ProjectionDecoder) DecodePLCFloat32(frameSize int) ([]float32, error) {
	frameSize = clampMultistreamFrameSize(frameSize, d.sampleRate)
	return float64sToFloat32s(d.DecodePLCFloat(frameSize))
}

// DecodeFECWithDuration decodes in-band FEC data for exactly frameSize lost
// samples per channel and demixes it (opus_projection_decode with
// decode_fec set).
func (d *ProjectionDecoder) DecodeFECWithDuration(data []byte, pcm []int16, frameSize int) (int, error) {
	frameSize = clampMultistreamFrameSize(frameSize, d.sampleRate)
	if err := d.checkLossOutput(len(pcm), frameSize); err != nil {
		return 0, err
	}
	coded, err := d.multistream.DecodeFECFloat(data, frameSize)
	if err != nil {
		return 0, err
	}
	d.demixInt16(pcm, coded, frameSize)
	return frameSize, nil
}

// DecodeFEC24 is DecodeFECWithDuration with signed 24-bit output stored in
// int32.
func (d *ProjectionDecoder) DecodeFEC24(data []byte, pcm []int32, frameSize int) (int, error) {
	frameSize = clampMultistreamFrameSize(frameSize, d.sampleRate)
	if err := d.checkLossOutput(len(pcm), frameSize); err != nil {
		return 0, err
	}
	coded, err := d.multistream.DecodeFECFloat(data, frameSize)
	if err != nil {
		return 0, err
	}
	d.demixInt24(pcm, coded, frameSize)
	return frameSize, nil
}

// DecodeFECFloat is DecodeFECWithDuration with float64 output.
func (d *ProjectionDecoder) DecodeFECFloat(data []byte, frameSize int) ([]float64, error) {
	frameSize = clampMultistreamFrameSize(frameSize, d.sampleRate)
	coded, err := d.multistream.DecodeFECFloat(data, frameSize)
	if err != nil {
		return nil, err
	}
	return d.demixFloat(coded), nil
}

// DecodeFECFloat32 is DecodeFECWithDuration with float32 output.
func (d *ProjectionDecoder) DecodeFECFloat32(data []byte, frameSize int) ([]float32, error) {
	frameSize = clampMultistreamFrameSize(frameSize, d.sampleRate)
	return float64sToFloat32s(d.DecodeFECFloat(data, frameSize))
}

func (d *ProjectionDecoder) checkOutput(have, duration int) error {
	if required := duration * d.channels; have < required {
		return fmt.Errorf("%w: got %d samples, need %d", ErrBufferTooSmall, have, required)
	}
	return nil
}

func (d *ProjectionDecoder) checkLossOutput(have, frameSize int) error {
	if !isValidLossFrameSize(frameSize, d.sampleRate) {
		return fmt.Errorf("%w: %w: frameSize %d at %d Hz", ErrBadArg, ErrUnsupportedFrameSize, frameSize, d.sampleRate)
	}
	return d.checkOutput(have, frameSize)
}

// demixedColumns is the number of coded channels that reach the output: the
// projection decoder's multistream layout maps output channel i to coded
// channel i, so only the first Channels() coded channels are demixed.
func (d *ProjectionDecoder) demixedColumns() int {
	return min(d.channels, d.demixing.cols)
}

// demixFloat is mapping_matrix_multiply_channel_out_float: each coded
// channel is accumulated into the output in float32.
func (d *ProjectionDecoder) demixFloat(coded []float64) []float64 {
	cols := d.demixing.cols
	frames := len(coded) / cols
	acc := make([]float32, frames*d.channels)
	for col := 0; col < d.demixedColumns(); col++ {
		for i := 0; i < frames; i++ {
			x := float32(coded[i*cols+col])
			for row := 0; row < d.channels; row++ {
				m := float32(1.0/32768) * float32(d.demixing.data[col*d.demixing.rows+row])
				acc[i*d.channels+row] += float32(m * x)
			}
		}
	}
	out := make([]float64, len(acc))
	for i, v := range acc {
		out[i] = float64(v)
	}
	return out
}

// demixInt16 is mapping_matrix_multiply_channel_out_short: each coded
// sample (soft-clipped for a decoded packet) is quantized to int16 and (m*x + 16384) >> 15
// is accumulated into the int16 output, which wraps rather than saturates.
func (d *ProjectionDecoder) demixInt16(pcm []int16, coded []float64, frames int) {
	out := pcm[:frames*d.channels]
	clear(out)
	cols := d.demixing.cols
	for col := 0; col < d.demixedColumns(); col++ {
		for i := 0; i < frames; i++ {
			x := int32(libopusFloatToInt16(coded[i*cols+col]))
			for row := 0; row < d.channels; row++ {
				tmp := int32(d.demixing.data[col*d.demixing.rows+row]) * x
				j := i*d.channels + row
				out[j] = int16(int32(out[j]) + (tmp+16384)>>15)
			}
		}
	}
}

// demixInt24 is mapping_matrix_multiply_channel_out_int24: RES2INT24 per
// coded sample, int64 products and a wrapping int32 accumulator.
func (d *ProjectionDecoder) demixInt24(pcm []int32, coded []float64, frames int) {
	out := pcm[:frames*d.channels]
	clear(out)
	cols := d.demixing.cols
	for col := 0; col < d.demixedColumns(); col++ {
		for i := 0; i < frames; i++ {
			x := int64(res2Int24(coded[i*cols+col]))
			for row := 0; row < d.channels; row++ {
				tmp := int64(d.demixing.data[col*d.demixing.rows+row]) * x
				j := i*d.channels + row
				out[j] += int32((tmp + 16384) >> 15)
			}
		}
	}
}

func float64sToFloat32s(pcm []float64, err error) ([]float32, error) {
	if err != nil {
		return nil, err
	}
	out := make([]float32, len(pcm))
	for i, v := range pcm {
		out[i] = float32(v)
	}
	return out, nil
}

// Reset clears all elementary decoder state while retaining configuration.
func (d *ProjectionDecoder) Reset() error {
	return d.multistream.Reset()
}

// AmbisonicsDecoder decodes RFC 8486 family 2 or 3.
type AmbisonicsDecoder struct {
	mappingFamily int
	family2       *MultistreamDecoder
	family3       *ProjectionDecoder
}

// NewAmbisonicsDecoder creates a decoder from RFC 8486 signalling fields.
// Family 2 uses mapping and ignores demixingMatrix. Family 3 ignores mapping
// and requires demixingMatrix.
func NewAmbisonicsDecoder(sampleRate, channels, mappingFamily, streams, coupledStreams int, mapping, demixingMatrix []byte) (*AmbisonicsDecoder, error) {
	switch mappingFamily {
	case MappingFamilyAmbisonics:
		if _, _, err := ambisonicsOrder(channels); err != nil {
			return nil, err
		}
		if len(mapping) != channels {
			return nil, fmt.Errorf("%w: family 2 mapping length %d, want %d", ErrBadArg, len(mapping), channels)
		}
		dec, err := NewMultistreamDecoder(sampleRate, channels, streams, coupledStreams, mapping)
		if err != nil {
			return nil, err
		}
		return &AmbisonicsDecoder{mappingFamily: mappingFamily, family2: dec}, nil
	case MappingFamilyProjection:
		dec, err := NewProjectionDecoder(sampleRate, channels, streams, coupledStreams, demixingMatrix)
		if err != nil {
			return nil, err
		}
		return &AmbisonicsDecoder{mappingFamily: mappingFamily, family3: dec}, nil
	default:
		return nil, fmt.Errorf("%w: ambisonics supports mapping family 2 or 3", ErrBadArg)
	}
}

// MappingFamily returns the configured RFC 8486 mapping family.
func (d *AmbisonicsDecoder) MappingFamily() int { return d.mappingFamily }

// DecodeFloat decodes a packet to caller-owned interleaved float64 PCM.
func (d *AmbisonicsDecoder) DecodeFloat(data []byte) ([]float64, error) {
	if d.family3 != nil {
		return d.family3.DecodeFloat(data)
	}
	return d.family2.DecodeFloat(data)
}

// DecodeFloat32 decodes a packet to caller-owned interleaved float32 PCM.
func (d *AmbisonicsDecoder) DecodeFloat32(data []byte) ([]float32, error) {
	if d.family3 != nil {
		return d.family3.DecodeFloat32(data)
	}
	return d.family2.DecodeFloat32(data)
}

// Decode decodes a packet into pcm and returns samples per channel.
func (d *AmbisonicsDecoder) Decode(data []byte, pcm []int16) (int, error) {
	if d.family3 != nil {
		return d.family3.Decode(data, pcm)
	}
	return d.family2.Decode(data, pcm)
}

// Decode24 decodes a packet to signed 24-bit PCM stored in int32.
func (d *AmbisonicsDecoder) Decode24(data []byte, pcm []int32) (int, error) {
	if d.family3 != nil {
		return d.family3.Decode24(data, pcm)
	}
	return d.family2.Decode24(data, pcm)
}

// Reset clears decoder state while retaining configuration.
func (d *AmbisonicsDecoder) Reset() error {
	if d.family3 != nil {
		return d.family3.Reset()
	}
	return d.family2.Reset()
}

// FinalRange returns the XOR of all elementary stream final ranges for the
// most recently decoded packet.
func (d *AmbisonicsDecoder) FinalRange() uint32 {
	if d.family3 != nil {
		return d.family3.FinalRange()
	}
	return d.family2.FinalRange()
}

func ambisonicsOrder(channels int) (orderPlusOne, nondiegeticChannels int, err error) {
	if channels < 1 || channels > 227 {
		return 0, 0, fmt.Errorf("%w: ambisonics channel count %d outside 1..227", ErrBadArg, channels)
	}
	orderPlusOne = int(math.Sqrt(float64(channels)))
	acnChannels := orderPlusOne * orderPlusOne
	nondiegeticChannels = channels - acnChannels
	if nondiegeticChannels != 0 && nondiegeticChannels != 2 {
		return 0, 0, fmt.Errorf("%w: %d channels is not full-order ambisonics with optional non-diegetic stereo", ErrBadArg, channels)
	}
	return orderPlusOne, nondiegeticChannels, nil
}

func ambisonicsFamily2Mapping(channels, coupledStreams int) []byte {
	mapping := make([]byte, channels)
	monoStreams := channels - 2*coupledStreams
	for i := 0; i < monoStreams; i++ {
		mapping[i] = byte(i + 2*coupledStreams)
	}
	for i := 0; i < 2*coupledStreams; i++ {
		mapping[monoStreams+i] = byte(i)
	}
	return mapping
}

func identityChannelMapping(channels int) []byte {
	mapping := make([]byte, channels)
	for i := range mapping {
		mapping[i] = byte(i)
	}
	return mapping
}
