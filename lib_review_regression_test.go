package opus

import (
	"errors"
	"math"
	"testing"
)

func TestMonoDecoderDecodesStereoCELTPacket(t *testing.T) {
	const sampleRate = 48000
	const frameSize = 960

	enc, err := NewEncoder(sampleRate, 2, ApplicationAudio)
	if err != nil {
		t.Fatal(err)
	}
	pcm := make([]float64, frameSize*2)
	for i := 0; i < frameSize; i++ {
		pcm[2*i] = 0.35 * math.Sin(2*math.Pi*440*float64(i)/sampleRate)
		pcm[2*i+1] = 0.25 * math.Sin(2*math.Pi*880*float64(i)/sampleRate)
	}
	pkt, err := enc.EncodeFloat(pcm, frameSize)
	if err != nil {
		t.Fatal(err)
	}

	stereoDec, err := NewDecoder(sampleRate, 2)
	if err != nil {
		t.Fatal(err)
	}
	stereo, err := stereoDec.DecodeFloat(pkt)
	if err != nil {
		t.Fatal(err)
	}

	monoDec, err := NewDecoder(sampleRate, 1)
	if err != nil {
		t.Fatal(err)
	}
	mono, err := monoDec.DecodeFloat(pkt)
	if err != nil {
		t.Fatal(err)
	}
	if len(mono) != frameSize {
		t.Fatalf("mono decode length = %d, want %d", len(mono), frameSize)
	}
	for i := range mono {
		want := 0.5 * (stereo[2*i] + stereo[2*i+1])
		if math.Abs(mono[i]-want) > 1e-12 {
			t.Fatalf("sample %d: mono=%g want downmix=%g", i, mono[i], want)
		}
	}
}

func TestEncoderRejectsInvalidPacketDurations(t *testing.T) {
	enc16, err := NewEncoder(16000, 1, ApplicationAudio)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := enc16.EncodeFloat(make([]float64, 480), 480); !errors.Is(err, ErrUnsupportedFrameSize) {
		t.Fatalf("16 kHz 30 ms encode error = %v, want ErrUnsupportedFrameSize", err)
	}

	enc48, err := NewEncoder(48000, 1, ApplicationAudio)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := enc48.EncodeFloat(make([]float64, 720), 720); !errors.Is(err, ErrUnsupportedFrameSize) {
		t.Fatalf("48 kHz 15 ms encode error = %v, want ErrUnsupportedFrameSize", err)
	}
	if _, err := enc48.EncodeFloat(make([]float64, 7*960), 7*960); !errors.Is(err, ErrUnsupportedFrameSize) {
		t.Fatalf("140 ms encode error = %v, want ErrUnsupportedFrameSize", err)
	}
}

func TestDecoderRejectsPacketDurationOver120ms(t *testing.T) {
	dec, err := NewDecoder(48000, 1)
	if err != nil {
		t.Fatal(err)
	}
	// CELT fullband 20 ms, code 3, CBR frame count 7 -> 140 ms, invalid.
	pkt := []byte{byte(31<<3) | 0x03, 0x07}
	if _, err := dec.DecodeFloat(pkt); !errors.Is(err, ErrInvalidPacket) {
		t.Fatalf("DecodeFloat overlong packet error = %v, want ErrInvalidPacket", err)
	}
}

func TestDecodeReturnsBufferTooSmall(t *testing.T) {
	const sampleRate = 48000
	const frameSize = 960

	enc, err := NewEncoder(sampleRate, 1, ApplicationAudio)
	if err != nil {
		t.Fatal(err)
	}
	pkt, err := enc.EncodeFloat(make([]float64, frameSize), frameSize)
	if err != nil {
		t.Fatal(err)
	}
	dec, err := NewDecoder(sampleRate, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dec.Decode(pkt, make([]int16, frameSize-1)); !errors.Is(err, ErrBufferTooSmall) {
		t.Fatalf("Decode small buffer error = %v, want ErrBufferTooSmall", err)
	}
}

func TestDecodeBufferTooSmallDoesNotAdvanceState(t *testing.T) {
	const sampleRate = 48000
	const frameSize = 960

	enc, err := NewEncoder(sampleRate, 1, ApplicationAudio)
	if err != nil {
		t.Fatal(err)
	}
	makeFrame := func(freq, phase float64) []float64 {
		pcm := make([]float64, frameSize)
		for i := range pcm {
			pcm[i] = 0.3 * math.Sin(2*math.Pi*freq*float64(i)/sampleRate+phase)
		}
		return pcm
	}
	firstPacket, err := enc.EncodeFloat(makeFrame(440, 0), frameSize)
	if err != nil {
		t.Fatal(err)
	}
	secondPacket, err := enc.EncodeFloat(makeFrame(733, 0.7), frameSize)
	if err != nil {
		t.Fatal(err)
	}

	retryDecoder, err := NewDecoder(sampleRate, 1)
	if err != nil {
		t.Fatal(err)
	}
	controlDecoder, err := NewDecoder(sampleRate, 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, dec := range []*Decoder{retryDecoder, controlDecoder} {
		if _, err := dec.Decode(firstPacket, make([]int16, frameSize)); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := retryDecoder.Decode(secondPacket, make([]int16, frameSize-1)); !errors.Is(err, ErrBufferTooSmall) {
		t.Fatalf("Decode small buffer error = %v, want ErrBufferTooSmall", err)
	}

	retried := make([]int16, frameSize)
	if _, err := retryDecoder.Decode(secondPacket, retried); err != nil {
		t.Fatal(err)
	}
	control := make([]int16, frameSize)
	if _, err := controlDecoder.Decode(secondPacket, control); err != nil {
		t.Fatal(err)
	}
	for i := range retried {
		if retried[i] != control[i] {
			t.Fatalf("retry output differs at sample %d: got %d, want %d", i, retried[i], control[i])
		}
	}
}

func TestLastPacketDurationTracksDecodedPacket(t *testing.T) {
	const sampleRate = 48000
	const frameSize = 960

	enc, err := NewEncoder(sampleRate, 1, ApplicationAudio)
	if err != nil {
		t.Fatal(err)
	}
	pcm := make([]float64, frameSize*2)
	for i := range pcm {
		pcm[i] = 0.1 * math.Sin(2*math.Pi*440*float64(i)/sampleRate)
	}
	pkt, err := enc.EncodeFloat(pcm, frameSize*2)
	if err != nil {
		t.Fatal(err)
	}
	dec, err := NewDecoder(sampleRate, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dec.DecodeFloat(pkt); err != nil {
		t.Fatal(err)
	}
	if got, want := dec.GetLastPacketDuration(), frameSize*2; got != want {
		t.Fatalf("last packet duration = %d, want %d", got, want)
	}
}

func TestSignalTypeVoiceAffectsBandwidthThreshold(t *testing.T) {
	enc, err := NewEncoder(48000, 1, ApplicationAudio)
	if err != nil {
		t.Fatal(err)
	}
	if err := enc.SetBitrate(48000); err != nil {
		t.Fatal(err)
	}
	if got := enc.Bandwidth(); got != BandwidthFullband {
		t.Fatalf("audio signal bandwidth = %d, want fullband", got)
	}
	enc.SetSignalType(SignalVoice)
	if got := enc.Bandwidth(); got != BandwidthSuperWideband {
		t.Fatalf("voice signal bandwidth = %d, want superwideband", got)
	}
}
