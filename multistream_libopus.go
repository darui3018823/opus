package opus

import (
	"fmt"

	framing "github.com/darui3018823/opus/internal"
	"github.com/darui3018823/opus/internal/celt"
)

// msMappingType is libopus's MappingType: how opus_multistream_encode_native
// allocates the rate and configures the elementary streams.
type msMappingType int

const (
	// msMappingNone is opus_multistream_encoder_create, mapping families 0
	// and 255, family 1 with one or two channels, and the projection
	// encoder.
	msMappingNone msMappingType = iota
	// msMappingSurround is mapping family 1 with more than two channels.
	msMappingSurround
	// msMappingAmbisonics is mapping family 2.
	msMappingAmbisonics
)

// msFrameTmp is MS_FRAME_TMP, the largest elementary packet (6 x 20 ms).
const msFrameTmp = 6*1275 + 12

// msOutDataBytes stands for the caller's output buffer of
// opus_multistream_encode: the Go API has none, so the packet is only
// limited by the elementary encoders.
const msOutDataBytes = 1275 * 6 * 255

// SetModePolicy selects the automatic decision policy of every elementary
// encoder and of the multistream layer. Under ModePolicyLibopus the packets
// follow libopus 1.6.1's opus_multistream_encode_native: its rate
// allocation between the streams, the per-stream packet budget, and the
// self-delimited framing of the repacketizer.
//
// Call it before the first Encode. Changing the policy after encoding has
// started resets the stream state as Reset does (every setting is kept).
// An unknown policy returns ErrBadArg and leaves the encoder unchanged.
func (e *MultistreamEncoder) SetModePolicy(policy ModePolicy) error {
	switch policy {
	case ModePolicyLegacy, ModePolicyLibopus:
	default:
		return fmt.Errorf("%w: unsupported mode policy %d", ErrBadArg, policy)
	}
	libopus := policy == ModePolicyLibopus
	if libopus == e.libopusPolicy {
		return nil
	}
	started := false
	for _, enc := range e.encoders {
		started = started || enc.streamStarted
	}
	for _, enc := range e.encoders {
		if err := enc.SetModePolicy(policy); err != nil {
			return err
		}
	}
	e.libopusPolicy = libopus
	if e.policyChanged != nil {
		e.policyChanged()
	}
	if started {
		return e.Reset()
	}
	return nil
}

// ModePolicy reports the automatic decision policy.
func (e *MultistreamEncoder) ModePolicy() ModePolicy {
	if e.libopusPolicy {
		return ModePolicyLibopus
	}
	return ModePolicyLegacy
}

// libopusRateAllocation is rate_allocation: surround_rate_allocation (also
// used without a surround layout, with no LFE) or
// ambisonics_rate_allocation, each rate at least 500 b/s. It returns the
// rates and their sum.
func (e *MultistreamEncoder) libopusRateAllocation(frameSize int) ([]int, int) {
	rates := make([]int, e.streams)
	fs := e.sampleRate
	if e.mappingType == msMappingAmbisonics {
		var total int
		switch e.bitrate {
		case BitrateAuto:
			total = (e.coupledStreams+e.streams)*(fs+60*fs/frameSize) + e.streams*15000
		case BitrateMax:
			total = (e.streams + e.coupledStreams) * 750000
		default:
			total = e.bitrate
		}
		for i := range rates {
			rates[i] = total / e.streams
		}
	} else {
		nbLFE := 0
		if e.lfeStream >= 0 {
			nbLFE = 1
		}
		nbCoupled := e.coupledStreams
		nbUncoupled := e.streams - nbCoupled - nbLFE
		nbNormal := 2*nbCoupled + nbUncoupled
		channelOffset := 40 * max(50, fs/frameSize)
		var bitrate int
		switch e.bitrate {
		case BitrateAuto:
			bitrate = nbNormal*(channelOffset+fs+10000) + 8000*nbLFE
		case BitrateMax:
			bitrate = nbNormal*750000 + nbLFE*128000
		default:
			bitrate = e.bitrate
		}
		lfeOffset := min(bitrate/20, 3000) + 15*max(50, fs/frameSize)
		streamOffset := (bitrate - channelOffset*nbNormal - lfeOffset*nbLFE) / nbNormal / 2
		streamOffset = max(0, min(20000, streamOffset))
		const coupledRatio, lfeRatio = 512, 32
		total := (nbUncoupled << 8) + coupledRatio*nbCoupled + nbLFE*lfeRatio
		channelRate := int(256 * int64(bitrate-lfeOffset*nbLFE-streamOffset*(nbCoupled+nbUncoupled)-channelOffset*nbNormal) / int64(total))
		for i := range rates {
			switch {
			case i < e.coupledStreams:
				rates[i] = 2*channelOffset + max(0, streamOffset+(channelRate*coupledRatio>>8))
			case i != e.lfeStream:
				rates[i] = channelOffset + max(0, streamOffset+channelRate)
			default:
				rates[i] = max(0, lfeOffset+(channelRate*lfeRatio>>8))
			}
		}
	}
	sum := 0
	for i := range rates {
		rates[i] = max(rates[i], 500)
		sum += rates[i]
	}
	return rates, sum
}

