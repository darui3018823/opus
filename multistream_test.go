package opus

import (
	"errors"
	"math"
	"testing"
)

func TestMultistreamRoundTrip51(t *testing.T) {
	const (
		rate      = 48000
		channels  = 6
		frameSize = 960
	)
	mapping := []byte{0, 4, 1, 2, 3, 5}
	enc, err := NewMultistreamEncoder(rate, channels, 4, 2, mapping, ApplicationAudio)
	if err != nil {
		t.Fatal(err)
	}
	enc.SetVBR(true)
	if err := enc.SetBitrate(256000); err != nil {
		t.Fatal(err)
	}
	pcm := make([]float64, frameSize*channels)
	for i := 0; i < frameSize; i++ {
		for ch := 0; ch < channels; ch++ {
			pcm[i*channels+ch] = 0.35 * math.Sin(2*math.Pi*float64(220+ch*113)*float64(i)/rate)
		}
	}
	packet, err := enc.EncodeFloat(pcm, frameSize)
	if err != nil {
		t.Fatal(err)
	}
	packets, duration, err := splitMultistreamPackets(packet, 4, rate)
	if err != nil {
		t.Fatal(err)
	}
	if len(packets) != 4 || duration != frameSize {
		t.Fatalf("split got %d packets, duration %d", len(packets), duration)
	}

	dec, err := NewMultistreamDecoder(rate, channels, 4, 2, mapping)
	if err != nil {
		t.Fatal(err)
	}
	out, err := dec.DecodeFloat(packet)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != len(pcm) {
		t.Fatalf("decoded %d samples, want %d", len(out), len(pcm))
	}
	for ch := 0; ch < channels; ch++ {
		var energy float64
		for i := 0; i < frameSize; i++ {
			v := out[i*channels+ch]
			energy += v * v
		}
		if energy == 0 {
			t.Fatalf("channel %d decoded to silence", ch)
		}
	}
	if enc.FinalRange() != dec.FinalRange() {
		t.Fatalf("final range encoder=%08x decoder=%08x", enc.FinalRange(), dec.FinalRange())
	}
}

func TestMultistreamMappingDuplicatesAndSilence(t *testing.T) {
	enc, err := NewMultistreamEncoder(48000, 2, 1, 1, []byte{0, 1}, ApplicationAudio)
	if err != nil {
		t.Fatal(err)
	}
	packet, err := enc.Encode(make([]int16, 1920), 960)
	if err != nil {
		t.Fatal(err)
	}
	dec, err := NewMultistreamDecoder(48000, 4, 1, 1, []byte{0, 1, 0, 255})
	if err != nil {
		t.Fatal(err)
	}
	out, err := dec.DecodeFloat(packet)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 960; i++ {
		if out[4*i] != out[4*i+2] {
			t.Fatalf("duplicate mapping differs at sample %d", i)
		}
		if out[4*i+3] != 0 {
			t.Fatalf("mapping 255 channel is non-zero at sample %d", i)
		}
	}
}

