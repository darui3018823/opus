package opus

import (
	"fmt"
	"math"
	"slices"
	"testing"
)

const (
	maxDecoderSequenceInput  = 4 << 10
	maxDecoderSequenceOps    = 16
	maxDecoderSequencePacket = 512
	maxDecoderSequenceWorkMS = 240
	decoderSequenceOpWidth   = 4
	decoderSequenceSentinel  = int16(-23457)
)

type decoderSequenceOp struct {
	desc    [decoderSequenceOpWidth]byte
	payload []byte
}

func FuzzDecoderSequence(f *testing.F) {
	f.Add(decoderSequenceSeed(4, 0,
		decoderSequencePacketOp(0, []byte{0xf8, 0xff, 0xfe}),
		decoderSequencePLCOp(5), decoderSequenceGainOp(0x0100), decoderSequencePhaseOp(true), decoderSequenceResetOp(), decoderSequencePLCOp(5),
	))
	f.Add(decoderSequenceSeed(2, 1,
		decoderSequencePacketOp(0, []byte{0x00, 0x00}),
		decoderSequencePacketOp(1, []byte{0x7f}),
		decoderSequencePLCOp(6), decoderSequenceResetOp(),
	))
	f.Add([]byte{4, 1, 0, 0xff, 0x07, 0xfc, 0, 1, 2, 3, 4})

	// Seed realistic SILK/FEC state transitions from the Pure Go encoder.
	seed, err := makeDecoderFECSequenceSeed()
	if err != nil {
		f.Fatal(err)
	}
	f.Add(seed)
	seed, err = makeDecoderHybridSequenceSeed()
	if err != nil {
		f.Fatal(err)
	}
	f.Add(seed)

	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) < 2 || len(data) > maxDecoderSequenceInput {
			return
		}
		rates := [...]int{8000, 12000, 16000, 24000, 48000}
		rate := rates[int(data[0])%len(rates)]
		channels := 1 + int(data[1]&1)
		a, err := NewDecoder(rate, channels)
		if err != nil {
			t.Fatal(err)
		}
		b, err := NewDecoder(rate, channels)
		if err != nil {
			t.Fatal(err)
		}
		decoderSequenceReplay(t, data[2:], rate, channels, a, b)
	})
}

func decoderSequenceReplay(t *testing.T, data []byte, rate, channels int, a, b *Decoder) {
	t.Helper()
	if len(data) == 0 {
		return
	}
	ops := 1 + int(data[0])%maxDecoderSequenceOps
	data = data[1:]
	if maxOps := len(data) / decoderSequenceOpWidth; ops > maxOps {
		ops = maxOps
	}
	descriptors := data[:ops*decoderSequenceOpWidth]
	payload := data[ops*decoderSequenceOpWidth:]
	workBudget := rate * maxDecoderSequenceWorkMS / 1000
	workUsed := 0
	for operation := 0; operation < ops; operation++ {
		desc := descriptors[operation*decoderSequenceOpWidth:][:decoderSequenceOpWidth]
		tag := desc[0] % 6
		switch tag {
		case 0, 1:
			length := (int(desc[1]) | int(desc[2])<<8) % (maxDecoderSequencePacket + 1)
			if length > len(payload) {
				return
			}
			packet := payload[:length]
			payload = payload[length:]
			cost := decoderSequenceCallCost(tag, packet, 0, rate)
			if workUsed+cost > workBudget {
				continue
			}
			workUsed += cost
			decoderSequenceCompareCall(t, tag, packet, 0, rate, channels, a, b)
		case 2:
			frameSize := decoderSequencePLCFrameSize(desc[1], rate)
			cost := decoderSequenceCallCost(tag, nil, frameSize, rate)
			if workUsed+cost > workBudget {
				continue
			}
			workUsed += cost
			decoderSequenceCompareCall(t, tag, nil, frameSize, rate, channels, a, b)
		case 3:
			errA, errB := a.Reset(), b.Reset()
			decoderSequenceCompareError(t, errA, errB)
			if errA == nil {
				if a.FinalRange() != 0 || a.Pitch() != 0 || a.Bandwidth() != BandwidthAuto || a.GetLastPacketDuration() != rate/50 {
					t.Fatalf("invalid state after Reset: range=%d pitch=%d bandwidth=%d duration=%d", a.FinalRange(), a.Pitch(), a.Bandwidth(), a.GetLastPacketDuration())
				}
			}
		case 4:
			gain := int(int32(uint32(desc[1])|uint32(desc[2])<<8|uint32(desc[3])<<16) << 8 >> 8)
			errA, errB := a.SetGain(gain), b.SetGain(gain)
			decoderSequenceCompareError(t, errA, errB)
			if errA == nil && (a.Gain() != gain || b.Gain() != gain) {
				t.Fatalf("gain did not round trip: %d/%d want %d", a.Gain(), b.Gain(), gain)
			}
		case 5:
			disabled := desc[1]&1 != 0
			a.SetPhaseInversionDisabled(disabled)
			b.SetPhaseInversionDisabled(disabled)
			if a.PhaseInversionDisabled() != disabled || b.PhaseInversionDisabled() != disabled {
				t.Fatal("phase-inversion control did not round trip")
			}
		}
		decoderSequenceCompareState(t, rate, channels, a, b)
	}
}