// encodeLibopus is opus_multistream_encode_native. lsbDepth is the input's
// depth (16 for int16, 24 otherwise); analysisPCM, when not nil, is the
// input the elementary encoders' tonality analysis reads instead of their
// own (the projection encoder's unmixed channels).
func (e *MultistreamEncoder) encodeLibopus(pcm []float64, frameSize, lsbDepth int, analysisPCM []float64) ([]byte, error) {
	fs := e.sampleRate
	vbr := e.encoders[0].rateMode != celt.RateModeCBR
	smallest := e.streams*2 - 1
	if fs/frameSize == 10 {
		smallest += e.streams
	}
	maxDataBytes := msOutDataBytes
	var commit func()
	if e.beforeEncodeFloat != nil {
		var err error
		commit, err = e.beforeEncodeFloat(pcm, frameSize)
		if err != nil {
			return nil, err
		}
	}
	rates, rateSum := e.libopusRateAllocation(frameSize)
	if !vbr {
		switch e.bitrate {
		case BitrateAuto:
			maxDataBytes = min(maxDataBytes, (bitrateToBits(rateSum, fs, frameSize)+4)/8)
		case BitrateMax:
		default:
			maxDataBytes = min(maxDataBytes, max(smallest, (bitrateToBits(e.bitrate, fs, frameSize)+4)/8))
		}
	}
	for s, enc := range e.encoders {
		enc.setLibopusBitrate(rates[s])
		switch e.mappingType {
		case msMappingSurround:
			equivRate := e.bitrate
			if frameSize*50 < fs {
				equivRate -= 60 * (fs/frameSize - 50) * e.channels
			}
			switch {
			case equivRate > 10000*e.channels:
				enc.forcedBandwidth = BandwidthFullband
			case equivRate > 7000*e.channels:
				enc.forcedBandwidth = BandwidthSuperWideband
			case equivRate > 5000*e.channels:
				enc.forcedBandwidth = BandwidthWideband
			default:
				enc.forcedBandwidth = BandwidthNarrowband
			}
			if s < e.coupledStreams {
				// To preserve the spatial image, force stereo CELT on
				// coupled streams.
				enc.libopusForcedMode = framing.ModeCELTOnly
				enc.forceChannels = ChannelsStereo
			}
		case msMappingAmbisonics:
			enc.libopusForcedMode = framing.ModeCELTOnly
		}
	}

	out := make([]byte, 0, 1275)
	last := e.streams - 1
	for s, enc := range e.encoders {
		streamChannels := enc.Channels()
		streamPCM := make([]float64, frameSize*streamChannels)
		var streamAnalysis []float64
		if analysisPCM != nil {
			streamAnalysis = make([]float64, frameSize*streamChannels)
		}
		for codedChannel := 0; codedChannel < streamChannels; codedChannel++ {
			mapped := s*2 + codedChannel
			if s >= e.coupledStreams {
				mapped = s + e.coupledStreams
			}
			inputChannel := findMappedChannel(e.mapping, byte(mapped))
			if inputChannel < 0 {
				return nil, fmt.Errorf("%w: coded channel %d is unmapped", ErrInvalidState, mapped)
			}
			for i := 0; i < frameSize; i++ {
				streamPCM[i*streamChannels+codedChannel] = pcm[i*e.channels+inputChannel]
				if streamAnalysis != nil {
					streamAnalysis[i*streamChannels+codedChannel] = analysisPCM[i*e.channels+inputChannel]
				}
			}
		}
		// Number of bytes left (+ToC); reserve one byte for the last stream
		// and two for the others.
		currMax := maxDataBytes - len(out)
		currMax -= max(0, 2*(e.streams-s-1)-1)
		// For 100 ms, reserve an extra byte per stream for the ToC.
		if fs/frameSize == 10 {
			currMax -= e.streams - s - 1
		}
		currMax = min(currMax, msFrameTmp)
		// The repacketizer adds one or two bytes for self-delimited frames.
		if s != last {
			if currMax > 253 {
				currMax -= 2
			} else {
				currMax--
			}
		}
		if !vbr && s == last {
			enc.setLibopusBitrate(bitsToBitrate(currMax*8, fs, frameSize))
		}
		enc.outDataBytes = currMax
		enc.inputLSBDepth = lsbDepth
		enc.analysisInput = streamAnalysis
		packet, err := enc.encodeFloat(streamPCM, frameSize)
		enc.outDataBytes = 0
		enc.analysisInput = nil
		if err != nil {
			return nil, fmt.Errorf("stream %d: %w", s, err)
		}
		framed, err := msRepacketize(packet, s != last, !vbr && s == last, maxDataBytes-len(out))
		if err != nil {
			return nil, fmt.Errorf("stream %d: %w", s, err)
		}
		out = append(out, framed...)
	}
	if commit != nil {
		commit()
	}
	return out, nil
}