func TestMultistreamRejectsDurationMismatch(t *testing.T) {
	enc20, _ := NewEncoder(48000, 1, ApplicationAudio)
	enc40, _ := NewEncoder(48000, 1, ApplicationAudio)
	p20, err := enc20.Encode(make([]int16, 960), 960)
	if err != nil {
		t.Fatal(err)
	}
	p40, err := enc40.Encode(make([]int16, 1920), 1920)
	if err != nil {
		t.Fatal(err)
	}
	first, err := makeSelfDelimitedPacket(p20)
	if err != nil {
		t.Fatal(err)
	}
	packet := append(first, p40...)
	dec, err := NewMultistreamDecoder(48000, 2, 2, 0, []byte{0, 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dec.DecodeFloat(packet); !errors.Is(err, ErrInvalidPacket) {
		t.Fatalf("duration mismatch error = %v", err)
	}
}

func TestSelfDelimitedPacketRoundTrip(t *testing.T) {
	enc, err := NewEncoder(48000, 2, ApplicationAudio)
	if err != nil {
		t.Fatal(err)
	}
	enc.SetVBR(true)
	for _, frameSize := range []int{120, 960, 1920, 2880} {
		packet, err := enc.Encode(make([]int16, frameSize*2), frameSize)
		if err != nil {
			t.Fatal(err)
		}
		selfDelimited, err := makeSelfDelimitedPacket(packet)
		if err != nil {
			t.Fatal(err)
		}
		got, used, err := parseSelfDelimitedPacket(append(selfDelimited, 1, 2, 3))
		if err != nil {
			t.Fatal(err)
		}
		if used != len(selfDelimited) {
			t.Fatalf("frameSize %d consumed %d, want %d", frameSize, used, len(selfDelimited))
		}
		wantSamples, _ := PacketGetNumSamples(packet, 48000)
		gotSamples, _ := PacketGetNumSamples(got, 48000)
		if gotSamples != wantSamples {
			t.Fatalf("frameSize %d samples %d, want %d", frameSize, gotSamples, wantSamples)
		}
	}
}

func TestMultistreamPacketPadUnpad(t *testing.T) {
	const (
		rate      = 48000
		channels  = 2
		frameSize = 960
		streams   = 2
	)
	enc, err := NewMultistreamEncoder(rate, channels, streams, 0, []byte{0, 1}, ApplicationAudio)
	if err != nil {
		t.Fatal(err)
	}
	packet, err := enc.Encode(make([]int16, frameSize*channels), frameSize)
	if err != nil {
		t.Fatal(err)
	}
	target := len(packet) + 37
	padded, err := MultistreamPacketPad(packet, streams, target)
	if err != nil {
		t.Fatal(err)
	}
	if len(padded) != target {
		t.Fatalf("padded len = %d, want %d", len(padded), target)
	}
	if _, _, err := splitMultistreamPackets(padded, streams, rate); err != nil {
		t.Fatalf("padded packet no longer parses: %v", err)
	}
	unpadded, err := MultistreamPacketUnpad(padded, streams)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := MultistreamPacketUnpad(packet, streams)
	if err != nil {
		t.Fatal(err)
	}
	if string(unpadded) != string(canonical) {
		t.Fatalf("unpad mismatch: got %x want %x", unpadded, canonical)
	}
}

func TestMultistreamPacketGetNumSamples(t *testing.T) {
	const (
		rate      = 48000
		channels  = 2
		streams   = 2
		frameSize = 960
	)
	enc, err := NewMultistreamEncoder(rate, channels, streams, 0, []byte{0, 1}, ApplicationAudio)
	if err != nil {
		t.Fatal(err)
	}
	packet, err := enc.Encode(make([]int16, frameSize*channels), frameSize)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := MultistreamPacketGetNumSamples(packet, streams, rate); err != nil || got != frameSize {
		t.Fatalf("MultistreamPacketGetNumSamples = (%d, %v), want (%d, nil)", got, err, frameSize)
	}
	if _, err := MultistreamPacketGetNumSamples(packet, streams+1, rate); !errors.Is(err, ErrInvalidPacket) {
		t.Fatalf("wrong stream count error = %v, want ErrInvalidPacket", err)
	}
}

func TestMultistreamDecodeFECRoundTripMapping(t *testing.T) {
	const (
		rate      = 16000
		frameSize = 320
		lost      = 4
	)
	enc, err := NewMultistreamEncoder(rate, 2, 2, 0, []byte{0, 1}, ApplicationVOIP)
	if err != nil {
		t.Fatal(err)
	}
	for stream := 0; stream < enc.Streams(); stream++ {
		child, err := enc.StreamEncoder(stream)
		if err != nil {
			t.Fatal(err)
		}
		if err := child.SetBitrate(18000); err != nil {
			t.Fatal(err)
		}
		child.SetSignalType(SignalVoice)
		child.SetPacketLossPerc(20)
		child.SetInbandFEC(true)
	}

	packets := make([][]byte, lost+2)
	for packet := range packets {
		input := strictSpeechLikeFrame(rate, 2, packet*frameSize, frameSize)
		packets[packet], err = enc.EncodeFloat(input, frameSize)
		if err != nil {
			t.Fatalf("encode packet %d: %v", packet, err)
		}
	}
	children, _, err := splitMultistreamPackets(packets[lost+1], 2, rate)
	if err != nil {
		t.Fatal(err)
	}
	for stream, packet := range children {
		hasLBRR, err := PacketHasLBRR(packet)
		if err != nil {
			t.Fatal(err)
		}
		if !hasLBRR {
			t.Fatalf("stream %d has no LBRR", stream)
		}
	}

	dec, err := NewMultistreamDecoder(rate, 4, 2, 0, []byte{1, 0, 1, 255})
	if err != nil {
		t.Fatal(err)
	}
	for packet := 0; packet < lost; packet++ {
		if _, err := dec.Decode(packets[packet], make([]int16, frameSize*4)); err != nil {
			t.Fatalf("prime packet %d: %v", packet, err)
		}
	}
	recovered := make([]int16, frameSize*4)
	if n, err := dec.DecodeFEC(packets[lost+1], recovered); err != nil || n != frameSize {
		t.Fatalf("DecodeFEC = (%d, %v), want (%d, nil)", n, err, frameSize)
	}

	var energy [2]int64
	for i := 0; i < frameSize; i++ {
		if recovered[4*i] != recovered[4*i+2] {
			t.Fatalf("duplicate mapping differs at sample %d", i)
		}
		if recovered[4*i+3] != 0 {
			t.Fatalf("mapping 255 channel is non-zero at sample %d", i)
		}
		energy[0] += int64(recovered[4*i]) * int64(recovered[4*i])
		energy[1] += int64(recovered[4*i+1]) * int64(recovered[4*i+1])
	}
	if energy[0] == 0 || energy[1] == 0 {
		t.Fatalf("recovered channel energy = %v", energy)
	}
}

func TestMultistreamDecodeFECUsesPLCForCELT(t *testing.T) {
	const (
		rate      = 16000
		frameSize = 320
		lost      = 4
	)
	enc, err := NewMultistreamEncoder(rate, 2, 2, 0, []byte{0, 1}, ApplicationVOIP)
	if err != nil {
		t.Fatal(err)
	}
	silkEnc, _ := enc.StreamEncoder(0)
	if err := silkEnc.SetBitrate(18000); err != nil {
		t.Fatal(err)
	}
	silkEnc.SetSignalType(SignalVoice)
	silkEnc.SetPacketLossPerc(20)
	silkEnc.SetInbandFEC(true)
	celtEnc, _ := enc.StreamEncoder(1)
	if err := celtEnc.SetBitrate(48000); err != nil {
		t.Fatal(err)
	}
	celtEnc.SetSignalType(SignalMusic)
	celtEnc.SetPredictionDisabled(true)

	packets := make([][]byte, lost+2)
	for packet := range packets {
		input := strictSpeechLikeFrame(rate, 2, packet*frameSize, frameSize)
		packets[packet], err = enc.EncodeFloat(input, frameSize)
		if err != nil {
			t.Fatalf("encode packet %d: %v", packet, err)
		}
	}
	children, _, err := splitMultistreamPackets(packets[lost+1], 2, rate)
	if err != nil {
		t.Fatal(err)
	}
	for stream, want := range []int{ModeSILKOnly, ModeCELTOnly} {
		mode, err := PacketGetMode(children[stream])
		if err != nil {
			t.Fatal(err)
		}
		if mode != want {
			t.Fatalf("stream %d mode = %d, want %d", stream, mode, want)
		}
	}

	dec, err := NewMultistreamDecoder(rate, 2, 2, 0, []byte{0, 1})
	if err != nil {
		t.Fatal(err)
	}
	for packet := 0; packet < lost; packet++ {
		if _, err := dec.Decode(packets[packet], make([]int16, frameSize*2)); err != nil {
			t.Fatalf("prime packet %d: %v", packet, err)
		}
	}
	recovered := make([]int16, frameSize*2)
	if n, err := dec.DecodeFEC(packets[lost+1], recovered); err != nil || n != frameSize {
		t.Fatalf("DecodeFEC = (%d, %v), want (%d, nil)", n, err, frameSize)
	}
	var celtEnergy int64
	for i := 0; i < frameSize; i++ {
		celtEnergy += int64(recovered[2*i+1]) * int64(recovered[2*i+1])
	}
	if celtEnergy == 0 {
		t.Fatal("CELT PLC channel decoded to silence")
	}

	fresh, err := NewMultistreamDecoder(rate, 2, 2, 0, []byte{0, 1})
	if err != nil {
		t.Fatal(err)
	}
	untouched := make([]int16, frameSize*2)
	for i := range untouched {
		untouched[i] = 1234
	}
	if _, err := fresh.DecodeFEC(packets[lost+1], untouched); err == nil {
		t.Fatal("DecodeFEC without CELT history succeeded")
	}
	for i, sample := range untouched {
		if sample != 1234 {
			t.Fatalf("DecodeFEC modified output[%d] on error: %d", i, sample)
		}
	}
}

func TestMultistreamDecodeFECUsesPLCAfterCELT(t *testing.T) {
	const (
		rate      = 16000
		frameSize = 320
	)
	enc, err := NewMultistreamEncoder(rate, 1, 1, 0, []byte{0}, ApplicationVOIP)
	if err != nil {
		t.Fatal(err)
	}
	child, err := enc.StreamEncoder(0)
	if err != nil {
		t.Fatal(err)
	}
	if err := child.SetBitrate(48000); err != nil {
		t.Fatal(err)
	}
	child.SetSignalType(SignalMusic)
	child.SetPredictionDisabled(true)
	celtPacket, err := enc.EncodeFloat(strictSpeechLikeFrame(rate, 1, 0, frameSize), frameSize)
	if err != nil {
		t.Fatal(err)
	}
	if mode, err := PacketGetMode(celtPacket); err != nil || mode != ModeCELTOnly {
		t.Fatalf("first packet mode = %d, %v; want CELT", mode, err)
	}

	if err := child.SetBitrate(18000); err != nil {
		t.Fatal(err)
	}
	child.SetPredictionDisabled(false)
	child.SetSignalType(SignalVoice)
	silkPacket, err := enc.EncodeFloat(strictSpeechLikeFrame(rate, 1, frameSize, frameSize), frameSize)
	if err != nil {
		t.Fatal(err)
	}
	if mode, err := PacketGetMode(silkPacket); err != nil || mode != ModeSILKOnly {
		t.Fatalf("second packet mode = %d, %v; want SILK", mode, err)
	}

	newPrimedDecoder := func() *MultistreamDecoder {
		dec, err := NewMultistreamDecoder(rate, 1, 1, 0, []byte{0})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := dec.Decode(celtPacket, make([]int16, frameSize)); err != nil {
			t.Fatal(err)
		}
		return dec
	}
	fecDec := newPrimedDecoder()
	plcDec := newPrimedDecoder()
	fecOut := make([]int16, frameSize)
	plcOut := make([]int16, frameSize)
	if n, err := fecDec.DecodeFEC(silkPacket, fecOut); err != nil || n != frameSize {
		t.Fatalf("DecodeFEC = (%d, %v), want (%d, nil)", n, err, frameSize)
	}
	plcChild, err := plcDec.StreamDecoder(0)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := plcChild.DecodePLC(plcOut, frameSize); err != nil || n != frameSize {
		t.Fatalf("DecodePLC = (%d, %v), want (%d, nil)", n, err, frameSize)
	}
	for i := range fecOut {
		if fecOut[i] != plcOut[i] {
			t.Fatalf("CELT-history fallback differs at sample %d: FEC=%d PLC=%d", i, fecOut[i], plcOut[i])
		}
	}
}

func TestMultistreamDecodePLCMonoAndCoupled(t *testing.T) {
	tests := []struct {
		name           string
		channels       int
		coupledStreams int
		mapping        []byte
		bitrate        int
		voice          bool
		wantMode       int
	}{
		{name: "mono", channels: 1, mapping: []byte{0}, bitrate: 18000, voice: true, wantMode: ModeSILKOnly},
		{name: "coupled", channels: 2, coupledStreams: 1, mapping: []byte{0, 1}, bitrate: 48000, wantMode: ModeCELTOnly},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			const (
				rate      = 16000
				frameSize = 320
			)
			enc, err := NewMultistreamEncoder(rate, tc.channels, 1, tc.coupledStreams, tc.mapping, ApplicationVOIP)
			if err != nil {
				t.Fatal(err)
			}
			child, err := enc.StreamEncoder(0)
			if err != nil {
				t.Fatal(err)
			}
			if err := child.SetBitrate(tc.bitrate); err != nil {
				t.Fatal(err)
			}
			if tc.voice {
				child.SetSignalType(SignalVoice)
			} else {
				child.SetSignalType(SignalMusic)
				child.SetPredictionDisabled(true)
			}

			var packet []byte
			for p := 0; p < 4; p++ {
				packet, err = enc.EncodeFloat(multistreamPLCFrame(rate, tc.channels, p*frameSize, frameSize), frameSize)
				if err != nil {
					t.Fatalf("encode packet %d: %v", p, err)
				}
			}
			if mode, err := PacketGetMode(packet); err != nil || mode != tc.wantMode {
				t.Fatalf("packet mode = %d, err=%v, want %d", mode, err, tc.wantMode)
			}

			dec, err := NewMultistreamDecoder(rate, tc.channels, 1, tc.coupledStreams, tc.mapping)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := dec.Decode(packet, make([]int16, frameSize*tc.channels)); err != nil {
				t.Fatal(err)
			}
			concealed := make([]int16, frameSize*tc.channels)
			if n, err := dec.DecodePLC(concealed, frameSize); err != nil || n != frameSize {
				t.Fatalf("DecodePLC = (%d, %v), want (%d, nil)", n, err, frameSize)
			}
			for channel := 0; channel < tc.channels; channel++ {
				if energy := multistreamChannelEnergy(concealed, tc.channels, channel); energy == 0 {
					t.Fatalf("channel %d concealed to silence", channel)
				}
			}
		})
	}
}