func decoderSequenceCompareCall(t *testing.T, tag byte, packet []byte, frameSize, rate, channels int, a, b *Decoder) {
	t.Helper()
	pcmLen := decoderSequencePCMLen(tag, packet, frameSize, rate, channels)
	pcmA := make([]int16, pcmLen)
	pcmB := make([]int16, len(pcmA))
	for i := range pcmA {
		pcmA[i], pcmB[i] = decoderSequenceSentinel, decoderSequenceSentinel
	}
	var nA, nB int
	var errA, errB error
	switch tag {
	case 0:
		nA, errA = a.Decode(packet, pcmA)
		nB, errB = b.Decode(packet, pcmB)
	case 1:
		nA, errA = a.DecodeFEC(packet, pcmA)
		nB, errB = b.DecodeFEC(packet, pcmB)
	case 2:
		nA, errA = a.DecodePLC(pcmA, frameSize)
		nB, errB = b.DecodePLC(pcmB, frameSize)
	}
	decoderSequenceCompareError(t, errA, errB)
	if nA != nB || !slices.Equal(pcmA, pcmB) {
		t.Fatalf("non-deterministic call tag=%d: n=%d/%d err=%v/%v", tag, nA, nB, errA, errB)
	}
	if errA != nil {
		if nA != 0 {
			t.Fatalf("error returned n=%d", nA)
		}
		for i, sample := range pcmA {
			if sample != decoderSequenceSentinel {
				t.Fatalf("error modified pcm[%d]=%d", i, sample)
			}
		}
		return
	}
	if nA <= 0 || nA > MaxFrameSize*rate/48000 {
		t.Fatalf("successful call returned invalid duration %d", nA)
	}
	if tag == 2 {
		if nA != frameSize {
			t.Fatalf("PLC duration=%d want %d", nA, frameSize)
		}
	} else {
		want, err := PacketGetNumSamples(packet, rate)
		if err != nil || nA != want {
			t.Fatalf("packet duration=%d, %v; decode returned %d", want, err, nA)
		}
	}
	for i := nA * channels; i < len(pcmA); i++ {
		if pcmA[i] != decoderSequenceSentinel {
			t.Fatalf("success modified guard pcm[%d]=%d", i, pcmA[i])
		}
	}
}

