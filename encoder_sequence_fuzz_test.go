package opus

import (
	"math"
	"slices"
	"testing"
)

const (
	maxEncoderSequenceInput  = 2 << 10
	maxEncoderSequenceOps    = 12
	encoderSequenceOpWidth   = 6
	maxEncoderSequenceWorkMS = 240
	encoderSequenceSentinel  = int16(-12345)
)

type encoderSequenceCall struct {
	packet []byte
	err    error
}

// FuzzEncoderSequence exercises encoder setters interleaved with adversarial
// PCM input across all public single-stream encode APIs. The oracle is stronger
// than no-panic: identical fresh encoders must produce identical packet bytes,
// errors, and observable state, and every successful packet must be accepted by
// a fresh decoder.
func FuzzEncoderSequence(f *testing.F) {
	f.Add(encoderSequenceSeed(4, 1, 1,
		encoderSequenceControlOp(6, 1, 1, 1, 0, 0),
		encoderSequenceControlOp(5, 9, 0, 0, 0, 0),
		encoderSequenceEncodeOp(3, 8, 0, 0, 0),
	))
	f.Add(encoderSequenceSeed(2, 0, 0,
		encoderSequenceControlOp(8, 0, 4, 1, 0, 0),
		encoderSequenceControlOp(4, 0, 0, 0, 0, 0),
		encoderSequenceControlOp(6, 1, 1, 0, 1, 20),
		encoderSequenceEncodeOp(2, 8, 0, 3, 0),
		encoderSequenceEncodeOp(2, 8, 0, 4, 0),
	))
	f.Add(encoderSequenceSeed(4, 1, 0,
		encoderSequenceControlOp(7, 1, 24, 4, 1, 1),
		encoderSequenceEncodeOp(0, 8, 0, 0, 0),
		encoderSequenceControlOp(9, 0, 0, 0, 0, 0),
		encoderSequenceEncodeOp(1, 8, 0, 0, 0),
	))

	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) < 4 || len(data) > maxEncoderSequenceInput {
			return
		}
		rates := [...]int{8000, 12000, 16000, 24000, 48000}
		applications := [...]Application{ApplicationVOIP, ApplicationAudio, ApplicationRestrictedLowDelay}
		rate := rates[int(data[0])%len(rates)]
		channels := 1 + int(data[1]&1)
		application := applications[int(data[2])%len(applications)]
		profile := EncoderProfileLegacy
		if data[3]&1 != 0 {
			profile = EncoderProfileLibopus
		}
		a, err := NewEncoderWithProfile(rate, channels, application, profile)
		if err != nil {
			t.Fatalf("NewEncoderWithProfile A: %v", err)
		}
		b, err := NewEncoderWithProfile(rate, channels, application, profile)
		if err != nil {
			t.Fatalf("NewEncoderWithProfile B: %v", err)
		}
		encoderSequenceReplay(t, data[4:], rate, channels, a, b)
	})
}

func encoderSequenceReplay(t *testing.T, data []byte, rate, channels int, a, b *Encoder) {
	t.Helper()
	if len(data) == 0 {
		return
	}
	ops := 1 + int(data[0])%maxEncoderSequenceOps
	data = data[1:]
	if maxOps := len(data) / encoderSequenceOpWidth; ops > maxOps {
		ops = maxOps
	}
	descriptors := data[:ops*encoderSequenceOpWidth]
	payload := data[ops*encoderSequenceOpWidth:]
	workBudget := rate * maxEncoderSequenceWorkMS / 1000
	workUsed := 0
	for operation := 0; operation < ops; operation++ {
		desc := descriptors[operation*encoderSequenceOpWidth:][:encoderSequenceOpWidth]
		tag := desc[0] % 10
		if tag <= 3 {
			frameSize := encoderSequenceFrameSize(desc[1], rate)
			cost := encoderSequenceEncodeCost(frameSize, rate)
			if workUsed+cost > workBudget {
				continue
			}
			workUsed += cost
			encoderSequenceCompareEncode(t, tag, desc, payload, frameSize, rate, channels, a, b)
		} else {
			encoderSequenceApplyControl(t, tag, desc, a, b)
		}
		encoderSequenceCompareState(t, rate, channels, a, b)
	}
}