func TestMultistreamDecodePLCMixedLayoutBurstAndRecovery(t *testing.T) {
	const (
		rate            = 16000
		inputChannels   = 3
		outputChannels  = 5
		frameSize       = 320
		primePackets    = 4
		concealedFrames = 2
	)
	enc, err := NewMultistreamEncoder(rate, inputChannels, 2, 1, []byte{0, 2, 1}, ApplicationVOIP)
	if err != nil {
		t.Fatal(err)
	}
	coupled, _ := enc.StreamEncoder(0)
	if err := coupled.SetBitrate(48000); err != nil {
		t.Fatal(err)
	}
	coupled.SetSignalType(SignalMusic)
	coupled.SetPredictionDisabled(true)
	mono, _ := enc.StreamEncoder(1)
	if err := mono.SetBitrate(18000); err != nil {
		t.Fatal(err)
	}
	mono.SetSignalType(SignalVoice)

	packets := make([][]byte, primePackets+concealedFrames+1)
	for p := range packets {
		packets[p], err = enc.EncodeFloat(multistreamPLCFrame(rate, inputChannels, p*frameSize, frameSize), frameSize)
		if err != nil {
			t.Fatalf("encode packet %d: %v", p, err)
		}
	}
	children, _, err := splitMultistreamPackets(packets[primePackets], 2, rate)
	if err != nil {
		t.Fatal(err)
	}
	for stream, want := range []int{ModeCELTOnly, ModeSILKOnly} {
		mode, err := PacketGetMode(children[stream])
		if err != nil || mode != want {
			t.Fatalf("stream %d mode = %d, err=%v, want %d", stream, mode, err, want)
		}
	}

	mapping := []byte{0, 2, 1, 2, 255}
	dec, err := NewMultistreamDecoder(rate, outputChannels, 2, 1, mapping)
	if err != nil {
		t.Fatal(err)
	}
	for p := 0; p < primePackets; p++ {
		if _, err := dec.Decode(packets[p], make([]int16, frameSize*outputChannels)); err != nil {
			t.Fatalf("prime packet %d: %v", p, err)
		}
	}

	concealed := make([][]int16, concealedFrames)
	for loss := range concealed {
		concealed[loss] = make([]int16, frameSize*outputChannels)
		if n, err := dec.DecodePLC(concealed[loss], frameSize); err != nil || n != frameSize {
			t.Fatalf("DecodePLC loss %d = (%d, %v), want (%d, nil)", loss, n, err, frameSize)
		}
		for i := 0; i < frameSize; i++ {
			if concealed[loss][i*outputChannels+1] != concealed[loss][i*outputChannels+3] {
				t.Fatalf("loss %d duplicate mapping differs at sample %d", loss, i)
			}
			if concealed[loss][i*outputChannels+4] != 0 {
				t.Fatalf("loss %d mapping 255 is non-zero at sample %d", loss, i)
			}
		}
	}
	for _, channel := range []int{0, 1, 2} {
		first := multistreamChannelEnergy(concealed[0], outputChannels, channel)
		second := multistreamChannelEnergy(concealed[1], outputChannels, channel)
		if first == 0 || second == 0 {
			t.Fatalf("channel %d PLC returned silence: first=%g second=%g", channel, first, second)
		}
		if second >= first {
			t.Fatalf("channel %d PLC energy did not decay: first=%g second=%g", channel, first, second)
		}
	}

	recovered := make([]int16, frameSize*outputChannels)
	if _, err := dec.Decode(packets[primePackets+concealedFrames], recovered); err != nil {
		t.Fatalf("normal decode after PLC: %v", err)
	}
	for _, channel := range []int{0, 1, 2, 3} {
		last := concealed[concealedFrames-1][(frameSize-1)*outputChannels+channel]
		jump := math.Abs(float64(recovered[channel]) - float64(last))
		if jump > 10000 {
			t.Fatalf("recovery boundary jump on channel %d is too large: %.0f", channel, jump)
		}
	}
}