func decoderSequenceCompareState(t *testing.T, rate, channels int, a, b *Decoder) {
	t.Helper()
	if a.SampleRate() != rate || b.SampleRate() != rate || a.Channels() != channels || b.Channels() != channels {
		t.Fatal("decoder identity changed")
	}
	if a.GetLastPacketDuration() != b.GetLastPacketDuration() || a.Bandwidth() != b.Bandwidth() ||
		a.GetBandwidth() != a.Bandwidth() || b.GetBandwidth() != b.Bandwidth() ||
		a.FinalRange() != b.FinalRange() || a.Pitch() != b.Pitch() || a.Gain() != b.Gain() ||
		a.PhaseInversionDisabled() != b.PhaseInversionDisabled() {
		t.Fatalf("decoder state diverged: A duration=%d bw=%d range=%d pitch=%d gain=%d phase=%v; B duration=%d bw=%d range=%d pitch=%d gain=%d phase=%v",
			a.GetLastPacketDuration(), a.Bandwidth(), a.FinalRange(), a.Pitch(), a.Gain(), a.PhaseInversionDisabled(),
			b.GetLastPacketDuration(), b.Bandwidth(), b.FinalRange(), b.Pitch(), b.Gain(), b.PhaseInversionDisabled())
	}
	if a.Pitch() < 0 {
		t.Fatalf("negative pitch %d", a.Pitch())
	}
}

func decoderSequenceCompareError(t *testing.T, a, b error) {
	t.Helper()
	if (a == nil) != (b == nil) {
		t.Fatalf("errors differ: %v / %v", a, b)
	}
	if a != nil && a.Error() != b.Error() {
		t.Fatalf("errors differ: %v / %v", a, b)
	}
}

func decoderSequenceCallCost(tag byte, packet []byte, frameSize, rate int) int {
	minCost := rate / 400
	switch tag {
	case 0, 1:
		if samples, err := PacketGetNumSamples(packet, rate); err == nil && samples > 0 {
			return samples
		}
	case 2:
		if frameSize > 0 {
			maxFrameSize := MaxFrameSize * rate / 48000
			if frameSize <= maxFrameSize {
				return frameSize
			}
			return maxFrameSize
		}
	}
	return minCost
}

func decoderSequencePCMLen(tag byte, packet []byte, frameSize, rate, channels int) int {
	samplesPerChannel := MaxFrameSize * rate / 48000
	switch tag {
	case 0, 1:
		if samples, err := PacketGetNumSamples(packet, rate); err == nil && samples > 0 {
			samplesPerChannel = samples
		}
	case 2:
		if frameSize > 0 && frameSize < samplesPerChannel {
			samplesPerChannel = frameSize
		}
	}
	return samplesPerChannel*channels + 8
}

func decoderSequencePLCFrameSize(selector byte, rate int) int {
	durations400 := [...]int{-1, 0, 1, 2, 4, 8, 16, 24, 32, 40, 48, 49}
	duration := durations400[int(selector)%len(durations400)]
	if duration < 0 {
		return duration
	}
	return rate * duration / 400
}

func decoderSequenceSeed(rateSelector, channelSelector byte, operations ...decoderSequenceOp) []byte {
	out := []byte{rateSelector, channelSelector, byte(len(operations))}
	for _, operation := range operations {
		out = append(out, operation.desc[:]...)
	}
	for _, operation := range operations {
		out = append(out, operation.payload...)
	}
	return out
}

func decoderSequencePacketOp(tag byte, packet []byte) decoderSequenceOp {
	return decoderSequenceOp{
		desc:    [decoderSequenceOpWidth]byte{tag, byte(len(packet)), byte(len(packet) >> 8), 0},
		payload: packet,
	}
}

func decoderSequencePLCOp(selector byte) decoderSequenceOp {
	return decoderSequenceOp{desc: [decoderSequenceOpWidth]byte{2, selector, 0, 0}}
}

func decoderSequenceResetOp() decoderSequenceOp {
	return decoderSequenceOp{desc: [decoderSequenceOpWidth]byte{3, 0, 0, 0}}
}

func decoderSequenceGainOp(gain int) decoderSequenceOp {
	return decoderSequenceOp{desc: [decoderSequenceOpWidth]byte{4, byte(gain), byte(gain >> 8), byte(gain >> 16)}}
}

