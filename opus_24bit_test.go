package opus

import (
	"errors"
	"math"
	"testing"
)

func TestEncodeDecode24(t *testing.T) {
	const (
		rate      = 48000
		channels  = 2
		frameSize = 960
	)
	enc, err := NewEncoder(rate, channels, ApplicationAudio)
	if err != nil {
		t.Fatal(err)
	}
	enc.SetVBR(true)

	pcm := make([]int32, frameSize*channels)
	for i := 0; i < frameSize; i++ {
		pcm[2*i] = int32(math.Round(0.7 * 8388607 * math.Sin(2*math.Pi*440*float64(i)/rate)))
		pcm[2*i+1] = int32(math.Round(0.5 * 8388607 * math.Sin(2*math.Pi*880*float64(i)/rate)))
	}
	packet, err := enc.Encode24(pcm, frameSize)
	if err != nil {
		t.Fatal(err)
	}

	dec, err := NewDecoder(rate, channels)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]int32, len(pcm))
	n, err := dec.Decode24(packet, out)
	if err != nil {
		t.Fatal(err)
	}
	if n != frameSize {
		t.Fatalf("decoded %d samples per channel, want %d", n, frameSize)
	}
	var energy int64
	for _, sample := range out {
		if sample < -8388608 || sample > 8388607 {
			t.Fatalf("sample %d outside signed 24-bit range", sample)
		}
		energy += int64(sample) * int64(sample)
	}
	if energy == 0 {
		t.Fatal("decoded 24-bit output is silent")
	}
}

func TestDecode24PreflightsBuffer(t *testing.T) {
	enc, err := NewEncoder(48000, 1, ApplicationAudio)
	if err != nil {
		t.Fatal(err)
	}
	packet, err := enc.Encode24(make([]int32, 960), 960)
	if err != nil {
		t.Fatal(err)
	}
	dec, err := NewDecoder(48000, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dec.Decode24(packet, make([]int32, 959)); !errors.Is(err, ErrBufferTooSmall) {
		t.Fatalf("Decode24 error = %v, want ErrBufferTooSmall", err)
	}
	out := make([]int32, 960)
	if _, err := dec.Decode24(packet, out); err != nil {
		t.Fatalf("retry after short buffer failed: %v", err)
	}
}
