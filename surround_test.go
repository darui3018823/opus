package opus

import (
	"errors"
	"math"
	"testing"
)

func TestVorbisSurroundLayouts(t *testing.T) {
	want := []struct {
		streams, coupled, lfe int
		mapping               []byte
	}{
		{1, 0, -1, []byte{0}},
		{1, 1, -1, []byte{0, 1}},
		{2, 1, -1, []byte{0, 2, 1}},
		{2, 2, -1, []byte{0, 1, 2, 3}},
		{3, 2, -1, []byte{0, 4, 1, 2, 3}},
		{4, 2, 3, []byte{0, 4, 1, 2, 3, 5}},
		{4, 3, 3, []byte{0, 4, 1, 2, 3, 5, 6}},
		{5, 3, 4, []byte{0, 6, 1, 2, 3, 4, 5, 7}},
	}
	for i, tc := range want {
		channels := i + 1
		enc, err := NewSurroundEncoder(48000, channels, MappingFamilyVorbis, ApplicationAudio)
		if err != nil {
			t.Fatal(err)
		}
		if enc.Streams() != tc.streams || enc.CoupledStreams() != tc.coupled || enc.LFEStream() != tc.lfe {
			t.Fatalf("%dch layout=(%d,%d,%d), want (%d,%d,%d)",
				channels, enc.Streams(), enc.CoupledStreams(), enc.LFEStream(),
				tc.streams, tc.coupled, tc.lfe)
		}
		got := enc.Mapping()
		for j := range got {
			if got[j] != tc.mapping[j] {
				t.Fatalf("%dch mapping[%d]=%d, want %d", channels, j, got[j], tc.mapping[j])
			}
		}
	}
}

func TestSurroundRoundTrip71(t *testing.T) {
	const (
		rate      = 48000
		channels  = 8
		frameSize = 960
	)
	enc, err := NewSurroundEncoder(rate, channels, MappingFamilyVorbis, ApplicationAudio)
	if err != nil {
		t.Fatal(err)
	}
	enc.SetVBR(true)
	if err := enc.SetBitrate(384000); err != nil {
		t.Fatal(err)
	}
	pcm := make([]float32, frameSize*channels)
	for i := 0; i < frameSize; i++ {
		for ch := 0; ch < channels; ch++ {
			pcm[i*channels+ch] = float32(0.3 * math.Sin(2*math.Pi*float64(160+83*ch)*float64(i)/rate))
		}
	}
	packet, err := enc.EncodeFloat32(pcm, frameSize)
	if err != nil {
		t.Fatal(err)
	}
	dec, err := NewSurroundDecoder(rate, channels, MappingFamilyVorbis)
	if err != nil {
		t.Fatal(err)
	}
	out, err := dec.DecodeFloat32(packet)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != len(pcm) {
		t.Fatalf("decoded %d samples, want %d", len(out), len(pcm))
	}
	for ch := 0; ch < channels; ch++ {
		var energy float64
		for i := 0; i < frameSize; i++ {
			v := float64(out[i*channels+ch])
			energy += v * v
		}
		if energy == 0 {
			t.Fatalf("channel %d decoded to silence", ch)
		}
	}
}

func TestSurroundLFERateAllocation(t *testing.T) {
	enc, err := NewSurroundEncoder(48000, 6, MappingFamilyVorbis, ApplicationAudio)
	if err != nil {
		t.Fatal(err)
	}
	if err := enc.SetBitrate(256000); err != nil {
		t.Fatal(err)
	}
	rates := enc.allocateRates(960)
	if enc.LFEStream() < 0 {
		t.Fatal("5.1 layout has no LFE stream")
	}
	lfeRate := rates[enc.LFEStream()]
	for stream, rate := range rates {
		if stream != enc.LFEStream() && lfeRate >= rate {
			t.Fatalf("LFE rate %d is not below stream %d rate %d", lfeRate, stream, rate)
		}
	}
	for stream := 0; stream < enc.CoupledStreams(); stream++ {
		child, _ := enc.StreamEncoder(stream)
		if !child.PredictionDisabled() || child.ForceChannels() != ChannelsStereo {
			t.Fatalf("coupled stream %d is not forced to stereo CELT", stream)
		}
	}
}

func TestSurroundMappingFamilies(t *testing.T) {
	discrete, err := NewSurroundEncoder(48000, 12, MappingFamilyDiscrete, ApplicationAudio)
	if err != nil {
		t.Fatal(err)
	}
	if discrete.Streams() != 12 || discrete.CoupledStreams() != 0 {
		t.Fatalf("discrete layout streams=%d coupled=%d", discrete.Streams(), discrete.CoupledStreams())
	}
	if _, err := NewSurroundEncoder(48000, 6, MappingFamilyMonoStereo, ApplicationAudio); !errors.Is(err, ErrBadArg) {
		t.Fatalf("family 0 with 6ch error=%v", err)
	}
	if _, err := NewSurroundEncoder(48000, 4, MappingFamilyAmbisonics, ApplicationAudio); !errors.Is(err, ErrUnimplemented) {
		t.Fatalf("family 2 error=%v", err)
	}
}

func TestSurroundDecoderPromotesDecodeFEC(t *testing.T) {
	const (
		rate      = 16000
		frameSize = 320
		lost      = 4
	)
	enc, err := NewSurroundEncoder(rate, 1, MappingFamilyVorbis, ApplicationVOIP)
	if err != nil {
		t.Fatal(err)
	}
	child, err := enc.StreamEncoder(0)
	if err != nil {
		t.Fatal(err)
	}
	if err := child.SetBitrate(18000); err != nil {
		t.Fatal(err)
	}
	child.SetSignalType(SignalVoice)
	child.SetPacketLossPerc(20)
	child.SetInbandFEC(true)

	packets := make([][]byte, lost+2)
	for packet := range packets {
		input := strictSpeechLikeFrame(rate, 1, packet*frameSize, frameSize)
		packets[packet], err = enc.EncodeFloat(input, frameSize)
		if err != nil {
			t.Fatalf("encode packet %d: %v", packet, err)
		}
	}

	dec, err := NewSurroundDecoder(rate, 1, MappingFamilyVorbis)
	if err != nil {
		t.Fatal(err)
	}
	for packet := 0; packet < lost; packet++ {
		if _, err := dec.Decode(packets[packet], make([]int16, frameSize)); err != nil {
			t.Fatalf("prime packet %d: %v", packet, err)
		}
	}
	recovered := make([]int16, frameSize)
	if n, err := dec.DecodeFEC(packets[lost+1], recovered); err != nil || n != frameSize {
		t.Fatalf("DecodeFEC = (%d, %v), want (%d, nil)", n, err, frameSize)
	}
	var energy int64
	for _, sample := range recovered {
		energy += int64(sample) * int64(sample)
	}
	if energy == 0 {
		t.Fatal("surround FEC recovery decoded to silence")
	}
}