func encoderSequenceCompareEncode(t *testing.T, tag byte, desc, payload []byte, frameSize, rate, channels int, a, b *Encoder) {
	t.Helper()
	callA := encoderSequenceEncodeCall(tag, desc, payload, frameSize, channels, a)
	callB := encoderSequenceEncodeCall(tag, desc, payload, frameSize, channels, b)
	encoderSequenceCompareError(t, callA.err, callB.err)
	if !slices.Equal(callA.packet, callB.packet) {
		t.Fatalf("non-deterministic encode tag=%d frameSize=%d: %x / %x", tag, frameSize, callA.packet, callB.packet)
	}
	if callA.err != nil {
		if len(callA.packet) != 0 {
			t.Fatalf("encode returned packet with error: len=%d err=%v", len(callA.packet), callA.err)
		}
		return
	}
	if len(callA.packet) == 0 {
		t.Fatal("successful encode returned an empty packet")
	}
	samples, err := PacketGetNumSamples(callA.packet, rate)
	if err != nil {
		t.Fatalf("successful encode produced invalid packet duration: %v packet=%x", err, callA.packet)
	}
	if samples <= 0 || samples > MaxFrameSize*rate/48000 {
		t.Fatalf("successful encode produced invalid sample count %d", samples)
	}
	dec, err := NewDecoder(rate, channels)
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	pcm := make([]int16, samples*channels+8)
	for i := range pcm {
		pcm[i] = encoderSequenceSentinel
	}
	n, err := dec.Decode(callA.packet, pcm)
	if err != nil {
		t.Fatalf("fresh decoder rejected successful encode packet: %v packet=%x", err, callA.packet)
	}
	if n != samples {
		t.Fatalf("decode returned %d samples, packet duration is %d", n, samples)
	}
	for i := n * channels; i < len(pcm); i++ {
		if pcm[i] != encoderSequenceSentinel {
			t.Fatalf("decode modified guard pcm[%d]=%d", i, pcm[i])
		}
	}
}

func encoderSequenceEncodeCall(tag byte, desc, payload []byte, frameSize, channels int, enc *Encoder) encoderSequenceCall {
	samples := encoderSequencePCMSamples(desc, frameSize, channels)
	switch tag {
	case 0:
		return encoderSequenceCallPacket(enc.Encode(encoderSequenceInt16PCM(desc, payload, samples), frameSize))
	case 1:
		return encoderSequenceCallPacket(enc.Encode24(encoderSequenceInt32PCM(desc, payload, samples), frameSize))
	case 2:
		return encoderSequenceCallPacket(enc.EncodeFloat(encoderSequenceFloat64PCM(desc, payload, samples), frameSize))
	default:
		return encoderSequenceCallPacket(enc.EncodeFloat32(encoderSequenceFloat32PCM(desc, payload, samples), frameSize))
	}
}

func encoderSequenceCallPacket(packet []byte, err error) encoderSequenceCall {
	if packet != nil {
		packet = append([]byte(nil), packet...)
	}
	return encoderSequenceCall{packet: packet, err: err}
}

