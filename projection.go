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
// signalling metadata.
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
func NewProjectionEncoder(sampleRate, channels, mappingFamily int, application Application) (*ProjectionEncoder, error) {
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
	e.configureStreams()
	return e, nil
}

// NewAmbisonicsEncoder is an alias for NewProjectionEncoder.
func NewAmbisonicsEncoder(sampleRate, channels, mappingFamily int, application Application) (*ProjectionEncoder, error) {
	return NewProjectionEncoder(sampleRate, channels, mappingFamily, application)
}

func (e *ProjectionEncoder) SampleRate() int     { return e.sampleRate }
func (e *ProjectionEncoder) Channels() int       { return e.channels }
func (e *ProjectionEncoder) MappingFamily() int  { return e.mappingFamily }
func (e *ProjectionEncoder) Streams() int        { return e.streams }
func (e *ProjectionEncoder) CoupledStreams() int { return e.coupledStreams }
func (e *ProjectionEncoder) Bitrate() int        { return e.bitrate }
func (e *ProjectionEncoder) FinalRange() uint32  { return e.multistream.FinalRange() }

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

func (e *ProjectionEncoder) StreamEncoder(stream int) (*Encoder, error) {
	return e.multistream.StreamEncoder(stream)
}

// SetBitrate sets the aggregate bitrate. Ambisonics divides numeric rates
// equally between elementary streams, matching libopus' family-2 policy.
func (e *ProjectionEncoder) SetBitrate(bitrate int) error {
	if bitrate != BitrateAuto && bitrate != BitrateMax &&
		(bitrate < 6000*e.streams || bitrate > 510000*e.streams) {
		return fmt.Errorf("%w: invalid projection bitrate %d", ErrBadArg, bitrate)
	}
	e.bitrate = bitrate
	return nil
}

func (e *ProjectionEncoder) SetVBR(enabled bool) {
	e.multistream.SetVBR(enabled)
}

func (e *ProjectionEncoder) SetVBRConstraint(enabled bool) {
	e.multistream.SetVBRConstraint(enabled)
}

func (e *ProjectionEncoder) SetComplexity(complexity int) error {
	return e.multistream.SetComplexity(complexity)
}

func (e *ProjectionEncoder) Encode(pcm []int16, frameSize int) ([]byte, error) {
	required := frameSize * e.channels
	if len(pcm) < required {
		return nil, fmt.Errorf("%w: insufficient PCM data: got %d, need %d", ErrBadArg, len(pcm), required)
	}
	floatPCM := make([]float64, required)
	for i := range floatPCM {
		floatPCM[i] = float64(pcm[i]) / 32768
	}
	return e.EncodeFloat(floatPCM, frameSize)
}

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

func (e *ProjectionEncoder) EncodeFloat(pcm []float64, frameSize int) ([]byte, error) {
	required := frameSize * e.channels
	if len(pcm) < required {
		return nil, fmt.Errorf("%w: insufficient PCM data: got %d, need %d", ErrBadArg, len(pcm), required)
	}
	if len(e.multistream.encoders) == 0 {
		return nil, fmt.Errorf("%w: no projection streams", ErrInvalidState)
	}
	if _, err := e.multistream.encoders[0].validateFrameSize(frameSize); err != nil {
		return nil, err
	}
	if err := e.prepareRates(frameSize); err != nil {
		return nil, err
	}
	mixed := pcm[:required]
	if e.mappingFamily == MappingFamilyProjection {
		var err error
		mixed, err = e.mixing.multiplyFloat64(mixed, frameSize, e.channels)
		if err != nil {
			return nil, err
		}
	}
	return e.multistream.EncodeFloat(mixed, frameSize)
}

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
// family-3 demixing matrix.
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
// and streams+coupledStreams columns.
func NewProjectionDecoder(sampleRate, channels, streams, coupledStreams int, demixingMatrix []byte) (*ProjectionDecoder, error) {
	codedChannels := streams + coupledStreams
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

func (d *ProjectionDecoder) SampleRate() int     { return d.sampleRate }
func (d *ProjectionDecoder) Channels() int       { return d.channels }
func (d *ProjectionDecoder) Streams() int        { return d.streams }
func (d *ProjectionDecoder) CoupledStreams() int { return d.coupledStreams }
func (d *ProjectionDecoder) FinalRange() uint32  { return d.multistream.FinalRange() }

func (d *ProjectionDecoder) StreamDecoder(stream int) (*Decoder, error) {
	return d.multistream.StreamDecoder(stream)
}

func (d *ProjectionDecoder) DecodeFloat(data []byte) ([]float64, error) {
	coded, err := d.multistream.DecodeFloat(data)
	if err != nil {
		return nil, err
	}
	frames := len(coded) / d.demixing.cols
	return d.demixing.multiplyFloat64(coded, frames, d.demixing.cols)
}

func (d *ProjectionDecoder) DecodeFloat32(data []byte) ([]float32, error) {
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

func (d *ProjectionDecoder) Decode(data []byte, pcm []int16) (int, error) {
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
		pcm[i] = int16(math.Round(max(-32768, min(32767, sample*32768))))
	}
	return duration, nil
}

func (d *ProjectionDecoder) Decode24(data []byte, pcm []int32) (int, error) {
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
		pcm[i] = int32(math.Round(max(-8388608, min(8388607, sample*8388608))))
	}
	return duration, nil
}

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

func (d *AmbisonicsDecoder) MappingFamily() int { return d.mappingFamily }

func (d *AmbisonicsDecoder) DecodeFloat(data []byte) ([]float64, error) {
	if d.family3 != nil {
		return d.family3.DecodeFloat(data)
	}
	return d.family2.DecodeFloat(data)
}

func (d *AmbisonicsDecoder) DecodeFloat32(data []byte) ([]float32, error) {
	if d.family3 != nil {
		return d.family3.DecodeFloat32(data)
	}
	return d.family2.DecodeFloat32(data)
}

func (d *AmbisonicsDecoder) Decode(data []byte, pcm []int16) (int, error) {
	if d.family3 != nil {
		return d.family3.Decode(data, pcm)
	}
	return d.family2.Decode(data, pcm)
}

func (d *AmbisonicsDecoder) Decode24(data []byte, pcm []int32) (int, error) {
	if d.family3 != nil {
		return d.family3.Decode24(data, pcm)
	}
	return d.family2.Decode24(data, pcm)
}

func (d *AmbisonicsDecoder) Reset() error {
	if d.family3 != nil {
		return d.family3.Reset()
	}
	return d.family2.Reset()
}

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
