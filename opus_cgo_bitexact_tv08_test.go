//go:build opusref

package opus

import (
	"testing"

	"github.com/darui3018823/opus/internal/cgoref"
)

func TestTV08Packet4RangeMatchesLibopus(t *testing.T) {
	packets := readOpusDemoPackets(t, "testvector08.bit")
	if len(packets) < 5 {
		t.Fatalf("vector has %d packets, need 5", len(packets))
	}

	goDec, err := NewDecoder(SampleRate48kHz, ChannelsStereo)
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	refDec, err := cgoref.NewDecoder(SampleRate48kHz, ChannelsStereo)
	if err != nil {
		t.Fatalf("cgoref.NewDecoder: %v", err)
	}
	defer refDec.Close()

	pcm := make([]int16, MaxFrameSize*ChannelsStereo)
	for i := 0; i <= 4; i++ {
		packet := packets[i]
		if _, err := goDec.Decode(packet.packet, pcm); err != nil {
			t.Fatalf("packet %d Go decode: %v", i, err)
		}
		if _, err := refDec.Decode(packet.packet, MaxFrameSize); err != nil {
			t.Fatalf("packet %d libopus decode: %v", i, err)
		}
		refRange, err := refDec.FinalRange()
		if err != nil {
			t.Fatalf("packet %d libopus final range: %v", i, err)
		}
		t.Logf("sequence packet=%d bytes=%d go=%08x libopus=%08x bitstream=%08x",
			i, len(packet.packet), goDec.FinalRange(), refRange, packet.finalRange)
		if refRange != packet.finalRange {
			t.Fatalf("packet %d libopus=%08x, bitstream=%08x", i, refRange, packet.finalRange)
		}
		if i < 4 && goDec.FinalRange() != refRange {
			t.Fatalf("packet %d precondition: Go=%08x, libopus=%08x", i, goDec.FinalRange(), refRange)
		}
	}

	isolatedGo, err := NewDecoder(SampleRate48kHz, ChannelsStereo)
	if err != nil {
		t.Fatalf("isolated NewDecoder: %v", err)
	}
	isolatedRef, err := cgoref.NewDecoder(SampleRate48kHz, ChannelsStereo)
	if err != nil {
		t.Fatalf("isolated cgoref.NewDecoder: %v", err)
	}
	defer isolatedRef.Close()
	if _, err := isolatedGo.Decode(packets[4].packet, pcm); err != nil {
		t.Fatalf("isolated Go decode: %v", err)
	}
	if _, err := isolatedRef.Decode(packets[4].packet, MaxFrameSize); err != nil {
		t.Fatalf("isolated libopus decode: %v", err)
	}
	isolatedRefRange, err := isolatedRef.FinalRange()
	if err != nil {
		t.Fatalf("isolated libopus final range: %v", err)
	}
	t.Logf("isolated packet=4 go=%08x libopus=%08x", isolatedGo.FinalRange(), isolatedRefRange)

	if got := goDec.FinalRange(); got != packets[4].finalRange {
		t.Fatalf("sequence packet 4 final range=%08x, libopus=%08x", got, packets[4].finalRange)
	}
}