func encoderSequenceApplyControl(t *testing.T, tag byte, desc []byte, a, b *Encoder) {
	t.Helper()
	switch tag {
	case 4:
		values := [...]int{BitrateAuto, BitrateMax, 6000, 12000, 24000, 64000, 128000, 510000, 5999, 510001, -2000, int(uint16(desc[1])|uint16(desc[2])<<8) - 45000}
		v := values[int(desc[3])%len(values)]
		encoderSequenceCompareError(t, a.SetBitrate(v), b.SetBitrate(v))
	case 5:
		values := [...]int{ComplexityMin, 1, 5, ComplexityDefault, ComplexityMax, -1, 11, int(int8(desc[1]))}
		v := values[int(desc[2])%len(values)]
		encoderSequenceCompareError(t, a.SetComplexity(v), b.SetComplexity(v))
	case 6:
		a.SetVBR(desc[1]&1 != 0)
		b.SetVBR(desc[1]&1 != 0)
		a.SetVBRConstraint(desc[2]&1 != 0)
		b.SetVBRConstraint(desc[2]&1 != 0)
		a.SetDTX(desc[3]&1 != 0)
		b.SetDTX(desc[3]&1 != 0)
		a.SetInbandFEC(desc[4]&1 != 0)
		b.SetInbandFEC(desc[4]&1 != 0)
		loss := int(int8(desc[5]))
		a.SetPacketLossPerc(loss)
		b.SetPacketLossPerc(loss)
		padding := int(int8(desc[1] ^ desc[5]))
		a.SetPacketPadding(padding)
		b.SetPacketPadding(padding)
	case 7:
		forceValues := [...]int{ChannelsAuto, ChannelsMono, ChannelsStereo, 0, 3, -2}
		force := forceValues[int(desc[1])%len(forceValues)]
		encoderSequenceCompareError(t, a.SetForceChannels(force), b.SetForceChannels(force))
		lsbValues := [...]int{LSBDepthMin, 16, LSBDepthDefault, LSBDepthMin - 1, LSBDepthMax + 1, int(desc[2])}
		lsb := lsbValues[int(desc[2])%len(lsbValues)]
		encoderSequenceCompareError(t, a.SetLSBDepth(lsb), b.SetLSBDepth(lsb))
		expertValues := [...]ExpertFrameDuration{
			ExpertFrameDurationArgument,
			ExpertFrameDuration2_5ms,
			ExpertFrameDuration5ms,
			ExpertFrameDuration10ms,
			ExpertFrameDuration20ms,
			ExpertFrameDuration40ms,
			ExpertFrameDuration60ms,
			ExpertFrameDuration80ms,
			ExpertFrameDuration100ms,
			ExpertFrameDuration120ms,
			ExpertFrameDuration(4999),
			ExpertFrameDuration(5010),
		}
		expert := expertValues[int(desc[3])%len(expertValues)]
		encoderSequenceCompareError(t, a.SetExpertFrameDuration(expert), b.SetExpertFrameDuration(expert))
		a.SetPredictionDisabled(desc[4]&1 != 0)
		b.SetPredictionDisabled(desc[4]&1 != 0)
		a.SetPhaseInversionDisabled(desc[5]&1 != 0)
		b.SetPhaseInversionDisabled(desc[5]&1 != 0)
	case 8:
		bwValues := [...]int{BandwidthAuto, BandwidthNarrowband, BandwidthMediumband, BandwidthWideband, BandwidthSuperWideband, BandwidthFullband, 0, 9999}
		maxBW := bwValues[1+int(desc[1])%(len(bwValues)-1)]
		encoderSequenceCompareError(t, a.SetMaxBandwidth(maxBW), b.SetMaxBandwidth(maxBW))
		forcedBW := bwValues[int(desc[2])%len(bwValues)]
		encoderSequenceCompareError(t, a.SetBandwidth(forcedBW), b.SetBandwidth(forcedBW))
		signals := [...]SignalType{SignalAuto, SignalVoice, SignalMusic, SignalType(desc[3])}
		signal := signals[int(desc[3])%len(signals)]
		a.SetSignalType(signal)
		b.SetSignalType(signal)
		apps := [...]Application{ApplicationVOIP, ApplicationAudio, ApplicationRestrictedLowDelay, Application(0), Application(9999)}
		app := apps[int(desc[4])%len(apps)]
		encoderSequenceCompareError(t, a.SetApplication(app), b.SetApplication(app))
	case 9:
		encoderSequenceCompareError(t, a.Reset(), b.Reset())
		if a.FinalRange() != 0 || a.InDTX() || b.FinalRange() != 0 || b.InDTX() {
			t.Fatalf("invalid state after Reset: A range=%d dtx=%v; B range=%d dtx=%v", a.FinalRange(), a.InDTX(), b.FinalRange(), b.InDTX())
		}
	}
}

