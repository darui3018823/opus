package opus

import "fmt"

// Opus channel mapping families used by Ogg Opus and the libopus surround API.
const (
	MappingFamilyMonoStereo = 0
	MappingFamilyVorbis     = 1
	MappingFamilyAmbisonics = 2
	MappingFamilyDiscrete   = 255
)

type surroundLayout struct {
	streams        int
	coupledStreams int
	mapping        []byte
	lfeStream      int
}

var vorbisSurroundLayouts = [...]surroundLayout{
	{1, 0, []byte{0}, -1},
	{1, 1, []byte{0, 1}, -1},
	{2, 1, []byte{0, 2, 1}, -1},
	{2, 2, []byte{0, 1, 2, 3}, -1},
	{3, 2, []byte{0, 4, 1, 2, 3}, -1},
	{4, 2, []byte{0, 4, 1, 2, 3, 5}, 3},
	{4, 3, []byte{0, 4, 1, 2, 3, 5, 6}, 3},
	{5, 3, []byte{0, 6, 1, 2, 3, 4, 5, 7}, 4},
}

// SurroundEncoder adds libopus-style channel layouts and rate allocation to a
// MultistreamEncoder.
type SurroundEncoder struct {
	*MultistreamEncoder
	mappingFamily int
	lfeStream     int
	bitrate       int
}

// NewSurroundEncoder creates an encoder for mapping family 0, 1, or 255.
// Mapping family 1 uses the standard Vorbis channel order for 1 through 8
// channels. Mapping family 255 creates one uncoupled stream per channel.
func NewSurroundEncoder(sampleRate, channels, mappingFamily int, application Application) (*SurroundEncoder, error) {
	layout, err := surroundLayoutFor(channels, mappingFamily)
	if err != nil {
		return nil, err
	}
	ms, err := NewMultistreamEncoder(sampleRate, channels, layout.streams, layout.coupledStreams, layout.mapping, application)
	if err != nil {
		return nil, err
	}
	s := &SurroundEncoder{
		MultistreamEncoder: ms,
		mappingFamily:      mappingFamily,
		lfeStream:          layout.lfeStream,
		bitrate:            BitrateAuto,
	}
	s.configureSurroundStreams()
	return s, nil
}

// MappingFamily returns the configured channel mapping family.
func (e *SurroundEncoder) MappingFamily() int { return e.mappingFamily }

// LFEStream returns the LFE elementary stream index, or -1 when absent.
func (e *SurroundEncoder) LFEStream() int { return e.lfeStream }

// SetBitrate sets the aggregate surround bitrate. It is distributed immediately
// before each encode because libopus' allocation depends on frame duration.
func (e *SurroundEncoder) SetBitrate(bitrate int) error {
	if bitrate != BitrateAuto && bitrate != BitrateMax &&
		(bitrate < 6000*e.streams || bitrate > 510000*e.streams) {
		return fmt.Errorf("%w: invalid surround bitrate %d", ErrBadArg, bitrate)
	}
	e.bitrate = bitrate
	return nil
}

// Bitrate returns the configured aggregate surround bitrate policy.
func (e *SurroundEncoder) Bitrate() int { return e.bitrate }

// Encode encodes frameSize samples per channel of interleaved int16 PCM.
func (e *SurroundEncoder) Encode(pcm []int16, frameSize int) ([]byte, error) {
	if err := e.prepareFrame(frameSize); err != nil {
		return nil, err
	}
	return e.MultistreamEncoder.Encode(pcm, frameSize)
}

// Encode24 encodes interleaved signed 24-bit PCM stored in int32 values.
func (e *SurroundEncoder) Encode24(pcm []int32, frameSize int) ([]byte, error) {
	if err := e.prepareFrame(frameSize); err != nil {
		return nil, err
	}
	return e.MultistreamEncoder.Encode24(pcm, frameSize)
}

// EncodeFloat32 encodes frameSize samples per channel of interleaved float32 PCM.
func (e *SurroundEncoder) EncodeFloat32(pcm []float32, frameSize int) ([]byte, error) {
	if err := e.prepareFrame(frameSize); err != nil {
		return nil, err
	}
	return e.MultistreamEncoder.EncodeFloat32(pcm, frameSize)
}