// msRepacketize is opus_repacketizer_cat followed by
// opus_repacketizer_out_range_impl over all frames of packet (no
// extensions): the frames are reframed (dropping the packet's padding),
// self-delimited when selfDelimited, and padded to maxLen when pad.
func msRepacketize(packet []byte, selfDelimited, pad bool, maxLen int) ([]byte, error) {
	if _, err := inspectPacket(packet, SampleRate48kHz); err != nil {
		return nil, err
	}
	toc := packet[0] & 0xfc
	frames, err := splitOpusFrames(packet[1:], int(packet[0]&3))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidPacket, err)
	}
	count := len(frames)
	sizeLen := func(n int) int {
		if n >= 252 {
			return 2
		}
		return 1
	}
	appendSize := func(b []byte, n int) []byte {
		l, _ := encodeOpusFrameLength(n)
		return append(b, l...)
	}
	totSize := 0
	if selfDelimited {
		totSize = sizeLen(len(frames[count-1]))
	}
	var head []byte
	switch count {
	case 1:
		totSize += len(frames[0]) + 1
		head = []byte{toc}
	case 2:
		if len(frames[1]) == len(frames[0]) {
			totSize += 2*len(frames[0]) + 1
			head = []byte{toc | 1}
		} else {
			totSize += len(frames[0]) + len(frames[1]) + 2 + sizeLen(len(frames[0])) - 1
			head = appendSize([]byte{toc | 2}, len(frames[0]))
		}
	}
	if count > 2 || (pad && totSize < maxLen) {
		// Code 3: restart for the padding case.
		totSize = 0
		if selfDelimited {
			totSize = sizeLen(len(frames[count-1]))
		}
		vbr := false
		for i := 1; i < count; i++ {
			vbr = vbr || len(frames[i]) != len(frames[0])
		}
		if vbr {
			totSize += 2
			for i := 0; i < count-1; i++ {
				totSize += sizeLen(len(frames[i])) + len(frames[i])
			}
			totSize += len(frames[count-1])
			head = []byte{toc | 3, byte(count) | 0x80}
		} else {
			totSize += count*len(frames[0]) + 2
			head = []byte{toc | 3, byte(count)}
		}
		padAmount := 0
		if pad {
			padAmount = maxLen - totSize
		}
		if padAmount != 0 {
			head[1] |= 0x40
			nb255 := (padAmount - 1) / 255
			for i := 0; i < nb255; i++ {
				head = append(head, 255)
			}
			head = append(head, byte(padAmount-255*nb255-1))
			totSize += padAmount
		}
		if vbr {
			for i := 0; i < count-1; i++ {
				head = appendSize(head, len(frames[i]))
			}
		}
		if totSize > maxLen {
			return nil, ErrBufferTooSmall
		}
		out := head
		if selfDelimited {
			out = appendSize(out, len(frames[count-1]))
		}
		for _, f := range frames {
			out = append(out, f...)
		}
		// The padding bytes are 0x01 (no extensions).
		for len(out) < totSize {
			out = append(out, 0x01)
		}
		return out, nil
	}
	if totSize > maxLen {
		return nil, ErrBufferTooSmall
	}
	out := head
	if selfDelimited {
		out = appendSize(out, len(frames[count-1]))
	}
	for _, f := range frames {
		out = append(out, f...)
	}
	return out, nil
}