func TestMultistreamDecodePLCValidationPreservesOutputAndState(t *testing.T) {
	const (
		rate      = 16000
		frameSize = 320
	)
	enc, err := NewEncoder(rate, 1, ApplicationAudio)
	if err != nil {
		t.Fatal(err)
	}
	enc.SetPredictionDisabled(true)
	packet, err := enc.EncodeFloat(multistreamPLCFrame(rate, 1, 0, frameSize), frameSize)
	if err != nil {
		t.Fatal(err)
	}
	newPrimed := func() *MultistreamDecoder {
		dec, err := NewMultistreamDecoder(rate, 1, 1, 0, []byte{0})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := dec.Decode(packet, make([]int16, frameSize)); err != nil {
			t.Fatal(err)
		}
		return dec
	}
	dec := newPrimed()
	control := newPrimed()

	invalid := make([]int16, frameSize+1)
	for i := range invalid {
		invalid[i] = 1234
	}
	if _, err := dec.DecodePLC(invalid, frameSize+1); !errors.Is(err, ErrUnsupportedFrameSize) {
		t.Fatalf("invalid frame size error = %v, want ErrUnsupportedFrameSize", err)
	}
	for i, sample := range invalid {
		if sample != 1234 {
			t.Fatalf("invalid frame size modified output[%d]: %d", i, sample)
		}
	}

	short := make([]int16, frameSize-1)
	for i := range short {
		short[i] = 2345
	}
	if _, err := dec.DecodePLC(short, frameSize); !errors.Is(err, ErrBufferTooSmall) {
		t.Fatalf("small buffer error = %v, want ErrBufferTooSmall", err)
	}
	for i, sample := range short {
		if sample != 2345 {
			t.Fatalf("small buffer modified output[%d]: %d", i, sample)
		}
	}

	got := make([]int16, frameSize)
	want := make([]int16, frameSize)
	if _, err := dec.DecodePLC(got, frameSize); err != nil {
		t.Fatal(err)
	}
	if _, err := control.DecodePLC(want, frameSize); err != nil {
		t.Fatal(err)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("validation changed decoder state at sample %d: got %d want %d", i, got[i], want[i])
		}
	}

	partial, err := NewMultistreamDecoder(rate, 2, 2, 0, []byte{0, 1})
	if err != nil {
		t.Fatal(err)
	}
	first, _ := partial.StreamDecoder(0)
	if _, err := first.Decode(packet, make([]int16, frameSize)); err != nil {
		t.Fatal(err)
	}
	untouched := make([]int16, frameSize*2)
	for i := range untouched {
		untouched[i] = 3456
	}
	if _, err := partial.DecodePLC(untouched, frameSize); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("partially primed error = %v, want ErrInvalidState", err)
	}
	for i, sample := range untouched {
		if sample != 3456 {
			t.Fatalf("child failure modified output[%d]: %d", i, sample)
		}
	}
}

func multistreamPLCFrame(rate, channels, start, frameSize int) []float64 {
	pcm := make([]float64, frameSize*channels)
	for i := 0; i < frameSize; i++ {
		time := float64(start+i) / float64(rate)
		for channel := 0; channel < channels; channel++ {
			frequency := float64(170 + 43*channel)
			pcm[i*channels+channel] = 0.32*math.Sin(2*math.Pi*frequency*time+0.2*float64(channel)) +
				0.09*math.Sin(2*math.Pi*2*frequency*time+0.35)
		}
	}
	return pcm
}

func multistreamChannelEnergy(pcm []int16, channels, channel int) float64 {
	var energy float64
	for i := channel; i < len(pcm); i += channels {
		sample := float64(pcm[i])
		energy += sample * sample
	}
	return energy
}