func encoderSequenceCompareState(t *testing.T, rate, channels int, a, b *Encoder) {
	t.Helper()
	if a.SampleRate() != rate || b.SampleRate() != rate || a.Channels() != channels || b.Channels() != channels {
		t.Fatalf("encoder identity changed: A %d/%d B %d/%d want %d/%d", a.SampleRate(), a.Channels(), b.SampleRate(), b.Channels(), rate, channels)
	}
	if a.Bitrate() != b.Bitrate() ||
		a.EffectiveBitrate() != b.EffectiveBitrate() ||
		a.Complexity() != b.Complexity() ||
		a.VBR() != b.VBR() ||
		a.VBRConstraint() != b.VBRConstraint() ||
		a.Application() != b.Application() ||
		a.Lookahead() != b.Lookahead() ||
		a.FinalRange() != b.FinalRange() ||
		a.InDTX() != b.InDTX() ||
		a.DTX() != b.DTX() ||
		a.InbandFEC() != b.InbandFEC() ||
		a.PacketLossPerc() != b.PacketLossPerc() ||
		a.ForceChannels() != b.ForceChannels() ||
		a.LSBDepth() != b.LSBDepth() ||
		a.ExpertFrameDuration() != b.ExpertFrameDuration() ||
		a.PredictionDisabled() != b.PredictionDisabled() ||
		a.PhaseInversionDisabled() != b.PhaseInversionDisabled() ||
		a.SignalType() != b.SignalType() ||
		a.MaxBandwidth() != b.MaxBandwidth() ||
		a.Bandwidth() != b.Bandwidth() ||
		a.GetBandwidth() != a.Bandwidth() ||
		b.GetBandwidth() != b.Bandwidth() {
		t.Fatalf("encoder state diverged:\nA=%+v\nB=%+v", encoderSequenceSnapshot(a), encoderSequenceSnapshot(b))
	}
}

type encoderSequenceState struct {
	Bitrate                int
	EffectiveBitrate       int
	Complexity             int
	VBR                    bool
	VBRConstraint          bool
	Application            Application
	Lookahead              int
	FinalRange             uint32
	InDTX                  bool
	DTX                    bool
	InbandFEC              bool
	PacketLossPerc         int
	ForceChannels          int
	LSBDepth               int
	ExpertFrameDuration    ExpertFrameDuration
	PredictionDisabled     bool
	PhaseInversionDisabled bool
	SignalType             SignalType
	MaxBandwidth           int
	Bandwidth              int
}

func encoderSequenceSnapshot(e *Encoder) encoderSequenceState {
	return encoderSequenceState{
		Bitrate:                e.Bitrate(),
		EffectiveBitrate:       e.EffectiveBitrate(),
		Complexity:             e.Complexity(),
		VBR:                    e.VBR(),
		VBRConstraint:          e.VBRConstraint(),
		Application:            e.Application(),
		Lookahead:              e.Lookahead(),
		FinalRange:             e.FinalRange(),
		InDTX:                  e.InDTX(),
		DTX:                    e.DTX(),
		InbandFEC:              e.InbandFEC(),
		PacketLossPerc:         e.PacketLossPerc(),
		ForceChannels:          e.ForceChannels(),
		LSBDepth:               e.LSBDepth(),
		ExpertFrameDuration:    e.ExpertFrameDuration(),
		PredictionDisabled:     e.PredictionDisabled(),
		PhaseInversionDisabled: e.PhaseInversionDisabled(),
		SignalType:             e.SignalType(),
		MaxBandwidth:           e.MaxBandwidth(),
		Bandwidth:              e.Bandwidth(),
	}
}

func encoderSequenceCompareError(t *testing.T, a, b error) {
	t.Helper()
	if (a == nil) != (b == nil) {
		t.Fatalf("errors differ: %v / %v", a, b)
	}
	if a != nil && a.Error() != b.Error() {
		t.Fatalf("errors differ: %v / %v", a, b)
	}
}

func encoderSequenceFrameSize(selector byte, rate int) int {
	durations400 := [...]int{-1, 0, 1, 2, 3, 4, 8, 16, 24, 32, 40, 48, 49}
	duration := durations400[int(selector)%len(durations400)]
	if duration < 0 {
		return duration
	}
	return rate * duration / 400
}

func encoderSequenceEncodeCost(frameSize, rate int) int {
	minCost := rate / 400
	if frameSize <= 0 {
		return minCost
	}
	maxFrameSize := MaxFrameSize * rate / 48000
	if frameSize > maxFrameSize {
		return maxFrameSize
	}
	return frameSize
}

