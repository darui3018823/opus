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
	decoderSequenceSentinel  = int16(-23457)
)

func FuzzDecoderSequence(f *testing.F) {
	f.Add(decoderSequenceSeed(4, 0,
		decoderSequencePacketOp(0, []byte{0xf8, 0xff, 0xfe}),
		[]byte{2, 3}, []byte{4, 0, 1}, []byte{5, 1}, []byte{3}, []byte{2, 3},
	))
	f.Add(decoderSequenceSeed(2, 1,
		decoderSequencePacketOp(0, []byte{0x00, 0x00}),
		decoderSequencePacketOp(1, []byte{0x7f}),
		[]byte{2, 8}, []byte{3},
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
	for operation := 0; operation < maxDecoderSequenceOps && len(data) > 0; operation++ {
		tag := data[0] % 6
		data = data[1:]
		switch tag {
		case 0, 1:
			if len(data) < 2 {
				return
			}
			length := (int(data[0]) | int(data[1])<<8) % (maxDecoderSequencePacket + 1)
			data = data[2:]
			if length > len(data) {
				length = len(data)
			}
			packet := data[:length]
			data = data[length:]
			decoderSequenceCompareCall(t, tag, packet, 0, rate, channels, a, b)
		case 2:
			if len(data) == 0 {
				return
			}
			durations400 := [...]int{1, 2, 4, 8, 16, 24, 32, 40, 48}
			frameSize := rate * durations400[int(data[0])%len(durations400)] / 400
			data = data[1:]
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
			if len(data) < 2 {
				return
			}
			gain := int(int16(uint16(data[0]) | uint16(data[1])<<8))
			data = data[2:]
			errA, errB := a.SetGain(gain), b.SetGain(gain)
			decoderSequenceCompareError(t, errA, errB)
			if errA == nil && (a.Gain() != gain || b.Gain() != gain) {
				t.Fatalf("gain did not round trip: %d/%d want %d", a.Gain(), b.Gain(), gain)
			}
		case 5:
			if len(data) == 0 {
				return
			}
			disabled := data[0]&1 != 0
			data = data[1:]
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
	if tag == 1 {
		hasLBRR, err := PacketHasLBRR(packet)
		if err != nil || !hasLBRR {
			return
		}
	}
	pcmA := make([]int16, MaxFrameSize*2+8)
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
	if fmt.Sprint(a) != fmt.Sprint(b) {
		t.Fatalf("errors differ: %v / %v", a, b)
	}
}

func decoderSequenceSeed(rateSelector, channelSelector byte, operations ...[]byte) []byte {
	out := []byte{rateSelector, channelSelector}
	for _, operation := range operations {
		out = append(out, operation...)
	}
	return out
}

func decoderSequencePacketOp(tag byte, packet []byte) []byte {
	out := []byte{tag, byte(len(packet)), byte(len(packet) >> 8)}
	return append(out, packet...)
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
			return nil, fmt.Errorf("SILK seed packet %d mode=%d: %w", packet, mode, modeErr)
		}
	}
	if hasLBRR, lbrrErr := PacketHasLBRR(packets[5]); lbrrErr != nil || !hasLBRR {
		return nil, fmt.Errorf("SILK seed packet hasLBRR=%v: %w", hasLBRR, lbrrErr)
	}
	operations := make([][]byte, 0, 9)
	for i := 0; i < 4; i++ {
		operations = append(operations, decoderSequencePacketOp(0, packets[i]))
	}
	operations = append(operations, decoderSequencePacketOp(1, packets[5]), decoderSequencePacketOp(0, packets[5]), []byte{2, 3}, []byte{3}, []byte{2, 3})
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
			return nil, fmt.Errorf("hybrid seed packet %d mode=%d: %w", packet, mode, modeErr)
		}
	}
	return decoderSequenceSeed(4, 0,
		decoderSequencePacketOp(0, packets[0]),
		[]byte{2, 8},
		decoderSequencePacketOp(0, packets[1]),
		[]byte{3},
		decoderSequencePacketOp(0, packets[0]),
	), nil
}
