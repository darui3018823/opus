//go:build opusref

package opus

import (
	"math"
	"testing"

	"github.com/darui3018823/opus/internal/cgoref"
)

func TestCGOMultistreamInteroperability(t *testing.T) {
	const (
		rate      = 48000
		channels  = 6
		streams   = 4
		coupled   = 2
		frameSize = 960
	)
	mapping := []byte{0, 4, 1, 2, 3, 5}
	pcm := make([]float32, frameSize*channels)
	for i := 0; i < frameSize; i++ {
		for ch := 0; ch < channels; ch++ {
			pcm[i*channels+ch] = float32(0.25 * math.Sin(2*math.Pi*float64(180+97*ch)*float64(i)/rate))
		}
	}

	goEnc, err := NewMultistreamEncoder(rate, channels, streams, coupled, mapping, ApplicationAudio)
	if err != nil {
		t.Fatal(err)
	}
	goEnc.SetVBR(true)
	goPacket, err := goEnc.EncodeFloat32(pcm, frameSize)
	if err != nil {
		t.Fatal(err)
	}
	refDec, err := cgoref.NewMultistreamDecoder(rate, channels, streams, coupled, mapping)
	if err != nil {
		t.Fatal(err)
	}
	defer refDec.Close()
	refOut, err := refDec.DecodeFloat(goPacket, frameSize)
	if err != nil {
		t.Fatalf("libopus rejected Go multistream packet: %v", err)
	}
	if len(refOut) != len(pcm) {
		t.Fatalf("libopus decoded %d samples, want %d", len(refOut), len(pcm))
	}

	refEnc, err := cgoref.NewMultistreamEncoder(rate, channels, streams, coupled, mapping, ApplicationAudio)
	if err != nil {
		t.Fatal(err)
	}
	defer refEnc.Close()
	refPacket, err := refEnc.Encode(pcm, frameSize)
	if err != nil {
		t.Fatal(err)
	}
	goDec, err := NewMultistreamDecoder(rate, channels, streams, coupled, mapping)
	if err != nil {
		t.Fatal(err)
	}
	goOut, err := goDec.DecodeFloat32(refPacket)
	if err != nil {
		t.Fatalf("Go decoder rejected libopus multistream packet: %v", err)
	}
	if len(goOut) != len(pcm) {
		t.Fatalf("Go decoded %d samples, want %d", len(goOut), len(pcm))
	}
}

func TestCGOMultistreamDecodeFEC(t *testing.T) {
	const (
		rate      = 16000
		channels  = 2
		streams   = 2
		coupled   = 0
		frameSize = 320
		lost      = 5
	)
	mapping := []byte{0, 1}
	refEnc, err := cgoref.NewMultistreamEncoder(rate, channels, streams, coupled, mapping, ApplicationVOIP)
	if err != nil {
		t.Fatal(err)
	}
	defer refEnc.Close()
	for name, configure := range map[string]func() error{
		"bitrate":     func() error { return refEnc.SetBitrate(48000) },
		"voice":       refEnc.SetVoiceMode,
		"packet loss": func() error { return refEnc.SetPacketLossPerc(20) },
		"FEC":         func() error { return refEnc.SetInbandFEC(true) },
	} {
		if err := configure(); err != nil {
			t.Fatalf("configure %s: %v", name, err)
		}
	}

	packets := make([][]byte, lost+2)
	for packet := range packets {
		pcm := make([]float32, frameSize*channels)
		for i := 0; i < frameSize; i++ {
			n := float64(packet*frameSize + i)
			pcm[2*i] = float32(0.28*math.Sin(2*math.Pi*220*n/rate) + 0.08*math.Sin(2*math.Pi*440*n/rate))
			pcm[2*i+1] = float32(0.25*math.Sin(2*math.Pi*310*n/rate) + 0.07*math.Sin(2*math.Pi*620*n/rate))
		}
		packets[packet], err = refEnc.Encode(pcm, frameSize)
		if err != nil {
			t.Fatalf("encode packet %d: %v", packet, err)
		}
	}
	children, duration, err := splitMultistreamPackets(packets[lost+1], streams, rate)
	if err != nil {
		t.Fatal(err)
	}
	if duration != frameSize {
		t.Fatalf("packet duration = %d, want %d", duration, frameSize)
	}
	for stream, packet := range children {
		hasLBRR, err := PacketHasLBRR(packet)
		if err != nil {
			t.Fatalf("stream %d LBRR inspection: %v", stream, err)
		}
		if !hasLBRR {
			t.Fatalf("stream %d has no LBRR", stream)
		}
	}

	refDec, err := cgoref.NewMultistreamDecoder(rate, channels, streams, coupled, mapping)
	if err != nil {
		t.Fatal(err)
	}
	defer refDec.Close()
	goDec, err := NewMultistreamDecoder(rate, channels, streams, coupled, mapping)
	if err != nil {
		t.Fatal(err)
	}
	for packet := 0; packet < lost; packet++ {
		if _, err := refDec.DecodeFloat(packets[packet], frameSize); err != nil {
			t.Fatalf("libopus prime packet %d: %v", packet, err)
		}
		if _, err := goDec.Decode(packets[packet], make([]int16, frameSize*channels)); err != nil {
			t.Fatalf("Go prime packet %d: %v", packet, err)
		}
	}
	refFEC, err := refDec.DecodeFloatFEC(packets[lost+1], frameSize)
	if err != nil {
		t.Fatal(err)
	}
	goFEC := make([]int16, frameSize*channels)
	if n, err := goDec.DecodeFEC(packets[lost+1], goFEC); err != nil || n != frameSize {
		t.Fatalf("Go DecodeFEC = (%d, %v), want (%d, nil)", n, err, frameSize)
	}

	var signal, squaredError float64
	for i, ref := range refFEC {
		want := float64(ref)
		got := float64(goFEC[i]) / 32768
		signal += want * want
		delta := want - got
		squaredError += delta * delta
	}
	if signal == 0 {
		t.Fatal("libopus FEC recovery decoded to silence")
	}
	snr := 10 * math.Log10(signal/squaredError)
	t.Logf("Go/libopus multistream FEC SNR: %.2f dB", snr)
	if snr < 10 {
		t.Fatalf("Go multistream FEC diverged from libopus: %.2f dB", snr)
	}
}