// EncodeFloat encodes frameSize samples per channel of interleaved float64 PCM.
func (e *SurroundEncoder) EncodeFloat(pcm []float64, frameSize int) ([]byte, error) {
	if err := e.prepareFrame(frameSize); err != nil {
		return nil, err
	}
	return e.MultistreamEncoder.EncodeFloat(pcm, frameSize)
}

func (e *SurroundEncoder) configureSurroundStreams() {
	if e.mappingFamily != MappingFamilyVorbis {
		return
	}
	for stream := 0; stream < e.coupledStreams; stream++ {
		enc := e.encoders[stream]
		enc.SetPredictionDisabled(true)
		_ = enc.SetForceChannels(ChannelsStereo)
	}
}

func (e *SurroundEncoder) prepareFrame(frameSize int) error {
	if len(e.encoders) == 0 {
		return fmt.Errorf("%w: no surround streams", ErrInvalidState)
	}
	selectedFrameSize, err := e.MultistreamEncoder.selectEncodeFrameSize(frameSize)
	if err != nil {
		return err
	}
	rates := e.allocateRates(selectedFrameSize)
	for stream, rate := range rates {
		if err := e.encoders[stream].SetBitrate(rate); err != nil {
			return fmt.Errorf("stream %d bitrate %d: %w", stream, rate, err)
		}
	}
	if e.mappingFamily == MappingFamilyVorbis {
		e.applySurroundBandwidth(selectedFrameSize)
	}
	return nil
}

func (e *SurroundEncoder) allocateRates(frameSize int) []int {
	rates := make([]int, e.streams)
	if e.mappingFamily == MappingFamilyDiscrete {
		return equalStreamRates(e.bitrate, e.streams, e.sampleRate, frameSize)
	}

	nbLFE := 0
	if e.lfeStream >= 0 {
		nbLFE = 1
	}
	nbCoupled := e.coupledStreams
	nbUncoupled := e.streams - nbCoupled - nbLFE
	nbNormal := 2*nbCoupled + nbUncoupled
	frameRate := e.sampleRate / frameSize
	if frameRate < 50 {
		frameRate = 50
	}
	channelOffset := 40 * frameRate

	totalBitrate := e.bitrate
	switch totalBitrate {
	case BitrateAuto:
		totalBitrate = nbNormal*(channelOffset+e.sampleRate+10000) + 8000*nbLFE
	case BitrateMax:
		totalBitrate = nbNormal*510000 + 128000*nbLFE
	}
	if nbNormal == 0 {
		nbNormal = 1
	}
	lfeOffset := min(totalBitrate/20, 3000) + 15*frameRate
	streamOffset := (totalBitrate - channelOffset*nbNormal - lfeOffset*nbLFE) / nbNormal / 2
	streamOffset = max(0, min(20000, streamOffset))
	const (
		coupledRatio = 512
		lfeRatio     = 32
	)
	totalWeight := (nbUncoupled << 8) + coupledRatio*nbCoupled + nbLFE*lfeRatio
	channelRate := 0
	if totalWeight > 0 {
		channelRate = 256 * (totalBitrate - lfeOffset*nbLFE - streamOffset*(nbCoupled+nbUncoupled) - channelOffset*nbNormal) / totalWeight
	}
	for stream := 0; stream < e.streams; stream++ {
		switch {
		case stream < nbCoupled:
			rates[stream] = 2*channelOffset + max(0, streamOffset+(channelRate*coupledRatio>>8))
		case stream == e.lfeStream:
			rates[stream] = max(0, lfeOffset+(channelRate*lfeRatio>>8))
		default:
			rates[stream] = channelOffset + max(0, streamOffset+channelRate)
		}
		rates[stream] = max(6000, min(510000, rates[stream]))
	}
	return rates
}

