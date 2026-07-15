package oggopus

import (
	"bytes"
	"errors"
	"testing"

	opus "github.com/darui3018823/opus"
)

func TestReaderPacketTimingPreSkipAndEndTrim(t *testing.T) {
	const packetDuration = 960
	var stream bytes.Buffer
	w, err := NewWriter(&stream, 10, Head{Version: 1, Channels: 1, PreSkip: 1200}, Tags{Vendor: "timing"})
	if err != nil {
		t.Fatal(err)
	}
	packet := []byte{0xf8, 0xff, 0xfe}
	for i := 0; i < 3; i++ {
		err := w.WritePacket(packet, PacketWriteOptions{
			GranulePosition: int64((i + 1) * packetDuration),
			EOS:             i == 2,
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	r, err := NewReader(bytes.NewReader(stream.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	wantStart := []int{960, 240, 0}
	wantEnd := []int{0, 0, 0}
	for i := range 3 {
		got, err := r.NextPacket()
		if err != nil {
			t.Fatal(err)
		}
		if got.Duration48k != packetDuration || got.DiscardStart != wantStart[i] || got.DiscardEnd != wantEnd[i] {
			t.Fatalf("packet %d timing = duration %d start %d end %d", i, got.Duration48k, got.DiscardStart, got.DiscardEnd)
		}
	}
}

func TestReaderPacketTimingTrimsFinalPageBackwards(t *testing.T) {
	var stream bytes.Buffer
	w, err := NewWriter(&stream, 11, Head{Version: 1, Channels: 1}, Tags{Vendor: "trim"})
	if err != nil {
		t.Fatal(err)
	}
	packet := []byte{0xf8, 0xff, 0xfe}
	for i := 0; i < 3; i++ {
		if err := w.WritePacket(packet, PacketWriteOptions{GranulePosition: 1500, EOS: i == 2}); err != nil {
			t.Fatal(err)
		}
	}
	r, err := NewReader(bytes.NewReader(stream.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	wantEnd := []int{0, 420, 960}
	// A 1380-sample trim is intentionally larger than the final packet to
	// verify defensive handling of non-recommended but decodable streams.
	got := make([]Packet, 3)
	for i := range got {
		got[i], err = r.NextPacket()
		if err != nil {
			t.Fatal(err)
		}
	}
	for i := range got {
		if got[i].DiscardEnd != wantEnd[i] {
			t.Fatalf("packet %d discard end = %d, want %d", i, got[i].DiscardEnd, wantEnd[i])
		}
	}
}

func TestReaderPacketTimingMultistreamDuration(t *testing.T) {
	const frameSize = 960
	enc, err := opus.NewMultistreamEncoder(48000, 2, 2, 0, []byte{0, 1}, opus.ApplicationAudio)
	if err != nil {
		t.Fatal(err)
	}
	packet, err := enc.Encode(make([]int16, frameSize*2), frameSize)
	if err != nil {
		t.Fatal(err)
	}
	head := Head{
		Version:        1,
		Channels:       2,
		MappingFamily:  255,
		StreamCount:    2,
		ChannelMapping: []byte{0, 1},
	}
	var stream bytes.Buffer
	w, err := NewWriter(&stream, 12, head, Tags{Vendor: "multistream"})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.WritePacket(packet, PacketWriteOptions{GranulePosition: frameSize, EOS: true}); err != nil {
		t.Fatal(err)
	}
	r, err := NewReader(bytes.NewReader(stream.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	got, err := r.NextPacket()
	if err != nil {
		t.Fatal(err)
	}
	if got.Duration48k != frameSize {
		t.Fatalf("duration = %d, want %d", got.Duration48k, frameSize)
	}
}

func TestReaderPacketTimingRejectsInvalidGranules(t *testing.T) {
	makeStream := func(preSkip uint16, granule int64, eos bool) []byte {
		var stream bytes.Buffer
		w, err := NewWriter(&stream, 13, Head{Version: 1, Channels: 1, PreSkip: preSkip}, Tags{Vendor: "invalid"})
		if err != nil {
			t.Fatal(err)
		}
		if err := w.WritePacket([]byte{0xf8, 0xff, 0xfe}, PacketWriteOptions{GranulePosition: granule, Flush: !eos, EOS: eos}); err != nil {
			t.Fatal(err)
		}
		return stream.Bytes()
	}
	for _, tc := range []struct {
		name string
		data []byte
	}{
		{"initial page before duration", makeStream(0, 100, false)},
		{"EOS before pre-skip", makeStream(500, 400, true)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, err := NewReader(bytes.NewReader(tc.data))
			if err != nil {
				t.Fatal(err)
			}
			for attempt := 0; attempt < 2; attempt++ {
				if _, err := r.NextPacket(); !errors.Is(err, ErrInvalidOpusStream) {
					t.Fatalf("attempt %d error = %v, want ErrInvalidOpusStream", attempt, err)
				}
			}
		})
	}
}