func decoderSequencePhaseOp(disabled bool) decoderSequenceOp {
	if disabled {
		return decoderSequenceOp{desc: [decoderSequenceOpWidth]byte{5, 1, 0, 0}}
	}
	return decoderSequenceOp{desc: [decoderSequenceOpWidth]byte{5, 0, 0, 0}}
}

func makeDecoderFECSequenceSeed() ([]byte, error) {
	const (
		rate      = 16000
		frameSize = 320
	)
	enc, err := NewEncoder(rate, 1, ApplicationVOIP)
	if err != nil {
		return nil, err
	}
	if err := enc.SetBitrate(18000); err != nil {
		return nil, err
	}
	enc.SetSignalType(SignalVoice)
	enc.SetInbandFEC(true)
	enc.SetPacketLossPerc(20)
	packets := make([][]byte, 6)
	for packet := range packets {
		pcm := make([]float64, frameSize)
		for i := range pcm {
			pcm[i] = 0.3*math.Sin(2*math.Pi*190*float64(packet*frameSize+i)/rate) + 0.08*math.Sin(2*math.Pi*380*float64(packet*frameSize+i)/rate)
		}
		packets[packet], err = enc.EncodeFloat(pcm, frameSize)
		if err != nil {
			return nil, err
		}
		mode, modeErr := PacketGetMode(packets[packet])
		if modeErr != nil || mode != ModeSILKOnly {
			return nil, fmt.Errorf("SILK seed packet %d mode=%d err=%v", packet, mode, modeErr)
		}
	}
	if hasLBRR, lbrrErr := PacketHasLBRR(packets[5]); lbrrErr != nil || !hasLBRR {
		return nil, fmt.Errorf("SILK seed packet hasLBRR=%v err=%v", hasLBRR, lbrrErr)
	}
	operations := make([]decoderSequenceOp, 0, 9)
	for i := 0; i < 4; i++ {
		operations = append(operations, decoderSequencePacketOp(0, packets[i]))
	}
	operations = append(operations, decoderSequencePacketOp(1, packets[5]), decoderSequencePacketOp(0, packets[5]), decoderSequencePLCOp(5), decoderSequenceResetOp(), decoderSequencePLCOp(5))
	return decoderSequenceSeed(2, 0, operations...), nil
}

func makeDecoderHybridSequenceSeed() ([]byte, error) {
	const (
		rate      = 48000
		frameSize = 960
	)
	enc, err := NewEncoder(rate, 1, ApplicationVOIP)
	if err != nil {
		return nil, err
	}
	if err := enc.SetBitrate(64000); err != nil {
		return nil, err
	}
	packets := make([][]byte, 2)
	for packet := range packets {
		pcm := make([]float64, frameSize)
		for i := range pcm {
			t := float64(packet*frameSize+i) / rate
			env := 0.42 + 0.18*math.Sin(2*math.Pi*2.7*t+0.3)
			pcm[i] = env*(0.34*math.Sin(2*math.Pi*175*t)+
				0.13*math.Sin(2*math.Pi*350*t+0.4)+
				0.07*math.Sin(2*math.Pi*700*t+0.8)) +
				0.045*math.Sin(2*math.Pi*16000*t+0.11)
		}
		packets[packet], err = enc.EncodeFloat(pcm, frameSize)
		if err != nil {
			return nil, err
		}
		mode, modeErr := PacketGetMode(packets[packet])
		if modeErr != nil || mode != ModeHybrid {
			return nil, fmt.Errorf("hybrid seed packet %d mode=%d err=%v", packet, mode, modeErr)
		}
	}
	return decoderSequenceSeed(4, 0,
		decoderSequencePacketOp(0, packets[0]),
		decoderSequencePLCOp(5),
		decoderSequencePacketOp(0, packets[1]),
		decoderSequenceResetOp(),
		decoderSequencePacketOp(0, packets[0]),
	), nil
}