func equalStreamRates(bitrate, streams, sampleRate, frameSize int) []int {
	rates := make([]int, streams)
	total := bitrate
	switch total {
	case BitrateAuto:
		total = streams * (sampleRate + 60*sampleRate/frameSize + 15000)
	case BitrateMax:
		total = streams * 510000
	}
	remaining := total
	for i := range rates {
		rates[i] = total / streams
		if i == streams-1 {
			rates[i] = remaining
		}
		rates[i] = max(6000, min(510000, rates[i]))
		remaining -= rates[i]
	}
	return rates
}

func (e *SurroundEncoder) applySurroundBandwidth(frameSize int) {
	equivalentRate := e.bitrate
	if equivalentRate == BitrateAuto {
		equivalentRate = (e.streams + e.coupledStreams) * (e.sampleRate + 60*e.sampleRate/frameSize + 15000)
	} else if equivalentRate == BitrateMax {
		equivalentRate = 510000 * e.channels
	}
	if frameSize*50 < e.sampleRate {
		equivalentRate -= 60 * (e.sampleRate/frameSize - 50) * e.channels
	}
	bandwidth := BandwidthNarrowband
	switch {
	case equivalentRate > 10000*e.channels:
		bandwidth = BandwidthFullband
	case equivalentRate > 7000*e.channels:
		bandwidth = BandwidthSuperWideband
	case equivalentRate > 5000*e.channels:
		bandwidth = BandwidthWideband
	}
	for _, enc := range e.encoders {
		_ = enc.SetBandwidth(bandwidth)
	}
}

// SurroundDecoder is a MultistreamDecoder initialized from a standard channel
// mapping family.
type SurroundDecoder struct {
	*MultistreamDecoder
	mappingFamily int
	lfeStream     int
}

// NewSurroundDecoder creates a decoder for mapping family 0, 1, or 255.
func NewSurroundDecoder(sampleRate, channels, mappingFamily int) (*SurroundDecoder, error) {
	layout, err := surroundLayoutFor(channels, mappingFamily)
	if err != nil {
		return nil, err
	}
	ms, err := NewMultistreamDecoder(sampleRate, channels, layout.streams, layout.coupledStreams, layout.mapping)
	if err != nil {
		return nil, err
	}
	return &SurroundDecoder{
		MultistreamDecoder: ms,
		mappingFamily:      mappingFamily,
		lfeStream:          layout.lfeStream,
	}, nil
}

// MappingFamily returns the configured Ogg Opus channel mapping family.
func (d *SurroundDecoder) MappingFamily() int { return d.mappingFamily }

// LFEStream returns the elementary stream carrying the LFE channel, or -1
// when the selected layout has no LFE channel.
func (d *SurroundDecoder) LFEStream() int { return d.lfeStream }

func surroundLayoutFor(channels, mappingFamily int) (surroundLayout, error) {
	switch mappingFamily {
	case MappingFamilyMonoStereo:
		if channels == 1 {
			return surroundLayout{1, 0, []byte{0}, -1}, nil
		}
		if channels == 2 {
			return surroundLayout{1, 1, []byte{0, 1}, -1}, nil
		}
		return surroundLayout{}, fmt.Errorf("%w: mapping family 0 requires one or two channels", ErrBadArg)
	case MappingFamilyVorbis:
		if channels < 1 || channels > len(vorbisSurroundLayouts) {
			return surroundLayout{}, fmt.Errorf("%w: mapping family 1 supports one through eight channels", ErrBadArg)
		}
		layout := vorbisSurroundLayouts[channels-1]
		layout.mapping = append([]byte(nil), layout.mapping...)
		return layout, nil
	case MappingFamilyDiscrete:
		if channels < 1 || channels > 255 {
			return surroundLayout{}, fmt.Errorf("%w: invalid discrete channel count %d", ErrBadArg, channels)
		}
		mapping := make([]byte, channels)
		for i := range mapping {
			mapping[i] = byte(i)
		}
		return surroundLayout{channels, 0, mapping, -1}, nil
	case MappingFamilyAmbisonics:
		return surroundLayout{}, fmt.Errorf("%w: mapping family 2 belongs to the projection API", ErrUnimplemented)
	default:
		return surroundLayout{}, fmt.Errorf("%w: unsupported mapping family %d", ErrBadArg, mappingFamily)
	}
}