func encoderSequencePCMSamples(desc []byte, frameSize, channels int) int {
	if frameSize <= 0 {
		if desc[2]&1 == 0 {
			return 0
		}
		return int(desc[3]%8) * channels
	}
	base := frameSize * channels
	switch desc[2] % 4 {
	case 0:
		return base
	case 1:
		if base == 0 {
			return 0
		}
		return base - 1
	case 2:
		extra := int(desc[3]%16) * channels
		return min(base+extra, MaxFrameSize*channels+32)
	default:
		return int(desc[3]%8) * channels
	}
}

func encoderSequenceInt16PCM(desc, payload []byte, samples int) []int16 {
	pcm := make([]int16, samples)
	for i := range pcm {
		switch encoderSequenceByte(desc, payload, i) % 8 {
		case 0:
			pcm[i] = 0
		case 1:
			pcm[i] = 32767
		case 2:
			pcm[i] = -32768
		case 3:
			pcm[i] = 1
		case 4:
			pcm[i] = -1
		default:
			lo := uint16(encoderSequenceByte(payload, desc, i*2))
			hi := uint16(encoderSequenceByte(payload, desc, i*2+1))
			pcm[i] = int16(lo | hi<<8)
		}
	}
	return pcm
}

func encoderSequenceInt32PCM(desc, payload []byte, samples int) []int32 {
	pcm := make([]int32, samples)
	for i := range pcm {
		switch encoderSequenceByte(desc, payload, i) % 8 {
		case 0:
			pcm[i] = 0
		case 1:
			pcm[i] = 8388607
		case 2:
			pcm[i] = -8388608
		case 3:
			pcm[i] = math.MaxInt32
		case 4:
			pcm[i] = math.MinInt32
		default:
			v := uint32(encoderSequenceByte(payload, desc, i*4)) |
				uint32(encoderSequenceByte(payload, desc, i*4+1))<<8 |
				uint32(encoderSequenceByte(payload, desc, i*4+2))<<16 |
				uint32(encoderSequenceByte(payload, desc, i*4+3))<<24
			pcm[i] = int32(v)
		}
	}
	return pcm
}

func encoderSequenceFloat64PCM(desc, payload []byte, samples int) []float64 {
	pcm := make([]float64, samples)
	for i := range pcm {
		pcm[i] = encoderSequenceFloatValue(desc, payload, i)
	}
	return pcm
}

func encoderSequenceFloat32PCM(desc, payload []byte, samples int) []float32 {
	pcm := make([]float32, samples)
	for i := range pcm {
		pcm[i] = float32(encoderSequenceFloatValue(desc, payload, i))
	}
	return pcm
}

func encoderSequenceFloatValue(desc, payload []byte, index int) float64 {
	switch encoderSequenceByte(desc, payload, index) % 12 {
	case 0:
		return 0
	case 1:
		return 1
	case 2:
		return -1
	case 3:
		return 1.25
	case 4:
		return -1.25
	case 5:
		return math.Inf(1)
	case 6:
		return math.Inf(-1)
	case 7:
		return math.NaN()
	default:
		b := float64(int8(encoderSequenceByte(payload, desc, index)))
		return b / 64.0
	}
}

func encoderSequenceByte(primary, fallback []byte, index int) byte {
	if len(primary) != 0 {
		return primary[index%len(primary)]
	}
	if len(fallback) != 0 {
		return fallback[index%len(fallback)]
	}
	return 0
}

func encoderSequenceSeed(rateSelector, channelSelector, profileSelector byte, operations ...[encoderSequenceOpWidth]byte) []byte {
	out := []byte{rateSelector, channelSelector, 0, profileSelector, byte(len(operations))}
	for _, op := range operations {
		out = append(out, op[:]...)
	}
	return out
}

func encoderSequenceEncodeOp(apiTag, frameSelector, lengthMode, payloadMode, salt byte) [encoderSequenceOpWidth]byte {
	return [encoderSequenceOpWidth]byte{apiTag, frameSelector, lengthMode, payloadMode, salt, 0}
}

func encoderSequenceControlOp(tag, a, b, c, d, e byte) [encoderSequenceOpWidth]byte {
	return [encoderSequenceOpWidth]byte{tag, a, b, c, d, e}
}
